package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
)

// Artifact identifies a store-owned job artifact. The closed set prevents
// callers from turning artifact access into an arbitrary path primitive.
type Artifact string

const (
	ArtifactCheckpoint     Artifact = "checkpoint.json"
	ArtifactCheckpointInfo Artifact = "checkpoint-info.json"
	ArtifactBest           Artifact = "best.png"
	ArtifactDiff           Artifact = "diff.png"
	ArtifactTrace          Artifact = "trace.jsonl"
	ArtifactCircles        Artifact = "circles.json"
)

// FSStore implements Store using a private filesystem tree rooted at baseDir.
// Job IDs are canonical UUIDs, directories below the resolved root cannot be
// symlinks, and replacement writes use unique same-directory temporary files.
type FSStore struct {
	baseDir string
	jobsDir string
	atomic  atomicFileOperations
	// Checkpoint and checkpoint-info are one logical commit. A small fixed set
	// of locks keeps same-job writes and listings consistent without serializing
	// checkpoint writes for every active job.
	checkpointLocks [64]sync.RWMutex
}

// atomicFileOperations keeps the two failure-prone boundaries of the atomic
// replacement protocol injectable for focused fault tests.
type atomicFileOperations struct {
	write  func(*os.File, func(io.Writer) error) error
	rename func(string, string) error
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

	return &FSStore{
		baseDir: root,
		jobsDir: jobsDir,
		atomic: atomicFileOperations{
			write: func(file *os.File, write func(io.Writer) error) error {
				return write(file)
			},
			rename: os.Rename,
		},
	}, nil
}

func (fs *FSStore) jobPath(jobID string) (string, error) {
	err := validateJobID(jobID)
	if err != nil {
		return "", fmt.Errorf("invalid jobID: %w", err)
	}

	path := filepath.Join(fs.jobsDir, jobID)

	err = ensureContained(fs.baseDir, path)
	if err != nil {
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
		return "", errors.New("refusing non-directory or symlink job path")
	}

	return jobDir, nil
}

func validateArtifact(artifact Artifact) error {
	switch artifact {
	case ArtifactCheckpoint, ArtifactCheckpointInfo, ArtifactBest, ArtifactDiff, ArtifactTrace, ArtifactCircles:
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
			return "", errors.New("refusing non-regular or symlink artifact path")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect artifact path: %w", err)
	}

	return path, nil
}

// SaveCheckpoint atomically saves a validated schema-v2 checkpoint.
func (fs *FSStore) SaveCheckpoint(jobID string, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return errors.New("checkpoint cannot be nil")
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

	infoPath, err := fs.ArtifactPath(jobID, ArtifactCheckpointInfo)
	if err != nil {
		return err
	}

	lock := fs.checkpointLock(jobID)
	lock.Lock()
	defer lock.Unlock()
	// An old summary must never describe a newly committed checkpoint. If the
	// primary write fails, listings safely fall back to projecting the preserved
	// checkpoint; if the process dies after the rename, the sidecar is absent.
	if err := os.Remove(infoPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale checkpoint summary: %w", err)
	}

	if err := fs.atomicWrite(path, func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		// Encode the wire struct rather than the Checkpoint: the latter would
		// run Checkpoint.MarshalJSON, which normalizes again and copies
		// BestParams a second time on a path that runs once per stage. The
		// bytes are identical because normalized() is idempotent.
		return encoder.Encode(checkpointWireFrom(normalized))
	}); err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	// The sidecar is a performance index, not the durable result. A failure to
	// write it leaves no stale copy and ListCheckpoints can still project the
	// primary checkpoint without materializing BestParams.
	if err := fs.atomicWrite(infoPath, func(writer io.Writer) error {
		return json.NewEncoder(writer).Encode(normalized.ToInfo())
	}); err != nil {
		_ = os.Remove(infoPath)

		slog.Warn("Failed to save checkpoint summary; listings will use the checkpoint projection",
			"jobID", jobID, "error", err)
	}

	slog.Debug("Checkpoint saved", "jobID", jobID, "path", path)

	return nil
}

