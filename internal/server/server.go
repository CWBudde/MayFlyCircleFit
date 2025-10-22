package server

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// Server represents the HTTP server
type Server struct {
	jobManager *JobManager
	addr       string
	server     *http.Server
}

// NewServer creates a new HTTP server
func NewServer(addr string) *Server {
	return &Server{
		jobManager: NewJobManager(),
		addr:       addr,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Register UI routes
	mux.HandleFunc("/", s.handleIndex)

	// Register API routes
	mux.HandleFunc("/api/v1/jobs", s.handleJobs)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobsWithID)

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
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
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

	// Start worker in background
	go runJob(context.Background(), s.jobManager, job.ID)

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
	renderer := fit.NewCPURenderer(ref, job.Config.Circles)
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
	renderer := fit.NewCPURenderer(ref, job.Config.Circles)
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

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("HTTP request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}
