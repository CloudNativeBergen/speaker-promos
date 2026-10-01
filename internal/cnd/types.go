// Package cnd models a Cloud Native Days conference program and loads it from
// the public website.
package cnd

import (
	"fmt"
	"strings"
	"time"
)

// Conference is the event a program belongs to.
type Conference struct {
	Title     string `json:"title"`
	StartDate string `json:"startDate"` // ISO date, e.g. "2026-10-26"
	EndDate   string `json:"endDate"`
	City      string `json:"city"`
	Country   string `json:"country"`
	Domain    string `json:"domain"` // primary public domain
	// LogoBright is the conference wordmark as inline SVG, in the light-on-dark
	// variant. Promo cards paint on a saturated brand gradient, so this is the
	// variant that applies; LogoDark is kept for light themes.
	LogoBright string `json:"-"`
	LogoDark   string `json:"-"`
	// LogomarkBright is the square mark without wordmark, for tight layouts.
	LogomarkBright string `json:"-"`
}

// Speaker is one presenter of a talk.
type Speaker struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	// Title is the website's job title / tagline: free text from the speaker's
	// profile, and wildly inconsistent across speakers: "Senior Platform
	// Engineer at Vestbit", "Bysten Labs", "Utvikler hos Bergsdal Consulting",
	// or empty. There is no structured employer field, so anything that needs
	// an employer has to guess — see promo.ParseRole.
	Title string `json:"title"`
	// Image is a cdn.sanity.io URL. It accepts crop transforms; use
	// ImageSourceOf.
	Image string `json:"image,omitempty"`
}

// sanityHost is the CMS image CDN, the only source that understands the
// transform parameters below.
const sanityHost = "cdn.sanity.io"

// IsRemoteImage reports whether an image reference is a URL rather than a path
// on disk.
func IsRemoteImage(image string) bool {
	return strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://")
}

// ImageSource is where a speaker photo comes from: exactly one of URL or Path
// is set, and both are empty when there is no photo.
type ImageSource struct {
	URL  string
	Path string
}

// Empty reports whether there is no photo to fetch.
func (s ImageSource) Empty() bool { return s.URL == "" && s.Path == "" }

// ImageSource resolves a photo reference — a CMS URL, any other URL, or a path
// on disk — for a square of the given size.
//
// Transform parameters are added ONLY for the CMS CDN, which is the only host
// that understands them. They used to be appended to every URL, which was
// wrong in both directions: a GitHub avatar or LinkedIn photo silently ignored
// them and came back at its own size, and an overridden photo on an arbitrary
// host could be handed query parameters that mean something else there.
func ImageSourceOf(image string, size int) ImageSource {
	image = strings.TrimSpace(image)
	switch {
	case image == "":
		return ImageSource{}
	case !IsRemoteImage(image):
		return ImageSource{Path: strings.TrimPrefix(image, "file://")}
	case !strings.Contains(image, sanityHost):
		return ImageSource{URL: image}
	}

	sep := "?"
	if strings.Contains(image, "?") {
		sep = "&"
	}
	// JPEG, not the source PNG: these get base64-embedded into an SVG, and at
	// 600px the PNG rendition is 600 KB against 47 KB for JPEG at q=82 — a 13x
	// difference in the size of every promo. `fm` is set explicitly rather than
	// via `auto=format` because the fetcher sends `Accept: */*`, which would
	// leave the choice of codec up to the CDN.
	return ImageSource{
		URL: fmt.Sprintf("%s%sw=%d&h=%d&fit=crop&fm=jpg&q=82", image, sep, size, size),
	}
}

// Talk is an accepted talk and the slot it was given.
//
// The website calls the document a `talk` (a "proposal" in its TypeScript), and
// attaches where it runs as `scheduleInfo`; Schedule mirrors that, so there is
// one thing called a talk rather than a talk wrapped in a session.
type Talk struct {
	ID string `json:"id"`
	// Title is presenter-authored and may contain emoji, which no text font
	// covers. See internal/layout for how that is handled.
	Title string `json:"title"`
	// Abstract is the talk's `description` flattened to plain text.
	Abstract string    `json:"abstract,omitempty"`
	Format   string    `json:"format,omitempty"` // e.g. "workshop_120", "presentation_25"
	Level    string    `json:"level,omitempty"`  // e.g. "beginner", "intermediate", "advanced"
	Status   string    `json:"status,omitempty"` // e.g. "confirmed", "accepted"
	Topics   []string  `json:"topics,omitempty"`
	Speakers []Speaker `json:"speakers"`
	Schedule Slot      `json:"schedule"`
}

