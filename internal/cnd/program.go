package cnd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vehagn/speaker-promos/internal/cache"
	"github.com/vehagn/speaker-promos/internal/rsc"
)

// DefaultDomain is the 2026 conference site. Override with Loader.Domain to
// generate promos for a different edition.
const DefaultDomain = "2026.cloudnativedays.no"

// Loader fetches and decodes a conference program from the public website.
type Loader struct {
	Domain string
	Cache  *cache.Cache
}

// NewLoader returns a Loader for the default domain, caching page fetches for
// the given TTL.
func NewLoader(domain string, ttl time.Duration) *Loader {
	if domain == "" {
		domain = DefaultDomain
	}
	return &Loader{Domain: domain, Cache: cache.New(ttl)}
}

// rawSpeaker/rawTalk/rawSlot/rawDay mirror the shapes found in the program
// page's RSC payload. They are decoded from `any` rather than straight from
// JSON because `$ref` substitution has to happen on the decoded tree first.
type rawSpeaker struct {
	ID    string `json:"_id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Image string `json:"image"`
}

type rawTalk struct {
	ID          string       `json:"_id"`
	Title       string       `json:"title"`
	Description any          `json:"description"`
	Format      string       `json:"format"`
	Level       string       `json:"level"`
	Status      string       `json:"status"`
	Speakers    []rawSpeaker `json:"speakers"`
	Topics      []struct {
		Title string `json:"title"`
	} `json:"topics"`
}

type rawSlot struct {
	StartTime string   `json:"startTime"`
	EndTime   string   `json:"endTime"`
	Talk      *rawTalk `json:"talk"`
}

type rawDay struct {
	Date   string `json:"date"`
	Status string `json:"status"`
	Tracks []struct {
		TrackTitle string    `json:"trackTitle"`
		Talks      []rawSlot `json:"talks"`
	} `json:"tracks"`
}

// Load fetches the program page and decodes the conference and its talks.
func (l *Loader) Load() (*Program, error) {
	url := fmt.Sprintf("https://%s/program", l.Domain)
	html, err := l.Cache.Get(url)
	if err != nil {
		return nil, err
	}

	flight, err := rsc.Chunks(string(html))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	rows := rsc.Rows(flight)

	conf, err := decodeConference(flight, rows, l.Domain)
	if err != nil {
		return nil, err
	}
	talks, err := decodeTalks(flight, rows)
	if err != nil {
		return nil, err
	}
	return &Program{Conference: conf, Talks: talks}, nil
}

func decodeConference(flight string, rows map[string]string, domain string) (Conference, error) {
	// The conference object is nested well above any single distinctive string
	// in it, so it is located by a field name and identified by required keys.
	raw, err := rsc.FindObject(flight, `"startDate":`, "title", "startDate", "city", "logoBright")
	if err != nil {
		return Conference{}, fmt.Errorf("locating conference metadata: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Conference{}, fmt.Errorf("decoding conference metadata: %w", err)
	}

	var c struct {
		Title          string   `json:"title"`
		StartDate      string   `json:"startDate"`
		EndDate        string   `json:"endDate"`
		City           string   `json:"city"`
		Country        string   `json:"country"`
		Domains        []string `json:"domains"`
		LogoBright     string   `json:"logoBright"`
		LogoDark       string   `json:"logoDark"`
		LogomarkBright string   `json:"logomarkBright"`
	}
	resolved, _ := json.Marshal(rsc.Resolve(decoded, rows))
	if err := json.Unmarshal(resolved, &c); err != nil {
		return Conference{}, fmt.Errorf("decoding conference metadata: %w", err)
	}

	// Prefer the domain actually fetched; the payload also lists wildcard and
	// localhost entries that are useless in a public post.
	primary := domain
	if primary == "" {
		for _, d := range c.Domains {
			if !strings.ContainsAny(d, "*") && !strings.HasPrefix(d, "localhost") {
				primary = d
				break
			}
		}
	}

	return Conference{
		Title:          c.Title,
		StartDate:      c.StartDate,
		EndDate:        c.EndDate,
		City:           c.City,
		Country:        c.Country,
		Domain:         primary,
		LogoBright:     svgOrEmpty(c.LogoBright),
		LogoDark:       svgOrEmpty(c.LogoDark),
		LogomarkBright: svgOrEmpty(c.LogomarkBright),
	}, nil
}

// svgOrEmpty drops values that are not inline SVG — an unresolved reference or
// "$undefined" would otherwise be pasted into a card as markup.
func svgOrEmpty(s string) string {
	if strings.HasPrefix(strings.TrimSpace(s), "<svg") {
		return s
	}
	return ""
}

func decodeTalks(flight string, rows map[string]string) ([]Talk, error) {
	raw, err := rsc.FindObject(flight, `"schedules":`, "schedules")
	if err != nil {
		return nil, fmt.Errorf("locating schedule: %w", err)
	}
	var decoded struct {
		Schedules any `json:"schedules"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decoding schedule: %w", err)
	}

	resolved, err := json.Marshal(rsc.Resolve(decoded.Schedules, rows))
	if err != nil {
		return nil, fmt.Errorf("decoding schedule: %w", err)
	}
	var days []rawDay
	if err := json.Unmarshal(resolved, &days); err != nil {
		return nil, fmt.Errorf("decoding schedule: %w", err)
	}

	// Day numbers must follow calendar order, which the payload does not
	// guarantee.
	sort.SliceStable(days, func(i, j int) bool { return days[i].Date < days[j].Date })

	var out []Talk
	for i, d := range days {
		for _, tr := range d.Tracks {
			for _, slot := range tr.Talks {
				// Slots without a talk are breaks, lunches and placeholders.
				if slot.Talk == nil || slot.Talk.Title == "" {
					continue
				}
				talk := convertTalk(*slot.Talk)
				talk.Schedule = Slot{
					Date:      d.Date,
					Day:       i + 1,
					Track:     tr.TrackTitle,
					StartTime: slot.StartTime,
					EndTime:   slot.EndTime,
				}
				out = append(out, talk)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("schedule contained no talks")
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Schedule, out[j].Schedule
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.StartTime != b.StartTime {
			return a.StartTime < b.StartTime
		}
		return a.Track < b.Track
	})
	return out, nil
}

func convertTalk(r rawTalk) Talk {
	speakers := make([]Speaker, 0, len(r.Speakers))
	for _, s := range r.Speakers {
		speakers = append(speakers, Speaker{
			ID:    s.ID,
			Name:  strings.TrimSpace(s.Name),
			Slug:  s.Slug,
			Title: strings.TrimSpace(s.Title),
			Image: s.Image,
		})
	}
	topics := make([]string, 0, len(r.Topics))
	for _, t := range r.Topics {
		if t.Title != "" {
			topics = append(topics, t.Title)
		}
	}
	return Talk{
		ID:       r.ID,
		Title:    strings.TrimSpace(r.Title),
		Abstract: flattenPortableText(r.Description),
		Format:   r.Format,
		Level:    r.Level,
		Status:   r.Status,
		Topics:   topics,
		Speakers: speakers,
	}
}
