package server

import (
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

// handleDashboardPage handles GET /.
func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
	// Only handle exact root path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	payload := s.dashboardPagePayload()

	jobs := make([]ui.DashboardRunningJob, 0, len(payload.RunningJobs))
	for _, runningJob := range payload.RunningJobs {
		jobs = append(jobs, ui.DashboardRunningJob{
			ID:               runningJob.ID,
			Project:          string(runningJob.Project),
			State:            runningJob.State,
			Iterations:       runningJob.Iterations,
			MaxIters:         runningJob.MaxIters,
			Circles:          runningJob.Circles,
			RequestedCircles: runningJob.RequestedCircles,
			BestCost:         runningJob.BestCost,
			InitialCost:      runningJob.InitialCost,
			CPS:              runningJob.CPS,
			EvaluationWidth:  runningJob.EvaluationWidth,
			ElapsedSec:       runningJob.ElapsedSec,
		})
	}

	err := ui.DashboardPage(ui.DashboardPageData{
		Campaigns:   payload.Campaigns,
		RunningJobs: jobs,
		Aggregates: ui.DashboardAggregates{
			Running:   payload.Aggregates.Running,
			Pending:   payload.Aggregates.Pending,
			Completed: payload.Aggregates.Completed,
			CPS:       payload.Aggregates.CPS,
		},
		HostFacts: ui.HostFacts{
			Version:                payload.HostFacts.Version,
			Commit:                 payload.HostFacts.Commit,
			BuildDate:              payload.HostFacts.BuildDate,
			GOOS:                   payload.HostFacts.GOOS,
			GOARCH:                 payload.HostFacts.GOARCH,
			GOMAXPROCS:             payload.HostFacts.GOMAXPROCS,
			GoVersion:              payload.HostFacts.GoVersion,
			SIMD:                   payload.HostFacts.SIMD,
			ActiveSSDKernel:        payload.HostFacts.ActiveSSDKernel,
			ActiveSADKernel:        payload.HostFacts.ActiveSADKernel,
			CompositingBackend:     payload.HostFacts.CompositingBackend,
			FastCompositingBackend: payload.HostFacts.FastCompositingBackend,
			GPU: ui.GPUFacts{
				State: payload.HostFacts.GPU.State,
				Error: payload.HostFacts.GPU.Error,
			},
		},
	}).Render(r.Context(), w)
	if err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// handleJobsPage handles GET /jobs.
func (s *Server) handleJobsPage(w http.ResponseWriter, r *http.Request) {
	// Only handle exact jobs path
	if r.URL.Path != "/jobs" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Bound both the fallback HTML and its hydration seed. Later pages are
	// fetched by the island as its sentinel approaches the viewport.
	page, err := paginateJobSummaries(s.jobManager.ListJobSummaries(), "", defaultJobListLimit)
	if err != nil {
		http.Error(w, "Failed to paginate jobs", http.StatusInternalServerError)
		return
	}

	// Convert to UI job list items
	jobItems := make([]ui.JobListItem, len(page.Jobs))
	for i, job := range page.Jobs {
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
	seed := ui.JobListPage{Jobs: jobItems, NextCursor: page.NextCursor, Total: page.Total}
	if err := ui.JobList(seed).Render(r.Context(), w); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// handleSettingsPage handles GET /settings.
func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/settings" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	err := ui.SettingsPage().Render(r.Context(), w)
	if err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// handleJobDetail handles GET /jobs/:id.
func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request) {
	// Extract job ID from path
	jobID := r.URL.Path[len("/jobs/"):]

	// Get job from manager
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		err := ui.JobNotFound(jobID).Render(r.Context(), w)
		if err != nil {
			http.Error(w, "Failed to render page", http.StatusInternalServerError)
		}

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	elapsed := jobElapsed(job)
	cps := circlesPerSecond(job, elapsed)

	refWidth, refHeight, refSize, _ := s.referenceImageFactsFor(job.Config.RefPath)

	psnr, psnrInfinite := cloneFloat(job.PSNR), job.PSNRInfinite
	if len(job.BestParams) > 0 {
		psnr, psnrInfinite = serializablePSNR(job.BestCost)
	}

	candidatePSNR, candidatePSNRInfinite := serializableCandidatePSNR(job.CandidateCost)

	metricHistory := make([]ui.MetricSample, len(job.MetricHistory))
	for i, sample := range job.MetricHistory {
		metricHistory[i] = ui.MetricSample{
			Iteration: sample.Iteration, Evaluations: sample.Evaluations, Cost: sample.Cost, CPS: sample.CPS,
			PSNR: cloneFloat(sample.PSNR), PSNRInfinite: sample.PSNRInfinite, SSIM: cloneFloat(sample.SSIM),
			Timestamp: sample.Timestamp,
		}
	}

	parameterCircles, err := decodeParameterCircles(job.BestParams)
	if err != nil {
		slog.Warn("Unable to display invalid job parameters", "job_id", job.ID, "error", err)
	}

	parameters := make([]ui.CircleParameter, len(parameterCircles))
	for i, circle := range parameterCircles {
		parameters[i] = ui.CircleParameter{
			Number: circle.Number, X: circle.X, Y: circle.Y, Radius: circle.Radius,
			Red: circle.Red, Green: circle.Green, Blue: circle.Blue, Opacity: circle.Opacity,
		}
	}

	maxIterations := plannedOptimizerIterations(job.Config)
	canPolish := s.store != nil &&
		job.State == StateCompleted &&
		job.Config.ResolvedOptimizer() == app.OptimizerMayfly &&
		job.Config.Mode == app.ModeBatch &&
		len(job.BestParams) == job.Config.Circles*7

	// Convert to UI job detail
	jobDetail := ui.JobDetail{
		ID:                       job.ID,
		State:                    string(job.State),
		RefPath:                  job.Config.RefPath,
		Mode:                     string(job.Config.Mode),
		Optimizer:                string(job.Config.ResolvedOptimizer()),
		Variant:                  string(job.Config.Variant),
		InitialSigma:             job.Config.ResolvedCMAESInitialSigma(),
		CovarianceMode:           string(job.Config.ResolvedCMAESCovarianceMode()),
		ActiveCMA:                job.Config.ResolvedCMAESActive(),
		RestartStrategy:          string(job.Config.ResolvedCMAESRestartStrategy()),
		EvaluationWorkers:        job.EvaluationWidth,
		FastCompositing:          job.Config.FastCompositing,
		Circles:                  job.Config.Circles,
		Iterations:               job.Iterations,
		Evaluations:              job.Evaluations,
		MaxIters:                 maxIterations,
		ItersPerEpoch:            job.Config.Iters,
		OptimizerEpochs:          max(job.Config.OptimizerEpochs, 1),
		PopSize:                  job.Config.PopSize,
		PolishingEnabled:         job.Config.PolishingEnabled,
		PolishingOnly:            job.Config.PolishingOnly,
		PolishingStrategy:        string(job.Config.PolishingStrategy),
		CanPolish:                canPolish,
		PolishingActiveSetSize:   job.Config.PolishingActiveSetSize,
		PolishingMaxSweeps:       job.Config.PolishingMaxSweeps,
		PolishingEpochs:          job.Config.PolishingEpochs,
		PolishingIters:           job.Config.PolishingIters,
		PolishingPopSize:         job.Config.PolishingPopSize,
		PolishingStagnationIters: job.Config.PolishingStagnationIters,
		PolishingMinImprovement:  job.Config.PolishingMinImprovement,
		BestCost:                 job.BestCost,
		CandidateCost:            cloneFloat(job.CandidateCost),
		CandidatePSNR:            candidatePSNR,
		CandidatePSNRInfinite:    candidatePSNRInfinite,
		BestRevision:             job.BestRevision,
		InitialCost:              job.InitialCost,
		StartTime:                job.StartTime,
		EndTime:                  job.EndTime,
		ElapsedSec:               elapsed.Seconds(),
		CPS:                      cps,
		Termination:              job.Termination,
		Error:                    job.Error,
		RefWidth:                 refWidth,
		RefHeight:                refHeight,
		RefSize:                  refSize,
		PSNR:                     psnr,
		PSNRInfinite:             psnrInfinite,
		SSIM:                     cloneFloat(job.SSIM),
		SSIMEnabled:              job.Config.EnableSSIM,
		MetricHistory:            metricHistory,
		Parameters:               parameters,
	}

	// Render the job detail page using templ
	if err := ui.JobDetailPage(jobDetail).Render(r.Context(), w); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

func plannedOptimizerIterations(config JobConfig) int {
	// Restarts multiply the work exactly as epochs do; a progress bar that
	// ignored them would sit at the wrong fraction for the whole run.
	perStage := config.Iters * max(config.OptimizerEpochs, 1) * max(config.OptimizerRestarts, 1)
	stages := 1

	switch config.Mode {
	case app.ModeSequential:
		stages = config.Circles
	case app.ModeBatch:
		// Only the planned batch stages count. A batch run's residual refills
		// are drawn from this same budget rather than added to it, so
		// including MaxExtraBatchStages here would divide by four times the
		// iterations a one-stage job can actually reach: the job would show a
		// quarter of its progress bar filled and then finish.
		batchSize := max(config.BatchSize, 1)
		stages = (config.Circles + batchSize - 1) / batchSize
	}

	total := stages * perStage
	if config.PolishingEnabled {
		total += config.PolishingMaxSweeps * config.PolishingEpochs * config.PolishingIters
	}

	return total
}

func referenceImageMetadata(path string) (width, height int, size int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, 0, 0, err
	}

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, 0, err
	}

	return config.Width, config.Height, info.Size(), nil
}

// referenceImageFactsFor answers the same question referenceImageMetadata does,
// but memoized per path. The job status endpoint is polled - by the detail
// island, and by however many clients are watching - and each call otherwise
// opened the reference image and decoded its header to learn dimensions that do
// not change while a job runs.
//
// The memo is validated rather than trusted: one Stat per call still happens,
// and a differing size or modification time re-reads the header. That keeps a
// reference image replaced under a running server from being served stale,
// while removing the decode from the hot path. Errors are not cached, so a file
// that appears later is picked up on the next request.
func (s *Server) referenceImageFactsFor(path string) (int, int, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("stat reference image %q: %w", path, err)
	}

	s.refImageMu.Lock()
	cached, ok := s.refImageCache[path]
	s.refImageMu.Unlock()

	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.width, cached.height, cached.size, nil
	}

	width, height, size, err := referenceImageMetadata(path)
	if err != nil {
		return 0, 0, 0, err
	}

	s.refImageMu.Lock()
	if s.refImageCache == nil {
		s.refImageCache = make(map[string]referenceImageFacts)
	}

	s.refImageCache[path] = referenceImageFacts{width: width, height: height, size: size, modTime: info.ModTime()}
	s.refImageMu.Unlock()

	return width, height, size, nil
}