// Slot is where a talk sits in the schedule: the website's `scheduleInfo`.
type Slot struct {
	Date      string `json:"date" yaml:"date"` // ISO date of the day it runs
	Day       int    `json:"day" yaml:"day"`   // 1-based day index within the conference
	Track     string `json:"track" yaml:"track"`
	StartTime string `json:"startTime" yaml:"startTime"` // "HH:MM"
	EndTime   string `json:"endTime" yaml:"endTime"`
}

// Program is a conference and its scheduled talks.
type Program struct {
	Conference Conference `json:"conference"`
	Talks      []Talk     `json:"talks"`
	// FetchedAt is when the program page was fetched — from the cache, so it
	// can be well before this run. Zero when unknown.
	FetchedAt time.Time `json:"fetchedAt,omitzero"`
}

// Talk returns a talk by id.
func (p Program) Talk(id string) (Talk, bool) {
	for _, t := range p.Talks {
		if t.ID == id {
			return t, true
		}
	}
	return Talk{}, false
}

// Speakers returns every distinct speaker in the program, in first-appearance
// order.
func (p Program) Speakers() []Speaker {
	seen := make(map[string]bool)
	var out []Speaker
	for _, t := range p.Talks {
		for _, sp := range t.Speakers {
			key := sp.Key()
			if !seen[key] {
				seen[key] = true
				out = append(out, sp)
			}
		}
	}
	return out
}

// SpeakerNames renders a talk's speakers as prose: "A", "A and B", or
// "A, B and C", using the given conjunction.
//
// The conjunction is a parameter rather than a constant because a Norwegian
// talk's card should read "A og B". It is passed in rather than resolved here
// so that the domain model stays free of presentation concerns.
func (t Talk) SpeakerNames(and string) string {
	names := make([]string, 0, len(t.Speakers))
	for _, sp := range t.Speakers {
		if sp.Name != "" {
			names = append(names, sp.Name)
		}
	}
	return JoinAnd(names, and)
}

// JoinAnd joins items as prose: "A", "A and B", or "A, B and C".
//
// The conjunction is a parameter because a Norwegian talk reads "A og B", and
// because the social copy joins names that carry their employer with them.
func JoinAnd(items []string, and string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " " + and + " " + items[len(items)-1]
	}
}

// DateRange renders the conference dates compactly: "26–27 October 2026", or
// "26 October – 2 November 2026" when the months differ.
func (c Conference) DateRange() string {
	start, err1 := time.Parse(time.DateOnly, c.StartDate)
	end, err2 := time.Parse(time.DateOnly, c.EndDate)
	switch {
	case err1 != nil && err2 != nil:
		return ""
	case err1 != nil:
		return end.Format("2 January 2006")
	case err2 != nil || start.Equal(end):
		return start.Format("2 January 2006")
	case start.Year() == end.Year() && start.Month() == end.Month():
		return fmt.Sprintf("%d–%s", start.Day(), end.Format("2 January 2006"))
	case start.Year() == end.Year():
		return fmt.Sprintf("%s – %s", start.Format("2 January"), end.Format("2 January 2006"))
	default:
		return fmt.Sprintf("%s – %s", start.Format("2 January 2006"), end.Format("2 January 2006"))
	}
}

// Location renders "City, Country", omitting either if absent.
func (c Conference) Location() string {
	parts := make([]string, 0, 2)
	if c.City != "" {
		parts = append(parts, c.City)
	}
	if c.Country != "" {
		parts = append(parts, c.Country)
	}
	return strings.Join(parts, ", ")
}

// URL is the conference site root.
func (c Conference) URL() string {
	if c.Domain == "" {
		return ""
	}
	return "https://" + c.Domain
}

// ProgramURL is the schedule page.
//
// This is the closest thing to a link for an individual talk. The site has no
// per-talk page — its sitemap carries 49 `/speaker/<slug>` URLs and exactly one
// `/program` — and the program page keeps its filters in client state with no
// URL parameters and renders no per-talk anchors, so there is nothing to deep
// link to either.
func (c Conference) ProgramURL() string {
	if c.Domain == "" {
		return ""
	}
	return "https://" + c.Domain + "/program"
}

// SpeakerURL is the public profile page for a speaker.
func (c Conference) SpeakerURL(s Speaker) string {
	if c.Domain == "" || s.Slug == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/speaker/%s", c.Domain, s.Slug)
}
