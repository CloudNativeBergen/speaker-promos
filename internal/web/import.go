package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/manifest"
)

// handleImport merges the edited promo.yaml files under the output directory
// back into the project manifest, as a background job.
//
// It is the mirror of the Export button and goes through the same
// manifest.ImportFiles as `promo import`, so it merges the same way: what you
// edited in a bundle is taken, what changed in the project since the export is
// kept, and a field edited in both is reported as a conflict.
//
// The final response swaps the whole row list, because a merge can change any
// number of talks and there is no way to know which rows moved.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	size := s.sizeParam(r)
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
		s.renderStatus(w, statusReport{Summary: msg, Muted: true})
		return
	}
	s.importJob(w, r, fmt.Sprintf("Importing %d bundles", len(paths)), paths, nil, size)
}

// handleTalkImport merges one talk's bundle, found by the talk id it records
// rather than by folder name, and re-renders just that row.
func (s *Server) handleTalkImport(w http.ResponseWriter, r *http.Request) {
	src, ok := s.opts.Program.Talk(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	path, err := manifest.FindBundle(s.opts.OutDir, src.ID)
	if err != nil {
		s.renderStatus(w, statusReport{Summary: "nothing to import: " + err.Error(), Muted: true})
		return
	}
	s.importJob(w, r, "Importing “"+src.Title+"”", []string{path}, &src, s.sizeParam(r))
}

// importJob runs an import. With one talk given, the final response re-renders
// that row; otherwise every row.
func (s *Server) importJob(w http.ResponseWriter, r *http.Request, label string,
	paths []string, one *cnd.Talk, size string) {
	opts := manifest.ImportOptions{Force: r.FormValue("force") != ""}
	s.startJob(w, r, label, func(j *job) func(http.ResponseWriter, *http.Request) {
		j.progress(0, len(paths), "")
		results, err := manifest.ImportFiles(s.opts.Set, paths, opts, j.progress)

		var applied, conflicts []manifest.Change
		for _, res := range results {
			for _, c := range res.Changes {
				if c.Conflict {
					conflicts = append(conflicts, c)
				} else {
					applied = append(applied, c)
				}
			}
		}
		if len(applied) > 0 {
			// Cards are addressed by revision, so bumping it is what makes the
			// browser refetch the ones an import changed.
			s.mu.Lock()
			s.rev++
			s.mu.Unlock()
		}

		report := statusReport{Summary: fmt.Sprintf("imported %d change(s) from %d file(s)",
			len(applied), len(results))}
		if len(applied) == 0 {
			report.Summary = fmt.Sprintf("nothing to import from %d file(s): no bundle was edited "+
				"since it was exported", len(results))
			report.Muted = true
		}
		if n := len(conflicts); n > 0 {
			report.Summary += fmt.Sprintf(" — %d conflict(s) kept as they are here, since they "+
				"changed in both places; tick “force” to take the bundles' version", n)
			report.Muted = false
		}
		for _, c := range append(applied, conflicts...) {
			report.Changes = append(report.Changes, c.String())
		}
		if err != nil {
			report = statusReport{Summary: err.Error(), Changes: report.Changes, Error: true}
		}

		return func(w http.ResponseWriter, r *http.Request) {
			var buf strings.Builder
			if one != nil {
				s.writeRow(&buf, *one, size)
			} else {
				s.writeRows(&buf, size)
			}
			if err := s.tmpl.ExecuteTemplate(&buf, "status.html", report); err != nil {
				s.fail(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(buf.String()))
		}
	})
}

// statusReport is what the status area shows after a job.
type statusReport struct {
	Summary string
	Changes []string
	Muted   bool
	Error   bool
}

// renderStatus answers with the status area alone, swapped out-of-band.
func (s *Server) renderStatus(w http.ResponseWriter, report statusReport) {
	s.renderTemplate(w, "status.html", report)
}

// writeRows renders every talk row, swapped out-of-band over the old list.
func (s *Server) writeRows(buf *strings.Builder, size string) {
	views, _ := s.views(size)
	rows := struct {
		Talks []talkView
		Size  string
		OOB   bool
	}{views, size, true}
	if err := s.tmpl.ExecuteTemplate(buf, "rows.html", rows); err != nil {
		fmt.Fprintf(buf, `<p class="error">%s</p>`, err)
	}
}

// writeRow renders one talk's row, swapped out-of-band over the old one.
func (s *Server) writeRow(buf *strings.Builder, src cnd.Talk, size string) {
	view := s.view(src, size)
	view.OOB = true
	if err := s.tmpl.ExecuteTemplate(buf, "talk.html", view); err != nil {
		fmt.Fprintf(buf, `<p class="error">%s</p>`, err)
	}
}
