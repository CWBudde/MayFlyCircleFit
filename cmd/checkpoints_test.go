package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/store"
)

func TestSelectCheckpointsForDeletion_ByAge(t *testing.T) {
	t.Parallel()

	now := time.Now()
	infos := []store.CheckpointInfo{
		{JobID: "job1", Timestamp: now.AddDate(0, 0, -10)}, // 10 days old
		{JobID: "job2", Timestamp: now.AddDate(0, 0, -5)},  // 5 days old
		{JobID: "job3", Timestamp: now.AddDate(0, 0, -1)},  // 1 day old
		{JobID: "job4", Timestamp: now.AddDate(0, 0, -30)}, // 30 days old
	}

	// Delete checkpoints older than 7 days
	toDelete := selectCheckpointsForDeletion(infos, 0, 7)

	if len(toDelete) != 2 {
		t.Errorf("Expected 2 checkpoints to delete, got %d", len(toDelete))
	}

	// Verify correct checkpoints selected
	found10 := false
	found30 := false

	for _, info := range toDelete {
		if info.JobID == "job1" {
			found10 = true
		}

		if info.JobID == "job4" {
			found30 = true
		}
	}

	if !found10 || !found30 {
		t.Error("Expected job1 and job4 to be selected for deletion")
	}
}

func TestSelectCheckpointsForDeletion_ByCount(t *testing.T) {
	t.Parallel()

	now := time.Now()
	infos := []store.CheckpointInfo{
		{JobID: "job1", Timestamp: now.AddDate(0, 0, -10)},
		{JobID: "job2", Timestamp: now.AddDate(0, 0, -5)},
		{JobID: "job3", Timestamp: now.AddDate(0, 0, -1)},
		{JobID: "job4", Timestamp: now.AddDate(0, 0, -30)},
	}

	// Keep only last 2 checkpoints
	toDelete := selectCheckpointsForDeletion(infos, 2, 0)

	if len(toDelete) != 2 {
		t.Errorf("Expected 2 checkpoints to delete, got %d", len(toDelete))
	}

	// Should delete oldest two (job4 and job1)
	found30 := false
	found10 := false

	for _, info := range toDelete {
		if info.JobID == "job4" {
			found30 = true
		}

		if info.JobID == "job1" {
			found10 = true
		}
	}

	if !found30 || !found10 {
		t.Error("Expected job4 and job1 to be selected for deletion (oldest)")
	}
}

func TestSelectCheckpointsForDeletion_Combined(t *testing.T) {
	t.Parallel()

	now := time.Now()
	infos := []store.CheckpointInfo{
		{JobID: "job1", Timestamp: now.AddDate(0, 0, -10)},
		{JobID: "job2", Timestamp: now.AddDate(0, 0, -5)},
		{JobID: "job3", Timestamp: now.AddDate(0, 0, -1)},
		{JobID: "job4", Timestamp: now.AddDate(0, 0, -30)},
		{JobID: "job5", Timestamp: now.AddDate(0, 0, -2)},
	}

	// Delete older than 7 days AND keep only last 3
	toDelete := selectCheckpointsForDeletion(infos, 3, 7)

	// Should delete job4 (30 days old) and job1 (10 days old) due to age
	// Should also delete 2 oldest to keep only 3: job4 and job1 are already in list
	// So total should be job4 and job1
	if len(toDelete) < 2 {
		t.Errorf("Expected at least 2 checkpoints to delete, got %d", len(toDelete))
	}
}

func TestGetDirSize(t *testing.T) {
	t.Parallel()

	// Create temp directory with files
	tmpDir := t.TempDir()

	// Create a file
	testFile := filepath.Join(tmpDir, "test.txt")

	content := []byte("Hello, World!")

	err := os.WriteFile(testFile, content, 0o600)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Get size
	size, err := getDirSize(tmpDir)
	if err != nil {
		t.Fatalf("getDirSize failed: %v", err)
	}

	if size < int64(len(content)) {
		t.Errorf("Expected size >= %d, got %d", len(content), size)
	}
}

func TestFormatBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		result := formatBytes(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatBytes(%d) = %s, expected %s", tt.bytes, result, tt.expected)
		}
	}
}