// LoadCheckpoint retrieves and validates a checkpoint for the given job.
func (fs *FSStore) LoadCheckpoint(jobID string) (*Checkpoint, error) {
	lock := fs.checkpointLock(jobID)

	lock.RLock()
	defer lock.RUnlock()

	return fs.loadCheckpoint(jobID)
}

func (fs *FSStore) loadCheckpoint(jobID string) (*Checkpoint, error) {
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

		lock := fs.checkpointLock(entry.Name())
		lock.RLock()

		info, err := fs.loadCheckpointInfo(entry.Name())

		lock.RUnlock()

		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}

			slog.Warn("Failed to load checkpoint for listing", "jobID", entry.Name(), "error", err)

			continue
		}

		infos = append(infos, info)
	}

	return infos, nil
}

func (fs *FSStore) checkpointLock(jobID string) *sync.RWMutex {
	const (
		offset = uint32(2166136261)
		prime  = uint32(16777619)
	)

	hash := offset
	for i := range len(jobID) {
		hash ^= uint32(jobID[i])
		hash *= prime
	}

	return &fs.checkpointLocks[hash%uint32(len(fs.checkpointLocks))]
}

func (fs *FSStore) loadCheckpointInfo(jobID string) (CheckpointInfo, error) {
	if _, err := fs.existingJobDir(jobID); err != nil {
		return CheckpointInfo{}, err
	}

	checkpointPath, err := fs.ArtifactPath(jobID, ArtifactCheckpoint)
	if err != nil {
		return CheckpointInfo{}, err
	}

	checkpointStat, err := os.Stat(checkpointPath)
	if os.IsNotExist(err) {
		return CheckpointInfo{}, &NotFoundError{JobID: jobID}
	}

	if err != nil {
		return CheckpointInfo{}, fmt.Errorf("stat checkpoint: %w", err)
	}

	infoPath, infoPathErr := fs.ArtifactPath(jobID, ArtifactCheckpointInfo)
	if infoPathErr == nil {
		if infoStat, statErr := os.Stat(infoPath); statErr == nil && !infoStat.ModTime().Before(checkpointStat.ModTime()) {
			data, readErr := os.ReadFile(infoPath)
			if readErr == nil {
				var info CheckpointInfo

				decodeErr := json.Unmarshal(data, &info)
				if decodeErr == nil && validateCheckpointInfo(info, jobID) == nil {
					return info, nil
				}
			}
		}
	}

	info, err := projectCheckpointInfo(checkpointPath, jobID)
	if err != nil {
		return CheckpointInfo{}, err
	}
	// Backfill checkpoints written before the summary existed. The caller holds
	// this job's read lock, so a checkpoint save cannot replace the primary
	// between projection and sidecar commit.
	if infoPathErr == nil {
		err := fs.atomicWrite(infoPath, func(writer io.Writer) error {
			return json.NewEncoder(writer).Encode(info)
		})
		if err != nil {
			slog.Debug("Unable to backfill checkpoint summary", "jobID", jobID, "error", err)
		}
	}

	return info, nil
}

