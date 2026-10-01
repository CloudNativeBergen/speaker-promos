package promo

import (
	"strings"
	"testing"

	"github.com/vehagn/speaker-promos/internal/manifest"
)

// These are the exact title strings the 2026 program contains, which is the
// reason the heuristic looks the way it does.
func TestParseRole(t *testing.T) {
	cases := []struct {
		in       string
		job      string
		employer string
	}{
		{"Senior Platform Engineer at Vestbit", "Senior Platform Engineer", "Vestbit"},
		{"Senior Consultant @Nordvik", "Senior Consultant", "Nordvik"},
		{"Utvikler hos Skyvakt", "Utvikler", "Skyvakt"},
		{"Developer Advocate at Fjordstack", "Developer Advocate", "Fjordstack"},
		{"Engineering Manager at Tindra Systems", "Engineering Manager", "Tindra Systems"},
		// No separator: the whole string is taken as the employer, because the
		// cases that occur in practice are company names and naming the wrong
		// thing is worse than naming nothing.
		{"Bysten Labs", "", "Bysten Labs"},
		{"", "", ""},
		{"   ", "", ""},
		// " at " must not be matched inside " hos ", and the longest separator
		// wins.
		{"Platform lead hos Tindra at Oslo", "Platform lead", "Tindra at Oslo"},
	}
	for _, c := range cases {
		got := ParseRole(c.in)
		if got.Job != c.job || got.Employer != c.employer {
			t.Errorf("ParseRole(%q) = {%q, %q}, want {%q, %q}",
				c.in, got.Job, got.Employer, c.job, c.employer)
		}
		if c.employer != "" && !got.Guessed {
			t.Errorf("ParseRole(%q) should be marked as guessed", c.in)
		}
	}
}

// " i " is deliberately NOT a separator: it is the lang.Norwegian "in" but also
// appears mid-phrase, where splitting on it produces nonsense.
func TestParseRoleLeavesNorwegianIAlone(t *testing.T) {
	got := ParseRole("Manager og faggruppeleder Platform Engineering i Tindra")
	if got.Job != "" {
		t.Errorf("Job = %q, want the whole string kept as the employer", got.Job)
	}
	if !strings.Contains(got.Employer, "Tindra") {
		t.Errorf("Employer = %q", got.Employer)
	}
}

func TestRoleLine(t *testing.T) {
	verbatim := "Maintainer, Principal Open source Architect, Co-Chair CNCF TAG Infrastructure"
	for _, tc := range []struct {
		spec manifest.SpeakerSpec
		want string
	}{
		// The upstream convention, so a corrected role reads like an
		// uncorrected one.
		{manifest.SpeakerSpec{Job: "Senior Platform Engineer", Employer: "Vestbit"}, "Senior Platform Engineer at Vestbit"},
		{manifest.SpeakerSpec{Employer: "Bysten Labs"}, "Bysten Labs"},
		{manifest.SpeakerSpec{Job: "Utvikler"}, "Utvikler"},
		// Plenty of real titles do not fit "<job> at <employer>", so an
		// explicit title sets the line verbatim and wins over both.
		{manifest.SpeakerSpec{Title: verbatim}, verbatim},
		{manifest.SpeakerSpec{Title: verbatim, Job: "Engineer", Employer: "Vestbit"}, verbatim},
		// Nothing said about the role: the caller keeps the upstream value.
		{manifest.SpeakerSpec{}, ""},
		{manifest.SpeakerSpec{Name: "Only a name"}, ""},
	} {
		if got := RoleLine(tc.spec); got != tc.want {
			t.Errorf("RoleLine(%+v) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}
