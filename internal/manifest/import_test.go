package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

// speakerDoc is a hand-written override: no revision.
func speakerDoc(key, spec string) string {
	return "apiVersion: " + APIVersion + "\nkind: SpeakerOverride\nmetadata:\n  name: " +
		key + "\nspec:\n" + spec
}

// fixedClock stamps edits at a known time, advancing a minute per call so
// successive edits are distinguishable.
func fixedClock() func() time.Time {
	t := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Minute)
		return t
	}
}

func newTarget(t *testing.T) *Set {
	t.Helper()
	set := New(filepath.Join(t.TempDir(), "promos.yaml"))
	set.Now = fixedClock()
	return set
}

// exportBundle renders what `promo export` writes for a talk's overrides, so
// the tests merge against real revisions.
func exportBundle(t *testing.T, set *Set, talkID string, keys ...string) string {
	t.Helper()
	data, err := Encode("", append([]Document{Doc(KindSource, Metadata{Name: talkID}, map[string]string{})},
		set.BundleOverrides(talkID, keys)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The property that makes import safe: a bundle imported unedited changes
// nothing, whatever the project had.
func TestImportOfAnUneditedBundleIsANoOp(t *testing.T) {
	target := newTarget(t)
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Bysten Labs"}); err != nil {
		t.Fatal(err)
	}
	bundle := exportBundle(t, target, "talk-1", "dario-haaland", "nobody")

	changes, err := target.ImportFrom(loadFrom(t, bundle), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("an unedited bundle imported %v", changes)
	}
	if s, tk := target.Len(); s != 1 || tk != 0 {
		t.Errorf("Len = %d, %d; empty bundle objects must not be stored", s, tk)
	}
}

// The bundle carries the whole object, so a deleted field is a cleared one and
// `hidden: false` turns hiding off — neither of which the old import could do.
func TestImportTakesEditsIncludingClears(t *testing.T) {
	target := newTarget(t)
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Name: "Dario", Employer: "Old Corp"}); err != nil {
		t.Fatal(err)
	}
	if err := target.SetTalk("talk-1", TalkSpec{Hidden: true}); err != nil {
		t.Fatal(err)
	}
	bundle := exportBundle(t, target, "talk-1", "dario-haaland")
	bundle = strings.Replace(bundle, "  name: Dario\n", "", 1)
	bundle = strings.Replace(bundle, "employer: Old Corp", "employer: New Corp", 1)
	bundle = strings.Replace(bundle, "hidden: true", "hidden: false", 1)

	changes, err := target.ImportFrom(loadFrom(t, bundle), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	SortChanges(changes)
	var got []string
	for _, c := range changes {
		got = append(got, c.String())
	}
	want := []string{
		`SpeakerOverride/dario-haaland employer: "Old Corp" → "New Corp"`,
		`SpeakerOverride/dario-haaland name: "Dario" → (cleared)`,
		`TalkOverride/talk-1 hidden: "true" → (cleared)`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if spec, _ := target.Speaker("dario-haaland"); spec != (SpeakerSpec{Employer: "New Corp"}) {
		t.Errorf("speaker = %+v", spec)
	}
	if _, ok := target.Talk("talk-1"); ok {
		t.Error("a talk override cleared to nothing should be deleted")
	}
}

// Edited in the project after export AND in the bundle: the project's version
// is kept and the bundle's reported, unless forced.
func TestImportReportsConflicts(t *testing.T) {
	target := newTarget(t)
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Exported Corp"}); err != nil {
		t.Fatal(err)
	}
	bundle := strings.Replace(exportBundle(t, target, "talk-1", "dario-haaland"),
		"employer: Exported Corp", "employer: Bundle Corp", 1)
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Project Corp"}); err != nil {
		t.Fatal(err)
	}

	changes, err := target.ImportFrom(loadFrom(t, bundle), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].Conflict {
		t.Fatalf("changes = %v, want one conflict", changes)
	}
	if !strings.HasPrefix(changes[0].String(), "conflict") {
		t.Errorf("String() = %q", changes[0].String())
	}
	if spec, _ := target.Speaker("dario-haaland"); spec.Employer != "Project Corp" {
		t.Errorf("employer = %q, want the project's kept", spec.Employer)
	}

	changes, err = target.ImportFrom(loadFrom(t, bundle), ImportOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Conflict {
		t.Fatalf("forced changes = %v", changes)
	}
	if spec, _ := target.Speaker("dario-haaland"); spec.Employer != "Bundle Corp" {
		t.Errorf("employer = %q, want the bundle's after --force", spec.Employer)
	}
}

// Edited in the project after export, but untouched in the bundle: an old
// bundle must not revert newer work, and must not cry conflict either.
func TestImportOfAStaleUneditedBundleKeepsNewerEdits(t *testing.T) {
	target := newTarget(t)
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Exported Corp"}); err != nil {
		t.Fatal(err)
	}
	bundle := exportBundle(t, target, "talk-1", "dario-haaland")
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Newer Corp"}); err != nil {
		t.Fatal(err)
	}

	changes, err := target.ImportFrom(loadFrom(t, bundle), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %v, want none", changes)
	}
	if spec, _ := target.Speaker("dario-haaland"); spec.Employer != "Newer Corp" {
		t.Errorf("employer = %q", spec.Employer)
	}
}