//nolint:paralleltest // mutates the package-level checkpoint flags, which every test in this package shares.
func TestCheckpointsListCommand_NoCheckpoints(t *testing.T) {
	// Create temp directory for checkpoints
	tmpDir := t.TempDir()

	// Set data dir
	originalDataDir := checkpointDataDir

	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Run list command
	var output bytes.Buffer

	err := runListCheckpoints(testCommand(context.Background(), &output), nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !strings.Contains(output.String(), "No checkpoints found.") {
		t.Errorf("unexpected output:\n%s", output.String())
	}
}

//nolint:paralleltest // mutates the package-level checkpoint flags, which every test in this package shares.
func TestCheckpointsListCommand_WithCheckpoints(t *testing.T) {
	// Create temp directory for checkpoints
	tmpDir := t.TempDir()

	// Create store and add checkpoint
	checkpointStore, err := store.NewFSStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create test checkpoint
	config := store.JobConfig{
		RefPath: "test.png",
		Mode:    "joint",
		Circles: 5,
		Iters:   100,
		PopSize: 30,
	}
	const jobID = "1f953c26-a9c8-4a53-9dd6-8f8459727010"
	checkpoint := store.NewCheckpoint(jobID, make([]float64, 35), 0.5, 1.0, 10, config)
	checkpoint.Termination = "stagnation"

	err = checkpointStore.SaveCheckpoint(jobID, checkpoint)
	if err != nil {
		t.Fatalf("Failed to save checkpoint: %v", err)
	}

	// Set data dir
	originalDataDir := checkpointDataDir

	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Run list command
	var output bytes.Buffer

	err = runListCheckpoints(testCommand(context.Background(), &output), nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	// The termination column reports why each checkpointed run stopped.
	if !strings.Contains(output.String(), "TERMINATION") || !strings.Contains(output.String(), "stagnation") {
		t.Errorf("checkpoint listing is missing the termination column:\n%s", output.String())
	}
}

//nolint:paralleltest // mutates the package-level checkpoint flags, which every test in this package shares.
func TestCheckpointsCleanCommand_NoFlags(t *testing.T) {
	tmpDir := t.TempDir()

	originalDataDir := checkpointDataDir

	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Reset flags, restoring them so the values do not leak into later tests.
	originalKeepLast, originalOlderThan := keepLast, olderThanDays
	defer func() { keepLast, olderThanDays = originalKeepLast, originalOlderThan }()

	keepLast = 0
	olderThanDays = 0

	// Should return error when no flags specified
	err := runCleanCheckpoints(nil, nil)
	if err == nil {
		t.Error("Expected error when no flags specified")
	}
}

//nolint:paralleltest // mutates the package-level checkpoint flags, which every test in this package shares.
func TestCheckpointsCleanCommand_WithForce(t *testing.T) {
	tmpDir := t.TempDir()

	// Create store and add old checkpoint
	checkpointStore, err := store.NewFSStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	config := store.JobConfig{
		RefPath: "test.png",
		Mode:    "joint",
		Circles: 5,
		Iters:   100,
		PopSize: 30,
	}
	const jobID = "7287daeb-7196-446c-832e-616ea3d3f615"
	checkpoint := store.NewCheckpoint(jobID, make([]float64, 35), 0.5, 1.0, 10, config)

	// Manually set timestamp to be old
	checkpoint.Timestamp = time.Now().AddDate(0, 0, -30)

	err = checkpointStore.SaveCheckpoint(jobID, checkpoint)
	if err != nil {
		t.Fatalf("Failed to save checkpoint: %v", err)
	}

	originalDataDir := checkpointDataDir

	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Set flags. They are package-level variables, so they must be restored:
	// leaving forceClean set would let a later test delete real checkpoints
	// without the confirmation prompt.
	originalKeepLast, originalOlderThan, originalForce := keepLast, olderThanDays, forceClean
	defer func() {
		keepLast, olderThanDays, forceClean = originalKeepLast, originalOlderThan, originalForce
	}()

	keepLast = 0
	olderThanDays = 7
	forceClean = true

	// Run clean command
	err = runCleanCheckpoints(nil, nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify checkpoint was deleted
	_, err = checkpointStore.LoadCheckpoint("old-job")
	if err == nil {
		t.Error("Expected checkpoint to be deleted")
	}
}
