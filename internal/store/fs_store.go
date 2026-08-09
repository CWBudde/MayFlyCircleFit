package store

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Artifact identifies a store-owned job artifact. The closed set prevents
// callers from turning artifact access into an arbitrary path primitive.
type Artifact string

const (
	ArtifactCheckpoint Artifact = "checkpoint.json"
	ArtifactBest       Artifact = "best.png"
	ArtifactDiff       Artifact = "diff.png"
	ArtifactTrace      Artifact = "trace.jsonl"
	ArtifactCircles    Artifact = "circles.json"
)

// FSStore implements Store using a private filesystem tree rooted at baseDir.
// Job IDs are canonical UUIDs, directories below the resolved root cannot be
// symlinks, and replacement writes use unique same-directory temporary files.
type FSStore struct {
	baseDir string
	jobsDir string
}

// NewFSStore creates a filesystem store. The configured root is resolved once
// so all later containment checks use a stable absolute path.
func NewFSStore(baseDir string) (*FSStore, error) {
	root, err := canonicalRoot(baseDir)
	if err != nil {
		return nil, err
	}
	jobsDir := filepath.Join(root, "jobs")
	if err := ensureSecureDir(root, jobsDir); err != nil {
		return nil, fmt.Errorf("create jobs directory: %w", err)
	}
	return &FSStore{baseDir: root, jobsDir: jobsDir}, nil
}

func (fs *FSStore) jobPath(jobID string) (string, error) {
	if err := validateJobID(jobID); err != nil {
		return "", fmt.Errorf("invalid jobID: %w", err)
	}
	path := filepath.Join(fs.jobsDir, jobID)
	if err := ensureContained(fs.baseDir, path); err != nil {
		return "", err
	}
	return path, nil
}

func (fs *FSStore) ensureJobDir(jobID string) (string, error) {
	jobDir, err := fs.jobPath(jobID)
	if err != nil {
		return "", err
	}
	if err := ensureSecureDir(fs.baseDir, fs.jobsDir); err != nil {
		return "", fmt.Errorf("secure jobs directory: %w", err)
	}
	if err := ensureSecureDir(fs.baseDir, jobDir); err != nil {
		return "", fmt.Errorf("create job directory: %w", err)
	}
	return jobDir, nil
}

func (fs *FSStore) existingJobDir(jobID string) (string, error) {
	jobDir, err := fs.jobPath(jobID)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(jobDir)
	if os.IsNotExist(err) {
		return "", &NotFoundError{JobID: jobID}
	}
	if err != nil {
		return "", fmt.Errorf("stat job directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("refusing non-directory or symlink job path")
	}
	return jobDir, nil
}

func validateArtifact(artifact Artifact) error {
	switch artifact {
	case ArtifactCheckpoint, ArtifactBest, ArtifactDiff, ArtifactTrace, ArtifactCircles:
		return nil
	default:
		return fmt.Errorf("unsupported artifact %q", artifact)
	}
}

// ArtifactPath returns a validated path for an existing or future job
// artifact. Only the closed Artifact set can be requested.
func (fs *FSStore) ArtifactPath(jobID string, artifact Artifact) (string, error) {
	if err := validateArtifact(artifact); err != nil {
		return "", err
	}
	jobDir, err := fs.jobPath(jobID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(jobDir, string(artifact))
	if err := ensureContained(fs.baseDir, path); err != nil {
		return "", err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("refusing non-regular or symlink artifact path")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect artifact path: %w", err)
	}
	return path, nil
}

// SaveCheckpoint atomically saves a validated schema-v2 checkpoint.
func (fs *FSStore) SaveCheckpoint(jobID string, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return fmt.Errorf("checkpoint cannot be nil")
	}
	if err := validateJobID(jobID); err != nil {
		return fmt.Errorf("invalid jobID: %w", err)
	}
	if checkpoint.JobID != jobID {
		return fmt.Errorf("checkpoint JobID %q does not match jobID %q", checkpoint.JobID, jobID)
	}
	normalized := checkpoint.normalized()
	if err := normalized.Validate(); err != nil {
		return fmt.Errorf("invalid checkpoint: %w", err)
	}
	if _, err := fs.ensureJobDir(jobID); err != nil {
		return err
	}
	path, err := fs.ArtifactPath(jobID, ArtifactCheckpoint)
	if err != nil {
		return err
	}
	if err := fs.atomicWrite(path, func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(normalized)
	}); err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	slog.Debug("Checkpoint saved", "jobID", jobID, "path", path)
	return nil
}

