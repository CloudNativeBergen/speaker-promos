package post

import (
	"fmt"
	"strings"
	"time"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/promo"
)

// BlueskyLimit is the maximum length of a Bluesky post, in graphemes as Bluesky
// counts them; runes are a close enough proxy for Latin text with URLs.
const BlueskyLimit = 300

// Draft is generated copy for one platform.
type Draft struct {
	Platform string
	Text     string
	// Notes are things the user should check before posting: guessed employers,
	// speakers with no handle to mention, a post that had to be shortened.
	Notes []string
	// Limit is the platform's length limit, or 0 for none worth counting.
	Limit int
}

// Runes is the post's length as a platform would count it.
func (d Draft) Runes() int { return len([]rune(d.Text)) }

// Over reports whether the post is longer than its platform allows.
func (d Draft) Over() bool { return d.Limit > 0 && d.Runes() > d.Limit }

// Count renders the length as "260/300" where a limit applies, else "582".
func (d Draft) Count() string {
	if d.Limit <= 0 {
		return fmt.Sprint(d.Runes())
	}
	return fmt.Sprintf("%d/%d", d.Runes(), d.Limit)
}

// Percent is how much of the limit the post uses, capped at 100, for a meter.
func (d Draft) Percent() int {
	if d.Limit <= 0 {
		return 0
	}
	return min(d.Runes()*100/d.Limit, 100)
}

// Input is everything needed to draft copy for a talk.
type Input struct {
	Conference cnd.Conference
	// Talk is the talk as resolved, corrections and language included: the
	// copy says what the card beside it says.
	Talk promo.Talk
}

// language is the wording the talk resolved to. A Talk from a Resolver is
// never Auto; one built by hand is detected rather than defaulted, since an
// English frame around a Norwegian abstract is what this exists to prevent.
func (in Input) language() lang.Language {
	return in.Talk.Language.Resolve(in.Talk.Title, in.Talk.Abstract)
}

// LinkedIn drafts a LinkedIn post.
//
// LinkedIn has no practical length limit for these, so the copy can afford a
// teaser from the abstract and a full list of speakers. Handles are given as
// profile URLs rather than "@name": LinkedIn only turns a mention into a link
// when it is picked from its own autocomplete, so a typed @name would post as
// plain text. The URLs are listed separately for the user to convert.
func LinkedIn(in Input) Draft {
	var d Draft
	d.Platform = "linkedin"
	t := in.Talk
	l := in.language()

	var b strings.Builder
	b.WriteString(hook(in))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%s\n", quoteTitle(t.Title, l))

	if teaser := cnd.FirstSentences(t.Abstract, 320); teaser != "" {
		fmt.Fprintf(&b, "\n%s\n", teaser)
	}

	fmt.Fprintf(&b, "\n📅 %s · %s\n", t.Schedule.TimeRange(), dayLabel(in.Conference, t.Schedule, l))
	if track := t.Schedule.ShortTrack(); track != "" {
		fmt.Fprintf(&b, "📍 %s\n", track)
	}
	if url := talkURL(in); url != "" {
		fmt.Fprintf(&b, "\n%s\n", url)
	}

	if tags := hashtags(in); tags != "" {
		fmt.Fprintf(&b, "\n%s\n", tags)
	}
	d.Text = strings.TrimRight(b.String(), "\n")
	d.Notes = notes(in, "linkedin")
	return d
}

// Bluesky drafts a Bluesky post.
//
// The 300-character limit is the binding constraint, so the copy is assembled
// shortest-first: the essential sentence, then the handles, then the link, and
// the teaser only if it still fits. Trimming afterwards would risk cutting the
// link, which is the one part that must survive.
func Bluesky(in Input) Draft {
	d := Draft{Platform: "bluesky", Limit: BlueskyLimit}
	t := in.Talk
	l := in.language()

	url := talkURL(in)
	mentions := blueskyMentions(in)

	// The post is built as head + optional middle + tail. The tail holds the
	// mentions and the link, which are the parts that must never be cut, so
	// they are reserved up front rather than trimmed off the end.
	head := hook(in) + "\n\n" + quoteTitle(t.Title, l)
	tail := ""
	if mentions != "" {
		tail += "\n\n" + mentions
	}
	if url != "" {
		tail += "\n" + url
	}

	slot := fmt.Sprintf("\n\n%s · %s", t.Schedule.TimeRange(), dayLabel(in.Conference, t.Schedule, l))
	fits := func(parts ...string) bool {
		return len([]rune(strings.Join(parts, ""))) <= BlueskyLimit
	}

	// If the hook and title alone cannot coexist with the tail, shorten the
	// HEAD rather than letting the whole post be trimmed from the end. trimTo
	// cuts at a word boundary from the right, and the title is the rightmost
	// part of the head, so this loses title words and keeps the link — the
	// opposite of what trimming the finished post would do.
	if !fits(head, tail) {
		if room := BlueskyLimit - len([]rune(tail)); room > 0 {
			head = trimTo(head, room)
		}
	}

	// Optional sections accumulate in display order. The teaser is sized
	// against the room left once the slot line is also accounted for, so that
	// adding the teaser cannot squeeze the slot out.
	mid := ""
	if room := BlueskyLimit - len([]rune(head+slot+tail)) - 2; room > 60 {
		if teaser := cnd.FirstSentences(t.Abstract, room); teaser != "" {
			if fits(head, "\n\n", teaser, slot, tail) {
				mid = "\n\n" + teaser
			}
		}
	}
	if fits(head, mid, slot, tail) {
		mid += slot
	}
	text := head + mid + tail

	d.Notes = notes(in, "bluesky")
	if n := len([]rune(text)); n > BlueskyLimit {
		// Only reachable when the title alone is enormous; trim the title
		// rather than the link.
		text = trimTo(text, BlueskyLimit)
		d.Notes = append(d.Notes, fmt.Sprintf("post was %d chars and had to be shortened to %d", n, BlueskyLimit))
	}
	d.Text = text
	return d
}

