package server

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// buildEarlyStop maps the optimizer-level stopping fields onto the adapter's
// option. A configuration that sets none of them yields a zero Stop, which
// leaves the optimizer unchanged.
func buildEarlyStop(config store.JobConfig) opt.Stop {
	return opt.Stop{
		TargetCost:      config.StopTargetCost,
		MinImprovement:  config.StopMinImprovement,
		StagnationIters: config.StopStagnationIters,
		MinIters:        config.StopMinIters,
	}
}

func buildConvergenceConfig(config store.JobConfig) renderer.ConvergenceConfig {
	return renderer.ConvergenceConfig{
		Enabled:   config.ConvergenceEnabled,
		Patience:  config.ConvergencePatience,
		Threshold: config.ConvergenceThreshold,
	}
}

// progressOptimizer injects one observer and optional saved best into each
// lifecycle-aware stage while maintaining cumulative counters across stages.
type progressOptimizer struct {
	base          opt.Optimizer
	observer      opt.Observer
	epochObserver opt.EpochObserver
	initial       *opt.Candidate
	resumeCount   int

	mu          sync.Mutex
	iterations  int
	evaluations int
}

func (o *progressOptimizer) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	return o.base.Run(eval, lower, upper, dim)
}

// ParallelEvaluationWorkers forwards the wrapped optimizer's evaluation width so
// callers that refuse a concurrent objective still see it through this wrapper.
func (o *progressOptimizer) ParallelEvaluationWorkers() int {
	return opt.ParallelEvaluationWidth(o.base)
}

func (o *progressOptimizer) RunContext(ctx context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	lifecycle, ok := o.base.(opt.LifecycleOptimizer)
	if !ok {
		return opt.Result{}, fmt.Errorf("optimizer does not support lifecycle execution")
	}

	o.mu.Lock()
	baseIterations, baseEvaluations := o.iterations, o.evaluations
	initial := options.Initial
	if o.initial != nil && len(o.initial.Params) == problem.Dim {
		initial = o.initial
		o.initial = nil
	}
	o.mu.Unlock()
	resumeCount := options.ResumeCount
	if o.resumeCount > 0 {
		resumeCount = o.resumeCount
	}

	result, err := lifecycle.RunContext(ctx, problem, opt.RunOptions{
		Initial:        initial,
		ProgressMapper: options.ProgressMapper,
		ResumeCount:    resumeCount,
		Observer: func(progress opt.Progress) {
			progress.Iterations += baseIterations
			progress.Evaluations += baseEvaluations
			if options.Observer != nil {
				options.Observer(progress)
			}
			if o.observer != nil {
				o.observer(progress)
			}
		},
		EpochObserver: func(boundary opt.EpochBoundary) error {
			boundary.Progress.Iterations += baseIterations
			boundary.Progress.Evaluations += baseEvaluations
			if options.EpochObserver != nil {
				if err := options.EpochObserver(boundary); err != nil {
					return err
				}
			}
			if o.epochObserver != nil {
				return o.epochObserver(boundary)
			}
			return nil
		},
	})

	o.mu.Lock()
	o.iterations = baseIterations + result.Iterations
	o.evaluations = baseEvaluations + result.Evaluations
	o.mu.Unlock()
	return result, err
}

