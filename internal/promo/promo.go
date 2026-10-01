// Package promo resolves a talk into what its promo actually says.
//
// The program page gives a talk as it was submitted; the manifest holds the
// corrections made to it; the speaker pages give handles to mention; and the
// talk's language decides the wording. Every card, draft, bundle and preview
// row needs all four combined, and they used to be combined separately in each
// of those places — keyed differently, in a different order, and so not always
// to the same answer. This package is now the only place that combines them,
// and everything downstream reads a resolved Talk rather than reassembling one.
package promo

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/manifest"
)

// Speaker is one presenter as the promo shows them.
type Speaker struct {
	// Source is the speaker as the website has them. It is what an override is
	// compared against, and what a guessed employer was guessed from.
	Source cnd.Speaker
	// Key is the speaker's identity: the manifest object name, the preview
	// server's URL segment, and the key every per-speaker cache uses.
	Key string
	// Name is the corrected name where there is one.
	Name string
	// Title is the role line the card prints under the names: an override's
	// verbatim title, else one composed from its job and employer, else the
	// website's own title.
	Title string
	// Role is what the copy says about where the speaker works. It is parsed
	// from Source.Title rather than from Title, so a verbatim role line on its
	// own changes the card and leaves the copy guessing — and saying what it
	// guessed from.
	Role Role
	// Image is the photo to draw: an override resolved against the manifest's
	// directory, or the website's.
	Image string
	// Links are the scraped handles with any correction merged over them.
	Links cnd.Links
}

// Values are the speaker's fields as the manifest names them: the values the
// card and copy actually use, which is what an edit form is pre-filled with.
func (sp Speaker) Values() manifest.SpeakerSpec {
	return manifest.SpeakerSpec{
		Name:     sp.Name,
		Employer: sp.Role.Employer,
		Job:      sp.Role.Job,
		Title:    sp.Title,
		Image:    sp.Image,
		Links:    sp.Links,
	}
}

// Talk is a talk as its promo presents it.
type Talk struct {
	// Talk is the talk with its display title applied. Talk.Speakers is left as
	// the website has them; the resolved speakers are Speakers below.
	cnd.Talk
	// SubmittedTitle is the title before any displayTitle override.
	SubmittedTitle string
	// Speakers are the resolved presenters, in program order.
	Speakers []Speaker
	// Language is what the card and copy are worded in: a per-talk override,
	// else the run's default, else detection. Never lang.Auto.
	Language lang.Language
	// Detected is what detection alone picks, so an "auto" choice can say what
	// it means for this talk.
	Detected lang.Language
	// Hidden excludes the talk from bulk operations.
	Hidden bool
}

// SpeakerNames renders the speakers' names as prose in the talk's language:
// "A og B" on a Norwegian talk.
//
// The language is resolved again here only for a Talk built by hand; one from
// a Resolver already carries a concrete language and this is a lookup.
func (t Talk) SpeakerNames() string {
	names := make([]string, 0, len(t.Speakers))
	for _, sp := range t.Speakers {
		if sp.Name != "" {
			names = append(names, sp.Name)
		}
	}
	return cnd.JoinAnd(names, t.Language.Resolve(t.Title, t.Abstract).Words().And)
}

// Resolver turns program talks into promo talks.
type Resolver struct {
	Program *cnd.Program
	// Set holds the corrections. Nil resolves a talk as submitted, which is
	// what the renderer's and the copy's own tests want.
	Set *manifest.Set
	// Language is the run's default, or lang.Auto to detect per talk. A per-talk
	// override in the manifest beats it.
	Language lang.Language
	// Links supplies a speaker's scraped handles. It is a callback because the
	// fetching policy is the caller's: the server caches across requests, and
	// the CLI may be told to skip it. Nil means none.
	Links func(cnd.Speaker) cnd.Links
}

// Talk resolves one talk against the manifest as it stands now.
func (r *Resolver) Talk(src cnd.Talk) Talk {
	var spec manifest.TalkSpec
	if r.Set != nil {
		spec, _ = r.Set.Talk(src.ID)
	}
	t := Talk{Talk: src, SubmittedTitle: src.Title, Hidden: spec.Hidden}
	if title := strings.TrimSpace(spec.DisplayTitle); title != "" {
		t.Title = title
	}
	t.Detected = lang.Detect(t.Title, t.Abstract)
	t.Language = r.language(spec).Resolve(t.Title, t.Abstract)

	t.Speakers = make([]Speaker, 0, len(src.Speakers))
	for _, sp := range src.Speakers {
		t.Speakers = append(t.Speakers, r.speaker(sp))
	}
	return t
}

// ByID resolves the talk with the given id.
func (r *Resolver) ByID(id string) (Talk, bool) {
	src, ok := r.Program.Talk(id)
	if !ok {
		return Talk{}, false
	}
	return r.Talk(src), true
}

// All resolves every talk in the program, hidden ones included.
func (r *Resolver) All() []Talk {
	out := make([]Talk, 0, len(r.Program.Talks))
	for _, t := range r.Program.Talks {
		out = append(out, r.Talk(t))
	}
	return out
}

