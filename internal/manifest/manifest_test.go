package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vehagn/speaker-promos/internal/cnd"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "promos.yaml")
}

// The manifest in the README, verbatim, so the documented format is the one
// that actually parses.
const sample = `apiVersion: promo.cloudnativedays.no/v1alpha1
kind: SpeakerOverride
metadata:
  name: dario-haaland
spec:
  employer: Bysten Labs
  job: Infrastructure Engineer
  links:
    linkedin: https://www.linkedin.com/in/dario
    bluesky: dario.bsky.social
---
apiVersion: promo.cloudnativedays.no/v1alpha1
kind: TalkOverride
metadata:
  name: 09b41694-27be-495d-abe3-1899bd725ad8
spec:
  displayTitle: Kort tittel
  hidden: false
`

func TestLoadSample(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	sp, ok := set.Speaker("dario-haaland")
	if !ok {
		t.Fatal("speaker override not loaded")
	}
	if sp.Employer != "Bysten Labs" || sp.Job != "Infrastructure Engineer" {
		t.Errorf("speaker spec = %+v", sp)
	}
	if sp.Links.Bluesky != "dario.bsky.social" {
		t.Errorf("bluesky = %q", sp.Links.Bluesky)
	}

	tk, ok := set.Talk("09b41694-27be-495d-abe3-1899bd725ad8")
	if !ok {
		t.Fatal("talk override not loaded")
	}
	if tk.DisplayTitle != "Kort tittel" || tk.Hidden {
		t.Errorf("talk spec = %+v", tk)
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	set, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load of a missing file: %v", err)
	}
	if s, tk := set.Len(); s != 0 || tk != 0 {
		t.Errorf("Len = %d, %d; want 0, 0", s, tk)
	}
}

// A save must be re-loadable and byte-stable, otherwise the file churns in git
// every time the server touches it.
func TestRoundTripIsStableAndSorted(t *testing.T) {
	path := tempPath(t)
	set := New(path)
	for _, slug := range []string{"zoe", "adam", "mia"} {
		if err := set.SetSpeaker(slug, SpeakerSpec{Employer: "Corp " + slug}); err != nil {
			t.Fatal(err)
		}
	}
	if err := set.SetTalk("t-2", TalkSpec{Hidden: true}); err != nil {
		t.Fatal(err)
	}
	if err := set.SetTalk("t-1", TalkSpec{DisplayTitle: "Short"}); err != nil {
		t.Fatal(err)
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s, tk := reloaded.Len(); s != 3 || tk != 2 {
		t.Fatalf("reloaded Len = %d, %d; want 3, 2", s, tk)
	}
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("load → save is not byte-stable:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	// Sorted by kind, then name.
	var order []string
	for _, line := range strings.Split(string(first), "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "name: "); ok {
			order = append(order, name)
		}
	}
	want := []string{"adam", "mia", "zoe", "t-1", "t-2"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("object order = %v; want %v", order, want)
	}
	if !strings.HasPrefix(string(first), "#") {
		t.Error("saved manifest should start with its explanatory header")
	}
}

// Clearing every field should remove the object rather than leave an empty one
// behind, since the preview server writes on every keystroke-ish edit.
func TestEmptySpecIsDeleted(t *testing.T) {
	path := tempPath(t)
	set := New(path)
	if err := set.SetSpeaker("adam", SpeakerSpec{Employer: "Corp"}); err != nil {
		t.Fatal(err)
	}
	if err := set.SetSpeaker("adam", SpeakerSpec{Employer: "   "}); err != nil {
		t.Fatal(err)
	}
	if _, ok := set.Speaker("adam"); ok {
		t.Error("override with only blank fields should have been removed")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "adam") {
		t.Errorf("manifest still mentions the cleared speaker:\n%s", b)
	}
}

// The whole point of validating apiVersion and kind is that a typo is loud.
func TestRejectsBadDocuments(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{
			"wrong apiVersion",
			"apiVersion: promo.cloudnativedays.no/v1\nkind: SpeakerOverride\nmetadata:\n  name: a\nspec:\n  employer: C\n",
			"unsupported apiVersion",
		},
		{
			"unknown kind",
			"apiVersion: " + APIVersion + "\nkind: SpeakerOverrides\nmetadata:\n  name: a\nspec:\n  employer: C\n",
			"unknown kind",
		},
		{
			"missing name",
			"apiVersion: " + APIVersion + "\nkind: SpeakerOverride\nmetadata: {}\nspec:\n  employer: C\n",
			"metadata.name",
		},
		{
			"typo in a spec field",
			"apiVersion: " + APIVersion + "\nkind: SpeakerOverride\nmetadata:\n  name: a\nspec:\n  employeer: C\n",
			`unknown field "employeer"`,
		},
		{
			"typo in a link field",
			"apiVersion: " + APIVersion + "\nkind: SpeakerOverride\nmetadata:\n  name: a\nspec:\n  links:\n    linkedn: x\n",
			`unknown field "linkedn"`,
		},
		{
			"typo at the top level",
			"apiVersion: " + APIVersion + "\nkind: TalkOverride\nmetadata:\n  name: a\nspecs:\n  hidden: true\n",
			`unknown field "specs"`,
		},
		{
			"talk field on a speaker",
			"apiVersion: " + APIVersion + "\nkind: SpeakerOverride\nmetadata:\n  name: a\nspec:\n  hidden: true\n",
			`unknown field "hidden"`,
		},
		{
			"duplicate object",
			"apiVersion: " + APIVersion + "\nkind: TalkOverride\nmetadata:\n  name: a\nspec:\n  hidden: true\n---\n" +
				"apiVersion: " + APIVersion + "\nkind: TalkOverride\nmetadata:\n  name: a\nspec:\n  hidden: false\n",
			"duplicate",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := tempPath(t)
			if err := os.WriteFile(path, []byte(c.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted %s", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q; want it to mention %q", err, c.want)
			}
			if !strings.Contains(err.Error(), "line ") {
				t.Errorf("error = %q; want it to name a line", err)
			}
		})
	}
}

