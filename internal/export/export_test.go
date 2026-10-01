package export

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/raster"
	"github.com/vehagn/speaker-promos/internal/render"
	"github.com/vehagn/speaker-promos/internal/theme"
)

const testLogo = `<svg width="2710" height="747" viewBox="0 0 2710 747" xmlns="http://www.w3.org/2000/svg"><rect width="2710" height="747" fill="#fff"/></svg>`

func testConference() cnd.Conference {
	return cnd.Conference{
		Title: "Cloud Native Days Norway 2026", StartDate: "2026-10-26", EndDate: "2026-10-27",
		City: "Bergen", Country: "Norway", Domain: "2026.cloudnativedays.no", LogoBright: testLogo,
	}
}

func testTalk() cnd.Talk {
	return cnd.Talk{
		ID: "talk-1", Title: "Kan skyen kjøre på en brødrister?",
		Format: "workshop_120", Level: "intermediate",
		Abstract: "Plattformer bygges best når teamet forstår hele stacken.",
		Speakers: []cnd.Speaker{{
			ID: "sp-1", Name: "Dario Haaland", Slug: "dario-haaland", Title: "Bysten Labs",
		}},
		Schedule: cnd.Slot{
			Date: "2026-10-26", Day: 1, Track: "Track 1: Full Day Workshops",
			StartTime: "09:00", EndTime: "11:00",
		},
	}
}

// write resolves a talk against the exporter's manifest as it stands, the way
// both callers do, and writes its bundle.
func (e *Exporter) write(root string, t cnd.Talk) (Result, error) {
	return e.Write(root, e.Resolver.Talk(t))
}

// Photos are off (Images nil) so nothing touches the network.
func newExporter(t *testing.T, formats []string, withRaster bool) (*Exporter, string) {
	t.Helper()
	dir := t.TempDir()
	th, err := theme.Default()
	if err != nil {
		t.Fatal(err)
	}
	r, err := render.New(th, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := &Exporter{
		Renderer: r,
		Resolver: &promo.Resolver{
			Set: manifest.New(filepath.Join(dir, "promos.yaml")),
			Program: &cnd.Program{
				Conference: testConference(),
				Talks:      []cnd.Talk{testTalk()},
			},
		},
		Formats: formats, Sizes: []string{"portrait"},
	}
	if withRaster {
		conv, _, ok := raster.Find()
		e.Converter, e.HasConverter = conv, ok
	}
	return e, filepath.Join(dir, "out")
}

func TestParseFormats(t *testing.T) {
	for in, want := range map[string]string{
		"":            "svg,png,jpg",
		"all":         "svg,png,jpg",
		"svg":         "svg",
		"png,jpg":     "png,jpg",
		"jpeg":        "jpg",     // alias
		"jpg,svg":     "svg,jpg", // canonical order, not flag order
		"svg,svg,png": "svg,png", // deduplicated
		" SVG , PNG ": "svg,png", // trimmed and lowercased
	} {
		got, err := ParseFormats(in)
		if err != nil {
			t.Errorf("ParseFormats(%q): %v", in, err)
			continue
		}
		if strings.Join(got, ",") != want {
			t.Errorf("ParseFormats(%q) = %v, want %s", in, got, want)
		}
	}
	for _, bad := range []string{"pdf", "svg,webp", ","} {
		if _, err := ParseFormats(bad); err == nil {
			t.Errorf("ParseFormats(%q) should have failed", bad)
		}
	}
}

// The bundle is the unit of work: one folder per talk holding the card, the
// copy and the manifest that produced them.
func TestWriteBundleLayout(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	sess := testTalk()

	res, err := e.write(root, sess)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dir != sess.FileStem() {
		t.Errorf("Dir = %q, want the CLI's file stem %q", res.Dir, sess.FileStem())
	}

	dir := filepath.Join(root, res.Dir)
	for _, name := range []string{"portrait.svg", "linkedin.txt", "bluesky.txt", "promo.yaml"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
		if !slices.Contains(res.Files, name) {
			t.Errorf("Files does not list %s: %v", name, res.Files)
		}
	}
}

// A missing rasteriser must cost only the raster formats, never the copy or the
// manifest — those are the parts you cannot regenerate from the SVG.
func TestWriteWithoutRasteriserStillWritesEverythingElse(t *testing.T) {
	e, root := newExporter(t, AllFormats, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, res.Dir)

	for _, name := range []string{"portrait.svg", "linkedin.txt", "bluesky.txt", "promo.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	for _, name := range []string{"portrait.png", "portrait.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s written with no rasteriser available", name)
		}
	}
}

