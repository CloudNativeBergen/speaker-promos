package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValidAndComplete(t *testing.T) {
	th, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "cnd-2026" {
		t.Errorf("Name = %q", th.Name)
	}
	for _, size := range []string{"portrait", "landscape"} {
		g, err := th.Size(size)
		if err != nil {
			t.Fatal(err)
		}
		// Every element the renderer looks up must be present, or a card would
		// silently lose a line of text.
		for _, element := range []string{"meta", "name", "role", "eyebrow", "talk", "footer"} {
			st, ok := g.Text[element]
			if !ok {
				t.Errorf("%s: missing text style %q", size, element)
				continue
			}
			if st.MaxSize <= 0 || st.MaxLines <= 0 || st.LineHeight <= 0 {
				t.Errorf("%s/%s: incomplete style %+v", size, element, st)
			}
		}
	}
	if got := (&Theme{Geometry: th.Geometry}).Sizes(); len(got) != 2 || got[0] != "landscape" {
		t.Errorf("Sizes = %v, want sorted", got)
	}
}

func TestDefaultFontsAreLoadable(t *testing.T) {
	th, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for key, face := range th.Fonts {
		b, err := face.Data()
		if err != nil {
			t.Errorf("face %q: %v", key, err)
			continue
		}
		if len(b) < 10_000 {
			t.Errorf("face %q is only %d bytes", key, len(b))
		}
	}
}

func TestSizeUnknownNameLists(t *testing.T) {
	th, _ := Default()
	_, err := th.Size("square")
	if err == nil {
		t.Fatal("want error for unknown size")
	}
	// The error should tell the user what is available.
	if !strings.Contains(err.Error(), "portrait") {
		t.Errorf("error = %v, want it to list valid sizes", err)
	}
}

