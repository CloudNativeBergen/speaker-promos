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

// The exported manifest is pre-filled with what the card actually used, so it
// can be edited and merged back rather than being a blank template.
func TestManifestIsPrefilledAndReloadable(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, res.Dir, "promo.yaml")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"apiVersion: " + manifest.APIVersion,
		"kind: SpeakerOverride", "name: dario-haaland", "employer: Bysten Labs"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("promo.yaml missing %q:\n%s", want, body)
		}
	}

	// It must be a manifest the tool accepts back, not merely manifest-shaped.
	reloaded, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("exported manifest does not load: %v", err)
	}
	spec, ok := reloaded.Speaker("dario-haaland")
	if !ok || spec.Employer != "Bysten Labs" {
		t.Errorf("reloaded spec = %+v, ok=%v", spec, ok)
	}
}

// A talk override is written only when there is one; an empty object would be
// noise in every folder.
func TestManifestOmitsAnEmptyTalkOverride(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, res.Dir, "promo.yaml"))
	if strings.Contains(string(body), "kind: TalkOverride") {
		t.Errorf("unexpected TalkOverride:\n%s", body)
	}

	// With a display title set, it appears.
	if err := e.Resolver.Set.SetTalk("talk-1", manifest.TalkSpec{DisplayTitle: "Kortere"}); err != nil {
		t.Fatal(err)
	}
	res, err = e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(filepath.Join(root, res.Dir, "promo.yaml"))
	if !strings.Contains(string(body), "kind: TalkOverride") ||
		!strings.Contains(string(body), "displayTitle: Kortere") {
		t.Errorf("promo.yaml missing the talk override:\n%s", body)
	}
}

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
	body, _ := os.ReadFile(filepath.Join(root, res.Dir, "promo.yaml"))

	var found bool
	for _, doc := range strings.Split(string(body), "\n---\n") {
		if strings.Contains(doc, "kind: SpeakerOverride") && strings.Contains(doc, "name: "+slugless.Key()) {
			found = true
			if !strings.Contains(doc, "employer: Skyvakt") {
				t.Errorf("the slugless speaker's override is not pre-filled:\n%s", doc)
			}
		}
	}
	if !found {
		t.Errorf("no override for the slugless speaker (key %q):\n%s", slugless.Key(), body)
	}
	if !strings.Contains(string(body), "name: dario-haaland") {
		t.Errorf("lost the speaker that does have a slug:\n%s", body)
	}
	// The copy still names both.
	copyText, _ := os.ReadFile(filepath.Join(root, res.Dir, "linkedin.txt"))
	if !strings.Contains(string(copyText), "Solveig Ulriksen") {
		t.Errorf("linkedin.txt lost a speaker: %q", copyText)
	}
}

// The record is the "all information" half of promo.yaml: everything the tool
// knew about the talk when it produced the folder.
func TestRecordCarriesTheWholeTalk(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, res.Dir, "promo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	for _, want := range []string{
		"kind: " + manifest.KindTalkInfo,
		"title: Cloud Native Days Norway 2026",
		"programUrl: https://2026.cloudnativedays.no/program",
		"id: talk-1",
		"day: 1",
		"startTime: \"09:00\"",
		"track: 'Track 1: Full Day Workshops'",
		"format: workshop_120",
		"formatLabel: 2 h workshop",
		"level: intermediate",
		"abstract: Plattformer bygges",
		"name: Dario Haaland",
		// The free text the employer was guessed from, so a wrong guess can be
		// judged without opening the website.
		"submittedTitle: Bysten Labs",
		"key: dario-haaland",
		"employerGuessed: true",
		// False here, which is exactly the case an image override fixes.
		"hasPhoto: false",
		"profileUrl: https://2026.cloudnativedays.no/speaker/dario-haaland",
		"cards:",
		"- portrait.svg",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("record missing %q:\n%s", want, text)
		}
	}

	// An export must be reproducible, so nothing in the file may vary per run.
	second, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(root, second.Dir, "promo.yaml"))
	if string(again) != text {
		t.Error("promo.yaml is not byte-stable across runs")
	}
}

// The record shows both titles when a display override is in play, so the
// folder says what was submitted as well as what the card shows.
func TestRecordKeepsTheSubmittedTitle(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	if err := e.Resolver.Set.SetTalk("talk-1", manifest.TalkSpec{DisplayTitle: "Kortere tittel"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, res.Dir, "promo.yaml"))

	if !strings.Contains(string(body), "title: Kortere tittel") {
		t.Errorf("record should show the displayed title:\n%s", body)
	}
	if !strings.Contains(string(body), "submittedTitle: Kan skyen kjøre") {
		t.Errorf("record should keep the submitted title:\n%s", body)
	}
}

// The whole file must still load as a manifest, TalkInfo included, so an
// exported bundle can be fed straight back.
func TestExportedManifestLoadsWithTheRecordPresent(t *testing.T) {
	e, root := newExporter(t, []string{FormatSVG}, false)
	res, err := e.write(root, testTalk())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, res.Dir, "promo.yaml")

	reloaded, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("exported bundle does not load: %v", err)
	}
	// TalkInfo is informational and must not become an override.
	if _, ok := reloaded.Talk("talk-1"); ok {
		t.Error("TalkInfo was mistaken for a TalkOverride")
	}
	spec, ok := reloaded.Speaker("dario-haaland")
	if !ok || spec.Employer != "Bysten Labs" {
		t.Errorf("speaker override = %+v, ok=%v", spec, ok)
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
		t.Errorf("record should report a photo:\n%s", body)
	}
	if !strings.Contains(string(body), "image: dario.png") {
		t.Errorf("record should name the override's image:\n%s", body)
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