// A raster-only bundle still needs an SVG on disk to convert from; it must not
// be left behind afterwards.
func TestRasterOnlyBundleDoesNotKeepTheSVG(t *testing.T) {
	conv, _, ok := raster.Find()
	if !ok {
		t.Skip("no SVG rasteriser on PATH")
	}
	e, root := newExporter(t, []string{FormatPNG}, true)
	e.Converter, e.HasConverter = conv, true
	e.RasterWidth = 200 // keep the test fast

	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, res.Dir)
	if _, err := os.Stat(filepath.Join(dir, "portrait.png")); err != nil {
		t.Fatalf("portrait.png: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "portrait.svg")); err == nil {
		t.Error("the intermediate SVG was left behind")
	}
	if slices.Contains(res.Files, "portrait.svg") {
		t.Errorf("Files lists an SVG that was not kept: %v", res.Files)
	}
}

func TestRasterFormatsWhenAvailable(t *testing.T) {
	if _, _, ok := raster.Find(); !ok {
		t.Skip("no SVG rasteriser on PATH")
	}
	e, root := newExporter(t, AllFormats, true)
	e.RasterWidth = 200

	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, res.Dir)
	for _, name := range []string{"portrait.svg", "portrait.png", "portrait.jpg"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

// The copy files are pasted verbatim into a post box, so they must hold the
// body alone. The review notes live in their own file precisely so they cannot
// be published by accident.
func TestCopyFilesHoldOnlyThePostBody(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, res.Dir)

	for _, name := range []string{"linkedin.txt", "bluesky.txt"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "Dario Haaland") || !strings.Contains(text, "brødrister") {
			t.Errorf("%s does not look like the post: %q", name, text)
		}
		for _, forbidden := range []string{"Check before posting", "check it", "Profiles to mention"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains review text %q", name, forbidden)
			}
		}
		if !strings.HasSuffix(text, "\n") {
			t.Errorf("%s should end with a newline", name)
		}
	}

	notes, err := os.ReadFile(filepath.Join(dir, "NOTES.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// The guessed employer is the thing most worth checking, so it must be here.
	if !strings.Contains(string(notes), "Bysten Labs") {
		t.Errorf("NOTES.txt = %q", notes)
	}
}

// docsOf splits a bundle into its documents, keyed "Kind/name".
func docsOf(t *testing.T, body string) (kinds []string, docs map[string]string) {
	t.Helper()
	docs = map[string]string{}
	for _, doc := range strings.Split(body, "\n---\n") {
		var kind, name string
		for _, line := range strings.Split(doc, "\n") {
			if v, ok := strings.CutPrefix(line, "kind: "); ok {
				kind = v
			}
			if v, ok := strings.CutPrefix(line, "  name: "); ok && name == "" {
				name = v
			}
		}
		kinds = append(kinds, kind)
		docs[kind+"/"+name] = doc
	}
	return kinds, docs
}

func readBundle(t *testing.T, root string, res Result) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, res.Dir, "promo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The three parts are kept apart: what the website said, what you corrected,
// and what came out. Above all, the overrides carry only real corrections —
// the guessed employer is in Output, marked as a guess, and never in an
// override where importing it would confirm it.
func TestBundleSeparatesSourceOverridesAndOutput(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	body := readBundle(t, root, res)

	kinds, docs := docsOf(t, body)
	if got := strings.Join(kinds, ","); got != "Source,TalkOverride,SpeakerOverride,Output" {
		t.Errorf("documents = %s", got)
	}
	for _, want := range []string{"apiVersion: " + manifest.APIVersion, "title: Kan skyen kjøre", "key: dario-haaland",
		"title: Bysten Labs", "startTime: \"09:00\"", "abstract: Plattformer bygges"} {
		if !strings.Contains(docs["Source/talk-1"], want) {
			t.Errorf("Source missing %q:\n%s", want, docs["Source/talk-1"])
		}
	}
	if sp := docs["SpeakerOverride/dario-haaland"]; !strings.Contains(sp, "spec: {}") || !strings.Contains(sp, "revision: ") {
		t.Errorf("an uncorrected speaker's override should be empty and carry a revision:\n%s", sp)
	}
	if strings.Contains(docs["SpeakerOverride/dario-haaland"], "Bysten") {
		t.Error("the guessed employer was pre-filled into the override")
	}
	for _, want := range []string{"employer: Bysten Labs", "employerGuessed: true", "hasPhoto: false",
		"profileUrl: https://2026.cloudnativedays.no/speaker/dario-haaland", "formatLabel: 2 h workshop",
		"programUrl: https://2026.cloudnativedays.no/program", "- portrait.svg"} {
		if !strings.Contains(docs["Output/talk-1"], want) {
			t.Errorf("Output missing %q:\n%s", want, docs["Output/talk-1"])
		}
	}

	// It loads as a manifest, and imports back as nothing.
	reloaded, err := manifest.Load(filepath.Join(root, res.Dir, "promo.yaml"))
	if err != nil {
		t.Fatalf("exported bundle does not load: %v", err)
	}
	if reloaded.BundleTalk() != "talk-1" {
		t.Errorf("BundleTalk = %q", reloaded.BundleTalk())
	}
	changes, err := e.Resolver.Set.ImportFrom(reloaded, manifest.ImportOptions{DryRun: true})
	if err != nil || len(changes) != 0 {
		t.Errorf("an unedited bundle imports %v, %v", changes, err)
	}
}

// An export must be reproducible: no generation time, so an unchanged
// re-export is byte-identical and a bundle in git does not churn.
func TestBundleIsByteStable(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	first, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	body := readBundle(t, root, first)
	second, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	if readBundle(t, root, second) != body {
		t.Error("promo.yaml is not byte-stable across runs")
	}
}

// Editing a bundle's override and importing it is the round trip export
// exists for.
func TestAnEditedBundleImportsTheEdit(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, res.Dir, "promo.yaml")
	body := strings.Replace(readBundle(t, root, res), "spec: {}\n---\napiVersion: "+manifest.APIVersion+"\nkind: Output",
		"spec:\n  employer: Bysten Labs AS\n---\napiVersion: "+manifest.APIVersion+"\nkind: Output", 1)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := e.Resolver.Set.ImportFrom(src, manifest.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].String() != `SpeakerOverride/dario-haaland employer: "Bysten Labs AS"` {
		t.Errorf("changes = %v", changes)
	}
}

