package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/cwbudde/circlefit/internal/ui"
	"github.com/google/uuid"
)

// Server represents the HTTP server.
type Server struct {
	jobManager *JobManager
	uiEvents   *UIEventHub
	store      store.Store
	projects   *projectRegistry
	addr       string
	server     *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
	options    ServerOptions
	metadata   BuildMetadata
	queue      chan string
	workerOnce sync.Once
	workerWG   sync.WaitGroup
	cancelMu   sync.Mutex
	jobCancels map[string]context.CancelFunc
	input      *inputPolicy
	inputErr   error
	// optimizerVersionOverride replaces the compiled-in optimizer version when
	// set. Only tests set it; see (*Server).optimizerVersion.
	optimizerVersionOverride string
	// schedulesMu guards scheduleDrivers, which holds one entry per schedule
	// with a live executor. It is the in-process guarantee that a schedule
	// never has two executors, and so never two jobs for one stage.
	schedulesMu     sync.Mutex
	scheduleDrivers map[string]struct{}
	scheduleWG      sync.WaitGroup

	chainCacheMu    sync.Mutex
	chainCache      []discoveredChain
	chainCacheValid bool

	// refImageMu guards refImageCache, which memoizes the reference-image
	// facts per path. The status endpoint is polled, and decoding an image
	// header on every poll is work the answer never changes for; see
	// (*Server).referenceImageFacts.
	refImageMu    sync.Mutex
	refImageCache map[string]referenceImageFacts
}

// referenceImageFacts is one memoized answer from referenceImageMetadata,
// together with what it was true of. modTime and size identify the file the
// dimensions were read from, so a reference image replaced under a running
// server is picked up rather than served stale.
type referenceImageFacts struct {
	width   int
	height  int
	size    int64
	modTime time.Time
}

// ServerOptions configures the trusted-local HTTP boundary.
type ServerOptions struct {
	EnablePprof       bool
	MaxConcurrentJobs int
	QueueSize         int
	InputRoots        []string
	BuildMetadata     BuildMetadata
	// DefaultBackend is used when a create-job request omits backend.
	DefaultBackend app.Backend
	// DataRoot enables multi-project support. When empty the server holds a
	// single project backed by the store passed to NewServerWithOptions, which
	// is what keeps store-injecting callers and tests working unchanged.
	DataRoot string
}

var (
	ErrJobQueueFull             = errors.New("job queue is full")
	errPausedWithoutCheckpoint  = errors.New("pause requires a checkpointable state")
	errScheduleStagePause       = errors.New("a schedule stage is paused through its schedule")
	errResumeCheckpointOverflow = errors.New("resume checkpoint evaluation count is out of range")
)

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

	options.DefaultBackend = normalizeServerBackend(options.DefaultBackend)
	ctx, cancel := context.WithCancel(context.Background())
	policy, policyErr := newInputPolicy(options.InputRoots)
	jobManager := NewJobManager()
	server := &Server{
		jobManager: jobManager,
		uiEvents:   jobManager.uiEvents,
		store:      checkpointStore,
		addr:       addr,
		ctx:        ctx,
		cancel:     cancel,
		options:    options,
		metadata:   normalizeBuildMetadata(options.BuildMetadata),
		queue:      make(chan string, options.QueueSize),
		jobCancels: make(map[string]context.CancelFunc),
		input:      policy,
		inputErr:   policyErr,

		scheduleDrivers: make(map[string]struct{}),
	}
	jobManager.onJobSetChanged = server.invalidateChainCache
	server.projects = newProjectRegistry(options.DataRoot, checkpointStore)
	server.restorePersistedJobs()
	// Schedules are adopted after the jobs, because adopting an interrupted
	// stage needs the restored job for that stage to already be visible.
	server.restoreSchedules()

	return server
}

func normalizeServerBackend(raw app.Backend) app.Backend {
	switch renderer.NormalizeBackend(string(raw)) {
	case renderer.BackendCPU:
		return app.BackendCPU
	case renderer.BackendOpenCL:
		return app.BackendOpenCL
	default:
		if raw != "" {
			slog.Warn("Invalid server default backend, falling back to cpu", "backend", raw)
		}

		return app.BackendCPU
	}
}

