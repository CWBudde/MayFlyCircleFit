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
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

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
	base        opt.Optimizer
	observer    opt.Observer
	initial     *opt.Candidate
	resumeCount int

	mu          sync.Mutex
	iterations  int
	evaluations int
}

func (o *progressOptimizer) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	return o.base.Run(eval, lower, upper, dim)
}

func (o *progressOptimizer) RunContext(ctx context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	lifecycle, ok := o.base.(opt.LifecycleOptimizer)
	if !ok {
		return opt.Result{}, fmt.Errorf("optimizer does not support lifecycle execution")
	}

	o.mu.Lock()
	baseIterations, baseEvaluations := o.iterations, o.evaluations
	initial := o.initial
	if initial != nil && len(initial.Params) == problem.Dim {
		o.initial = nil
	} else {
		initial = nil
	}
	o.mu.Unlock()

	result, err := lifecycle.RunContext(ctx, problem, opt.RunOptions{
		Initial:     initial,
		ResumeCount: o.resumeCount,
		Observer: func(progress opt.Progress) {
			progress.Iterations += baseIterations
			progress.Evaluations += baseEvaluations
			if o.observer != nil {
				o.observer(progress)
			}
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
	optimizer, err := opt.NewMayflyVariant(string(job.Config.Variant), job.Config.Iters, job.Config.PopSize, seed)
	if err != nil {
		markJobFailed(jm, jobID, err)
		return err
	}
	start := time.Now()
	baseIterations, baseEvaluations := job.Iterations, job.Evaluations
	initialCost := job.InitialCost
	if len(job.BestParams) == 0 {
		initialCost = rend.Cost(make([]float64, job.Config.Circles*7))
		_ = jm.UpdateJob(jobID, func(live *Job) { live.InitialCost = initialCost })
	}

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
		_ = traceWriter.Write(store.TraceEntry{Iteration: baseIterations, Cost: initialCost, Timestamp: start})
	}

	nextBroadcast := start
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
		if traceWriter != nil {
			_ = traceWriter.Write(store.TraceEntry{Iteration: iterations, Cost: progress.BestCost, Timestamp: now})
		}
		if !now.Before(nextCheckpoint) && checkpointStore != nil && !nextCheckpoint.IsZero() {
			if err := saveCheckpoint(jm, checkpointStore, rend, jobID); err != nil {
				slog.Warn("Failed to save periodic checkpoint", "job_id", jobID, "error", err)
			}
			nextCheckpoint = now.Add(time.Duration(job.Config.CheckpointInterval) * time.Second)
		}
		if !now.Before(nextBroadcast) {
			elapsed := now.Sub(start).Seconds()
			cps := 0.0
			if elapsed > 0 {
				cps = float64(evaluations*job.Config.Circles) / elapsed
			}
			jm.broadcaster.Broadcast(ProgressEvent{
				JobID: jobID, State: StateRunning, Iterations: iterations,
				BestCost: progress.BestCost, CPS: cps, Timestamp: now,
			})
			nextBroadcast = now.Add(500 * time.Millisecond)
		}
	}

	wrapped := &progressOptimizer{base: optimizer, observer: observer, resumeCount: job.Config.ResumeCount}
	if len(job.BestParams) > 0 {
		wrapped.initial = &opt.Candidate{Params: append([]float64(nil), job.BestParams...), Cost: job.BestCost}
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
			err = fmt.Errorf("batch resume is not supported")
		} else {
			result, err = renderer.OptimizeBatchContext(ctx, rend, wrapped, job.Config.Circles, job.Config.BatchSize, convergence)
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
	if len(circleData) > 0 {
		if err := checkpointStore.SaveCircleData(jobID, circleData); err != nil {
			slog.Warn("Failed to save circle metadata", "job_id", jobID, "error", err)
		}
	}
	if traceWriter != nil {
		_ = traceWriter.Write(store.TraceEntry{Iteration: iterations, Cost: result.BestCost, Timestamp: time.Now()})
		_ = traceWriter.Flush()
	}
	if checkpointStore != nil && job.Config.CheckpointInterval > 0 {
		if err := saveCheckpoint(jm, checkpointStore, rend, jobID); err != nil {
			slog.Warn("Failed to save final checkpoint", "job_id", jobID, "error", err)
		}
	}
	if artifacts, ok := checkpointStore.(store.ArtifactStore); ok && result.BestImage != nil {
		_ = artifacts.SavePNGArtifact(jobID, store.ArtifactBest, result.BestImage)
		_ = artifacts.SavePNGArtifact(jobID, store.ArtifactDiff, computeDiffImage(ref, result.BestImage))
	}

	elapsed := time.Since(start).Seconds()
	cps := 0.0
	if elapsed > 0 {
		cps = float64(result.Evaluations*job.Config.Circles) / elapsed
	}
	jm.broadcaster.Broadcast(ProgressEvent{
		JobID: jobID, State: StateCompleted, Iterations: iterations,
		BestCost: result.BestCost, CPS: cps, Timestamp: time.Now(),
	})
	slog.Info("Job completed", "job_id", jobID, "iterations", iterations, "evaluations", evaluations, "best_cost", result.BestCost)
	return nil
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
		cpu.SetThreads(config.Threads)
		return cpu, func() {}, nil
	}
	backend := config.Backend
	if backend == "" {
		backend = app.BackendCPU
	}
	if backend == app.BackendCPU {
		cpu := renderer.NewCPURenderer(ref, circleCount)
		cpu.SetThreads(config.Threads)
		return cpu, func() {}, nil
	}
	return renderer.NewRendererForBackend(string(backend), ref, circleCount)
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
	if err := checkpointStore.SaveCheckpoint(jobID, checkpoint); err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	return saveCheckpointArtifacts(checkpointStore, rend, job.Config, jobID, job.BestParams)
}

func saveCheckpointArtifacts(checkpointStore store.Store, rend renderer.Renderer, config store.JobConfig, jobID string, bestParams []float64) error {
	artifacts, ok := checkpointStore.(store.ArtifactStore)
	if !ok {
		return nil
	}
	ref := rend.Reference()
	snapshotRenderer, cleanup, err := rendererForJob(config, ref, len(bestParams)/7)
	if err != nil {
		return err
	}
	defer cleanup()
	best := snapshotRenderer.Render(bestParams)
	if err := artifacts.SavePNGArtifact(jobID, store.ArtifactBest, best); err != nil {
		return err
	}
	return artifacts.SavePNGArtifact(jobID, store.ArtifactDiff, computeDiffImage(ref, best))
}
