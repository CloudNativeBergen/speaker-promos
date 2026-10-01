package cnd

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Find returns the talks matching a selector.
//
// Selectors are resolved most-specific first so that a precise identifier is
// never ambiguous: talk id prefix, then speaker slug, then a case-insensitive
// substring of the talk title, then of a speaker name. The first tier that
// matches anything wins, which is why `promo export dario-haaland` and
// `promo export "pods on mars"` both do the obvious thing.
func (p Program) Find(selector string) []Talk {
	q := strings.ToLower(strings.TrimSpace(selector))
	if q == "" {
		return nil
	}

	tiers := []func(Talk) bool{
		func(t Talk) bool { return strings.HasPrefix(strings.ToLower(t.ID), q) },
		func(t Talk) bool {
			for _, sp := range t.Speakers {
				if strings.EqualFold(sp.Slug, q) {
					return true
				}
			}
			return false
		},
		// Speaker slugs keep their Norwegian letters upstream
		// ("audun-øygard"), and `list` prints them verbatim, so the
		// ASCII form a user can actually type has to match too.
		func(t Talk) bool {
			for _, sp := range t.Speakers {
				if slugify(sp.Slug) == slugify(q) || slugify(sp.Name) == slugify(q) {
					return true
				}
			}
			return false
		},
		func(t Talk) bool { return strings.Contains(strings.ToLower(t.Title), q) },
		func(t Talk) bool { return strings.Contains(strings.ToLower(slugify(t.Title)), slugify(q)) },
		func(t Talk) bool {
			for _, sp := range t.Speakers {
				if strings.Contains(strings.ToLower(sp.Name), q) {
					return true
				}
			}
			return false
		},
	}

	for _, match := range tiers {
		var hits []Talk
		for _, t := range p.Talks {
			if match(t) {
				hits = append(hits, t)
			}
		}
		if len(hits) > 0 {
			return hits
		}
	}
	return nil
}

// FindOne resolves a selector that must identify exactly one talk.
func (p Program) FindOne(selector string) (Talk, error) {
	hits := p.Find(selector)
	switch len(hits) {
	case 0:
		return Talk{}, fmt.Errorf("no talk matches %q", selector)
	case 1:
		return hits[0], nil
	default:
		var names []string
		for _, h := range hits {
			names = append(names, fmt.Sprintf("%q (%s)", h.Title, h.SpeakerNames("and")))
		}
		return Talk{}, fmt.Errorf("%q matches %d talks: %s", selector, len(hits), strings.Join(names, ", "))
	}
}

// Key identifies a speaker in a manifest and in the preview server's URLs.
//
// The CMS slug is used where there is one, but it is not always there: the 2026
// program has a speaker with an empty slug, and keying corrections by slug left
// him unaddressable — his edit form posted to /speaker/ and 404'd, and his
// bundle got no override document to type into. Worse, anything that did get
// stored under the empty key would have applied to every slugless speaker at
// once.
//
// The fallbacks are stable rather than pretty: the name is the one the program
// submitted, so correcting the name does not move the key, and the CMS id is
// there for a speaker with neither.
func (s Speaker) Key() string {
	switch {
	case s.Slug != "":
		return s.Slug
	case s.Name != "":
		return slugify(s.Name)
	default:
		return s.ID
	}
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify reduces text to a lowercase ASCII slug.
//
// Accented letters are decomposed and stripped of their combining marks, so
// "Zoë" becomes "zoe" rather than "zo-" — these slugs end up in
// filenames, and a speaker's name should still be readable in one.
//
// æ, ø and å are handled explicitly because they are distinct letters rather
// than accented vowels: Unicode does not decompose them, so without this they
// would be dropped entirely and "Håvard" would slug to "hvard".
func slugify(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case r == 'æ':
			b.WriteString("ae")
		case r == 'ø':
			b.WriteString("o")
		case r == 'å':
			b.WriteString("a")
		case unicode.Is(unicode.Mn, r):
			// A combining mark left over from decomposition.
		case r <= unicode.MaxASCII:
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(nonSlug.ReplaceAllString(b.String(), "-"), "-")
}

// FileStem is the output filename base for a talk's promo, e.g.
// "d1-0900-sindre-vik-pods-on-mars". The day and start time lead so that a
// directory listing falls into schedule order.
func (t Talk) FileStem() string {
	parts := []string{fmt.Sprintf("d%d", t.Schedule.Day)}
	if hm := nonSlug.ReplaceAllString(strings.ToLower(t.Schedule.StartTime), ""); hm != "" {
		parts = append(parts, hm)
	}
	var who []string
	for _, sp := range t.Speakers {
		if sl := sp.Slug; sl != "" {
			who = append(who, slugify(sl))
		} else if sp.Name != "" {
			who = append(who, slugify(sp.Name))
		}
	}
	if len(who) > 2 {
		who = append(who[:2], "et-al")
	}
	if len(who) > 0 {
		parts = append(parts, strings.Join(who, "_"))
	}
	if title := slugify(t.Title); title != "" {
		parts = append(parts, truncateSlug(title, 48))
	}
	return strings.Join(parts, "-")
}

func truncateSlug(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '-'); i > max/2 {
		cut = cut[:i]
	}
	return strings.Trim(cut, "-")
}

// formatLabels maps the website's format identifiers to display text. Unknown
// identifiers fall back to a generic prettifier rather than being dropped, so a
// newly added format still renders something sensible.
var formatLabels = map[string]string{
	"presentation_20": "20 min talk",
	"presentation_25": "25 min talk",
	"presentation_40": "40 min talk",
	"presentation_45": "45 min talk",
	"lightning_10":    "Lightning talk",
	"workshop_120":    "2 h workshop",
	"workshop_240":    "4 h workshop",
}

// FormatLabel renders a talk's format for display.
func (t Talk) FormatLabel() string {
	if l, ok := formatLabels[t.Format]; ok {
		return l
	}
	return prettify(t.Format)
}

// IsWorkshop reports whether the talk is one of the website's workshop
// formats ("workshop_120", "workshop_240"), which the copy announces
// differently from a talk.
func (t Talk) IsWorkshop() bool { return strings.HasPrefix(t.Format, "workshop") }

// LevelLabel renders a talk's audience level for display.
func (t Talk) LevelLabel() string { return prettify(t.Level) }

// prettify turns an identifier like "workshop_120" or "intermediate" into
// "Workshop 120" / "Intermediate".
func prettify(s string) string {
	s = strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// ShortTrack is the slot's track without the website's "Track N: " prefix,
// which is noise everywhere the column or line is already labelled.
func (s Slot) ShortTrack() string {
	if _, rest, ok := strings.Cut(s.Track, ": "); ok {
		return rest
	}
	return s.Track
}

// TimeRange renders a slot as "09:00–11:00".
func (s Slot) TimeRange() string {
	switch {
	case s.StartTime == "":
		return ""
	case s.EndTime == "":
		return s.StartTime
	default:
		return s.StartTime + "–" + s.EndTime
	}
}
