package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// createJobRequest is the wire shape of POST /api/v1/jobs. JobConfig is
// embedded so its fields stay promoted and the request format is byte-for-byte
// what it was before projects existed, while DisallowUnknownFields still
// rejects genuine typos. The project is a server-side placement concern, so it
// lives here rather than in app.JobConfig.
type createJobRequest struct {
	app.JobConfig
	Project string `json:"project,omitempty"`
}

// projectResponse is the API projection. It deliberately exposes no filesystem
// path: the trusted-local boundary gains nothing from leaking the data root.
type projectResponse struct {
	Slug    string `json:"slug"`
	Default bool   `json:"default,omitempty"`
	Jobs    int    `json:"jobCount"`
}

// resolveRequestedProject picks the slug from the body, falling back to the
// `?project=` query parameter and finally the default project.
func (s *Server) resolveRequestedProject(fromBody string, r *http.Request) (string, error) {
	fromQuery := strings.TrimSpace(r.URL.Query().Get("project"))
	body := strings.TrimSpace(fromBody)

	switch {
	case body != "" && fromQuery != "" && body != fromQuery:
		return "", errConflictingProject
	case body != "":
		return body, app.ValidateProjectSlug(body)
	case fromQuery != "":
		return fromQuery, app.ValidateProjectSlug(fromQuery)
	default:
		return app.DefaultProject, nil
	}
}

// ensureProject creates the project's store on first use. The default project
// is never created here: it is the store the server was built with.
func (s *Server) ensureProject(slug string) (string, error) {
	if slug == "" || slug == app.DefaultProject {
		return app.DefaultProject, nil
	}
	if s.projects == nil {
		return "", errUnknownProject
	}
	if _, err := s.projects.GetOrCreate(slug); err != nil {
		return "", err
	}
	return slug, nil
}

// handleProjects serves GET /api/v1/projects.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	counts := make(map[string]int)
	for _, job := range s.jobManager.ListJobs() {
		slug := job.Project
		if slug == "" {
			slug = app.DefaultProject
		}
		counts[slug]++
	}

	slugs := []string{app.DefaultProject}
	if s.projects != nil {
		slugs = s.projects.Slugs()
	}
	// A project may exist only in memory (jobs created before its directory was
	// written), so union the two sources rather than trusting either alone.
	seen := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		seen[slug] = true
	}
	for slug := range counts {
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}

	response := make([]projectResponse, 0, len(slugs))
	for _, slug := range slugs {
		response = append(response, projectResponse{
			Slug:    slug,
			Default: slug == app.DefaultProject,
			Jobs:    counts[slug],
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode projects response", "error", err)
	}
}
