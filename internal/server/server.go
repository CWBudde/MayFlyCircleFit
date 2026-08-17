package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/google/uuid"
)

// Server represents the HTTP server
type Server struct {
	jobManager *JobManager
	store      store.Store
	projects   *projectRegistry
	addr       string
	server     *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
	options    ServerOptions
	queue      chan string
	workerOnce sync.Once
	workerWG   sync.WaitGroup
	cancelMu   sync.Mutex
	jobCancels map[string]context.CancelFunc
	input      *inputPolicy
	inputErr   error
	// schedulesMu guards scheduleDrivers, which holds one entry per schedule
	// with a live executor. It is the in-process guarantee that a schedule
	// never has two executors, and so never two jobs for one stage.
	schedulesMu     sync.Mutex
	scheduleDrivers map[string]struct{}
	scheduleWG      sync.WaitGroup
}

// ServerOptions configures the trusted-local HTTP boundary.
type ServerOptions struct {
	EnablePprof       bool
	MaxConcurrentJobs int
	QueueSize         int
	InputRoots        []string
	// DataRoot enables multi-project support. When empty the server holds a
	// single project backed by the store passed to NewServerWithOptions, which
	// is what keeps store-injecting callers and tests working unchanged.
	DataRoot string
}

var ErrJobQueueFull = errors.New("job queue is full")

// storeForSlug returns the store owning a project's artifacts. An empty slug
// and the default project resolve to the store the server was built with, so a
// server given an injected store behaves exactly as it did before projects
// existed. Any other slug must be in the registry: a project that failed to
// load has no store, and silently substituting another project's directory
// would write its checkpoints into, and read them from, the wrong place.
func (s *Server) storeForSlug(slug app.Project) (store.Store, error) {
	if slug == "" || slug == app.DefaultProject {
		return s.store, nil
	}
	projectStore, ok := s.projects.Get(slug)
	if !ok {
		return nil, fmt.Errorf("%w: %q", errUnknownProject, slug)
	}
	return projectStore, nil
}

// storeForJob resolves the store from the job's own project. It deliberately
// never searches other projects: a job ID that is unknown here must resolve to
// nothing rather than to another project's artifacts.
func (s *Server) storeForJob(jobID string) (store.Store, error) {
	job, ok := s.jobManager.GetJob(jobID)
	if !ok {
		return s.store, nil
	}
	return s.storeForSlug(job.Project)
}

// projectForJob returns the slug a continuation job should inherit.
func (s *Server) projectForJob(jobID string) app.Project {
	if job, ok := s.jobManager.GetJob(jobID); ok {
		return app.NormalizeProject(job.Project)
	}
	return app.DefaultProject
}

// NewServer creates a new HTTP server with optional checkpoint store.
// If store is nil, checkpointing is disabled.
func NewServer(addr string, checkpointStore store.Store) *Server {
	return NewServerWithOptions(addr, checkpointStore, ServerOptions{})
}

// NewServerWithOptions creates a server with explicit security options.
func NewServerWithOptions(addr string, checkpointStore store.Store, options ServerOptions) *Server {
	if options.MaxConcurrentJobs <= 0 {
		options.MaxConcurrentJobs = 1
	}
	if options.QueueSize <= 0 {
		options.QueueSize = 16
	}
	ctx, cancel := context.WithCancel(context.Background())
	policy, policyErr := newInputPolicy(options.InputRoots)
	server := &Server{
		jobManager: NewJobManager(),
		store:      checkpointStore,
		addr:       addr,
		ctx:        ctx,
		cancel:     cancel,
		options:    options,
		queue:      make(chan string, options.QueueSize),
		jobCancels: make(map[string]context.CancelFunc),
		input:      policy,
		inputErr:   policyErr,

		scheduleDrivers: make(map[string]struct{}),
	}
	server.projects = newProjectRegistry(options.DataRoot, checkpointStore)
	server.restorePersistedJobs()
	// Schedules are adopted after the jobs, because adopting an interrupted
	// stage needs the restored job for that stage to already be visible.
	server.restoreSchedules()
	return server
}

