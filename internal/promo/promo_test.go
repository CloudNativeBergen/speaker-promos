package promo

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/source"
)

func testResolver(t *testing.T, talks ...cnd.Talk) *Resolver {
	t.Helper()
	return &Resolver{
		Program: &cnd.Program{Talks: talks},
		Set:     manifest.New(filepath.Join(t.TempDir(), "promos.yaml")),
	}
}

func setSpeaker(t *testing.T, r *Resolver, key string, spec manifest.SpeakerSpec) {
	t.Helper()
	if err := r.Set.SetSpeaker(key, spec); err != nil {
		t.Fatal(err)
	}
}

func setTalk(t *testing.T, r *Resolver, id string, spec manifest.TalkSpec) {
	t.Helper()
	if err := r.Set.SetTalk(id, spec); err != nil {
		t.Fatal(err)
	}
}

func TestOverridesWin(t *testing.T) {
	src := cnd.Talk{ID: "t", Speakers: []cnd.Speaker{
		{Slug: "dario-haaland", Name: "Dario Haaland", Title: "Bysten Labs"},
		{Slug: "someone-else", Name: "Someone Else", Title: "Dev at Acme"},
	}}
	r := testResolver(t, src)
	r.Links = func(sp cnd.Speaker) cnd.Links {
		return cnd.Links{Bluesky: "scraped.example", GitHub: "kept"}
	}
	setSpeaker(t, r, "dario-haaland", manifest.SpeakerSpec{
		Employer: "Bysten Labs AS",
		Job:      "Infrastructure Engineer",
		Links: cnd.Links{
			LinkedIn: "https://www.linkedin.com/in/dario",
			Bluesky:  "@dario.example",
		},
	})

	got := r.Talk(src)
	sp := got.Speakers[0]
	if sp.Role.Employer != "Bysten Labs AS" || sp.Role.Job != "Infrastructure Engineer" {
		t.Errorf("Role = %+v", sp.Role)
	}
	// An overridden employer is no longer a guess, so the copy stops asking
	// the user to check it.
	if sp.Role.Guessed {
		t.Error("an overridden employer should not be marked as guessed")
	}
	// The correction reaches the CARD, not just the copy: the reason to correct
	// an employer is that the graphic says the wrong thing.
	if want := "Infrastructure Engineer at Bysten Labs AS"; sp.Title != want {
		t.Errorf("Title = %q, want %q", sp.Title, want)
	}
	if sp.Links.LinkedIn != "https://www.linkedin.com/in/dario" {
		t.Errorf("LinkedIn = %q", sp.Links.LinkedIn)
	}
	// A leading @ in the config is stripped so it is not doubled in the post.
	if sp.Links.Bluesky != "dario.example" {
		t.Errorf("Bluesky = %q, want the @ stripped", sp.Links.Bluesky)
	}
	// Scraped values with no override survive.
	if sp.Links.GitHub != "kept" {
		t.Errorf("GitHub = %q", sp.Links.GitHub)
	}

	// A speaker with no entry is untouched.
	other := got.Speakers[1]
	if other.Role.Employer != "Acme" || other.Title != "Dev at Acme" || !other.Role.Guessed {
		t.Errorf("unrelated speaker = %+v", other)
	}
}

// Regression: a talk's Speakers slice shares a backing array with the Program,
// so a speaker on two talks has one array. Writing a correction in place leaked
// one talk's override into every other talk that speaker appeared in.
func TestResolveDoesNotMutateTheProgram(t *testing.T) {
	speakers := []cnd.Speaker{{Slug: "shared", Name: "Shared Speaker", Title: "Original"}}
	first := cnd.Talk{ID: "talk-1", Title: "First", Speakers: speakers}
	second := cnd.Talk{ID: "talk-2", Title: "Second", Speakers: speakers}
	r := testResolver(t, first, second)
	setSpeaker(t, r, "shared", manifest.SpeakerSpec{Employer: "Corrected", Name: "Corrected Name"})
	setTalk(t, r, "talk-1", manifest.TalkSpec{DisplayTitle: "Short"})

	got := r.Talk(first)
	if got.Speakers[0].Title != "Corrected" || got.Speakers[0].Name != "Corrected Name" {
		t.Fatalf("override did not apply: %+v", got.Speakers[0])
	}
	if speakers[0].Title != "Original" || speakers[0].Name != "Shared Speaker" {
		t.Errorf("the shared backing array was mutated: %+v", speakers[0])
	}
	if got.Talk.Speakers[0].Title != "Original" {
		t.Errorf("the source speakers were altered: %+v", got.Talk.Speakers[0])
	}
	if r.Program.Talks[0].Title != "First" {
		t.Errorf("the program's talk was retitled: %q", r.Program.Talks[0].Title)
	}
}

