package cnd

import (
	"fmt"
	"regexp"
	"strings"
)

// Links are a speaker's public profiles, for @-mentions in social copy.
//
// The website stores these as a plain list of URLs on the speaker; this is that
// list sorted into the platforms the copy can use. The yaml tags are the
// manifest's field names, so a correction is written in the same shape.
type Links struct {
	LinkedIn string `yaml:"linkedin,omitempty"` // profile URL
	Bluesky  string `yaml:"bluesky,omitempty"`  // handle, e.g. "dario.bsky.social"
	X        string `yaml:"x,omitempty"`        // handle without "@"
	GitHub   string `yaml:"github,omitempty"`   // username
}

// Empty reports whether no links were found.
func (l Links) Empty() bool { return l == Links{} }

// Normalize strips the "@" a handle may be written with — the way people say
// it — so that it is not doubled when a post adds its own.
func (l Links) Normalize() Links {
	l.Bluesky = strings.TrimPrefix(strings.TrimSpace(l.Bluesky), "@")
	l.X = strings.TrimPrefix(strings.TrimSpace(l.X), "@")
	return l
}

// Merge returns l with every non-empty field of over replacing it, which is how
// a correction wins over what was scraped.
func (l Links) Merge(over Links) Links {
	if over.LinkedIn != "" {
		l.LinkedIn = over.LinkedIn
	}
	if over.Bluesky != "" {
		l.Bluesky = over.Bluesky
	}
	if over.GitHub != "" {
		l.GitHub = over.GitHub
	}
	if over.X != "" {
		l.X = over.X
	}
	return l
}

var linkPatterns = []struct {
	re   *regexp.Regexp
	pick func(*Links, string)
}{
	{regexp.MustCompile(`https?://(?:www\.)?linkedin\.com/in/([A-Za-z0-9_%\-.]+)`),
		func(l *Links, v string) { l.LinkedIn = "https://www.linkedin.com/in/" + strings.TrimRight(v, ".") }},
	{regexp.MustCompile(`https?://bsky\.app/profile/([A-Za-z0-9_.\-]+)`),
		func(l *Links, v string) { l.Bluesky = strings.TrimRight(v, ".") }},
	{regexp.MustCompile(`https?://(?:www\.)?github\.com/([A-Za-z0-9_\-]+)`),
		func(l *Links, v string) { l.GitHub = v }},
	{regexp.MustCompile(`https?://(?:www\.)?(?:x|twitter)\.com/([A-Za-z0-9_]+)`),
		func(l *Links, v string) { l.X = v }},
}

// organiserAccounts are the conference's own profiles. Every speaker page links
// them in its footer, so without this filter each speaker would appear to have
// the organisers' handles.
var organiserAccounts = map[string]bool{
	"cloudnativebergen":     true,
	"cloudnativebergen.dev": true,
	"cloud-native-bergen":   true,
	"cloudnativedays":       true,
	"cloudnativedays.no":    true,
	"cloud-native-days":     true,
	"cloudnativedaysnorway": true,
	"cncf":                  true,
}

// SpeakerLinks scrapes a speaker's public profile page for social links.
//
// The speaker page is server-rendered with no JSON payload — unlike the program
// page, its data is not recoverable from an RSC row — so the links are read out
// of the HTML. That makes this the most fragile part of the tool, and it is
// used only for optional @-mention suggestions in draft copy: a speaker with no
// detectable links simply gets none, and the copy still works.
func (l *Loader) SpeakerLinks(s Speaker) (Links, error) {
	if s.Slug == "" {
		return Links{}, nil
	}
	url := fmt.Sprintf("https://%s/speaker/%s", l.Domain, s.Slug)
	body, err := l.Cache.Get(url)
	if err != nil {
		return Links{}, err
	}
	return parseLinks(string(body)), nil
}

func parseLinks(html string) Links {
	var links Links
	for _, p := range linkPatterns {
		for _, m := range p.re.FindAllStringSubmatch(html, -1) {
			handle := m[1]
			if organiserAccounts[strings.ToLower(strings.TrimRight(handle, "/"))] {
				continue
			}
			p.pick(&links, handle)
			break
		}
	}
	return links
}