// Start starts the HTTP server
func (s *Server) Start() error {
	if s.options.EnablePprof && !isLoopbackAddress(s.addr) {
		return fmt.Errorf("pprof requires a loopback bind address")
	}
	handler := s.Handler()

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    app.MaxRequestBody,
	}

	slog.Info("Starting HTTP server", "addr", s.addr)
	return s.server.ListenAndServe()
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler returns the fully configured HTTP handler. It is exposed so the
// security boundary can be tested without opening a network listener.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Register UI routes
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/jobs/", s.handleJobDetail)
	mux.HandleFunc("/create", s.handleCreatePage)
	mux.HandleFunc("/schedules", s.handleCampaignList)
	mux.HandleFunc("/schedules/", s.handleCampaignDetail)
	mux.HandleFunc("/chains/", s.handleChainDetail)

	// Register API routes
	mux.HandleFunc("/api/v1/jobs", s.handleJobs)
	mux.HandleFunc("/api/v1/projects", s.handleProjects)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobsWithID)
	mux.HandleFunc("/api/v1/schedules", s.handleSchedules)
	mux.HandleFunc("/api/v1/schedules/", s.handleSchedulesWithID)
	mux.HandleFunc("/api/v1/chains", s.handleChains)
	mux.HandleFunc("/api/v1/chains/", s.handleChainsWithID)

	if s.options.EnablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return s.loggingMiddleware(s.corsMiddleware(mux))
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
		if err := s.server.Shutdown(ctx); err != nil {
			return err
		}
	}

	waited := make(chan struct{})
	go func() {
		// Schedule executors are waited on beside the job workers. An executor
		// that is mid-stage returns on the cancelled context and leaves its
		// stage record in `running`, which is what the next start adopts.
		s.scheduleWG.Wait()
		s.workerWG.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for optimization workers: %w", ctx.Err())
	}
}

func (s *Server) ensureWorkers() {
	s.workerOnce.Do(func() {
		for range s.options.MaxConcurrentJobs {
			s.workerWG.Add(1)
			go s.workerLoop()
		}
	})
}

func (s *Server) enqueueJob(jobID string) error {
	s.ensureWorkers()
	select {
	case s.queue <- jobID:
		return nil
	default:
		return ErrJobQueueFull
	}
}

func (s *Server) workerLoop() {
	defer s.workerWG.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case jobID := <-s.queue:
			job, ok := s.jobManager.GetJob(jobID)
			if !ok || job.State != StatePending {
				continue
			}
			// A job whose project cannot be resolved must not run against
			// another project's store, and the worker has nobody to return an
			// error to, so it fails the job loudly instead.
			jobStore, err := s.storeForJob(jobID)
			if err != nil {
				slog.Error("Refusing to run job with an unresolvable project",
					"job_id", jobID, "project", string(job.Project), "error", err)
				_ = s.jobManager.FailJob(jobID, "project store is unavailable")
				continue
			}
			ctx, cancel := context.WithCancel(s.ctx)
			s.cancelMu.Lock()
			s.jobCancels[jobID] = cancel
			s.cancelMu.Unlock()
			_ = runJob(ctx, s.jobManager, jobStore, jobID)
			cancel()
			s.cancelMu.Lock()
			delete(s.jobCancels, jobID)
			s.cancelMu.Unlock()
		}
	}
}

