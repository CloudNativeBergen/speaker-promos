package post

import (
	"strings"
	"testing"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/promo"
)

func norwegianInput() Input {
	return Input{
		Conference: cnd.Conference{
			Title: "Cloud Native Days Norway 2026", StartDate: "2026-10-26",
			EndDate: "2026-10-27", Domain: "2026.cloudnativedays.no",
		},
		Talk: promo.Talk{
			Talk: cnd.Talk{
				Title:    "Kan skyen kjøre på en brødrister?",
				Abstract: "Vi ser på hvordan det ikke fungerte og hva vi gjorde med det.",
				Format:   "workshop_120",
				Schedule: cnd.Slot{
					Date: "2026-10-26", Day: 1, StartTime: "09:00", EndTime: "11:00",
					Track: "Track 1: Full Day Workshops",
				},
			},
			Speakers: []promo.Speaker{withRole(
				speaker(cnd.Speaker{Name: "Dario Haaland", Slug: "dario-haaland"}),
				promo.Role{Employer: "Bysten Labs"},
			)},
		},
	}
}

// The whole point: no lang.English scaffolding around a lang.Norwegian abstract.
func TestNorwegianDraftHasNoEnglishScaffolding(t *testing.T) {
	in := norwegianInput()
	for _, d := range []Draft{LinkedIn(in), Bluesky(in)} {
		text := d.Text
		for _, want := range []string{
			"holder workshop på Cloud Native Days Norway 2026",
			"«Kan skyen kjøre på en brødrister?»", // angle quotes, per lang.Norwegian typography
			"mandag 26. oktober",                  // localised weekday and month, with the ordinal point
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s draft missing %q:\n%s", d.Platform, want, text)
			}
		}
		for _, forbidden := range []string{
			"is speaking", "are speaking", "is running", "are running",
			" at Cloud Native", "Monday", "October", "“", "”",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s draft contains lang.English %q:\n%s", d.Platform, forbidden, text)
			}
		}
	}
}

func TestNorwegianJoinsNamesWithOg(t *testing.T) {
	in := norwegianInput()
	in.Talk.Speakers = append(in.Talk.Speakers, withRole(
		speaker(cnd.Speaker{Name: "Solveig Ulriksen"}), promo.Role{Employer: "Skyvakt"},
	))
	got := hook(in)
	if !strings.Contains(got, "og Solveig Ulriksen") {
		t.Errorf("hook = %q, want the names joined with \"og\"", got)
	}
	// lang.Norwegian has no verb-number agreement, so the verb does not change.
	if !strings.Contains(got, "holder workshop") {
		t.Errorf("hook = %q", got)
	}
}

func TestEnglishIsUnchanged(t *testing.T) {
	in := norwegianInput()
	in.Talk.Language = lang.English
	got := hook(in)
	if !strings.Contains(got, "is running a workshop at Cloud Native Days Norway 2026") {
		t.Errorf("hook = %q", got)
	}
	if !strings.Contains(quoteTitle("Pods on Mars", lang.English), "“Pods on Mars”") {
		t.Error("lang.English should keep curly quotes")
	}
}

func TestNorwegianTalkDraftStaysInsideTheBlueskyLimit(t *testing.T) {
	in := norwegianInput()
	in.Talk.Title = strings.Repeat("Veldig lang tittel om plattformer ", 5)
	in.Talk.Abstract = strings.Repeat("Vi ser på hvordan det ikke fungerte. ", 30)
	d := Bluesky(in)
	if d.Runes() > BlueskyLimit {
		t.Errorf("draft is %d runes:\n%s", d.Runes(), d.Text)
	}
}

// A title that already carries quotation marks is not double-quoted, in either
// language's style.
func TestQuoteTitleRespectsExistingQuotes(t *testing.T) {
	for _, title := range []string{"«Allerede sitert»", "“Already quoted”", `"Quoted"`} {
		if got := quoteTitle(title, lang.Norwegian); got != title {
			t.Errorf("quoteTitle(%q) = %q", title, got)
		}
	}
}

// An unrecognised Language value falls back to DETECTION rather than to a
// fixed language, so a garbage value still produces coherent copy rather than
// an lang.English frame around a lang.Norwegian abstract — which is the bug this whole
// feature exists to fix.
func TestUnknownLanguageFallsBackToDetection(t *testing.T) {
	in := norwegianInput()
	in.Talk.Language = lang.Language("sv")
	if got := hook(in); !strings.Contains(got, "holder workshop") {
		t.Errorf("hook = %q, want the detected Norwegian", got)
	}

}
