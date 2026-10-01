package web

import (
	"net/http"

	"github.com/vehagn/speaker-promos/internal/textcase"
)

// handleTalkTitleCase fixes the capitalisation of a talk's display title.
//
// It rewrites the submitted form value and then hands over to the ordinary
// update handler, so the change goes through exactly the same diffing, saving
// and re-rendering as typing it by hand would. The alternative — doing it in
// the browser — would have put the rule in two places and in a language where
// it could not be tested.
//
// Language comes from the talk, because the two conventions differ: English
// gets title case, Norwegian sentence case.
func (s *Server) handleTalkTitleCase(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	// Resolved against the talk's abstract, not just the title being fixed: a
	// title is a few words and detection needs prose. "Praktisk AI-drevet
	// Kubernetes-drift" carries no Norwegian function words at all and reads as
	// English on its own.
	language := s.resolver.Language
	if t, ok := s.resolver.ByID(r.PathValue("id")); ok {
		language = t.Language
	}
	r.Form.Set("displayTitle", textcase.Title(r.FormValue("displayTitle"), language))
	s.handleTalkUpdate(w, r)
}

// handleSpeakerNameCase capitalises a speaker's name, for the ones who typed it
// in lower case.
func (s *Server) handleSpeakerNameCase(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, err)
		return
	}
	r.Form.Set("name", textcase.Name(r.FormValue("name")))
	s.handleSpeakerUpdate(w, r)
}
