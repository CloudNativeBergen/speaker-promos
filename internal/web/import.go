package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/promo"
)

// handleImport merges the edited promo.yaml files under the output directory
// back into the project manifest.
//
// It is the mirror of the Export button and goes through the same
// manifest.ImportFrom as `promo import`, including the baseline that makes an
// import take the EDITS rather than the file — an exported bundle pre-fills the
// guessed employer, and importing that verbatim would silence the warnings that
// exist to be read.
//
// The response swaps the whole row list, because a merge can change any number
// of talks and there is no way to know which rows moved. The summary comes back
// out-of-band so it survives that swap.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	size := s.sizeParam(r)
	confirm := r.FormValue("confirm-guesses") != ""

	paths, err := manifest.FindFiles([]string{s.opts.OutDir})
	if err != nil {
		// Having nothing to import is a normal state — you have not exported
		// yet — so it is reported in the status area rather than as a failure,
		// and says what to do about it instead of surfacing a stat error.
		msg := fmt.Sprintf("nothing to import: no promo.yaml under %s/ yet — use "+
			"“Export all” first, then edit the promo.yaml in a talk's folder", s.opts.OutDir)
		if !errors.Is(err, os.ErrNotExist) {
			msg = fmt.Sprintf("nothing to import from %s/: %v", s.opts.OutDir, err)
		}
		s.renderRows(w, size, statusLine(msg, nil, true))
		return
	}

	opts := promo.ImportBaseline(s.opts.Program)
	opts.ConfirmGuesses = confirm

	s.mu.Lock()
	var changes []manifest.Change
	for _, path := range paths {
		src, loadErr := manifest.Load(path)
		if loadErr != nil {
			s.mu.Unlock()
			s.fail(w, fmt.Errorf("reading %s: %w", path, loadErr))
			return
		}
		applied, importErr := s.opts.Set.ImportFrom(src, opts)
		if importErr != nil {
			s.mu.Unlock()
			s.fail(w, importErr)
			return
		}
		changes = append(changes, applied...)
	}
	if len(changes) > 0 {
		// Cards are addressed by revision, so bumping it is what makes the
		// browser refetch the ones an import changed.
		s.rev++
	}
	s.mu.Unlock()

	manifest.SortChanges(changes)
	summary := fmt.Sprintf("imported %d change(s) from %d file(s) under %s/",
		len(changes), len(paths), s.opts.OutDir)
	if len(changes) == 0 {
		summary = fmt.Sprintf("nothing to import from %d file(s): every value matches either the "+
			"manifest or the tool's own guess", len(paths))
		if !confirm {
			summary += ` — tick "guesses" to accept the guesses as correct`
		}
	}
	s.renderRows(w, size, statusLine(summary, changes, len(changes) == 0))
}

// statusReport is what the status area shows after an import.
type statusReport struct {
	Summary string
	Changes []string
	Muted   bool
}

func statusLine(summary string, changes []manifest.Change, muted bool) statusReport {
	out := statusReport{Summary: summary, Muted: muted}
	for _, c := range changes {
		out.Changes = append(out.Changes, c.String())
	}
	return out
}

// renderRows re-renders every talk row, with the status report swapped in
// out-of-band so it survives replacing the rows.
func (s *Server) renderRows(w http.ResponseWriter, size string, status statusReport) {
	views, _ := s.views(size)

	var buf strings.Builder
	rows := struct {
		Talks []talkView
		Size  string
	}{views, size}
	// The status element is addressed by id, so it updates even though the swap
	// itself replaced the rows. Both fragments go through the templates, which
	// is what escapes an imported value on its way onto the page.
	for _, part := range []struct {
		name string
		data any
	}{{"rows.html", rows}, {"status.html", status}} {
		if err := s.tmpl.ExecuteTemplate(&buf, part.name, part.data); err != nil {
			s.fail(w, fmt.Errorf("rendering %s: %w", part.name, err))
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(buf.String()))
}
