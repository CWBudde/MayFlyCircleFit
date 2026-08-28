package server

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

// maxTraceSamplesPerRun bounds how many progress records one optimizer run
// contributes to trace.jsonl. It is the largest iteration count that was
// requestable before app.MaxIterations was raised, so the worst-case trace an
// epoch-and-restart campaign can produce is exactly what it was then.
const maxTraceSamplesPerRun = 10000

// traceSampleStride returns the iteration stride that keeps one run's trace at
// or below maxTraceSamplesPerRun records. It is 1 whenever the run fits, which
// is why raising the iteration cap changes no trace that was already valid.
func traceSampleStride(iters int) int {
	if iters <= maxTraceSamplesPerRun {
		return 1
	}

	stride := iters / maxTraceSamplesPerRun
	if iters%maxTraceSamplesPerRun != 0 {
		stride++
	}

	return stride
}

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

// IterationBudget forwards the wrapped optimizer's iteration cap so the
// pipeline can budget its stages through this wrapper.
func (o *progressOptimizer) IterationBudget() int {
	return opt.StageIterationBudget(o.base)
}

func (o *progressOptimizer) RunContext(ctx context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	lifecycle, ok := o.base.(opt.LifecycleOptimizer)
	if !ok {
		return opt.Result{}, errors.New("optimizer does not support lifecycle execution")
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
				err := options.EpochObserver(boundary)
				if err != nil {
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
		if state := jm.getJobState(jobID); state != StatePaused {
			markJobCancelled(jm, jobID)
		}

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

	rend, effectiveBackend, cleanup, err := rendererForJob(job.Config, ref, job.Config.Circles)
	if err != nil {
		markJobFailed(jm, jobID, err)
		return err
	}
	defer cleanup()
	// Record what the renderer will actually do, not what the configuration
	// asked for. This is the only point where the backend's decision and the
	// GOMAXPROCS clamp have both been applied.
	width := renderer.EvaluationWidth(rend)

	err = jm.UpdateJob(jobID, func(j *Job) {
		j.EffectiveBackend = effectiveBackend

		if width > 1 {
			j.EvaluationWidth = width
		}
	})
	if err != nil {
		return err
	}

	seed := job.Config.EffectiveSeed
	if seed == 0 {
		seed = job.Config.Seed
	}

	optimizer, err := newStageOptimizer(job.Config, rend, seed)
	if err != nil {
		markJobFailed(jm, jobID, err)
		return err
	}

	// Restarts wrap epochs: one attempt is a whole epoch chain, and the
	// attempts themselves are independent.
	optimizer = opt.WithRestarts(opt.WithEpochs(optimizer, job.Config.OptimizerEpochs), job.Config.OptimizerRestarts)
	start := time.Now()
	baseIterations, baseEvaluations := job.Iterations, job.Evaluations
	initialCost := job.InitialCost
	metricCost := job.BestCost

	metricParams := append([]float64(nil), job.BestParams...)
	if len(job.BestParams) == 0 {
		metricParams = make([]float64, job.Config.Circles*app.ParamsPerCircle)
		initialCost = rend.Cost(metricParams)
		metricCost = initialCost
		// A hand-authored arrangement seeds the run here, and only here: this is
		// the branch for a job with no parent, so a continuation's inherited
		// parameters can never be overwritten by a spec the schedule copied
		// forward. initialCost stays the blank-canvas cost either way, because
		// that is what every other run and every continuation measures its
		// improvement against.
		if len(job.Config.InitialCircles) > 0 {
			seeded, seedErr := initialCircleParams(job.Config, ref)
			if seedErr != nil {
				markJobFailed(jm, jobID, seedErr)
				return seedErr
			}

			metricParams = seeded
			metricCost = rend.Cost(seeded)
			job.BestParams = seeded
			job.BestCost = metricCost
			seededParams, seededCost := seeded, metricCost
			_ = jm.UpdateJob(jobID, func(live *Job) {
				live.InitialCost = initialCost
				updateBestResult(live, seededParams, seededCost)
			})
		} else {
			_ = jm.UpdateJob(jobID, func(live *Job) { live.InitialCost = initialCost })
		}
	}
	var initialSSIM *float64

	if job.Config.EnableSSIM {
		metricRenderer := rend

		metricCleanup := func() {}
		if len(metricParams) != rend.Dim() {
			metricRenderer, _, metricCleanup, err = rendererForJob(job.Config, ref, len(metricParams)/7)
			if err != nil {
				markJobFailed(jm, jobID, err)
				return err
			}
		}

		initialSSIM = calculateSSIM(metricRenderer.Render(metricParams), ref, jobID)

		metricCleanup()
	}

	initialSample := qualitySample(baseIterations, metricCost, initialSSIM, start)
	// No evaluation of this stage has run yet, so its throughput is zero even
	// when the job inherited a parent's evaluation count.
	initialSample.CPS = 0
	initialSample.Evaluations = baseEvaluations
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
			closeErr := traceWriter.Close()
			if closeErr != nil {
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

	// A device that fails mid-run leaves the OpenCL renderer answering from its
	// CPU fallback and returning no error, so nothing else in this loop would
	// notice. The renderer degrades permanently once it degrades at all, which
	// is why one bool is enough: after the first report this costs a type
	// assertion per progress callback and writes nothing.
	// trace.jsonl is written once per completed optimizer iteration, so its size
	// is proportional to the iteration budget rather than to wall clock. That is
	// affordable at a few thousand iterations and is not affordable at a
	// million, because restoreJobTrace reads the whole file back into
	// MetricHistory at startup and the job-detail page seeds the island with it.
	// The stride bounds one run to maxTraceSamplesPerRun records; it is 1 for
	// every configuration that was expressible before app.MaxIterations was
	// raised, so no existing job's trace changes.
	traceStride := traceSampleStride(job.Config.Iters)
	lastTracedCost := math.Inf(1)

	backendDegradationRecorded := false
	noteBackendDegradation := func() {
		if backendDegradationRecorded || !renderer.Degraded(rend) {
			return
		}

		backendDegradationRecorded = true

		slog.Warn("Renderer degraded to its CPU fallback mid-run",
			"job_id", jobID, "backend", effectiveBackend)

		_ = jm.UpdateJob(jobID, func(j *Job) { j.BackendDegraded = true })
	}

	observer := func(progress opt.Progress) {
		noteBackendDegradation()

		iterations := baseIterations + progress.Iterations

		evaluations := baseEvaluations + progress.Evaluations

		err := jm.UpdateProgress(jobID, iterations, evaluations, progress.BestParams, progress.BestCost)
		if err != nil {
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
		sample.Evaluations = evaluations
		sample.OptimizerDiagnostics = progress.Diagnostics

		sample.CPS = throughputCPS(progress.Evaluations, job.Config.Circles, now.Sub(start).Seconds())

		// Every improvement is recorded whatever the stride, because a trace is
		// scored by scanning it for the lowest cost at or below an evaluation
		// cap. Decimating improvements would move the evaluation count a
		// measurement attributes that cost to.
		traceSample := progress.Iterations%traceStride == 0
		if progress.BestCost < lastTracedCost {
			lastTracedCost = progress.BestCost
			traceSample = true
		}

		if traceWriter != nil && traceSample {
			_ = traceWriter.Write(traceEntry(sample))
		}

		if !now.Before(nextCheckpoint) && checkpointStore != nil && !nextCheckpoint.IsZero() {
			err := saveCheckpoint(jm, checkpointStore, rend, jobID)
			if err != nil {
				slog.Warn("Failed to save periodic checkpoint", "job_id", jobID, "error", err)
			}

			nextCheckpoint = now.Add(time.Duration(job.Config.CheckpointInterval) * time.Second)
		}

		if shouldBroadcast {
			bestCost, bestRevision, candidateCost, ok := jm.bestSnapshot(jobID)
			if !ok {
				return
			}

			cps := sample.CPS
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

		err := jm.UpdateProgress(jobID, iterations, evaluations, boundary.Progress.BestParams, boundary.Progress.BestCost)
		if err != nil {
			return err
		}

		if checkpointStore != nil {
			err := saveCheckpoint(jm, checkpointStore, rend, jobID)
			if err != nil {
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

	var retainedPrefixCanvas *image.NRGBA
	if job.Config.Mode == app.ModeBatch && len(job.BestParams) > 0 && len(job.BestParams) < job.Config.Circles*app.ParamsPerCircle {
		retainedPrefixCanvas = loadRetainedPrefixCanvas(checkpointStore, job, ref)
	}

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
			err = errors.New("sequential resume is not supported")
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
				if retainedPrefixCanvas != nil {
					result, err = renderer.OptimizeBatchAppendFromCanvasContext(
						ctx,
						rend,
						wrapped,
						job.BestParams,
						retainedPrefixCanvas,
						job.BestCost,
						job.Config.Circles,
						job.Config.BatchSize,
						convergence,
					)
				} else {
					result, err = renderer.OptimizeBatchAppendContext(
						ctx,
						rend,
						wrapped,
						job.BestParams,
						job.Config.Circles,
						job.Config.BatchSize,
						convergence,
					)
				}

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
				err = errors.New("batch resume is not supported")
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
		err = errors.New("unknown mode")
	}

	// The observer stops firing before the last evaluations are made, and a run
	// short enough to have no progress callback at all never fired it once.
	noteBackendDegradation()

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if state := jm.getJobState(jobID); state != StatePaused {
				markJobCancelled(jm, jobID)
			}

			return err
		}

		markJobFailed(jm, jobID, err)

		return err
	}

	iterations := baseIterations + result.Iterations
	evaluations := baseEvaluations + result.Evaluations
	// The outcome is recorded first and published as `completed` last, with the
	// checkpoint write in between. Completion is the signal every continuation
	// waits on — the extend and polish endpoints and the schedule executor all
	// read it as "a completed checkpoint exists" — so a job that announced it
	// before saveCheckpoint returned handed out a checkpoint that was not on
	// disk yet. On a loaded host the gap is wide enough that the very next stage
	// of a campaign asked for the parent checkpoint and was told it did not
	// exist, which failed the whole campaign.
	if err := jm.RecordFinalResult(jobID, iterations, evaluations, result.BestParams, result.BestCost, initialCost, string(result.Termination)); err != nil {
		return err
	}

	completedAt := time.Now()

	var finalSSIM *float64
	if job.Config.EnableSSIM && result.BestImage != nil {
		finalSSIM = calculateSSIM(result.BestImage, ref, jobID)
	}

	finalSample := qualitySample(iterations, result.BestCost, finalSSIM, completedAt)
	finalSample.Evaluations = evaluations
	finalSample.CPS = throughputCPS(result.Evaluations, job.Config.Circles, time.Since(start).Seconds())

	_ = jm.RecordMetrics(jobID, finalSample)
	if len(circleData) > 0 {
		err := checkpointStore.SaveCircleData(jobID, circleData)
		if err != nil {
			slog.Warn("Failed to save circle metadata", "job_id", jobID, "error", err)
		}
	}

	if traceWriter != nil {
		_ = traceWriter.Write(traceEntry(finalSample))
		_ = traceWriter.Flush()
	}
	var persistenceErr error

	if checkpointStore != nil {
		err := saveCheckpointWithImage(jm, checkpointStore, rend, jobID, result.BestImage)
		if err != nil {
			persistenceErr = fmt.Errorf("persist final result: %w", err)
			_ = jm.UpdateJob(jobID, func(live *Job) {
				live.Error = "failed to persist final result"
			})
			slog.Error("Failed to persist final job result", "job_id", jobID, "error", err)
		}
	}

	// Everything durable is on disk, so the job may now say so. A cancellation
	// that landed while the result was being written wins: the transition is
	// refused, and the job stays cancelled rather than being resurrected as a
	// completed one whose caller was told it had stopped.
	if err := jm.MarkJobCompleted(jobID); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			slog.Info("Job settled before its final result was published", "job_id", jobID, "error", err)
			return persistenceErr
		}

		return err
	}

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
	initialVisitCounts, err := inheritedContiguousWindowVisitCounts(checkpointStore, job)
	if err != nil {
		return nil, fmt.Errorf("restore polishing coverage: %w", err)
	}

	seed := job.Config.EffectiveSeed
	if seed == 0 {
		seed = job.Config.Seed
	}
	// Polishing leases a session per evaluation like the staged pipelines do, so
	// it honors the job's evaluation width instead of falling back to a serial
	// optimizer while the rest of the run is 48 evaluations wide.
	polishingOptions := []opt.MayflyOption{
		opt.WithLogger(slog.Default()),
		opt.WithEarlyStop(opt.Stop{
			MinImprovement:  job.Config.PolishingMinImprovement,
			StagnationIters: job.Config.PolishingStagnationIters,
		}),
		parallelEvaluationOption(job.Config, rend),
	}
	// Diagnostics are a job-wide opt-in, and a polishing-only job has no other
	// optimizer to report them. Leaving the polishing adapter out would make
	// such a job complete with every trace entry missing optimizerDiagnostics
	// despite having explicitly asked for them.
	if job.Config.EnableOptimizerDiagnostics {
		polishingOptions = append(polishingOptions, opt.WithMayflySearchDiagnostics())
	}

	polisher, err := opt.NewMayflyVariant(
		string(app.VariantStandard), job.Config.PolishingIters, job.Config.PolishingPopSize, seed,
		polishingOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("create polishing optimizer: %w", err)
	}

	polisher = opt.WithEpochs(polisher, job.Config.PolishingEpochs)
	polishingWidth := opt.ParallelEvaluationWidth(polisher)

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
		err := saveCheckpoint(jm, checkpointStore, rend, job.ID)
		if err != nil {
			return nil, fmt.Errorf("persist pre-polishing result: %w", err)
		}
	}

	committedParams := append([]float64(nil), batch.BestParams...)
	committedCost := batch.BestCost

	persistPolishingBoundary := func(params []float64, cost float64, iterations, evaluations int, clearCandidate bool) error {
		iterations += baseIterations + mainIterations
		evaluations += baseEvaluations + mainEvaluations

		err := jm.UpdateJob(job.ID, func(live *Job) {
			live.Iterations = iterations
			live.Evaluations = evaluations
			updateBestResult(live, params, cost)

			if clearCandidate {
				live.CandidateCost = nil
			}
		})
		if err != nil {
			return err
		}

		if checkpointStore != nil {
			return saveCheckpoint(jm, checkpointStore, rend, job.ID)
		}

		return nil
	}

	polish, err := renderer.PolishCircleBatchContext(ctx, rend, polisher, batch.BestParams, renderer.BatchPolishOptions{
		ActiveSetSize:      job.Config.PolishingActiveSetSize,
		MaxSweeps:          job.Config.PolishingMaxSweeps,
		Strategy:           renderer.BatchPolishStrategy(job.Config.PolishingStrategy),
		InitialVisitCounts: initialVisitCounts,
		Observer: func(progress opt.Progress) {
			progress.Iterations += mainIterations

			progress.Evaluations += mainEvaluations

			err := jm.UpdateCandidateProgress(
				job.ID,
				baseIterations+progress.Iterations,
				baseEvaluations+progress.Evaluations,
				progress.BestCost,
			)
			if err != nil {
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
		"evaluation_workers", polishingWidth,
		"population", job.Config.PolishingPopSize,
		"best_cost", polish.BestCost,
	)

	return batch, nil
}

// inheritedContiguousWindowVisitCounts reconstructs the selection history a
// polishing continuation inherits without adding derived state to checkpoints.
// Only PolishedFrom links are followed: an extension changes the vector and a
// merit-based strategy does not leave enough information to replay its active
// sets. The checkpoints are replayed oldest first through the renderer's own
// planner so selection and reconstruction cannot disagree.
func inheritedContiguousWindowVisitCounts(checkpointStore store.Store, job *Job) ([]int, error) {
	if checkpointStore == nil || job == nil || job.PolishedFrom == "" ||
		job.Config.PolishingStrategy != app.PolishingContiguousWindow {
		return nil, nil
	}

	circleCount := len(job.BestParams) / app.ParamsPerCircle
	if circleCount == 0 {
		circleCount = job.Config.Circles
	}

	seen := map[string]struct{}{job.ID: {}}
	lineage := make([]*store.Checkpoint, 0, 4)

	for parentID := job.PolishedFrom; parentID != ""; {
		if _, repeated := seen[parentID]; repeated {
			return nil, fmt.Errorf("checkpoint polishing lineage is cyclic at %q", parentID)
		}

		seen[parentID] = struct{}{}

		if len(lineage) >= maxChainLength {
			return nil, fmt.Errorf("checkpoint polishing lineage exceeds %d stages", maxChainLength)
		}

		checkpoint, err := checkpointStore.LoadCheckpoint(parentID)
		if err != nil {
			return nil, fmt.Errorf("load parent checkpoint %q: %w", parentID, err)
		}

		config, err := app.Normalize(checkpoint.Config)
		if err != nil {
			return nil, fmt.Errorf("normalize parent checkpoint %q: %w", parentID, err)
		}

		if checkpoint.ActualCircles != circleCount ||
			checkpoint.ActualCircles != checkpoint.RequestedCircles ||
			!config.PolishingEnabled ||
			config.PolishingStrategy != app.PolishingContiguousWindow ||
			!completedCheckpointTermination(checkpoint.Termination) {
			break
		}

		lineage = append(lineage, checkpoint)
		parentID = checkpoint.PolishedFrom
	}

	var visits []int

	for _, l := range slices.Backward(lineage) {
		config, _ := app.Normalize(l.Config)

		_, nextVisits, err := renderer.PlanContiguousWindows(
			circleCount,
			config.PolishingActiveSetSize,
			config.PolishingMaxSweeps,
			visits,
		)
		if err != nil {
			return nil, fmt.Errorf("replay parent checkpoint %q: %w", l.JobID, err)
		}

		visits = nextVisits
	}

	return visits, nil
}

func completedCheckpointTermination(termination string) bool {
	switch termination {
	case "completed", "target_cost", "stagnation", "convergence", "stage_convergence", "refill_limit":
		return true
	default:
		return false
	}
}

// parallelEvaluationOption derives the optimizer option from what the renderer
// can actually deliver and warns when an explicit request cannot be honored,
// which is the case for every backend without independent sessions.
// newStageOptimizer builds the optimizer a job's stages run with.
//
// It is the server's half of a decision cmd/run.go makes in its own
// newStageOptimizer; the two must agree on which engine a configuration names,
// because a job resumed from the CLI has to run what the server ran. Nothing
// here refuses a MayFly-only setting: app.JobConfig.Validate already did, so a
// Dragonfly configuration reaching this point carries no variant, no advanced
// MayFly knob and no polishing.
//
// An empty optimizer is MayFly. That is what every checkpoint and job payload
// written before the field existed carries, and they must keep running exactly
// as they did.
func newStageOptimizer(config store.JobConfig, rend renderer.Renderer, seed int64) (opt.Optimizer, error) {
	switch config.ResolvedOptimizer() {
	case app.OptimizerDragonfly:
		return newDragonflyOptimizer(config, rend, seed), nil
	case app.OptimizerCMAES:
		return newCMAESOptimizer(config, rend, seed), nil
	}

	mayflyOptions := []opt.MayflyOption{
		opt.WithLogger(slog.Default()), opt.WithEarlyStop(buildEarlyStop(config)),
		opt.WithCrossoverCount(config.CrossoverCount),
		// Optimizer stages only. Polishing runs its own smaller
		// standard-variant population and is deliberately left alone. That
		// includes the initial-population sequence: a polishing run starts
		// from the incumbent it is polishing, so how a cold population would
		// have sampled the box does not apply to it.
		opt.WithQMCInit(string(config.ResolvedQMCInit())),
		opt.OptionalFloat(config.DanceDamp, opt.WithDanceDamp),
		opt.OptionalFloat(config.AquilaWeight, opt.WithAquilaWeight),
		opt.OptionalFloat(config.OppositionProbability, opt.WithOppositionProbability),
		parallelEvaluationOption(config, rend),
	}
	if config.EnableOptimizerDiagnostics {
		mayflyOptions = append(mayflyOptions, opt.WithMayflySearchDiagnostics())
	}

	optimizer, err := opt.NewMayflyVariant(
		string(config.Variant), config.Iters, config.PopSize, seed, mayflyOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("create optimizer: %w", err)
	}

	return optimizer, nil
}

// newCMAESOptimizer builds the configured CMA-ES adapter. The application
// validates the mode and resource limits before a worker reaches this point.
func newCMAESOptimizer(config store.JobConfig, rend renderer.Renderer, seed int64) opt.Optimizer {
	options := []opt.CMAESOption{
		opt.WithCMAESLogger(slog.Default()),
		opt.WithCMAESEarlyStop(buildEarlyStop(config)),
		opt.WithCMAESInitialSigma(config.ResolvedCMAESInitialSigma()),
		opt.WithCMAESCovarianceMode(
			string(config.ResolvedCMAESCovarianceMode()), app.ParametersPerCircle,
		),
		opt.WithCMAESActiveCMA(config.ResolvedCMAESActive()),
		opt.WithCMAESRestartStrategy(string(config.ResolvedCMAESRestartStrategy())),
	}
	if config.EnableOptimizerDiagnostics {
		options = append(options, opt.WithCMAESSearchDiagnostics())
	}

	width, granted := renderer.ParallelEvaluationWidth(rend, config.ParallelEvaluation)
	if granted {
		options = append(options, opt.WithCMAESParallelEvaluation(width))
	}

	if config.ParallelEvaluation && !granted {
		slog.Warn("Parallel evaluation requested but unavailable; evaluating serially",
			"backend", config.Backend, "evaluationWorkers", config.EvaluationWorkers)
	}

	return opt.NewCMAES(config.Iters, config.PopSize, seed, options...)
}

// newDragonflyOptimizer builds the proof-of-concept Dragonfly adapter. It
// carries the iteration cap, population, seed, logging, optimizer-level early
// stopping and parallel evaluation across, and nothing else.
func newDragonflyOptimizer(config store.JobConfig, rend renderer.Renderer, seed int64) opt.Optimizer {
	options := []opt.DragonflyOption{
		opt.WithDragonflyLogger(slog.Default()),
		opt.WithDragonflyEarlyStop(buildEarlyStop(config)),
	}

	width, granted := renderer.ParallelEvaluationWidth(rend, config.ParallelEvaluation)
	if granted {
		options = append(options, opt.WithDragonflyParallelEvaluation(width))
	}

	if config.ParallelEvaluation && !granted {
		slog.Warn("Parallel evaluation requested but unavailable; evaluating serially",
			"backend", config.Backend, "evaluationWorkers", config.EvaluationWorkers)
	}

	return opt.NewDragonfly(config.Iters, config.PopSize, seed, options...)
}

func parallelEvaluationOption(config store.JobConfig, rend renderer.Renderer) opt.MayflyOption {
	option, enabled := renderer.ParallelEvaluationOption(rend, config.ParallelEvaluation)
	if config.ParallelEvaluation && !enabled {
		slog.Warn("Parallel evaluation requested but unavailable; evaluating serially",
			"backend", config.Backend, "evaluationWorkers", config.EvaluationWorkers)
	}

	return option
}

// initialCircleParams converts a hand-authored arrangement into the optimizer's
// parameter vector, refusing any circle the canvas cannot hold.
//
// It refuses rather than clamps on purpose. Clamping is right for a candidate
// the optimizer proposed -- it explores past the edges and is pulled back -- but
// a hand-placed circle that silently moves is a run that no longer matches the
// document describing it, and the cost it reports would be unexplainable.
func initialCircleParams(config store.JobConfig, ref *image.NRGBA) ([]float64, error) {
	params, err := config.InitialCircles.ToParams()
	if err != nil {
		return nil, err
	}

	bounds := fit.NewBounds(config.Circles, ref.Bounds().Dx(), ref.Bounds().Dy())

	clamped := append([]float64(nil), params...)
	bounds.ClampVector(clamped)

	for i := range params {
		if params[i] == clamped[i] {
			continue
		}

		return nil, fmt.Errorf("initialCircles[%d].%s is outside the bounds this canvas allows",
			i/app.ParamsPerCircle, initialCircleFields[i%app.ParamsPerCircle])
	}

	return params, nil
}

// initialCircleFields names the slots of one circle in the parameter vector so
// a rejected value can say which one it was.
var initialCircleFields = [app.ParamsPerCircle]string{"x", "y", "r", "color.r", "color.g", "color.b", "opacity"}

var (
	// errCanvasRequiresCPU and errCanvasDimensionMismatch are the two ways a
	// supplied base canvas is refused. Only the CPU renderer accumulates one.
	errCanvasRequiresCPU       = errors.New("custom canvas requires CPU backend")
	errCanvasDimensionMismatch = errors.New("canvas dimensions do not match reference")
)

// rendererForJob builds the renderer a job will run on and reports the backend
// it actually got. The two differ when BackendFallback is set and the requested
// backend cannot be constructed, which is why the backend is returned rather
// than read back off config: nothing on the Renderer interface says which
// implementation answered.
func rendererForJob(
	config store.JobConfig, ref *image.NRGBA, circleCount int,
) (renderer.Renderer, app.Backend, func(), error) {
	if config.CanvasPath != "" {
		return canvasRendererForJob(config, ref, circleCount)
	}

	backend := config.Backend
	if backend == "" {
		backend = app.BackendCPU
	}

	if backend == app.BackendCPU {
		return newJobCPURenderer(config, ref, circleCount), app.BackendCPU, func() {}, nil
	}

	rend, cleanup, err := renderer.NewRendererForBackend(string(backend), ref, circleCount)
	if err == nil {
		return rend, backend, cleanup, nil
	}

	// Only an unavailable backend falls back. A misspelled name is a client
	// mistake and silently running it on the CPU would hide that, and an error
	// from a backend that did start is a device problem the run should report.
	if config.BackendFallback != app.BackendCPU || !errors.Is(err, renderer.ErrBackendUnavailable) {
		// Deliberately unwrapped: safeJobError trims the sentinel's own prefix
		// off this message to build the client-facing one, and a wrapper in
		// between would leave the sentinel printed twice.
		return nil, "", cleanup, err //nolint:wrapcheck // safeJobError formats this error by trimming its sentinel prefix
	}

	cleanup()
	slog.Warn("Requested backend unavailable, falling back to cpu",
		"backend", backend, "fallback", app.BackendCPU, "reason", err)

	return newJobCPURenderer(config, ref, circleCount), app.BackendCPU, func() {}, nil
}

// canvasRendererForJob builds the renderer for a job that starts from a
// supplied base canvas, which only the CPU backend accumulates.
func canvasRendererForJob(
	config store.JobConfig, ref *image.NRGBA, circleCount int,
) (renderer.Renderer, app.Backend, func(), error) {
	if config.Backend != "" && config.Backend != app.BackendCPU {
		return nil, "", func() {}, errCanvasRequiresCPU
	}

	canvas, err := loadReferenceImage(config.CanvasPath)
	if err != nil {
		return nil, "", func() {}, fmt.Errorf("load canvas: %w", err)
	}

	if canvas.Bounds().Dx() != ref.Bounds().Dx() || canvas.Bounds().Dy() != ref.Bounds().Dy() {
		return nil, "", func() {}, errCanvasDimensionMismatch
	}

	cpu := renderer.NewCPURendererWithCanvas(ref, canvas, circleCount)
	configureJobCPURenderer(cpu, config)

	return cpu, app.BackendCPU, func() {}, nil
}

// newJobCPURenderer builds the CPU renderer a job runs on, configured from its
// parallelism and compositing settings.
func newJobCPURenderer(config store.JobConfig, ref *image.NRGBA, circleCount int) *renderer.CPURenderer {
	cpu := renderer.NewCPURenderer(ref, circleCount)
	configureJobCPURenderer(cpu, config)

	return cpu
}

// configureJobCPURenderer applies a job's parallelism settings and records the
// effective evaluation width. The server had no equivalent of the CLI's startup
// line, so a job's actual concurrency was invisible in the server log.
func configureJobCPURenderer(cpu *renderer.CPURenderer, config store.JobConfig) {
	renderer.ConfigureCPUParallelism(cpu, config.Threads, config.EvaluationWorkers, config.ParallelEvaluation)
	renderer.ConfigureCPUCompositing(cpu, config.FastCompositing)
	renderer.LogCPURendererConfiguration(cpu)
}

func markJobFailed(jm *JobManager, jobID string, err error) {
	_ = jm.FailJob(jobID, safeJobError(err))
	slog.Error("Job failed", "job_id", jobID, "error", err)
}

func safeJobError(err error) string {
	switch {
	case errors.Is(err, renderer.ErrStagedOptimizationUnsupported):
		return "selected backend does not support this optimization mode"
	case errors.Is(err, renderer.ErrBackendUnavailable):
		detail := strings.TrimPrefix(err.Error(), renderer.ErrBackendUnavailable.Error()+": ")
		return fmt.Sprintf("renderer backend unavailable: %v", detail)
	case errors.Is(err, renderer.ErrInvalidOptimizationInput):
		return "optimizer produced an invalid result"
	default:
		return "job execution failed"
	}
}

func markJobCancelled(jm *JobManager, jobID string) {
	err := jm.CancelJob(jobID)
	if err != nil && !errors.Is(err, ErrInvalidTransition) {
		slog.Warn("Failed to mark job cancelled", "job_id", jobID, "error", err)
	}
}

// loadRetainedPrefixCanvas reuses the completed parent's exact best artifact
// for a batch append. The cost check is deliberately exact: if an artifact is
// missing, stale, or was produced with different pixels, falling back to a
// parameter replay is slower but preserves the checkpoint's semantics.
func loadRetainedPrefixCanvas(checkpointStore store.Store, job *Job, ref *image.NRGBA) *image.NRGBA {
	if checkpointStore == nil || job == nil || job.ExtendedFrom == "" {
		return nil
	}

	artifacts, ok := checkpointStore.(store.ArtifactStore)
	if !ok {
		return nil
	}

	path, err := artifacts.ArtifactPath(job.ExtendedFrom, store.ArtifactBest)
	if err != nil {
		slog.Debug("Could not resolve retained prefix artifact; replaying parameters", "job_id", job.ID, "parent_job_id", job.ExtendedFrom, "error", err)
		return nil
	}

	canvas, err := loadReferenceImage(path)
	if err != nil {
		slog.Debug("Could not load retained prefix artifact; replaying parameters", "job_id", job.ID, "parent_job_id", job.ExtendedFrom, "error", err)
		return nil
	}

	if canvas.Bounds().Dx() != ref.Bounds().Dx() || canvas.Bounds().Dy() != ref.Bounds().Dy() {
		slog.Warn("Retained prefix artifact dimensions do not match; replaying parameters", "job_id", job.ID, "parent_job_id", job.ExtendedFrom)
		return nil
	}

	if artifactCost := fit.FastMSECost(canvas, ref); artifactCost != job.BestCost {
		slog.Warn("Retained prefix artifact cost does not match checkpoint; replaying parameters",
			"job_id", job.ID,
			"parent_job_id", job.ExtendedFrom,
			"artifact_cost", artifactCost,
			"checkpoint_cost", job.BestCost,
		)

		return nil
	}

	return canvas
}

func saveCheckpoint(jm *JobManager, checkpointStore store.Store, rend renderer.Renderer, jobID string) error {
	return saveCheckpointWithImage(jm, checkpointStore, rend, jobID, nil)
}

func saveCheckpointWithImage(jm *JobManager, checkpointStore store.Store, rend renderer.Renderer, jobID string, bestImage *image.NRGBA) error {
	job, exists := jm.GetJob(jobID)
	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}

	if len(job.BestParams) == 0 || math.IsInf(job.BestCost, 0) || math.IsNaN(job.BestCost) {
		return nil
	}

	checkpoint := store.NewCheckpoint(jobID, job.BestParams, job.BestCost, job.InitialCost, job.Iterations, job.Config)
	checkpoint.Evaluations = int64(job.Evaluations)
	applyJobLineage(checkpoint, job)

	if job.Termination != "" {
		checkpoint.Termination = job.Termination
	}

	var persistenceErrors []error

	err := checkpointStore.SaveCheckpoint(jobID, checkpoint)
	if err != nil {
		persistenceErrors = append(persistenceErrors, fmt.Errorf("save checkpoint: %w", err))
	}

	err = saveCheckpointArtifacts(checkpointStore, rend, job.Config, jobID, job.BestParams, bestImage)
	if err != nil {
		persistenceErrors = append(persistenceErrors, err)
	}

	return errors.Join(persistenceErrors...)
}

func saveCheckpointArtifacts(checkpointStore store.Store, rend renderer.Renderer, config store.JobConfig, jobID string, bestParams []float64, bestImage *image.NRGBA) error {
	artifacts, ok := checkpointStore.(store.ArtifactStore)
	if !ok {
		return nil
	}

	ref := rend.Reference()

	best := bestImage
	if best == nil {
		snapshotRenderer, _, cleanup, err := rendererForJob(config, ref, len(bestParams)/7)
		if err != nil {
			return fmt.Errorf("create artifact renderer: %w", err)
		}
		defer cleanup()

		best = snapshotRenderer.Render(bestParams)
	}

	if best.Bounds().Dx() != ref.Bounds().Dx() || best.Bounds().Dy() != ref.Bounds().Dy() {
		return errors.New("best artifact dimensions do not match reference")
	}

	diff := computeDiffImage(ref, best, fit.ColormapTurbo)
	var artifactErrors [2]error
	var writes sync.WaitGroup

	writes.Add(2)
	go func() {
		defer writes.Done()

		err := artifacts.SavePNGArtifact(jobID, store.ArtifactBest, best)
		if err != nil {
			artifactErrors[0] = fmt.Errorf("save best artifact: %w", err)
		}
	}()
	go func() {
		defer writes.Done()

		err := artifacts.SavePNGArtifact(jobID, store.ArtifactDiff, diff)
		if err != nil {
			artifactErrors[1] = fmt.Errorf("save diff artifact: %w", err)
		}
	}()

	writes.Wait()

	return errors.Join(artifactErrors[:]...)
}