// checkpointInfoProjection deliberately has no []float64 field. The decoder
// still validates the JSON document, but skips bestParams instead of allocating
// the potentially tens-of-thousands-element vector merely to build a row.
type checkpointInfoProjection struct {
	SchemaVersion    int                        `json:"schemaVersion"`
	JobID            string                     `json:"jobId"`
	BestParams       parameterCount             `json:"bestParams"`
	BestCost         float64                    `json:"bestCost"`
	InitialCost      float64                    `json:"initialCost"`
	RequestedCircles int                        `json:"requestedCircles"`
	ActualCircles    int                        `json:"actualCircles"`
	EffectiveSeed    int64                      `json:"effectiveSeed"`
	ResumeCount      int                        `json:"resumeCount"`
	Iterations       int                        `json:"iterations"`
	Evaluations      int64                      `json:"evaluations"`
	Termination      string                     `json:"termination"`
	Iteration        int                        `json:"iteration"`
	Timestamp        time.Time                  `json:"timestamp"`
	ExtendedFrom     string                     `json:"extendedFrom"`
	PolishedFrom     string                     `json:"polishedFrom"`
	ScheduleID       string                     `json:"scheduleId"`
	StageIndex       *int                       `json:"stageIndex"`
	OptimizerVersion string                     `json:"optimizerVersion"`
	Config           checkpointConfigProjection `json:"config"`
}

type checkpointConfigProjection struct {
	RefPath       string   `json:"refPath"`
	Mode          app.Mode `json:"mode"`
	Circles       int      `json:"circles"`
	Iters         int      `json:"iters"`
	PopSize       int      `json:"popSize"`
	Seed          int64    `json:"seed"`
	EffectiveSeed int64    `json:"effectiveSeed"`
	ResumeCount   int      `json:"resumeCount"`
}

func (config checkpointConfigProjection) jobConfig() JobConfig {
	return JobConfig{
		RefPath: config.RefPath, Mode: config.Mode, Circles: config.Circles,
		Iters: config.Iters, PopSize: config.PopSize, Seed: config.Seed,
		EffectiveSeed: config.EffectiveSeed, ResumeCount: config.ResumeCount,
	}
}

func projectCheckpointInfo(path, jobID string) (CheckpointInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return CheckpointInfo{}, err
	}
	defer file.Close()

	var projection checkpointInfoProjection

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&projection); err != nil {
		return CheckpointInfo{}, fmt.Errorf("deserialize checkpoint summary: %w", err)
	}
	// Reject a second JSON value while allowing trailing whitespace.
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return CheckpointInfo{}, errors.New("deserialize checkpoint summary: trailing JSON value")
		}

		return CheckpointInfo{}, fmt.Errorf("deserialize checkpoint summary: %w", err)
	}

	return projection.toInfo(jobID)
}

// parameterCount validates and counts the JSON array without retaining its
// float values. The decoder hands this method a view into its input buffer, so
// even a legacy checkpoint without a sidecar never materializes BestParams.
type parameterCount int

func (count *parameterCount) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))

	token, err := decoder.Token()
	if err != nil {
		return err
	}

	opening, ok := token.(json.Delim)
	if !ok || opening != '[' {
		return errors.New("bestParams must be an array")
	}

	n := 0

	for decoder.More() {
		var value float64

		err := decoder.Decode(&value)
		if err != nil {
			return err
		}

		n++
	}

	if _, err := decoder.Token(); err != nil {
		return err
	}

	*count = parameterCount(n)

	return nil
}