// runJob executes one server-owned job. Progress, SSE, trace, and checkpoint
// writes all consume the same immutable optimizer callback snapshot.
func runJob(ctx context.Context, jm *JobManager, checkpointStore store.Store, jobID string) error {
	job, exists := jm.GetJob(jobID)
	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}
	if err := jm.StartJob(jobID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		markJobCancelled(jm, jobID)
		return err
	}

	ref, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		markJobFailed(jm, jobID, err)
		return err
	}
	if err := app.ValidateImageDimensions(ref.Bounds().Dx(), ref.Bounds().Dy()); err != nil {
		markJobFailed(jm, jobID, err)
		return err
	}

	rend, cleanup, err := rendererForJob(job.Config, ref, job.Config.Circles)
	if err != nil {
		markJobFailed(jm, jobID, err)
		return err
	}
	defer cleanup()

	seed := job.Config.EffectiveSeed
	if seed == 0 {
		seed = job.Config.Seed
	}
	optimizer, err := opt.NewMayflyVariant(string(job.Config.Variant), job.Config.Iters, job.Config.PopSize, seed,
		opt.WithLogger(slog.Default()), opt.WithEarlyStop(buildEarlyStop(job.Config)),
		parallelEvaluationOption(job.Config, rend))
	if err != nil {
		markJobFailed(jm, jobID, err)
		return err
	}
	optimizer = opt.WithEpochs(optimizer, job.Config.OptimizerEpochs)
	start := time.Now()
	baseIterations, baseEvaluations := job.Iterations, job.Evaluations
	initialCost := job.InitialCost
	metricCost := job.BestCost
	metricParams := append([]float64(nil), job.BestParams...)
	if len(job.BestParams) == 0 {
		metricParams = make([]float64, job.Config.Circles*7)
		initialCost = rend.Cost(metricParams)
		metricCost = initialCost
		_ = jm.UpdateJob(jobID, func(live *Job) { live.InitialCost = initialCost })
	}
	var initialSSIM *float64
	if job.Config.EnableSSIM {
		metricRenderer := rend
		metricCleanup := func() {}
		if len(metricParams) != rend.Dim() {
			metricRenderer, metricCleanup, err = rendererForJob(job.Config, ref, len(metricParams)/7)
			if err != nil {
				markJobFailed(jm, jobID, err)
				return err
			}
		}
		initialSSIM = calculateSSIM(metricRenderer.Render(metricParams), ref, jobID)
		metricCleanup()
	}
	initialSample := qualitySample(baseIterations, metricCost, initialSSIM, start)
	_ = jm.RecordMetrics(jobID, initialSample)

	var traceWriter *store.TraceWriter
	if job.Config.EnableTrace {
		if artifacts, ok := checkpointStore.(store.ArtifactStore); ok {
			traceWriter, err = artifacts.NewTraceWriter(jobID, false)
			if err != nil {
				slog.Warn("Failed to open trace", "job_id", jobID, "error", err)
				traceWriter = nil
			}
		}
	}
	if traceWriter != nil {
		defer func() {
			if closeErr := traceWriter.Close(); closeErr != nil {
				slog.Warn("Failed to close trace", "job_id", jobID, "error", closeErr)
			}
		}()
		_ = traceWriter.Write(traceEntry(initialSample))
	}

	nextBroadcast := start
	lastSSIMAt := start
	lastSSIMCost := metricCost
	if job.Config.EnableSSIM && initialSSIM == nil {
		lastSSIMCost = math.Inf(1)
	}
	nextCheckpoint := time.Time{}
	if job.Config.CheckpointInterval > 0 {
		nextCheckpoint = start.Add(time.Duration(job.Config.CheckpointInterval) * time.Second)
	}
	observer := func(progress opt.Progress) {
		iterations := baseIterations + progress.Iterations
		evaluations := baseEvaluations + progress.Evaluations
		if err := jm.UpdateProgress(jobID, iterations, evaluations, progress.BestParams, progress.BestCost); err != nil {
			return
		}
		now := time.Now()
		shouldBroadcast := !now.Before(nextBroadcast)
		var sampledSSIM *float64
		if shouldBroadcast && job.Config.EnableSSIM && shouldSampleSSIM(now, lastSSIMAt, progress.BestCost, lastSSIMCost) {
			sampledSSIM = calculateSSIM(rend.Render(progress.BestParams), ref, jobID)
			lastSSIMAt = now
			if sampledSSIM != nil {
				lastSSIMCost = progress.BestCost
			}
		}
		sample := qualitySample(iterations, progress.BestCost, sampledSSIM, now)
		if traceWriter != nil {
			_ = traceWriter.Write(traceEntry(sample))
		}
		if !now.Before(nextCheckpoint) && checkpointStore != nil && !nextCheckpoint.IsZero() {
			if err := saveCheckpoint(jm, checkpointStore, rend, jobID); err != nil {
				slog.Warn("Failed to save periodic checkpoint", "job_id", jobID, "error", err)
			}
			nextCheckpoint = now.Add(time.Duration(job.Config.CheckpointInterval) * time.Second)
		}
		if shouldBroadcast {
			bestCost, bestRevision, candidateCost, ok := jm.bestSnapshot(jobID)
			if !ok {
				return
			}
			elapsed := now.Sub(start).Seconds()
			cps := 0.0
			if elapsed > 0 {
				cps = float64(evaluations*job.Config.Circles) / elapsed
			}
			_ = jm.RecordMetrics(jobID, sample)
			candidatePSNR, candidatePSNRInfinite := serializableCandidatePSNR(candidateCost)
			jm.broadcaster.Broadcast(ProgressEvent{
				JobID: jobID, State: StateRunning, Iterations: iterations, Evaluations: evaluations,
				BestCost: bestCost, BestRevision: bestRevision, PSNR: cloneFloat(sample.PSNR),
				CandidateCost: candidateCost, CandidatePSNR: candidatePSNR, CandidatePSNRInfinite: candidatePSNRInfinite,
				PSNRInfinite: sample.PSNRInfinite, SSIM: cloneFloat(sample.SSIM), CPS: cps, Timestamp: now,
			})
			nextBroadcast = now.Add(500 * time.Millisecond)
		}
	}

	wrapped := &progressOptimizer{base: optimizer, observer: observer, resumeCount: job.Config.ResumeCount}
	wrapped.epochObserver = func(boundary opt.EpochBoundary) error {
		iterations := baseIterations + boundary.Progress.Iterations
		evaluations := baseEvaluations + boundary.Progress.Evaluations
		if err := jm.UpdateProgress(jobID, iterations, evaluations, boundary.Progress.BestParams, boundary.Progress.BestCost); err != nil {
			return err
		}
		if checkpointStore != nil {
			if err := saveCheckpoint(jm, checkpointStore, rend, jobID); err != nil {
				return fmt.Errorf("persist optimizer epoch %d: %w", boundary.Epoch, err)
			}
		}
		return nil
	}
	if len(job.BestParams) == job.Config.Circles*7 {
		initialParams := append([]float64(nil), job.BestParams...)
		parameterBounds := fit.NewBounds(job.Config.Circles, ref.Bounds().Dx(), ref.Bounds().Dy())
		parameterBounds.ClampVector(initialParams)
		wrapped.initial = &opt.Candidate{Params: initialParams, Cost: rend.Cost(initialParams)}
	}

	convergence := buildConvergenceConfig(job.Config)
	var result *renderer.OptimizationResult
	var circleData []store.CircleData
	var callback renderer.CircleCallback
	if job.Config.SaveSnapshots && checkpointStore != nil {
		callback = func(circleNum int, params []float64, cost float64, img image.Image) {
			if err := checkpointStore.SaveCircleSnapshot(jobID, circleNum, img); err != nil {
				slog.Warn("Failed to save circle snapshot", "job_id", jobID, "circle", circleNum, "error", err)
			}
			current, err := store.ParamVectorToCircles(params)
			if err != nil {
				return
			}
			for i := range current {
				if i < len(circleData) {
					current[i].CostAfter = circleData[i].CostAfter
					current[i].Timestamp = circleData[i].Timestamp
				}
			}
			if circleNum > 0 && circleNum <= len(current) {
				current[circleNum-1].CostAfter = cost
				current[circleNum-1].Timestamp = time.Now()
			}
			circleData = current
		}
	}

	switch job.Config.Mode {
	case app.ModeJoint:
		result, err = renderer.OptimizeJointContext(ctx, rend, wrapped, job.Config.Circles, convergence)
	case app.ModeSequential:
		if len(job.BestParams) > 0 {
			err = fmt.Errorf("sequential resume is not supported")
		} else {
			result, err = renderer.OptimizeSequentialContext(ctx, rend, wrapped, job.Config.Circles, convergence, callback)
		}
	case app.ModeBatch:
		if len(job.BestParams) > 0 {
			if job.Config.PolishingOnly && len(job.BestParams) == job.Config.Circles*7 {
				bestParams := append([]float64(nil), job.BestParams...)
				bestCost := rend.Cost(bestParams)
				result = &renderer.OptimizationResult{
					BestParams:       bestParams,
					BestCost:         bestCost,
					InitialCost:      initialCost,
					OptimizedCircles: job.Config.Circles,
					BestImage:        rend.Render(bestParams),
					Termination:      opt.TerminationCompleted,
				}
				result, err = polishBatchResult(
					ctx,
					jm,
					checkpointStore,
					rend,
					job,
					result,
					observer,
					baseIterations,
					baseEvaluations,
				)
			} else if len(job.BestParams) < job.Config.Circles*7 && len(job.BestParams)%7 == 0 {
				result, err = renderer.OptimizeBatchAppendContext(
					ctx,
					rend,
					wrapped,
					job.BestParams,
					job.Config.Circles,
					job.Config.BatchSize,
					convergence,
				)
				if err == nil && job.Config.PolishingEnabled && result.OptimizedCircles == job.Config.Circles {
					result, err = polishBatchResult(
						ctx,
						jm,
						checkpointStore,
						rend,
						job,
						result,
						observer,
						baseIterations,
						baseEvaluations,
					)
				}
			} else if job.Config.BatchSize >= job.Config.Circles && len(job.BestParams) == job.Config.Circles*7 {
				// A full-size batch is one optimizer stage over the complete
				// parameter vector, so progressOptimizer can safely replace that
				// stage's seed with the checkpoint candidate.
				result, err = renderer.OptimizeBatchContext(ctx, rend, wrapped, job.Config.Circles, job.Config.BatchSize, convergence)
				if err == nil && job.Config.PolishingEnabled && result.OptimizedCircles == job.Config.Circles {
					result, err = polishBatchResult(
						ctx,
						jm,
						checkpointStore,
						rend,
						job,
						result,
						observer,
						baseIterations,
						baseEvaluations,
					)
				}
			} else {
				err = fmt.Errorf("batch resume is not supported")
			}
		} else {
			result, err = renderer.OptimizeBatchContext(ctx, rend, wrapped, job.Config.Circles, job.Config.BatchSize, convergence)
			if err == nil && job.Config.PolishingEnabled && result.OptimizedCircles == job.Config.Circles {
				result, err = polishBatchResult(
					ctx,
					jm,
					checkpointStore,
					rend,
					job,
					result,
					observer,
					baseIterations,
					baseEvaluations,
				)
			}
		}
	default:
		err = fmt.Errorf("unknown mode")
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			markJobCancelled(jm, jobID)
			return err
		}
		markJobFailed(jm, jobID, err)
		return err
	}

	iterations := baseIterations + result.Iterations
	evaluations := baseEvaluations + result.Evaluations
	if err := jm.CompleteJob(jobID, iterations, evaluations, result.BestParams, result.BestCost, initialCost, string(result.Termination)); err != nil {
		return err
	}
	completedAt := time.Now()
	var finalSSIM *float64
	if job.Config.EnableSSIM && result.BestImage != nil {
		finalSSIM = calculateSSIM(result.BestImage, ref, jobID)
	}
	finalSample := qualitySample(iterations, result.BestCost, finalSSIM, completedAt)
	_ = jm.RecordMetrics(jobID, finalSample)
	if len(circleData) > 0 {
		if err := checkpointStore.SaveCircleData(jobID, circleData); err != nil {
			slog.Warn("Failed to save circle metadata", "job_id", jobID, "error", err)
		}
	}
	if traceWriter != nil {
		_ = traceWriter.Write(traceEntry(finalSample))
		_ = traceWriter.Flush()
	}
	var persistenceErr error
	if checkpointStore != nil {
		if err := saveCheckpoint(jm, checkpointStore, rend, jobID); err != nil {
			persistenceErr = fmt.Errorf("persist final result: %w", err)
			_ = jm.UpdateJob(jobID, func(live *Job) {
				live.Error = "failed to persist final result"
			})
			slog.Error("Failed to persist final job result", "job_id", jobID, "error", err)
		}
	}

	elapsed := time.Since(start).Seconds()
	cps := 0.0
	if elapsed > 0 {
		cps = float64(result.Evaluations*job.Config.Circles) / elapsed
	}
	bestCost, bestRevision, _, _ := jm.bestSnapshot(jobID)
	jm.broadcaster.Broadcast(ProgressEvent{
		JobID: jobID, State: StateCompleted, Iterations: iterations, Evaluations: evaluations,
		BestCost: bestCost, BestRevision: bestRevision, PSNR: cloneFloat(finalSample.PSNR),
		PSNRInfinite: finalSample.PSNRInfinite, SSIM: cloneFloat(finalSample.SSIM), CPS: cps, Timestamp: completedAt,
	})
	slog.Info("Job completed", "job_id", jobID, "iterations", iterations, "evaluations", evaluations, "best_cost", result.BestCost)
	return persistenceErr
}

