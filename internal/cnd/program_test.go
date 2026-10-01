package cnd

import (
	"compress/gzip"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/vehagn/speaker-promos/internal/rsc"
)

// The fixture is the real program page with everything but its RSC payload
// stripped, then anonymised and gzipped. Testing against a genuinely-shaped
// payload is the point: the parser's contract is with a page this tool does not
// control, so a hand-written fixture would only prove the parser agrees with
// itself.
//
// Anonymisation replaced people only, structurally — every speaker name, slug,
// employer, photo URL, talk title, abstract and id is fabricated, while the row
// types, $ref graph, nesting and element markers are the real page's. The
// substitutions preserve the characteristics the tests below rely on: 36 talks
// over 2 days, 49 speakers, one emoji title, slugs carrying "ø", a speaker with
// no slug at all, five with no job title, and a description assembled through a
// $ref. The conference itself is not anonymised — it is what the tool is for.
var loadFixture = sync.OnceValues(func() (*Program, error) {
	f, err := os.Open("testdata/program.html.gz")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	html, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}

	flight, err := rsc.Chunks(string(html))
	if err != nil {
		return nil, err
	}
	rows := rsc.Rows(flight)
	conf, err := decodeConference(flight, rows, "2026.cloudnativedays.no")
	if err != nil {
		return nil, err
	}
	talks, err := decodeTalks(flight, rows)
	if err != nil {
		return nil, err
	}
	return &Program{Conference: conf, Talks: talks}, nil
})

func fixture(t *testing.T) *Program {
	t.Helper()
	p, err := loadFixture()
	if err != nil {
		t.Fatalf("loading fixture: %v", err)
	}
	return p
}

func TestDecodeConference(t *testing.T) {
	c := fixture(t).Conference

	if c.Title != "Cloud Native Days Norway 2026" {
		t.Errorf("Title = %q", c.Title)
	}
	if c.StartDate != "2026-10-26" || c.EndDate != "2026-10-27" {
		t.Errorf("dates = %s..%s", c.StartDate, c.EndDate)
	}
	if got := c.Location(); got != "Bergen, Norway" {
		t.Errorf("Location = %q", got)
	}
	if got := c.DateRange(); got != "26–27 October 2026" {
		t.Errorf("DateRange = %q", got)
	}
	// The logo arrives as a $ref to a ~190 KB row; if it were left unresolved
	// the card would contain the literal text "$36".
	if !strings.HasPrefix(c.LogoBright, "<svg") {
		t.Errorf("LogoBright not inline SVG: %.40q", c.LogoBright)
	}
	if len(c.LogoBright) < 1000 {
		t.Errorf("LogoBright suspiciously short: %d bytes", len(c.LogoBright))
	}
}

func TestDecodeTalks(t *testing.T) {
	p := fixture(t)

	if got := len(p.Talks); got != 36 {
		t.Errorf("sessions = %d, want 36", got)
	}
	if got := len(p.Speakers()); got != 49 {
		t.Errorf("speakers = %d, want 49", got)
	}

	days := map[int]string{}
	for _, s := range p.Talks {
		days[s.Schedule.Day] = s.Schedule.Date
		if s.Title == "" {
			t.Error("session with empty talk title got through")
		}
		if s.ID == "" {
			t.Errorf("talk %q has no id", s.Title)
		}
		if len(s.Speakers) == 0 {
			t.Errorf("talk %q has no speakers", s.Title)
		}
		if s.Schedule.Track == "" {
			t.Errorf("talk %q has no track", s.Title)
		}
	}
	if days[1] != "2026-10-26" || days[2] != "2026-10-27" {
		t.Errorf("day numbering = %v, want day 1 first by date", days)
	}
}

// No field anywhere may retain an unresolved reference; these become visible
// text in a rendered promo.
func TestNoUnresolvedReferences(t *testing.T) {
	p := fixture(t)
	for _, s := range p.Talks {
		for _, field := range []struct{ name, val string }{
			{"title", s.Title},
			{"abstract", s.Abstract},
		} {
			if strings.HasPrefix(field.val, "$") || strings.Contains(field.val, "\"$") {
				t.Errorf("talk %q %s holds a reference: %.60q", s.Title, field.name, field.val)
			}
		}
		for _, sp := range s.Speakers {
			if strings.HasPrefix(sp.Name, "$") || strings.HasPrefix(sp.Title, "$") {
				t.Errorf("speaker %q holds a reference (title %q)", sp.Name, sp.Title)
			}
		}
	}
}

