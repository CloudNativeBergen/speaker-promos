// Package manifest stores promo corrections as Kubernetes-style YAML documents.
//
// The data the program page gives us is not quite enough to post from: there is
// no structured employer field, so `promo post` guesses one, and a few talk
// titles are too long to read on a card. Those corrections have to live
// somewhere durable and reviewable, which means a file in git rather than
// re-typed flags.
//
// The format is a multi-document manifest — apiVersion, kind, metadata.name,
// spec — because there are two kinds of correction (speaker and talk) and that
// shape gives each its own object without inventing nesting. apiVersion and
// kind are validated rather than ignored, so a typo is an error instead of a
// silently dropped override.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/lang"
	"gopkg.in/yaml.v3"
)

// DefaultPath is the manifest a command reads when none is given.
const DefaultPath = "promos.yaml"

// APIVersion is the version every file is written with.
//
// v1alpha2 added the timestamps and revisions in Metadata, the posted date, and
// the bundle's Source and Output kinds. A v1alpha1 project manifest still
// loads, since everything it can say means the same now, and is written back as
// v1alpha2 on the next save.
const APIVersion = "promo.cloudnativedays.no/v1alpha2"

// apiVersionV1Alpha1 is the previous version, still accepted on load.
const apiVersionV1Alpha1 = "promo.cloudnativedays.no/v1alpha1"

// The kinds of object a manifest may contain.
const (
	KindSpeakerOverride = "SpeakerOverride"
	KindTalkOverride    = "TalkOverride"
	// KindSource and KindOutput are informational: a bundle's record of what
	// the website said and of what its cards and copy then used. They are
	// accepted when a manifest is loaded and then ignored, so an exported
	// bundle can be passed straight back with --manifest.
	KindSource = "Source"
	KindOutput = "Output"
	// KindTalkInfo is the v1alpha1 bundle record. It still loads, but marks
	// the file as an old bundle, which an import refuses: those pre-filled
	// every speaker with the tool's guesses, and importing that would turn
	// every guess into a confirmed correction.
	KindTalkInfo = "TalkInfo"
)

// Metadata names an object and says when it last changed.
type Metadata struct {
	// Name is a speaker key (cnd.Speaker.Key: the slug, where there is one) or
	// a talk id.
	Name string `yaml:"name"`
	// EditedAt is when an override last changed: set by `promo serve` and
	// `promo import` when — and only when — the correction itself changes.
	EditedAt time.Time `yaml:"editedAt,omitempty"`
	// UpdatedAt is when the website's data for a Source last changed, and for
	// an Output the latest of that and its overrides' EditedAt.
	UpdatedAt time.Time `yaml:"updatedAt,omitempty"`
	// Revision identifies the override a bundle was exported with, so an
	// import can tell what you edited in the bundle from what changed in the
	// project since. See ImportFrom.
	Revision string `yaml:"revision,omitempty"`
}

// metadataFields are the keys Metadata accepts, checked on load.
var metadataFields = []string{"name", "editedAt", "updatedAt", "revision"}

