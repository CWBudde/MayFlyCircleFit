package cmd

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
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
		return runResumeLocal(jobID)
	}
	return runResumeServer(jobID)
}

// runResumeServer sends a resume request to the server
func runResumeServer(jobID string) error {
	url := fmt.Sprintf("%s/api/v1/jobs/%s/resume", resumeServerURL, jobID)

	slog.Info("Resuming job via server", "job_id", jobID, "url", url)

	// Send POST request
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("checkpoint not found for job %s", jobID)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	// Parse response
	var result struct {
		JobID   string  `json:"jobId"`
		State   string  `json:"state"`
		Message string  `json:"message,omitempty"`
		Cost    float64 `json:"cost,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Printf("✓ Job resumed successfully\n")
	fmt.Printf("  Job ID: %s\n", result.JobID)
	fmt.Printf("  State: %s\n", result.State)
	if result.Message != "" {
		fmt.Printf("  Message: %s\n", result.Message)
	}
	fmt.Printf("\nUse 'mayflycirclefit status %s' to monitor progress\n", result.JobID)

	return nil
}

// runResumeLocal loads checkpoint and runs optimization locally
func runResumeLocal(jobID string) error {
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

	// Create renderer
	renderer := fit.NewCPURenderer(ref, checkpoint.Config.Circles)

	// Create optimizer
	optimizer := opt.NewMayfly(checkpoint.Config.Iters, checkpoint.Config.PopSize, checkpoint.Config.Seed)

	// Check if optimizer supports resume
	resumable, ok := optimizer.(opt.ResumableOptimizer)
	if !ok {
		return fmt.Errorf("optimizer does not support resume")
	}

	// Resume optimization
	fmt.Printf("Resuming optimization...\n")
	start := time.Now()

	var bestParams []float64
	var bestCost float64

	switch checkpoint.Config.Mode {
	case "joint":
		lower, upper := renderer.Bounds()
		bestParams, bestCost = resumable.RunWithInitial(
			checkpoint.BestParams,
			checkpoint.BestCost,
			renderer.Cost,
			lower,
			upper,
			renderer.Dim(),
		)
	case "sequential", "batch":
		return fmt.Errorf("resume not yet supported for mode: %s", checkpoint.Config.Mode)
	default:
		return fmt.Errorf("unknown mode: %s", checkpoint.Config.Mode)
	}

	elapsed := time.Since(start)

	// Display results
	fmt.Printf("\n✓ Optimization completed in %s\n", elapsed)
	fmt.Printf("  Previous cost: %f\n", checkpoint.BestCost)
	fmt.Printf("  New cost: %f\n", bestCost)
	improvement := ((checkpoint.BestCost - bestCost) / checkpoint.BestCost) * 100
	if improvement > 0 {
		fmt.Printf("  Improvement: %.2f%%\n", improvement)
	} else if improvement < 0 {
		fmt.Printf("  No improvement (checkpoint preserved)\n")
	} else {
		fmt.Printf("  Cost unchanged\n")
	}

	// Compute throughput
	totalEvals := checkpoint.Config.Iters * checkpoint.Config.PopSize
	totalCircles := totalEvals * checkpoint.Config.Circles
	cps := float64(totalCircles) / elapsed.Seconds()
	fmt.Printf("  Throughput: %.0f circles/sec\n", cps)

	// Create output directory
	if err := os.MkdirAll(resumeOutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Render and save best image
	bestImg := renderer.Render(bestParams)
	bestPath := filepath.Join(resumeOutputDir, fmt.Sprintf("%s_resumed.png", jobID))
	if err := saveImage(bestImg, bestPath); err != nil {
		return fmt.Errorf("failed to save output image: %w", err)
	}

	fmt.Printf("\n✓ Output saved to: %s\n", bestPath)

	// Update checkpoint
	updatedCheckpoint := store.NewCheckpoint(
		jobID,
		bestParams,
		bestCost,
		checkpoint.InitialCost,
		checkpoint.Iteration+checkpoint.Config.Iters, // Cumulative iterations
		checkpoint.Config,
	)

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
