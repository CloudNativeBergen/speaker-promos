// Package source keeps a snapshot of what the conference website said.
//
// The program is re-read from the website on every run, which is right for
// the data but leaves no memory of it: nothing records when a talk's abstract
// changed, or that a speaker retitled themselves after you corrected their
// role line. The snapshot is that memory. It is a file meant to be committed,
// rewritten only when the website's content actually changes, so
// `git diff source.yaml` is a log of what the CMS changed and when.
//
// Each talk and speaker carries an updatedAt: the time the tool first saw its
// current content. The time of the fetch itself is deliberately not stored —
// it would change every run — and comes from the HTTP cache instead
// (cnd.Program.FetchedAt).
package source

import (
	"slices"
	"sync"
	"time"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"gopkg.in/yaml.v3"
)

// DefaultPath is the snapshot a command keeps when none is given.
const DefaultPath = "source.yaml"

// kindConference is the snapshot's conference document. Each talk is a
// manifest.KindSource document, the same one a bundle carries.
const kindConference = "Conference"

const fileHeader = `# What the conference website said, as of the updatedAt on each object.
#
# Written by every promo command after it reads the program, and only when the
# website's content changed — so this file's history is a log of CMS edits.
# Do not edit it: corrections go in promos.yaml, and this file is overwritten.
`

// Speaker is one presenter as the website has them.
type Speaker struct {
	Key   string    `yaml:"key"`
	ID    string    `yaml:"id,omitempty"`
	Slug  string    `yaml:"slug,omitempty"`
	Name  string    `yaml:"name"`
	Title string    `yaml:"title,omitempty"`
	Image string    `yaml:"image,omitempty"`
	Links cnd.Links `yaml:"links,omitempty"`
	// UpdatedAt is when this speaker's content, links included, last changed.
	UpdatedAt time.Time `yaml:"updatedAt,omitempty"`
}

// Talk is one talk as the website has it: the spec of a Source document.
type Talk struct {
	Title    string    `yaml:"title"`
	Abstract string    `yaml:"abstract,omitempty"`
	Format   string    `yaml:"format,omitempty"`
	Level    string    `yaml:"level,omitempty"`
	Status   string    `yaml:"status,omitempty"`
	Topics   []string  `yaml:"topics,omitempty"`
	Schedule cnd.Slot  `yaml:"schedule"`
	Speakers []Speaker `yaml:"speakers"`
}

// Conference is the conference as the website has it, without its logos —
// inline SVG that would bury every real change in the diff.
type Conference struct {
	Title     string `yaml:"title"`
	StartDate string `yaml:"startDate"`
	EndDate   string `yaml:"endDate"`
	City      string `yaml:"city,omitempty"`
	Country   string `yaml:"country,omitempty"`
	Domain    string `yaml:"domain,omitempty"`
}

// record is something the snapshot stores, and when its content last changed.
type record[T any] struct {
	Value     T
	UpdatedAt time.Time
}

// talk is a stored talk: its own fields, and the keys of its speakers.
type talk struct {
	Talk     Talk // Speakers left empty
	Speakers []string
}

// Snapshot is a loaded snapshot. It is safe for concurrent use, and a nil
// Snapshot is an empty one that records nothing.
type Snapshot struct {
	mu         sync.Mutex
	path       string
	conference record[Conference]
	talks      map[string]record[talk]
	order      []string
	speakers   map[string]Speaker

	// Now is the clock changes are stamped with; nil means time.Now.
	Now func() time.Time
}

// New returns an empty snapshot that will save to path.
func New(path string) *Snapshot {
	if path == "" {
		path = DefaultPath
	}
	return &Snapshot{path: path, talks: map[string]record[talk]{}, speakers: map[string]Speaker{}}
}

// Path is the file this snapshot saves to.
func (s *Snapshot) Path() string { return s.path }