// Stamp is the form every timestamp is written in: UTC, to the second. Finer
// than that is noise in a file meant to be read in a diff.
func Stamp(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// SpeakerSpec corrects what is known about one speaker.
//
// These are precisely the fields `promo post` flags for checking: the employer
// and job it guessed out of free text, and the handles it scraped from a
// speaker page.
type SpeakerSpec struct {
	// Name replaces the speaker's name on the card and in the copy. The CMS is
	// where people typed their own name and accents get lost — "Aurelie" for
	// "Aurélie" — and there is nowhere else to fix that.
	//
	// The slug is NOT derived from this: it stays the speaker's identity, so
	// correcting a name does not rename the export folder or change what
	// `promo list` tells you to type.
	Name     string `yaml:"name,omitempty"`
	Employer string `yaml:"employer,omitempty"`
	Job      string `yaml:"job,omitempty"`
	// Title sets the card's role line verbatim, instead of composing it from
	// Job and Employer.
	//
	// It exists because plenty of real titles do not fit "<job> at <employer>":
	// "Maintainer, Principal Open source Architect, Co-Chair CNCF TAG
	// Infrastructure" is one from the 2026 program. Employer still drives what
	// the POST says, so setting Title alone changes the card but leaves the
	// copy guessing — set both when the guess is wrong.
	Title string `yaml:"title,omitempty"`
	// Image replaces the speaker's photo. Five of the 2026 speakers have none
	// and get a monogram instead, and a CMS photo is sometimes just bad.
	//
	// Either an http(s) URL or a path on disk; a relative path resolves against
	// the manifest's own directory, so a bundle can carry its own photo next to
	// the promo.yaml that names it.
	Image string `yaml:"image,omitempty"`
	// Links are a speaker's profiles, overriding anything scraped.
	Links cnd.Links `yaml:"links,omitempty"`
}

// A spec is empty when it corrects nothing; every field is a string, so the
// zero value says exactly that.
func (spec SpeakerSpec) empty() bool { return spec == SpeakerSpec{} }

// specFields binds each of a SpeakerSpec's fields to the name it carries in the
// manifest and on the preview server's form.
//
// Everything that walks a spec field by field goes through this table:
// trimming, the import merge, and the two compositions below. Adding a field to
// SpeakerSpec is therefore one line here rather than a hunt through half a
// dozen near-identical field listings, each of which used to be its own chance
// to forget one.
var specFields = []struct {
	name string
	of   func(*SpeakerSpec) *string
}{
	{"name", func(s *SpeakerSpec) *string { return &s.Name }},
	{"employer", func(s *SpeakerSpec) *string { return &s.Employer }},
	{"job", func(s *SpeakerSpec) *string { return &s.Job }},
	{"title", func(s *SpeakerSpec) *string { return &s.Title }},
	{"image", func(s *SpeakerSpec) *string { return &s.Image }},
	{"linkedin", func(s *SpeakerSpec) *string { return &s.Links.LinkedIn }},
	{"bluesky", func(s *SpeakerSpec) *string { return &s.Links.Bluesky }},
	{"x", func(s *SpeakerSpec) *string { return &s.Links.X }},
	{"github", func(s *SpeakerSpec) *string { return &s.Links.GitHub }},
}

// SpeakerFields are the names of a speaker's correctable fields, in the order
// the table declares them. The preview server's form inputs carry these names,
// so a submission can be read through the same table.
func SpeakerFields() []string {
	out := make([]string, 0, len(specFields))
	for _, f := range specFields {
		out = append(out, f.name)
	}
	return out
}

// Value returns one field by the name it has in the manifest, or "" when there
// is no such field.
func (spec SpeakerSpec) Value(field string) string {
	for _, f := range specFields {
		if f.name == field {
			return *f.of(&spec)
		}
	}
	return ""
}

// SetValue sets one field by the name it has in the manifest, and ignores a
// name the table does not know.
func (spec *SpeakerSpec) SetValue(field, value string) {
	for _, f := range specFields {
		if f.name == field {
			*f.of(spec) = value
			return
		}
	}
}

// combine builds a spec field by field out of two others.
func combine(a, b SpeakerSpec, pick func(a, b string) string) SpeakerSpec {
	var out SpeakerSpec
	for _, f := range specFields {
		*f.of(&out) = pick(*f.of(&a), *f.of(&b))
	}
	return out
}

// Overlay returns the spec's own values where it has them and base's elsewhere:
// the correction where one was made, the found value otherwise.
//
// It is what the preview server pre-fills its form with, so that a found value
// can be edited in place rather than retyped from a grey placeholder.
func (spec SpeakerSpec) Overlay(base SpeakerSpec) SpeakerSpec {
	return combine(spec, base, func(override, found string) string {
		if override != "" {
			return override
		}
		return found
	})
}

// Diff reduces a spec to the fields that actually differ from base, so the
// manifest records corrections and not a copy of everything the tool already
// knew.
//
// An empty field means "no opinion" rather than "make it empty": an override
// has no way to express an explicit blank, and the value simply reverts to what
// was found — which the form then shows again.
func (spec SpeakerSpec) Diff(base SpeakerSpec) SpeakerSpec {
	return combine(spec, base, func(value, found string) string {
		if value == "" || value == found {
			return ""
		}
		return value
	})
}

// TalkSpec overrides how a talk is presented, and records what has been done
// with it.
type TalkSpec struct {
	// Language forces the draft copy's language when detection gets it wrong,
	// which a bilingual title will: "en" or "no", empty to auto-detect.
	Language string `yaml:"language,omitempty"`
	// DisplayTitle replaces the title on the card and in the copy, for titles
	// too long to read at card size.
	DisplayTitle string `yaml:"displayTitle,omitempty"`
	// Hidden excludes the talk from bulk operations: a cancelled session, or
	// one whose promo has already gone out.
	Hidden bool `yaml:"hidden,omitempty"`
	// Posted is the date the promo went out, "YYYY-MM-DD", and empty while it
	// has not. It is a record kept by hand rather than anything the tool acts
	// on: posting stays manual.
	Posted string `yaml:"posted,omitempty"`
}

func (t TalkSpec) empty() bool { return t == TalkSpec{} }

// talkFields binds each of a TalkSpec's fields to its manifest name, as
// specFields does for a speaker, so the import can walk and report them.
var talkFields = []struct {
	name string
	get  func(TalkSpec) string
	set  func(*TalkSpec, string)
}{
	{"displayTitle", func(t TalkSpec) string { return t.DisplayTitle }, func(t *TalkSpec, v string) { t.DisplayTitle = v }},
	{"language", func(t TalkSpec) string { return t.Language }, func(t *TalkSpec, v string) { t.Language = v }},
	{"hidden", func(t TalkSpec) string {
		if t.Hidden {
			return "true"
		}
		return ""
	}, func(t *TalkSpec, v string) { t.Hidden = v == "true" }},
	{"posted", func(t TalkSpec) string { return t.Posted }, func(t *TalkSpec, v string) { t.Posted = v }},
}

// ParsePosted validates a posted date. Empty means not posted.
func ParsePosted(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return "", fmt.Errorf("posted %q is not a date (want YYYY-MM-DD)", s)
	}
	return s, nil
}

