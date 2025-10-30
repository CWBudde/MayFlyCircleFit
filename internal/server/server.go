package server

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// Server represents the HTTP server
type Server struct {
	jobManager *JobManager
	store      store.Store
	addr       string
	server     *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewServer creates a new HTTP server with optional checkpoint store.
// If store is nil, checkpointing is disabled.
func NewServer(addr string, checkpointStore store.Store) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		jobManager: NewJobManager(),
		store:      checkpointStore,
		addr:       addr,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Register UI routes
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/jobs/", s.handleJobDetail)
	mux.HandleFunc("/create", s.handleCreatePage)

	// Register API routes
	mux.HandleFunc("/api/v1/jobs", s.handleJobs)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobsWithID)

	// Register pprof routes for profiling
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// Wrap with middleware
	handler := s.loggingMiddleware(s.corsMiddleware(mux))

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: handler,
	}

	slog.Info("Starting HTTP server", "addr", s.addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("Shutting down HTTP server")

	// Cancel server context to signal workers to stop
	s.cancel()

	// Checkpoint all running jobs before shutdown
	if s.store != nil {
		s.checkpointRunningJobs(ctx)
	}

	// Shutdown HTTP server
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// checkpointRunningJobs saves checkpoints for all running jobs
func (s *Server) checkpointRunningJobs(ctx context.Context) {
	runningJobs := s.jobManager.GetRunningJobs()

	if len(runningJobs) == 0 {
		slog.Info("No running jobs to checkpoint")
		return
	}

	slog.Info("Checkpointing running jobs", "count", len(runningJobs))

	// Use a wait group to checkpoint jobs concurrently
	type checkpointResult struct {
		jobID string
		err   error
	}

	results := make(chan checkpointResult, len(runningJobs))

	for _, job := range runningJobs {
		go func(j *Job) {
			// Load reference image to create renderer
			ref, err := loadReferenceImage(j.Config.RefPath)
			if err != nil {
				slog.Error("Failed to load reference for checkpoint",
					"job_id", j.ID,
					"error", err,
				)
				results <- checkpointResult{jobID: j.ID, err: err}
				return
			}

			// Create renderer
			renderer := renderer.NewCPURenderer(ref, j.Config.Circles)

			// Save checkpoint
			err = saveCheckpoint(s.jobManager, s.store, renderer, j.ID)

			// Re-fetch job to get updated values after potential checkpoint
			job, exists := s.jobManager.GetJob(j.ID)
			if !exists {
				results <- checkpointResult{jobID: j.ID, err: fmt.Errorf("job not found")}
				return
			}

			// Only log if checkpoint was actually saved (job has params)
			if err != nil {
				slog.Error("Failed to checkpoint job on shutdown",
					"job_id", j.ID,
					"error", err,
				)
			} else if len(job.BestParams) > 0 {
				slog.Info("Job checkpointed on shutdown",
					"job_id", j.ID,
					"iteration", job.Iterations,
					"best_cost", job.BestCost,
				)
			} else {
				slog.Debug("Skipped checkpoint for job with no progress",
					"job_id", j.ID,
				)
			}
			results <- checkpointResult{jobID: j.ID, err: err}
		}(job)
	}

	// Wait for all checkpoints to complete or timeout
	checkpointed := 0
	failed := 0

	for i := 0; i < len(runningJobs); i++ {
		select {
		case result := <-results:
			if result.err == nil {
				checkpointed++
			} else {
				failed++
			}
		case <-ctx.Done():
			slog.Warn("Checkpoint timeout during shutdown",
				"checkpointed", checkpointed,
				"failed", failed,
				"pending", len(runningJobs)-checkpointed-failed,
			)
			return
		}
	}

	slog.Info("Shutdown checkpoint complete",
		"checkpointed", checkpointed,
		"failed", failed,
	)
}

// handleJobs handles /api/v1/jobs
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateJob(w, r)
	case http.MethodGet:
		s.handleListJobs(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJobsWithID handles /api/v1/jobs/:id/*
func (s *Server) handleJobsWithID(w http.ResponseWriter, r *http.Request) {
	// Parse job ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	jobID := parts[0]

	// Route based on subpath
	if len(parts) == 1 || parts[1] == "status" {
		s.handleGetJobStatus(w, r, jobID)
	} else if parts[1] == "best.png" {
		s.handleGetBestImage(w, r, jobID)
	} else if parts[1] == "diff.png" {
		s.handleGetDiffImage(w, r, jobID)
	} else if parts[1] == "ref.png" {
		s.handleGetRefImage(w, r, jobID)
	} else if parts[1] == "stream" {
		s.handleJobStream(w, r, jobID)
	} else if parts[1] == "resume" {
		s.handleResumeJob(w, r, jobID)
	} else {
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

// handleCreateJob handles POST /api/v1/jobs
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var config JobConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Validate config
	if config.RefPath == "" {
		http.Error(w, "refPath is required", http.StatusBadRequest)
		return
	}
	if config.Circles <= 0 {
		config.Circles = 10
	}
	if config.Iters <= 0 {
		config.Iters = 100
	}
	if config.PopSize <= 0 {
		config.PopSize = 30
	}
	if config.Mode == "" {
		config.Mode = "joint"
	}

	// Create job
	job := s.jobManager.CreateJob(config)

	// Start worker in background with checkpoint store
	go runJob(s.ctx, s.jobManager, s.store, job.ID)

	// Return job
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(job)
}

// handleListJobs handles GET /api/v1/jobs
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := s.jobManager.ListJobs()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

// handleGetJobStatus handles GET /api/v1/jobs/:id/status
func (s *Server) handleGetJobStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Compute elapsed time and CPS
	var elapsed time.Duration
	if job.EndTime != nil {
		elapsed = job.EndTime.Sub(job.StartTime)
	} else {
		elapsed = time.Since(job.StartTime)
	}

	cps := float64(0)
	if elapsed.Seconds() > 0 {
		totalEvals := job.Config.Iters * job.Config.PopSize
		totalCircles := totalEvals * job.Config.Circles
		cps = float64(totalCircles) / elapsed.Seconds()
	}

	// Create response
	response := map[string]interface{}{
		"id":          job.ID,
		"state":       job.State,
		"config":      job.Config,
		"bestCost":    job.BestCost,
		"initialCost": job.InitialCost,
		"iterations":  job.Iterations,
		"elapsed":     elapsed.Seconds(),
		"cps":         cps,
		"startTime":   job.StartTime,
		"endTime":     job.EndTime,
		"error":       job.Error,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleGetBestImage handles GET /api/v1/jobs/:id/best.png
func (s *Server) handleGetBestImage(w http.ResponseWriter, r *http.Request, jobID string) {
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Check if job has results
	if len(job.BestParams) == 0 {
		http.Error(w, "No results yet", http.StatusNotFound)
		return
	}

	// Load reference image to get dimensions
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load reference: %v", err), http.StatusInternalServerError)
		return
	}

	// Render best image
	renderer := renderer.NewCPURenderer(ref, job.Config.Circles)
	img := renderer.Render(job.BestParams)

	// Set headers
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")

	// Encode and send
	if err := png.Encode(w, img); err != nil {
		slog.Error("Failed to encode PNG", "error", err)
	}
}

// handleGetDiffImage handles GET /api/v1/jobs/:id/diff.png
func (s *Server) handleGetDiffImage(w http.ResponseWriter, r *http.Request, jobID string) {
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Check if job has results
	if len(job.BestParams) == 0 {
		http.Error(w, "No results yet", http.StatusNotFound)
		return
	}

	// Load reference image
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load reference: %v", err), http.StatusInternalServerError)
		return
	}

	// Render best image
	renderer := renderer.NewCPURenderer(ref, job.Config.Circles)
	best := renderer.Render(job.BestParams)

	// Compute difference image (simple visualization for now)
	diff := computeDiffImage(ref, best)

	// Set headers
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")

	// Encode and send
	if err := png.Encode(w, diff); err != nil {
		slog.Error("Failed to encode PNG", "error", err)
	}
}

// corsMiddleware adds CORS headers
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// handleGetRefImage handles GET /api/v1/jobs/:id/ref.png
func (s *Server) handleGetRefImage(w http.ResponseWriter, r *http.Request, jobID string) {
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Load reference image
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load reference: %v", err), http.StatusInternalServerError)
		return
	}

	// Set headers
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	// Encode and send
	if err := png.Encode(w, ref); err != nil {
		slog.Error("Failed to encode PNG", "error", err)
	}
}

// handleResumeJob handles POST /api/v1/jobs/:id/resume
func (s *Server) handleResumeJob(w http.ResponseWriter, r *http.Request, jobID string) {
	// Only allow POST
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if checkpoint store is available
	if s.store == nil {
		http.Error(w, "Checkpoint feature not enabled", http.StatusServiceUnavailable)
		return
	}

	// Load checkpoint
	checkpoint, err := s.store.LoadCheckpoint(jobID)
	if err != nil {
		if _, ok := err.(*store.NotFoundError); ok {
			http.Error(w, fmt.Sprintf("Checkpoint not found for job %s", jobID), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to load checkpoint: %v", err), http.StatusInternalServerError)
		return
	}

	// Validate checkpoint
	if err := checkpoint.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("Invalid checkpoint: %v", err), http.StatusBadRequest)
		return
	}

	slog.Info("Resuming job from checkpoint",
		"job_id", jobID,
		"iteration", checkpoint.Iteration,
		"best_cost", checkpoint.BestCost,
	)

	// Create a new job with resumed state
	// We use the same configuration but mark it as a resumed job
	config := checkpoint.Config
	newJob := s.jobManager.CreateJob(config)

	// Initialize the new job with checkpoint data
	s.jobManager.UpdateJob(newJob.ID, func(j *Job) {
		j.BestParams = checkpoint.BestParams
		j.BestCost = checkpoint.BestCost
		j.InitialCost = checkpoint.InitialCost
		j.Iterations = checkpoint.Iteration
	})

	// Start worker in background with checkpoint store
	go runJob(s.ctx, s.jobManager, s.store, newJob.ID)

	// Return response
	response := map[string]interface{}{
		"jobId":         newJob.ID,
		"resumedFrom":   jobID,
		"state":         string(newJob.State),
		"previousCost":  checkpoint.BestCost,
		"previousIters": checkpoint.Iteration,
		"message":       "Job resumed successfully from checkpoint",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("HTTP request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}
