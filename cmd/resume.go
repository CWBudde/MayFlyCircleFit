package cmd

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/spf13/cobra"
)

var (
	resumeServerURL string
	resumeLocalMode bool
	resumeOutputDir string
)

var resumeCmd = &cobra.Command{
	Use:   "resume [job-id]",
	Short: "Resume optimization from a checkpoint",
	Long: `Resume an optimization job from a saved checkpoint.

Supports two modes:
  1. Server mode (default): POST to server's resume endpoint
  2. Local mode (--local): Load checkpoint and run optimizer locally

Examples:
  # Resume via server
  mayflycirclefit resume abc123 --server-url http://localhost:8080

  # Resume locally
  mayflycirclefit resume abc123 --local --output ./results`,
	Args: cobra.ExactArgs(1),
	RunE: runResume,
}

func init() {
	resumeCmd.Flags().StringVar(&resumeServerURL, "server-url", "http://localhost:8080", "Server URL for remote resume")
	resumeCmd.Flags().BoolVar(&resumeLocalMode, "local", false, "Run resume locally instead of via server")
	resumeCmd.Flags().StringVar(&resumeOutputDir, "output", "./resumed", "Output directory for local mode")
	rootCmd.AddCommand(resumeCmd)
}

func runResume(cmd *cobra.Command, args []string) error {
	jobID := args[0]

	if resumeLocalMode {
		return runResumeLocal(cmd.Context(), jobID)
	}
	return runResumeServer(cmd.Context(), cmd.OutOrStdout(), jobID)
}

// runResumeServer sends a resume request to the server
func runResumeServer(ctx context.Context, output io.Writer, jobID string) error {
	endpoint := fmt.Sprintf("%s/api/v1/jobs/%s/resume", strings.TrimRight(resumeServerURL, "/"), url.PathEscape(jobID))

	slog.Info("Resuming job via server", "job_id", jobID, "url", endpoint)

	body, err := requestCLI(ctx, http.MethodPost, endpoint)
	if err != nil {
		var apiErr *cliAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("checkpoint not found for job %q: %w", jobID, err)
		}
		return fmt.Errorf("resume job %q: %w", jobID, err)
	}

	var result struct {
		JobID         string   `json:"jobId"`
		ResumedFrom   string   `json:"resumedFrom"`
		State         string   `json:"state"`
		PreviousCost  *float64 `json:"previousCost"`
		PreviousIters *int     `json:"previousIters"`
		Message       string   `json:"message,omitempty"`
	}
	if err := decodeCLIResponse(body, &result); err != nil {
		return fmt.Errorf("decode resume response: %w", err)
	}
	if strings.TrimSpace(result.JobID) == "" || result.ResumedFrom != jobID {
		return fmt.Errorf("invalid resume response: mismatched or missing job identifiers")
	}
	if result.State != "pending" && result.State != "running" {
		return fmt.Errorf("invalid resume response: unexpected state %q", result.State)
	}
	if result.PreviousCost == nil || result.PreviousIters == nil ||
		!finiteNonnegative(*result.PreviousCost) || *result.PreviousIters < 0 {
		return fmt.Errorf("invalid resume response: invalid checkpoint progress")
	}

	fmt.Fprintln(output, "✓ Job resumed successfully")
	fmt.Fprintf(output, "  Job ID: %s\n", result.JobID)
	fmt.Fprintf(output, "  State: %s\n", result.State)
	if result.Message != "" {
		fmt.Fprintf(output, "  Message: %s\n", result.Message)
	}
	fmt.Fprintf(output, "\nUse 'mayflycirclefit status %s' to monitor progress\n", result.JobID)

	return nil
}

