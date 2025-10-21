package server

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"os"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// runJob executes an optimization job in the background
func runJob(ctx context.Context, jm *JobManager, jobID string) error {
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

	// Get initial cost
	initialParams := make([]float64, job.Config.Circles*7)
	initialCost := renderer.Cost(initialParams)

	jm.UpdateJob(jobID, func(j *Job) {
		j.InitialCost = initialCost
	})

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

	switch job.Config.Mode {
	case "joint":
		result = fit.OptimizeJoint(renderer, optimizer, job.Config.Circles)
	case "sequential":
		result = fit.OptimizeSequential(renderer, optimizer, job.Config.Circles)
	case "batch":
		batchSize := 5
		passes := job.Config.Circles / batchSize
		if job.Config.Circles%batchSize != 0 {
			passes++
		}
		result = fit.OptimizeBatch(renderer, optimizer, batchSize, passes)
	default:
		err := fmt.Errorf("unknown mode: %s", job.Config.Mode)
		markJobFailed(jm, jobID, err)
		return err
	}

	elapsed := time.Since(start)

	// Check for cancellation after optimization
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

	return nil
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