func TestImportStampsEditedAtOnlyOnChange(t *testing.T) {
	target := newTarget(t)
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Corp"}); err != nil {
		t.Fatal(err)
	}
	first := target.EditedAt(KindSpeakerOverride, "dario-haaland")
	if first.IsZero() {
		t.Fatal("an edit was not stamped")
	}
	// The same value again is not an edit.
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Corp"}); err != nil {
		t.Fatal(err)
	}
	if got := target.EditedAt(KindSpeakerOverride, "dario-haaland"); !got.Equal(first) {
		t.Errorf("a no-op save moved editedAt from %v to %v", first, got)
	}

	bundle := strings.Replace(exportBundle(t, target, "talk-1", "dario-haaland"),
		"employer: Corp", "employer: Other", 1)
	if _, err := target.ImportFrom(loadFrom(t, bundle), ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := target.EditedAt(KindSpeakerOverride, "dario-haaland"); !got.After(first) {
		t.Errorf("an import that changed the override left editedAt at %v", got)
	}
}

// A hand-written object has no revision to merge against, so only the fields
// it sets are taken — it cannot clear anything.
func TestImportOfAHandWrittenDocMergesFields(t *testing.T) {
	target := newTarget(t)
	if err := target.SetSpeaker("dario-haaland", SpeakerSpec{Employer: "Kept Corp", Image: "kept.jpg"}); err != nil {
		t.Fatal(err)
	}
	src := loadFrom(t, speakerDoc("dario-haaland", "  job: New Job\n")+
		"---\n"+speakerDoc("someone-else", "  name: Someone Else\n"))

	changes, err := target.ImportFrom(src, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Errorf("changes = %v, want job and the new speaker's name", changes)
	}
	spec, _ := target.Speaker("dario-haaland")
	if spec != (SpeakerSpec{Employer: "Kept Corp", Image: "kept.jpg", Job: "New Job"}) {
		t.Errorf("spec = %+v", spec)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	target := newTarget(t)
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dárió Håaland\n"))
	if first, err := target.ImportFrom(src, ImportOptions{}); err != nil || len(first) != 1 {
		t.Fatalf("first import = %v, %v", first, err)
	}
	if second, err := target.ImportFrom(src, ImportOptions{}); err != nil || len(second) != 0 {
		t.Errorf("second import = %v, %v; want no changes", second, err)
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promos.yaml")
	target := New(path)
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dárió Håaland\n"))

	changes, err := target.ImportFrom(src, ImportOptions{DryRun: true})
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
	target := newTarget(t)
	body := "apiVersion: " + APIVersion + `
kind: TalkOverride
metadata:
  name: talk-1
spec:
  displayTitle: Kortere tittel
  language: no
  hidden: true
  posted: "2026-10-16"
`
	changes, err := target.ImportFrom(loadFrom(t, body), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 {
		t.Fatalf("changes = %v, want four", changes)
	}
	spec, ok := target.Talk("talk-1")
	if !ok {
		t.Fatal("no talk override written")
	}
	want := TalkSpec{DisplayTitle: "Kortere tittel", Language: "no", Hidden: true, Posted: "2026-10-16"}
	if spec != want {
		t.Errorf("spec = %+v", spec)
	}
}

// A v1alpha1 bundle pre-filled every guess; importing it would confirm them
// all, so it is refused with a way forward.
func TestImportRefusesAnOldBundle(t *testing.T) {
	target := newTarget(t)
	body := "apiVersion: " + apiVersionV1Alpha1 + `
kind: TalkInfo
metadata:
  name: talk-1
spec:
  talk:
    title: Something
---
apiVersion: ` + apiVersionV1Alpha1 + `
kind: SpeakerOverride
metadata:
  name: dario-haaland
spec:
  employer: Guessed Corp
`
	_, err := target.ImportFrom(loadFrom(t, body), ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), "re-export") {
		t.Errorf("err = %v, want the re-export message", err)
	}
	if s, _ := target.Len(); s != 0 {
		t.Error("an old bundle was partly imported")
	}
}

// An import either lands completely or not at all, so a reader of the manifest
// never sees half a merge.
func TestImportWritesOnceAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promos.yaml")
	target := New(path)
	src := loadFrom(t, speakerDoc("dario-haaland", "  name: Dárió Håaland\n  job: Engineer\n")+
		"---\n"+speakerDoc("espen-tveitan", "  image: photos/espen.jpg\n"))

	if _, err := target.ImportFrom(src, ImportOptions{}); err != nil {
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
	if reloaded.EditedAt(KindSpeakerOverride, "espen-tveitan").IsZero() {
		t.Error("editedAt did not survive a save and reload")
	}
}

func TestFindBundleGoesByTalkID(t *testing.T) {
	dir := t.TempDir()
	set := newTarget(t)
	for name, id := range map[string]string{"d1-renamed-folder": "talk-1", "d2-other": "talk-2"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "promo.yaml"), []byte(exportBundle(t, set, id)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FindBundle(dir, "talk-1")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(got)) != "d1-renamed-folder" {
		t.Errorf("FindBundle = %q", got)
	}
	if _, err := FindBundle(dir, "talk-9"); err == nil {
		t.Error("want an error for a talk with no bundle")
	}
}

func TestImportFilesReportsProgress(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"a", "b"} {
		p := filepath.Join(dir, name, "promo.yaml")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(speakerDoc(name, "  name: N "+name+"\n")), 0o644)
		paths = append(paths, p)
	}
	var steps []int
	res, err := ImportFiles(newTarget(t), paths, ImportOptions{}, func(done, total int, label string) {
		if total != 2 {
			t.Errorf("total = %d", total)
		}
		steps = append(steps, done)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || len(res[0].Changes) != 1 || len(steps) != 2 || steps[1] != 2 {
		t.Errorf("res = %+v, steps = %v", res, steps)
	}
}

func TestSpeakersAndTalksReturnCopies(t *testing.T) {
	set := newTarget(t)
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
