package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

)

// setupTestStore creates a temporary directory and returns an FSStore for testing.
func setupTestStore(t *testing.T) (*FSStore, string) {
	t.Helper()

	tempDir := t.TempDir() // Automatically cleaned up after test
	store, err := NewFSStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	return store, tempDir
}

// createTestCheckpoint creates a checkpoint with test data.
func createTestCheckpoint(jobID string) *Checkpoint {
	return &Checkpoint{
		JobID:       jobID,
		BestParams:  []float64{100.5, 50.2, 25.0, 0.8, 0.2, 0.1, 0.9},
		BestCost:    0.0234,
		InitialCost: 0.5621,
		Iteration:   500,
		Timestamp:   time.Now(),
		Config: JobConfig{
			RefPath: "assets/test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   1000,
			PopSize: 30,
			Seed:    42,
		},
	}
}

func TestNewFSStore(t *testing.T) {
	tempDir := t.TempDir()

	store, err := NewFSStore(tempDir)
	if err != nil {
		t.Fatalf("NewFSStore failed: %v", err)
	}

	if store == nil {
		t.Fatal("Expected non-nil store")
	}

	// Verify base directory was created
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Fatal("Base directory was not created")
	}
}

func TestSaveCheckpoint(t *testing.T) {
	store, tempDir := setupTestStore(t)

	jobID := "test-job-123"
	checkpoint := createTestCheckpoint(jobID)

	// Save checkpoint
	err := store.SaveCheckpoint(jobID, checkpoint)
	if err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	// Verify checkpoint file exists
	expectedPath := filepath.Join(tempDir, "jobs", jobID, "checkpoint.json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("Checkpoint file was not created at %s", expectedPath)
	}

	// Verify no temp file remains
	tempPath := expectedPath + ".tmp"
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("Temp file should not exist after save: %s", tempPath)
	}
}

func TestSaveCheckpoint_EmptyJobID(t *testing.T) {
	store, _ := setupTestStore(t)
	checkpoint := createTestCheckpoint("any-id")

	err := store.SaveCheckpoint("", checkpoint)
	if err == nil {
		t.Fatal("Expected error for empty jobID")
	}
}

func TestSaveCheckpoint_NilCheckpoint(t *testing.T) {
	store, _ := setupTestStore(t)

	err := store.SaveCheckpoint("test-job", nil)
	if err == nil {
		t.Fatal("Expected error for nil checkpoint")
	}
}

func TestSaveCheckpoint_Overwrite(t *testing.T) {
	store, _ := setupTestStore(t)

	jobID := "test-job-overwrite"
	checkpoint1 := createTestCheckpoint(jobID)
	checkpoint1.BestCost = 0.5

	checkpoint2 := createTestCheckpoint(jobID)
	checkpoint2.BestCost = 0.1

	// Save first checkpoint
	if err := store.SaveCheckpoint(jobID, checkpoint1); err != nil {
		t.Fatalf("First save failed: %v", err)
	}

	// Overwrite with second checkpoint
	if err := store.SaveCheckpoint(jobID, checkpoint2); err != nil {
		t.Fatalf("Second save failed: %v", err)
	}

	// Load and verify it's the second checkpoint
	loaded, err := store.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.BestCost != 0.1 {
		t.Errorf("Expected BestCost=0.1, got %f", loaded.BestCost)
	}
}