func polishBatchResult(
	ctx context.Context,
	jm *JobManager,
	checkpointStore store.Store,
	rend renderer.Renderer,
	job *Job,
	batch *renderer.OptimizationResult,
	observer opt.Observer,
	baseIterations, baseEvaluations int,
) (*renderer.OptimizationResult, error) {
	seed := job.Config.EffectiveSeed
	if seed == 0 {
		seed = job.Config.Seed
	}
	polisher, err := opt.NewMayflyVariant(string(app.VariantStandard), job.Config.PolishingIters, job.Config.PopSize, seed,
		opt.WithLogger(slog.Default()),
		opt.WithEarlyStop(opt.Stop{
			MinImprovement:  job.Config.PolishingMinImprovement,
			StagnationIters: job.Config.PolishingStagnationIters,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create polishing optimizer: %w", err)
	}
	polisher = opt.WithEpochs(polisher, job.Config.PolishingEpochs)

	mainIterations := batch.Iterations
	mainEvaluations := batch.Evaluations
	if err := jm.UpdateProgress(
		job.ID,
		baseIterations+mainIterations,
		baseEvaluations+mainEvaluations,
		batch.BestParams,
		batch.BestCost,
	); err != nil {
		return nil, fmt.Errorf("record pre-polishing result: %w", err)
	}
	if checkpointStore != nil {
		if err := saveCheckpoint(jm, checkpointStore, rend, job.ID); err != nil {
			return nil, fmt.Errorf("persist pre-polishing result: %w", err)
		}
	}
	committedParams := append([]float64(nil), batch.BestParams...)
	committedCost := batch.BestCost

	persistPolishingBoundary := func(params []float64, cost float64, iterations, evaluations int, clearCandidate bool) error {
		iterations += baseIterations + mainIterations
		evaluations += baseEvaluations + mainEvaluations
		if err := jm.UpdateJob(job.ID, func(live *Job) {
			live.Iterations = iterations
			live.Evaluations = evaluations
			updateBestResult(live, params, cost)
			if clearCandidate {
				live.CandidateCost = nil
			}
		}); err != nil {
			return err
		}
		if checkpointStore != nil {
			return saveCheckpoint(jm, checkpointStore, rend, job.ID)
		}
		return nil
	}

	polish, err := renderer.PolishCircleBatchContext(ctx, rend, polisher, batch.BestParams, renderer.BatchPolishOptions{
		ActiveSetSize: job.Config.PolishingActiveSetSize,
		MaxSweeps:     job.Config.PolishingMaxSweeps,
		Strategy:      renderer.BatchPolishStrategy(job.Config.PolishingStrategy),
		Observer: func(progress opt.Progress) {
			progress.Iterations += mainIterations
			progress.Evaluations += mainEvaluations
			if err := jm.UpdateCandidateProgress(
				job.ID,
				baseIterations+progress.Iterations,
				baseEvaluations+progress.Evaluations,
				progress.BestCost,
			); err != nil {
				return
			}
			// Polishing is transactional. Publish counters continuously, but do
			// not use or checkpoint an in-flight candidate before the sweep's
			// full-image usefulness audit accepts it. The separately labelled
			// candidate cost is informational only.
			progress.BestParams = append([]float64(nil), committedParams...)
			progress.BestCost = committedCost
			observer(progress)
		},
		OnEpoch: func(boundary renderer.BatchPolishEpoch) error {
			return persistPolishingBoundary(
				committedParams,
				committedCost,
				boundary.Iterations,
				boundary.Evaluations,
				false,
			)
		},
		OnSweep: func(progress renderer.BatchPolishProgress) error {
			committedParams = append(committedParams[:0], progress.BestParams...)
			committedCost = progress.BestCost
			slog.Info("Batch polishing sweep complete",
				"job_id", job.ID,
				"sweep", progress.Sweep,
				"accepted", progress.Accepted,
				"region", progress.Region,
				"active_circles", progress.ActiveSet,
				"best_cost", progress.BestCost,
			)
			return persistPolishingBoundary(
				progress.BestParams,
				progress.BestCost,
				progress.Iterations,
				progress.Evaluations,
				true,
			)
		},
	})
	if err != nil {
		return nil, err
	}

	batch.BestParams = append(batch.BestParams[:0], polish.BestParams...)
	batch.BestCost = polish.BestCost
	batch.BestImage = polish.BestImage
	batch.Iterations += polish.Iterations
	batch.Evaluations += polish.Evaluations
	batch.Stages += polish.Sweeps
	slog.Info("Batch polishing complete",
		"job_id", job.ID,
		"sweeps", polish.Sweeps,
		"accepted_sweeps", polish.AcceptedSweeps,
		"best_cost", polish.BestCost,
	)
	return batch, nil
}

// parallelEvaluationOption derives the optimizer option from what the renderer
// can actually deliver and warns when an explicit request cannot be honored,
// which is the case for every backend without independent sessions.
func parallelEvaluationOption(config store.JobConfig, rend renderer.Renderer) opt.MayflyOption {
	option, enabled := renderer.ParallelEvaluationOption(rend, config.ParallelEvaluation)
	if config.ParallelEvaluation && !enabled {
		slog.Warn("Parallel evaluation requested but unavailable; evaluating serially",
			"backend", config.Backend, "evaluationWorkers", config.EvaluationWorkers)
	}
	return option
}

func rendererForJob(config store.JobConfig, ref *image.NRGBA, circleCount int) (renderer.Renderer, func(), error) {
	if config.CanvasPath != "" {
		if config.Backend != "" && config.Backend != app.BackendCPU {
			return nil, func() {}, fmt.Errorf("custom canvas requires CPU backend")
		}
		canvas, err := loadReferenceImage(config.CanvasPath)
		if err != nil {
			return nil, func() {}, fmt.Errorf("load canvas: %w", err)
		}
		if canvas.Bounds().Dx() != ref.Bounds().Dx() || canvas.Bounds().Dy() != ref.Bounds().Dy() {
			return nil, func() {}, fmt.Errorf("canvas dimensions do not match reference")
		}
		cpu := renderer.NewCPURendererWithCanvas(ref, canvas, circleCount)
		configureJobCPURenderer(cpu, config)
		return cpu, func() {}, nil
	}
	backend := config.Backend
	if backend == "" {
		backend = app.BackendCPU
	}
	if backend == app.BackendCPU {
		cpu := renderer.NewCPURenderer(ref, circleCount)
		configureJobCPURenderer(cpu, config)
		return cpu, func() {}, nil
	}
	return renderer.NewRendererForBackend(string(backend), ref, circleCount)
}

// parallelEvaluationWidth reports the concurrent evaluation width a job ran
// with, or zero when it did not opt in. It reads the configuration rather than
// a renderer because the detail view outlives the job's renderer.
func parallelEvaluationWidth(config store.JobConfig) int {
	if !config.ParallelEvaluation {
		return 0
	}
	return config.EvaluationWorkers
}

// configureJobCPURenderer applies a job's parallelism settings and records the
// effective evaluation width. The server had no equivalent of the CLI's startup
// line, so a job's actual concurrency was invisible in the server log.
func configureJobCPURenderer(cpu *renderer.CPURenderer, config store.JobConfig) {
	renderer.ConfigureCPUParallelism(cpu, config.Threads, config.EvaluationWorkers, config.ParallelEvaluation)
	slog.Info("Configured CPU renderer", "threads", cpu.Threads(),
		"evaluationWorkers", renderer.EvaluationWidth(cpu))
}

func markJobFailed(jm *JobManager, jobID string, err error) {
	_ = jm.FailJob(jobID, safeJobError(err))
	slog.Error("Job failed", "job_id", jobID, "error", err)
}

func safeJobError(err error) string {
	switch {
	case errors.Is(err, renderer.ErrStagedOptimizationUnsupported):
		return "selected backend does not support this optimization mode"
	case errors.Is(err, renderer.ErrInvalidOptimizationInput):
		return "optimizer produced an invalid result"
	default:
		return "job execution failed"
	}
}

func markJobCancelled(jm *JobManager, jobID string) {
	if err := jm.CancelJob(jobID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		slog.Warn("Failed to mark job cancelled", "job_id", jobID, "error", err)
	}
}

func saveCheckpoint(jm *JobManager, checkpointStore store.Store, rend renderer.Renderer, jobID string) error {
	job, exists := jm.GetJob(jobID)
	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}
	if len(job.BestParams) == 0 || math.IsInf(job.BestCost, 0) || math.IsNaN(job.BestCost) {
		return nil
	}
	checkpoint := store.NewCheckpoint(jobID, job.BestParams, job.BestCost, job.InitialCost, job.Iterations, job.Config)
	checkpoint.Evaluations = int64(job.Evaluations)
	if job.Termination != "" {
		checkpoint.Termination = job.Termination
	}
	var persistenceErrors []error
	if err := checkpointStore.SaveCheckpoint(jobID, checkpoint); err != nil {
		persistenceErrors = append(persistenceErrors, fmt.Errorf("save checkpoint: %w", err))
	}
	if err := saveCheckpointArtifacts(checkpointStore, rend, job.Config, jobID, job.BestParams); err != nil {
		persistenceErrors = append(persistenceErrors, err)
	}
	return errors.Join(persistenceErrors...)
}

func saveCheckpointArtifacts(checkpointStore store.Store, rend renderer.Renderer, config store.JobConfig, jobID string, bestParams []float64) error {
	artifacts, ok := checkpointStore.(store.ArtifactStore)
	if !ok {
		return nil
	}
	ref := rend.Reference()
	snapshotRenderer, cleanup, err := rendererForJob(config, ref, len(bestParams)/7)
	if err != nil {
		return fmt.Errorf("create artifact renderer: %w", err)
	}
	defer cleanup()
	best := snapshotRenderer.Render(bestParams)
	var artifactErrors []error
	if err := artifacts.SavePNGArtifact(jobID, store.ArtifactBest, best); err != nil {
		artifactErrors = append(artifactErrors, fmt.Errorf("save best artifact: %w", err))
	}
	if err := artifacts.SavePNGArtifact(jobID, store.ArtifactDiff, computeDiffImage(ref, best, fit.ColormapTurbo)); err != nil {
		artifactErrors = append(artifactErrors, fmt.Errorf("save diff artifact: %w", err))
	}
	return errors.Join(artifactErrors...)
}
