package post

import (
	"strings"
	"testing"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/promo"
)

func testInput() Input {
	return Input{
		Conference: cnd.Conference{
			Title:     "Cloud Native Days Norway 2026",
			StartDate: "2026-10-26",
			EndDate:   "2026-10-27",
			City:      "Bergen",
			Country:   "Norway",
			Domain:    "2026.cloudnativedays.no",
		},
		Talk: promo.Talk{
			Talk: cnd.Talk{
				Title:    "Pods on Mars",
				Abstract: "Running Kubernetes with no upstream at all. A talk about self-sufficiency, and what breaks when the registry is unreachable.",
				Format:   "presentation_25",
				Topics:   []string{"Cloud Infrastructure & Operations", "Kubernetes & Orchestration"},
				Schedule: cnd.Slot{
					Date:      "2026-10-26",
					Day:       1,
					Track:     "Track 2: Platform Engineering",
					StartTime: "13:20",
					EndTime:   "13:45",
				},
			},
			Speakers: []promo.Speaker{withLinks(
				speaker(cnd.Speaker{Name: "Sindre Vik", Slug: "sindre-vik", Title: "Platform Engineer at Fjordstack"}),
				cnd.Links{Bluesky: "sindre.example", LinkedIn: "https://www.linkedin.com/in/sindre"},
			)},
		},
	}
}

// speaker is a presenter as the resolver gives them with no corrections.
func speaker(src cnd.Speaker) promo.Speaker {
	return promo.Speaker{
		Source: src, Key: src.Key(), Name: src.Name, Title: src.Title,
		Image: src.Image, Role: promo.ParseRole(src.Title),
	}
}

func withLinks(sp promo.Speaker, links cnd.Links) promo.Speaker {
	sp.Links = links
	return sp
}

func withRole(sp promo.Speaker, role promo.Role) promo.Speaker {
	sp.Role = role
	return sp
}

func TestLinkedInDraft(t *testing.T) {
	d := LinkedIn(testInput())
	if d.Platform != "linkedin" {
		t.Errorf("Platform = %q", d.Platform)
	}
	for _, want := range []string{
		"Sindre Vik (Fjordstack)",
		"is speaking at Cloud Native Days Norway 2026",
		"“Pods on Mars”",
		"13:20–13:45",
		"Monday 26 October",
		"Platform Engineering",
		"https://2026.cloudnativedays.no/program",
		"#CloudNativeDaysNorway",
	} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("LinkedIn draft missing %q:\n%s", want, d.Text)
		}
	}
	// The track prefix the website uses is noise in a post.
	if strings.Contains(d.Text, "Track 2:") {
		t.Error("track prefix should be stripped")
	}
	// Topics become hashtags with punctuation removed.
	if !strings.Contains(d.Text, "#CloudInfrastructureOperations") {
		t.Errorf("topic hashtag missing:\n%s", d.Text)
	}
	if strings.Contains(d.Text, "#") && strings.Contains(d.Text, "&") && strings.Contains(d.Text, "#Cloud Infrastructure") {
		t.Error("hashtag was not camel-cased")
	}
}

// The 300-character limit is the binding constraint on Bluesky, so it is
// checked against inputs designed to blow past it.
func TestBlueskyRespectsLimit(t *testing.T) {
	in := testInput()
	in.Talk.Title = strings.Repeat("An Extremely Long Talk Title About Kubernetes ", 6)
	in.Talk.Abstract = strings.Repeat("Filler prose that would never fit. ", 40)
	in.Talk.Speakers = append(in.Talk.Speakers, withLinks(
		speaker(cnd.Speaker{Name: "Another Very Long Speaker Name", Slug: "b", Title: "Principal Engineer at A Company With A Long Name"}),
		cnd.Links{Bluesky: "another.speaker.example"},
	))

	d := Bluesky(in)
	if d.Runes() > BlueskyLimit {
		t.Errorf("draft is %d runes, over the %d limit:\n%s", d.Runes(), BlueskyLimit, d.Text)
	}
	if len(d.Notes) == 0 {
		t.Error("a shortened post should say so")
	}
}

