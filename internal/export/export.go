// Package export writes one folder per talk holding everything needed to post
// it: the card in each requested format, the draft copy, and the manifest that
// produced them.
//
// It is shared by `promo export` and the preview server's Export button so the
// two cannot drift — a bundle downloaded from the browser and one written from
// the terminal are the same bundle.
package export

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/post"
	"github.com/vehagn/speaker-promos/internal/progress"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/raster"
	"github.com/vehagn/speaker-promos/internal/render"
	"github.com/vehagn/speaker-promos/internal/theme"
)

// Formats a bundle can contain.
const (
	FormatSVG = "svg"
	FormatPNG = "png"
	FormatJPG = "jpg"
)

// AllFormats is the default set.
var AllFormats = []string{FormatSVG, FormatPNG, FormatJPG}

// ParseFormats validates a comma-separated format list.
func ParseFormats(spec string) ([]string, error) {
	if strings.TrimSpace(spec) == "" || spec == "all" {
		return slices.Clone(AllFormats), nil
	}
	var out []string
	for _, f := range strings.Split(spec, ",") {
		f = strings.ToLower(strings.TrimSpace(f))
		switch f {
		case "":
			continue
		case "jpeg":
			f = FormatJPG
		}
		if !slices.Contains(AllFormats, f) {
			return nil, fmt.Errorf("unknown format %q (want svg, png or jpg)", f)
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no format selected")
	}
	// Canonical order, so a bundle's contents do not depend on flag order.
	slices.SortFunc(out, func(a, b string) int {
		return slices.Index(AllFormats, a) - slices.Index(AllFormats, b)
	})
	return out, nil
}

// Exporter writes bundles.
type Exporter struct {
	Renderer *render.Renderer
	// Resolver applies the manifest, the scraped handles and the run's
	// language, so a bundle says exactly what the preview of it does.
	Resolver *promo.Resolver

	// Formats and Sizes select what each bundle contains.
	Formats []string
	Sizes   []string
	// RasterWidth overrides the card's own pixel width; 0 keeps it.
	RasterWidth int
	// JPEGQuality defaults to 88 when zero.
	JPEGQuality int

	// Converter rasterises SVG. When HasConverter is false only SVG, copy and
	// manifest are written — a missing rasteriser must not cost you the rest
	// of the bundle.
	Converter    raster.Converter
	HasConverter bool
}

// Result reports what one bundle produced.
type Result struct {
	// Dir is the bundle folder, relative to the export root.
	Dir string
	// Files written, in the order they were created.
	Files []string
	// Warnings are per-talk notes worth surfacing: truncated text, emoji that
	// some renderers drop, or a raster step that failed.
	Warnings []string
}

// WriteAll writes a bundle per talk, reporting progress after each. It is the
// whole of `promo export` and of the preview server's Export buttons.
//
// It stops at the first talk that fails; the bundles before it are written.
func (e *Exporter) WriteAll(root string, talks []promo.Talk, report progress.Func) ([]Result, error) {
	out := make([]Result, 0, len(talks))
	for i, t := range talks {
		res, err := e.Write(root, t)
		if err != nil {
			return out, err
		}
		out = append(out, res)
		report.Report(i+1, len(talks), res.Dir)
	}
	return out, nil
}

// Totals counts the files a run wrote, and lists its warnings, each led by the
// bundle it belongs to.
func Totals(results []Result) (files int, warnings []string) {
	for _, res := range results {
		files += len(res.Files)
		for _, w := range res.Warnings {
			warnings = append(warnings, res.Dir+": "+w)
		}
	}
	return files, warnings
}

// Write produces one talk's bundle under root. The talk comes from the same
// Resolver, so the record and the copy agree with the cards about every
// correction.
func (e *Exporter) Write(root string, t promo.Talk) (Result, error) {
	res := Result{Dir: t.FileStem()}
	dir := filepath.Join(root, res.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("creating %s: %w", dir, err)
	}

	wantSVG := slices.Contains(e.Formats, FormatSVG)
	wantPNG := slices.Contains(e.Formats, FormatPNG)
	wantJPG := slices.Contains(e.Formats, FormatJPG)

	for _, size := range e.Sizes {
		card, err := e.Renderer.Card(e.conference(), t, size)
		if err != nil {
			return res, fmt.Errorf("rendering %q at %s: %w", t.Title, size, err)
		}
		for _, w := range card.Warnings() {
			res.Warnings = append(res.Warnings, size+": "+w)
		}

		svgPath := filepath.Join(dir, size+".svg")
		if err := os.WriteFile(svgPath, []byte(card.SVG), 0o644); err != nil {
			return res, fmt.Errorf("writing %s: %w", svgPath, err)
		}
		// A raster format still needs an SVG on disk to convert from, so it is
		// written either way and removed afterwards when unwanted.
		keepSVG := wantSVG
		if keepSVG {
			res.Files = append(res.Files, size+".svg")
		}

		if (wantPNG || wantJPG) && e.HasConverter {
			pngPath := filepath.Join(dir, size+".png")
			width := e.RasterWidth
			if width <= 0 {
				width = card.Width
			}
			if err := e.Converter.PNG(svgPath, pngPath, width); err != nil {
				// A failed conversion is reported and the bundle continues:
				// losing the copy and manifest because one tool misbehaved
				// would be a poor trade.
				res.Warnings = append(res.Warnings, size+": "+err.Error())
			} else {
				if wantJPG {
					jpgPath := filepath.Join(dir, size+".jpg")
					if err := raster.JPEG(pngPath, jpgPath, e.JPEGQuality); err != nil {
						res.Warnings = append(res.Warnings, size+": "+err.Error())
					} else {
						res.Files = append(res.Files, size+".jpg")
					}
				}
				if wantPNG {
					res.Files = append(res.Files, size+".png")
				} else {
					os.Remove(pngPath)
				}
			}
		}

		if !keepSVG {
			os.Remove(svgPath)
		}
	}

	in := post.Input{Conference: e.conference(), Talk: t}
	copyFiles, err := e.writeCopy(dir, in)
	if err != nil {
		return res, err
	}
	res.Files = append(res.Files, copyFiles...)

	// Written last, so it can record which card files the run actually produced.
	yamlName, err := e.writeManifest(dir, t, res)
	if err != nil {
		return res, err
	}
	res.Files = append(res.Files, yamlName)
	return res, nil
}

// writeCopy writes the draft post for each platform, and the notes beside them.
//
// The drafts are built once and used for both: they are not cheap, and asking
// for them twice invited the copy and its own review notes to disagree.
func (e *Exporter) writeCopy(dir string, in post.Input) ([]string, error) {
	drafts := []post.Draft{post.LinkedIn(in), post.Bluesky(in)}

	var written []string
	for _, d := range drafts {
		name := d.Platform + ".txt"
		// The file holds the post body and nothing else, so it can be pasted
		// verbatim. The "check before posting" notes go in NOTES.txt, where
		// they cannot end up in a published post by accident.
		if err := os.WriteFile(filepath.Join(dir, name), []byte(d.Text+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", name, err)
		}
		written = append(written, name)
	}

	if notes := collectNotes(drafts, in); notes != "" {
		const name = "NOTES.txt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(notes), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", name, err)
		}
		written = append(written, name)
	}
	return written, nil
}

