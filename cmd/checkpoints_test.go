package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/store"
)

func TestSelectCheckpointsForDeletion_ByAge(t *testing.T) {
	now := time.Now()
	infos := []store.CheckpointInfo{
		{JobID: "job1", Timestamp: now.AddDate(0, 0, -10)},  // 10 days old
		{JobID: "job2", Timestamp: now.AddDate(0, 0, -5)},   // 5 days old
		{JobID: "job3", Timestamp: now.AddDate(0, 0, -1)},   // 1 day old
		{JobID: "job4", Timestamp: now.AddDate(0, 0, -30)},  // 30 days old
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
	// Create temp directory with files
	tmpDir := t.TempDir()

	// Create a file
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World!")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
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

func TestCheckpointsListCommand_NoCheckpoints(t *testing.T) {
	// Create temp directory for checkpoints
	tmpDir := t.TempDir()

	// Set data dir
	originalDataDir := checkpointDataDir
	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Run list command
	err := runListCheckpoints(nil, nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

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
	checkpoint := store.NewCheckpoint("test-job-id", []float64{1, 2, 3}, 0.5, 1.0, 10, config)

	err = checkpointStore.SaveCheckpoint("test-job-id", checkpoint)
	if err != nil {
		t.Fatalf("Failed to save checkpoint: %v", err)
	}

	// Set data dir
	originalDataDir := checkpointDataDir
	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Run list command
	err = runListCheckpoints(nil, nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestCheckpointsCleanCommand_NoFlags(t *testing.T) {
	tmpDir := t.TempDir()

	originalDataDir := checkpointDataDir
	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Reset flags
	keepLast = 0
	olderThanDays = 0

	// Should return error when no flags specified
	err := runCleanCheckpoints(nil, nil)
	if err == nil {
		t.Error("Expected error when no flags specified")
	}
}

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
	checkpoint := store.NewCheckpoint("old-job", []float64{1, 2, 3}, 0.5, 1.0, 10, config)

	// Manually set timestamp to be old
	checkpoint.Timestamp = time.Now().AddDate(0, 0, -30)

	err = checkpointStore.SaveCheckpoint("old-job", checkpoint)
	if err != nil {
		t.Fatalf("Failed to save checkpoint: %v", err)
	}

	originalDataDir := checkpointDataDir
	checkpointDataDir = tmpDir
	defer func() { checkpointDataDir = originalDataDir }()

	// Set flags
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