// Load reads a snapshot. A missing file is an empty snapshot: the first run
// creates it.
func Load(path string) (*Snapshot, error) {
	snap := New(path)
	err := manifest.ReadFile(snap.path, func(node *yaml.Node) error {
		var doc struct {
			Kind     string            `yaml:"kind"`
			Metadata manifest.Metadata `yaml:"metadata"`
			Spec     yaml.Node         `yaml:"spec"`
		}
		if err := node.Decode(&doc); err != nil {
			return err
		}
		switch doc.Kind {
		case kindConference:
			snap.conference.UpdatedAt = doc.Metadata.UpdatedAt
			return doc.Spec.Decode(&snap.conference.Value)
		case manifest.KindSource:
			var t Talk
			if err := doc.Spec.Decode(&t); err != nil {
				return err
			}
			rec := talk{Speakers: []string{}}
			for _, sp := range t.Speakers {
				rec.Speakers = append(rec.Speakers, sp.Key)
				snap.speakers[sp.Key] = sp
			}
			t.Speakers = nil
			rec.Talk = t
			snap.talks[doc.Metadata.Name] = record[talk]{rec, doc.Metadata.UpdatedAt}
			snap.order = append(snap.order, doc.Metadata.Name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func talkOf(t cnd.Talk) Talk {
	return Talk{
		Title: t.Title, Abstract: t.Abstract, Format: t.Format, Level: t.Level,
		Status: t.Status, Topics: t.Topics, Schedule: t.Schedule,
	}
}

func speakerOf(sp cnd.Speaker) Speaker {
	return Speaker{Key: sp.Key(), ID: sp.ID, Slug: sp.Slug, Name: sp.Name, Title: sp.Title, Image: sp.Image}
}

// same reports whether two values have the same content, by the hash a bundle
// revision uses — which also makes nil and empty topic lists the same.
func same(a, b any) bool { return manifest.Revision(a) == manifest.Revision(b) }

// keep returns rec unchanged if its value is the same as v, or v stamped now.
func keep[T any](rec record[T], had bool, v T, now time.Time) (record[T], bool) {
	if had && same(rec.Value, v) {
		return record[T]{v, rec.UpdatedAt}, false
	}
	return record[T]{v, now}, true
}

// Reconcile records a freshly loaded program, stamping whatever changed, and
// saves the snapshot if anything did. It reports whether anything changed.
//
// Links are not part of the program page; a speaker keeps the links the
// snapshot already has until SetLinks says otherwise.
func (s *Snapshot) Reconcile(p *cnd.Program) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := manifest.StampNow(s.Now)

	c := p.Conference
	conf, changed := keep(s.conference, !s.conference.UpdatedAt.IsZero(), Conference{
		Title: c.Title, StartDate: c.StartDate, EndDate: c.EndDate,
		City: c.City, Country: c.Country, Domain: c.Domain,
	}, now)

	talks := map[string]record[talk]{}
	speakers := map[string]Speaker{}
	var order []string
	for _, t := range p.Talks {
		rec := talk{Talk: talkOf(t), Speakers: []string{}}
		for _, sp := range t.Speakers {
			fresh := speakerOf(sp)
			rec.Speakers = append(rec.Speakers, fresh.Key)
			if _, done := speakers[fresh.Key]; done {
				continue
			}
			old, had := s.speakers[fresh.Key]
			fresh.Links = old.Links
			r, c := keep(record[Speaker]{withoutTime(old), old.UpdatedAt}, had, fresh, now)
			r.Value.UpdatedAt = r.UpdatedAt
			speakers[fresh.Key], changed = r.Value, changed || c
		}
		old, had := s.talks[t.ID]
		r, c := keep(old, had, rec, now)
		talks[t.ID], changed = r, changed || c
		order = append(order, t.ID)
	}
	if len(speakers) != len(s.speakers) || !slices.Equal(order, s.order) {
		changed = true
	}
	s.conference, s.talks, s.speakers, s.order = conf, talks, speakers, order
	if !changed {
		return false, nil
	}
	return true, s.save()
}

func withoutTime(sp Speaker) Speaker {
	sp.UpdatedAt = time.Time{}
	return sp
}

// SetLinks records a speaker's freshly scraped links, stamping and saving if
// they changed.
func (s *Snapshot) SetLinks(key string, links cnd.Links) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.speakers[key]
	if !ok || sp.Links == links {
		return nil
	}
	sp.Links, sp.UpdatedAt = links, manifest.StampNow(s.Now)
	s.speakers[key] = sp
	return s.save()
}

// LinksFunc is the resolver's Links callback over this snapshot.
//
// fetch scrapes a speaker's page; nil means do not fetch (--no-links), and the
// snapshot's links are used as they stand. A successful fetch is recorded; a
// failed one falls back to what the snapshot had, so a flaky network does not
// erase handles that were found last time.
func (s *Snapshot) LinksFunc(fetch func(cnd.Speaker) (cnd.Links, error)) func(cnd.Speaker) cnd.Links {
	return func(sp cnd.Speaker) cnd.Links {
		if fetch != nil && sp.Slug != "" {
			if links, err := fetch(sp); err == nil {
				// A failure to save is not worth failing a render over; the
				// links are in hand and the next run records them.
				_ = s.SetLinks(sp.Key(), links)
				return links
			}
		}
		return s.speaker(sp.Key()).Links
	}
}

// speaker is the stored speaker for a key, or the zero Speaker.
func (s *Snapshot) speaker(key string) Speaker {
	if s == nil {
		return Speaker{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.speakers[key]
}

// TalkUpdatedAt is when a talk's website content last changed.
func (s *Snapshot) TalkUpdatedAt(id string) time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.talks[id].UpdatedAt
}

// SpeakerUpdatedAt is when a speaker's website content last changed.
func (s *Snapshot) SpeakerUpdatedAt(key string) time.Time { return s.speaker(key).UpdatedAt }

// UpdatedAt is when anything in the snapshot last changed.
func (s *Snapshot) UpdatedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest := s.conference.UpdatedAt
	for _, t := range s.talks {
		latest = manifest.Latest(latest, t.UpdatedAt)
	}
	for _, sp := range s.speakers {
		latest = manifest.Latest(latest, sp.UpdatedAt)
	}
	return latest
}

// Document is a talk's Source document: what the website says about it, its
// speakers inline. A bundle carries exactly this. For a talk the snapshot has
// not recorded, or on a nil snapshot, it is built from t with no times.
func (s *Snapshot) Document(t cnd.Talk) manifest.Document {
	spec := talkOf(t)
	for _, sp := range t.Speakers {
		rec := speakerOf(sp)
		stored := s.speaker(rec.Key)
		rec.Links, rec.UpdatedAt = stored.Links, stored.UpdatedAt
		spec.Speakers = append(spec.Speakers, rec)
	}
	return manifest.Doc(manifest.KindSource, manifest.Metadata{Name: t.ID, UpdatedAt: s.TalkUpdatedAt(t.ID)}, spec)
}

// save writes the snapshot. The caller must hold s.mu.
func (s *Snapshot) save() error {
	docs := []manifest.Document{manifest.Doc(kindConference,
		manifest.Metadata{Name: s.conference.Value.Domain, UpdatedAt: s.conference.UpdatedAt}, s.conference.Value)}
	for _, id := range s.order {
		rec := s.talks[id]
		t := rec.Value.Talk
		for _, key := range rec.Value.Speakers {
			t.Speakers = append(t.Speakers, s.speakers[key])
		}
		docs = append(docs, manifest.Doc(manifest.KindSource, manifest.Metadata{Name: id, UpdatedAt: rec.UpdatedAt}, t))
	}
	return manifest.WriteFile(s.path, fileHeader, docs)
}
