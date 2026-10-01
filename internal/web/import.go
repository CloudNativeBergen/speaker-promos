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
	src, ok := s.talkParam(w, r, r.PathValue("id"))
	if !ok {
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
		results, err := manifest.ImportFiles(s.resolver.Set, paths, opts, j.progress)

		applied, conflicts := manifest.Tally(results)
		if applied > 0 {
			// Cards are addressed by revision, so bumping it is what makes the
			// browser refetch the ones an import changed.
			s.mu.Lock()
			s.rev++
			s.mu.Unlock()
		}
		report := statusReport{
			Summary: manifest.Summary(results, "tick “force”"),
			Muted:   applied == 0 && conflicts == 0,
		}
		for _, res := range results {
			for _, c := range res.Changes {
				report.Changes = append(report.Changes, c.String())
			}
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
		buf.WriteString(errorHTML(err))
	}
}

// writeRow renders one talk's row, swapped out-of-band over the old one.
func (s *Server) writeRow(buf *strings.Builder, src cnd.Talk, size string) {
	view := s.view(src, size)
	view.OOB = true
	if err := s.tmpl.ExecuteTemplate(buf, "talk.html", view); err != nil {
		buf.WriteString(errorHTML(err))
	}
}