// collectNotes gathers the checks and mentions for a talk into one file.
func collectNotes(drafts []post.Draft, in post.Input) string {
	var checks []string
	for _, d := range drafts {
		checks = append(checks, d.Notes...)
	}
	checks = append(checks, in.Talk.StaleNotes()...)

	var b strings.Builder
	section := func(heading string, items []string) {
		if len(items) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s\n%s\n\n", heading, strings.Repeat("=", len(heading)))
		for _, item := range items {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	section("Check before posting", compactUnique(checks))
	section("Profiles to mention", post.Mentions(in))
	return b.String()
}

// compactUnique drops repeats, keeping first occurrences in order: both drafts
// raise the same guessed employer, and it needs checking once.
func compactUnique(items []string) []string {
	seen := map[string]bool{}
	return slices.DeleteFunc(items, func(s string) bool {
		dup := seen[s]
		seen[s] = true
		return dup
	})
}

// bundleHeader introduces a bundle's promo.yaml.
const bundleHeader = `# Everything that produced the cards in this folder, in three parts.
#
#   Source           what the website said. A record: edits here do nothing.
#   TalkOverride     your corrections — the only part an import takes. Each
#   SpeakerOverride  speaker on the talk has one, empty until you correct them.
#   Output           what the cards and copy actually used. A record as well;
#                    copy a value from here into an override to start from it.
#
# Edit the overrides and run "promo import" on this file or its folder, or use
# Import in "promo serve". Leave metadata.revision alone: it is how an import
# tells your edit here from a later edit to the project, and reports a
# conflict rather than overwriting one.
#
# SpeakerOverride fields:
#   name       the CMS is where people typed their own name, so accents go
#              missing. Correcting it does not rename this folder.
#   employer   what the post names; job is the role before it.
#   job
#   title      sets the card's role line verbatim, for roles that do not fit
#              "<job> at <employer>". Employer still drives what the post says.
#   image      a URL, or a file next to this manifest. A speaker with no photo
#              renders a monogram.
#   links      linkedin, bluesky, x, github.
#   photoX     which part of the photo its square shows: -1 its left edge to
#   photoY     1 its right (photoY: top to bottom); photoZoom enlarges it,
#   photoZoom  from 1. They follow the photo to every talk the speaker is on.
#
# TalkOverride fields:
#   displayTitle  the title on the card and in the copy.
#   language      en or no; omit to detect.
#   hidden        true to leave the talk out of bulk exports.
#   posted        the date the promo went out, YYYY-MM-DD.
#   card          this card's sliders, over the project's CardDefaults:
#                 titleScale, nameScale, photoScale, spacing (0.5 to 2), and
#                 gradientFrom, gradientTo (colours).
`

// Output is the spec of a bundle's Output document: what the cards and copy
// actually used, once every correction was applied.
type Output struct {
	Conference OutputConference `yaml:"conference"`
	Talk       OutputTalk       `yaml:"talk"`
	Speakers   []OutputSpeaker  `yaml:"speakers"`
	// Cards lists the image files written beside this manifest.
	Cards []string `yaml:"cards,omitempty"`
	// Warnings is what the render reported: truncated text, or emoji that some
	// renderers drop.
	Warnings []string `yaml:"warnings,omitempty"`
	// Stale names the speakers whose correction predates a website change.
	Stale []string `yaml:"stale,omitempty"`
}

// OutputConference is the event a bundle belongs to.
type OutputConference struct {
	Title      string `yaml:"title"`
	Dates      string `yaml:"dates,omitempty"`
	Location   string `yaml:"location,omitempty"`
	ProgramURL string `yaml:"programUrl,omitempty"`
}

// OutputTalk is the talk as the cards present it.
type OutputTalk struct {
	Title string `yaml:"title"`
	// Language is what the copy is worded in; Detected what detection alone
	// would have picked.
	Language    string `yaml:"language"`
	Detected    string `yaml:"detectedLanguage"`
	Slot        string `yaml:"slot,omitempty"`
	FormatLabel string `yaml:"formatLabel,omitempty"`
	Hidden      bool   `yaml:"hidden,omitempty"`
	Posted      string `yaml:"posted,omitempty"`
	// Card is the adjustments the card was drawn with, defaults included.
	Card theme.Adjust `yaml:"card,omitempty"`
}

// OutputSpeaker is one speaker as the card and copy show them.
type OutputSpeaker struct {
	Key  string `yaml:"key"`
	Name string `yaml:"name"`
	// Title is the role line the card printed.
	Title    string `yaml:"title,omitempty"`
	Employer string `yaml:"employer,omitempty"`
	Job      string `yaml:"job,omitempty"`
	// EmployerGuessed marks an employer parsed from the website's title rather
	// than taken from an override.
	EmployerGuessed bool   `yaml:"employerGuessed,omitempty"`
	Image           string `yaml:"image,omitempty"`
	// HasPhoto is false when the card fell back to a monogram, which is the
	// case an `image:` override exists to fix. It reports whether the photo
	// was actually fetched, so an URL that 404s shows up here too.
	HasPhoto   bool      `yaml:"hasPhoto"`
	ProfileURL string    `yaml:"profileUrl,omitempty"`
	Links      cnd.Links `yaml:"links,omitempty"`
}

// writeManifest writes the bundle's promo.yaml: Source, overrides, Output.
func (e *Exporter) writeManifest(dir string, t promo.Talk, res Result) (string, error) {
	const name = "promo.yaml"
	conf := e.conference()

	out := Output{
		Conference: OutputConference{
			Title: conf.Title, Dates: conf.DateRange(), Location: conf.Location(),
			ProgramURL: conf.ProgramURL(),
		},
		Talk: OutputTalk{
			Title: t.Title, Language: string(t.Language), Detected: string(t.Detected),
			Slot:        t.Schedule.Label(),
			FormatLabel: t.FormatLabel(), Hidden: t.Hidden, Posted: t.Posted, Card: t.Card,
		},
		Cards:    cardFiles(res.Files),
		Warnings: res.Warnings,
		Stale:    t.StaleSpeakers(),
	}
	keys := make([]string, 0, len(t.Speakers))
	for _, sp := range t.Speakers {
		keys = append(keys, sp.Key)
		out.Speakers = append(out.Speakers, OutputSpeaker{
			Key:             sp.Key,
			Name:            sp.Name,
			Title:           sp.Title,
			Employer:        sp.Role.Employer,
			Job:             sp.Role.Job,
			EmployerGuessed: sp.Role.Guessed,
			Image:           sp.Image,
			HasPhoto:        e.Renderer.HasPhoto(sp.Image),
			ProfileURL:      conf.SpeakerURL(sp.Source),
			Links:           sp.Links,
		})
	}

	// The source talk is the one the website gave, not the display title.
	src := t.Talk
	src.Title = t.SubmittedTitle
	docs := []manifest.Document{e.Resolver.Source.Document(src)}
	docs = append(docs, e.Resolver.Set.BundleOverrides(t.ID, keys)...)
	// LastChanged rather than the time of the export, so an unchanged
	// re-export is byte-identical and a bundle in git does not churn.
	docs = append(docs, manifest.Doc(manifest.KindOutput,
		manifest.Metadata{Name: t.ID, UpdatedAt: t.LastChanged()}, out))

	if err := manifest.WriteFile(filepath.Join(dir, name), bundleHeader, docs); err != nil {
		return "", err
	}
	return name, nil
}

// cardFiles keeps just the image files from a bundle's file list.
func cardFiles(files []string) []string {
	var out []string
	for _, f := range files {
		switch filepath.Ext(f) {
		case ".svg", ".png", ".jpg":
			out = append(out, f)
		}
	}
	return out
}

// conference is the event a bundle belongs to.
func (e *Exporter) conference() cnd.Conference {
	if e.Resolver.Program == nil {
		return cnd.Conference{}
	}
	return e.Resolver.Program.Conference
}
