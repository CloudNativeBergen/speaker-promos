package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/progress"
)

func cmdImport(args []string) error {
	fs := newFlagSet("import")
	var manifestPath manifestFlag
	manifestPath.register(fs)
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	force := fs.Bool("force", false,
		"where a bundle and the manifest both changed an override, take the bundle's")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	paths, err := manifest.FindFiles(fs.Args())
	if err != nil {
		return err
	}
	target, err := manifest.Load(manifestPath.path)
	if err != nil {
		return err
	}

	// Each bundle records the revision its overrides were exported at, so the
	// merge needs nothing but the bundle and the manifest — not the program.
	bar := progress.NewBar(os.Stderr)
	results, err := manifest.ImportFiles(target, paths, manifest.ImportOptions{
		Force: *force, DryRun: *dryRun,
	}, bar.Func())
	bar.Clear()

	for _, res := range results {
		if len(res.Changes) == 0 {
			continue
		}
		fmt.Println(relativeTo(res.Path))
		for _, c := range res.Changes {
			fmt.Println("  " + c.String())
		}
		fmt.Println()
	}
	if errors.Is(err, manifest.ErrLegacyBundle) {
		return fmt.Errorf("%w\n(run `promo export` again; your corrections are in %s, not lost)", err, target.Path())
	}
	if err != nil {
		return err
	}

	fmt.Print(manifest.Summary(results, "re-run with --force"))
	if applied, _ := manifest.Tally(results); applied > 0 {
		if *dryRun {
			fmt.Print(" — nothing written (--dry-run)")
		} else {
			fmt.Printf(", written to %s", target.Path())
		}
	}
	fmt.Println()
	return nil
}

// relativeTo shortens a path against the working directory for reporting.
func relativeTo(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
