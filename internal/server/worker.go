package server

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// runJob executes an optimization job in the background.
// If checkpointStore is not nil and job has checkpointInterval > 0, periodic checkpoints are saved.
func runJob(ctx context.Context, jm *JobManager, checkpointStore store.Store, jobID string) error {
	// Get the job
	job, exists := jm.GetJob(jobID)
	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}

	// Update state to running
	err := jm.UpdateJob(jobID, func(j *Job) {
		j.State = StateRunning
	})
	if err != nil {
		return err
	}

	slog.Info("Starting job", "job_id", jobID, "ref", job.Config.RefPath)

	// Load reference image
	f, err := os.Open(job.Config.RefPath)
	if err != nil {
		markJobFailed(jm, jobID, fmt.Errorf("failed to open reference: %w", err))
		return err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		markJobFailed(jm, jobID, fmt.Errorf("failed to decode image: %w", err))
		return err
	}

	// Convert to NRGBA
	bounds := img.Bounds()
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, img.At(x, y))
		}
	}

	slog.Info("Loaded reference image", "job_id", jobID, "width", bounds.Dx(), "height", bounds.Dy())

	// Create renderer
	renderer := fit.NewCPURenderer(ref, job.Config.Circles)

	// Create optimizer
	optimizer := opt.NewMayfly(job.Config.Iters, job.Config.PopSize, job.Config.Seed)

	// Check if this is a resumed job (has existing best params)
	isResume := len(job.BestParams) > 0

	// Get initial cost (or use existing if resume)
	var initialCost float64
	if isResume {
		initialCost = job.InitialCost
		slog.Info("Resuming from checkpoint",
			"job_id", jobID,
			"previous_cost", job.BestCost,
			"previous_iterations", job.Iterations,
		)
	} else {
		initialParams := make([]float64, job.Config.Circles*7)
		initialCost = renderer.Cost(initialParams)
		jm.UpdateJob(jobID, func(j *Job) {
			j.InitialCost = initialCost
		})
	}

	// Run optimization based on mode
	start := time.Now()
	var result *fit.OptimizationResult

	// Check for cancellation before starting expensive operation
	select {
	case <-ctx.Done():
		markJobCancelled(jm, jobID)
		return ctx.Err()
	default:
	}

	// Start trace writer if enabled
	var traceWriter *store.TraceWriter
	if job.Config.EnableTrace {
		tw, err := store.NewTraceWriter("./data", jobID, false)
		if err != nil {
			slog.Warn("Failed to create trace writer", "job_id", jobID, "error", err)
		} else {
			traceWriter = tw
			defer func() {
				if err := traceWriter.Close(); err != nil {
					slog.Warn("Failed to close trace writer", "job_id", jobID, "error", err)
				}
			}()
			// Log initial state
			traceWriter.Write(store.TraceEntry{
				Iteration: 0,
				Cost:      job.InitialCost,
				Timestamp: start,
			})
		}
	}

	// Start progress monitoring goroutine
	progressDone := make(chan struct{})
	go monitorProgress(ctx, jm, jobID, start, progressDone)

	// Start trace monitoring goroutine if enabled
	traceDone := make(chan struct{})
	traceEnabled := traceWriter != nil
	if traceEnabled {
		go monitorTrace(ctx, jm, traceWriter, jobID, traceDone)
	} else {
		close(traceDone) // No tracing, close immediately
	}

	// Start checkpoint monitoring goroutine if enabled
	checkpointDone := make(chan struct{})
	checkpointEnabled := checkpointStore != nil && job.Config.CheckpointInterval > 0
	if checkpointEnabled {
		go monitorCheckpoints(ctx, jm, checkpointStore, renderer, jobID, checkpointDone)
	} else {
		close(checkpointDone) // No checkpointing, close immediately
	}

	// If resuming, use optimizer with initial params
	if isResume {
		resumable, ok := optimizer.(opt.ResumableOptimizer)
		if !ok {
			err := fmt.Errorf("optimizer does not support resume")
			markJobFailed(jm, jobID, err)
			close(progressDone)
			if traceEnabled {
				close(traceDone)
			}
			if checkpointEnabled {
				close(checkpointDone)
			}
			return err
		}

		// Call optimizer directly with resume capability
		lower, upper := renderer.Bounds()
		bestParams, bestCost := resumable.RunWithInitial(
			job.BestParams,
			job.BestCost,
			renderer.Cost,
			lower,
			upper,
			renderer.Dim(),
		)

		// Create result
		result = &fit.OptimizationResult{
			BestParams:  bestParams,
			BestCost:    bestCost,
			InitialCost: initialCost,
			Iterations:  job.Config.Iters + job.Iterations, // Cumulative
		}
	} else {
		// Normal optimization from scratch
		// Use default convergence config (enabled by default for server jobs)
		convergenceConfig := fit.DefaultConvergenceConfig()

		switch job.Config.Mode {
		case "joint":
			result = fit.OptimizeJoint(renderer, optimizer, job.Config.Circles, convergenceConfig)
		case "sequential":
			result = fit.OptimizeSequential(renderer, optimizer, job.Config.Circles, convergenceConfig)
		case "batch":
			batchSize := 5
			passes := job.Config.Circles / batchSize
			if job.Config.Circles%batchSize != 0 {
				passes++
			}
			result = fit.OptimizeBatch(renderer, optimizer, batchSize, passes, convergenceConfig)
		default:
			err := fmt.Errorf("unknown mode: %s", job.Config.Mode)
			markJobFailed(jm, jobID, err)
			close(progressDone)
			if traceEnabled {
				close(traceDone)
			}
			if checkpointEnabled {
				close(checkpointDone)
			}
			return err
		}
	}

	// Close monitoring goroutines (only close if they were started)
	close(progressDone)
	if traceEnabled {
		close(traceDone)
	}
	if checkpointEnabled {
		close(checkpointDone)
	}
	elapsed := time.Since(start)

	// Check for cancellation after optimization
	// Note: Don't checkpoint here - shutdown already handles checkpointing running jobs
	select {
	case <-ctx.Done():
		markJobCancelled(jm, jobID)
		return ctx.Err()
	default:
	}

	// Update job with results
	endTime := time.Now()
	err = jm.UpdateJob(jobID, func(j *Job) {
		j.State = StateCompleted
		j.BestParams = result.BestParams
		j.BestCost = result.BestCost
		j.InitialCost = result.InitialCost
		j.Iterations = result.Iterations
		j.EndTime = &endTime
	})

	if err != nil {
		return err
	}

	// Compute throughput
	totalEvals := job.Config.Iters * job.Config.PopSize
	totalCircles := totalEvals * job.Config.Circles
	cps := float64(totalCircles) / elapsed.Seconds()

	slog.Info("Job completed",
		"job_id", jobID,
		"elapsed", elapsed,
		"initial_cost", result.InitialCost,
		"best_cost", result.BestCost,
		"circles_per_second", cps,
	)

	// Broadcast final completion event
	jm.broadcaster.Broadcast(ProgressEvent{
		JobID:      jobID,
		State:      StateCompleted,
		Iterations: result.Iterations,
		BestCost:   result.BestCost,
		CPS:        cps,
		Timestamp:  time.Now(),
	})

	return nil
}

