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

	// Get all jobs from job manager
	jobs := s.jobManager.ListJobs()

	// Convert to UI job list items
	jobItems := make([]ui.JobListItem, len(jobs))
	for i, job := range jobs {
		jobItems[i] = ui.JobListItem{
			ID:          job.ID,
			State:       string(job.State),
			RefPath:     job.Config.RefPath,
			Mode:        job.Config.Mode,
			Circles:     job.Config.Circles,
			Iterations:  job.Iterations,
			BestCost:    job.BestCost,
			InitialCost: job.InitialCost,
			StartTime:   job.StartTime,
			EndTime:     job.EndTime,
			Error:       job.Error,
		}
	}

	// Render the job list page using templ
	if err := ui.JobList(jobItems).Render(r.Context(), w); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}
