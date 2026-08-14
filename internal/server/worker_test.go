package server

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

func TestRendererForJobConfiguresThreads(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	rend, cleanup, err := rendererForJob(JobConfig{Backend: "cpu", Threads: 1}, ref, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	cpu, ok := rend.(*renderer.CPURenderer)
	if !ok {
		t.Fatalf("renderer type = %T, want *renderer.CPURenderer", rend)
	}
	if cpu.Threads() != 1 {
		t.Fatalf("renderer threads = %d, want 1", cpu.Threads())
	}
}

func TestRunJob_Success(t *testing.T) {
	// Create temporary test image
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)

	jm := NewJobManager()
	config := JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 20,
		Seed:    42,
	}

	job := jm.CreateJob(config)

	ctx := context.Background()
	err := runJob(ctx, jm, nil, job.ID)

	if err != nil {
		t.Errorf("runJob should succeed: %v", err)
	}

	updated, _ := jm.GetJob(job.ID)
	if updated.State != StateCompleted {
		t.Errorf("Job should be completed, got %s", updated.State)
	}

	if updated.BestCost == 0 {
		t.Error("BestCost should be set")
	}

	if len(updated.BestParams) != 14 { // 2 circles * 7 params
		t.Errorf("Expected 14 params, got %d", len(updated.BestParams))
	}

	// Note: Iterations tracking will be added in a future enhancement
	// For now, just verify the job completed successfully
}

func TestRunJobRecordsPSNRAndOptionalSSIM(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)
	persistence, err := store.NewFSStore(filepath.Join(tmpDir, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	jm := NewJobManager()
	job := jm.CreateJob(JobConfig{
		RefPath: imgPath, Mode: "joint", Circles: 2, Iters: 5, PopSize: 20, Seed: 42,
		EnableTrace: true, EnableSSIM: true,
	})
	if err := runJob(context.Background(), jm, persistence, job.ID); err != nil {
		t.Fatal(err)
	}

	completed, _ := jm.GetJob(job.ID)
	if completed.PSNR == nil || completed.PSNRInfinite || completed.SSIM == nil {
		t.Fatalf("completed metrics unavailable: PSNR=%v infinite=%v SSIM=%v", completed.PSNR, completed.PSNRInfinite, completed.SSIM)
	}
	if len(completed.MetricHistory) < 2 || completed.MetricHistory[len(completed.MetricHistory)-1].SSIM == nil {
		t.Fatalf("metric history missing initial/final samples: %+v", completed.MetricHistory)
	}

	reader, err := persistence.NewTraceReader(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 || entries[0].PSNR == nil || entries[0].SSIM == nil {
		t.Fatalf("initial trace metrics unavailable: %+v", entries)
	}
	last := entries[len(entries)-1]
	if last.PSNR == nil || last.SSIM == nil {
		t.Fatalf("final trace metrics unavailable: %+v", last)
	}
}

func TestRunJob_InvalidImage(t *testing.T) {
	jm := NewJobManager()
	config := JobConfig{
		RefPath: "/nonexistent/image.png",
		Mode:    "joint",
		Circles: 2,
		Iters:   10,
		PopSize: 20,
		Seed:    42,
	}

	job := jm.CreateJob(config)

	ctx := context.Background()
	err := runJob(ctx, jm, nil, job.ID)

	if err == nil {
		t.Error("runJob should fail with invalid image path")
	}

	updated, _ := jm.GetJob(job.ID)
	if updated.State != StateFailed {
		t.Errorf("Job should be failed, got %s", updated.State)
	}

	if updated.Error == "" {
		t.Error("Error message should be set")
	}
}

func TestRunJob_Cancellation(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestImage(t, imgPath)

	jm := NewJobManager()
	config := JobConfig{
		RefPath: imgPath,
		Mode:    "joint",
		Circles: 5,
		Iters:   1000, // Long-running job
		PopSize: 30,
		Seed:    42,
	}

	job := jm.CreateJob(config)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- runJob(ctx, jm, nil, job.ID)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the job
	cancel()

	// Wait for completion
	err := <-done

	if err == nil {
		t.Error("runJob should return error when cancelled")
	}

	updated, _ := jm.GetJob(job.ID)
	// State could be running or cancelled depending on timing
	if updated.State != StateRunning && updated.State != StateCancelled {
		t.Errorf("Job should be running or cancelled, got %s", updated.State)
	}
}

// Helper function to create a simple test image
func createTestImage(t *testing.T, path string) {
	img := image.NewNRGBA(image.Rect(0, 0, 50, 50))
	white := color.NRGBA{255, 255, 255, 255}
	red := color.NRGBA{255, 0, 0, 255}

	// Fill with white
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			img.Set(x, y, white)
		}
	}

	// Add red square
	for y := 20; y < 30; y++ {
		for x := 20; x < 30; x++ {
			img.Set(x, y, red)
		}
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create test image: %v", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}
}

// terminationOptimizer returns a fixed termination reason so the wrapper's
// pass-through behavior can be checked without running a real optimizer.
type terminationOptimizer struct {
	reason opt.Termination
}

func (t terminationOptimizer) Run(_ func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	return make([]float64, dim), 0
}

type optionsCaptureOptimizer struct {
	options opt.RunOptions
}

func (o *optionsCaptureOptimizer) Run(_ func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	return make([]float64, dim), 0
}

func (o *optionsCaptureOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	o.options = options
	return opt.Result{BestParams: append([]float64(nil), options.Initial.Params...), BestCost: options.Initial.Cost}, nil
}

func (t terminationOptimizer) RunContext(_ context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	return opt.Result{
		BestParams:  make([]float64, problem.Dim),
		BestCost:    1,
		Iterations:  5,
		Evaluations: 9,
		Termination: t.reason,
	}, nil
}

// TestProgressOptimizerPreservesTermination pins the wrapper that rebuilds
// RunOptions: it must still return the base optimizer's termination reason,
// because the worker now reports that reason instead of a hardcoded value.
func TestProgressOptimizerPreservesTermination(t *testing.T) {
	reasons := []opt.Termination{
		opt.TerminationCompleted,
		opt.TerminationTargetCost,
		opt.TerminationStagnation,
	}

	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			wrapped := &progressOptimizer{base: terminationOptimizer{reason: reason}}
			result, err := wrapped.RunContext(context.Background(), opt.Problem{
				Eval:  func([]float64) float64 { return 1 },
				Lower: []float64{0},
				Upper: []float64{1},
				Dim:   1,
			}, opt.RunOptions{})
			if err != nil {
				t.Fatalf("RunContext() error = %v", err)
			}
			if result.Termination != reason {
				t.Fatalf("Termination = %q, want %q", result.Termination, reason)
			}
		})
	}
}

func TestProgressOptimizerForwardsPipelineInitialSeed(t *testing.T) {
	base := &optionsCaptureOptimizer{}
	wrapped := &progressOptimizer{base: base}
	initial := &opt.Candidate{Params: []float64{0.75}, Cost: 2}
	if _, err := wrapped.RunContext(context.Background(), opt.Problem{
		Eval: func([]float64) float64 { return 1 }, Lower: []float64{0}, Upper: []float64{1}, Dim: 1,
	}, opt.RunOptions{Initial: initial, ResumeCount: 3}); err != nil {
		t.Fatal(err)
	}
	if base.options.Initial != initial || base.options.ResumeCount != 3 {
		t.Fatalf("forwarded options = %+v, want initial seed and resume count 3", base.options)
	}
}
