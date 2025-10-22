package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// FSStore implements the Store interface using filesystem-based persistence.
// Checkpoints are stored in a directory structure: <baseDir>/jobs/<jobID>/
//
// Thread-safety: This implementation uses atomic file operations (rename)
// and does not require locks. Multiple goroutines can safely call methods
// concurrently.
type FSStore struct {
	baseDir string // Root directory for all checkpoint data (e.g., "./data")
}

// NewFSStore creates a new filesystem-based store.
// The baseDir will be created if it doesn't exist.
func NewFSStore(baseDir string) (*FSStore, error) {
	// Ensure base directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &FSStore{
		baseDir: baseDir,
	}, nil
}

// jobDir returns the directory path for a given job ID.
func (fs *FSStore) jobDir(jobID string) string {
	return filepath.Join(fs.baseDir, "jobs", jobID)
}

// checkpointPath returns the path to the checkpoint.json file for a job.
func (fs *FSStore) checkpointPath(jobID string) string {
	return filepath.Join(fs.jobDir(jobID), "checkpoint.json")
}

// SaveCheckpoint atomically saves a checkpoint for the given job.
// Uses temp file + rename pattern to ensure atomicity.
func (fs *FSStore) SaveCheckpoint(jobID string, checkpoint *Checkpoint) error {
	if jobID == "" {
		return fmt.Errorf("jobID cannot be empty")
	}
	if checkpoint == nil {
		return fmt.Errorf("checkpoint cannot be nil")
	}

	// Ensure job directory exists
	jobDir := fs.jobDir(jobID)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return fmt.Errorf("failed to create job directory: %w", err)
	}

	// Serialize checkpoint to JSON
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize checkpoint: %w", err)
	}

	// Write to temporary file first (atomic pattern)
	tempPath := fs.checkpointPath(jobID) + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp checkpoint file: %w", err)
	}

	// Atomic rename to final location
	finalPath := fs.checkpointPath(jobID)
	if err := os.Rename(tempPath, finalPath); err != nil {
		// Clean up temp file on failure
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename checkpoint file: %w", err)
	}

	slog.Debug("Checkpoint saved", "jobID", jobID, "path", finalPath)
	return nil
}

// LoadCheckpoint retrieves the checkpoint for the given job.
func (fs *FSStore) LoadCheckpoint(jobID string) (*Checkpoint, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID cannot be empty")
	}

	path := fs.checkpointPath(jobID)

	// Check if checkpoint exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, &NotFoundError{JobID: jobID}
	} else if err != nil {
		return nil, fmt.Errorf("failed to stat checkpoint file: %w", err)
	}

	// Read checkpoint file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint file: %w", err)
	}

	// Deserialize JSON
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, fmt.Errorf("failed to deserialize checkpoint: %w", err)
	}

	slog.Debug("Checkpoint loaded", "jobID", jobID, "path", path)
	return &checkpoint, nil
}

// ListCheckpoints returns metadata for all available checkpoints.
func (fs *FSStore) ListCheckpoints() ([]CheckpointInfo, error) {
	jobsDir := filepath.Join(fs.baseDir, "jobs")

	// Check if jobs directory exists
	if _, err := os.Stat(jobsDir); os.IsNotExist(err) {
		// No checkpoints exist yet, return empty slice
		return []CheckpointInfo{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to stat jobs directory: %w", err)
	}

	// Read all job directories
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read jobs directory: %w", err)
	}

	var infos []CheckpointInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue // Skip non-directory entries
		}

		jobID := entry.Name()
		checkpointPath := fs.checkpointPath(jobID)

		// Check if checkpoint.json exists
		if _, err := os.Stat(checkpointPath); os.IsNotExist(err) {
			continue // Skip directories without checkpoint.json
		}

		// Load full checkpoint to extract metadata
		checkpoint, err := fs.LoadCheckpoint(jobID)
		if err != nil {
			slog.Warn("Failed to load checkpoint for listing", "jobID", jobID, "error", err)
			continue // Skip corrupted checkpoints
		}

		infos = append(infos, checkpoint.ToInfo())
	}

	slog.Debug("Listed checkpoints", "count", len(infos))
	return infos, nil
}

// DeleteCheckpoint removes the checkpoint and all associated artifacts.
func (fs *FSStore) DeleteCheckpoint(jobID string) error {
	if jobID == "" {
		return fmt.Errorf("jobID cannot be empty")
	}

	jobDir := fs.jobDir(jobID)

	// Check if job directory exists
	if _, err := os.Stat(jobDir); os.IsNotExist(err) {
		return &NotFoundError{JobID: jobID}
	} else if err != nil {
		return fmt.Errorf("failed to stat job directory: %w", err)
	}

	// Remove entire job directory and all contents
	if err := os.RemoveAll(jobDir); err != nil {
		return fmt.Errorf("failed to remove job directory: %w", err)
	}

	slog.Debug("Checkpoint deleted", "jobID", jobID, "path", jobDir)
	return nil
}