// applyDefaultBackend fills in the server-wide default for a configuration that
// names no backend and canonicalizes whatever remains. Every job entry point
// runs through it, so a dashboard or schedule job honours `serve --backend` the
// same way an API request does. A whitespace-only value counts as omission,
// because NormalizeBackend trims before matching and would otherwise resolve it
// to CPU behind the default's back.
func (s *Server) applyDefaultBackend(config *JobConfig) {
	if strings.TrimSpace(string(config.Backend)) == "" {
		config.Backend = s.options.DefaultBackend
	}

	config.Backend = app.Backend(renderer.NormalizeBackend(string(config.Backend)))
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	if s.options.EnablePprof && !isLoopbackAddress(s.addr) {
		return errors.New("pprof requires a loopback bind address")
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
	mux.HandleFunc("/", s.handleDashboardPage)
	mux.HandleFunc("/jobs", s.handleJobsPage)
	mux.HandleFunc("/jobs/", s.handleJobDetail)
	mux.HandleFunc("/settings", s.handleSettingsPage)
	mux.HandleFunc("/create", s.handleCreatePage)
	mux.HandleFunc("/schedules", s.handleCampaignList)
	mux.HandleFunc("/schedules/", s.handleCampaignDetail)
	mux.HandleFunc("/chains/", s.handleChainDetail)

	// Embedded frontend assets. Nothing below this prefix touches the
	// filesystem; ui.StaticHandler serves only what is compiled into the
	// binary.
	mux.Handle(ui.StaticPrefix, ui.StaticHandler())

	// Register API routes
	mux.HandleFunc("/api/v1/jobs", s.handleJobs)
	mux.HandleFunc("/api/v1/stream", s.handleAllJobStream)
	mux.HandleFunc("/api/v1/events", s.handleUIEvents)
	mux.HandleFunc("/api/v1/projects", s.handleProjects)
	mux.HandleFunc("/api/v1/dashboard", s.handleDashboard)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobsWithID)
	mux.HandleFunc("/api/v1/system", s.handleSystem)
	mux.HandleFunc("/api/v1/schedules", s.handleSchedules)
	mux.HandleFunc("/api/v1/schedules/", s.handleSchedulesWithID)
	mux.HandleFunc("/api/v1/chains", s.handleChains)
	mux.HandleFunc("/api/v1/chains/", s.handleChainsWithID)
	mux.HandleFunc("/api/v1/campaigns", s.handleCampaignViewList)
	mux.HandleFunc("/api/v1/campaigns/", s.handleCampaignViewDetail)

	// Catch-all for the API subtree. Without it an unrouted /api/v1 path falls
	// through to the dashboard handler and answers with a plain-text 404, which
	// would break the promise that every API failure parses as the JSON error
	// envelope. The mux prefers the longest registered pattern, so the routes
	// above still win.
	mux.HandleFunc("/api/v1/", s.handleAPINotFound)

	if s.options.EnablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return s.loggingMiddleware(s.corsMiddleware(staticPathGuard(mux)))
}

// staticPathGuard answers non-canonical /static/ paths with 404 instead of
// letting http.ServeMux redirect them.
//
// ServeMux replies to a non-canonical path with a 307 to the cleaned one, so
// "/static/../go.mod" becomes a redirect to "/go.mod" and leaves the static
// prefix behind. Nothing is exposed by that today — the cleaned path lands on
// the catch-all, which serves only "/" and 404s everything else — but it makes
// the boundary of an embedded-asset route depend on what some unrelated route
// happens to do. Rejecting the request outright keeps the property local to
// the prefix: below /static/, only names that are actually embedded answer,
// and nothing walks anywhere.
//
// Only the /static/ prefix is guarded; every other route keeps the mux's
// ordinary redirect behavior.
func staticPathGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, ui.StaticPrefix) && !isCanonicalPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isCanonicalPath reports whether p is already the path http.ServeMux would
// redirect it to, mirroring net/http's own cleanPath: a trailing slash is
// significant to the mux but is dropped by path.Clean.
func isCanonicalPath(p string) bool {
	cleaned := path.Clean(p)
	if strings.HasSuffix(p, "/") && cleaned != "/" {
		cleaned += "/"
	}

	return cleaned == p
}