// hook is the opening sentence, naming the speakers and their employers.
func hook(in Input) string {
	w := in.language().Words()

	names := make([]string, 0, len(in.Talk.Speakers))
	for _, sp := range in.Talk.Speakers {
		name := sp.Name
		if sp.Role.Employer != "" {
			name += " (" + sp.Role.Employer + ")"
		}
		names = append(names, name)
	}
	who := cnd.JoinAnd(names, w.And)
	if who == "" {
		who = w.Anonymous
	}

	plural := len(in.Talk.Speakers) > 1
	verb := w.SpeakingSingular
	if plural {
		verb = w.SpeakingPlural
	}
	if in.Talk.IsWorkshop() {
		verb = w.WorkshopSingular
		if plural {
			verb = w.WorkshopPlural
		}
	}
	return fmt.Sprintf("%s %s %s %s 🎤", who, verb, w.At, in.Conference.Title)
}

// quoteTitle wraps a talk title in quotation marks, unless it already carries
// its own. Norwegian uses angle quotation marks.
func quoteTitle(title string, l lang.Language) string {
	if title == "" {
		return ""
	}
	for _, already := range []string{"“", "«", "\""} {
		if strings.HasPrefix(title, already) {
			return title
		}
	}
	w := l.Words()
	return w.QuoteOpen + title + w.QuoteClose
}

// dayLabel names the conference day, e.g. "Monday 26 October" or
// "mandag 26. oktober".
//
// The names are looked up rather than taken from time.Format, which only knows
// English.
func dayLabel(conf cnd.Conference, s cnd.Slot, l lang.Language) string {
	if d, err := time.Parse(time.DateOnly, s.Date); err == nil {
		w := l.Words()
		return w.Date(w, d)
	}
	return conf.DateRange()
}

// talkURL is the link a promo points at: the program, since a talk has no page
// of its own (see cnd.Conference.ProgramURL).
//
// It deliberately does not fall back to a speaker's profile, which on a
// multi-speaker talk would promote whoever is listed first. Profiles are
// surfaced as mentions instead.
func talkURL(in Input) string {
	return in.Conference.ProgramURL()
}

// blueskyMentions builds "@handle" mentions, which Bluesky resolves from plain
// text — unlike LinkedIn.
func blueskyMentions(in Input) string {
	var out []string
	for _, sp := range in.Talk.Speakers {
		if sp.Links.Bluesky != "" {
			out = append(out, "@"+sp.Links.Bluesky)
		}
	}
	return strings.Join(out, " ")
}

// hashtags are the conference tag plus the talk's topics, which are curated
// upstream and so make better tags than anything derived from the title.
func hashtags(in Input) string {
	tags := []string{"#CloudNativeDaysNorway", "#CloudNative"}
	seen := map[string]bool{}
	for _, t := range tags {
		seen[strings.ToLower(t)] = true
	}
	for _, topic := range in.Talk.Topics {
		tag := "#" + hashtagify(topic)
		if len(tag) > 1 && !seen[strings.ToLower(tag)] {
			seen[strings.ToLower(tag)] = true
			tags = append(tags, tag)
		}
	}
	if len(tags) > 5 {
		tags = tags[:5]
	}
	return strings.Join(tags, " ")
}

// hashtagify reduces a topic name to a CamelCase tag.
func hashtagify(s string) string {
	var b strings.Builder
	for _, word := range strings.FieldsFunc(s, func(r rune) bool {
		return !isAlnum(r)
	}) {
		r := []rune(word)
		b.WriteString(strings.ToUpper(string(r[0])))
		if len(r) > 1 {
			b.WriteString(string(r[1:]))
		}
	}
	return b.String()
}

// notes collects things the user should verify before posting.
func notes(in Input, platform string) []string {
	var out []string
	for _, sp := range in.Talk.Speakers {
		// Quoted from the website's title, which is what the guess was made
		// from — not the role line, which a correction may have replaced.
		if sp.Role.Guessed && sp.Role.Employer != "" {
			out = append(out, fmt.Sprintf("employer for %s guessed as %q from %q — check it",
				sp.Name, sp.Role.Employer, sp.Source.Title))
		}
		if sp.Role.Employer == "" {
			out = append(out, fmt.Sprintf("no employer known for %s", sp.Name))
		}
		switch platform {
		case "bluesky":
			if sp.Links.Bluesky == "" {
				out = append(out, fmt.Sprintf("no Bluesky handle for %s", sp.Name))
			}
		case "linkedin":
			if sp.Links.LinkedIn == "" {
				out = append(out, fmt.Sprintf("no LinkedIn profile for %s", sp.Name))
			}
		}
	}
	return out
}

// Mentions lists the profile URLs for a draft, for the user to turn into real
// mentions by hand.
func Mentions(in Input) []string {
	var out []string
	for _, sp := range in.Talk.Speakers {
		var parts []string
		if sp.Links.LinkedIn != "" {
			parts = append(parts, sp.Links.LinkedIn)
		}
		if sp.Links.Bluesky != "" {
			parts = append(parts, "@"+sp.Links.Bluesky)
		}
		if sp.Links.X != "" {
			parts = append(parts, "x.com/"+sp.Links.X)
		}
		if len(parts) > 0 {
			out = append(out, fmt.Sprintf("%s: %s", sp.Name, strings.Join(parts, "  ")))
		}
	}
	return out
}