// The description is Portable Text whose long spans are $refs; flattening has
// to run after resolution or abstracts come out with "$55" embedded mid-word.
func TestAbstractsAreFlattenedPlainText(t *testing.T) {
	p := fixture(t)

	withAbstract := 0
	for _, s := range p.Talks {
		if s.Abstract == "" {
			continue
		}
		withAbstract++
		if strings.Contains(s.Abstract, `"_type"`) || strings.Contains(s.Abstract, `"_key"`) {
			t.Errorf("talk %q abstract still holds Portable Text JSON", s.Title)
		}
	}
	if withAbstract < 30 {
		t.Errorf("only %d/%d talks got an abstract", withAbstract, len(p.Talks))
	}

	// The Norwegian workshop's abstract is assembled from a $ref mid-paragraph;
	// resolving it is what makes the full sentence appear.
	s := findOne(t, p, "brødrister")
	if !strings.Contains(s.Abstract, "digital suverenitet") {
		t.Errorf("abstract missing text from the resolved reference:\n%.300s", s.Abstract)
	}
}

func TestImageSourceOf(t *testing.T) {
	// The CMS CDN is the only host that understands the transform parameters.
	cms := Speaker{Image: "https://cdn.sanity.io/images/mvzwvw14/production/abc-740x827.png"}
	got := ImageSourceOf(cms.Image, 600)
	if want := cms.Image + "?w=600&h=600&fit=max&fm=jpg&q=82"; got.URL != want {
		t.Errorf("CMS URL = %q, want %q", got.URL, want)
	}
	if got.Path != "" {
		t.Errorf("CMS source should have no Path: %+v", got)
	}
	// An URL that already carries a query must gain "&", not a second "?".
	q := Speaker{Image: "https://cdn.sanity.io/x.png?rect=1,2,3,4"}
	if g := ImageSourceOf(q.Image, 600); !strings.Contains(g.URL, "?rect=1,2,3,4&w=600") {
		t.Errorf("existing query = %q", g.URL)
	}

	// Any other host is fetched verbatim. Appending CMS parameters there was
	// wrong: a GitHub avatar ignored them, and on an arbitrary host they could
	// mean something else entirely.
	for _, raw := range []string{
		"https://avatars.githubusercontent.com/u/12345?v=4",
		"https://example.com/photo.jpg",
	} {
		g := ImageSourceOf(raw, 600)
		if g.URL != raw {
			t.Errorf("ImageSource(%q).URL = %q, want it untouched", raw, g.URL)
		}
	}

	// A photo supplied by an override may be a path on disk.
	for in, want := range map[string]string{
		"/tmp/photo.jpg":        "/tmp/photo.jpg",
		"photos/dario.png":      "photos/dario.png",
		"file:///tmp/photo.jpg": "/tmp/photo.jpg",
	} {
		g := ImageSourceOf(in, 600)
		if g.Path != want {
			t.Errorf("ImageSource(%q).Path = %q, want %q", in, g.Path, want)
		}
		if g.URL != "" {
			t.Errorf("ImageSource(%q) should have no URL: %+v", in, g)
		}
	}

	if g := ImageSourceOf("", 600); !g.Empty() {
		t.Errorf("no photo = %+v, want empty", g)
	}
}

func TestIsRemoteImage(t *testing.T) {
	for in, want := range map[string]bool{
		"https://example.com/a.jpg": true,
		"http://example.com/a.jpg":  true,
		"/tmp/a.jpg":                false,
		"photos/a.jpg":              false,
		"":                          false,
	} {
		if got := IsRemoteImage(in); got != want {
			t.Errorf("IsRemoteImage(%q) = %v", in, got)
		}
	}
}

func TestFindSelectorTiers(t *testing.T) {
	p := fixture(t)

	// Speaker slug.
	got := findOne(t, p, "dario-haaland")
	if !strings.Contains(got.Title, "brødrister") {
		t.Errorf("slug selector found %q", got.Title)
	}

	// Talk id prefix beats everything else.
	byID := findOne(t, p, got.ID[:8])
	if byID.ID != got.ID {
		t.Errorf("id selector found %q", byID.Title)
	}

	// Title substring, case-insensitively.
	findOne(t, p, "skyen kjøre")

	// Slug-form title, so Norwegian letters can be typed as ASCII.
	findOne(t, p, "skyen-kjore")

	if hits := p.Find("definitely-not-a-talk"); len(hits) != 0 {
		t.Errorf("an unmatched selector found %d talks", len(hits))
	}
}

// findOne resolves a selector that must identify exactly one talk.
func findOne(t *testing.T, p *Program, selector string) Talk {
	t.Helper()
	hits := p.Find(selector)
	if len(hits) != 1 {
		t.Fatalf("%q matched %d talks, want 1", selector, len(hits))
	}
	return hits[0]
}

func TestFileStemIsScheduleOrdered(t *testing.T) {
	p := fixture(t)
	seen := map[string]string{}
	for _, s := range p.Talks {
		stem := s.FileStem()
		if stem == "" {
			t.Errorf("empty stem for %q", s.Title)
		}
		if strings.ContainsAny(stem, "/ .") {
			t.Errorf("stem %q unsafe as a filename", stem)
		}
		if prev, dup := seen[stem]; dup {
			t.Errorf("stem %q collides: %q and %q", stem, prev, s.Title)
		}
		seen[stem] = s.Title
		if !strings.HasPrefix(stem, "d1-") && !strings.HasPrefix(stem, "d2-") {
			t.Errorf("stem %q does not lead with the day", stem)
		}
	}
}

