package web

import (
	"net/http"
)

func (s *Server) handleSaveNotes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	req, err := decodeBody[struct {
		Text *string `json:"text"`
	}](r)
	if err != nil || req.Text == nil {
		http.Error(w, "expected notes text", http.StatusBadRequest)
		return
	}
	if err := s.progress.Scope().SaveDocument(p.Slug, "notes", *req.Text); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Text string `json:"text"`
	}{*req.Text})
}
