package promo

import (
	"strings"

	"github.com/vehagn/speaker-promos/internal/manifest"
)

// Role is a speaker's job title and employer, as far as they can be determined.
type Role struct {
	// Job is the role, e.g. "Staff Developer Advocate". May be empty.
	Job string
	// Employer is the company, e.g. "Vestbit". May be empty.
	Employer string
	// Guessed is true when Employer was inferred from free text rather than
	// taken from an override. Copy marks these so they get checked before
	// posting.
	Guessed bool
}

// separators split a profile title into role and employer.
//
// The upstream `title` field is free text with no structure, and the 2026
// program shows the full range: "Senior Platform Engineer at Vestbit",
// "Senior Consultant @Nordvik", "Utvikler hos Bergsdal Consulting",
// "Bysten Labs", and empty. These are the separators that actually occur.
//
// " i " is deliberately absent. It is the Norwegian "in", and while
// "faggruppeleder i Tindra" does mean Tindra is the employer, " i " also
// appears mid-phrase often enough ("Drift i Praksis") that matching it
// produced worse guesses than leaving the whole string as the employer.
var separators = []string{" at ", " hos ", " @ ", " @", "@"}

// ParseRole splits a speaker's profile title into job and employer.
//
// The LEFTMOST separator wins rather than the first one in the list: the role
// comes before the employer, so in "Platform lead hos Tindra at Oslo" the
// split belongs at " hos ", and preferring " at " because it happens to be
// listed first would take "Oslo" as the employer.
//
// A title with no separator is treated as a bare employer rather than a bare
// job: the cases that occur in practice are company names ("Bysten Labs"), and
// naming the wrong thing in a post is worse than naming nothing.
func ParseRole(title string) Role {
	title = strings.TrimSpace(title)
	if title == "" {
		return Role{}
	}

	best := -1
	var bestSep string
	for _, sep := range separators {
		i := strings.Index(title, sep)
		if i < 0 {
			continue
		}
		// On a tie the longer separator wins, so " @ " beats "@" at the same
		// position and the surrounding spaces are not left in the output.
		if best < 0 || i < best || (i == best && len(sep) > len(bestSep)) {
			best, bestSep = i, sep
		}
	}
	if best >= 0 {
		job := strings.TrimSpace(title[:best])
		employer := strings.TrimSpace(title[best+len(bestSep):])
		if job != "" && employer != "" {
			return Role{Job: job, Employer: employer, Guessed: true}
		}
	}
	// No usable separator: assume the whole string names the employer.
	return Role{Employer: title, Guessed: true}
}

// RoleLine composes a speaker override into the line a card prints under the
// names, following the upstream convention ("Senior Platform Engineer at
// Vestbit"). It returns "" when the override says nothing about the role, so
// the upstream value is kept.
func RoleLine(spec manifest.SpeakerSpec) string {
	switch {
	// An explicit title wins: it is the escape hatch for roles that do not fit
	// the "<job> at <employer>" shape at all.
	case spec.Title != "":
		return spec.Title
	case spec.Job != "" && spec.Employer != "":
		return spec.Job + " at " + spec.Employer
	case spec.Job != "":
		return spec.Job
	default:
		return spec.Employer
	}
}