// monitorProgress periodically broadcasts progress events during optimization
func monitorProgress(ctx context.Context, jm *JobManager, jobID string, startTime time.Time, done chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond) // Throttle to 2 updates per second
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get current job state
			job, exists := jm.GetJob(jobID)
			if !exists {
				return
			}

			elapsed := time.Since(startTime).Seconds()

			// Calculate CPS based on current iterations
			var cps float64
			if elapsed > 0 && job.Iterations > 0 {
				// Rough estimate: iterations completed so far
				totalEvals := job.Iterations * job.Config.PopSize
				totalCircles := totalEvals * job.Config.Circles
				cps = float64(totalCircles) / elapsed
			}

			// Broadcast progress event
			jm.broadcaster.Broadcast(ProgressEvent{
				JobID:      jobID,
				State:      job.State,
				Iterations: job.Iterations,
				BestCost:   job.BestCost,
				CPS:        cps,
				Timestamp:  time.Now(),
			})
		}
	}
}

// markJobFailed marks a job as failed with an error message
func markJobFailed(jm *JobManager, jobID string, err error) {
	endTime := time.Now()
	jm.UpdateJob(jobID, func(j *Job) {
		j.State = StateFailed
		j.Error = err.Error()
		j.EndTime = &endTime
	})
	slog.Error("Job failed", "job_id", jobID, "error", err)
}

// markJobCancelled marks a job as cancelled
func markJobCancelled(jm *JobManager, jobID string) {
	endTime := time.Now()
	jm.UpdateJob(jobID, func(j *Job) {
		j.State = StateCancelled
		j.EndTime = &endTime
	})
	slog.Info("Job cancelled", "job_id", jobID)
}