// handleCreatePage handles GET /create and POST /create.
func (s *Server) handleCreatePage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleCreatePageGet(w, r)
	case http.MethodPost:
		s.handleCreatePagePost(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Bounds the creation form enforces that internal/app does not name as a
// constant of its own. Each one is the number the form handler below checks, so
// the browser and the handler cannot disagree; app.Validate still decides the
// request, and each of these is at least as strict as what it accepts.
const (
	// maxConvergencePatience mirrors the bound app.Validate spells out inline
	// for convergencePatience.
	maxConvergencePatience = 100
	// The convergence threshold is a relative improvement ratio. app.Validate
	// accepts anything finite in [0,1]; the form has always offered the useful
	// part of that range, and 0.0001 doubles as the step so the control's
	// arrows land on values it accepts.
	minConvergenceThreshold = 0.0001
	maxConvergenceThreshold = 0.1
	// app.Validate asks only that polishingMinImprovement be finite and
	// positive, which no HTML min attribute can express; this is the smallest
	// value the control offers.
	minPolishingMinImprovement = 1e-9
)

// createJobLimits projects the server's bounds onto the creation page. The
// fallback form writes its min/max attributes from this value and the page
// seeds CreateJobIsland with the same one, so neither the markup nor the
// TypeScript carries a limit of its own.
func createJobLimits() ui.CreateJobLimits {
	return ui.CreateJobLimits{
		MaxCircles:                 app.MaxCircles,
		MaxIterations:              app.MaxIterations,
		MinPopulation:              app.MinPopulation,
		MaxPopulation:              app.MaxPopulation,
		MaxOptimizerEpochs:         app.MaxOptimizerEpochs,
		MaxBatchSize:               app.MaxBatchSize,
		MaxPolishingSweeps:         app.MaxPolishingSweeps,
		MaxConvergencePatience:     maxConvergencePatience,
		MinConvergenceThreshold:    minConvergenceThreshold,
		MaxConvergenceThreshold:    maxConvergenceThreshold,
		MinPolishingMinImprovement: minPolishingMinImprovement,
		DefaultInitialSigma:        app.DefaultCMAESInitialSigma,
	}
}

// handleCreatePageGet renders the job creation form.
func (s *Server) handleCreatePageGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// The form posts to a bare "/create", so the project has to survive the
	// round trip in a hidden field rather than in the query string.
	requestedProject := strings.TrimSpace(r.URL.Query().Get("project"))

	// Render the create job page with no error message
	err := ui.CreateJobPage(ui.CreateJobPageData{
		Project: requestedProject,
		Limits:  createJobLimits(),
	}).Render(r.Context(), w)
	if err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// renderCreateJobError re-renders the job creation form with a validation
// message. Every rejection in handleCreatePagePost ends the same way — same
// content type, same page, same early return — and each site dropped the render
// error, so a response that failed to write left no trace anywhere. Rendering
// through one helper gives that failure a single place to be logged.
//
// The error is logged rather than turned into an http.Error: the status line and
// the first bytes are already committed by the time Render can fail, so there is
// no second response to send. What is left to do is record it.
func renderCreateJobError(w http.ResponseWriter, r *http.Request, message, project string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	err := ui.CreateJobPage(ui.CreateJobPageData{
		ErrorMessage: message,
		Project:      project,
		Limits:       createJobLimits(),
	}).Render(r.Context(), w)
	if err != nil {
		slog.Error("Failed to render the job creation form", "error", err, "path", r.URL.Path)
	}
}

// handleCreatePagePost processes the job creation form submission.
func (s *Server) handleCreatePagePost(w http.ResponseWriter, r *http.Request) {
	// Parse form data
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)
	if err := r.ParseForm(); err != nil {
		renderCreateJobError(w, r, "Failed to parse form data", "")
		return
	}

	// Echoed back into the re-rendered form so a validation error does not
	// silently move the job to the default project. It is deliberately the raw
	// submitted value: resolveRequestedProject below validates it, and templ
	// escapes it on the way out.
	formProject := strings.TrimSpace(r.FormValue("project"))

	// Extract and validate form fields
	refPath := r.FormValue("refPath")
	canvasPath := r.FormValue("canvasPath")
	mode := r.FormValue("mode")
	optimizer := r.FormValue("optimizer")
	circlesStr := r.FormValue("circles")
	itersStr := r.FormValue("iters")
	popSizeStr := r.FormValue("popSize")
	optimizerEpochsStr := r.FormValue("optimizerEpochs")
	batchSizeStr := r.FormValue("batchSize")
	polishingEnabled := r.FormValue("polishingEnabled") == "on"
	polishingStrategy := app.PolishingStrategy(r.FormValue("polishingStrategy"))
	polishingActiveSetSizeStr := r.FormValue("polishingActiveSetSize")
	polishingMaxSweepsStr := r.FormValue("polishingMaxSweeps")
	polishingEpochsStr := r.FormValue("polishingEpochs")
	polishingItersStr := r.FormValue("polishingIters")
	polishingPopSizeStr := r.FormValue("polishingPopSize")
	polishingStagnationItersStr := r.FormValue("polishingStagnationIters")
	polishingMinImprovementStr := r.FormValue("polishingMinImprovement")
	seedStr := r.FormValue("seed")
	convergenceEnabledStr := r.FormValue("convergenceEnabled")
	convergencePatienceStr := r.FormValue("convergencePatience")
	convergenceThresholdStr := r.FormValue("convergenceThreshold")
	enableSSIM := r.FormValue("enableSSIM") == "on"

	// Validate required fields
	if refPath == "" {
		renderCreateJobError(w, r, "Reference image path is required", formProject)
		return
	}

	if mode == "" {
		renderCreateJobError(w, r, "Mode is required", formProject)
		return
	}

	// Parse integer fields
	circles, err := strconv.Atoi(circlesStr)
	if err != nil || circles < 1 || circles > app.MaxCircles {
		renderCreateJobError(w, r, fmt.Sprintf("Circles must be between 1 and %d", app.MaxCircles), formProject)
		return
	}

	iters, err := strconv.Atoi(itersStr)
	if err != nil || iters < 1 || iters > app.MaxIterations {
		renderCreateJobError(w, r, fmt.Sprintf("Iterations must be between 1 and %d", app.MaxIterations), formProject)
		return
	}

	popSize, err := strconv.Atoi(popSizeStr)
	if err != nil || popSize < app.MinPopulation || popSize > app.MaxPopulation {
		renderCreateJobError(w, r, fmt.Sprintf(
			"Population size must be between %d and %d", app.MinPopulation, app.MaxPopulation,
		), formProject)

		return
	}

	optimizerEpochs := 1
	if optimizerEpochsStr != "" {
		optimizerEpochs, err = strconv.Atoi(optimizerEpochsStr)
		if err != nil || optimizerEpochs < 1 || optimizerEpochs > app.MaxOptimizerEpochs {
			renderCreateJobError(w, r, fmt.Sprintf("Optimizer epochs must be between 1 and %d", app.MaxOptimizerEpochs), formProject)
			return
		}
	}

	batchSize := 0
	if batchSizeStr != "" {
		batchSize, err = strconv.Atoi(batchSizeStr)
		if err != nil || batchSize < 0 {
			renderCreateJobError(w, r, "Batch size must be zero or a positive whole number", formProject)
			return
		}
	}

	polishingActiveSetSize, err := formIntOrDefault(polishingActiveSetSizeStr, 0, "Polishing active set size")
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	polishingMaxSweeps, err := formIntOrDefault(polishingMaxSweepsStr, 0, "Polishing max sweeps")
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	polishingEpochs, err := formIntOrDefault(polishingEpochsStr, 0, "Polishing epochs")
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	polishingIters, err := formIntOrDefault(polishingItersStr, 0, "Polishing iterations")
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	polishingPopSize, err := formIntOrDefault(polishingPopSizeStr, 0, "Polishing population size")
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	polishingStagnationIters, err := formIntOrDefault(polishingStagnationItersStr, 0, "Polishing stagnation iterations")
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	polishingMinImprovement, err := formFloatOrDefault(polishingMinImprovementStr, 0, "Polishing minimum improvement")
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	seed, err := strconv.ParseInt(seedStr, 10, 64)
	if err != nil {
		renderCreateJobError(w, r, "Invalid seed value", formProject)
		return
	}
	// Parse convergence fields (with defaults)
	convergenceEnabled := convergenceEnabledStr == "on" // checkbox is "on" when checked, empty otherwise

	convergencePatience := 3 // default
	if convergencePatienceStr != "" {
		convergencePatience, err = strconv.Atoi(convergencePatienceStr)
		if err != nil || convergencePatience < 1 || convergencePatience > maxConvergencePatience {
			renderCreateJobError(w, r, fmt.Sprintf(
				"Convergence patience must be between 1 and %d", maxConvergencePatience,
			), formProject)

			return
		}
	}

	convergenceThreshold := 0.001 // default
	if convergenceThresholdStr != "" {
		convergenceThreshold, err = strconv.ParseFloat(convergenceThresholdStr, 64)
		if err != nil || convergenceThreshold < minConvergenceThreshold || convergenceThreshold > maxConvergenceThreshold {
			renderCreateJobError(w, r, fmt.Sprintf(
				"Convergence threshold must be between %g and %g", minConvergenceThreshold, maxConvergenceThreshold,
			), formProject)

			return
		}
	}

	// Optimizer-level early stopping. These fields only need to parse here;
	// their bounds belong to app.Normalize, so they are not duplicated.
	stopTargetCost, err := optionalFormFloat(r, "stopTargetCost")
	if err != nil {
		renderCreateJobError(w, r, err.Error(), formProject)
		return
	}

	stopMinImprovement, err := optionalFormFloat(r, "stopMinImprovement")
	if err != nil {
		renderCreateJobError(w, r, err.Error(), formProject)
		return
	}

	stopStagnationIters, err := optionalFormInt(r, "stopStagnationIters")
	if err != nil {
		renderCreateJobError(w, r, err.Error(), formProject)
		return
	}

	stopMinIters, err := optionalFormInt(r, "stopMinIters")
	if err != nil {
		renderCreateJobError(w, r, err.Error(), formProject)
		return
	}

	cmaes, err := parseCMAESForm(r, optimizer)
	if err != nil {
		renderCreateError(w, r, formProject, err)
		return
	}

	// Create job configuration through the same normalization path as the API.
	requestedConfig := JobConfig{
		RefPath:                  refPath,
		CanvasPath:               canvasPath,
		Mode:                     app.Mode(mode),
		Optimizer:                app.Optimizer(optimizer),
		InitialSigma:             cmaes.initialSigma,
		ActiveCMA:                cmaes.activeCMA,
		CovarianceMode:           cmaes.covarianceMode,
		RestartStrategy:          cmaes.restartStrategy,
		Circles:                  circles,
		Iters:                    iters,
		PopSize:                  popSize,
		OptimizerEpochs:          optimizerEpochs,
		BatchSize:                batchSize,
		PolishingEnabled:         polishingEnabled,
		PolishingStrategy:        polishingStrategy,
		PolishingActiveSetSize:   polishingActiveSetSize,
		PolishingMaxSweeps:       polishingMaxSweeps,
		PolishingEpochs:          polishingEpochs,
		PolishingIters:           polishingIters,
		PolishingPopSize:         polishingPopSize,
		PolishingStagnationIters: polishingStagnationIters,
		PolishingMinImprovement:  polishingMinImprovement,
		Seed:                     seed,
		EnableSSIM:               enableSSIM,
		ConvergenceEnabled:       convergenceEnabled,
		DisableConvergence:       !convergenceEnabled,
		ConvergencePatience:      convergencePatience,
		ConvergenceThreshold:     convergenceThreshold,
		StopTargetCost:           stopTargetCost,
		StopMinImprovement:       stopMinImprovement,
		StopStagnationIters:      stopStagnationIters,
		StopMinIters:             stopMinIters,
	}
	s.applyDefaultBackend(&requestedConfig)

	config, err := app.Normalize(requestedConfig)
	if err != nil {
		renderCreateJobError(w, r, err.Error(), formProject)
		return
	}

	if s.inputErr != nil {
		renderCreateJobError(w, r, "Server input roots are unavailable", formProject)
		return
	}

	config.RefPath, err = s.input.resolveImage(config.RefPath)
	if err != nil {
		renderCreateJobError(w, r, err.Error(), formProject)
		return
	}

	if config.CanvasPath != "" {
		config.CanvasPath, err = s.input.resolveImage(config.CanvasPath)
		if err != nil {
			renderCreateJobError(w, r, err.Error(), formProject)
			return
		}
	}

	// The form carries the project as a plain field; an absent one is the
	// default project, which is the legacy jobs directory.
	requested, err := s.resolveRequestedProject(r.FormValue("project"), r)

	project := requested
	if err == nil {
		project, err = s.ensureProject(requested)
	}

	if err != nil {
		// A store fault is logged server-side and shown generically; only the
		// charset-constrained validation message is echoed to the browser.
		renderCreateJobError(w, r, projectErrorMessage(requested, err), formProject)
		return
	}

	// Create the job
	job := s.jobManager.CreateJob(project, config)

	// The server owns every job context, including jobs created through the UI.
	if err := s.enqueueJob(job.ID); err != nil {
		_ = s.jobManager.FailJob(job.ID, "server job queue is full")

		renderCreateJobError(w, r, "Server job queue is full", formProject)

		return
	}

	// Redirect to job detail page
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// renderCreateError is renderCreateJobError for a rejection that already has an
// error value, which is most of the parsed-field checks.
func renderCreateError(w http.ResponseWriter, r *http.Request, project string, err error) {
	renderCreateJobError(w, r, err.Error(), project)
}

// cmaesFormValues carries the CMA-ES section of the creation form into the
// JobConfig literal. Every field stays at its zero value for a job that does
// not run CMA-ES, which is what app.Normalize requires: InitialSigma and
// ActiveCMA are pointers precisely so "omitted" is distinguishable from an
// explicit value, and the two string types are empty when omitted.
type cmaesFormValues struct {
	initialSigma    *float64
	activeCMA       *bool
	covarianceMode  app.CMAESCovarianceMode
	restartStrategy app.CMAESRestartStrategy
}

// parseCMAESForm reads the four CMA-ES-only settings, but only for a CMA-ES
// job. The creation form carries no JavaScript, so it always renders and always
// submits those inputs whatever engine is selected; app.Normalize refuses a
// CMA-ES-only field on a MayFly or Dragonfly job rather than ignoring it, so
// they have to be dropped here instead of passed along.
//
// The form deliberately has no optimizerRestarts input, so OptimizerRestarts
// keeps its default of 1 and the "ipop or bipop needs optimizerRestarts == 1"
// rejection in app cannot be triggered from this page. Adding such an input
// later would make that rejection reachable and it would need its own message.
func parseCMAESForm(r *http.Request, optimizer string) (cmaesFormValues, error) {
	if app.Optimizer(optimizer) != app.OptimizerCMAES {
		return cmaesFormValues{}, nil
	}

	// An emptied field means "use the CMA-ES default" rather than zero, which
	// app.Normalize would refuse as a non-positive step size. Bounds stay in
	// app.Normalize so the form and the JSON API cannot drift apart.
	sigma, err := formFloatOrDefault(r.FormValue("initialSigma"), app.DefaultCMAESInitialSigma, "initialSigma")
	if err != nil {
		return cmaesFormValues{}, err
	}

	// An unchecked checkbox is absent from the submission, which is the only
	// way the form can express a disabled active adaptation.
	active := r.FormValue("activeCMA") == "on"

	return cmaesFormValues{
		initialSigma:    &sigma,
		activeCMA:       &active,
		covarianceMode:  app.CMAESCovarianceMode(r.FormValue("covarianceMode")),
		restartStrategy: app.CMAESRestartStrategy(r.FormValue("restartStrategy")),
	}, nil
}

func formIntOrDefault(raw string, defaultValue int, label string) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", label)
	}

	return value, nil
}

func formFloatOrDefault(raw string, defaultValue float64, label string) (float64, error) {
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", label)
	}

	return value, nil
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
