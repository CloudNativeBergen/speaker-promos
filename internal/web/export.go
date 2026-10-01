package web

import (
	"fmt"
	"html/template"
	"net/http"
)

// handleExport writes a bundle per visible talk into the output directory.
//
// It goes through the same internal/export code as `promo export`, so a bundle
// produced from the browser and one produced from the terminal are identical.
// Hidden talks are skipped, matching the CLI.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	size := s.sizeParam(r)

	// The same selection as `promo export --all`, hidden talks skipped.
	selected, skipped, err := s.resolver.Select(true, nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	exporter := s.exporter([]string{size})

	talks, files := 0, 0
	var warnings []string
	for _, t := range selected {
		res, err := exporter.Write(s.opts.OutDir, t)
		if err != nil {
			s.fail(w, err)
			return
		}
		talks++
		files += len(res.Files)
		warnings = append(warnings, res.Warnings...)
	}

	msg := fmt.Sprintf("wrote %d talks, %d files", talks, files)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d hidden)", skipped)
	}
	if !s.hasConverter {
		msg += " — SVG only, no rasteriser on PATH"
	}
	if n := len(warnings); n > 0 {
		msg += fmt.Sprintf(", %d warning(s)", n)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<span>%s</span>", template.HTMLEscapeString(msg))
}
