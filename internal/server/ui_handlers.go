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
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

// handleDashboardPage handles GET /
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
			ID:              runningJob.ID,
			Project:         string(runningJob.Project),
			State:           runningJob.State,
			Iterations:      runningJob.Iterations,
			MaxIters:        runningJob.MaxIters,
			BestCost:        runningJob.BestCost,
			InitialCost:     runningJob.InitialCost,
			CPS:             runningJob.CPS,
			EvaluationWidth: runningJob.EvaluationWidth,
			ElapsedSec:      runningJob.ElapsedSec,
		})
	}
	if err := ui.DashboardPage(ui.DashboardPageData{
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
	}).Render(r.Context(), w); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

// handleJobsPage handles GET /jobs
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

// handleSettingsPage handles GET /settings
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
	if err := ui.SettingsPage().Render(r.Context(), w); err != nil {
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

	elapsed := jobElapsed(job)
	cps := circlesPerSecond(job, elapsed)

	refWidth, refHeight, refSize, _ := referenceImageMetadata(job.Config.RefPath)
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

	// Convert to UI job detail
	jobDetail := ui.JobDetail{
		ID:                       job.ID,
		State:                    string(job.State),
		RefPath:                  job.Config.RefPath,
		Mode:                     string(job.Config.Mode),
		Variant:                  string(job.Config.Variant),
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
		CanPolish:                s.store != nil && job.State == StateCompleted && job.Config.Mode == app.ModeBatch && len(job.BestParams) == job.Config.Circles*7,
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
	perStage := config.Iters * max(config.OptimizerEpochs, 1)
	stages := 1
	switch config.Mode {
	case app.ModeSequential:
		stages = config.Circles
	case app.ModeBatch:
		batchSize := max(config.BatchSize, 1)
		stages = (config.Circles+batchSize-1)/batchSize + renderer.MaxExtraBatchStages
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

	// The form posts to a bare "/create", so the project has to survive the
	// round trip in a hidden field rather than in the query string.
	requestedProject := strings.TrimSpace(r.URL.Query().Get("project"))

	// Render the create job page with no error message
	if err := ui.CreateJobPage("", requestedProject).Render(r.Context(), w); err != nil {
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
		ui.CreateJobPage("Failed to parse form data", "").Render(r.Context(), w)
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Reference image path is required", formProject).Render(r.Context(), w)
		return
	}

	if mode == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Mode is required", formProject).Render(r.Context(), w)
		return
	}

	// Parse integer fields
	circles, err := strconv.Atoi(circlesStr)
	if err != nil || circles < 1 || circles > app.MaxCircles {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(fmt.Sprintf("Circles must be between 1 and %d", app.MaxCircles), formProject).Render(r.Context(), w)
		return
	}

	iters, err := strconv.Atoi(itersStr)
	if err != nil || iters < 1 || iters > 10000 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Iterations must be between 1 and 10000", formProject).Render(r.Context(), w)
		return
	}

	popSize, err := strconv.Atoi(popSizeStr)
	if err != nil || popSize < 2 || popSize > 200 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Population size must be between 2 and 200", formProject).Render(r.Context(), w)
		return
	}

	optimizerEpochs := 1
	if optimizerEpochsStr != "" {
		optimizerEpochs, err = strconv.Atoi(optimizerEpochsStr)
		if err != nil || optimizerEpochs < 1 || optimizerEpochs > app.MaxOptimizerEpochs {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage(fmt.Sprintf("Optimizer epochs must be between 1 and %d", app.MaxOptimizerEpochs), formProject).Render(r.Context(), w)
			return
		}
	}

	batchSize := 0
	if batchSizeStr != "" {
		batchSize, err = strconv.Atoi(batchSizeStr)
		if err != nil || batchSize < 0 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage("Batch size must be zero or a positive whole number", formProject).Render(r.Context(), w)
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Invalid seed value", formProject).Render(r.Context(), w)
		return
	}
	// Parse convergence fields (with defaults)
	convergenceEnabled := convergenceEnabledStr == "on" // checkbox is "on" when checked, empty otherwise
	convergencePatience := 3                            // default
	if convergencePatienceStr != "" {
		convergencePatience, err = strconv.Atoi(convergencePatienceStr)
		if err != nil || convergencePatience < 1 || convergencePatience > 100 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage("Convergence patience must be between 1 and 100", formProject).Render(r.Context(), w)
			return
		}
	}

	convergenceThreshold := 0.001 // default
	if convergenceThresholdStr != "" {
		convergenceThreshold, err = strconv.ParseFloat(convergenceThresholdStr, 64)
		if err != nil || convergenceThreshold < 0.0001 || convergenceThreshold > 0.1 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage("Convergence threshold must be between 0.0001 and 0.1", formProject).Render(r.Context(), w)
			return
		}
	}

	// Optimizer-level early stopping. These fields only need to parse here;
	// their bounds belong to app.Normalize, so they are not duplicated.
	stopTargetCost, err := optionalFormFloat(r, "stopTargetCost")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error(), formProject).Render(r.Context(), w)
		return
	}
	stopMinImprovement, err := optionalFormFloat(r, "stopMinImprovement")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error(), formProject).Render(r.Context(), w)
		return
	}
	stopStagnationIters, err := optionalFormInt(r, "stopStagnationIters")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error(), formProject).Render(r.Context(), w)
		return
	}
	stopMinIters, err := optionalFormInt(r, "stopMinIters")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error(), formProject).Render(r.Context(), w)
		return
	}

	// Create job configuration through the same normalization path as the API.
	requestedConfig := JobConfig{
		RefPath:                  refPath,
		CanvasPath:               canvasPath,
		Mode:                     app.Mode(mode),
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error(), formProject).Render(r.Context(), w)
		return
	}
	if s.inputErr != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Server input roots are unavailable", formProject).Render(r.Context(), w)
		return
	}
	config.RefPath, err = s.input.resolveImage(config.RefPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(err.Error(), formProject).Render(r.Context(), w)
		return
	}
	if config.CanvasPath != "" {
		config.CanvasPath, err = s.input.resolveImage(config.CanvasPath)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			ui.CreateJobPage(err.Error(), formProject).Render(r.Context(), w)
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage(projectErrorMessage(requested, err), formProject).Render(r.Context(), w)
		return
	}

	// Create the job
	job := s.jobManager.CreateJob(project, config)

	// The server owns every job context, including jobs created through the UI.
	if err := s.enqueueJob(job.ID); err != nil {
		_ = s.jobManager.FailJob(job.ID, "server job queue is full")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ui.CreateJobPage("Server job queue is full", formProject).Render(r.Context(), w)
		return
	}

	// Redirect to job detail page
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

func renderCreateError(w http.ResponseWriter, r *http.Request, project string, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ui.CreateJobPage(err.Error(), project).Render(r.Context(), w)
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
