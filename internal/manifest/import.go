package manifest

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ErrLegacyBundle is returned when importing a v1alpha1 bundle.
var ErrLegacyBundle = errors.New("exported by an older version of promo, which pre-filled " +
	"every guess as if it were a correction — re-export and edit the new bundle")

// Change is one field an import alters, or would have altered but for a
// conflict.
type Change struct {
	Kind  string
	Name  string
	Field string
	// From is the project manifest's current value, empty when it had none.
	From string
	// To is the bundle's value; empty means the bundle cleared the field.
	To string
	// Conflict marks a change that was NOT applied: the project changed this
	// object after the bundle was exported, and the bundle changed it too.
	Conflict bool
}

// String renders a change for a report line.
func (c Change) String() string {
	var what string
	switch {
	case c.To == "":
		what = fmt.Sprintf("%q → (cleared)", c.From)
	case c.From == "":
		what = fmt.Sprintf("%q", c.To)
	default:
		what = fmt.Sprintf("%q → %q", c.From, c.To)
	}
	line := fmt.Sprintf("%s/%s %s: %s", c.Kind, c.Name, c.Field, what)
	if c.Conflict {
		line = "conflict, not applied: " + line
	}
	return line
}

// ImportOptions controls a merge.
type ImportOptions struct {
	// Force takes the bundle's version of a conflicting object rather than
	// keeping the project's.
	Force bool
	// DryRun computes the changes without writing anything.
	DryRun bool
}

// ImportFrom merges another manifest's overrides into this one.
//
// A bundle records the revision each override had when it was exported, which
// makes this a three-way merge rather than a guess. For each object, with base
// the exported revision, theirs the bundle's spec and ours the project's:
//
//   - theirs is still base: nobody edited it in the bundle, so nothing to take.
//   - ours equals theirs: already in agreement.
//   - ours is still base: only the bundle changed, so it is taken whole —
//     cleared fields and `hidden: false` included, since the bundle carried the
//     entire object and a missing field is one you deleted.
//   - otherwise both changed, which is a conflict. It is reported and the
//     project's version kept, unless Force.
//
// An object with no revision was written by hand rather than exported, so
// there is no base to compare with; only the fields it sets are taken.
//
// The whole merge happens under one lock and is written once, so an import
// either lands completely or not at all.
func (s *Set) ImportFrom(src *Set, opts ImportOptions) ([]Change, error) {
	if src == nil {
		return nil, nil
	}
	src.mu.Lock()
	if src.legacy {
		src.mu.Unlock()
		return nil, ErrLegacyBundle
	}
	theirSpeakers, theirTalks := maps.Clone(src.speakers), maps.Clone(src.talks)
	revisions := maps.Clone(src.revisions)
	src.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	var changes []Change
	dirty := false
	for _, key := range slices.Sorted(maps.Keys(theirSpeakers)) {
		theirs := trimSpeaker(theirSpeakers[key].Spec)
		ours := s.speakers[key].Spec
		merged, applied, cs := merge3(ours, theirs, revisions[ref{KindSpeakerOverride, key}], opts.Force,
			func(a, b SpeakerSpec) []fieldDiff {
				var out []fieldDiff
				for _, f := range specFields {
					if x, y := *f.of(&a), *f.of(&b); x != y {
						out = append(out, fieldDiff{f.name, x, y})
					}
				}
				return out
			},
			func(ours, theirs SpeakerSpec) SpeakerSpec {
				for _, f := range specFields {
					if v := *f.of(&theirs); v != "" {
						*f.of(&ours) = v
					}
				}
				return ours
			})
		changes = append(changes, tag(cs, KindSpeakerOverride, key)...)
		if applied && !opts.DryRun && putEntry(s.speakers, key, merged, now) {
			dirty = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(theirTalks)) {
		theirs := trimTalk(theirTalks[id].Spec)
		ours := s.talks[id].Spec
		merged, applied, cs := merge3(ours, theirs, revisions[ref{KindTalkOverride, id}], opts.Force,
			func(a, b TalkSpec) []fieldDiff {
				var out []fieldDiff
				for _, f := range talkFields {
					if x, y := f.get(a), f.get(b); x != y {
						out = append(out, fieldDiff{f.name, x, y})
					}
				}
				return out
			},
			func(ours, theirs TalkSpec) TalkSpec {
				for _, f := range talkFields {
					if v := f.get(theirs); v != "" {
						f.set(&ours, v)
					}
				}
				return ours
			})
		changes = append(changes, tag(cs, KindTalkOverride, id)...)
		if applied && !opts.DryRun && putEntry(s.talks, id, merged, now) {
			dirty = true
		}
	}

	if !dirty {
		return changes, nil
	}
	return changes, s.save()
}

// fieldDiff is one field that differs between two specs.
type fieldDiff struct{ field, from, to string }

// merge3 decides one object's merge; see ImportFrom. It returns the spec to
// store, whether to store it, and the field changes to report.
func merge3[T comparable](ours, theirs T, base string, force bool,
	diff func(a, b T) []fieldDiff, overlay func(ours, theirs T) T) (T, bool, []Change) {
	report := func(merged T, conflict bool) []Change {
		var out []Change
		for _, d := range diff(ours, merged) {
			out = append(out, Change{Field: d.field, From: d.from, To: d.to, Conflict: conflict})
		}
		return out
	}

	if base == "" {
		merged := overlay(ours, theirs)
		return merged, merged != ours, report(merged, false)
	}
	switch {
	case Revision(theirs) == base, ours == theirs:
		return ours, false, nil
	case Revision(ours) == base, force:
		return theirs, true, report(theirs, false)
	default:
		return ours, false, report(theirs, true)
	}
}

func tag(cs []Change, kind, name string) []Change {
	for i := range cs {
		cs[i].Kind, cs[i].Name = kind, name
	}
	return cs
}

// Speakers returns every speaker override, keyed by speaker key. The map is a
// copy.
func (s *Set) Speakers() map[string]SpeakerSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]SpeakerSpec, len(s.speakers))
	for k, e := range s.speakers {
		out[k] = e.Spec
	}
	return out
}

// Talks returns every talk override, keyed by talk id. The map is a copy.
func (s *Set) Talks() map[string]TalkSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]TalkSpec, len(s.talks))
	for k, e := range s.talks {
		out[k] = e.Spec
	}
	return out
}

// SortChanges orders changes for a stable report: applied ones first, then
// conflicts, each by kind, name and field.
func SortChanges(changes []Change) {
	slices.SortFunc(changes, func(a, b Change) int {
		ca, cb := 0, 0
		if a.Conflict {
			ca = 1
		}
		if b.Conflict {
			cb = 1
		}
		return cmp.Or(
			cmp.Compare(ca, cb),
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.Field, b.Field),
		)
	})
}
