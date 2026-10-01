package web

import (
	"bytes"
	"encoding/xml"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"time"

	"github.com/vehagn/speaker-promos/internal/cache"
	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/render"
	"github.com/vehagn/speaker-promos/internal/theme"
)

const talkID = "09b41694-27be-495d-abe3-1899bd725ad8"

// testLogo stands in for the ~190 KB conference wordmark; all the renderer
// needs is a viewBox to scale against.
const testLogo = `<svg width="2710" height="747" viewBox="0 0 2710 747" xmlns="http://www.w3.org/2000/svg"><rect width="2710" height="747" fill="#fff"/></svg>`

func testProgram() *cnd.Program {
	return &cnd.Program{
		Conference: cnd.Conference{
			Title:      "Cloud Native Days Norway 2026",
			StartDate:  "2026-10-26",
			EndDate:    "2026-10-27",
			City:       "Bergen",
			Country:    "Norway",
			Domain:     "2026.cloudnativedays.no",
			LogoBright: testLogo,
		},
		Talks: []cnd.Talk{
			{
				ID: talkID, Title: "Nok nett", Format: "workshop_120", Level: "intermediate",
				Abstract: "Et foredrag om åpen kildekode og norsk suverenitet.",
				Speakers: []cnd.Speaker{{
					ID: "sp-1", Name: "Dario Haaland", Slug: "dario-haaland", Title: "Bysten Labs",
				}},
				Schedule: cnd.Slot{
					Date: "2026-10-26", Day: 1, Track: "Track 1: Full Day Workshops",
					StartTime: "09:00", EndTime: "11:00",
				},
			},
			{
				ID: "talk-2", Title: "Pods on Mars", Format: "presentation_25",
				Speakers: []cnd.Speaker{{ID: "sp-2", Name: "Someone Else", Slug: "someone-else"}},
				Schedule: cnd.Slot{
					Date: "2026-10-27", Day: 2, Track: "Track 2: Platform Engineering",
					StartTime: "13:20", EndTime: "13:45",
				},
			},
		},
	}
}

// Photos and link scraping are disabled: the tests must not touch the network,
// and that also exercises the monogram fallback.
func newTestServer(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "promos.yaml")

	th, err := theme.Default()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{
		Program: testProgram(),
		Set:     manifest.New(manifestPath),
		Theme:   th,
		Size:    "portrait",
		OutDir:  filepath.Join(dir, "out"),
		NoLinks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Handler(), manifestPath
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// runJob follows a job the way the browser does: the response that started it
// carries a poll, and polling continues until a response comes back without
// one. It returns that final response. A response with no job in it — the
// job never started — is returned as it is.
func runJob(t *testing.T, h http.Handler, started *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	rec := started
	for i := 0; i < 600; i++ {
		m := regexp.MustCompile(`hx-get="(/jobs/\d+)"`).FindStringSubmatch(rec.Body.String())
		if m == nil {
			return rec
		}
		time.Sleep(20 * time.Millisecond)
		rec = get(t, h, m[1])
	}
	t.Fatal("job did not finish")
	return nil
}

// do posts and follows the job it starts.
func do(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return runJob(t, h, postForm(t, h, path, form))
}

func TestIndexListsEveryTalk(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := get(t, h, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if n := strings.Count(body, `class="row `); n != 2 {
		t.Errorf("rendered %d rows, want 2", n)
	}
	for _, want := range []string{
		"Nok nett", "Pods on Mars", "Dario Haaland",
		"Cloud Native Days Norway 2026", "26–27 October 2026",
		`src="/card/` + talkID, "htmx.min.js",
		// The website's "Track N: " prefix is noise once the row is labelled.
		"Full Day Workshops",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	if strings.Contains(body, "Track 1:") {
		t.Error("track prefix should be stripped")
	}
}

func TestCardEndpointServesSVG(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := get(t, h, "/card/"+talkID+"?size=portrait")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "<svg") || !strings.Contains(body, "@font-face") {
		t.Errorf("body does not look like a self-contained card: %.80q", body)
	}
	// Landscape is a different size, so a different document.
	land := get(t, h, "/card/"+talkID+"?size=landscape")
	if land.Code != http.StatusOK {
		t.Fatalf("landscape status = %d", land.Code)
	}
	if !strings.Contains(land.Body.String(), `width="1200"`) {
		t.Error("landscape card is not 1200 wide")
	}
}

func TestUnknownTalkIs404(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, path := range []string{"/card/nope", "/talk/nope", "/download/nope"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	// A POST naming an unknown talk must not create an override for it.
	if rec := postForm(t, h, "/speaker/whoever", url.Values{"talk": {"nope"}}); rec.Code != http.StatusNotFound {
		t.Errorf("POST with unknown talk = %d, want 404", rec.Code)
	}
}

// An invalid size must fall back rather than error: it is usually a stale
// bookmark, and failing the whole page over it would be worse.
func TestInvalidSizeFallsBack(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := get(t, h, "/card/"+talkID+"?size=square")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `width="1400"`) {
		t.Error("want the configured portrait default")
	}
}

// The whole point of the server: an edit persists and changes both the card and
// the copy next to it.
func TestSpeakerEditPersistsAndRerenders(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	before := get(t, h, "/card/"+talkID).Body.String()
	if !strings.Contains(before, "Bysten Labs") {
		t.Fatal("expected the upstream title on the card first")
	}

	rec := postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk":     {talkID},
		"employer": {"Bysten Labs AS"},
		"job":      {"Infrastructure Engineer"},
		"bluesky":  {"@dario.example"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}

	// The manifest is written immediately — there is no save button.
	saved, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: SpeakerOverride", "name: dario-haaland",
		"employer: Bysten Labs AS", "job: Infrastructure Engineer", "bluesky: dario.example"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("manifest missing %q:\n%s", want, saved)
		}
	}
	// A leading @ is stripped so the post does not double it.
	if strings.Contains(string(saved), "bluesky: '@") {
		t.Error("the @ prefix should have been stripped")
	}

	// The returned fragment shows the new values and the new copy.
	frag := rec.Body.String()
	if !strings.Contains(frag, `value="Bysten Labs AS"`) {
		t.Error("fragment does not show the saved employer")
	}
	if !strings.Contains(frag, "@dario.example") {
		t.Error("fragment copy does not mention the new handle")
	}
	// The employer is no longer a guess, so the warning must be gone.
	if strings.Contains(frag, "guessed from") {
		t.Error("fragment still flags the employer as guessed")
	}

	// And the card itself now carries the corrected role line.
	after := get(t, h, "/card/"+talkID).Body.String()
	if !strings.Contains(after, "Infrastructure Engineer at Bysten Labs AS") {
		t.Error("card was not re-rendered with the override")
	}
}

