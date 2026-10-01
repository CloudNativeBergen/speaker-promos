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
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/raster"
	"github.com/vehagn/speaker-promos/internal/render"
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
		if card.EmojiFallback {
			res.Warnings = append(res.Warnings,
				size+": contains emoji, which Inkscape and librsvg drop")
		}
		for _, el := range card.Overflow {
			res.Warnings = append(res.Warnings, size+": text truncated to fit ("+el+")")
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
	var b strings.Builder
	seen := map[string]bool{}
	for _, d := range drafts {
		for _, n := range d.Notes {
			if !seen[n] {
				seen[n] = true
				if b.Len() == 0 {
					b.WriteString("Check before posting\n====================\n\n")
				}
				fmt.Fprintf(&b, "- %s\n", n)
			}
		}
	}
	if mentions := post.Mentions(in); len(mentions) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Profiles to mention\n===================\n\n")
		for _, m := range mentions {
			fmt.Fprintf(&b, "- %s\n", m)
		}
	}
	return b.String()
}

// writeManifest writes the per-talk record and its editable overrides.
func (e *Exporter) writeManifest(dir string, t promo.Talk, res Result) (string, error) {
	const name = "promo.yaml"
	conf := e.conference()

	info := manifest.TalkInfoSpec{
		Conference: manifest.ConferenceInfo{
			Title: conf.Title, StartDate: conf.StartDate, EndDate: conf.EndDate,
			City: conf.City, Country: conf.Country, Domain: conf.Domain,
			ProgramURL: conf.ProgramURL(),
		},
		Talk: manifest.TalkDetail{
			ID: t.ID, Title: t.Title, Day: t.Schedule.Day, Date: t.Schedule.Date,
			StartTime: t.Schedule.StartTime, EndTime: t.Schedule.EndTime, Track: t.Schedule.Track,
			Format: t.Format, FormatLabel: t.FormatLabel(),
			Level: t.Level, Topics: t.Topics, Abstract: t.Abstract,
		},
		Cards:    cardFiles(res.Files),
		Warnings: res.Warnings,
	}
	// The submitted title is recorded only when an override changed it, so the
	// record shows both.
	if t.SubmittedTitle != t.Title {
		info.Talk.SubmittedTitle = t.SubmittedTitle
	}

	speakers := make([]manifest.BundleSpeaker, 0, len(t.Speakers))
	for _, sp := range t.Speakers {
		info.Speakers = append(info.Speakers, manifest.SpeakerInfo{
			Name:            sp.Name,
			Key:             sp.Key,
			Slug:            sp.Source.Slug,
			SubmittedTitle:  sp.Source.Title,
			Title:           sp.Title,
			Employer:        sp.Role.Employer,
			Job:             sp.Role.Job,
			EmployerGuessed: sp.Role.Guessed,
			Image:           sp.Image,
			// Recorded rather than inferred from Image being set: an URL that
			// fails to fetch also lands on the monogram, and this is the field
			// that tells you an `image:` override would help.
			HasPhoto:   e.Renderer.HasPhoto(sp.Image),
			ProfileURL: conf.SpeakerURL(sp.Source),
			Links:      sp.Links,
		})
		current, _ := e.Resolver.Set.Speaker(sp.Key)
		speakers = append(speakers, manifest.BundleSpeaker{Key: sp.Key, Spec: promo.BundleSpec(current, sp)})
	}

	data, err := e.Resolver.Set.EncodeBundle(info, speakers)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", name, err)
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