func (s *Server) requestCancellation(jobID string) error {
	job, ok := s.jobManager.GetJob(jobID)
	if !ok {
		return store.ErrNotFound
	}
	if job.State == StatePending {
		return s.jobManager.CancelJob(jobID)
	}
	if job.State != StateRunning {
		return fmt.Errorf("%w: job is %s", ErrInvalidTransition, job.State)
	}
	s.cancelMu.Lock()
	cancel := s.jobCancels[jobID]
	s.cancelMu.Unlock()
	if cancel == nil {
		return fmt.Errorf("job cancellation is not yet available")
	}
	cancel()
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
			// Resolve the owning store first: checkpointing into another
			// project's directory would be worse than not checkpointing.
			jobStore, err := s.storeForSlug(j.Project)
			if err != nil {
				slog.Error("Failed to resolve project store for shutdown checkpoint",
					"job_id", j.ID,
					"project", string(j.Project),
					"error", err,
				)
				results <- checkpointResult{jobID: j.ID, err: err}
				return
			}

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
			// This renderer only supplies the reference image and renders the
			// shutdown artifacts; it never runs an optimizer, so evaluation
			// width is irrelevant here.
			renderer := renderer.NewCPURenderer(ref, j.Config.Circles)
			renderer.SetThreads(j.Config.Threads)
			renderer.SetFastCompositing(j.Config.FastCompositing)

			// Save checkpoint
			err = saveCheckpoint(s.jobManager, jobStore, renderer, j.ID)

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
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
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
	parsedID, err := uuid.Parse(jobID)
	if err != nil || parsedID == uuid.Nil || parsedID.String() != jobID {
		writeAPIError(w, http.StatusBadRequest, "invalid_job_id", "job ID must be a canonical UUID")
		return
	}

	// Route based on subpath
	if len(parts) == 1 && r.Method == http.MethodDelete {
		s.handleDeleteJob(w, r, jobID)
	} else if len(parts) == 1 || len(parts) == 2 && parts[1] == "status" {
		s.handleGetJobStatus(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "best.png" {
		s.handleGetBestImage(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "diff.png" {
		s.handleGetDiffImage(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "ref.png" {
		s.handleGetRefImage(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "params.json" {
		s.handleGetParameters(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "report.html" {
		s.handleGetReport(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "stream" {
		s.handleJobStream(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "resume" {
		s.handleResumeJob(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "polish" {
		s.handlePolishJob(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "extend" {
		s.handleExtendJob(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "cancel" {
		s.handleCancelJob(w, r, jobID)
	} else {
		writeAPIError(w, http.StatusNotFound, "not_found", "resource not found")
	}
}

// handleCreateJob handles POST /api/v1/jobs
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var request createJobRequest
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the size limit")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return
	}
	project, err := s.resolveRequestedProject(request.Project, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_project", err.Error())
		return
	}
	config, err := app.Normalize(request.JobConfig)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	if config.PolishingOnly {
		writeAPIError(w, http.StatusBadRequest, "invalid_config", "polishingOnly jobs must be created from a completed checkpoint")
		return
	}
	if s.inputErr != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_config", "server input roots are unavailable")
		return
	}
	config.RefPath, err = s.input.resolveImage(config.RefPath)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_ref_path", err.Error())
		return
	}
	if config.CanvasPath != "" {
		config.CanvasPath, err = s.input.resolveImage(config.CanvasPath)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_canvas_path", err.Error())
			return
		}
	}

	// Creating the project store before the job keeps a failed mkdir from
	// leaving an unrunnable job behind.
	if _, err := s.ensureProject(project); err != nil {
		s.writeProjectError(w, project, err)
		return
	}

	// Create job
	job := s.jobManager.CreateJob(project, config)

	if err := s.enqueueJob(job.ID); err != nil {
		_ = s.jobManager.FailJob(job.ID, "server job queue is full")
		writeAPIError(w, http.StatusTooManyRequests, "queue_full", "server job queue is full")
		return
	}

	// Return job
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(job); err != nil {
		slog.Error("Failed to encode create-job response", "error", err)
	}
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if err := s.requestCancellation(jobID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		} else {
			writeAPIError(w, http.StatusConflict, "invalid_state", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, _ *http.Request, jobID string) {
	// Resolve the owning store before the job leaves the manager: afterwards
	// the project is unknown and the lookup could not find the real artifacts.
	// An unresolvable project fails the request instead of half-deleting the
	// job and stranding its checkpoint on disk.
	jobStore, err := s.storeForJob(jobID)
	if err != nil {
		slog.Error("Failed to resolve project store for delete", "job_id", jobID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "project_unavailable", "the project store is unavailable")
		return
	}
	if err := s.jobManager.DeleteJob(jobID); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			writeAPIError(w, http.StatusConflict, "invalid_state", "active jobs must be cancelled before deletion")
		} else {
			writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		}
		return
	}
	s.jobManager.broadcaster.CleanupJob(jobID)
	if jobStore != nil {
		if err := jobStore.DeleteCheckpoint(jobID); err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Warn("Failed to delete persisted job", "job_id", jobID, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListJobs handles GET /api/v1/jobs
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := s.jobManager.ListJobs()

	// An absent or "all" filter keeps the pre-project contract of listing every
	// job, which cmd/status.go relies on. Filtering is read-only and never
	// creates a project.
	// Boundary: the `?project=` query parameter is untrusted input. It becomes
	// an app.Project here, immediately before ValidateProjectSlug.
	if filter := app.Project(strings.TrimSpace(r.URL.Query().Get("project"))); filter != "" && filter != "all" {
		if err := app.ValidateProjectSlug(filter); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_project", err.Error())
			return
		}
		jobs = filterJobsByProject(jobs, filter)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

// filterJobsByProject keeps jobs belonging to slug, treating an empty project
// on a job as the default project.
func filterJobsByProject(jobs []*Job, slug app.Project) []*Job {
	filtered := make([]*Job, 0, len(jobs))
	for _, job := range jobs {
		if app.NormalizeProject(job.Project) == slug {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

type jobStatusResponse struct {
	ID string `json:"id"`
	// Project mirrors Job.Project so a client holding only a job ID can learn
	// which project owns it. Create and list responses serialize the Job itself
	// and so have always carried it; this projection did not, which left the
	// single-job endpoints the only way to see a job without seeing its project.
	Project               app.Project `json:"project"`
	State                 JobState    `json:"state"`
	Config                JobConfig   `json:"config"`
	BestCost              float64     `json:"bestCost"`
	CandidateCost         *float64    `json:"candidateCost,omitempty"`
	CandidatePSNR         *float64    `json:"candidatePsnr,omitempty"`
	CandidatePSNRInfinite bool        `json:"candidatePsnrInfinite,omitempty"`
	InitialCost           float64     `json:"initialCost"`
	PSNR                  *float64    `json:"psnr"`
	PSNRInfinite          bool        `json:"psnrInfinite,omitempty"`
	SSIM                  *float64    `json:"ssim,omitempty"`
	Iterations            int         `json:"iterations"`
	Evaluations           int         `json:"evaluations"`
	// EvaluationWidth is the concurrency the run measured from its renderer, and
	// is omitted when the run was serial or the width is unknown. Config carries
	// only the request, which differs whenever the backend declined it or the
	// GOMAXPROCS clamp applied, so clients comparing two runs must read this.
	EvaluationWidth int        `json:"evaluationWidth,omitempty"`
	Termination     string     `json:"termination,omitempty"`
	Elapsed         float64    `json:"elapsed"`
	CPS             float64    `json:"cps"`
	StartTime       time.Time  `json:"startTime"`
	EndTime         *time.Time `json:"endTime,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// handleGetJobStatus handles GET /api/v1/jobs/:id/status
func (s *Server) handleGetJobStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
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
		totalCircles := job.Evaluations * max(1, len(job.BestParams)/7)
		cps = float64(totalCircles) / elapsed.Seconds()
	}

	psnr, psnrInfinite := cloneFloat(job.PSNR), job.PSNRInfinite
	if len(job.BestParams) > 0 {
		psnr, psnrInfinite = serializablePSNR(job.BestCost)
	}
	response := jobStatusResponse{
		ID: job.ID, Project: app.NormalizeProject(job.Project), State: job.State, Config: job.Config,
		BestCost: job.BestCost, CandidateCost: cloneFloat(job.CandidateCost), InitialCost: job.InitialCost,
		PSNR: psnr, PSNRInfinite: psnrInfinite, SSIM: cloneFloat(job.SSIM),
		Iterations: job.Iterations, Evaluations: job.Evaluations,
		EvaluationWidth: job.EvaluationWidth,
		Termination:     job.Termination, Elapsed: elapsed.Seconds(), CPS: cps,
		StartTime: job.StartTime, EndTime: job.EndTime, Error: job.Error,
	}
	response.CandidatePSNR, response.CandidatePSNRInfinite = serializableCandidatePSNR(job.CandidateCost)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func serializableCandidatePSNR(cost *float64) (*float64, bool) {
	if cost == nil {
		return nil, false
	}
	return serializablePSNR(*cost)
}

// handleGetBestImage handles GET /api/v1/jobs/:id/best.png
func (s *Server) handleGetBestImage(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
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
	if snapshotNotModified(w, r, fmt.Sprintf(`"best-%d"`, job.BestRevision)) {
		return
	}

	// Load reference image to get dimensions
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load reference: %v", err), http.StatusInternalServerError)
		return
	}

	img, cleanup, err := renderBestSnapshot(job, ref)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "render_failed", "failed to render job snapshot")
		return
	}
	defer cleanup()

	// Set headers
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	if downloadRequested(r) {
		setAttachment(w, artifactFilename(jobID, "best.png"))
	}

	// Encode and send
	if err := png.Encode(w, img); err != nil {
		slog.Error("Failed to encode PNG", "error", err)
	}
}

// handleGetDiffImage handles GET /api/v1/jobs/:id/diff.png
func (s *Server) handleGetDiffImage(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	colormap, err := requestedColormap(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_colormap", err.Error())
		return
	}
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
	if snapshotNotModified(w, r, fmt.Sprintf(`"diff-%s-%d"`, colormap, job.BestRevision)) {
		return
	}

	// Load reference image
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load reference: %v", err), http.StatusInternalServerError)
		return
	}

	best, cleanup, err := renderBestSnapshot(job, ref)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "render_failed", "failed to render job snapshot")
		return
	}
	defer cleanup()

	diff := computeDiffImage(ref, best, colormap)

	// Set headers
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	if downloadRequested(r) {
		setAttachment(w, artifactFilename(jobID, "diff.png"))
	}

	// Encode and send
	if err := png.Encode(w, diff); err != nil {
		slog.Error("Failed to encode PNG", "error", err)
	}
}

func renderBestSnapshot(job *Job, ref *image.NRGBA) (*image.NRGBA, func(), error) {
	actualCircles := len(job.BestParams) / 7
	rend, cleanup, err := rendererForJob(job.Config, ref, actualCircles)
	if err != nil {
		return nil, func() {}, err
	}
	return rend.Render(job.BestParams), cleanup, nil
}

func snapshotNotModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") != etag {
		return false
	}
	w.WriteHeader(http.StatusNotModified)
	return true
}

// corsMiddleware enforces the trusted-local same-origin browser policy. CLI
// clients normally omit Origin and remain supported.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !sameOrigin(origin, r) {
			writeAPIError(w, http.StatusForbidden, "origin_forbidden", "cross-origin requests are not allowed")
			return
		}
		w.Header().Add("Vary", "Origin")
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin string, r *http.Request) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

type apiErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	var response apiErrorResponse
	response.Error.Code = code
	response.Error.Message = message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode API error", "error", err)
	}
}

// handleGetRefImage handles GET /api/v1/jobs/:id/ref.png
func (s *Server) handleGetRefImage(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
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
	jobStore, err := s.storeForJob(jobID)
	if err != nil {
		slog.Error("Failed to resolve project store for resume", "job_id", jobID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "project_unavailable", "the project store is unavailable")
		return
	}
	checkpoint, err := jobStore.LoadCheckpoint(jobID)
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
	config, err := app.Normalize(checkpoint.Config)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_checkpoint", "checkpoint configuration is invalid")
		return
	}
	if s.inputErr != nil {
		writeAPIError(w, http.StatusInternalServerError, "server_config", "server input roots are unavailable")
		return
	}
	config.RefPath, err = s.input.resolveImage(config.RefPath)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_checkpoint", "checkpoint reference is outside configured input roots")
		return
	}
	if config.CanvasPath != "" {
		config.CanvasPath, err = s.input.resolveImage(config.CanvasPath)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_checkpoint", "checkpoint canvas is outside configured input roots")
			return
		}
	}
	config.EffectiveSeed = checkpoint.EffectiveSeed
	config.ResumeCount = checkpoint.ResumeCount + 1
	newJob := s.jobManager.CreateJob(s.projectForJob(jobID), config)

	// Initialize the new job with checkpoint data
	s.jobManager.UpdateJob(newJob.ID, func(j *Job) {
		updateBestResult(j, checkpoint.BestParams, checkpoint.BestCost)
		j.InitialCost = checkpoint.InitialCost
		j.Iterations = checkpoint.Iteration
		j.Evaluations = int(checkpoint.Evaluations)
	})

	if err := s.enqueueJob(newJob.ID); err != nil {
		_ = s.jobManager.FailJob(newJob.ID, "server job queue is full")
		writeAPIError(w, http.StatusTooManyRequests, "queue_full", "server job queue is full")
		return
	}

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

// handlePolishJob creates a continuation that runs only transactional
// active-set polishing from a completed batch checkpoint.
type polishJobRequest struct {
	Strategy        *app.PolishingStrategy `json:"strategy,omitempty"`
	ActiveSetSize   *int                   `json:"activeSetSize,omitempty"`
	MaxSweeps       *int                   `json:"maxSweeps,omitempty"`
	Epochs          *int                   `json:"epochs,omitempty"`
	Iters           *int                   `json:"iters,omitempty"`
	StagnationIters *int                   `json:"stagnationIters,omitempty"`
	MinImprovement  *float64               `json:"minImprovement,omitempty"`
	PopSize         *int                   `json:"popSize,omitempty"`
	Seed            *int64                 `json:"seed,omitempty"`
}

func (s *Server) handlePolishJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if failure := s.requireCheckpointStore(); failure != nil {
		writeContinuationError(w, failure)
		return
	}
	var request polishJobRequest
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid polishing configuration")
		return
	}
	source, failure := s.continuationSourceFor(jobID, polishContinuation)
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}

	config := source.config
	config.PolishingEnabled = true
	config.PolishingOnly = true
	if request.Strategy != nil {
		config.PolishingStrategy = *request.Strategy
	}
	if request.ActiveSetSize != nil {
		config.PolishingActiveSetSize = *request.ActiveSetSize
	}
	if request.MaxSweeps != nil {
		config.PolishingMaxSweeps = *request.MaxSweeps
	}
	if request.Epochs != nil {
		config.PolishingEpochs = *request.Epochs
	}
	if request.Iters != nil {
		config.PolishingIters = *request.Iters
	}
	if request.StagnationIters != nil {
		config.PolishingStagnationIters = *request.StagnationIters
	}
	if request.MinImprovement != nil {
		config.PolishingMinImprovement = *request.MinImprovement
	}
	if request.PopSize != nil {
		config.PopSize = *request.PopSize
	}
	if request.Seed != nil {
		config.Seed = *request.Seed
		config.EffectiveSeed = *request.Seed
	}
	config, err := app.Normalize(config)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid polishing configuration")
		return
	}

	newJob, failure := s.startContinuation("", s.projectForJob(jobID), config, source, func(job *Job) {
		job.PolishedFrom = jobID
	})
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobId":         newJob.ID,
		"polishedFrom":  jobID,
		"state":         string(newJob.State),
		"previousCost":  source.checkpoint.BestCost,
		"strategy":      config.PolishingStrategy,
		"activeSetSize": config.PolishingActiveSetSize,
	})
}

// extendJobRequest appends a new draw-order suffix to a completed batch. The
// existing circles remain in their original slots and are never reordered.
type extendJobRequest struct {
	AdditionalCircles int    `json:"additionalCircles"`
	BatchSize         *int   `json:"batchSize,omitempty"`
	Epochs            *int   `json:"epochs,omitempty"`
	Iters             *int   `json:"iters,omitempty"`
	PopSize           *int   `json:"popSize,omitempty"`
	Seed              *int64 `json:"seed,omitempty"`
	// Polish re-enables active-set polishing once the appended circles complete.
	// It is off unless requested, so an extension stays a pure append by default.
	Polish *bool `json:"polish,omitempty"`
}

func (s *Server) handleExtendJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if failure := s.requireCheckpointStore(); failure != nil {
		writeContinuationError(w, failure)
		return
	}
	var request extendJobRequest
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid extension configuration")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return
	}
	source, failure := s.continuationSourceFor(jobID, extendContinuation)
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}
	previousCircles := source.config.Circles
	if request.AdditionalCircles < 1 || request.AdditionalCircles > app.MaxCircles-previousCircles {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "additionalCircles exceeds the supported circle limit")
		return
	}

	config := source.config
	config.Circles += request.AdditionalCircles
	config.BatchSize = min(request.AdditionalCircles, app.MaxBatchSize)
	config.PolishingEnabled = false
	config.PolishingOnly = false
	if request.BatchSize != nil {
		config.BatchSize = *request.BatchSize
	}
	if request.Epochs != nil {
		config.OptimizerEpochs = *request.Epochs
	}
	if request.Iters != nil {
		config.Iters = *request.Iters
	}
	if request.PopSize != nil {
		config.PopSize = *request.PopSize
	}
	if request.Seed != nil {
		config.Seed = *request.Seed
		config.EffectiveSeed = *request.Seed
	}
	if request.Polish != nil {
		config.PolishingEnabled = *request.Polish
	}
	config, err := app.Normalize(config)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid extension configuration")
		return
	}

	newJob, failure := s.startContinuation("", s.projectForJob(jobID), config, source, func(job *Job) {
		job.ExtendedFrom = jobID
	})
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobId":             newJob.ID,
		"extendedFrom":      jobID,
		"state":             string(newJob.State),
		"previousCost":      source.checkpoint.BestCost,
		"previousCircles":   previousCircles,
		"additionalCircles": request.AdditionalCircles,
		"targetCircles":     config.Circles,
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