func trimTalk(t TalkSpec) TalkSpec {
	t.DisplayTitle = strings.TrimSpace(t.DisplayTitle)
	t.Language = strings.TrimSpace(t.Language)
	t.Posted = strings.TrimSpace(t.Posted)
	return t
}

// Revision is a short content hash of an override spec. A bundle records the
// revision each override had when it was exported, which is what lets an
// import tell your edit to the bundle apart from a later edit to the project.
func Revision(spec any) string {
	b, err := yaml.Marshal(spec)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:6])
}

// entry is one stored override and when it last changed.
type entry[T any] struct {
	Spec     T
	EditedAt time.Time
}

// ref names one object in a Set.
type ref struct{ Kind, Name string }

// Set is a loaded manifest.
//
// A Set owns both the overrides and the file they came from, and every mutating
// method persists before returning — there is no unsaved state in this design,
// so there is nowhere for the two to drift apart. That also makes a Set safe to
// share between concurrent HTTP handlers, which is what the preview server
// does.
type Set struct {
	mu       sync.Mutex
	path     string
	speakers map[string]entry[SpeakerSpec]
	talks    map[string]entry[TalkSpec]
	// revisions are the ones a bundle's overrides were exported with, read on
	// load. Only an import consults them.
	revisions map[ref]string
	// legacy is set when the file holds a v1alpha1 bundle record.
	legacy bool
	// bundleTalk is the talk id a bundle's Source names, or "" for a file that
	// is not a bundle.
	bundleTalk string

	// Now is the clock edits are stamped with; nil means time.Now. Tests set
	// it to get reproducible files.
	Now func() time.Time
}

// New returns an empty Set that will save to path.
func New(path string) *Set {
	if path == "" {
		path = DefaultPath
	}
	return &Set{
		path:      path,
		speakers:  map[string]entry[SpeakerSpec]{},
		talks:     map[string]entry[TalkSpec]{},
		revisions: map[ref]string{},
	}
}