// Regression: the teaser variant used to be composed from scratch, which
// dropped the slot line whenever a teaser fitted.
func TestBlueskyKeepsBothSlotAndTeaser(t *testing.T) {
	d := Bluesky(testInput())
	if d.Runes() > BlueskyLimit {
		t.Fatalf("draft is %d runes", d.Runes())
	}
	if !strings.Contains(d.Text, "13:20–13:45") {
		t.Errorf("slot line was dropped:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "Running Kubernetes") {
		t.Errorf("teaser was dropped:\n%s", d.Text)
	}
	// Mentions and the link are the parts that must never be cut.
	if !strings.Contains(d.Text, "@sindre.example") {
		t.Errorf("mention missing:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "https://2026.cloudnativedays.no/program") {
		t.Errorf("link missing:\n%s", d.Text)
	}
}

// When there is no room for anything optional, the link still survives — it is
// reserved before the optional sections are considered.
func TestBlueskyAlwaysKeepsTheLink(t *testing.T) {
	in := testInput()
	in.Talk.Title = strings.Repeat("x", 260)
	d := Bluesky(in)
	if !strings.Contains(d.Text, "2026.cloudnativedays.no") {
		t.Errorf("link was cut:\n%s", d.Text)
	}
	if d.Runes() > BlueskyLimit {
		t.Errorf("draft is %d runes", d.Runes())
	}
}

func TestHookVerbAgreement(t *testing.T) {
	in := testInput()
	if got := hook(in); !strings.Contains(got, "is speaking") {
		t.Errorf("one speaker: %q", got)
	}

	in.Talk.Speakers = append(in.Talk.Speakers, speaker(cnd.Speaker{Name: "Second Person"}))
	if got := hook(in); !strings.Contains(got, "are speaking") {
		t.Errorf("two speakers: %q", got)
	}

	in.Talk.Format = "workshop_120"
	if got := hook(in); !strings.Contains(got, "are running a workshop") {
		t.Errorf("workshop, two speakers: %q", got)
	}
	in.Talk.Speakers = in.Talk.Speakers[:1]
	if got := hook(in); !strings.Contains(got, "is running a workshop") {
		t.Errorf("workshop, one speaker: %q", got)
	}

	// A speaker with no known employer is named without empty parentheses.
	in.Talk.Speakers[0].Role = promo.Role{}
	if got := hook(in); strings.Contains(got, "()") {
		t.Errorf("empty parentheses: %q", got)
	}
}

func TestNotesFlagGuessesAndGaps(t *testing.T) {
	in := testInput()
	notes := Bluesky(in).Notes
	joined := strings.Join(notes, "\n")
	// A guessed employer must be surfaced with the text it came from, so it
	// can be judged without opening the website.
	if !strings.Contains(joined, "Fjordstack") || !strings.Contains(joined, "Platform Engineer at Fjordstack") {
		t.Errorf("notes = %v", notes)
	}

	// A speaker with a known-good employer and a handle produces no notes.
	in.Talk.Speakers[0].Role = promo.Role{Employer: "Fjordstack", Guessed: false}
	if n := Bluesky(in).Notes; len(n) != 0 {
		t.Errorf("notes for complete data = %v", n)
	}

	// Missing employer and missing handle are both reported.
	in.Talk.Speakers[0].Role = promo.Role{}
	in.Talk.Speakers[0].Links = cnd.Links{}
	joined = strings.Join(Bluesky(in).Notes, "\n")
	if !strings.Contains(joined, "no employer known") || !strings.Contains(joined, "no Bluesky handle") {
		t.Errorf("notes = %q", joined)
	}
}

func TestMentionsListsProfiles(t *testing.T) {
	got := Mentions(testInput())
	if len(got) != 1 {
		t.Fatalf("Mentions = %v", got)
	}
	// LinkedIn is given as a URL because LinkedIn only linkifies a mention
	// picked from its own autocomplete, so a typed @name posts as plain text.
	if !strings.Contains(got[0], "linkedin.com/in/sindre") || !strings.Contains(got[0], "@sindre.example") {
		t.Errorf("Mentions = %q", got[0])
	}

	in := testInput()
	in.Talk.Speakers[0].Links = cnd.Links{}
	if got := Mentions(in); len(got) != 0 {
		t.Errorf("Mentions with no links = %v", got)
	}
}

func TestQuoteTitle(t *testing.T) {
	if got := quoteTitle("Pods on Mars", lang.English); got != "“Pods on Mars”" {
		t.Errorf("quoteTitle = %q", got)
	}
	// A title that already quotes itself is not double-quoted.
	if got := quoteTitle("“Already quoted”", lang.English); got != "“Already quoted”" {
		t.Errorf("quoteTitle = %q", got)
	}
	if got := quoteTitle("", lang.English); got != "" {
		t.Errorf("quoteTitle empty = %q", got)
	}
}

func TestHashtagify(t *testing.T) {
	for in, want := range map[string]string{
		"Cloud Infrastructure & Operations": "CloudInfrastructureOperations",
		"Kubernetes & Orchestration":        "KubernetesOrchestration",
		"AI/ML":                             "AIML",
		"":                                  "",
	} {
		if got := hashtagify(in); got != want {
			t.Errorf("hashtagify(%q) = %q, want %q", in, got, want)
		}
	}
}

// The link is the talk's, not a speaker's. Falling back to the first speaker's
// profile read oddly on a multi-speaker talk, where it silently promoted
// whoever happened to be listed first.
func TestLinkPointsAtTheProgramNotASpeaker(t *testing.T) {
	in := testInput()
	in.Talk.Speakers = append(in.Talk.Speakers, withRole(
		speaker(cnd.Speaker{Name: "Second Person", Slug: "second-person"}),
		promo.Role{Employer: "Acme"},
	))

	for _, d := range []Draft{LinkedIn(in), Bluesky(in)} {
		if !strings.Contains(d.Text, "https://2026.cloudnativedays.no/program") {
			t.Errorf("%s: want the program link:\n%s", d.Platform, d.Text)
		}
		if strings.Contains(d.Text, "/speaker/") {
			t.Errorf("%s: post body should not link a speaker profile:\n%s", d.Platform, d.Text)
		}
	}

	// Profiles are still surfaced for the user to turn into real mentions.
	mentions := Mentions(in)
	if len(mentions) == 0 || !strings.Contains(mentions[0], "linkedin.com/in/sindre") {
		t.Errorf("Mentions lost the speaker profiles: %v", mentions)
	}
}

// A conference with no known domain must simply omit the link rather than emit
// a malformed one.
func TestNoDomainOmitsTheLink(t *testing.T) {
	in := testInput()
	in.Conference.Domain = ""
	for _, d := range []Draft{LinkedIn(in), Bluesky(in)} {
		if strings.Contains(d.Text, "http") {
			t.Errorf("%s: emitted a link without a domain:\n%s", d.Platform, d.Text)
		}
		if !strings.Contains(d.Text, "Pods on Mars") {
			t.Errorf("%s: lost the rest of the copy:\n%s", d.Platform, d.Text)
		}
	}
}
