package manifest

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Change is one field an import would alter.
type Change struct {
	Kind  string
	Name  string
	Field string
	// From is the project manifest's current value, empty when it had none.
	From string
	To   string
}

// String renders a change for a report line.
func (c Change) String() string {
	if c.From == "" {
		return fmt.Sprintf("%s/%s %s: %q", c.Kind, c.Name, c.Field, c.To)
	}
	return fmt.Sprintf("%s/%s %s: %q → %q", c.Kind, c.Name, c.Field, c.From, c.To)
}

// ImportOptions controls a merge.
type ImportOptions struct {
	// SpeakerBaseline reports what the tool would produce for a speaker with no
	// override at all. It is what makes an import mean "take the edits": an
	// exported manifest pre-fills the name, employer and job, so importing it
	// verbatim would turn every guess into a confirmed correction and silence
	// the warnings that exist to be read.
	SpeakerBaseline func(key string) (SpeakerSpec, bool)
	// TalkBaseline reports a talk's submitted title, so a displayTitle that
	// merely repeats it is not mistaken for a shortening.
	TalkBaseline func(id string) (title string, ok bool)
	// ConfirmGuesses imports the pre-filled values too, which is how you say
	// "yes, that guess was right" and stop being asked.
	ConfirmGuesses bool
	// DryRun computes the changes without writing anything.
	DryRun bool
}

// ImportFrom merges another manifest's overrides into this one.
//
// The whole merge happens under one lock and is written once, so an import
// either lands completely or not at all — a half-merged manifest would be worse
// than a failed one, since the point of the file is to be trustworthy.
func (s *Set) ImportFrom(src *Set, opts ImportOptions) ([]Change, error) {
	if src == nil {
		return nil, nil
	}
	incoming := src.Speakers()
	incomingTalks := src.Talks()

	s.mu.Lock()
	defer s.mu.Unlock()

	var changes []Change
	for _, key := range slices.Sorted(maps.Keys(incoming)) {
		spec := trimSpeaker(incoming[key])
		current := s.speakers[key]

		var base SpeakerSpec
		if opts.SpeakerBaseline != nil && !opts.ConfirmGuesses {
			if b, ok := opts.SpeakerBaseline(key); ok {
				base = trimSpeaker(b)
			}
		}

		merged := current
		for _, f := range specFields {
			in, was, guess := *f.of(&spec), *f.of(&current), *f.of(&base)
			switch {
			// Unset in the import: nothing said, so nothing changes. Clearing a
			// field is done by editing the project manifest, not by omitting it
			// from a bundle — half the bundles would otherwise clear whatever
			// they happened not to mention.
			case in == "":
				continue
			// Equal to the baseline: the pre-filled guess came back unedited.
			case in == guess:
				continue
			// Already what the project manifest says: nothing to record.
			case in == was:
				continue
			}
			changes = append(changes, Change{
				Kind: KindSpeakerOverride, Name: key, Field: f.name,
				From: was, To: in,
			})
			*f.of(&merged) = in
		}
		if merged != current && !opts.DryRun {
			if merged = trimSpeaker(merged); merged.empty() {
				delete(s.speakers, key)
			} else {
				s.speakers[key] = merged
			}
		}
	}

	for _, id := range slices.Sorted(maps.Keys(incomingTalks)) {
		spec := incomingTalks[id]
		current := s.talks[id]
		merged := current

		title := strings.TrimSpace(spec.DisplayTitle)
		if title != "" && title != current.DisplayTitle {
			submitted := ""
			if opts.TalkBaseline != nil {
				if t, ok := opts.TalkBaseline(id); ok {
					submitted = t
				}
			}
			if title != submitted {
				changes = append(changes, Change{
					Kind: KindTalkOverride, Name: id, Field: "displayTitle",
					From: current.DisplayTitle, To: title,
				})
				merged.DisplayTitle = title
			}
		}
		if lang := strings.TrimSpace(spec.Language); lang != "" && lang != current.Language {
			changes = append(changes, Change{
				Kind: KindTalkOverride, Name: id, Field: "language",
				From: current.Language, To: lang,
			})
			merged.Language = lang
		}
		// Hidden is a bool, so "unset" and "false" are indistinguishable in the
		// file. Only turning it ON is importable; unhiding is done in the
		// project manifest or the browser, where the intent is unambiguous.
		if spec.Hidden && !current.Hidden {
			changes = append(changes, Change{
				Kind: KindTalkOverride, Name: id, Field: "hidden",
				From: "false", To: "true",
			})
			merged.Hidden = true
		}

		if merged != current && !opts.DryRun {
			if merged.empty() {
				delete(s.talks, id)
			} else {
				s.talks[id] = merged
			}
		}
	}

	if len(changes) == 0 || opts.DryRun {
		return changes, nil
	}
	if err := s.save(); err != nil {
		return changes, err
	}
	return changes, nil
}

// Speakers returns every speaker override, keyed by speaker key. The map is a copy.
func (s *Set) Speakers() map[string]SpeakerSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.speakers)
}

// Talks returns every talk override, keyed by talk id. The map is a copy.
func (s *Set) Talks() map[string]TalkSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.talks)
}

// SortChanges orders changes for a stable report.
func SortChanges(changes []Change) {
	slices.SortFunc(changes, func(a, b Change) int {
		return cmp.Or(
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.Field, b.Field),
		)
	})
}
