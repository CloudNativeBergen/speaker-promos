package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vehagn/speaker-promos/internal/cnd"
)

func clock(at *time.Time) func() time.Time { return func() time.Time { return *at } }

func testProgram() *cnd.Program {
	return &cnd.Program{
		Conference: cnd.Conference{Title: "CND 2026", StartDate: "2026-10-26", EndDate: "2026-10-27", Domain: "x.example"},
		Talks: []cnd.Talk{
			{ID: "t1", Title: "One", Topics: []string{"Kubernetes"}, Schedule: cnd.Slot{Day: 1, StartTime: "09:00"},
				Speakers: []cnd.Speaker{{ID: "a", Slug: "ada", Name: "Ada", Title: "Dev at Acme"}}},
			{ID: "t2", Title: "Two",
				Speakers: []cnd.Speaker{{ID: "a", Slug: "ada", Name: "Ada", Title: "Dev at Acme"}, {ID: "b", Name: "No Slug"}}},
		},
	}
}

func TestReconcileStampsOnlyWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yaml")
	day1 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	now := day1
	snap := New(path)
	snap.Now = clock(&now)

	if changed, err := snap.Reconcile(testProgram()); err != nil || !changed {
		t.Fatalf("first reconcile = %v, %v; want a change", changed, err)
	}
	first, _ := os.ReadFile(path)

	// The same program a day later is not a change: nothing is written.
	now = day1.Add(24 * time.Hour)
	if changed, err := snap.Reconcile(testProgram()); err != nil || changed {
		t.Fatalf("unchanged reconcile = %v, %v", changed, err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(first) {
		t.Error("an unchanged program rewrote the snapshot")
	}

	// A speaker retitling themselves stamps that speaker and nothing else.
	p := testProgram()
	p.Talks[0].Speakers[0].Title = "Staff at Acme"
	p.Talks[1].Speakers[0].Title = "Staff at Acme"
	if changed, _ := snap.Reconcile(p); !changed {
		t.Fatal("a retitled speaker was not a change")
	}
	if got := snap.SpeakerUpdatedAt("ada"); !got.Equal(now) {
		t.Errorf("ada updatedAt = %v, want %v", got, now)
	}
	if got := snap.SpeakerUpdatedAt("no-slug"); !got.Equal(day1) {
		t.Errorf("an unchanged speaker was restamped: %v", got)
	}
	if got := snap.TalkUpdatedAt("t1"); !got.Equal(day1) {
		t.Errorf("a talk whose own fields did not change was restamped: %v", got)
	}
	if got := snap.UpdatedAt(); !got.Equal(now) {
		t.Errorf("UpdatedAt = %v", got)
	}
}

func TestSnapshotRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yaml")
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	snap := New(path)
	snap.Now = clock(&at)
	if _, err := snap.Reconcile(testProgram()); err != nil {
		t.Fatal(err)
	}
	if err := snap.SetLinks("ada", cnd.Links{Bluesky: "ada.example"}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for _, want := range []string{"kind: Conference", "kind: Source", "name: t1", "key: no-slug",
		"bluesky: ada.example", "- Kubernetes", "updatedAt: 2026-10-01T09:00:00Z"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("snapshot missing %q:\n%s", want, body)
		}
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Now = clock(&at)
	if changed, _ := reloaded.Reconcile(testProgram()); changed {
		t.Error("a reloaded snapshot saw the same program as a change")
	}
	if got := reloaded.Links("ada"); got.Bluesky != "ada.example" {
		t.Errorf("links lost on reload: %+v", got)
	}
}

func TestLinksFuncKeepsWhatItHadWhenAFetchFails(t *testing.T) {
	snap := New(filepath.Join(t.TempDir(), "source.yaml"))
	if _, err := snap.Reconcile(testProgram()); err != nil {
		t.Fatal(err)
	}
	ada := testProgram().Talks[0].Speakers[0]

	ok := snap.LinksFunc(func(cnd.Speaker) (cnd.Links, error) { return cnd.Links{X: "ada"}, nil })
	if got := ok(ada); got.X != "ada" {
		t.Fatalf("fetched links = %+v", got)
	}
	failing := snap.LinksFunc(func(cnd.Speaker) (cnd.Links, error) { return cnd.Links{}, errors.New("down") })
	if got := failing(ada); got.X != "ada" {
		t.Errorf("a failed fetch erased the stored links: %+v", got)
	}
	// No fetching at all: --no-links still gets what the snapshot knows.
	if got := snap.LinksFunc(nil)(ada); got.X != "ada" {
		t.Errorf("--no-links lost the stored links: %+v", got)
	}
}

func TestDocumentWithoutASnapshot(t *testing.T) {
	var snap *Snapshot
	doc := snap.Document(testProgram().Talks[1])
	spec := doc.Spec.(Talk)
	if doc.Metadata.Name != "t2" || len(spec.Speakers) != 2 || spec.Speakers[1].Key != "no-slug" {
		t.Errorf("document = %+v", doc)
	}
}