// Card URLs carry a revision so an HTMX swap does not re-insert a src the
// browser will serve from cache, which would make an edit look like a no-op.
func TestCardURLRevisionChangesAfterAnEdit(t *testing.T) {
	_, h, _ := newTestServer(t)
	first := extractCardURL(t, get(t, h, "/talk/"+talkID).Body.String())

	postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk": {talkID}, "employer": {"New Corp"},
	})
	second := extractCardURL(t, get(t, h, "/talk/"+talkID).Body.String())

	if first == second {
		t.Errorf("card URL unchanged after an edit: %s", first)
	}
}

func extractCardURL(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `src="/card/`)
	if i < 0 {
		t.Fatal("no card URL in fragment")
	}
	rest := body[i+5:]
	return rest[:strings.IndexByte(rest, '"')]
}

func TestTalkOverrideAndHidden(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	rec := postForm(t, h, "/talk/"+talkID, url.Values{
		"displayTitle": {"Shorter Title"},
		"hidden":       {"1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `class="row hidden"`) {
		t.Error("fragment should mark the row as hidden")
	}

	saved, _ := os.ReadFile(manifestPath)
	for _, want := range []string{"kind: TalkOverride", "displayTitle: Shorter Title", "hidden: true"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("manifest missing %q:\n%s", want, saved)
		}
	}

	// The display title reaches the card.
	if !strings.Contains(get(t, h, "/card/"+talkID).Body.String(), "Shorter Title") {
		t.Error("card does not use the display title")
	}

	// Unchecking clears it again.
	postForm(t, h, "/talk/"+talkID, url.Values{"displayTitle": {"Shorter Title"}})
	saved, _ = os.ReadFile(manifestPath)
	if strings.Contains(string(saved), "hidden: true") {
		t.Errorf("hidden was not cleared:\n%s", saved)
	}
}

func TestExportWritesABundlePerTalk(t *testing.T) {
	srv, h, _ := newTestServer(t)

	rec := do(t, h, "/export", url.Values{"size": {"portrait"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "wrote 2 talks") {
		t.Errorf("export said %q", rec.Body.String())
	}

	// One folder per talk, named the same as the CLI names it.
	dirs, _ := filepath.Glob(filepath.Join(srv.opts.OutDir, "*"))
	if len(dirs) != 2 {
		t.Fatalf("wrote %d folders, want 2: %v", len(dirs), dirs)
	}
	var bundle string
	for _, d := range dirs {
		if strings.Contains(filepath.Base(d), "dario-haaland") {
			bundle = d
		}
	}
	if bundle == "" {
		t.Fatalf("no folder named after the speaker: %v", dirs)
	}

	// The card, both drafts and the editable manifest. PNG and JPEG depend on
	// a rasteriser being installed, so they are not required here.
	for _, name := range []string{"portrait.svg", "linkedin.txt", "bluesky.txt", "promo.yaml"} {
		fi, err := os.Stat(filepath.Join(bundle, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	// The copy file must hold the post body alone, so it pastes verbatim.
	body, err := os.ReadFile(filepath.Join(bundle, "bluesky.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "check before posting") {
		t.Error("bluesky.txt should not carry the review notes")
	}
	if !strings.Contains(string(body), "Dario Haaland") {
		t.Errorf("bluesky.txt = %q", body)
	}

	// The manifest carries the overrides to edit — here an empty one, since
	// nothing was corrected — and the guess only in the output record.
	yml, err := os.ReadFile(filepath.Join(bundle, "promo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: SpeakerOverride", "name: dario-haaland", "kind: Output", "employerGuessed: true"} {
		if !strings.Contains(string(yml), want) {
			t.Errorf("promo.yaml missing %q:\n%s", want, yml)
		}
	}

	// Hiding a talk excludes it, matching `promo export --all`.
	postForm(t, h, "/talk/talk-2", url.Values{"hidden": {"1"}})
	os.RemoveAll(srv.opts.OutDir)
	rec = do(t, h, "/export", url.Values{})
	if !strings.Contains(rec.Body.String(), "wrote 1 talks") || !strings.Contains(rec.Body.String(), "1 hidden") {
		t.Errorf("export said %q", rec.Body.String())
	}
}

func TestDownloadUsesTheCLIFilename(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := get(t, h, "/download/"+talkID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	cd := rec.Header().Get("Content-Disposition")
	// Same stem as `promo export` writes, so a browser download and a CLI export
	// land on the same name.
	if !strings.Contains(cd, "d1-0900-dario-haaland") || !strings.Contains(cd, ".svg") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

// Talk titles and speaker names come from a CMS, so the templates must escape
// them. html/template does this by default; the test pins that nothing is
// emitted through a raw-HTML path.
func TestTemplatesEscapeUpstreamText(t *testing.T) {
	dir := t.TempDir()
	th, _ := theme.Default()
	program := testProgram()
	program.Talks[0].Title = `<script>alert("x")</script> & co`
	program.Talks[0].Speakers[0].Name = `<b>bold</b>`

	srv, err := New(Options{
		Program: program, Set: manifest.New(filepath.Join(dir, "m.yaml")),
		Theme: th, Size: "portrait", OutDir: dir, NoLinks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := get(t, srv.Handler(), "/").Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<b>bold</b>") {
		t.Error("upstream markup was not escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the escaped form to be present")
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := get(t, h, "/static/htmx.min.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "htmx") {
		t.Error("does not look like htmx")
	}
}

// A browser will have several edits in flight at once. Run with -race.
func TestConcurrentEditsAreSerialised(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			postForm(t, h, "/speaker/dario-haaland", url.Values{
				"talk": {talkID}, "employer": {"Corp"},
			})
			postForm(t, h, "/talk/talk-2", url.Values{"displayTitle": {"T"}})
			get(t, h, "/")
			get(t, h, "/card/"+talkID)
		}(i)
	}
	wg.Wait()

	// The manifest must still be valid and loadable after the pile-up.
	set, err := manifest.Load(manifestPath)
	if err != nil {
		t.Fatalf("manifest is corrupt after concurrent edits: %v", err)
	}
	if sp, ok := set.Speaker("dario-haaland"); !ok || sp.Employer != "Corp" {
		t.Errorf("speaker override = %+v, ok=%v", sp, ok)
	}
}

// Regression: the draft copy was built from the ORIGINAL speakers while the
// card used the rewritten ones, so a corrected name appeared on the card but
// not in the draft sitting beside it.
func TestCorrectedNameReachesBothCardAndCopy(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	rec := postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk": {talkID},
		"name": {"Dárió Håaland"},
		// A role line that does not fit "<job> at <employer>" at all.
		"title": {"Maintainer, Co-Chair CNCF TAG Infrastructure"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	frag := rec.Body.String()

	// The draft copy names the corrected speaker.
	if !strings.Contains(frag, "Dárió Håaland") {
		t.Errorf("fragment does not use the corrected name:\n%s", frag)
	}
	if strings.Contains(svgText(t, get(t, h, "/card/"+talkID).Body.String()), "Dario Haaland") {
		t.Error("the card still shows the uncorrected name")
	}

	// The form now shows the corrected value, since that is what it edits.
	if !strings.Contains(frag, `value="Dárió Håaland"`) {
		t.Error("the name input should hold the corrected name")
	}

	// The card's role line is the verbatim title.
	card := svgText(t, get(t, h, "/card/"+talkID).Body.String())
	if !strings.Contains(card, "Maintainer, Co-Chair CNCF TAG Infrastructure") {
		t.Errorf("card role line = %q", card)
	}

	saved, _ := os.ReadFile(manifestPath)
	for _, want := range []string{"name: Dárió Håaland", "title: Maintainer,"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("manifest missing %q:\n%s", want, saved)
		}
	}
}

// svgText returns a card's visible text with lines joined by spaces, so an
// assertion does not depend on where autofit happened to wrap. The <style>
// block is skipped so embedded font CSS does not pollute the result.
func svgText(t *testing.T, doc string) string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(doc))
	var parts []string
	inStyle := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch e := tok.(type) {
		case xml.StartElement:
			if e.Name.Local == "style" {
				inStyle = true
			}
		case xml.EndElement:
			if e.Name.Local == "style" {
				inStyle = false
			}
		case xml.CharData:
			if inStyle {
				continue
			}
			if v := strings.TrimSpace(string(e)); v != "" {
				parts = append(parts, v)
			}
		}
	}
	return strings.Join(parts, " ")
}

// Regression: the speaker form's trigger was "change from:find input", and
// `find` selects the FIRST matching descendant — the hidden talk input. Hidden
// inputs never fire change, so every speaker field silently did nothing in the
// browser. Verified in a real browser: a bare "change" on the form posts,
// because change events bubble; the "from:find input" spelling never fires.
//
// The handler tests around this one all POST directly and so could not catch
// it, which is exactly why this asserts on the markup.
func TestSpeakerFormTriggerFiresInABrowser(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := get(t, h, "/talk/"+talkID).Body.String()

	i := strings.Index(body, `hx-post="/speaker/`)
	if i < 0 {
		t.Fatal("no speaker form in the fragment")
	}
	form := body[i:]
	if end := strings.Index(form, ">"); end > 0 {
		form = form[:end]
	}
	if !strings.Contains(form, `hx-trigger="change"`) {
		t.Errorf("speaker form trigger is not a bare form-level change: %s", form)
	}

	// No `from:` modifier anywhere: a trigger sourced from one element cannot
	// see changes to the others, and every field in these forms must post.
	if strings.Contains(body, "from:find") || strings.Contains(body, "from:") {
		t.Error("a from: trigger modifier is back; it cannot see sibling inputs")
	}

	// Every editable speaker field has to be inside that form, or it is not
	// included in the post.
	for _, field := range []string{"name", "employer", "job", "title", "image",
		"linkedin", "bluesky", "x"} {
		if !strings.Contains(body, `name="`+field+`"`) {
			t.Errorf("speaker form is missing the %q field", field)
		}
	}
}

// A photo on an arbitrary host must be fetched exactly as given: no CMS
// transform parameters bolted on, which is what broke GitHub avatars and
// LinkedIn photos before ImageSource existed.
func TestArbitraryPhotoHostIsFetchedVerbatim(t *testing.T) {
	const photo = "https://media.licdn.com/dms/image/v2/ABC/profile-displayphoto-crop_800_800/X/0/1777263639281?e=1790812800&v=beta&t=sig"

	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.String())
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG(t))
	}))
	defer srv.Close()

	// The same shape as a LinkedIn URL — query string and signature included —
	// pointed at a server that records what it was asked for.
	url := srv.URL + "/dms/image/v2/ABC/photo?e=1790812800&v=beta&t=sig"
	sp := cnd.Speaker{Slug: "s", Name: "A Speaker", Image: url}
	if got := cnd.ImageSourceOf(sp.Image, 600); got.URL != url {
		t.Fatalf("ImageSource = %q, want the URL untouched", got.URL)
	}

	th, _ := theme.Default()
	r, err := render.New(th, cache.New(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasPhoto(sp.Image) {
		t.Error("a photo on an arbitrary host was not fetched")
	}
	if len(asked) == 0 {
		t.Fatal("the host was never asked")
	}
	for _, a := range asked {
		for _, bolted := range []string{"fit=crop", "fm=jpg", "w=600"} {
			if strings.Contains(a, bolted) {
				t.Errorf("request %q carries a CMS transform parameter %q", a, bolted)
			}
		}
	}
	// And the real URL shape parses the same way.
	if got := cnd.ImageSourceOf(photo, 600); got.URL != photo {
		t.Errorf("LinkedIn URL = %q, want it untouched", got.URL)
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

// The import button is the mirror of the export button, so the two together
// close the loop without dropping to a terminal.
func TestImportButtonIsPresent(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := get(t, h, "/").Body.String()

	for _, want := range []string{
		`hx-post="/import"`,
		`name="force"`,
		`id="status"`,
		// And one of each per talk.
		`hx-post="/talk/` + talkID + `/export`,
		`hx-post="/talk/` + talkID + `/import`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

// Having nothing to import is a normal state, not an error, and the message has
// to say what to do rather than surface a stat failure.
func TestImportWithNothingExported(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := do(t, h, "/import", url.Values{})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "nothing to import") {
		t.Errorf("body = %q", body)
	}
	if !strings.Contains(body, "Export all") {
		t.Error("the message should say what to do next")
	}
	if strings.Contains(body, "no such file or directory") {
		t.Error("a raw stat error leaked into the UI")
	}
}

// The whole loop, through the same code the CLI uses.
func TestImportButtonMergesAnEditedBundle(t *testing.T) {
	srv, h, manifestPath := newTestServer(t)

	if rec := do(t, h, "/export", url.Values{"size": {"portrait"}}); rec.Code != http.StatusOK {
		t.Fatalf("export failed: %s", rec.Body)
	}

	// Unedited bundles must import as nothing.
	rec := do(t, h, "/import", url.Values{})
	if !strings.Contains(rec.Body.String(), "nothing to import from") {
		t.Errorf("an unedited import changed something: %s", rec.Body)
	}

	// Edit one bundle's override document, leaving the record alone.
	bundles, _ := filepath.Glob(filepath.Join(srv.opts.OutDir, "*", "promo.yaml"))
	var edited string
	for _, b := range bundles {
		if strings.Contains(b, "dario-haaland") {
			edited = b
		}
	}
	if edited == "" {
		t.Fatalf("no bundle for the speaker: %v", bundles)
	}
	raw, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	docs := strings.Split(string(raw), "\n---\n")
	for i, d := range docs {
		if strings.Contains(d, "kind: SpeakerOverride") {
			docs[i] = strings.Replace(d, "spec: {}", "spec:\n  name: Dárió Håaland", 1)
		}
	}
	if err := os.WriteFile(edited, []byte(strings.Join(docs, "\n---\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	rec = do(t, h, "/import", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	// The report names the change, so an import is never silent.
	if !strings.Contains(body, "imported 1 change") {
		t.Errorf("report = %q", body)
	}
	if !strings.Contains(body, "SpeakerOverride/dario-haaland name") {
		t.Errorf("the report does not name the field: %q", body)
	}
	// The status is swapped out-of-band, so it survives replacing the rows.
	if !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Error("the status was not swapped out-of-band")
	}
	// The re-rendered rows show the imported value.
	if !strings.Contains(body, "Dárió Håaland") {
		t.Error("the rows were not re-rendered with the imported name")
	}
	// And it reached the project manifest.
	saved, _ := os.ReadFile(manifestPath)
	if !strings.Contains(string(saved), "name: Dárió Håaland") {
		t.Errorf("manifest = %s", saved)
	}
}

// Card URLs carry a revision, so an import has to bump it or the browser keeps
// serving the cards from before the merge.
func TestImportBumpsTheCardRevision(t *testing.T) {
	srv, h, _ := newTestServer(t)
	do(t, h, "/export", url.Values{"size": {"portrait"}})

	bundles, _ := filepath.Glob(filepath.Join(srv.opts.OutDir, "*", "promo.yaml"))
	for _, b := range bundles {
		if !strings.Contains(b, "dario-haaland") {
			continue
		}
		raw, _ := os.ReadFile(b)
		docs := strings.Split(string(raw), "\n---\n")
		for i, d := range docs {
			if strings.Contains(d, "kind: SpeakerOverride") {
				docs[i] = strings.Replace(d, "spec: {}", "spec:\n  name: Ny Navn", 1)
			}
		}
		os.WriteFile(b, []byte(strings.Join(docs, "\n---\n")), 0o644)
	}

	before := extractCardURL(t, get(t, h, "/talk/"+talkID).Body.String())
	do(t, h, "/import", url.Values{})
	after := extractCardURL(t, get(t, h, "/talk/"+talkID).Body.String())

	if before == after {
		t.Errorf("card URL unchanged after an import: %s", before)
	}
}

// A malformed bundle is a failure, not a silent skip.
func TestImportRejectsAMalformedBundle(t *testing.T) {
	srv, h, manifestPath := newTestServer(t)
	dir := filepath.Join(srv.opts.OutDir, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "apiVersion: wrong\nkind: SpeakerOverride\nmetadata:\n  name: a\nspec: {}\n"
	if err := os.WriteFile(filepath.Join(dir, "promo.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, "/import", url.Values{})
	if !strings.Contains(rec.Body.String(), `class=" error"`) {
		t.Errorf("the failure is not shown as an error: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "unsupported apiVersion") {
		t.Errorf("body = %q", rec.Body)
	}
	if _, err := os.Stat(manifestPath); err == nil {
		t.Error("a failed import wrote the manifest")
	}
}

// Regression: the page hung. Every row's warnings came from a full card render
// inside the page lock, so rendering the index fetched 36 photos and
// base64-encoded two fonts per row — and one unresponsive photo host blocked
// the lock every request needs, wedging the whole server rather than just the
// image it belonged to.
//
// Now the rows are built with Inspect, which does no network I/O, and the
// probing that does happens outside the lock and is bounded by a timeout.
func TestIndexDoesNotWaitOnADeadPhotoHost(t *testing.T) {
	block := make(chan struct{})
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // accepts the connection, never answers
	}))
	defer dead.Close()
	defer close(block)

	dir := t.TempDir()
	th, err := theme.Default()
	if err != nil {
		t.Fatal(err)
	}
	set := manifest.New(filepath.Join(dir, "promos.yaml"))
	if err := set.SetSpeaker("dario-haaland", manifest.SpeakerSpec{
		Image: dead.URL + "/photo.png",
	}); err != nil {
		t.Fatal(err)
	}

	srv, err := New(Options{
		Program: testProgram(),
		Set:     set,
		Theme:   th,
		// Long enough that a per-load fetch would be unmistakable — a failed
		// fetch is not cached, so rendering cards in the page would pay this
		// on every load. The probe pays it once.
		Images:  &cache.Cache{Dir: filepath.Join(dir, "img"), TTL: time.Minute, Timeout: 2 * time.Second},
		Size:    "portrait",
		OutDir:  filepath.Join(dir, "out"),
		NoLinks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	// The probe pays the timeout once and remembers the answer, so the page
	// itself must be quick — and quick again.
	get(t, h, "/")
	for i := range 3 {
		start := time.Now()
		rec := get(t, h, "/")
		if rec.Code != http.StatusOK {
			t.Fatalf("load %d: status = %d", i, rec.Code)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("load %d took %v; the page is waiting on the photo", i, elapsed)
		}
		// And it still reports the fallback, which is the whole reason the
		// probe exists.
		if !strings.Contains(rec.Body.String(), "showing initials") {
			t.Error("the page does not report that the card fell back to initials")
		}
	}

	// An edit must not block on it either.
	start := time.Now()
	rec := postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk": {talkID}, "employer": {"Corp"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("edit status = %d", rec.Code)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("an edit took %v with a dead photo host", elapsed)
	}
}

// A found value has to be editable in place. Showing it only as a grey
// placeholder meant retyping it to change one character.
func TestFormIsPrefilledWithTheValuesInUse(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := get(t, h, "/talk/"+talkID).Body.String()

	for _, want := range []string{
		// The employer guessed out of the profile title, in the input itself.
		`name="employer" value="Bysten Labs"`,
		// The name the CMS has.
		`name="name" value="Dario Haaland"`,
		// And the talk's own title, so shortening it is an edit not a retype.
		`name="displayTitle"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `value="Nok nett"`) {
		t.Error("the display title input is not pre-filled with the title in use")
	}
	// Placeholders are now only for genuinely empty fields.
	if strings.Contains(body, `placeholder="Bysten Labs"`) {
		t.Error("the found employer is still only a placeholder")
	}
}

// Because the form arrives fully populated, the handler must store only what
// changed. Writing the lot back would mark every guess as confirmed the first
// time any field was touched.
func TestEditingOneFieldDoesNotConfirmTheRest(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	// Submit the whole form with only the name altered — exactly what the
	// browser sends when you edit one input.
	rec := postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk":     {talkID},
		"name":     {"Dárió Håaland"},
		"employer": {"Bysten Labs"}, // unchanged: the guess
		"job":      {""},
		"title":    {"Bysten Labs"}, // unchanged: the upstream role line
		"image":    {""},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}

	saved, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "name: Dárió Håaland") {
		t.Errorf("the edit was not stored:\n%s", saved)
	}
	// The guessed employer must NOT have been recorded as a correction.
	if strings.Contains(string(saved), "employer:") {
		t.Errorf("an unchanged guess was stored as a correction:\n%s", saved)
	}
	if strings.Contains(string(saved), "title:") {
		t.Errorf("an unchanged role line was stored:\n%s", saved)
	}

	// And the employer is still reported as a guess, which is the point.
	frag := rec.Body.String()
	if !strings.Contains(frag, "guessed from") {
		t.Error("editing the name silenced the employer guess")
	}
}

// Submitting the form untouched must change nothing at all.
func TestSubmittingAnUnchangedFormStoresNothing(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	rec := postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk":     {talkID},
		"name":     {"Dario Haaland"},
		"employer": {"Bysten Labs"},
		"title":    {"Bysten Labs"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body, err := os.ReadFile(manifestPath); err == nil {
		if strings.Contains(string(body), "dario-haaland") {
			t.Errorf("an unchanged submission wrote an override:\n%s", body)
		}
	}
}

// Clearing a pre-filled field is "no opinion", not "make it empty": an override
// cannot express a blank, so the value reverts to what was found and the form
// shows it again.
func TestClearingAFieldRevertsToTheFoundValue(t *testing.T) {
	_, h, _ := newTestServer(t)

	// Correct it, then clear it.
	postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk": {talkID}, "name": {"Dárió Håaland"},
	})
	rec := postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk": {talkID}, "name": {""},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `name="name" value="Dario Haaland"`) {
		t.Errorf("clearing did not revert to the found name:\n%s", rec.Body)
	}
}

// The talk's title input is pre-filled too, so submitting it unchanged must not
// be recorded as a shortening.
func TestUnchangedDisplayTitleIsNotStored(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	rec := postForm(t, h, "/talk/"+talkID, url.Values{
		"displayTitle": {"Nok nett"}, // the title as submitted
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body, err := os.ReadFile(manifestPath); err == nil {
		if strings.Contains(string(body), "displayTitle") {
			t.Errorf("the unchanged title was stored:\n%s", body)
		}
	}

	// A real shortening still is.
	postForm(t, h, "/talk/"+talkID, url.Values{"displayTitle": {"Kortere"}})
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "displayTitle: Kortere") {
		t.Errorf("a real shortening was not stored:\n%s", body)
	}
}

// The role line input shows the line the CARD draws, which the card composes
// from employer and job. Showing the upstream text there while the card said
// something else is the drift this pre-filling is meant to remove — and
// diffing against the upstream would record the composed line as a verbatim
// override, freezing it so employer and job stopped driving it.
func TestRoleLineFieldTracksTheCard(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	// Correct only the employer.
	rec := postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk":     {talkID},
		"name":     {"Dario Haaland"},
		"employer": {"Bysten Labs AS"},
		"title":    {"Bysten Labs"}, // as shown before the edit
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}

	// Only the employer is stored; the role line is still composed.
	saved, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "employer: Bysten Labs AS") {
		t.Errorf("employer not stored:\n%s", saved)
	}
	if strings.Contains(string(saved), "title:") {
		t.Errorf("the composed role line was frozen as an override:\n%s", saved)
	}

	// The card and the field now agree on the composed value.
	card := svgText(t, get(t, h, "/card/"+talkID).Body.String())
	if !strings.Contains(card, "Bysten Labs AS") {
		t.Errorf("card role line = %q", card)
	}
	frag := get(t, h, "/talk/"+talkID).Body.String()
	if !strings.Contains(frag, `name="title" value="Bysten Labs AS"`) {
		t.Errorf("the role line field does not show the card's value:\n%s", frag)
	}

	// A genuinely different role line is still taken verbatim.
	postForm(t, h, "/speaker/dario-haaland", url.Values{
		"talk": {talkID}, "employer": {"Bysten Labs AS"},
		"title": {"Maintainer, Co-Chair CNCF TAG"},
	})
	saved, _ = os.ReadFile(manifestPath)
	if !strings.Contains(string(saved), "title: Maintainer, Co-Chair CNCF TAG") {
		t.Errorf("a real role line edit was not stored:\n%s", saved)
	}
}

// The preview server must resolve the card's language the same way it resolves
// the copy's, so the image and the draft beside it cannot disagree.
func TestServedCardUsesTheTalksLanguage(t *testing.T) {
	dir := t.TempDir()
	th, err := theme.Default()
	if err != nil {
		t.Fatal(err)
	}
	program := testProgram()
	program.Talks[0].Title = "Praktisk AI-drevet Kubernetes-drift"
	program.Talks[0].Abstract = "Vi ser på hvordan det ikke fungerte og hva vi gjorde med det."
	program.Talks[0].Speakers = []cnd.Speaker{
		{ID: "a", Name: "leffen", Slug: "leffen"},
		{ID: "b", Name: "Lars", Slug: "lars"},
	}

	set := manifest.New(filepath.Join(dir, "promos.yaml"))
	srv, err := New(Options{
		Program: program, Set: set, Theme: th, Size: "portrait",
		OutDir: filepath.Join(dir, "out"), NoLinks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	card := svgText(t, get(t, h, "/card/"+talkID).Body.String())
	if !strings.Contains(card, "leffen og Lars") {
		t.Errorf("served card = %q, want the Norwegian conjunction", card)
	}

	// Forcing English through the talk override changes the card too.
	if rec := postForm(t, h, "/talk/"+talkID, url.Values{"language": {"en"}}); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	card = svgText(t, get(t, h, "/card/"+talkID).Body.String())
	if !strings.Contains(card, "leffen and Lars") {
		t.Errorf("served card = %q, want the forced English conjunction", card)
	}
}

// The wand buttons rewrite the field's current value and then go through the
// ordinary update path, so they diff, save and re-render exactly as typing
// would.
func TestWandButtonsArePresent(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := get(t, h, "/talk/"+talkID).Body.String()

	for _, want := range []string{
		`hx-post="/talk/` + talkID + `/titlecase"`,
		`hx-post="/speaker/dario-haaland/namecase"`,
		// They send the form, so they act on what is on screen rather than on
		// what was last saved.
		`hx-include="closest form"`,
		`class="wand"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

func TestNameWandCapitalises(t *testing.T) {
	dir := t.TempDir()
	th, err := theme.Default()
	if err != nil {
		t.Fatal(err)
	}
	program := testProgram()
	program.Talks[0].Speakers[0].Name = "leffen"

	srv, err := New(Options{
		Program: program, Set: manifest.New(filepath.Join(dir, "promos.yaml")),
		Theme: th, Size: "portrait", OutDir: filepath.Join(dir, "out"), NoLinks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	rec := postForm(t, h, "/speaker/dario-haaland/namecase", url.Values{
		"talk": {talkID}, "name": {"leffen"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `name="name" value="Leffen"`) {
		t.Errorf("the field was not capitalised:\n%s", rec.Body)
	}
	saved, _ := os.ReadFile(filepath.Join(dir, "promos.yaml"))
	if !strings.Contains(string(saved), "name: Leffen") {
		t.Errorf("manifest = %s", saved)
	}
	// The card shows it too.
	if card := svgText(t, get(t, h, "/card/"+talkID).Body.String()); !strings.Contains(card, "Leffen") {
		t.Errorf("card = %q", card)
	}
}

// The title wand picks its convention from the talk's LANGUAGE, resolved
// against the abstract: a title is a few words and detection needs prose.
func TestTitleWandFollowsTheTalksLanguage(t *testing.T) {
	newServer := func(t *testing.T, title, abstract string) (http.Handler, string) {
		t.Helper()
		dir := t.TempDir()
		th, err := theme.Default()
		if err != nil {
			t.Fatal(err)
		}
		program := testProgram()
		program.Talks[0].Title = title
		program.Talks[0].Abstract = abstract
		srv, err := New(Options{
			Program: program, Set: manifest.New(filepath.Join(dir, "promos.yaml")),
			Theme: th, Size: "portrait", OutDir: filepath.Join(dir, "out"), NoLinks: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return srv.Handler(), filepath.Join(dir, "promos.yaml")
	}

	// Norwegian: sentence case, and a known acronym inside a compound is
	// restored. The title alone carries no Norwegian function words, so this
	// only works because the abstract is consulted.
	h, path := newServer(t, "Agentic Cloud Ops",
		"Er du klar for å gi slipp på kontrollen og la agenter drifte clusteret ditt?")
	rec := postForm(t, h, "/talk/"+talkID+"/titlecase", url.Values{
		"displayTitle": {"agentic cloud ops: praktisk ai-drevet drift"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	saved, _ := os.ReadFile(path)
	if !strings.Contains(string(saved), "Agentic cloud ops: Praktisk AI-drevet drift") {
		t.Errorf("Norwegian title not sentence-cased:\n%s", saved)
	}

	// English: title case, minor words lowercase in the middle.
	h, path = newServer(t, "Shift Left",
		"This talk walks through what broke and why the obvious fix made it worse.")
	rec = postForm(t, h, "/talk/"+talkID+"/titlecase", url.Values{
		"displayTitle": {"shift left with reliability testing"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	saved, _ = os.ReadFile(path)
	if !strings.Contains(string(saved), "Shift Left with Reliability Testing") {
		t.Errorf("English title not title-cased:\n%s", saved)
	}
}

// A wand press that changes nothing must store nothing, like any other
// unchanged submission.
func TestWandOnAlreadyCorrectValuesStoresNothing(t *testing.T) {
	_, h, manifestPath := newTestServer(t)

	if rec := postForm(t, h, "/speaker/dario-haaland/namecase", url.Values{
		"talk": {talkID}, "name": {"Dario Haaland"},
	}); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body, err := os.ReadFile(manifestPath); err == nil && strings.Contains(string(body), "dario-haaland") {
		t.Errorf("an already-correct name was stored:\n%s", body)
	}
}

// One talk at a time: the row's Export writes just its folder, and its Import
// merges just its bundle and swaps back just its row.
func TestPerTalkExportAndImport(t *testing.T) {
	srv, h, manifestPath := newTestServer(t)

	rec := do(t, h, "/talk/"+talkID+"/export", url.Values{"size": {"portrait"}})
	if !strings.Contains(rec.Body.String(), "dario-haaland") {
		t.Errorf("export said %q", rec.Body)
	}
	dirs, _ := filepath.Glob(filepath.Join(srv.opts.OutDir, "*"))
	if len(dirs) != 1 {
		t.Fatalf("wrote %d folders, want just this talk's: %v", len(dirs), dirs)
	}

	// A display title renames the next export's folder; the import must still
	// find this one, by the talk id it records.
	postForm(t, h, "/talk/"+talkID, url.Values{"displayTitle": {"Ny tittel"}})
	bundle := filepath.Join(dirs[0], "promo.yaml")
	raw, _ := os.ReadFile(bundle)
	docs := strings.Split(string(raw), "\n---\n")
	for i, d := range docs {
		if strings.Contains(d, "kind: SpeakerOverride") {
			docs[i] = strings.Replace(d, "spec: {}", "spec:\n  employer: Bysten Labs AS", 1)
		}
	}
	os.WriteFile(bundle, []byte(strings.Join(docs, "\n---\n")), 0o644)

	rec = do(t, h, "/talk/"+talkID+"/import", url.Values{})
	body := rec.Body.String()
	if !strings.Contains(body, "imported 1 change") {
		t.Errorf("import said %q", body)
	}
	// Just this row comes back, out-of-band, with the import applied — and the
	// display title set after the export survives, since the bundle did not
	// touch it.
	if n := strings.Count(body, `class="row `); n != 1 || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Errorf("want one row swapped out-of-band, got %d rows", n)
	}
	if !strings.Contains(body, "Bysten Labs AS") || !strings.Contains(body, "Ny tittel") {
		t.Errorf("row does not show both edits:\n%s", body)
	}
	saved, _ := os.ReadFile(manifestPath)
	if !strings.Contains(string(saved), "employer: Bysten Labs AS") || !strings.Contains(string(saved), "editedAt:") {
		t.Errorf("manifest = %s", saved)
	}

	// The other talk has no bundle, which is said rather than failed.
	if rec := postForm(t, h, "/talk/talk-2/import", url.Values{}); !strings.Contains(rec.Body.String(), "export it first") {
		t.Errorf("importing an unexported talk said %q", rec.Body)
	}
}

// One job at a time: a second one is refused, not queued.
func TestASecondJobIsRefusedWhileOneRuns(t *testing.T) {
	srv, h, _ := newTestServer(t)
	srv.jobs.running = &job{label: "Exporting 2 talks"}
	rec := postForm(t, h, "/export", url.Values{})
	if !strings.Contains(rec.Body.String(), "already running: exporting 2 talks") {
		t.Errorf("body = %q", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "/jobs/") {
		t.Error("a second job was started")
	}
}

func TestPostedIsRecordedAndShown(t *testing.T) {
	_, h, manifestPath := newTestServer(t)
	rec := postForm(t, h, "/talk/"+talkID, url.Values{"postedToday": {"1"}})
	today := time.Now().Format(time.DateOnly)
	if !strings.Contains(rec.Body.String(), "posted "+today) {
		t.Errorf("row has no posted badge:\n%s", rec.Body)
	}
	saved, _ := os.ReadFile(manifestPath)
	if !strings.Contains(string(saved), `posted: "`+today+`"`) {
		t.Errorf("manifest = %s", saved)
	}

	if rec := postForm(t, h, "/talk/"+talkID, url.Values{"posted": {"next week"}}); rec.Code != http.StatusInternalServerError {
		t.Errorf("an invalid date was accepted: %d", rec.Code)
	}
}