// Shutdown gracefully shuts down the server.
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
		err := s.server.Shutdown(ctx)
		if err != nil {
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
			if !ok || (job.State != StatePending && job.State != StateRunning) {
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

	if job.State == StatePaused {
		return s.jobManager.CancelJob(jobID)
	}

	if job.State != StateRunning {
		return fmt.Errorf("%w: job is %s", ErrInvalidTransition, job.State)
	}

	s.cancelMu.Lock()
	cancel := s.jobCancels[jobID]
	s.cancelMu.Unlock()

	if cancel == nil {
		return errors.New("job cancellation is not yet available")
	}

	cancel()

	return nil
}

// requestPause pauses one running job. The paused state is claimed before the
// checkpoint is written so the snapshot cannot outlive the state it describes;
// a failed write puts the job back to running rather than leaving it paused
// without anything to resume from.
func (s *Server) requestPause(jobID string) error {
	job, ok := s.jobManager.GetJob(jobID)
	if !ok {
		return store.ErrNotFound
	}
	// A stage of a campaign is driven by the schedule executor, which waits for
	// a terminal job state and would wait forever on a paused one. Pausing a
	// campaign is the schedule's own action, and it stops at a stage boundary.
	if job.ScheduleID != "" {
		return errScheduleStagePause
	}

	claimed, err := s.jobManager.claimPause(jobID)
	if err != nil {
		return err
	}

	if err := s.persistPauseCheckpoint(jobID, claimed); err != nil {
		resumeErr := s.jobManager.ResumeJob(jobID)
		if resumeErr != nil && !errors.Is(resumeErr, ErrInvalidTransition) {
			slog.Error("Unable to restore a job after a failed pause", "job_id", jobID, "error", resumeErr)
		}

		return err
	}

	s.invalidateChainCache()

	if paused, ok := s.jobManager.GetJob(jobID); ok {
		s.jobManager.broadcaster.Broadcast(jobProgressSnapshot(paused))
	}

	s.cancelMu.Lock()
	cancel := s.jobCancels[jobID]
	s.cancelMu.Unlock()

	if cancel != nil {
		cancel()
	}

	return nil
}

func (s *Server) requestResume(jobID string, allowOptimizerMismatch bool) (*store.Checkpoint, error) {
	job, ok := s.jobManager.GetJob(jobID)
	if !ok {
		return nil, store.ErrNotFound
	}

	if job.State != StatePaused {
		return nil, fmt.Errorf("%w: job is %s", ErrInvalidTransition, job.State)
	}

	jobStore, err := s.storeForJob(jobID)
	if err != nil {
		return nil, err
	}

	if jobStore == nil {
		return nil, errPausedWithoutCheckpoint
	}

	checkpoint, err := jobStore.LoadCheckpoint(jobID)
	if err != nil {
		return nil, err
	}

	if err := checkpoint.Validate(); err != nil {
		return nil, err
	}

	if int64(int(checkpoint.Evaluations)) != checkpoint.Evaluations {
		return nil, errResumeCheckpointOverflow
	}

	warning, err := opt.GuardCheckpointVersion(
		optimizerLibraryName(checkpoint.Config.ResolvedOptimizer()),
		checkpoint.OptimizerVersion,
		s.optimizerVersion(checkpoint.Config.ResolvedOptimizer()),
		allowOptimizerMismatch,
	)
	if err != nil {
		return nil, err
	}

	if warning != "" {
		slog.Warn("Optimizer version check", "job_id", jobID, "warning", warning)
	}

	if err := s.jobManager.UpdateJob(jobID, func(j *Job) {
		j.BestParams = append([]float64(nil), checkpoint.BestParams...)
		j.BestCost = checkpoint.BestCost
		j.InitialCost = checkpoint.InitialCost
		j.Iterations = checkpoint.Iterations
		j.Evaluations = int(checkpoint.Evaluations)
		j.Config.EffectiveSeed = checkpoint.EffectiveSeed
		j.Config.ResumeCount = checkpoint.ResumeCount + 1
		j.CandidateCost = nil
		j.Error = ""
		j.Termination = checkpoint.Termination
	}); err != nil {
		return nil, err
	}

	if err := s.jobManager.ResumeJob(jobID); err != nil {
		return nil, err
	}

	return checkpoint, nil
}

func (s *Server) persistPauseCheckpoint(jobID string, job *Job) error {
	if s.store == nil {
		return errPausedWithoutCheckpoint
	}

	if job == nil {
		return store.ErrNotFound
	}

	if len(job.BestParams) == 0 || math.IsNaN(job.BestCost) || math.IsInf(job.BestCost, 0) {
		return errPausedWithoutCheckpoint
	}

	jobStore, err := s.storeForJob(jobID)
	if err != nil {
		return err
	}

	if jobStore == nil {
		return errPausedWithoutCheckpoint
	}

	checkpoint := store.NewCheckpoint(job.ID, job.BestParams, job.BestCost, job.InitialCost, job.Iterations, job.Config)
	checkpoint.Evaluations = int64(job.Evaluations)
	checkpoint.Termination = job.Termination
	applyJobLineage(checkpoint, job)

	if err := jobStore.SaveCheckpoint(jobID, checkpoint); err != nil {
		slog.Error("Failed to persist pause checkpoint", "job_id", jobID, "error", err)
		return err
	}

	return nil
}

// checkpointRunningJobs saves checkpoints for all running jobs.
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
				results <- checkpointResult{jobID: j.ID, err: errors.New("job not found")}
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

	for range runningJobs {
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

// handleJobs handles /api/v1/jobs.
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

// handleSystem handles GET /api/v1/system.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	facts := HostFactsFromMetadata(s.metadata)

	w.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(w).Encode(facts)
	if err != nil {
		slog.Error("Failed to encode system facts response", "error", err)
	}
}

// handleJobsWithID handles /api/v1/jobs/:id/*.
func (s *Server) handleJobsWithID(w http.ResponseWriter, r *http.Request) {
	// Parse job ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")

	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_job_id", "job ID required")
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
	} else if len(parts) == 2 && parts[1] == "metrics" {
		s.handleGetJobMetrics(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "report.html" {
		s.handleGetReport(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "stream" {
		s.handleJobStream(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "resume" {
		s.handleResumeJob(w, r, jobID)
	} else if len(parts) == 2 && parts[1] == "pause" {
		s.handlePauseJob(w, r, jobID)
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

// handleCreateJob handles POST /api/v1/jobs.
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)
	// The body is kept, not just decoded: which keys the caller actually wrote
	// is the only thing that separates an omitted field from an explicit zero,
	// and app.NormalizeRequest needs it to refuse a value the defaults would
	// otherwise swallow.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the size limit")
			return
		}

		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unable to read request body")

		return
	}
	var request createJobRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
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

	s.applyDefaultBackend(&request.JobConfig)

	config, err := app.NormalizeRequest(body, request.JobConfig)
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

	err := s.requestCancellation(jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		} else {
			writeAPIError(w, http.StatusConflict, "invalid_state", err.Error())
		}

		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handlePauseJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	err := s.requestPause(jobID)
	if err != nil {
		slog.Warn("Failed to pause job", "job_id", jobID, "error", err)

		switch {
		case errors.Is(err, store.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		case errors.Is(err, opt.ErrOptimizerVersionMismatch):
			writeAPIError(w, http.StatusConflict, "optimizer_version_mismatch", err.Error())
		case errors.Is(err, errPausedWithoutCheckpoint):
			writeAPIError(w, http.StatusConflict, "invalid_state", "job has no checkpointable progress yet")
		case errors.Is(err, errScheduleStagePause):
			writeAPIError(w, http.StatusConflict, "invalid_state", "pause the schedule that owns this stage instead")
		case errors.Is(err, ErrInvalidTransition):
			writeAPIError(w, http.StatusConflict, "invalid_state", "job cannot be paused in its current state")
		default:
			writeAPIError(w, http.StatusInternalServerError, "pause_failed", "unable to pause job")
		}

		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, _ *http.Request, jobID string) {
	deletedJob, _ := s.jobManager.GetJob(jobID)
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
		err := jobStore.DeleteCheckpoint(jobID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Warn("Failed to delete persisted job", "job_id", jobID, "error", err)
		}
	}
	// Publish campaign invalidation after the filesystem operation. Publishing
	// before it lets an eager browser rebuild and cache the chain while the
	// deleted checkpoint is still visible, with no later event to correct it.
	if deletedJob != nil {
		if deletedJob.ScheduleID != "" {
			s.publishScheduleChanged(deletedJob.ScheduleID)
		} else {
			s.publishChainsChanged()
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleListJobs handles GET /api/v1/jobs.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := s.jobManager.ListJobSummaries()

	// An absent or "all" filter keeps the pre-project contract of listing every
	// job. Filtering is read-only and never creates a project.
	// Boundary: the `?project=` query parameter is untrusted input. It becomes
	// an app.Project here, immediately before ValidateProjectSlug.
	if filter := app.Project(strings.TrimSpace(r.URL.Query().Get("project"))); filter != "" && filter != "all" {
		err := app.ValidateProjectSlug(filter)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_project", err.Error())
			return
		}

		jobs = filterJobSummariesByProject(jobs, filter)
	}

	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()
	if !query.Has("limit") && !query.Has("cursor") {
		// Preserve the original array response for clients that have not opted in
		// to pagination. The browser and bundled CLI always request bounded pages.
		err := json.NewEncoder(w).Encode(jobs)
		if err != nil {
			slog.Error("Failed to encode job list response", "error", err)
		}

		return
	}

	limit := defaultJobListLimit

	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxJobListLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maxJobListLimit))
			return
		}

		limit = parsed
	}

	page, err := paginateJobSummaries(jobs, strings.TrimSpace(query.Get("cursor")), limit)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}

	if err := json.NewEncoder(w).Encode(page); err != nil {
		slog.Error("Failed to encode job list response", "error", err)
	}
}

const (
	defaultJobListLimit = 100
	maxJobListLimit     = 500
)

type jobListPage struct {
	Jobs       []JobSummary `json:"jobs"`
	NextCursor string       `json:"nextCursor,omitempty"`
	Total      int          `json:"total"`
}

type jobListCursor struct {
	StartTime time.Time `json:"startTime"`
	ID        string    `json:"id"`
}

func paginateJobSummaries(jobs []JobSummary, rawCursor string, limit int) (jobListPage, error) {
	start := 0

	if rawCursor != "" {
		cursor, err := decodeJobListCursor(rawCursor)
		if err != nil {
			return jobListPage{}, err
		}

		start = sort.Search(len(jobs), func(i int) bool {
			return jobs[i].StartTime.Before(cursor.StartTime) ||
				(jobs[i].StartTime.Equal(cursor.StartTime) && jobs[i].ID > cursor.ID)
		})
	}

	end := min(start+limit, len(jobs))

	page := jobListPage{Jobs: jobs[start:end], Total: len(jobs)}
	if end < len(jobs) && end > start {
		page.NextCursor = encodeJobListCursor(jobs[end-1])
	}

	return page, nil
}

func encodeJobListCursor(job JobSummary) string {
	payload, _ := json.Marshal(jobListCursor{StartTime: job.StartTime, ID: job.ID})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeJobListCursor(raw string) (jobListCursor, error) {
	if len(raw) > 512 {
		return jobListCursor{}, errors.New("job list cursor is too long")
	}

	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return jobListCursor{}, err
	}
	var cursor jobListCursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&cursor); err != nil {
		return jobListCursor{}, err
	}

	if cursor.StartTime.IsZero() || cursor.ID == "" || decoder.Decode(&struct{}{}) != io.EOF {
		return jobListCursor{}, errors.New("invalid job list cursor")
	}

	return cursor, nil
}