func (s *Set) now() time.Time {
	if s.Now != nil {
		return Stamp(s.Now())
	}
	return Stamp(time.Now())
}

// Path is the file this Set saves to.
func (s *Set) Path() string { return s.path }

// BundleTalk is the talk a bundle was exported for, or "" when the file is not
// a bundle.
func (s *Set) BundleTalk() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bundleTalk
}

// Load reads a manifest. A missing file yields an empty Set and no error: the
// path has a default and most runs start without one.
func Load(path string) (*Set, error) {
	set := New(path)
	f, err := os.Open(set.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return set, nil
		}
		return nil, fmt.Errorf("reading %s: %w", set.path, err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	for i := 0; ; i++ {
		// Each document is decoded into a Node first so that every error can
		// name the line it is on. Hand-edited config is miserable precisely
		// when it fails without saying where.
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parsing %s: %w", set.path, err)
		}
		if err := set.addDocument(&node); err != nil {
			return nil, fmt.Errorf("%s: %w", set.path, err)
		}
	}
	return set, nil
}

// addDocument validates one manifest document and records its override.
func (s *Set) addDocument(node *yaml.Node) error {
	// A document node wraps its single content node; an empty document (a
	// stray "---" or a trailing separator) has none.
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		node = node.Content[0]
	}
	if node.Kind == 0 || node.Tag == "!!null" {
		return nil
	}

	var doc struct {
		APIVersion string    `yaml:"apiVersion"`
		Kind       string    `yaml:"kind"`
		Metadata   Metadata  `yaml:"metadata"`
		Spec       yaml.Node `yaml:"spec"`
	}
	if err := node.Decode(&doc); err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	if err := checkFields(node, "apiVersion", "kind", "metadata", "spec"); err != nil {
		return err
	}

	if doc.APIVersion != APIVersion && doc.APIVersion != apiVersionV1Alpha1 {
		return fmt.Errorf("line %d: unsupported apiVersion %q (want %q)", node.Line, doc.APIVersion, APIVersion)
	}
	if err := checkFields(field(node, "metadata"), metadataFields...); err != nil {
		return err
	}
	if doc.Metadata.Name == "" {
		return fmt.Errorf("line %d: %s needs a metadata.name (a speaker key or talk id)", node.Line, doc.Kind)
	}
	if rev := doc.Metadata.Revision; rev != "" {
		s.revisions[ref{doc.Kind, doc.Metadata.Name}] = rev
	}
	editedAt := doc.Metadata.EditedAt
	if !editedAt.IsZero() {
		editedAt = Stamp(editedAt)
	}

	switch doc.Kind {
	case KindSpeakerOverride:
		if err := checkFields(&doc.Spec, "name", "employer", "job", "title", "image", "links"); err != nil {
			return err
		}
		var spec SpeakerSpec
		if err := doc.Spec.Decode(&spec); err != nil {
			return fmt.Errorf("line %d: %s/%s: %w", doc.Spec.Line, doc.Kind, doc.Metadata.Name, err)
		}
		if links := field(&doc.Spec, "links"); links != nil {
			if err := checkFields(links, "linkedin", "bluesky", "x", "github"); err != nil {
				return err
			}
		}
		if _, dup := s.speakers[doc.Metadata.Name]; dup {
			return fmt.Errorf("line %d: duplicate %s for %q", node.Line, doc.Kind, doc.Metadata.Name)
		}
		s.speakers[doc.Metadata.Name] = entry[SpeakerSpec]{spec, editedAt}
	case KindTalkOverride:
		if err := checkFields(&doc.Spec, "displayTitle", "hidden", "language", "posted"); err != nil {
			return err
		}
		var spec TalkSpec
		if err := doc.Spec.Decode(&spec); err != nil {
			return fmt.Errorf("line %d: %s/%s: %w", doc.Spec.Line, doc.Kind, doc.Metadata.Name, err)
		}
		// Validated on load so a typo is an error here rather than silently
		// falling back to English in every generated post.
		if _, err := lang.ParseLanguage(spec.Language); err != nil {
			return fmt.Errorf("line %d: %s/%s: %w", doc.Spec.Line, doc.Kind, doc.Metadata.Name, err)
		}
		if _, err := ParsePosted(spec.Posted); err != nil {
			return fmt.Errorf("line %d: %s/%s: %w", doc.Spec.Line, doc.Kind, doc.Metadata.Name, err)
		}
		if _, dup := s.talks[doc.Metadata.Name]; dup {
			return fmt.Errorf("line %d: duplicate %s for %q", node.Line, doc.Kind, doc.Metadata.Name)
		}
		s.talks[doc.Metadata.Name] = entry[TalkSpec]{trimTalk(spec), editedAt}
	case KindSource, KindOutput:
		if doc.Kind == KindSource {
			s.bundleTalk = doc.Metadata.Name
		}
		// Informational, and deliberately not field-checked: they are a record
		// of what an export contained, so they may grow fields that an older
		// binary has never heard of. Rejecting those would make bundles from a
		// newer version unloadable for no benefit.
	case KindTalkInfo:
		s.legacy = true
		s.bundleTalk = doc.Metadata.Name
	default:
		return fmt.Errorf("line %d: unknown kind %q (want %s, %s, %s or %s)",
			node.Line, doc.Kind, KindSpeakerOverride, KindTalkOverride, KindSource, KindOutput)
	}
	return nil
}