// A theme file overriding one colour must not blank out everything else. YAML
// replaces whole maps rather than merging them, so nested maps are merged by
// hand and that is what this pins.
func TestLoadMergesOntoDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	err := os.WriteFile(path, []byte(`
name: mine
palette:
  gradientFrom: "#000000"
geometry:
  portrait:
    pad: 200
    text:
      talk:
        maxSize: 70
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	th, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := Default()

	if th.Name != "mine" {
		t.Errorf("Name = %q", th.Name)
	}
	if th.Palette.GradientFrom != "#000000" {
		t.Errorf("GradientFrom = %q", th.Palette.GradientFrom)
	}
	// Untouched palette entries survive.
	if th.Palette.GradientTo != base.Palette.GradientTo {
		t.Errorf("GradientTo = %q, want inherited %q", th.Palette.GradientTo, base.Palette.GradientTo)
	}
	// Fonts were not mentioned at all and must still be there.
	if len(th.Fonts) != len(base.Fonts) {
		t.Errorf("Fonts = %v, want inherited", th.Fonts)
	}
	// The landscape size was not mentioned and must survive whole.
	if _, err := th.Size("landscape"); err != nil {
		t.Errorf("landscape lost: %v", err)
	}

	g, _ := th.Size("portrait")
	bg, _ := base.Size("portrait")
	if g.Pad != 200 {
		t.Errorf("Pad = %d", g.Pad)
	}
	if g.Width != bg.Width || g.PhotoSize != bg.PhotoSize {
		t.Errorf("portrait lost inherited fields: %+v", g)
	}
	if g.Text["talk"].MaxSize != 70 {
		t.Errorf("talk maxSize = %v", g.Text["talk"].MaxSize)
	}
	// Sibling properties of the overridden style survive.
	if g.Text["talk"].Face != bg.Text["talk"].Face || g.Text["talk"].MaxLines != bg.Text["talk"].MaxLines {
		t.Errorf("talk style lost inherited fields: %+v", g.Text["talk"])
	}
	// And sibling styles survive.
	if g.Text["name"].MaxSize != bg.Text["name"].MaxSize {
		t.Errorf("name style lost: %+v", g.Text["name"])
	}
}

// Merging as YAML rather than as structs is what makes a zero value express
// an intent: the struct merge this replaced could only see a non-zero field, so
// "uppercase: false" and "not mentioned" were indistinguishable.
func TestLoadCanSetAFieldBackToItsZeroValue(t *testing.T) {
	base, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	bg, _ := base.Size("portrait")
	if !bg.Text["eyebrow"].Uppercase {
		t.Skip("the built-in eyebrow is no longer uppercase; nothing to turn off")
	}

	path := filepath.Join(t.TempDir(), "t.yaml")
	body := "geometry:\n  portrait:\n    text:\n      eyebrow:\n        uppercase: false\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := th.Size("portrait")
	if g.Text["eyebrow"].Uppercase {
		t.Error("uppercase: false did not turn the eyebrow's capitals off")
	}
	// And the rest of that style is still inherited.
	if g.Text["eyebrow"].MaxSize != bg.Text["eyebrow"].MaxSize {
		t.Errorf("eyebrow lost inherited fields: %+v", g.Text["eyebrow"])
	}
}

// A theme file that says nothing is the default, not an error and not an empty
// theme.
func TestLoadEmptyOverlayIsTheDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path, []byte("# nothing to change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := Default()
	if th.Name != base.Name || len(th.Geometry) != len(base.Geometry) || len(th.Fonts) != len(base.Fonts) {
		t.Errorf("empty overlay did not inherit the default: %+v", th)
	}
}

func TestLoadRejectsBadThemes(t *testing.T) {
	for name, body := range map[string]string{
		"undefined face": "geometry:\n  portrait:\n    text:\n      talk:\n        face: nope\n",
		"inverted range": "geometry:\n  portrait:\n    text:\n      talk:\n        minSize: 300\n",
		"zero dimension": "geometry:\n  square:\n    width: 0\n    height: 0\n",
		"malformed yaml": "palette: [this is not a map\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.yaml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Errorf("want error for %s", name)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("want error for missing theme file")
	}
	// An empty path means "use the default", not "read ''".
	if _, err := Load(""); err != nil {
		t.Fatalf("Load(\"\") = %v, want the default theme", err)
	}
}

func TestPaintResolvesPaletteKeys(t *testing.T) {
	th, _ := Default()
	for in, want := range map[string]string{
		"":                th.Palette.Text,
		"text":            th.Palette.Text,
		"textMuted":       th.Palette.TextMuted,
		"accent":          th.Palette.Accent,
		"#ABCDEF":         "#ABCDEF",
		"rgba(0,0,0,0.5)": "rgba(0,0,0,0.5)",
	} {
		if got := th.Paint(in); got != want {
			t.Errorf("Paint(%q) = %q, want %q", in, got, want)
		}
	}
}

// Regression: a struct copy shares its maps, and unmarshalling into a non-nil
// map mutates it. Loading a partial theme once corrupted the built-in default
// it was merging against, so a second Load produced a geometry with zero
// dimensions.
func TestLoadDoesNotMutateTheBuiltInTheme(t *testing.T) {
	before, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	wantWidth := before.Geometry["portrait"].Width
	wantTalkLines := before.Geometry["portrait"].Text["talk"].MaxLines

	path := filepath.Join(t.TempDir(), "t.yaml")
	body := "geometry:\n  portrait:\n    pad: 7\n    text:\n      talk:\n        maxSize: 51\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}

	after, err := Default()
	if err != nil {
		t.Fatalf("Default() broke after Load: %v", err)
	}
	if got := after.Geometry["portrait"].Width; got != wantWidth {
		t.Errorf("built-in portrait width = %d after Load, want %d", got, wantWidth)
	}
	if got := after.Geometry["portrait"].Pad; got == 7 {
		t.Error("built-in portrait pad picked up the overlay's value")
	}
	if got := after.Geometry["portrait"].Text["talk"].MaxLines; got != wantTalkLines {
		t.Errorf("built-in talk maxLines = %d, want %d", got, wantTalkLines)
	}
	// Also pin that the in-memory value handed out earlier was not rewritten.
	if got := before.Geometry["portrait"].Text["talk"].MaxSize; got == 51 {
		t.Error("an earlier Default() result was mutated by Load")
	}
}

func TestAdjusted(t *testing.T) {
	base, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	// Nothing set is the theme itself — which is what keeps every unadjusted
	// card identical.
	if base.Adjusted(Adjust{}) != base {
		t.Error("an empty Adjust copied the theme")
	}

	a := base.Adjusted(Adjust{TitleScale: 1.5, PhotoScale: 0.5, Spacing: 2, GradientFrom: "#000000"})
	p, ap := base.Geometry["portrait"], a.Geometry["portrait"]
	if ap.Text["talk"].MaxSize != p.Text["talk"].MaxSize*1.5 || ap.Text["talk"].MinSize != p.Text["talk"].MinSize*1.5 {
		t.Errorf("talk = %+v", ap.Text["talk"])
	}
	if ap.Text["name"] != p.Text["name"] {
		t.Error("an unset scale changed the names")
	}
	if ap.PhotoSize != p.PhotoSize/2 || ap.Gap != p.Gap*2 {
		t.Errorf("photo %d, gap %d", ap.PhotoSize, ap.Gap)
	}
	if a.Palette.GradientFrom != "#000000" || a.Palette.GradientTo != base.Palette.GradientTo {
		t.Errorf("palette = %+v", a.Palette)
	}
	// The original is untouched.
	if base.Geometry["portrait"].Text["talk"] != p.Text["talk"] {
		t.Error("Adjusted modified the theme it was given")
	}
}

func TestAdjustOverAndValidate(t *testing.T) {
	talk := Adjust{TitleScale: 1.2}
	global := Adjust{TitleScale: 0.8, NameScale: 0.9}
	if got := talk.Over(global); got != (Adjust{TitleScale: 1.2, NameScale: 0.9}) {
		t.Errorf("Over = %+v", got)
	}
	if err := (Adjust{PhotoScale: 3}).Validate(); err == nil {
		t.Error("a photo scale of 3 should be out of range")
	}
}
