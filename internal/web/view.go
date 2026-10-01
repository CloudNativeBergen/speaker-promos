package web

import (
	"fmt"
	"strings"

	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/post"
	"github.com/vehagn/speaker-promos/internal/promo"
)

// talkView is everything the page shows about one talk.
//
// The templates render only from this, never from the domain types directly, so
// that "what the card will say" and "what the form is pre-filled with" are
// computed in one place and cannot drift apart.
type talkView struct {
	// Talk is the talk as resolved: its title is the display title, its
	// language the one the drafts below are written in, and SubmittedTitle,
	// Detected and Hidden say what the form needs to compare against.
	Talk promo.Talk
	// Override is what the manifest currently says about the talk, so the
	// language selector can tell "auto" from a forced choice.
	Override manifest.TalkSpec
	// CardURL carries the manifest revision so an edit busts the browser's
	// cache for that one card. Without it an HTMX swap re-inserts the same
	// <img src>, the browser serves the stale bytes, and the edit looks like it
	// did nothing.
	CardURL  string
	Size     string
	Speakers []speakerView
	Drafts   []draftView
	// Warnings are the render-side notes for this card: truncated text, or
	// emoji that some renderers will drop.
	Warnings []string
}

// speakerView is one speaker as resolved, plus what the form needs on top.
type speakerView struct {
	// Speaker is what the card and copy actually use. Its values are what the
	// form inputs are pre-filled with, so a found value can be edited in place
	// rather than retyped from a grey placeholder.
	//
	// Because every field therefore arrives populated, the handler stores only
	// what differs from the baseline — promo.Baseline, the same speaker with no
	// override at all. Writing the lot back would turn every guess into a
	// confirmed correction the first time any field was touched.
	Speaker promo.Speaker
	// HasPhoto is false when the card fell back to a monogram — either because
	// the speaker has no photo upstream or because the one they have could not
	// be fetched. It is what the image field on the form is for.
	HasPhoto bool
}

// speakerField is one input on a speaker's form.
type speakerField struct {
	Label string
	// Name is the input's name AND the manifest field it corrects — which is
	// what lets the handler read a submission back through the manifest's own
	// field table instead of naming every field again.
	Name        string
	Value       string
	Placeholder string
	// Hint sits under the input, for a value the tool guessed.
	Hint string
	// Class marks the surrounding cell: a photo the card could not fetch is
	// outlined so the field that fixes it is the obvious one.
	Class string
	// Wand is the endpoint of the 🪄 button on the label, and WandTitle its
	// tooltip. Both are empty for the fields with no capitalisation rule.
	Wand      string
	WandTitle string
	// Anchor is the row the wand posts back into.
	Anchor string
}

// Fields are the speaker's editable inputs, in display order, pre-filled with
// the values the card and copy actually used.
//
// The form is generated from this table rather than written out once per field
// in the template, so adding a correctable field is one line here and one line
// in the manifest's own table. GitHub is deliberately absent: nothing on a card
// or in a post uses it, so it is carried through the manifest untouched.
func (v speakerView) Fields(anchor string) []speakerField {
	photo, photoClass := "Photo", ""
	if !v.HasPhoto {
		photo, photoClass = "Photo — none, showing initials", "missing"
	}
	var hint string
	if v.Speaker.Role.Guessed {
		hint = "guessed from “" + v.Speaker.Source.Title + "”"
	}

	fields := []speakerField{
		{Label: "Name", Name: "name", Placeholder: "unknown",
			Wand:      "/speaker/" + v.Speaker.Key + "/namecase",
			WandTitle: "Capitalise the name"},
		{Label: "Employer", Name: "employer", Placeholder: "unknown", Hint: hint},
		{Label: "Job title", Name: "job", Placeholder: "none"},
		{Label: "Role line (verbatim)", Name: "title",
			Placeholder: "composed from employer + job"},
		{Label: photo, Name: "image", Placeholder: "URL or a file path", Class: photoClass},
		{Label: "LinkedIn", Name: "linkedin", Placeholder: "none found"},
		{Label: "Bluesky", Name: "bluesky", Placeholder: "none found"},
		{Label: "X", Name: "x", Placeholder: "none found"},
	}
	values := v.Speaker.Values()
	for i := range fields {
		f := &fields[i]
		f.Value = values.Value(f.Name)
		f.Anchor = anchor
	}
	return fields
}

// draftView is one platform's copy, with its length budget resolved.
type draftView struct {
	Draft post.Draft
	Limit int
	Over  bool
}

// Percent is how much of the platform's limit the draft uses, for a meter.
func (d draftView) Percent() int {
	if d.Limit <= 0 {
		return 0
	}
	p := d.Draft.Runes() * 100 / d.Limit
	if p > 100 {
		p = 100
	}
	return p
}

// Count renders the length as "260/300" where a limit applies, else "582".
func (d draftView) Count() string {
	if d.Limit <= 0 {
		return fmt.Sprintf("%d", d.Draft.Runes())
	}
	return fmt.Sprintf("%d/%d", d.Draft.Runes(), d.Limit)
}

// Anchor is a stable DOM id for a talk's row, used as the HTMX swap target.
func (v talkView) Anchor() string { return "talk-" + domID(v.Talk.ID) }

// domID reduces an identifier to something safe in an HTML id attribute.
func domID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
