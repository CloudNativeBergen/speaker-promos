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
	target, err := manifestPath.load()
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

	var applied, conflicts int
	for _, res := range results {
		if len(res.Changes) == 0 {
			continue
		}
		fmt.Printf("%s\n", relativeTo(res.Path))
		for _, c := range res.Changes {
			fmt.Println("  " + c.String())
			if c.Conflict {
				conflicts++
			} else {
				applied++
			}
		}
		fmt.Println()
	}
	if errors.Is(err, manifest.ErrLegacyBundle) {
		return fmt.Errorf("%w\n(run `promo export` again; your corrections are in %s, not lost)", err, target.Path())
	}
	if err != nil {
		return err
	}

	switch {
	case applied == 0 && conflicts == 0:
		fmt.Printf("nothing to import from %d file(s): no bundle was edited since it was exported\n", len(paths))
	case *dryRun:
		fmt.Printf("%d change(s) from %d file(s) — nothing written (--dry-run)\n", applied, len(paths))
	default:
		fmt.Printf("%d change(s) from %d file(s) written to %s\n", applied, len(paths), target.Path())
	}
	if conflicts > 0 {
		fmt.Printf("%d conflict(s) left as the manifest has them: changed in both places since the export.\n"+
			"Re-run with --force to take the bundles' version.\n", conflicts)
	}
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
