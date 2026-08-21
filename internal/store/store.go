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
	//   - checkpoint-info.json
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

// ArtifactStore is the optional store-owned artifact API. Keeping it separate
// from Store preserves compatibility for checkpoint-only implementations while
// allowing server and CLI code to avoid constructing filesystem paths.
type ArtifactStore interface {
	ArtifactPath(jobID string, artifact Artifact) (string, error)
	SavePNGArtifact(jobID string, artifact Artifact, img image.Image) error
	NewTraceWriter(jobID string, appendMode bool) (*TraceWriter, error)
	NewTraceReader(jobID string) (*TraceReader, error)
	DeleteTrace(jobID string) error
}

// ErrNotFound is returned when a requested checkpoint does not exist.
// Use errors.Is(err, ErrNotFound) to check for this error.
var ErrNotFound = &NotFoundError{}

// NotFoundError represents a missing store entity. Kind names what was missing
// and defaults to a checkpoint, so the message a caller has always seen is
// unchanged while a schedule can say what it actually is.
type NotFoundError struct {
	JobID string
	Kind  string
}

func (e *NotFoundError) Error() string {
	kind := e.Kind
	if kind == "" {
		kind = "checkpoint"
	}
	if e.JobID != "" {
		return kind + " not found: " + e.JobID
	}
	return kind + " not found"
}

func (e *NotFoundError) Is(target error) bool {
	_, ok := target.(*NotFoundError)
	return ok
}
