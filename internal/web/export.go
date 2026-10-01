package web

import (
	"fmt"
	"net/http"

	"github.com/vehagn/speaker-promos/internal/promo"
)

// handleExport writes a bundle per visible talk into the output directory, as
// a background job.
//
// It goes through the same export.Exporter.WriteAll as `promo export --all`,
// so a bundle produced from the browser and one produced from the terminal
// are identical. Hidden talks are skipped, matching the CLI.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	size := s.sizeParam(r)
	talks, skipped, err := s.resolver.Select(true, nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.exportJob(w, r, fmt.Sprintf("Exporting %d talks", len(talks)), size, talks, skipped, false)
}

// handleTalkExport writes one talk's bundle, hidden or not: asking for a talk
// by name is not a bulk operation.
func (s *Server) handleTalkExport(w http.ResponseWriter, r *http.Request) {
	t, ok := s.resolver.ByID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.exportJob(w, r, "Exporting “"+t.Title+"”", s.sizeParam(r), []promo.Talk{t}, 0, true)
}

func (s *Server) exportJob(w http.ResponseWriter, r *http.Request, label, size string,
	talks []promo.Talk, skipped int, single bool) {
	exporter := s.exporter([]string{size})
	s.startJob(w, r, label, func(j *job) func(http.ResponseWriter, *http.Request) {
		j.progress(0, len(talks), "")
		results, err := exporter.WriteAll(s.opts.OutDir, talks, j.progress)

		files, warnings := 0, 0
		for _, res := range results {
			files += len(res.Files)
			warnings += len(res.Warnings)
		}
		msg := fmt.Sprintf("wrote %d talks, %d files to %s/", len(results), files, s.opts.OutDir)
		if single && len(results) == 1 {
			msg = fmt.Sprintf("wrote %s/%s/ — %d files", s.opts.OutDir, results[0].Dir, files)
		}
		if skipped > 0 {
			msg += fmt.Sprintf(" (%d hidden)", skipped)
		}
		if !s.hasConverter {
			msg += " — SVG only, no rasteriser on PATH"
		}
		if warnings > 0 {
			msg += fmt.Sprintf(", %d warning(s)", warnings)
		}
		return func(w http.ResponseWriter, r *http.Request) {
			if err != nil {
				s.renderStatus(w, statusReport{Summary: msg + " — then failed: " + err.Error(), Error: true})
				return
			}
			s.renderStatus(w, statusReport{Summary: msg})
		}
	})
}