// LoadCheckpoint retrieves and validates a checkpoint for the given job.
func (fs *FSStore) LoadCheckpoint(jobID string) (*Checkpoint, error) {
	if _, err := fs.existingJobDir(jobID); err != nil {
		return nil, err
	}
	path, err := fs.ArtifactPath(jobID, ArtifactCheckpoint)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, &NotFoundError{JobID: jobID}
	}
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, fmt.Errorf("deserialize checkpoint: %w", err)
	}
	if checkpoint.JobID != jobID {
		return nil, fmt.Errorf("checkpoint JobID %q does not match jobID %q", checkpoint.JobID, jobID)
	}
	if err := checkpoint.Validate(); err != nil {
		return nil, fmt.Errorf("invalid checkpoint: %w", err)
	}
	slog.Debug("Checkpoint loaded", "jobID", jobID, "path", path)
	return &checkpoint, nil
}

// ListCheckpoints returns metadata for valid checkpoint directories.
func (fs *FSStore) ListCheckpoints() ([]CheckpointInfo, error) {
	if err := ensureSecureDir(fs.baseDir, fs.jobsDir); err != nil {
		return nil, fmt.Errorf("secure jobs directory: %w", err)
	}
	entries, err := os.ReadDir(fs.jobsDir)
	if err != nil {
		return nil, fmt.Errorf("read jobs directory: %w", err)
	}
	infos := make([]CheckpointInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || validateJobID(entry.Name()) != nil {
			continue
		}
		checkpoint, err := fs.LoadCheckpoint(entry.Name())
		if err != nil {
			slog.Warn("Failed to load checkpoint for listing", "jobID", entry.Name(), "error", err)
			continue
		}
		infos = append(infos, checkpoint.ToInfo())
	}
	return infos, nil
}

// DeleteCheckpoint removes the checkpoint and all associated artifacts.
func (fs *FSStore) DeleteCheckpoint(jobID string) error {
	jobDir, err := fs.existingJobDir(jobID)
	if err != nil {
		return err
	}
	if err := ensureContained(fs.baseDir, jobDir); err != nil {
		return err
	}
	if err := os.RemoveAll(jobDir); err != nil {
		return fmt.Errorf("remove job directory: %w", err)
	}
	slog.Debug("Checkpoint deleted", "jobID", jobID, "path", jobDir)
	return nil
}

// SaveCircleSnapshot atomically saves an intermediate canvas snapshot.
func (fs *FSStore) SaveCircleSnapshot(jobID string, circleNum int, img image.Image) error {
	if img == nil {
		return fmt.Errorf("image cannot be nil")
	}
	if circleNum < 1 {
		return fmt.Errorf("circleNum must be >= 1")
	}
	jobDir, err := fs.ensureJobDir(jobID)
	if err != nil {
		return err
	}
	snapshotsDir := filepath.Join(jobDir, "snapshots")
	if err := ensureSecureDir(fs.baseDir, snapshotsDir); err != nil {
		return fmt.Errorf("create snapshots directory: %w", err)
	}
	path := filepath.Join(snapshotsDir, fmt.Sprintf("canvas-%02d.png", circleNum))
	if err := fs.atomicWrite(path, func(writer io.Writer) error { return png.Encode(writer, img) }); err != nil {
		return fmt.Errorf("save circle snapshot: %w", err)
	}
	slog.Debug("Circle snapshot saved", "jobID", jobID, "circleNum", circleNum, "path", path)
	return nil
}

// SaveCircleData atomically saves per-circle metadata as indented JSON.
func (fs *FSStore) SaveCircleData(jobID string, circles []CircleData) error {
	if _, err := fs.ensureJobDir(jobID); err != nil {
		return err
	}
	path, err := fs.ArtifactPath(jobID, ArtifactCircles)
	if err != nil {
		return err
	}
	if err := fs.atomicWrite(path, func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(circles)
	}); err != nil {
		return fmt.Errorf("save circles: %w", err)
	}
	slog.Debug("Circle data saved", "jobID", jobID, "numCircles", len(circles), "path", path)
	return nil
}

// SavePNGArtifact atomically saves one of the supported PNG artifacts.
func (fs *FSStore) SavePNGArtifact(jobID string, artifact Artifact, img image.Image) error {
	if artifact != ArtifactBest && artifact != ArtifactDiff {
		return fmt.Errorf("artifact %q is not a writable PNG artifact", artifact)
	}
	if img == nil {
		return fmt.Errorf("image cannot be nil")
	}
	if _, err := fs.ensureJobDir(jobID); err != nil {
		return err
	}
	path, err := fs.ArtifactPath(jobID, artifact)
	if err != nil {
		return err
	}
	if err := fs.atomicWrite(path, func(writer io.Writer) error { return png.Encode(writer, img) }); err != nil {
		return fmt.Errorf("save %s: %w", artifact, err)
	}
	return nil
}

func (fs *FSStore) atomicWrite(path string, write func(io.Writer) error) (resultErr error) {
	if err := ensureContained(fs.baseDir, path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat destination directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing non-directory or symlink destination")
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		if resultErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(artifactMode); err != nil {
		return fmt.Errorf("secure temporary file permissions: %w", err)
	}
	if err := write(temp); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	temp = nil
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync destination directory: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
