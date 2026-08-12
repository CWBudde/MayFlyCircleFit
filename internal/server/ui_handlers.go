package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
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
			Mode:        string(job.Config.Mode),
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
		totalCircles := job.Evaluations * max(1, len(job.BestParams)/7)
		cps = float64(totalCircles) / elapsed
	}

	// Convert to UI job detail
	jobDetail := ui.JobDetail{
		ID:          job.ID,
		State:       string(job.State),
		RefPath:     job.Config.RefPath,
		Mode:        string(job.Config.Mode),
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
		Termination: job.Termination,
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
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)
	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Failed to parse form data").Render(r.Context(), w)
		return
	}

	// Extract and validate form fields
	refPath := r.FormValue("refPath")
	canvasPath := r.FormValue("canvasPath")
	mode := r.FormValue("mode")
	circlesStr := r.FormValue("circles")
	itersStr := r.FormValue("iters")
	popSizeStr := r.FormValue("popSize")
	seedStr := r.FormValue("seed")
	convergenceEnabledStr := r.FormValue("convergenceEnabled")
	convergencePatienceStr := r.FormValue("convergencePatience")
	convergenceThresholdStr := r.FormValue("convergenceThreshold")

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
	// Parse convergence fields (with defaults)
	convergenceEnabled := convergenceEnabledStr == "on" // checkbox is "on" when checked, empty otherwise
	convergencePatience := 3                            // default
	if convergencePatienceStr != "" {
		convergencePatience, err = strconv.Atoi(convergencePatienceStr)
		if err != nil || convergencePatience < 1 || convergencePatience > 100 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage("Convergence patience must be between 1 and 100").Render(r.Context(), w)
			return
		}
	}

	convergenceThreshold := 0.001 // default
	if convergenceThresholdStr != "" {
		convergenceThreshold, err = strconv.ParseFloat(convergenceThresholdStr, 64)
		if err != nil || convergenceThreshold < 0.0001 || convergenceThreshold > 0.1 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage("Convergence threshold must be between 0.0001 and 0.1").Render(r.Context(), w)
			return
		}
	}

	// Optimizer-level early stopping. These fields only need to parse here;
	// their bounds belong to app.Normalize, so they are not duplicated.
	stopTargetCost, err := optionalFormFloat(r, "stopTargetCost")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error()).Render(r.Context(), w)
		return
	}
	stopMinImprovement, err := optionalFormFloat(r, "stopMinImprovement")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error()).Render(r.Context(), w)
		return
	}
	stopStagnationIters, err := optionalFormInt(r, "stopStagnationIters")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error()).Render(r.Context(), w)
		return
	}
	stopMinIters, err := optionalFormInt(r, "stopMinIters")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error()).Render(r.Context(), w)
		return
	}

	// Create job configuration through the same normalization path as the API.
	config, err := app.Normalize(JobConfig{
		RefPath:              refPath,
		CanvasPath:           canvasPath,
		Mode:                 app.Mode(mode),
		Circles:              circles,
		Iters:                iters,
		PopSize:              popSize,
		Seed:                 seed,
		ConvergenceEnabled:   convergenceEnabled,
		DisableConvergence:   !convergenceEnabled,
		ConvergencePatience:  convergencePatience,
		ConvergenceThreshold: convergenceThreshold,
		StopTargetCost:       stopTargetCost,
		StopMinImprovement:   stopMinImprovement,
		StopStagnationIters:  stopStagnationIters,
		StopMinIters:         stopMinIters,
	})
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error()).Render(r.Context(), w)
		return
	}
	if s.inputErr != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Server input roots are unavailable").Render(r.Context(), w)
		return
	}
	config.RefPath, err = s.input.resolveImage(config.RefPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error()).Render(r.Context(), w)
		return
	}
	if config.CanvasPath != "" {
		config.CanvasPath, err = s.input.resolveImage(config.CanvasPath)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage(err.Error()).Render(r.Context(), w)
			return
		}
	}

	// Create the job
	job := s.jobManager.CreateJob(config)

	// The server owns every job context, including jobs created through the UI.
	if err := s.enqueueJob(job.ID); err != nil {
		_ = s.jobManager.FailJob(job.ID, "server job queue is full")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Server job queue is full").Render(r.Context(), w)
		return
	}

	// Redirect to job detail page
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// optionalFormFloat parses an optional numeric form field. An absent or empty
// value yields zero. Range checks deliberately live in app.Normalize so the
// form and the JSON API cannot drift apart.
func optionalFormFloat(r *http.Request, field string) (float64, error) {
	raw := r.FormValue(field)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", field)
	}
	return value, nil
}

// optionalFormInt parses an optional integer form field. An absent or empty
// value yields zero.
func optionalFormInt(r *http.Request, field string) (int, error) {
	raw := r.FormValue(field)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", field)
	}
	return value, nil
}
