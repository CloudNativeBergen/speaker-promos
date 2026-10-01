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
		merged, apply, cs := merge3(speakerFields, s.speakers[key].Spec, theirSpeakers[key].Spec,
			revisions[ref{KindSpeakerOverride, key}], opts.Force)
		changes = append(changes, tag(cs, KindSpeakerOverride, key)...)
		if apply && !opts.DryRun && putEntry(s.speakers, key, merged, now) {
			dirty = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(theirTalks)) {
		merged, apply, cs := merge3(talkFields, s.talks[id].Spec, theirTalks[id].Spec,
			revisions[ref{KindTalkOverride, id}], opts.Force)
		changes = append(changes, tag(cs, KindTalkOverride, id)...)
		if apply && !opts.DryRun && putEntry(s.talks, id, merged, now) {
			dirty = true
		}
	}

	if !dirty {
		return changes, nil
	}
	return changes, s.save()
}

// merge3 decides one object's merge; see ImportFrom. It returns the spec to
// store, whether to store it, and the field changes to report.
func merge3[T comparable](fs fields[T], ours, theirs T, base string, force bool) (T, bool, []Change) {
	theirs = fs.trim(theirs)
	report := func(merged T, conflict bool) []Change {
		var out []Change
		for _, d := range fs.diffs(ours, merged) {
			out = append(out, Change{Field: d.field, From: d.from, To: d.to, Conflict: conflict})
		}
		return out
	}
	switch {
	case base == "":
		// Hand-written, so there is no base: take only what it sets.
		merged := fs.overlay(theirs, ours)
		return merged, merged != ours, report(merged, false)
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