// Blank documents occur naturally when a file is edited by hand.
func TestSkipsEmptyDocuments(t *testing.T) {
	path := tempPath(t)
	body := "---\n" + sample + "---\n# just a comment\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s, tk := set.Len(); s != 1 || tk != 1 {
		t.Errorf("Len = %d, %d; want 1, 1", s, tk)
	}
}

// The field table is what every field-by-field walk now goes through — the
// import merge, the form's values, and the two compositions below — so it has
// to cover every field a spec has.
func TestSpecFieldTableCoversEveryField(t *testing.T) {
	full := SpeakerSpec{
		Name: "n", Employer: "e", Job: "j", Title: "t", Image: "i",
		Links: cnd.Links{LinkedIn: "l", Bluesky: "b", X: "x", GitHub: "g"},
	}
	var rebuilt SpeakerSpec
	for _, field := range SpeakerFields() {
		value := full.Value(field)
		if value == "" {
			t.Errorf("field %q reads back empty from a fully populated spec", field)
		}
		rebuilt.SetValue(field, value)
	}
	// A field missing from the table would leave a gap here, which is exactly
	// the mistake the table exists to make impossible.
	if rebuilt != full {
		t.Errorf("round trip through the table lost a field:\n got %+v\nwant %+v", rebuilt, full)
	}
	if got := full.Value("nope"); got != "" {
		t.Errorf("unknown field = %q, want empty", got)
	}
	// Setting an unknown field is ignored rather than fatal: a form may carry
	// inputs that are not corrections.
	spec := full
	spec.SetValue("nope", "value")
	if spec != full {
		t.Errorf("an unknown field changed the spec: %+v", spec)
	}
}

func TestOverlayAndDiff(t *testing.T) {
	base := SpeakerSpec{
		Name: "Dario Haaland", Employer: "Bysten Labs", Job: "Engineer",
		Links: cnd.Links{Bluesky: "scraped.example"},
	}
	override := SpeakerSpec{Employer: "Bysten Labs AS", Links: cnd.Links{Bluesky: "dario.example"}}

	// Overlay is what the form shows: the correction where there is one, the
	// found value everywhere else.
	got := override.Overlay(base)
	want := SpeakerSpec{
		Name: "Dario Haaland", Employer: "Bysten Labs AS", Job: "Engineer",
		Links: cnd.Links{Bluesky: "dario.example"},
	}
	if got != want {
		t.Errorf("Overlay =\n %+v\nwant %+v", got, want)
	}

	// Diff is what gets stored: only what the form actually changed, so the
	// found values do not turn into confirmed corrections.
	if got, want := got.Diff(base), override; got != want {
		t.Errorf("Diff =\n %+v\nwant %+v", got, want)
	}
	// An empty field is "no opinion", not "clear it".
	if got := (SpeakerSpec{}).Diff(base); !got.empty() {
		t.Errorf("an empty submission recorded %+v", got)
	}
}

