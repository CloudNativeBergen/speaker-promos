package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vehagn/speaker-promos/internal/cache"
	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/export"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/raster"
	"github.com/vehagn/speaker-promos/internal/render"
	"github.com/vehagn/speaker-promos/internal/source"
	"github.com/vehagn/speaker-promos/internal/theme"
)

// manifestFlag is the override manifest's path.
type manifestFlag struct{ path string }

func (m *manifestFlag) register(fs *flag.FlagSet) {
	// No backticks in the usage text: the flag package reads those as the
	// value-name placeholder.
	fs.StringVar(&m.path, "manifest", manifest.DefaultPath, "override manifest of corrections")
}

// projectFlags locate everything a program-reading command works from: the
// website, its cached pages, the snapshot of it, and the corrections.
type projectFlags struct {
	manifestFlag
	domain  string
	ttl     time.Duration
	noCache bool
	source  string
}

func (f *projectFlags) register(fs *flag.FlagSet) {
	f.manifestFlag.register(fs)
	fs.StringVar(&f.domain, "domain", cnd.DefaultDomain, "conference site to read the program from")
	fs.DurationVar(&f.ttl, "cache-ttl", 6*time.Hour, "how long a cached page stays fresh")
	fs.BoolVar(&f.noCache, "no-cache", false, "always re-fetch, ignoring the cache")
	fs.StringVar(&f.source, "source", source.DefaultPath, "snapshot of the website's data, updated whenever it changes")
}

// project is what those flags open.
type project struct {
	flags   *projectFlags
	loader  *cnd.Loader
	program *cnd.Program
	snap    *source.Snapshot
	set     *manifest.Set
}

// open loads the corrections and the program, and records the program in the
// website snapshot — which is rewritten only if the website changed.
func (f *projectFlags) open() (*project, error) {
	set, err := manifest.Load(f.path)
	if err != nil {
		return nil, err
	}
	loader := cnd.NewLoader(f.domain, f.ttl)
	loader.Cache.Disabled = f.noCache
	program, err := loader.Load()
	if err != nil {
		return nil, err
	}
	snap, err := source.Load(f.source)
	if err != nil {
		return nil, err
	}
	changed, err := snap.Reconcile(program)
	if err != nil {
		return nil, err
	}
	if changed {
		fmt.Fprintf(os.Stderr, "note: the website's data changed; recorded in %s\n", snap.Path())
	}
	return &project{flags: f, loader: loader, program: program, snap: snap, set: set}, nil
}

// copyFlags choose how talks are worded and where handles come from.
type copyFlags struct {
	noLinks  bool
	language string
}

func (f *copyFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&f.noLinks, "no-links", false,
		"use the handles last recorded in the snapshot rather than fetching speaker pages")
	fs.StringVar(&f.language, "language", "auto", "copy language: auto, en or no")
}

// resolver is the one resolver a command drafts from.
func (p *project) resolver(f copyFlags) (*promo.Resolver, error) {
	language, err := lang.ParseLanguage(f.language)
	if err != nil {
		return nil, err
	}
	fetch := func(sp cnd.Speaker) (cnd.Links, error) {
		links, err := p.loader.SpeakerLinks(sp)
		if err != nil {
			// Handles only suggest mentions, so this never aborts a run; the
			// snapshot's last-known handles are used instead.
			fmt.Fprintf(os.Stderr, "note: could not read %s's profile page: %v\n", sp.Name, err)
		}
		return links, err
	}
	if f.noLinks {
		fetch = nil
	}
	return &promo.Resolver{
		Program: p.program, Set: p.set, Source: p.snap, Language: language,
		Links: p.snap.LinksFunc(fetch),
	}, nil
}

// selectTalks resolves positional selectors, or every talk with --all — and
// says so when --all skipped hidden talks, since a quietly shorter export is
// easy to miss.
func selectTalks(r *promo.Resolver, all bool, selectors []string) ([]promo.Talk, error) {
	talks, skipped, err := r.Select(all, selectors)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "note: skipping %d talk(s) marked hidden in %s\n", skipped, r.Set.Path())
	}
	return talks, nil
}

// cardFlags are how cards are drawn and written, shared by `export` and the
// Export buttons in `serve` so the two produce the same bundles.
type cardFlags struct {
	theme      string
	formats    string
	width      int
	quality    int
	noPhotos   bool
	stripEmoji bool
}

func (f *cardFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.theme, "theme", "", "theme YAML to merge over the built-in theme")
	fs.StringVar(&f.formats, "formats", "svg,png,jpg", "card formats: any of svg, png, jpg")
	fs.IntVar(&f.width, "width", 0, "raster width in pixels (default: the card's own width)")
	fs.IntVar(&f.quality, "jpeg-quality", 88, "JPEG quality, 1-100")
	fs.BoolVar(&f.noPhotos, "no-photos", false, "skip speaker photos (renders initials instead)")
	fs.BoolVar(&f.stripEmoji, "strip-emoji", false, "remove emoji rather than relying on a system emoji font")
}

// exporter builds the bundle writer, renderer included. Sizes is left for the
// caller, which knows which cards it wants.
func (p *project) exporter(f cardFlags, r *promo.Resolver) (*export.Exporter, error) {
	th, err := theme.Load(f.theme)
	if err != nil {
		return nil, err
	}
	formats, err := export.ParseFormats(f.formats)
	if err != nil {
		return nil, err
	}
	var photos *cache.Cache
	if !f.noPhotos {
		// Photos are cached far longer than the program: a portrait does not
		// change between runs, and each one is a separate CDN fetch.
		photos = cache.New(30 * 24 * time.Hour)
		photos.Disabled = p.flags.noCache
	}
	renderer, err := render.New(th, photos)
	if err != nil {
		return nil, err
	}
	renderer.StripEmoji = f.stripEmoji
	conv, _, hasConv := raster.Find()
	return &export.Exporter{
		Renderer: renderer, Resolver: r, Formats: formats,
		RasterWidth: f.width, JPEGQuality: f.quality,
		Converter: conv, HasConverter: hasConv,
	}, nil
}
