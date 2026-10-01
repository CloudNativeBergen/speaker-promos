package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vehagn/speaker-promos/internal/export"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/raster"
	"github.com/vehagn/speaker-promos/internal/render"
	"github.com/vehagn/speaker-promos/internal/theme"
)

func cmdExport(args []string) error {
	fs := newFlagSet("export")
	var common commonFlags
	common.register(fs)
	var manifestPath manifestFlag
	manifestPath.register(fs)
	all := fs.Bool("all", false, "export every talk in the program")
	out := fs.String("out", "out", "directory to write the per-talk folders into")
	sizes := fs.String("size", "portrait", "card sizes: portrait, landscape, or both")
	formats := fs.String("formats", "svg,png,jpg", "card formats: any of svg, png, jpg")
	themePath := fs.String("theme", "", "theme YAML to merge over the built-in theme")
	noPhotos := fs.Bool("no-photos", false, "skip speaker photos (renders initials instead)")
	noLinks := fs.Bool("no-links", false, "skip fetching speaker pages for social handles")
	stripEmoji := fs.Bool("strip-emoji", false, "remove emoji rather than relying on a system emoji font")
	width := fs.Int("width", 0, "raster width in pixels (default: the card's own width)")
	quality := fs.Int("jpeg-quality", 88, "JPEG quality, 1-100")
	language := fs.String("language", "auto", "copy language: auto, en or no")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	th, err := theme.Load(*themePath)
	if err != nil {
		return err
	}
	wantSizes, err := resolveSizes(th, *sizes)
	if err != nil {
		return err
	}
	wantFormats, err := export.ParseFormats(*formats)
	if err != nil {
		return err
	}
	copyLang, err := lang.ParseLanguage(*language)
	if err != nil {
		return err
	}

	set, err := manifestPath.load()
	if err != nil {
		return err
	}
	loader := common.loader()
	program, err := loader.Load()
	if err != nil {
		return err
	}
	resolver := &promo.Resolver{
		Program:  program,
		Set:      set,
		Language: copyLang,
		Links:    linkFetcher(loader, *noLinks, nil),
	}
	talks, err := selectTalks(resolver, *all, fs.Args())
	if err != nil {
		return err
	}

	renderer, err := render.New(th, common.photoCache(!*noPhotos))
	if err != nil {
		return err
	}
	renderer.StripEmoji = *stripEmoji

	conv, path, hasConv := raster.Find()
	needsRaster := len(wantFormats) > 1 || wantFormats[0] != export.FormatSVG
	if needsRaster && hasConv {
		fmt.Printf("rasterising with %s (%s)\n", conv.Name, path)
		if !conv.EmbedsFonts {
			fmt.Printf("note: %s ignores the cards' embedded fonts — run `promo fonts install` first\n", conv.Name)
		}
	}

	exporter := &export.Exporter{
		Renderer:     renderer,
		Resolver:     resolver,
		Formats:      wantFormats,
		Sizes:        wantSizes,
		RasterWidth:  *width,
		JPEGQuality:  *quality,
		Converter:    conv,
		HasConverter: hasConv,
	}

	var warned []string
	files := 0
	for _, t := range talks {
		res, err := exporter.Write(*out, t)
		if err != nil {
			return err
		}
		files += len(res.Files)
		fmt.Printf("%s/  %s\n", res.Dir, strings.Join(res.Files, " "))
		for _, w := range res.Warnings {
			warned = append(warned, res.Dir+": "+w)
		}
	}

	fmt.Printf("\n%d talks, %d files under %s/\n", len(talks), files, *out)
	if needsRaster && !hasConv {
		fmt.Fprint(os.Stderr, "\n"+raster.NoConverterMessage)
	}
	if len(warned) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d warning(s):\n", len(warned))
		for _, w := range warned {
			fmt.Fprintln(os.Stderr, "  "+w)
		}
	}
	return nil
}

// resolveSizes expands the --size flag against the theme's defined sizes.
func resolveSizes(th *theme.Theme, spec string) ([]string, error) {
	if spec == "both" || spec == "all" {
		return th.Sizes(), nil
	}
	var out []string
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := th.Size(name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no card size selected")
	}
	return out, nil
}
