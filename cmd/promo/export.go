package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/vehagn/speaker-promos/internal/export"
	"github.com/vehagn/speaker-promos/internal/progress"
	"github.com/vehagn/speaker-promos/internal/raster"
	"github.com/vehagn/speaker-promos/internal/theme"
)

func cmdExport(args []string) error {
	fs := newFlagSet("export")
	var pf projectFlags
	var cf copyFlags
	var kf cardFlags
	pf.register(fs)
	cf.register(fs)
	kf.register(fs)
	all := fs.Bool("all", false, "export every talk in the program")
	out := fs.String("out", "out", "directory to write the per-talk folders into")
	sizes := fs.String("size", "portrait", "card sizes: portrait, landscape, or both")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	p, err := pf.open()
	if err != nil {
		return err
	}
	resolver, err := p.resolver(cf)
	if err != nil {
		return err
	}
	exporter, err := p.exporter(kf, resolver)
	if err != nil {
		return err
	}
	if exporter.Sizes, err = resolveSizes(exporter.Renderer.Theme, *sizes); err != nil {
		return err
	}
	talks, err := selectTalks(resolver, *all, fs.Args())
	if err != nil {
		return err
	}

	conv := exporter.Converter
	needsRaster := !slices.Equal(exporter.Formats, []string{export.FormatSVG})
	if needsRaster && exporter.HasConverter {
		fmt.Printf("rasterising with %s\n", conv.Name)
		if !conv.EmbedsFonts {
			fmt.Printf("note: %s ignores the cards' embedded fonts — run `promo fonts install` first\n", conv.Name)
		}
	}

	bar := progress.NewBar(os.Stderr)
	results, err := exporter.WriteAll(*out, talks, bar.Func())
	bar.Clear()

	for _, res := range results {
		fmt.Printf("%s/  %s\n", res.Dir, strings.Join(res.Files, " "))
	}
	files, warned := export.Totals(results)

	if err != nil {
		return err
	}

	fmt.Printf("\n%d talks, %d files under %s/\n", len(talks), files, *out)
	if needsRaster && !exporter.HasConverter {
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
