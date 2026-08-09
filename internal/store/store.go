package store

import (
	"image"
)

// Store defines the interface for checkpoint persistence operations.
// Implementations must be thread-safe and handle concurrent access gracefully.
//
// Error handling conventions:
//   - Return nil error on success
//   - Return ErrNotFound if checkpoint doesn't exist (for Load/Delete)
//   - Return descriptive errors for I/O, serialization, or validation failures
//   - Wrap underlying errors with context using fmt.Errorf("context: %w", err)
type Store interface {
	// SaveCheckpoint atomically saves a checkpoint for the given job.
	// If a checkpoint already exists for this jobID, it is overwritten.
	// The implementation should use atomic write strategies (e.g., temp file + rename)
	// to prevent corruption in case of failures.
	//
	// Returns an error if the checkpoint cannot be saved (e.g., disk full,
	// permission denied, serialization failure).
	SaveCheckpoint(jobID string, checkpoint *Checkpoint) error

	// LoadCheckpoint retrieves the checkpoint for the given job.
	// Returns ErrNotFound if no checkpoint exists for this jobID.
	// Returns an error if the checkpoint exists but cannot be read or deserialized.
	LoadCheckpoint(jobID string) (*Checkpoint, error)

	// ListCheckpoints returns metadata for all available checkpoints.
	// The returned slice may be empty if no checkpoints exist.
	// Returns an error if the checkpoint directory cannot be scanned.
	ListCheckpoints() ([]CheckpointInfo, error)

	// DeleteCheckpoint removes the checkpoint and all associated artifacts
	// for the given job. This includes:
	//   - checkpoint.json
	//   - best.png
	//   - diff.png
	//   - trace.jsonl
	//   - snapshots/ directory (if exists)
	//   - circles.json (if exists)
	//
	// Returns ErrNotFound if no checkpoint exists for this jobID.
	// Returns an error if the checkpoint exists but cannot be deleted.
	DeleteCheckpoint(jobID string) error

	// SaveCircleSnapshot saves an intermediate canvas snapshot during sequential optimization.
	// The snapshot is saved to ./data/jobs/<jobID>/snapshots/canvas-NN.png where NN is zero-padded.
	// This method creates the snapshots directory if it doesn't exist.
	//
	// Returns an error if the image cannot be encoded or written to disk.
	SaveCircleSnapshot(jobID string, circleNum int, img image.Image) error

	// SaveCircleData saves the per-circle metadata as a JSON array.
	// The data is saved to ./data/jobs/<jobID>/circles.json with pretty formatting.
	// Each CircleData entry should contain circle parameters and optimization metadata.
	//
	// Returns an error if the data cannot be encoded or written to disk.
	SaveCircleData(jobID string, circles []CircleData) error
}

// ErrNotFound is returned when a requested checkpoint does not exist.
// Use errors.Is(err, ErrNotFound) to check for this error.
var ErrNotFound = &NotFoundError{}

// NotFoundError represents a missing checkpoint error.
type NotFoundError struct {
	JobID string
}

func (e *NotFoundError) Error() string {
	if e.JobID != "" {
		return "checkpoint not found: " + e.JobID
	}
	return "checkpoint not found"
}

func (e *NotFoundError) Is(target error) bool {
	_, ok := target.(*NotFoundError)
	return ok
}