// runResumeLocal loads checkpoint and runs optimization locally
func runResumeLocal(ctx context.Context, jobID string) error {
	slog.Info("Resuming job locally", "job_id", jobID)

	// Load checkpoint
	checkpointStore, err := store.NewFSStore("./data")
	if err != nil {
		return fmt.Errorf("failed to create checkpoint store: %w", err)
	}

	checkpoint, err := checkpointStore.LoadCheckpoint(jobID)
	if err != nil {
		return fmt.Errorf("failed to load checkpoint: %w", err)
	}

	// Validate checkpoint
	if err := checkpoint.Validate(); err != nil {
		return fmt.Errorf("invalid checkpoint: %w", err)
	}

	fmt.Printf("Loaded checkpoint:\n")
	fmt.Printf("  Job ID: %s\n", checkpoint.JobID)
	fmt.Printf("  Iteration: %d\n", checkpoint.Iteration)
	fmt.Printf("  Best cost: %f\n", checkpoint.BestCost)
	fmt.Printf("  Mode: %s\n", checkpoint.Config.Mode)
	fmt.Printf("  Circles: %d\n", checkpoint.Config.Circles)
	fmt.Printf("  Checkpoint time: %s\n\n", checkpoint.Timestamp.Format(time.RFC3339))

	// Load reference image
	f, err := os.Open(checkpoint.Config.RefPath)
	if err != nil {
		return fmt.Errorf("failed to open reference: %w", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// Convert to NRGBA
	bounds := img.Bounds()
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, img.At(x, y))
		}
	}

	var rend renderer.Renderer
	cleanup := func() {}
	if checkpoint.Config.CanvasPath != "" {
		canvasFile, err := os.Open(checkpoint.Config.CanvasPath)
		if err != nil {
			return fmt.Errorf("failed to open canvas: %w", err)
		}
		defer canvasFile.Close()
		canvasImage, _, err := image.Decode(canvasFile)
		if err != nil {
			return fmt.Errorf("failed to decode canvas: %w", err)
		}
		if canvasImage.Bounds().Dx() != bounds.Dx() || canvasImage.Bounds().Dy() != bounds.Dy() {
			return fmt.Errorf("canvas dimensions do not match reference")
		}
		canvas := image.NewNRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				canvas.Set(x, y, canvasImage.At(x, y))
			}
		}
		cpu := renderer.NewCPURendererWithCanvas(ref, canvas, checkpoint.Config.Circles)
		cpu.SetThreads(checkpoint.Config.Threads)
		rend = cpu
	} else {
		backend := checkpoint.Config.Backend
		if backend == "" {
			backend = "cpu"
		}
		if backend == "cpu" {
			cpu := renderer.NewCPURenderer(ref, checkpoint.Config.Circles)
			cpu.SetThreads(checkpoint.Config.Threads)
			rend = cpu
		} else {
			rend, cleanup, err = renderer.NewRendererForBackend(string(backend), ref, checkpoint.Config.Circles)
			if err != nil {
				return err
			}
		}
	}
	defer cleanup()

	seed := checkpoint.EffectiveSeed
	if seed == 0 {
		seed = checkpoint.Config.Seed
	}
	optimizer := opt.NewMayfly(checkpoint.Config.Iters, checkpoint.Config.PopSize, seed)
	lifecycle, ok := optimizer.(opt.LifecycleOptimizer)
	if !ok {
		return fmt.Errorf("optimizer does not support lifecycle resume")
	}

	// Resume optimization
	fmt.Printf("Resuming optimization...\n")
	start := time.Now()

	var optimization opt.Result

	switch checkpoint.Config.Mode {
	case "joint":
		lower, upper := rend.Bounds()
		optimization, err = lifecycle.RunContext(ctx, opt.Problem{
			Eval: rend.Cost, Lower: lower, Upper: upper, Dim: rend.Dim(),
		}, opt.RunOptions{
			Initial:     &opt.Candidate{Params: checkpoint.BestParams, Cost: checkpoint.BestCost},
			ResumeCount: checkpoint.ResumeCount + 1,
		})
		if err != nil {
			return fmt.Errorf("resume optimization: %w", err)
		}
	case "sequential", "batch":
		return fmt.Errorf("resume not yet supported for mode: %s", checkpoint.Config.Mode)
	default:
		return fmt.Errorf("unknown mode: %s", checkpoint.Config.Mode)
	}

	elapsed := time.Since(start)
	bestParams, bestCost := optimization.BestParams, optimization.BestCost

	// Display results
	fmt.Printf("\n✓ Optimization completed in %s\n", elapsed)
	fmt.Printf("  Previous cost: %f\n", checkpoint.BestCost)
	fmt.Printf("  New cost: %f\n", bestCost)
	improvement := 0.0
	if checkpoint.BestCost > 0 {
		improvement = (checkpoint.BestCost - bestCost) / checkpoint.BestCost * 100
	}
	if improvement > 0 {
		fmt.Printf("  Improvement: %.2f%%\n", improvement)
	} else if improvement < 0 {
		fmt.Printf("  No improvement (checkpoint preserved)\n")
	} else {
		fmt.Printf("  Cost unchanged\n")
	}

	// Compute throughput
	totalCircles := optimization.Evaluations * checkpoint.Config.Circles
	cps := float64(totalCircles) / elapsed.Seconds()
	fmt.Printf("  Throughput: %.0f circles/sec\n", cps)

	// Create output directory
	if err := os.MkdirAll(resumeOutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Render and save best image
	bestImg := rend.Render(bestParams)
	bestPath := filepath.Join(resumeOutputDir, fmt.Sprintf("%s_resumed.png", jobID))
	if err := saveImage(bestImg, bestPath); err != nil {
		return fmt.Errorf("failed to save output image: %w", err)
	}

	fmt.Printf("\n✓ Output saved to: %s\n", bestPath)

	// Update checkpoint
	updatedConfig := checkpoint.Config
	updatedConfig.EffectiveSeed = seed
	updatedConfig.ResumeCount = checkpoint.ResumeCount + 1
	updatedCheckpoint := store.NewCheckpoint(
		jobID,
		bestParams,
		bestCost,
		checkpoint.InitialCost,
		checkpoint.Iterations+optimization.Iterations,
		updatedConfig,
	)
	updatedCheckpoint.Evaluations = checkpoint.Evaluations + int64(optimization.Evaluations)
	updatedCheckpoint.Termination = string(optimization.Termination)

	if err := checkpointStore.SaveCheckpoint(jobID, updatedCheckpoint); err != nil {
		slog.Warn("Failed to update checkpoint", "error", err)
	} else {
		fmt.Printf("✓ Checkpoint updated\n")
	}

	return nil
}

// Helper to save image
func saveImage(img image.Image, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return png.Encode(file, img)
}
