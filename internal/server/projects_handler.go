package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/cwbudde/circlefit/internal/app"
)

// createJobRequest is the wire shape of POST /api/v1/jobs. JobConfig is
// embedded so its fields stay promoted and the request format is byte-for-byte
// what it was before projects existed, while DisallowUnknownFields still
// rejects genuine typos. The project is a server-side placement concern, so it
// lives here rather than in app.JobConfig.
type createJobRequest struct {
	app.JobConfig

	Project string `json:"project,omitempty"`
	// Project stays a string here because this is the raw decoded request body.
	// resolveRequestedProject performs the one conversion to app.Project,
	// immediately before validation.
}

// projectResponse is the API projection. It deliberately exposes no filesystem
// path: the trusted-local boundary gains nothing from leaking the data root.
type projectResponse struct {
	Slug    app.Project `json:"slug"`
	Default bool        `json:"default,omitempty"`
	Jobs    int         `json:"jobCount"`
}

// resolveRequestedProject picks the slug from the body, falling back to the
// `?project=` query parameter and finally the default project.
func (s *Server) resolveRequestedProject(fromBody string, r *http.Request) (app.Project, error) {
	// Boundary: the query parameter and the decoded body field are untrusted
	// input. They become app.Project here, immediately before validation.
	fromQuery := app.Project(strings.TrimSpace(r.URL.Query().Get("project")))
	body := app.Project(strings.TrimSpace(fromBody))

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

// projectStoreError marks a server-side failure to create or open a project's
// store. It exists so callers can tell a rejected slug (the client's fault, and
// safe to echo) from a filesystem fault, whose wrapped os error carries the
// absolute data root and must never reach a client.
type projectStoreError struct {
	slug app.Project
	err  error
}

func (e *projectStoreError) Error() string {
	return fmt.Sprintf("prepare project %q: %v", e.slug, e.err)
}

func (e *projectStoreError) Unwrap() error { return e.err }

// ensureProject creates the project's store on first use. The default project
// is never created here: it is the store the server was built with. A slug that
// fails validation is returned as-is; every other failure is wrapped in a
// *projectStoreError so the caller can answer 500 instead of 400.
func (s *Server) ensureProject(slug app.Project) (app.Project, error) {
	if slug == "" || slug == app.DefaultProject {
		return app.DefaultProject, nil
	}

	err := app.ValidateProjectSlug(slug)
	if err != nil {
		return "", err
	}

	_, err = s.projects.GetOrCreate(slug)
	if err != nil {
		return "", &projectStoreError{slug: slug, err: err}
	}

	return slug, nil
}

// writeProjectError answers an ensureProject failure. Validation messages are
// charset-constrained and safe to echo; store faults are logged server-side and
// answered generically so no filesystem path is disclosed.
func (s *Server) writeProjectError(w http.ResponseWriter, slug app.Project, err error) {
	var storeErr *projectStoreError
	if errors.As(err, &storeErr) {
		slog.Error("Unable to prepare project store", "project", string(slug), "error", storeErr.err)
		writeAPIError(w, http.StatusInternalServerError, "project_unavailable", "the project store is unavailable")

		return
	}

	writeAPIError(w, http.StatusBadRequest, "invalid_project", err.Error())
}

// projectErrorMessage is the UI counterpart of writeProjectError: it logs a
// store fault and returns the text the create page should display.
func projectErrorMessage(slug app.Project, err error) string {
	var storeErr *projectStoreError
	if errors.As(err, &storeErr) {
		slog.Error("Unable to prepare project store", "project", string(slug), "error", storeErr.err)
		return "The project store is unavailable"
	}

	return err.Error()
}

// handleProjects serves GET /api/v1/projects.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	counts := make(map[app.Project]int)
	for _, job := range s.jobManager.ListJobSummaries() {
		counts[app.NormalizeProject(job.Project)]++
	}

	slugs := s.projects.Slugs()
	// A project may exist only in memory (jobs created before its directory was
	// written), so union the two sources rather than trusting either alone.
	seen := make(map[app.Project]bool, len(slugs))
	for _, slug := range slugs {
		seen[slug] = true
	}

	for slug := range counts {
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	// Slugs() is sorted, but the union above appends in Go map order, so the
	// response is only stable once everything is sorted together.
	slices.Sort(slugs)

	response := make([]projectResponse, 0, len(slugs))
	for _, slug := range slugs {
		response = append(response, projectResponse{
			Slug:    slug,
			Default: slug == app.DefaultProject,
			Jobs:    counts[slug],
		})
	}

	w.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		slog.Error("Failed to encode projects response", "error", err)
	}
}
