package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vehagn/speaker-promos/internal/progress"
)

// FindFiles expands paths into the manifest files they contain.
//
// A directory is searched rather than rejected, because the thing you have
// after an export is `out/` — one folder per talk — and naming every promo.yaml
// inside it would be absurd. Shared by the CLI and the preview server so the
// two cannot disagree about what an import covers.
func FindFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("give a promo.yaml, a bundle folder, or an export directory")
	}

	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, p)
		}
	}

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", arg, err)
		}
		if !info.IsDir() {
			add(arg)
			continue
		}
		var found int
		err = filepath.WalkDir(arg, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == "promo.yaml" {
				add(path)
				found++
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("searching %s: %w", arg, err)
		}
		if found == 0 {
			return nil, fmt.Errorf("no promo.yaml found under %s", arg)
		}
	}
	sort.Strings(out)
	return out, nil
}

// FindBundle finds the bundle exported for one talk under dir.
//
// It goes by the talk id the bundle records rather than by folder name, since
// the folder is named after the display title and correcting that title
// renames the next export's folder. When several bundles name the talk — an
// old folder left beside a renamed one — the most recently modified wins.
func FindBundle(dir, talkID string) (string, error) {
	paths, err := FindFiles([]string{dir})
	if err != nil {
		return "", err
	}
	var best string
	var bestTime int64
	for _, p := range paths {
		set, err := Load(p)
		if err != nil || set.BundleTalk() != talkID {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if t := fi.ModTime().UnixNano(); best == "" || t > bestTime {
			best, bestTime = p, t
		}
	}
	if best == "" {
		return "", fmt.Errorf("no bundle for this talk under %s — export it first", dir)
	}
	return best, nil
}

// FileChanges is what importing one file did.
type FileChanges struct {
	Path    string
	Changes []Change
}

// ImportFiles merges each file into target in turn, reporting progress after
// each. It is the whole of `promo import` and of the preview server's Import
// buttons, so the two cannot disagree about what an import covers.
//
// It stops at the first file that cannot be read or merged; the files before
// it have been imported, since each merge lands on its own.
func ImportFiles(target *Set, paths []string, opts ImportOptions, report progress.Func) ([]FileChanges, error) {
	var out []FileChanges
	for i, path := range paths {
		src, err := Load(path)
		if err != nil {
			return out, fmt.Errorf("reading %s: %w", path, err)
		}
		changes, err := target.ImportFrom(src, opts)
		if err != nil {
			return out, fmt.Errorf("%s: %w", path, err)
		}
		SortChanges(changes)
		out = append(out, FileChanges{Path: path, Changes: changes})
		report.Report(i+1, len(paths), filepath.Base(filepath.Dir(path)))
	}
	return out, nil
}

// Tally counts the changes an import applied, and the conflicts it left.
func Tally(results []FileChanges) (applied, conflicts int) {
	for _, res := range results {
		for _, c := range res.Changes {
			if c.Conflict {
				conflicts++
			} else {
				applied++
			}
		}
	}
	return applied, conflicts
}

// Summary says what an import did in a sentence, for `promo import` and the
// Import buttons alike. force names how to take the bundles' side of a
// conflict in the caller's interface.
func Summary(results []FileChanges, force string) string {
	applied, conflicts := Tally(results)
	msg := fmt.Sprintf("imported %d change(s) from %d file(s)", applied, len(results))
	if applied == 0 && conflicts == 0 {
		msg = fmt.Sprintf("nothing to import from %d file(s): no bundle was edited since it was exported", len(results))
	}
	if conflicts > 0 {
		msg += fmt.Sprintf(" — %d conflict(s) kept as the manifest has them, since they changed in both "+
			"places; %s to take the bundles' version", conflicts, force)
	}
	return msg
}
