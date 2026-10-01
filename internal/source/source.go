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
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"gopkg.in/yaml.v3"
)

// DefaultPath is the snapshot a command keeps when none is given.
const DefaultPath = "source.yaml"

// KindConference is the snapshot's conference document. Each talk is a
// manifest.KindSource document, the same one a bundle carries.
const KindConference = "Conference"

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

// talkRecord is a stored talk: its own fields and the keys of its speakers.
type talkRecord struct {
	ID        string
	Fields    talkFields
	Topics    []string
	Speakers  []string
	UpdatedAt time.Time
}

// talkFields are the parts of a talk that decide whether it changed.
type talkFields struct {
	Title, Abstract, Format, Level, Status string
	Topics                                 string // joined, to keep this comparable
	Schedule                               cnd.Slot
}

// Snapshot is a loaded snapshot. It is safe for concurrent use.
type Snapshot struct {
	mu         sync.Mutex
	path       string
	conference Conference
	confAt     time.Time
	talks      map[string]talkRecord
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
	return &Snapshot{path: path, talks: map[string]talkRecord{}, speakers: map[string]Speaker{}}
}

// Path is the file this snapshot saves to.
func (s *Snapshot) Path() string { return s.path }

func (s *Snapshot) now() time.Time {
	if s.Now != nil {
		return manifest.Stamp(s.Now())
	}
	return manifest.Stamp(time.Now())
}