func (p checkpointInfoProjection) toInfo(jobID string) (CheckpointInfo, error) {
	config := p.Config.jobConfig()

	originalSchema := p.SchemaVersion
	if originalSchema < 0 || originalSchema > CheckpointSchemaVersion || (originalSchema != 0 && originalSchema != 1 && originalSchema != CheckpointSchemaVersion) {
		return CheckpointInfo{}, fmt.Errorf("unsupported checkpoint schema version %d", originalSchema)
	}

	if p.Iterations == 0 && p.Iteration != 0 {
		p.Iterations = p.Iteration
	}

	if p.RequestedCircles == 0 {
		p.RequestedCircles = config.Circles
	}

	if p.ActualCircles == 0 && int(p.BestParams)%7 == 0 {
		p.ActualCircles = int(p.BestParams) / 7
	}

	if p.EffectiveSeed == 0 {
		p.EffectiveSeed = effectiveSeed(config)
	}

	if p.ResumeCount == 0 {
		p.ResumeCount = config.ResumeCount
	}

	if p.Termination == "" {
		if originalSchema == 0 || originalSchema == 1 {
			p.Termination = TerminationLegacy
		} else {
			p.Termination = TerminationUnknown
		}
	}

	if (originalSchema == 0 || originalSchema == 1) && p.Evaluations == 0 && p.Iterations > 0 && config.PopSize > 0 {
		p.Evaluations = int64(p.Iterations) * int64(config.PopSize)
	}

	info := CheckpointInfo{
		SchemaVersion: CheckpointSchemaVersion, JobID: p.JobID, BestCost: p.BestCost,
		Iteration: p.Iterations, Evaluations: p.Evaluations, RequestedCircles: p.RequestedCircles,
		ActualCircles: p.ActualCircles, EffectiveSeed: p.EffectiveSeed, ResumeCount: p.ResumeCount,
		Termination: p.Termination, Timestamp: p.Timestamp, ExtendedFrom: p.ExtendedFrom,
		PolishedFrom: p.PolishedFrom, ScheduleID: p.ScheduleID, OptimizerVersion: p.OptimizerVersion,
		Mode: config.Mode, Circles: config.Circles, RefPath: config.RefPath,
	}

	err := validateCheckpointInfo(info, jobID)
	if err != nil {
		return CheckpointInfo{}, err
	}

	if int(p.BestParams) == 0 || int(p.BestParams)%7 != 0 || int(p.BestParams) != p.ActualCircles*7 || p.InitialCost < 0 || config.Iters <= 0 || config.PopSize <= 0 || p.RequestedCircles != config.Circles || p.ActualCircles > p.RequestedCircles {
		return CheckpointInfo{}, errors.New("invalid checkpoint metadata")
	}

	lineage := Checkpoint{JobID: p.JobID, ExtendedFrom: p.ExtendedFrom, PolishedFrom: p.PolishedFrom, ScheduleID: p.ScheduleID, StageIndex: p.StageIndex}

	err = lineage.validateLineage()
	if err != nil {
		return CheckpointInfo{}, err
	}

	return info, nil
}

func validateCheckpointInfo(info CheckpointInfo, jobID string) error {
	if info.JobID != jobID || validateJobID(info.JobID) != nil || info.SchemaVersion != CheckpointSchemaVersion {
		return fmt.Errorf("checkpoint summary does not match job %q", jobID)
	}

	if info.BestCost < 0 || info.Iteration < 0 || info.Evaluations < 0 || info.ResumeCount < 0 || info.Timestamp.IsZero() {
		return errors.New("invalid checkpoint summary progress")
	}

	if info.RefPath == "" || info.Mode == "" || info.Circles <= 0 || info.RequestedCircles != info.Circles || info.ActualCircles < 1 || info.ActualCircles > info.RequestedCircles {
		return errors.New("invalid checkpoint summary configuration")
	}

	lineage := Checkpoint{JobID: info.JobID, ExtendedFrom: info.ExtendedFrom, PolishedFrom: info.PolishedFrom, ScheduleID: info.ScheduleID}

	return lineage.validateLineage()
}

// DeleteCheckpoint removes the checkpoint and all associated artifacts.
func (fs *FSStore) DeleteCheckpoint(jobID string) error {
	lock := fs.checkpointLock(jobID)
	lock.Lock()
	defer lock.Unlock()

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
		return errors.New("image cannot be nil")
	}

	if circleNum < 1 {
		return errors.New("circleNum must be >= 1")
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
		return errors.New("image cannot be nil")
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
		return errors.New("refusing non-directory or symlink destination")
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

	writeTemp := fs.atomic.write
	if writeTemp == nil {
		writeTemp = func(file *os.File, write func(io.Writer) error) error { return write(file) }
	}

	if err := writeTemp(temp, write); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}

	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}

	temp = nil

	rename := fs.atomic.rename
	if rename == nil {
		rename = os.Rename
	}

	if err := rename(tempPath, path); err != nil {
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