// checkFields rejects keys a mapping does not recognise.
//
// yaml.v3 offers Decoder.KnownFields for this, but it cannot be used here: the
// spec is decoded from an already-parsed Node, and a strict stream decoder
// would reject the spec of the *other* kind. Checking the keys directly also
// means the error can point at the offending line rather than the document.
func checkFields(n *yaml.Node, allowed ...string) error {
	if n == nil || n.Kind == 0 || n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expected a mapping with keys %s", n.Line, strings.Join(allowed, ", "))
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if !slices.Contains(allowed, key.Value) {
			return fmt.Errorf("line %d: unknown field %q (known fields: %s)",
				key.Line, key.Value, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// field returns the value node for a key in a mapping, or nil.
func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// Speaker returns the override for a speaker key.
func (s *Set) Speaker(key string) (SpeakerSpec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.speakers[key]
	return e.Spec, ok
}

// Talk returns the override for a talk id.
func (s *Set) Talk(id string) (TalkSpec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.talks[id]
	return e.Spec, ok
}

// EditedAt is when an override last changed, or the zero time when it has no
// stamp — never edited, or written by hand without one.
func (s *Set) EditedAt(kind, name string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case KindSpeakerOverride:
		return s.speakers[name].EditedAt
	case KindTalkOverride:
		return s.talks[name].EditedAt
	}
	return time.Time{}
}

// Len reports how many overrides of each kind are loaded.
func (s *Set) Len() (speakers, talks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.speakers), len(s.talks)
}

// SetSpeaker records a speaker override and saves the manifest.
//
// An override that sets nothing is deleted rather than written as an empty
// object, so clearing a field in the preview server leaves the manifest as
// clean as it was before the edit. The edit is stamped only if it changed
// something, so saving a form unchanged leaves the file byte-identical.
func (s *Set) SetSpeaker(key string, spec SpeakerSpec) error {
	spec = trimSpeaker(spec)
	s.mu.Lock()
	defer s.mu.Unlock()
	putEntry(s.speakers, key, spec, s.now())
	return s.save()
}

// SetTalk records a talk override and saves the manifest.
func (s *Set) SetTalk(id string, spec TalkSpec) error {
	spec = trimTalk(spec)
	s.mu.Lock()
	defer s.mu.Unlock()
	putEntry(s.talks, id, spec, s.now())
	return s.save()
}

// putEntry stores spec under name, stamping it with now if it changed, and
// deleting it when it is empty. It reports whether anything changed.
func putEntry[T comparable](m map[string]entry[T], name string, spec T, now time.Time) bool {
	var zero T
	cur, had := m[name]
	switch {
	case spec == zero:
		delete(m, name)
		return had
	case had && cur.Spec == spec:
		return false
	default:
		m[name] = entry[T]{spec, now}
		return true
	}
}

// trimSpeaker trims every field, since these arrive either from a browser form
// or from a file someone edited by hand.
func trimSpeaker(spec SpeakerSpec) SpeakerSpec {
	for _, f := range specFields {
		p := f.of(&spec)
		*p = strings.TrimSpace(*p)
	}
	return spec
}

// Save writes the manifest to its path.
func (s *Set) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save()
}