// The source records what was submitted, the override what you changed, and
// the output what the card shows.
func TestBundleKeepsTheSubmittedTitle(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	if err := e.Resolver.Set.SetTalk("talk-1", manifest.TalkSpec{DisplayTitle: "Kortere tittel", Posted: "2026-10-16"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	_, docs := docsOf(t, readBundle(t, root, res))
	if !strings.Contains(docs["Source/talk-1"], "title: Kan skyen kjøre") {
		t.Errorf("Source should keep the submitted title:\n%s", docs["Source/talk-1"])
	}
	if o := docs["TalkOverride/talk-1"]; !strings.Contains(o, "displayTitle: Kortere tittel") ||
		!strings.Contains(o, "editedAt: ") || !strings.Contains(o, "posted: \"2026-10-16\"") {
		t.Errorf("TalkOverride:\n%s", o)
	}
	if o := docs["Output/talk-1"]; !strings.Contains(o, "title: Kortere tittel") || !strings.Contains(o, "posted: \"2026-10-16\"") {
		t.Errorf("Output should show the displayed title and the posted date:\n%s", o)
	}
}

func TestWriteAllReportsProgress(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	talks := []promo.Talk{e.Resolver.Talk(testTalk())}
	var calls int
	res, err := e.WriteAll(root, talks, func(done, total int, label string) {
		calls++
		if done != 1 || total != 1 || label != res0(e, talks) {
			t.Errorf("progress(%d, %d, %q)", done, total, label)
		}
	})
	if err != nil || len(res) != 1 || calls != 1 {
		t.Errorf("WriteAll = %v, %v; %d progress calls", res, err, calls)
	}
}

func res0(e *Exporter, talks []promo.Talk) string { return talks[0].FileStem() }

func TestWarningsAreReported(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	sess := testTalk()
	sess.Title = "Kan 🇳🇴 skyen kjøre på en brødrister?"

	res, err := e.write(root, sess)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "emoji") {
		t.Errorf("Warnings = %v, want the emoji note", res.Warnings)
	}
	if !strings.Contains(joined, "portrait") {
		t.Errorf("Warnings should name the size: %v", res.Warnings)
	}
}

// A speaker with no slug is still addressable by key, so the bundle carries an
// override for them like anyone else — there used to be no line to correct
// their name or employer on, and the preview server's form for them 404'd.
func TestSpeakerWithoutSlugGetsAnOverride(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	sess := testTalk()
	slugless := cnd.Speaker{ID: "sp-2", Name: "Solveig Ulriksen", Title: "Skyvakt"}
	sess.Speakers = append(sess.Speakers, slugless)

	res, err := e.write(root, sess)
	if err != nil {
		t.Fatal(err)
	}
	_, docs := docsOf(t, readBundle(t, root, res))
	if _, ok := docs["SpeakerOverride/"+slugless.Key()]; !ok {
		t.Errorf("no override for the slugless speaker (key %q)", slugless.Key())
	}
	copyText, _ := os.ReadFile(filepath.Join(root, res.Dir, "linkedin.txt"))
	if !strings.Contains(string(copyText), "Solveig Ulriksen") {
		t.Errorf("linkedin.txt lost a speaker: %q", copyText)
	}
}

// A speaker with no photo is the case the image override exists for: it has to
// reach the card, and the record has to say the card now has one.
func TestImageOverrideReachesTheCardAndTheRecord(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)

	// A real 2x2 PNG, so the renderer actually embeds it.
	photo := filepath.Join(filepath.Dir(e.Resolver.Set.Path()), "dario.png")
	if err := os.WriteFile(photo, tinyPNG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	// Named relatively, which resolves against the manifest's directory rather
	// than the process working directory.
	if err := e.Resolver.Set.SetSpeaker("dario-haaland", manifest.SpeakerSpec{
		Employer: "Bysten Labs", Image: "dario.png",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	card, err := os.ReadFile(filepath.Join(root, res.Dir, "portrait.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(card), "<image") {
		t.Error("the overridden photo was not embedded in the card")
	}
	if strings.Contains(string(card), ">DH<") {
		t.Error("the card still shows a monogram")
	}

	body, _ := os.ReadFile(filepath.Join(root, res.Dir, "promo.yaml"))
	if !strings.Contains(string(body), "hasPhoto: true") {
		t.Errorf("Output should report a photo:\n%s", body)
	}
	if !strings.Contains(string(body), "image: dario.png") {
		t.Errorf("the override should name its image:\n%s", body)
	}
}

// tinyPNG is a 2x2 opaque PNG.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
