package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

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

// handleJobDetail handles GET /jobs/:id
func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request) {
	// Extract job ID from path
	jobID := r.URL.Path[len("/jobs/"):]

	// Get job from manager
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ui.JobNotFound(jobID).Render(r.Context(), w); err != nil {
			http.Error(w, "Failed to render page", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Compute elapsed time and CPS
	var elapsed float64
	if job.EndTime != nil {
		elapsed = job.EndTime.Sub(job.StartTime).Seconds()
	} else {
		elapsed = time.Since(job.StartTime).Seconds()
	}

	cps := float64(0)
	if elapsed > 0 {
		totalEvals := job.Config.Iters * job.Config.PopSize
		totalCircles := totalEvals * job.Config.Circles
		cps = float64(totalCircles) / elapsed
	}

	// Convert to UI job detail
	jobDetail := ui.JobDetail{
		ID:          job.ID,
		State:       string(job.State),
		RefPath:     job.Config.RefPath,
		Mode:        job.Config.Mode,
		Circles:     job.Config.Circles,
		Iterations:  job.Iterations,
		MaxIters:    job.Config.Iters,
		PopSize:     job.Config.PopSize,
		BestCost:    job.BestCost,
		InitialCost: job.InitialCost,
		StartTime:   job.StartTime,
		EndTime:     job.EndTime,
		ElapsedSec:  elapsed,
		CPS:         cps,
		Error:       job.Error,
	}

	// Render the job detail page using templ
	if err := ui.JobDetailPage(jobDetail).Render(r.Context(), w); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// handleCreatePage handles GET /create and POST /create
func (s *Server) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleCreatePageGet(w, r)
	} else if r.Method == http.MethodPost {
		s.handleCreatePagePost(w, r)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleCreatePageGet renders the job creation form
func (s *Server) handleCreatePageGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Render the create job page with no error message
	if err := ui.CreateJobPage("").Render(r.Context(), w); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// handleCreatePagePost processes the job creation form submission
func (s *Server) handleCreatePagePost(w http.ResponseWriter, r *http.Request) {
	// Parse form data
	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Failed to parse form data").Render(r.Context(), w)
		return
	}

	// Extract and validate form fields
	refPath := r.FormValue("refPath")
	mode := r.FormValue("mode")
	circlesStr := r.FormValue("circles")
	itersStr := r.FormValue("iters")
	popSizeStr := r.FormValue("popSize")
	seedStr := r.FormValue("seed")

	// Validate required fields
	if refPath == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Reference image path is required").Render(r.Context(), w)
		return
	}

	if mode == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Mode is required").Render(r.Context(), w)
		return
	}

	// Parse integer fields
	circles, err := strconv.Atoi(circlesStr)
	if err != nil || circles < 1 || circles > 1000 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Circles must be between 1 and 1000").Render(r.Context(), w)
		return
	}

	iters, err := strconv.Atoi(itersStr)
	if err != nil || iters < 1 || iters > 10000 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Iterations must be between 1 and 10000").Render(r.Context(), w)
		return
	}

	popSize, err := strconv.Atoi(popSizeStr)
	if err != nil || popSize < 2 || popSize > 200 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Population size must be between 2 and 200").Render(r.Context(), w)
		return
	}

	seed, err := strconv.ParseInt(seedStr, 10, 64)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Invalid seed value").Render(r.Context(), w)
		return
	}

	// Create job configuration
	config := JobConfig{
		RefPath: refPath,
		Mode:    mode,
		Circles: circles,
		Iters:   iters,
		PopSize: popSize,
		Seed:    seed,
	}

	// Create the job
	job := s.jobManager.CreateJob(config)

	// Start the job in background with checkpoint store and context.Background() to avoid cancellation
	go runJob(context.Background(), s.jobManager, s.store, job.ID)

	// Redirect to job detail page
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}