// TalkInfo is informational: it must load without error and must not become an
// override, so an exported bundle can be fed straight back.
func TestTalkInfoIsAcceptedAndIgnored(t *testing.T) {
	path := tempPath(t)
	body := "apiVersion: " + APIVersion + `
kind: TalkInfo
metadata:
  name: talk-1
spec:
  conference:
    title: Cloud Native Days Norway 2026
  talk:
    id: talk-1
    title: Kan skyen kjøre?
  speakers:
    - name: Dario Haaland
      hasPhoto: false
---
apiVersion: ` + APIVersion + `
kind: SpeakerOverride
metadata:
  name: dario-haaland
spec:
  employer: Bysten Labs
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := set.Talk("talk-1"); ok {
		t.Error("TalkInfo was mistaken for a TalkOverride")
	}
	if s, tk := set.Len(); s != 1 || tk != 0 {
		t.Errorf("Len = %d, %d; want 1, 0", s, tk)
	}

	// Its spec is deliberately not field-checked, so a bundle written by a
	// newer version stays loadable.
	unknown := "apiVersion: " + APIVersion + "\nkind: TalkInfo\nmetadata:\n  name: t\nspec:\n  somethingNew: 1\n"
	if err := os.WriteFile(path, []byte(unknown), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("an unknown TalkInfo field should be tolerated: %v", err)
	}
}

// An image-only override is a real override, so it must survive a save/load
// cycle rather than being pruned as empty.
func TestImageOnlyOverrideRoundTrips(t *testing.T) {
	path := tempPath(t)
	set := New(path)
	if err := set.SetSpeaker("dario-haaland", SpeakerSpec{Image: "photos/dario.jpg"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := reloaded.Speaker("dario-haaland")
	if !ok || spec.Image != "photos/dario.jpg" {
		t.Errorf("reloaded = %+v, ok=%v", spec, ok)
	}
}

// A name-only or title-only override is a real override and must survive a
// save/load cycle rather than being pruned as empty.
func TestNameAndTitleOnlyOverridesRoundTrip(t *testing.T) {
	for _, spec := range []SpeakerSpec{
		{Name: "Aurélie Vache"},
		{Title: "Tech Lead, Platform"},
	} {
		path := tempPath(t)
		set := New(path)
		if err := set.SetSpeaker("a", spec); err != nil {
			t.Fatal(err)
		}
		reloaded, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := reloaded.Speaker("a")
		if !ok || got != spec {
			t.Errorf("reloaded %+v as %+v (ok=%v)", spec, got, ok)
		}
	}
}

func TestSpeakerSpecRejectsUnknownFields(t *testing.T) {
	path := tempPath(t)
	body := "apiVersion: " + APIVersion + "\nkind: SpeakerOverride\nmetadata:\n  name: a\nspec:\n  nmae: x\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("want an error for a misspelled field")
	}
	// The error must list the fields that now exist, name and title included.
	for _, want := range []string{"name", "title", "employer", "job", "image", "links"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should list %q", err, want)
		}
	}
}

// A v1alpha1 manifest still loads, and is written back as v1alpha2.
func TestV1Alpha1LoadsAndMigrates(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatalf("a v1alpha1 manifest did not load: %v", err)
	}
	if err := set.Save(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "apiVersion: "+APIVersion) || strings.Contains(string(body), apiVersionV1Alpha1) {
		t.Errorf("not migrated:\n%s", body)
	}
}

func TestPostedIsValidated(t *testing.T) {
	path := tempPath(t)
	doc := func(posted string) string {
		return "apiVersion: " + APIVersion + "\nkind: TalkOverride\nmetadata:\n  name: t\nspec:\n  posted: " + posted + "\n"
	}
	os.WriteFile(path, []byte(doc(`"2026-10-16"`)), 0o644)
	set, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if spec, _ := set.Talk("t"); spec.Posted != "2026-10-16" {
		t.Errorf("posted = %q", spec.Posted)
	}
	os.WriteFile(path, []byte(doc("last tuesday")), 0o644)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "line 6") {
		t.Errorf("err = %v, want a line-numbered rejection", err)
	}
}

// metadata is field-checked like spec, so a typo there is an error too.
func TestMetadataRejectsUnknownFields(t *testing.T) {
	path := tempPath(t)
	body := "apiVersion: " + APIVersion + "\nkind: TalkOverride\nmetadata:\n  name: t\n  editedOn: x\nspec: {}\n"
	os.WriteFile(path, []byte(body), 0o644)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "editedOn") {
		t.Errorf("err = %v", err)
	}
}