func TestLoadCheckpoint(t *testing.T) {
	store, _ := setupTestStore(t)

	jobID := "test-job-load"
	original := createTestCheckpoint(jobID)

	// Save checkpoint
	if err := store.SaveCheckpoint(jobID, original); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	// Load checkpoint
	loaded, err := store.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	// Verify loaded checkpoint matches original
	if loaded.JobID != original.JobID {
		t.Errorf("JobID mismatch: expected %s, got %s", original.JobID, loaded.JobID)
	}
	if loaded.BestCost != original.BestCost {
		t.Errorf("BestCost mismatch: expected %f, got %f", original.BestCost, loaded.BestCost)
	}
	if loaded.Iteration != original.Iteration {
		t.Errorf("Iteration mismatch: expected %d, got %d", original.Iteration, loaded.Iteration)
	}
	if len(loaded.BestParams) != len(original.BestParams) {
		t.Errorf("BestParams length mismatch: expected %d, got %d", len(original.BestParams), len(loaded.BestParams))
	}
	if loaded.Config.Mode != original.Config.Mode {
		t.Errorf("Config.Mode mismatch: expected %s, got %s", original.Config.Mode, loaded.Config.Mode)
	}
}

func TestLoadCheckpoint_NotFound(t *testing.T) {
	store, _ := setupTestStore(t)

	_, err := store.LoadCheckpoint("nonexistent-job")
	if err == nil {
		t.Fatal("Expected error for nonexistent checkpoint")
	}

	var notFoundErr *NotFoundError
	if !isErrorType(err, &notFoundErr) {
		t.Errorf("Expected NotFoundError, got %T: %v", err, err)
	}
}

func TestLoadCheckpoint_EmptyJobID(t *testing.T) {
	store, _ := setupTestStore(t)

	_, err := store.LoadCheckpoint("")
	if err == nil {
		t.Fatal("Expected error for empty jobID")
	}
}

func TestListCheckpoints_Empty(t *testing.T) {
	store, _ := setupTestStore(t)

	infos, err := store.ListCheckpoints()
	if err != nil {
		t.Fatalf("ListCheckpoints failed: %v", err)
	}

	if len(infos) != 0 {
		t.Errorf("Expected empty list, got %d checkpoints", len(infos))
	}
}

func TestListCheckpoints_Multiple(t *testing.T) {
	store, _ := setupTestStore(t)

	// Create multiple checkpoints
	jobs := []string{"job-1", "job-2", "job-3"}
	for _, jobID := range jobs {
		checkpoint := createTestCheckpoint(jobID)
		if err := store.SaveCheckpoint(jobID, checkpoint); err != nil {
			t.Fatalf("Failed to save checkpoint %s: %v", jobID, err)
		}
	}

	// List checkpoints
	infos, err := store.ListCheckpoints()
	if err != nil {
		t.Fatalf("ListCheckpoints failed: %v", err)
	}

	if len(infos) != len(jobs) {
		t.Errorf("Expected %d checkpoints, got %d", len(jobs), len(infos))
	}

	// Verify all job IDs are present
	foundJobs := make(map[string]bool)
	for _, info := range infos {
		foundJobs[info.JobID] = true
	}

	for _, jobID := range jobs {
		if !foundJobs[jobID] {
			t.Errorf("Job %s not found in list", jobID)
		}
	}
}

func TestListCheckpoints_SkipsInvalidDirectories(t *testing.T) {
	store, tempDir := setupTestStore(t)

	// Create valid checkpoint
	validJobID := "valid-job"
	checkpoint := createTestCheckpoint(validJobID)
	if err := store.SaveCheckpoint(validJobID, checkpoint); err != nil {
		t.Fatalf("Failed to save valid checkpoint: %v", err)
	}

	// Create directory without checkpoint.json
	invalidJobDir := filepath.Join(tempDir, "jobs", "invalid-job")
	if err := os.MkdirAll(invalidJobDir, 0755); err != nil {
		t.Fatalf("Failed to create invalid job directory: %v", err)
	}

	// Create non-directory file in jobs directory
	jobsDir := filepath.Join(tempDir, "jobs")
	dummyFile := filepath.Join(jobsDir, "dummy.txt")
	if err := os.WriteFile(dummyFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}

	// List should only return valid checkpoint
	infos, err := store.ListCheckpoints()
	if err != nil {
		t.Fatalf("ListCheckpoints failed: %v", err)
	}

	if len(infos) != 1 {
		t.Errorf("Expected 1 checkpoint, got %d", len(infos))
	}

	if len(infos) > 0 && infos[0].JobID != validJobID {
		t.Errorf("Expected jobID %s, got %s", validJobID, infos[0].JobID)
	}
}

