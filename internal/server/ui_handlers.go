package server

import (
	"net/http"

	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

// handleIndex handles GET /
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Only handle exact root path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Render the index page using templ
	if err := ui.Index().Render(r.Context(), w); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}