func filterJobSummariesByProject(jobs []JobSummary, slug app.Project) []JobSummary {
	filtered := make([]JobSummary, 0, len(jobs))
	for _, job := range jobs {
		if app.NormalizeProject(job.Project) == slug {
			filtered = append(filtered, job)
		}
	}

	return filtered
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
	RequestedCircles      int         `json:"requestedCircles"`
	ActualCircles         int         `json:"actualCircles"`
	BestCost              float64     `json:"bestCost"`
	BestRevision          uint64      `json:"bestRevision"`
	CandidateCost         *float64    `json:"candidateCost,omitempty"`
	CandidatePSNR         *float64    `json:"candidatePsnr,omitempty"`
	CandidatePSNRInfinite bool        `json:"candidatePsnrInfinite,omitempty"`
	InitialCost           float64     `json:"initialCost"`
	PSNR                  *float64    `json:"psnr"`
	PSNRInfinite          bool        `json:"psnrInfinite,omitempty"`
	SSIM                  *float64    `json:"ssim,omitempty"`
	Iterations            int         `json:"iterations"`
	Evaluations           int         `json:"evaluations"`
	MaxIterations         int         `json:"maxIterations,omitempty"`
	Actions               *jobActions `json:"actions,omitempty"`
	// EvaluationWidth is the concurrency the run measured from its renderer, and
	// is omitted when the run was serial or the width is unknown. Config carries
	// only the request, which differs whenever the backend declined it or the
	// GOMAXPROCS clamp applied, so clients comparing two runs must read this.
	EvaluationWidth int `json:"evaluationWidth,omitempty"`
	// RefWidth, RefHeight and RefSize describe the reference image file itself,
	// not the canvas the run evaluates against. They are omitted rather than
	// zeroed when the file cannot be probed, because a client that received
	// zeros could not tell a missing image apart from a genuine 0x0 image of
	// zero bytes and would render one as the other.
	RefWidth    int        `json:"refWidth,omitempty"`
	RefHeight   int        `json:"refHeight,omitempty"`
	RefSize     int64      `json:"refSize,omitempty"`
	Termination string     `json:"termination,omitempty"`
	Elapsed     float64    `json:"elapsed"`
	CPS         float64    `json:"cps"`
	StartTime   time.Time  `json:"startTime"`
	EndTime     *time.Time `json:"endTime,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type jobActions struct {
	Pause  bool `json:"pause"`
	Resume bool `json:"resume"`
	Cancel bool `json:"cancel"`
	Delete bool `json:"delete"`
	Polish bool `json:"polish"`
}

// handleGetJobStatus handles GET /api/v1/jobs/:id/status.
func (s *Server) handleGetJobStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	elapsed := jobElapsed(job)
	cps := circlesPerSecond(job, elapsed)

	psnr, psnrInfinite := cloneFloat(job.PSNR), job.PSNRInfinite
	if len(job.BestParams) > 0 {
		psnr, psnrInfinite = serializablePSNR(job.BestCost)
	}

	actions := s.jobActions(job)

	// The error is deliberately dropped, exactly as the job detail page drops
	// it: the reference image is a display fact, not a job fact, so a missing
	// or unreadable file leaves the three fields at zero (and so unserialized)
	// instead of failing a status request that is otherwise answerable.
	refWidth, refHeight, refSize, _ := s.referenceImageFactsFor(job.Config.RefPath)

	response := jobStatusResponse{
		ID: job.ID, Project: app.NormalizeProject(job.Project), State: job.State, Config: job.Config,
		RequestedCircles: job.RequestedCircles, ActualCircles: job.ActualCircles,
		BestCost: job.BestCost, BestRevision: job.BestRevision,
		CandidateCost: cloneFloat(job.CandidateCost), InitialCost: job.InitialCost,
		PSNR: psnr, PSNRInfinite: psnrInfinite, SSIM: cloneFloat(job.SSIM),
		Iterations: job.Iterations, Evaluations: job.Evaluations,
		MaxIterations:   plannedOptimizerIterations(job.Config),
		Actions:         &actions,
		EvaluationWidth: job.EvaluationWidth,
		RefWidth:        refWidth, RefHeight: refHeight, RefSize: refSize,
		Termination: job.Termination, Elapsed: elapsed.Seconds(), CPS: cps,
		StartTime: job.StartTime, EndTime: job.EndTime, Error: job.Error,
	}
	response.CandidatePSNR, response.CandidatePSNRInfinite = serializableCandidatePSNR(job.CandidateCost)

	w.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		slog.Error("Failed to encode job status response", "error", err)
	}
}

func (s *Server) jobActions(job *Job) jobActions {
	if job == nil {
		return jobActions{}
	}

	terminal := job.State == StateCompleted || job.State == StateFailed || job.State == StateCancelled

	return jobActions{
		Pause: job.State == StateRunning && job.ScheduleID == "" && s.store != nil &&
			len(job.BestParams) == job.Config.Circles*7,
		Resume: job.State == StatePaused && s.store != nil,
		Cancel: job.State == StatePending || job.State == StateRunning || job.State == StatePaused,
		Delete: terminal,
		Polish: s.store != nil && job.State == StateCompleted && job.Config.Mode == app.ModeBatch &&
			len(job.BestParams) == job.Config.Circles*7,
	}
}

const (
	defaultJobMetricsLimit = 1000
	maxJobMetricsLimit     = 5000
)

// handleGetJobMetrics returns a bounded tail of the in-memory live history.
// It exists separately from status so CLI status responses stay small.
func (s *Server) handleGetJobMetrics(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	limit := defaultJobMetricsLimit

	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxJobMetricsLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maxJobMetricsLimit))
			return
		}

		limit = parsed
	}

	job, ok := s.jobManager.GetJob(jobID)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	history := job.MetricHistory
	if len(history) > limit {
		history = history[len(history)-limit:]
	}

	if history == nil {
		history = []MetricSample{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(history)
}

func serializableCandidatePSNR(cost *float64) (*float64, bool) {
	if cost == nil {
		return nil, false
	}

	return serializablePSNR(*cost)
}

// handleGetBestImage handles GET /api/v1/jobs/:id/best.png.
func (s *Server) handleGetBestImage(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	// Check if job has results
	if len(job.BestParams) == 0 {
		writeAPIError(w, http.StatusNotFound, "no_results", "no results yet")
		return
	}

	if snapshotNotModified(w, r, fmt.Sprintf(`"best-%d"`, job.BestRevision)) {
		return
	}

	// Load reference image to get dimensions
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		slog.Error("Failed to load reference image", "job_id", jobID, "path", job.Config.RefPath, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "reference_load_failed", "failed to load reference image")

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

// handleGetDiffImage handles GET /api/v1/jobs/:id/diff.png.
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
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	// Check if job has results
	if len(job.BestParams) == 0 {
		writeAPIError(w, http.StatusNotFound, "no_results", "no results yet")
		return
	}

	if snapshotNotModified(w, r, fmt.Sprintf(`"diff-%s-%d"`, colormap, job.BestRevision)) {
		return
	}

	// Load reference image
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		slog.Error("Failed to load reference image", "job_id", jobID, "path", job.Config.RefPath, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "reference_load_failed", "failed to load reference image")

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

	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		slog.Error("Failed to encode API error", "error", err)
	}
}

// handleAPINotFound answers any /api/v1 path that no route claims.
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	// The requested path is deliberately not echoed back into the response.
	slog.Debug("No API route for request", "method", r.Method, "path", r.URL.Path)
	writeAPIError(w, http.StatusNotFound, "not_found", "no API endpoint at this path")
}

// handleGetRefImage handles GET /api/v1/jobs/:id/ref.png.
func (s *Server) handleGetRefImage(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	// Load reference image
	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		slog.Error("Failed to load reference image", "job_id", jobID, "path", job.Config.RefPath, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "reference_load_failed", "failed to load reference image")

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

// handleResumeJob handles POST /api/v1/jobs/:id/resume. The route carries two
// continuations that the job's own state tells apart. A paused job is one an
// operator suspended, so it resumes in place, under its own ID, from the
// snapshot the pause wrote. Any other job resumes the way it always has: its
// checkpoint seeds a new job, which is the answer the `resume` CLI command and
// the release lifecycle expect for a run that has already stopped.
func (s *Server) handleResumeJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	failure := s.requireCheckpointStore()
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}

	allowOptimizerMismatch := boolQueryParam(r, "allowOptimizerMismatch")

	if job, ok := s.jobManager.GetJob(jobID); ok && job.State == StatePaused {
		s.resumePausedJob(w, jobID, allowOptimizerMismatch)
		return
	}

	s.forkJobFromCheckpoint(w, jobID, allowOptimizerMismatch)
}

// optimizerVersion reports the version this server resumes a checkpoint of the
// given engine under. The override exists for tests: a test binary carries no
// module information, so the library lookups cannot produce a version to
// disagree with.
func (s *Server) optimizerVersion(optimizer app.Optimizer) string {
	if s.optimizerVersionOverride != "" {
		return s.optimizerVersionOverride
	}

	switch optimizer {
	case app.OptimizerDragonfly:
		return opt.DragonflyLibraryVersion()
	case app.OptimizerCMAES:
		return opt.CMAESLibraryVersion()
	}

	return opt.LibraryVersion()
}

// optimizerLibraryName reports the human-readable library name used in
// operator-facing messages, so a Dragonfly checkpoint is never described as a
// MayFly one.
func optimizerLibraryName(optimizer app.Optimizer) string {
	switch optimizer {
	case app.OptimizerDragonfly:
		return "Dragonfly"
	case app.OptimizerCMAES:
		return "CMA-ES"
	}

	return "MayFly"
}

// boolQueryParam reads an opt-in query flag. Only an explicit true enables it,
// so a malformed value fails closed rather than silently overriding a guard.
func boolQueryParam(r *http.Request, name string) bool {
	value, err := strconv.ParseBool(r.URL.Query().Get(name))
	return err == nil && value
}

// resumePausedJob restarts a suspended job under its own identity.
func (s *Server) resumePausedJob(w http.ResponseWriter, jobID string, allowOptimizerMismatch bool) {
	checkpoint, err := s.requestResume(jobID, allowOptimizerMismatch)
	if err != nil {
		slog.Warn("Failed to resume job", "job_id", jobID, "error", err)

		var validationErr *store.ValidationError
		switch {
		case errors.As(err, &validationErr):
			writeAPIError(w, http.StatusBadRequest, "invalid_checkpoint", validationErr.Error())
		case errors.Is(err, store.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "job or checkpoint not found")
		case errors.Is(err, errResumeCheckpointOverflow):
			writeAPIError(w, http.StatusBadRequest, "invalid_checkpoint", "checkpoint evaluation count is out of range")
		case errors.Is(err, ErrInvalidTransition):
			writeAPIError(w, http.StatusConflict, "invalid_state", err.Error())
		case errors.Is(err, opt.ErrOptimizerVersionMismatch):
			writeAPIError(w, http.StatusConflict, "optimizer_version_mismatch", err.Error())
		case errors.Is(err, errPausedWithoutCheckpoint):
			writeAPIError(w, http.StatusConflict, "invalid_state", "job has no checkpoint to resume from")
		default:
			writeAPIError(w, http.StatusInternalServerError, "resume_failed", "unable to resume job")
		}

		return
	}

	if err := s.enqueueJob(jobID); err != nil {
		resumeErr := s.jobManager.PauseJob(jobID)
		if resumeErr != nil {
			slog.Warn("Failed to restore job to paused state", "job_id", jobID, "error", resumeErr)
		}

		if errors.Is(err, ErrJobQueueFull) {
			writeAPIError(w, http.StatusTooManyRequests, "queue_full", "server job queue is full")
		} else {
			writeAPIError(w, http.StatusInternalServerError, "resume_failed", "unable to resume job")
		}

		return
	}

	slog.Info("Resuming job from checkpoint", "job_id", jobID, "iteration", checkpoint.Iteration, "best_cost", checkpoint.BestCost)
	response := map[string]any{
		"jobId":         jobID,
		"resumedFrom":   jobID,
		"state":         string(StateRunning),
		"previousCost":  checkpoint.BestCost,
		"previousIters": checkpoint.Iterations,
		"message":       "Job resumed from checkpoint",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode resume-job response", "error", err)
	}
}

// forkJobFromCheckpoint seeds a new job from a stopped job's checkpoint. The
// source job is left exactly as it is, so a cancelled run stays cancelled and
// its artifacts stay addressable under the original ID.
func (s *Server) forkJobFromCheckpoint(w http.ResponseWriter, jobID string, allowOptimizerMismatch bool) {
	jobStore, err := s.storeForJob(jobID)
	if err != nil {
		slog.Error("Failed to resolve project store for resume", "job_id", jobID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "project_unavailable", "the project store is unavailable")

		return
	}

	checkpoint, err := jobStore.LoadCheckpoint(jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "job or checkpoint not found")
			return
		}

		slog.Error("Failed to load checkpoint for resume", "job_id", jobID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "resume_failed", "unable to load checkpoint")

		return
	}

	if err := checkpoint.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_checkpoint", err.Error())
		return
	}

	warning, err := opt.GuardCheckpointVersion(
		optimizerLibraryName(checkpoint.Config.ResolvedOptimizer()),
		checkpoint.OptimizerVersion,
		s.optimizerVersion(checkpoint.Config.ResolvedOptimizer()),
		allowOptimizerMismatch,
	)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "optimizer_version_mismatch", err.Error())
		return
	}

	if warning != "" {
		slog.Warn("Optimizer version check", "job_id", jobID, "warning", warning)
	}

	slog.Info("Resuming job from checkpoint",
		"job_id", jobID,
		"iteration", checkpoint.Iteration,
		"best_cost", checkpoint.BestCost,
	)

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

	if err := s.jobManager.UpdateJob(newJob.ID, func(j *Job) {
		updateBestResult(j, checkpoint.BestParams, checkpoint.BestCost)
		j.InitialCost = checkpoint.InitialCost
		j.Iterations = checkpoint.Iteration
		j.Evaluations = int(checkpoint.Evaluations)
	}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "resume_failed", "unable to seed the resumed job")
		return
	}

	if err := s.enqueueJob(newJob.ID); err != nil {
		_ = s.jobManager.FailJob(newJob.ID, "server job queue is full")

		writeAPIError(w, http.StatusTooManyRequests, "queue_full", "server job queue is full")

		return
	}

	response := map[string]any{
		"jobId":         newJob.ID,
		"resumedFrom":   jobID,
		"state":         string(newJob.State),
		"previousCost":  checkpoint.BestCost,
		"previousIters": checkpoint.Iteration,
		"message":       "Job resumed successfully from checkpoint",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode fork-job response", "error", err)
	}
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
	config.InitialCircles = nil
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
	// popSize on a polish request addresses the polishing population, which is
	// the only population this continuation ever runs at. It used to reach the
	// polisher through the job-wide PopSize; now that polishing has a population
	// of its own, the request field keeps meaning what it always meant.
	if request.PopSize != nil {
		config.PolishingPopSize = *request.PopSize
	}

	if request.Seed != nil {
		config.Seed = *request.Seed
		config.EffectiveSeed = *request.Seed
	}

	config, err := app.Normalize(config)
	if err != nil {
		// The parent's engine is the failure this endpoint sees in practice: a
		// completed CMA-ES job inherits its optimizer into the continuation,
		// polishing is MayFly-only, and a fixed envelope would report a
		// deliberate restriction as an unexplained bad request. Validation
		// messages name their field, so this is the same disclosure the job
		// creation endpoint already makes at the same trust boundary.
		writeAPIError(w, http.StatusBadRequest, "invalid_config", err.Error())
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
	// A continuation is seeded from its parent checkpoint, never from the
	// arrangement someone authored for the parent. Carrying the list forward
	// would be ignored at best, and here it is worse than ignored: the exact
	// count check would reject every extension of a seeded job, because the
	// retained list still holds the parent's circle count. Schedule expansion
	// clears the field for the same reason.
	config.InitialCircles = nil
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

type responseStatusWriter struct {
	http.ResponseWriter

	status int
}

func (w *responseStatusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}

	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(body)
}

func (w *responseStatusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *responseStatusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}

	return w.status
}

type responseStatusFlusher struct {
	*responseStatusWriter
}

func (w *responseStatusFlusher) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}

	w.ResponseWriter.(http.Flusher).Flush()
}

// loggingMiddleware logs HTTP requests with a server-controlled correlation ID.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)

		statusWriter := &responseStatusWriter{ResponseWriter: w}

		var responseWriter http.ResponseWriter = statusWriter
		if _, ok := w.(http.Flusher); ok {
			responseWriter = &responseStatusFlusher{responseStatusWriter: statusWriter}
		}

		next.ServeHTTP(responseWriter, r)
		slog.Debug("HTTP request",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", statusWriter.statusCode(),
			"duration", time.Since(start),
		)
	})
}