// monitorCheckpoints periodically saves checkpoints during optimization
func monitorCheckpoints(ctx context.Context, jm *JobManager, checkpointStore store.Store, renderer fit.Renderer, jobID string, done chan struct{}) {
	job, exists := jm.GetJob(jobID)
	if !exists {
		return
	}

	interval := time.Duration(job.Config.CheckpointInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Save checkpoint
			if err := saveCheckpoint(jm, checkpointStore, renderer, jobID); err != nil {
				slog.Error("Failed to save checkpoint", "job_id", jobID, "error", err)
			}
		}
	}
}

// saveCheckpoint saves a checkpoint for the given job
func saveCheckpoint(jm *JobManager, checkpointStore store.Store, renderer fit.Renderer, jobID string) error {
	// Get current job state
	job, exists := jm.GetJob(jobID)
	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}

	// Skip if no best params yet
	if len(job.BestParams) == 0 {
		slog.Debug("Skipping checkpoint, no best params yet", "job_id", jobID)
		return nil
	}

	// Create checkpoint
	checkpoint := store.NewCheckpoint(
		jobID,
		job.BestParams,
		job.BestCost,
		job.InitialCost,
		job.Iterations,
		job.Config,
	)

	// Save checkpoint metadata
	if err := checkpointStore.SaveCheckpoint(jobID, checkpoint); err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	slog.Info("Checkpoint saved",
		"job_id", jobID,
		"iteration", job.Iterations,
		"best_cost", job.BestCost,
	)

	// Save checkpoint artifacts (best.png, diff.png)
	if err := saveCheckpointArtifacts(checkpointStore, renderer, jobID, job.BestParams); err != nil {
		slog.Warn("Failed to save checkpoint artifacts", "job_id", jobID, "error", err)
		// Don't fail the checkpoint if artifacts fail - metadata is most important
	}

	return nil
}

// saveCheckpointArtifacts saves best.png and diff.png to the checkpoint directory
func saveCheckpointArtifacts(checkpointStore store.Store, renderer fit.Renderer, jobID string, bestParams []float64) error {
	// We need to access the filesystem directly since Store interface doesn't expose artifact paths
	// This assumes FSStore with ./data/jobs/<jobID>/ structure
	// TODO: Consider adding GetJobDir() to Store interface if we need different store implementations

	// For now, assume FSStore with ./data base directory
	jobDir := filepath.Join("./data", "jobs", jobID)

	// Render best image
	bestImg := renderer.Render(bestParams)

	// Save best.png
	bestPath := filepath.Join(jobDir, "best.png")
	bestFile, err := os.Create(bestPath)
	if err != nil {
		return fmt.Errorf("failed to create best.png: %w", err)
	}
	defer bestFile.Close()

	if err := png.Encode(bestFile, bestImg); err != nil {
		return fmt.Errorf("failed to encode best.png: %w", err)
	}

	// Compute and save diff.png
	ref := renderer.Reference()
	diffImg := computeDiffImage(ref, bestImg)

	diffPath := filepath.Join(jobDir, "diff.png")
	diffFile, err := os.Create(diffPath)
	if err != nil {
		return fmt.Errorf("failed to create diff.png: %w", err)
	}
	defer diffFile.Close()

	if err := png.Encode(diffFile, diffImg); err != nil {
		return fmt.Errorf("failed to encode diff.png: %w", err)
	}

	slog.Debug("Checkpoint artifacts saved", "job_id", jobID, "best_path", bestPath, "diff_path", diffPath)
	return nil
}

// monitorTrace periodically logs cost history to trace file
func monitorTrace(ctx context.Context, jm *JobManager, traceWriter *store.TraceWriter, jobID string, done chan struct{}) {
	// Log trace every 1 second
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastIteration := 0

	for {
		select {
		case <-done:
			// Log final state before exiting
			job, exists := jm.GetJob(jobID)
			if exists && job.Iterations > lastIteration {
				traceWriter.Write(store.TraceEntry{
					Iteration: job.Iterations,
					Cost:      job.BestCost,
					Timestamp: time.Now(),
				})
				traceWriter.Flush()
			}
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			job, exists := jm.GetJob(jobID)
			if !exists {
				return
			}

			// Only log if iteration has progressed
			if job.Iterations > lastIteration {
				entry := store.TraceEntry{
					Iteration: job.Iterations,
					Cost:      job.BestCost,
					Timestamp: time.Now(),
					// Don't include params to save space - can be reconstructed from checkpoints
					Params: nil,
				}

				if err := traceWriter.Write(entry); err != nil {
					slog.Error("Failed to write trace entry", "job_id", jobID, "error", err)
				}

				lastIteration = job.Iterations
			}
		}
	}
}