func TestDeleteCheckpoint(t *testing.T) {
	store, _ := setupTestStore(t)

	jobID := "test-job-delete"
	checkpoint := createTestCheckpoint(jobID)

	// Save checkpoint
	if err := store.SaveCheckpoint(jobID, checkpoint); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	// Delete checkpoint
	err := store.DeleteCheckpoint(jobID)
	if err != nil {
		t.Fatalf("DeleteCheckpoint failed: %v", err)
	}

	// Verify checkpoint no longer exists
	_, err = store.LoadCheckpoint(jobID)
	if err == nil {
		t.Fatal("Expected error when loading deleted checkpoint")
	}

	var notFoundErr *NotFoundError
	if !isErrorType(err, &notFoundErr) {
		t.Errorf("Expected NotFoundError, got %T: %v", err, err)
	}
}

func TestDeleteCheckpoint_NotFound(t *testing.T) {
	store, _ := setupTestStore(t)

	err := store.DeleteCheckpoint("nonexistent-job")
	if err == nil {
		t.Fatal("Expected error for nonexistent checkpoint")
	}

	var notFoundErr *NotFoundError
	if !isErrorType(err, &notFoundErr) {
		t.Errorf("Expected NotFoundError, got %T: %v", err, err)
	}
}

func TestDeleteCheckpoint_EmptyJobID(t *testing.T) {
	store, _ := setupTestStore(t)

	err := store.DeleteCheckpoint("")
	if err == nil {
		t.Fatal("Expected error for empty jobID")
	}
}

func TestCheckpointToInfo(t *testing.T) {
	checkpoint := createTestCheckpoint("test-job")

	info := checkpoint.ToInfo()

	if info.JobID != checkpoint.JobID {
		t.Errorf("JobID mismatch: expected %s, got %s", checkpoint.JobID, info.JobID)
	}
	if info.BestCost != checkpoint.BestCost {
		t.Errorf("BestCost mismatch: expected %f, got %f", checkpoint.BestCost, info.BestCost)
	}
	if info.Iteration != checkpoint.Iteration {
		t.Errorf("Iteration mismatch: expected %d, got %d", checkpoint.Iteration, info.Iteration)
	}
	if info.Mode != checkpoint.Config.Mode {
		t.Errorf("Mode mismatch: expected %s, got %s", checkpoint.Config.Mode, info.Mode)
	}
	if info.Circles != checkpoint.Config.Circles {
		t.Errorf("Circles mismatch: expected %d, got %d", checkpoint.Config.Circles, info.Circles)
	}
}

func TestConcurrentSave(t *testing.T) {
	store, _ := setupTestStore(t)

	// Save multiple checkpoints concurrently
	const numJobs = 10
	done := make(chan bool, numJobs)

	for i := 0; i < numJobs; i++ {
		go func(idx int) {
			jobID := fmt.Sprintf("concurrent-job-%d", idx)
			checkpoint := createTestCheckpoint(jobID)
			if err := store.SaveCheckpoint(jobID, checkpoint); err != nil {
				t.Errorf("Concurrent save failed for job %s: %v", jobID, err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numJobs; i++ {
		<-done
	}

	// Verify all checkpoints were saved
	infos, err := store.ListCheckpoints()
	if err != nil {
		t.Fatalf("ListCheckpoints failed: %v", err)
	}

	if len(infos) != numJobs {
		t.Errorf("Expected %d checkpoints, got %d", numJobs, len(infos))
	}
}

// Helper function to check error type (workaround for errors.As in tests)
func isErrorType(err error, target interface{}) bool {
	if err == nil {
		return false
	}
	// Simple type check for NotFoundError
	_, ok := err.(*NotFoundError)
	return ok
}
