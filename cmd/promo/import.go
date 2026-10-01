package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/promo"
)

func cmdImport(args []string) error {
	fs := newFlagSet("import")
	var common commonFlags
	common.register(fs)
	var manifestPath manifestFlag
	manifestPath.register(fs)
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	confirm := fs.Bool("confirm-guesses", false,
		"also import the pre-filled guesses, accepting them as correct")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	paths, err := manifest.FindFiles(fs.Args())
	if err != nil {
		return err
	}
	fmt.Printf("importing %d file(s)\n", len(paths))

	target, err := manifestPath.load()
	if err != nil {
		return err
	}
	// The program supplies the baseline: what the tool would say with no
	// overrides at all. Without it an import cannot tell an edit from a
	// pre-filled guess coming back unchanged.
	program, err := common.load()
	if err != nil {
		return err
	}
	opts := promo.ImportBaseline(program)
	opts.ConfirmGuesses = *confirm
	opts.DryRun = *dryRun

	var all []manifest.Change
	for _, path := range paths {
		src, err := manifest.Load(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		changes, err := target.ImportFrom(src, opts)
		if err != nil {
			return err
		}
		if len(changes) > 0 {
			rel := relativeTo(path)
			fmt.Printf("\n%s\n", rel)
			manifest.SortChanges(changes)
			for _, c := range changes {
				fmt.Println("  " + c.String())
			}
		}
		all = append(all, changes...)
	}

	fmt.Println()
	switch {
	case len(all) == 0 && *confirm:
		fmt.Println("nothing to import: every value already matches the manifest")
	case len(all) == 0:
		fmt.Printf("nothing to import: every value matches either the manifest or the\n" +
			"tool's own guess. Use --confirm-guesses to accept the guesses as correct.\n")
	case *dryRun:
		fmt.Printf("%d change(s) — nothing written (--dry-run)\n", len(all))
	default:
		fmt.Printf("%d change(s) written to %s\n", len(all), target.Path())
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