// Select resolves positional selectors, or every talk with all.
//
// A hidden talk is dropped from all but kept when named explicitly: asking for
// a talk by name and being told no talk matches would be a worse surprise than
// rendering one that was meant to be skipped in bulk. skipped says how many
// were dropped, so the caller can say so.
func (r *Resolver) Select(all bool, selectors []string) (talks []Talk, skipped int, err error) {
	if all {
		if len(selectors) > 0 {
			return nil, 0, errors.New("--all takes no selectors")
		}
		for _, t := range r.All() {
			if t.Hidden {
				skipped++
				continue
			}
			talks = append(talks, t)
		}
		return talks, skipped, nil
	}
	if len(selectors) == 0 {
		return nil, 0, errors.New("give at least one selector, or --all")
	}

	seen := make(map[string]bool)
	for _, sel := range selectors {
		hits := r.Program.Find(sel)
		if len(hits) == 0 {
			return nil, 0, fmt.Errorf("no talk matches %q", sel)
		}
		for _, h := range hits {
			if !seen[h.ID] {
				seen[h.ID] = true
				talks = append(talks, r.Talk(h))
			}
		}
	}
	return talks, 0, nil
}

// language is the precedence every caller needs: a per-talk override beats the
// run's default, which beats detection.
func (r *Resolver) language(spec manifest.TalkSpec) lang.Language {
	// Already validated at load; a bad value here can only come from a
	// programmatic Set and falls through to the default rather than failing.
	if l, err := lang.ParseLanguage(spec.Language); err == nil && l != lang.Auto {
		return l
	}
	return r.Language
}

// speaker applies a speaker's override.
func (r *Resolver) speaker(src cnd.Speaker) Speaker {
	sp := Speaker{
		Source: src,
		Key:    src.Key(),
		Name:   src.Name,
		Title:  src.Title,
		Role:   ParseRole(src.Title),
		Image:  src.Image,
	}
	var scraped cnd.Links
	if r.Links != nil {
		scraped = r.Links(src)
	}

	var spec manifest.SpeakerSpec
	ok := false
	if r.Set != nil {
		spec, ok = r.Set.Speaker(sp.Key)
	}
	if !ok {
		sp.Links = scraped
		return sp
	}
	if name := strings.TrimSpace(spec.Name); name != "" {
		sp.Name = name
	}
	if line := RoleLine(spec); line != "" {
		sp.Title = line
	}
	if image := r.image(spec.Image); image != "" {
		sp.Image = image
	}
	// An overridden employer is no longer a guess, so the copy stops asking for
	// it to be checked.
	if spec.Employer != "" {
		sp.Role.Employer = spec.Employer
		sp.Role.Guessed = false
	}
	if spec.Job != "" {
		sp.Role.Job = spec.Job
	}
	sp.Links = scraped.Merge(spec.Links.Normalize())
	return sp
}

// image turns an override's image value into something a renderer can fetch.
//
// A relative path is resolved against the manifest's own directory rather than
// the process working directory, so a bundle that carries a photo next to its
// promo.yaml keeps working whatever directory the tool is run from.
func (r *Resolver) image(image string) string {
	image = strings.TrimSpace(image)
	if image == "" || cnd.IsRemoteImage(image) || filepath.IsAbs(image) {
		return image
	}
	if dir := filepath.Dir(r.Set.Path()); dir != "" && dir != "." {
		return filepath.Join(dir, image)
	}
	return image
}

// Baseline is what the tool produces for a speaker with no override at all:
// their upstream name, title and photo, the employer and job guessed out of
// that title, and the handles scraped from their profile page.
//
// It is what a submitted correction is compared against, so that touching one
// field cannot quietly record the rest of the found values as corrections too.
func Baseline(sp cnd.Speaker, links cnd.Links) manifest.SpeakerSpec {
	role := ParseRole(sp.Title)
	return manifest.SpeakerSpec{
		Name:     sp.Name,
		Employer: role.Employer,
		Job:      role.Job,
		Title:    sp.Title,
		Image:    sp.Image,
		Links:    links,
	}
}

// BundleSpec is the speaker override an exported bundle carries: the current
// correction, pre-filled with the values in use so that fixing one is editing a
// line rather than knowing the field exists.
//
// It pre-fills exactly what ImportBaseline dismisses — the name and the
// employer and job — so an unedited bundle imports as nothing. Title is
// deliberately left out: it is an escape hatch, and pre-filling it would freeze
// the composed job-and-employer line and stop those two from doing anything.
func BundleSpec(current manifest.SpeakerSpec, sp Speaker) manifest.SpeakerSpec {
	if current.Name == "" {
		current.Name = sp.Source.Name
	}
	if current.Employer == "" && current.Job == "" {
		current.Employer, current.Job = sp.Role.Employer, sp.Role.Job
	}
	return current
}

// ImportBaseline builds the import options that make a merge mean "take the
// edits": the program supplies what the tool would say with no overrides at
// all, so a pre-filled guess coming back unchanged is not mistaken for a
// correction.
//
// The speaker baseline is deliberately narrower than Baseline: a bundle
// pre-fills the name and the guessed employer and job, but never the role line,
// the photo or the handles, so a value in one of those is always something
// someone typed and is taken as an edit.
func ImportBaseline(program *cnd.Program) manifest.ImportOptions {
	speakers := map[string]manifest.SpeakerSpec{}
	for _, sp := range program.Speakers() {
		spec := Baseline(sp, cnd.Links{})
		spec.Title, spec.Image = "", ""
		speakers[sp.Key()] = spec
	}
	titles := map[string]string{}
	for _, t := range program.Talks {
		titles[t.ID] = t.Title
	}
	return manifest.ImportOptions{
		SpeakerBaseline: func(key string) (manifest.SpeakerSpec, bool) {
			spec, ok := speakers[key]
			return spec, ok
		},
		TalkBaseline: func(id string) (string, bool) {
			title, ok := titles[id]
			return title, ok
		},
	}
}