// Load reads a snapshot. A missing file is an empty snapshot: the first run
// creates it.
func Load(path string) (*Snapshot, error) {
	snap := New(path)
	f, err := os.Open(snap.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snap, nil
		}
		return nil, fmt.Errorf("reading %s: %w", snap.path, err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	for {
		var doc struct {
			Kind     string            `yaml:"kind"`
			Metadata manifest.Metadata `yaml:"metadata"`
			Spec     yaml.Node         `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parsing %s: %w", snap.path, err)
		}
		switch doc.Kind {
		case KindConference:
			if err := doc.Spec.Decode(&snap.conference); err != nil {
				return nil, fmt.Errorf("%s: line %d: %w", snap.path, doc.Spec.Line, err)
			}
			snap.confAt = doc.Metadata.UpdatedAt
		case manifest.KindSource:
			var t Talk
			if err := doc.Spec.Decode(&t); err != nil {
				return nil, fmt.Errorf("%s: line %d: %w", snap.path, doc.Spec.Line, err)
			}
			rec := talkRecord{ID: doc.Metadata.Name, Fields: fieldsOf(t), Topics: t.Topics, UpdatedAt: doc.Metadata.UpdatedAt}
			for _, sp := range t.Speakers {
				rec.Speakers = append(rec.Speakers, sp.Key)
				snap.speakers[sp.Key] = sp
			}
			snap.talks[rec.ID] = rec
			snap.order = append(snap.order, rec.ID)
		}
	}
	return snap, nil
}

func fieldsOf(t Talk) talkFields {
	return talkFields{
		Title: t.Title, Abstract: t.Abstract, Format: t.Format, Level: t.Level, Status: t.Status,
		Topics: fmt.Sprintf("%q", t.Topics), Schedule: t.Schedule,
	}
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

// sameSpeaker compares everything but the time.
func sameSpeaker(a, b Speaker) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	return a == b
}

// Reconcile records a freshly loaded program, stamping whatever changed, and
// saves the snapshot if anything did. It reports whether anything changed.
//
// Links are not part of the program page; a speaker keeps the links the
// snapshot already has until SetLinks says otherwise.
func (s *Snapshot) Reconcile(p *cnd.Program) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	changed := false

	conf := Conference{
		Title: p.Conference.Title, StartDate: p.Conference.StartDate, EndDate: p.Conference.EndDate,
		City: p.Conference.City, Country: p.Conference.Country, Domain: p.Conference.Domain,
	}
	if conf != s.conference || s.confAt.IsZero() {
		s.conference, s.confAt, changed = conf, now, true
	}

	talks := map[string]talkRecord{}
	speakers := map[string]Speaker{}
	var order []string
	for _, t := range p.Talks {
		rec := talkRecord{ID: t.ID, Fields: fieldsOf(talkOf(t)), Topics: t.Topics}
		for _, sp := range t.Speakers {
			fresh := speakerOf(sp)
			rec.Speakers = append(rec.Speakers, fresh.Key)
			if _, done := speakers[fresh.Key]; done {
				continue
			}
			old, had := s.speakers[fresh.Key]
			fresh.Links = old.Links
			if had && sameSpeaker(old, fresh) {
				fresh.UpdatedAt = old.UpdatedAt
			} else {
				fresh.UpdatedAt, changed = now, true
			}
			speakers[fresh.Key] = fresh
		}
		old, had := s.talks[t.ID]
		if had && old.Fields == rec.Fields && slices.Equal(old.Speakers, rec.Speakers) {
			rec.UpdatedAt = old.UpdatedAt
		} else {
			rec.UpdatedAt, changed = now, true
		}
		talks[t.ID] = rec
		order = append(order, t.ID)
	}
	if len(talks) != len(s.talks) || len(speakers) != len(s.speakers) || !slices.Equal(order, s.order) {
		changed = true
	}
	s.talks, s.speakers, s.order = talks, speakers, order

	if !changed {
		return false, nil
	}
	return true, s.save()
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
	sp.Links, sp.UpdatedAt = links, s.now()
	s.speakers[key] = sp
	return s.save()
}

// Links returns the links the snapshot has for a speaker.
func (s *Snapshot) Links(key string) cnd.Links {
	if s == nil {
		return cnd.Links{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.speakers[key].Links
}

// LinksFunc is the resolver's Links callback over this snapshot.
//
// fetch scrapes a speaker's page; nil means do not fetch (--no-links), and the
// snapshot's links are used as they stand. A successful fetch is recorded; a
// failed one falls back to what the snapshot had, so a flaky network does not
// erase handles that were found last time.
func (s *Snapshot) LinksFunc(fetch func(cnd.Speaker) (cnd.Links, error)) func(cnd.Speaker) cnd.Links {
	return func(sp cnd.Speaker) cnd.Links {
		key := sp.Key()
		if fetch == nil || sp.Slug == "" {
			return s.Links(key)
		}
		links, err := fetch(sp)
		if err != nil {
			return s.Links(key)
		}
		// A failure to save is not worth failing a render over; the links are
		// in hand and the next run records them.
		_ = s.SetLinks(key, links)
		return links
	}
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
func (s *Snapshot) SpeakerUpdatedAt(key string) time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.speakers[key].UpdatedAt
}

// UpdatedAt is when anything in the snapshot last changed.
func (s *Snapshot) UpdatedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest := s.confAt
	for _, t := range s.talks {
		latest = later(latest, t.UpdatedAt)
	}
	for _, sp := range s.speakers {
		latest = later(latest, sp.UpdatedAt)
	}
	return latest
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Document is a talk's Source document: what the website says about it, its
// speakers inline. A bundle carries exactly this. On a nil snapshot, or for a
// talk the snapshot has not recorded, it is built from t with no times.
func (s *Snapshot) Document(t cnd.Talk) manifest.Document {
	spec := talkOf(t)
	var updatedAt time.Time
	if s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		updatedAt = s.talks[t.ID].UpdatedAt
	}
	for _, sp := range t.Speakers {
		rec := speakerOf(sp)
		if s != nil {
			if stored, ok := s.speakers[rec.Key]; ok {
				rec.Links, rec.UpdatedAt = stored.Links, stored.UpdatedAt
			}
		}
		spec.Speakers = append(spec.Speakers, rec)
	}
	return manifest.Doc(manifest.KindSource, manifest.Metadata{Name: t.ID, UpdatedAt: updatedAt}, spec)
}

// save writes the snapshot. The caller must hold s.mu.
func (s *Snapshot) save() error {
	docs := []manifest.Document{manifest.Doc(KindConference,
		manifest.Metadata{Name: s.conference.Domain, UpdatedAt: s.confAt}, s.conference)}
	ids := s.order
	if len(ids) != len(s.talks) {
		ids = slices.Sorted(maps.Keys(s.talks))
	}
	for _, id := range ids {
		rec := s.talks[id]
		var t Talk
		t.Title, t.Abstract, t.Format, t.Level, t.Status = rec.Fields.Title, rec.Fields.Abstract,
			rec.Fields.Format, rec.Fields.Level, rec.Fields.Status
		t.Schedule, t.Topics = rec.Fields.Schedule, rec.Topics
		for _, key := range rec.Speakers {
			t.Speakers = append(t.Speakers, s.speakers[key])
		}
		docs = append(docs, manifest.Doc(manifest.KindSource,
			manifest.Metadata{Name: id, UpdatedAt: rec.UpdatedAt}, t))
	}
	data, err := manifest.Encode(fileHeader, docs)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", s.path, err)
	}
	if err := manifest.WriteAtomic(s.path, data); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	return nil
}