func TestSpeakerNames(t *testing.T) {
	mk := func(names ...string) Talk {
		var sp []Speaker
		for _, n := range names {
			sp = append(sp, Speaker{Name: n})
		}
		return Talk{Speakers: sp}
	}
	for _, tc := range []struct {
		in   Talk
		and  string
		want string
	}{
		{mk(), "and", ""},
		{mk("A"), "and", "A"},
		{mk("A", "B"), "and", "A and B"},
		{mk("A", "B", "C"), "and", "A, B and C"},
		// A Norwegian talk's card joins with "og".
		{mk("A", "B"), "og", "A og B"},
		{mk("A", "B", "C"), "og", "A, B og C"},
	} {
		if got := tc.in.SpeakerNames(tc.and); got != tc.want {
			t.Errorf("SpeakerNames = %q, want %q", got, tc.want)
		}
	}
}

func TestJoinAnd(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"A"}, "A"},
		{[]string{"A", "B"}, "A and B"},
		{[]string{"A", "B", "C"}, "A, B and C"},
	} {
		if got := JoinAnd(tc.in, "and"); got != tc.want {
			t.Errorf("JoinAnd(%v) = %q", tc.in, got)
		}
	}
}

func TestShortTrack(t *testing.T) {
	for in, want := range map[string]string{
		"Track 1: Full Day Workshops": "Full Day Workshops",
		"Keynote":                     "Keynote",
		"":                            "",
	} {
		if got := (Slot{Track: in}).ShortTrack(); got != want {
			t.Errorf("ShortTrack(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstSentences(t *testing.T) {
	text := "Første setning her. Andre setning som er ganske lang og fortsetter. Tredje."
	if got := FirstSentences(text, 200); got != text {
		t.Errorf("short-circuit failed: %q", got)
	}
	got := FirstSentences(text, 30)
	if !strings.HasSuffix(got, ".") || len([]rune(got)) > 30 {
		t.Errorf("FirstSentences = %q, want a sentence-boundary cut within 30 runes", got)
	}
	// No sentence end early enough: fall back to a word boundary with an ellipsis.
	noStop := FirstSentences("aaa bbb ccc ddd eee fff ggg hhh", 12)
	if !strings.HasSuffix(noStop, "…") || strings.HasSuffix(noStop, " …") {
		t.Errorf("word-boundary fallback = %q", noStop)
	}
}

func TestFormatAndLevelLabels(t *testing.T) {
	if got := (Talk{Format: "workshop_120"}).FormatLabel(); got != "2 h workshop" {
		t.Errorf("FormatLabel = %q", got)
	}
	// An unknown format must degrade to something readable, not vanish.
	if got := (Talk{Format: "keynote_60"}).FormatLabel(); got != "Keynote 60" {
		t.Errorf("unknown FormatLabel = %q", got)
	}
	if got := (Talk{Level: "intermediate"}).LevelLabel(); got != "Intermediate" {
		t.Errorf("LevelLabel = %q", got)
	}
	if got := (Talk{}).FormatLabel(); got != "" {
		t.Errorf("empty FormatLabel = %q", got)
	}
}

func TestSlugifyNorwegian(t *testing.T) {
	for in, want := range map[string]string{
		"Håvard Østby":     "havard-ostby",
		"Æsj og Ørn":       "aesj-og-orn",
		"Pods on Mars!":    "pods-on-mars",
		"  spaced   out  ": "spaced-out",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// Speaker slugs keep Norwegian letters upstream and `list` prints them
// verbatim, so the ASCII form a user can type must select the same talk.
func TestFindMatchesTransliteratedSpeakerSlug(t *testing.T) {
	p := fixture(t)

	exact := findOne(t, p, "audun-øygard")
	ascii := findOne(t, p, "audun-oygard")
	if ascii.ID != exact.ID {
		t.Errorf("ASCII selector found %q, want %q", ascii.Title, exact.Title)
	}
}

func TestConferenceURLs(t *testing.T) {
	c := fixture(t).Conference
	if got := c.ProgramURL(); got != "https://2026.cloudnativedays.no/program" {
		t.Errorf("ProgramURL = %q", got)
	}
	if got := c.SpeakerURL(Speaker{Slug: "dario-haaland"}); got != "https://2026.cloudnativedays.no/speaker/dario-haaland" {
		t.Errorf("SpeakerURL = %q", got)
	}
	// Without a domain there is nothing valid to emit, so callers get "" and
	// can omit the link rather than print a broken one.
	empty := Conference{}
	if empty.ProgramURL() != "" || empty.SpeakerURL(Speaker{Slug: "x"}) != "" {
		t.Error("a conference with no domain should yield no URLs")
	}
	// A speaker with no slug has no profile page.
	if got := c.SpeakerURL(Speaker{}); got != "" {
		t.Errorf("SpeakerURL without a slug = %q", got)
	}
}