func TestResolveWithoutOverridesKeepsTheSource(t *testing.T) {
	src := cnd.Talk{ID: "t", Title: "Kept", Speakers: []cnd.Speaker{
		{Slug: "nobody", Name: "No Body", Title: "Original", Image: "https://example.com/a.jpg"},
	}}
	got := testResolver(t, src).Talk(src)
	sp := got.Speakers[0]
	if got.Title != "Kept" || got.SubmittedTitle != "Kept" {
		t.Errorf("title = %q, submitted %q", got.Title, got.SubmittedTitle)
	}
	if sp.Name != "No Body" || sp.Title != "Original" || sp.Image != "https://example.com/a.jpg" {
		t.Errorf("speaker altered: %+v", sp)
	}
	if sp.Key != "nobody" || sp.Source != src.Speakers[0] {
		t.Errorf("identity = %q, source %+v", sp.Key, sp.Source)
	}
}

func TestSelectDropsHiddenOnlyInBulk(t *testing.T) {
	r := testResolver(t,
		cnd.Talk{ID: "keep", Title: "A Very Long Original Title"},
		cnd.Talk{ID: "gone", Title: "Cancelled"},
		cnd.Talk{ID: "other", Title: "Untouched"},
	)
	setTalk(t, r, "keep", manifest.TalkSpec{DisplayTitle: "Short Title"})
	setTalk(t, r, "gone", manifest.TalkSpec{Hidden: true})

	got, skipped, err := r.Select(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || skipped != 1 {
		t.Fatalf("Select(all) = %d talks, %d skipped; want 2, 1", len(got), skipped)
	}
	if got[0].Title != "Short Title" || got[0].SubmittedTitle != "A Very Long Original Title" {
		t.Errorf("title = %q (submitted %q); want the display title", got[0].Title, got[0].SubmittedTitle)
	}
	if got[1].Title != "Untouched" {
		t.Errorf("title = %q; want it unchanged", got[1].Title)
	}

	// An explicit selection is resolved but not filtered.
	named, _, err := r.Select(false, []string{"cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	if len(named) != 1 || named[0].ID != "gone" || !named[0].Hidden {
		t.Errorf("Select(\"cancelled\") = %+v", named)
	}

	if _, _, err := r.Select(true, []string{"x"}); err == nil {
		t.Error("--all with a selector should be an error")
	}
	if _, _, err := r.Select(false, []string{"no such talk"}); err == nil {
		t.Error("an unmatched selector should be an error")
	}
}

// The image override has to reach the rendered speaker, and a relative path has
// to resolve against the manifest rather than the working directory.
func TestImageOverrideResolvesAgainstTheManifest(t *testing.T) {
	src := cnd.Talk{ID: "t", Speakers: []cnd.Speaker{
		{Slug: "dario-haaland", Name: "Dario Haaland"},
		{Slug: "other", Name: "Someone Else", Image: "https://example.com/keep.jpg"},
	}}
	r := testResolver(t, src)
	setSpeaker(t, r, "dario-haaland", manifest.SpeakerSpec{Image: "photos/dario.jpg"})

	got := r.Talk(src)
	want := filepath.Join(filepath.Dir(r.Set.Path()), "photos/dario.jpg")
	if got.Speakers[0].Image != want {
		t.Errorf("image = %q, want %q resolved against the manifest", got.Speakers[0].Image, want)
	}
	if got.Speakers[1].Image != "https://example.com/keep.jpg" {
		t.Errorf("an unrelated speaker's image changed: %q", got.Speakers[1].Image)
	}

	// An URL and an absolute path are passed through untouched.
	for _, image := range []string{"https://example.com/a.jpg", "/tmp/a.jpg"} {
		setSpeaker(t, r, "dario-haaland", manifest.SpeakerSpec{Image: image})
		if got := r.Talk(src).Speakers[0].Image; got != image {
			t.Errorf("image %q became %q", image, got)
		}
	}
}

// A name is typed by the speaker into a CMS, so accents go missing. Correcting
// it must reach the card and the copy but must NOT change the speaker's key or
// the talk's file stem, which drive folder names and selectors.
func TestNameOverrideDoesNotChangeIdentity(t *testing.T) {
	src := cnd.Talk{
		ID: "t", Title: "Understanding Kubernetes",
		Schedule: cnd.Slot{Day: 2, StartTime: "15:30"},
		Speakers: []cnd.Speaker{{Slug: "aurelie-vache", Name: "Aurelie Vache"}},
	}
	r := testResolver(t, src)
	setSpeaker(t, r, "aurelie-vache", manifest.SpeakerSpec{Name: "Aurélie Vache"})

	got := r.Talk(src)
	if got.Speakers[0].Name != "Aurélie Vache" {
		t.Errorf("name = %q", got.Speakers[0].Name)
	}
	if got.Speakers[0].Key != "aurelie-vache" {
		t.Errorf("key changed to %q", got.Speakers[0].Key)
	}
	if before, after := src.FileStem(), got.FileStem(); after != before {
		t.Errorf("file stem changed from %q to %q", before, after)
	}
}

// A verbatim role line changes the card, and the copy keeps guessing the
// employer — from the WEBSITE's title, which is what it then quotes. Guessing
// from the override's own title instead put an employer nobody chose into the
// copy, and turned an unedited bundle into a confirmed correction on import.
func TestTitleOnlyOverrideLeavesTheEmployerGuessed(t *testing.T) {
	verbatim := "Maintainer, Principal Open source Architect, Co-Chair CNCF TAG Infrastructure"
	src := cnd.Talk{ID: "t", Speakers: []cnd.Speaker{{Slug: "a", Name: "A", Title: "Dev at Acme"}}}
	r := testResolver(t, src)
	setSpeaker(t, r, "a", manifest.SpeakerSpec{Title: verbatim})

	sp := r.Talk(src).Speakers[0]
	if sp.Title != verbatim {
		t.Errorf("Title = %q, want the verbatim role line", sp.Title)
	}
	if sp.Role.Employer != "Acme" || sp.Role.Job != "Dev" {
		t.Errorf("Role = %+v, want it guessed from the website's title", sp.Role)
	}
	if !sp.Role.Guessed {
		t.Error("a title-only override should leave the employer marked as guessed")
	}
}

// The 2026 program has a speaker with no slug. Keying overrides by slug left
// him unaddressable; the key falls back to his name.
func TestSluglessSpeakerCanBeOverridden(t *testing.T) {
	src := cnd.Talk{ID: "t", Speakers: []cnd.Speaker{
		{ID: "abc", Name: "Dev Speaker", Title: "Bysten Labs"},
		{ID: "def", Name: "Other Speaker"},
	}}
	r := testResolver(t, src)
	key := src.Speakers[0].Key()
	if key == "" {
		t.Fatal("a slugless speaker has no key")
	}
	setSpeaker(t, r, key, manifest.SpeakerSpec{Name: "Dev Spéaker", Employer: "Vestbit"})

	got := r.Talk(src)
	if sp := got.Speakers[0]; sp.Name != "Dev Spéaker" || sp.Role.Employer != "Vestbit" || sp.Key != key {
		t.Errorf("slugless speaker = %+v", sp)
	}
	// Nor does it bleed into any other slugless speaker.
	if sp := got.Speakers[1]; sp.Name != "Other Speaker" || sp.Role.Employer != "" {
		t.Errorf("another slugless speaker picked up the override: %+v", sp)
	}
}

func TestLanguagePrecedence(t *testing.T) {
	norwegian := cnd.Talk{
		ID: "no", Title: "Kan skyen kjøre på en brødrister?",
		Abstract: "Vi ser på hvordan det ikke fungerte og hva vi gjorde med det.",
	}
	r := testResolver(t, norwegian)

	// Detection, with no default and no override.
	if got := r.Talk(norwegian); got.Language != lang.Norwegian || got.Detected != lang.Norwegian {
		t.Errorf("detected = %q / %q, want no", got.Language, got.Detected)
	}
	// The run's default beats detection.
	r.Language = lang.English
	if got := r.Talk(norwegian); got.Language != lang.English || got.Detected != lang.Norwegian {
		t.Errorf("with --language en = %q (detected %q)", got.Language, got.Detected)
	}
	// A per-talk override beats the default.
	setTalk(t, r, "no", manifest.TalkSpec{Language: "no"})
	if got := r.Talk(norwegian); got.Language != lang.Norwegian {
		t.Errorf("with an override = %q, want no", got.Language)
	}
}

// The card joins names in the talk's own language.
func TestSpeakerNamesFollowTheLanguage(t *testing.T) {
	src := cnd.Talk{ID: "t", Speakers: []cnd.Speaker{
		{Slug: "a", Name: "leffen"}, {Slug: "b", Name: "Lars"},
	}}
	r := testResolver(t, src)
	setTalk(t, r, "t", manifest.TalkSpec{Language: "no"})
	if got := r.Talk(src).SpeakerNames(); got != "leffen og Lars" {
		t.Errorf("SpeakerNames = %q", got)
	}
}

// A correction made before the website last changed may be out of date, and
// is flagged; one made after is not.
func TestStaleCorrections(t *testing.T) {
	src := cnd.Talk{ID: "t", Title: "Talk", Speakers: []cnd.Speaker{
		{Slug: "old", Name: "Old Fix", Title: "Dev at Acme"},
		{Slug: "new", Name: "New Fix", Title: "Ops at Acme"},
		{Slug: "none", Name: "No Fix"},
	}}
	r := testResolver(t, src)
	r.Source = source.New(filepath.Join(t.TempDir(), "source.yaml"))

	day := func(d int) time.Time { return time.Date(2026, 10, d, 9, 0, 0, 0, time.UTC) }
	at := day(1)
	r.Set.Now = func() time.Time { return at }
	r.Source.Now = func() time.Time { return at }

	setSpeaker(t, r, "old", manifest.SpeakerSpec{Employer: "Acme AS"})
	setTalk(t, r, "t", manifest.TalkSpec{Posted: "2026-10-01"})
	at = day(2)
	if _, err := r.Source.Reconcile(&cnd.Program{Talks: []cnd.Talk{src}}); err != nil {
		t.Fatal(err)
	}
	at = day(3)
	setSpeaker(t, r, "new", manifest.SpeakerSpec{Employer: "Acme AS"})

	got := r.Talk(src)
	if !got.Speakers[0].Stale {
		t.Error("a correction older than the website's data is not flagged")
	}
	if got.Speakers[1].Stale || got.Speakers[2].Stale {
		t.Errorf("flagged speakers that are not stale: %+v", got.StaleSpeakers())
	}
	if !got.Speakers[0].UpdatedAt.Equal(day(2)) || !got.Speakers[0].EditedAt.Equal(day(1)) {
		t.Errorf("times = %v / %v", got.Speakers[0].UpdatedAt, got.Speakers[0].EditedAt)
	}
	// Recording a posted date is not a correction of the talk's content.
	if got.Stale {
		t.Error("a posted date made the talk stale")
	}
	if got.Posted != "2026-10-01" {
		t.Errorf("Posted = %q", got.Posted)
	}
	if !got.LastChanged().Equal(day(3)) {
		t.Errorf("LastChanged = %v", got.LastChanged())
	}
}

func TestValuesAreWhatTheCardUses(t *testing.T) {
	src := cnd.Talk{ID: "t", Speakers: []cnd.Speaker{{Slug: "a", Name: "A", Title: "Dev at Acme"}}}
	r := testResolver(t, src)
	setSpeaker(t, r, "a", manifest.SpeakerSpec{Employer: "Vestbit"})

	got := r.Talk(src).Speakers[0].Values()
	want := manifest.SpeakerSpec{Name: "A", Employer: "Vestbit", Job: "Dev", Title: "Vestbit"}
	if got != want {
		t.Errorf("Values =\n %+v\nwant %+v", got, want)
	}
	// Against the baseline, only the correction is a difference.
	if diff := got.Diff(Baseline(src.Speakers[0], cnd.Links{})); diff.Employer != "Vestbit" || diff.Name != "" || diff.Job != "" {
		t.Errorf("Diff = %+v", diff)
	}
}