const fileHeader = `# Promo overrides for Cloud Native Days.
#
# Written by "promo serve" and read by "promo export" and "promo post". Safe to
# edit by hand and meant to be committed: this is the record of every
# correction made to the guessed employers and the titles that were too long.
`

// save writes the manifest. The caller must hold s.mu.
func (s *Set) save() error {
	if s.path == "" {
		return errors.New("manifest has no path to save to")
	}
	data, err := Encode(fileHeader, s.documents())
	if err != nil {
		return fmt.Errorf("encoding %s: %w", s.path, err)
	}
	if err := WriteAtomic(s.path, data); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	return nil
}

// Encode renders manifest documents as a YAML file led by a header comment.
// It is shared by the project manifest, the website snapshot and the per-talk
// bundles, which differ only in their header and their documents.
func Encode(header string, docs []Document) ([]byte, error) {
	var b strings.Builder
	b.WriteString(header)
	// A yaml.Encoder that is closed without having written a document errors,
	// and an empty manifest is a normal state: every override can be cleared.
	if len(docs) > 0 {
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		for _, doc := range docs {
			if err := enc.Encode(doc); err != nil {
				return nil, err
			}
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
	}
	return []byte(b.String()), nil
}

// WriteAtomic writes data through a temporary file in the same directory, so an
// interrupted save cannot truncate a manifest that holds an afternoon of
// corrections.
func WriteAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Document is one object as it appears on disk.
type Document struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       any      `yaml:"spec"`
}

// Doc builds a document of the current apiVersion.
func Doc(kind string, meta Metadata, spec any) Document {
	return Document{APIVersion, kind, meta, spec}
}

// documents renders the whole Set, sorted by kind then name so that the file
// stays diff-stable: an edit to one speaker should show up as a change to one
// object, not a reshuffle of the file. The caller must hold s.mu.
func (s *Set) documents() []Document {
	out := make([]Document, 0, len(s.speakers)+len(s.talks))
	for _, name := range slices.Sorted(maps.Keys(s.speakers)) {
		e := s.speakers[name]
		if e.Spec.empty() {
			continue
		}
		out = append(out, Doc(KindSpeakerOverride, Metadata{Name: name, EditedAt: e.EditedAt}, e.Spec))
	}
	for _, name := range slices.Sorted(maps.Keys(s.talks)) {
		e := s.talks[name]
		if e.Spec.empty() {
			continue
		}
		out = append(out, Doc(KindTalkOverride, Metadata{Name: name, EditedAt: e.EditedAt}, e.Spec))
	}
	return out
}

// BundleOverrides renders the overrides a bundle carries for one talk: one
// TalkOverride and one SpeakerOverride per speaker key, in that order.
//
// Each is written even when it corrects nothing — as `spec: {}` — so a bundle
// always has the object to type a correction into, and each carries the
// revision it was exported at, which is what ImportFrom merges against.
func (s *Set) BundleOverrides(talkID string, speakerKeys []string) []Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.talks[talkID]
	out := []Document{Doc(KindTalkOverride,
		Metadata{Name: talkID, EditedAt: t.EditedAt, Revision: Revision(t.Spec)}, t.Spec)}
	for _, key := range speakerKeys {
		sp := s.speakers[key]
		out = append(out, Doc(KindSpeakerOverride,
			Metadata{Name: key, EditedAt: sp.EditedAt, Revision: Revision(sp.Spec)}, sp.Spec))
	}
	return out
}
