package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseline is what the tool would say about this speaker with no override: the
// upstream name, and the employer guessed out of the free-text profile title.
func testBaseline() ImportOptions {
	base := map[string]SpeakerSpec{
		"dario-haaland": {Name: "Dario Haaland", Employer: "Bysten Labs"},
		"espen-tveitan": {Name: "Espen Tveitan", Employer: "Vestbit", Job: "Senior Platform Engineer"},
	}
	titles := map[string]string{"talk-1": "Kan skyen kjøre på en brødrister?"}
	return ImportOptions{
		SpeakerBaseline: func(key string) (SpeakerSpec, bool) {
			s, ok := base[key]
			return s, ok
		},
		TalkBaseline: func(id string) (string, bool) {
			t, ok := titles[id]
			return t, ok
		},
	}
}

func loadFrom(t *testing.T, body string) *Set {
	t.Helper()
	path := filepath.Join(t.TempDir(), "promo.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func speakerDoc(slug, spec string) string {
	return "apiVersion: " + APIVersion + "\nkind: SpeakerOverride\nmetadata:\n  name: " +
		slug + "\nspec:\n" + spec
}

// The property that makes import safe: an exported manifest pre-fills the
// guessed employer and the upstream name, so importing it unedited must change
// nothing. Otherwise every guess silently becomes a confirmed correction and
// the warnings that exist to be read stop appearing.
func TestImportIgnoresUneditedPrefills(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dario Haaland\n  employer: Bysten Labs\n"))

	changes, err := target.ImportFrom(src, testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("imported unedited pre-fills: %v", changes)
	}
	if s, _ := target.Len(); s != 0 {
		t.Errorf("target gained %d speaker overrides", s)
	}
}

func TestImportTakesOnlyTheEdits(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	src := loadFrom(t, speakerDoc("dario-haaland",
		"  name: Dárió Håaland\n  employer: Bysten Labs\n  image: photos/dario.jpg\n"))

	changes, err := target.ImportFrom(src, testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	SortChanges(changes)
	if len(changes) != 2 {
		t.Fatalf("changes = %v, want the two edited fields", changes)
	}
	// employer came back equal to the guess, so it is not among them.
	got := []string{changes[0].Field, changes[1].Field}
	if got[0] != "image" || got[1] != "name" {
		t.Errorf("fields = %v, want image and name", got)
	}

	spec, ok := target.Speaker("dario-haaland")
	if !ok {
		t.Fatal("no override written")
	}
	if spec.Name != "Dárió Håaland" || spec.Image != "photos/dario.jpg" {
		t.Errorf("spec = %+v", spec)
	}
	// The unedited employer must not have been written either.
	if spec.Employer != "" {
		t.Errorf("employer = %q, want it left unset", spec.Employer)
	}
}

// Confirming is how you say "that guess was right" and stop being asked.
func TestImportConfirmGuesses(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dario Haaland\n  employer: Bysten Labs\n"))

	opts := testBaseline()
	opts.ConfirmGuesses = true
	changes, err := target.ImportFrom(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %v, want name and employer", changes)
	}
	spec, _ := target.Speaker("dario-haaland")
	if spec.Employer != "Bysten Labs" {
		t.Errorf("employer = %q", spec.Employer)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dárió Håaland\n"))

	first, err := target.ImportFrom(src, testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first import = %v", first)
	}
	second, err := target.ImportFrom(src, testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("second import = %v, want no changes", second)
	}
}

// A conflict is reported with both values, so it is visible rather than silent.
func TestImportReportsConflicts(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Old Corp"}); err != nil {
		t.Fatal(err)
	}
	src := loadFrom(t, speakerDoc("dario-haaland", "  employer: New Corp\n"))

	changes, err := target.ImportFrom(src, testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v", changes)
	}
	c := changes[0]
	if c.From != "Old Corp" || c.To != "New Corp" {
		t.Errorf("change = %+v", c)
	}
	if !strings.Contains(c.String(), "→") {
		t.Errorf("String() = %q, want it to show both values", c.String())
	}
	if spec, _ := target.Speaker("dario-haaland"); spec.Employer != "New Corp" {
		t.Errorf("employer = %q, want the import to win", spec.Employer)
	}
}

// A field the bundle does not mention must not clear one the project manifest
// has: half the bundles would otherwise wipe whatever they happened to omit.
func TestImportDoesNotClearOmittedFields(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{
		Employer: "Kept Corp", Image: "kept.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	src := loadFrom(t, speakerDoc("dario-haaland", "  job: New Job\n"))

	if _, err := target.ImportFrom(src, testBaseline()); err != nil {
		t.Fatal(err)
	}
	spec, _ := target.Speaker("dario-haaland")
	if spec.Employer != "Kept Corp" || spec.Image != "kept.jpg" {
		t.Errorf("import cleared fields it did not mention: %+v", spec)
	}
	if spec.Job != "New Job" {
		t.Errorf("job = %q", spec.Job)
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promos.yaml")
	target := New(path)
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dárió Håaland\n"))

	opts := testBaseline()
	opts.DryRun = true
	changes, err := target.ImportFrom(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want the change still reported", changes)
	}
	if _, ok := target.Speaker("dario-haaland"); ok {
		t.Error("dry run mutated the set")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("dry run wrote the manifest")
	}
}

func TestImportTalkFields(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	body := "apiVersion: " + APIVersion + `
kind: TalkOverride
metadata:
  name: talk-1
spec:
  displayTitle: Kortere tittel
  language: no
  hidden: true
`
	changes, err := target.ImportFrom(loadFrom(t, body), testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %v, want three", changes)
	}
	spec, ok := target.Talk("talk-1")
	if !ok {
		t.Fatal("no talk override written")
	}
	if spec.DisplayTitle != "Kortere tittel" || spec.Language != "no" || !spec.Hidden {
		t.Errorf("spec = %+v", spec)
	}
}

// A displayTitle that merely repeats the submitted title is not a shortening,
// so it is not an edit.
func TestImportIgnoresADisplayTitleEqualToTheSubmittedOne(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	body := "apiVersion: " + APIVersion + `
kind: TalkOverride
metadata:
  name: talk-1
spec:
  displayTitle: Kan skyen kjøre på en brødrister?
`
	changes, err := target.ImportFrom(loadFrom(t, body), testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %v, want none", changes)
	}
}

// TalkInfo carries no overrides, so a bundle's record must import as nothing.
func TestImportIgnoresTheRecord(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	body := "apiVersion: " + APIVersion + `
kind: TalkInfo
metadata:
  name: talk-1
spec:
  talk:
    title: Something Else
  speakers:
    - name: Dario Haaland
      employer: Bysten Labs
`
	changes, err := target.ImportFrom(loadFrom(t, body), testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("the record was imported as overrides: %v", changes)
	}
}

// An import either lands completely or not at all, so a reader of the manifest
// never sees half a merge.
func TestImportWritesOnceAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promos.yaml")
	target := New(path)
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dárió Håaland\n  job: Engineer\n")+
		"---\n"+speakerDoc("espen-tveitan", "  image: photos/espen.jpg\n"))

	if _, err := target.ImportFrom(src, testBaseline()); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("manifest does not reload after import: %v", err)
	}
	if a, ok := reloaded.Speaker("dario-haaland"); !ok || a.Name != "Dárió Håaland" || a.Job != "Engineer" {
		t.Errorf("dario = %+v ok=%v", a, ok)
	}
	if b, ok := reloaded.Speaker("espen-tveitan"); !ok || b.Image != "photos/espen.jpg" {
		t.Errorf("espen = %+v ok=%v", b, ok)
	}
}

// A speaker the program has never heard of has no baseline, so every field it
// carries is an edit — importing a bundle from another conference should not
// silently drop its contents.
func TestImportWithoutABaselineTakesEverything(t *testing.T) {
	target := New(filepath.Join(t.TempDir(), "promos.yaml"))
	src := loadFrom(t, speakerDoc("someone-else", "  name: Someone Else\n  employer: Acme\n"))

	changes, err := target.ImportFrom(src, testBaseline())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Errorf("changes = %v, want both fields", changes)
	}
}

func TestSpeakersAndTalksReturnCopies(t *testing.T) {
	set := New(filepath.Join(t.TempDir(), "promos.yaml"))
	if err := set.SetSpeaker("a", SpeakerSpec{Employer: "Corp"}); err != nil {
		t.Fatal(err)
	}
	if err := set.SetTalk("t", TalkSpec{Hidden: true}); err != nil {
		t.Fatal(err)
	}

	speakers := set.Speakers()
	speakers["a"] = SpeakerSpec{Employer: "Mutated"}
	speakers["b"] = SpeakerSpec{Employer: "Added"}
	talks := set.Talks()
	delete(talks, "t")

	if spec, _ := set.Speaker("a"); spec.Employer != "Corp" {
		t.Errorf("the returned map aliases the Set: %+v", spec)
	}
	if _, ok := set.Speaker("b"); ok {
		t.Error("writing to the returned map added to the Set")
	}
	if _, ok := set.Talk("t"); !ok {
		t.Error("deleting from the returned map removed from the Set")
	}
}
