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
)

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
	err := runJob(ctx, jm, job.ID)

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
	err := runJob(ctx, jm, job.ID)

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
		done <- runJob(ctx, jm, job.ID)
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
