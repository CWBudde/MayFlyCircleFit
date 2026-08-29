package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
)

// ScheduleRecordSchemaVersion is the persisted schedule format written by this
// version. It is deliberately separate from CheckpointSchemaVersion: a schedule
// and a checkpoint are different documents with different lifetimes.
const ScheduleRecordSchemaVersion = 1

// schedulesDirName holds every schedule below a store root, beside `jobs`. A
// schedule is keyed by its own identifier rather than by any job, which is what
// lets a campaign outlive, and be read back independently of, the jobs it
// realizes.
const schedulesDirName = "schedules"

// stagesDirName holds one file per realized stage. One file per index makes a
// stage update idempotent — the executor rewrites the same path when a stage
// moves from running to completed — so there is no second progress file that
// can drift from the stage records.
const stagesDirName = "stages"

// ScheduleState is the lifecycle of a schedule and of each of its stages. The
// two share a vocabulary because a stage is just the part of the campaign that
// is running now, and the values match the job states the server already uses.
type ScheduleState string

const (
	ScheduleStatePending   ScheduleState = "pending"
	ScheduleStateRunning   ScheduleState = "running"
	ScheduleStateCompleted ScheduleState = "completed"
	ScheduleStateFailed    ScheduleState = "failed"
	ScheduleStateCancelled ScheduleState = "cancelled"

	// ScheduleStatePaused is a campaign an operator has stopped between stages.
	// It is a schedule-level state only: a stage is never paused, because a
	// stage is the unit that either runs to a result or does not, and a paused
	// stage would be a second thing progress could mean.
	ScheduleStatePaused ScheduleState = "paused"

	// ScheduleStateSkipped is a planned stage the campaign's own policy declined
	// to run. It is a stage-level state only, and it is recorded rather than
	// left as a gap so the stage records still answer "what happened to every
	// planned stage?" without anyone re-deriving the policy by hand.
	ScheduleStateSkipped ScheduleState = "skipped"
)

// validScheduleStageState reports the states a stage record may hold. Pause is
// absent by construction; see ScheduleStatePaused.
func validScheduleStageState(state ScheduleState) bool {
	switch state {
	case ScheduleStatePending, ScheduleStateRunning, ScheduleStateCompleted,
		ScheduleStateFailed, ScheduleStateCancelled, ScheduleStateSkipped:
		return true
	default:
		return false
	}
}

// validScheduleState reports the states a schedule record may hold. A schedule
// is never skipped: policy declines stages, never whole campaigns.
func validScheduleState(state ScheduleState) bool {
	switch state {
	case ScheduleStatePending, ScheduleStateRunning, ScheduleStateCompleted,
		ScheduleStateFailed, ScheduleStateCancelled, ScheduleStatePaused:
		return true
	default:
		return false
	}
}

// ScheduleRecord is a persisted campaign: the document as authored, plus the
// bookkeeping the store owns. The document itself stays an app type — the store
// persists application configuration but does not define it.
type ScheduleRecord struct {
	SchemaVersion int           `json:"schemaVersion"`
	ScheduleID    string        `json:"scheduleId"`
	State         ScheduleState `json:"state"`

	// CampaignSeed is the resolved seed every stage inherits. It is recorded
	// here rather than left to the document so a campaign whose document omitted
	// a seed still replays the same way after a restart.
	CampaignSeed int64 `json:"campaignSeed"`

	Document app.ScheduleDocument `json:"document"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Error records why a schedule stopped short — a failure, or the barrier it
	// paused at. It is free text for an operator, never a control signal.
	Error string `json:"error,omitempty"`

	// ReleasedThroughStage is the highest barrier an operator has already
	// released with a resume. Without it a resumed campaign would meet the same
	// `pauseBefore` stage, find no record for it, and pause again forever.
	//
	// Zero is the correct default rather than a sentinel: stage zero is the
	// base, which no step can put a barrier on, so "released through nothing"
	// and "released through stage zero" mean the same thing.
	ReleasedThroughStage int `json:"releasedThroughStage,omitempty"`
}

// NewScheduleRecord seeds a pending record from an accepted document. The
// campaign seed is resolved here if the document omitted one, and written back
// into the persisted document, so every later expansion — in this process or
// after a restart — plans the same stages.
func NewScheduleRecord(scheduleID string, document app.ScheduleDocument) (*ScheduleRecord, error) {
	err := document.ResolveSeed()
	if err != nil {
		return nil, fmt.Errorf("resolve campaign seed: %w", err)
	}

	now := time.Now().UTC()

	return &ScheduleRecord{
		SchemaVersion: ScheduleRecordSchemaVersion,
		ScheduleID:    scheduleID,
		State:         ScheduleStatePending,
		CampaignSeed:  document.Seed,
		Document:      document,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// Validate checks the record without reference to the filesystem.
func (r *ScheduleRecord) Validate() error {
	if r == nil {
		return &ValidationError{Field: "ScheduleRecord", Reason: "cannot be nil"}
	}

	err := validateScheduleID(r.ScheduleID)
	if err != nil {
		return &ValidationError{Field: "scheduleID", Reason: err.Error()}
	}

	if r.SchemaVersion != ScheduleRecordSchemaVersion {
		return &ValidationError{Field: "SchemaVersion", Reason: fmt.Sprintf("must be %d", ScheduleRecordSchemaVersion)}
	}

	if !validScheduleState(r.State) {
		return &ValidationError{Field: "State", Reason: fmt.Sprintf("%q is not a schedule state", r.State)}
	}

	if r.CreatedAt.IsZero() {
		return &ValidationError{Field: "CreatedAt", Reason: "cannot be zero"}
	}
	// The document is validated, not merely expanded: expansion alone would
	// accept an embedded schema version or a step type this build does not
	// understand, which is exactly what a record written by a newer binary looks
	// like.
	err = r.Document.Validate()
	if err != nil {
		return &ValidationError{Field: "Document", Reason: err.Error()}
	}
	// A campaign whose seed is unresolved, or disagrees with its document, would
	// replan with a fresh random seed on the next expansion.
	if r.CampaignSeed == 0 {
		return &ValidationError{Field: "CampaignSeed", Reason: "must be resolved before a schedule is persisted"}
	}

	if r.Document.Seed != r.CampaignSeed {
		return &ValidationError{Field: "CampaignSeed", Reason: fmt.Sprintf("must match the document seed (%d)", r.Document.Seed)}
	}

	return nil
}

// ScheduleStageRecord is one realized stage: the planned stage plus what it
// actually ran with and what came of it. It is the single source of truth for
// campaign progress, which is why the outcome fields live here rather than in a
// separate progress file.
type ScheduleStageRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	ScheduleID    string `json:"scheduleId"`

	// Index is the position in the realized stage list and the key on disk.
	Index      int                   `json:"index"`
	Kind       app.ScheduleStageKind `json:"kind"`
	StepIndex  int                   `json:"stepIndex"`
	Repetition int                   `json:"repetition"`

	Circles           int `json:"circles"`
	AdditionalCircles int `json:"additionalCircles,omitempty"`

	// ActualCircles is the count the stage actually materialized, recorded at
	// settlement from the job's own tally. It differs from Circles because
	// Circles is the *planned* count: it is copied from the expanded plan and
	// pinned to Config.Circles by Validate, so it cannot be rewritten in place
	// once the stage has run. A batch stage that terminates at its refill limit
	// materializes fewer circles than it asked for, and anything reasoning about
	// what the canvas holds — the cost projection's per-circle rate above all —
	// has to read this rather than the request.
	//
	// It is zero for a stage that has not settled and for every record written
	// before the field existed, which is why MaterializedCircles falls back to
	// the planned count and why ScheduleRecordSchemaVersion is deliberately not
	// bumped: the field is additive and optional, and an older record's zero
	// reads correctly through that fallback.
	ActualCircles int `json:"actualCircles,omitempty"`

	// Config is what the stage ran with, seed included, so a single stage can be
	// replayed without re-deriving it from the document.
	Config JobConfig `json:"config"`

	State ScheduleState `json:"state"`

	// JobID is the job that realized the stage, empty until one is created.
	// ParentJobID is the checkpoint it continued from, empty for the base stage.
	JobID       string `json:"jobId,omitempty"`
	ParentJobID string `json:"parentJobId,omitempty"`

	BestCost    float64 `json:"bestCost,omitempty"`
	Iterations  int     `json:"iterations,omitempty"`
	Evaluations int64   `json:"evaluations,omitempty"`

	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`

	Error string `json:"error,omitempty"`

	// Reason records why a stage is in the state it is when nothing went wrong
	// — in practice, why policy declined a skipped stage. It is free text for an
	// operator, never a control signal: the decision is re-derived from the
	// outcomes, never read back from here.
	Reason string `json:"reason,omitempty"`
}

// NewScheduleStageRecord seeds a pending stage record from a planned stage.
func NewScheduleStageRecord(scheduleID string, stage app.ScheduleStage) *ScheduleStageRecord {
	return &ScheduleStageRecord{
		SchemaVersion:     ScheduleRecordSchemaVersion,
		ScheduleID:        scheduleID,
		Index:             stage.Index,
		Kind:              stage.Kind,
		StepIndex:         stage.StepIndex,
		Repetition:        stage.Repetition,
		Circles:           stage.Circles,
		AdditionalCircles: stage.AdditionalCircles,
		Config:            stage.Config,
		State:             ScheduleStatePending,
		UpdatedAt:         time.Now().UTC(),
	}
}

// MaterializedCircles reports the circle count a settled stage actually
// produced, falling back to the planned count.
//
// The fallback is what lets every reader use one accessor: a pending stage has
// nothing to report yet, and a record written before ActualCircles existed
// never will, so in both cases the plan's count is the only figure there is and
// it is the figure those readers used before. A settled stage overrides it with
// what it really built.
func (s *ScheduleStageRecord) MaterializedCircles() int {
	if s.ActualCircles > 0 {
		return s.ActualCircles
	}

	return s.Circles
}

// Validate checks a stage record without reference to the filesystem.
func (s *ScheduleStageRecord) Validate() error {
	if s == nil {
		return &ValidationError{Field: "ScheduleStageRecord", Reason: "cannot be nil"}
	}

	err := validateScheduleID(s.ScheduleID)
	if err != nil {
		return &ValidationError{Field: "scheduleID", Reason: err.Error()}
	}

	if s.SchemaVersion != ScheduleRecordSchemaVersion {
		return &ValidationError{Field: "SchemaVersion", Reason: fmt.Sprintf("must be %d", ScheduleRecordSchemaVersion)}
	}

	if s.Index < 0 {
		return &ValidationError{Field: "Index", Reason: "cannot be negative"}
	}

	switch s.Kind {
	case app.ScheduleStageBase, app.ScheduleStageExtend, app.ScheduleStagePolish:
	default:
		return &ValidationError{Field: "Kind", Reason: fmt.Sprintf("%q is not a stage kind", s.Kind)}
	}

	if !validScheduleStageState(s.State) {
		return &ValidationError{Field: "State", Reason: fmt.Sprintf("%q is not a stage state", s.State)}
	}

	if s.Circles < 1 {
		return &ValidationError{Field: "Circles", Reason: "must be positive"}
	}
	// The stage config is what a single stage is replayed from, so it is checked
	// like any other persisted configuration, and it must describe this stage:
	// a config for a different circle count cannot reproduce the run.
	err = s.Config.Validate()
	if err != nil {
		return &ValidationError{Field: "Config", Reason: err.Error()}
	}

	if s.Config.Circles != s.Circles {
		return &ValidationError{Field: "Config.Circles", Reason: fmt.Sprintf("must match the stage circle count (%d)", s.Circles)}
	}
	// A stage can materialize fewer circles than it planned for, never more, so
	// a count above the request is a record that disagrees with the job it came
	// from rather than an unusually productive stage. Zero is not an error: it
	// is what an unsettled stage carries, and what every record written before
	// the field existed carries.
	if s.ActualCircles < 0 || s.ActualCircles > s.Circles {
		return &ValidationError{
			Field:  "ActualCircles",
			Reason: fmt.Sprintf("must be between zero and the planned circle count (%d)", s.Circles),
		}
	}
	// A skipped stage never ran, so a job identifier on one would mean the
	// record and the job tree disagree about whether work happened.
	if s.State == ScheduleStateSkipped && s.JobID != "" {
		return &ValidationError{Field: "JobID", Reason: "a skipped stage cannot name a job"}
	}

	if s.JobID != "" {
		err := validateJobID(s.JobID)
		if err != nil {
			return &ValidationError{Field: "JobID", Reason: err.Error()}
		}
	}

	if s.ParentJobID != "" {
		err := validateJobID(s.ParentJobID)
		if err != nil {
			return &ValidationError{Field: "ParentJobID", Reason: err.Error()}
		}
	}

	if s.JobID != "" && s.JobID == s.ParentJobID {
		return &ValidationError{Field: "ParentJobID", Reason: "cannot name the stage's own job"}
	}

	return nil
}

// ScheduleStore is the optional schedule persistence API, kept separate from
// Store for the same reason ArtifactStore is: a checkpoint-only implementation
// stays valid without it.
type ScheduleStore interface {
	SaveSchedule(record *ScheduleRecord) error
	LoadSchedule(scheduleID string) (*ScheduleRecord, error)
	ListSchedules() ([]ScheduleRecord, error)
	DeleteSchedule(scheduleID string) error

	// SaveScheduleStage writes one stage by index, replacing any earlier write
	// for the same index. Re-recording a stage is therefore idempotent, which is
	// what keeps a crash between "stage accepted" and "stage recorded" from
	// forking the chain.
	SaveScheduleStage(scheduleID string, stage *ScheduleStageRecord) error

	// LoadScheduleStages returns the recorded stages in index order.
	LoadScheduleStages(scheduleID string) ([]ScheduleStageRecord, error)

	// LoadScheduleStage returns one recorded stage by index. It exists because
	// a stage's configuration is what replaying that stage alone needs, and
	// reading the whole campaign to get one stage's configuration is what makes
	// a listing grow with the fit rather than with the reader's question.
	LoadScheduleStage(scheduleID string, index int) (*ScheduleStageRecord, error)
}

func (fs *FSStore) schedulesDir() (string, error) {
	dir := filepath.Join(fs.baseDir, schedulesDirName)

	err := ensureSecureDir(fs.baseDir, dir)
	if err != nil {
		return "", fmt.Errorf("create schedules directory: %w", err)
	}

	return dir, nil
}

func (fs *FSStore) schedulePath(scheduleID string) (string, error) {
	err := validateScheduleID(scheduleID)
	if err != nil {
		return "", fmt.Errorf("invalid scheduleID: %w", err)
	}

	root, err := fs.schedulesDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(root, scheduleID)

	err = ensureContained(fs.baseDir, path)
	if err != nil {
		return "", err
	}

	return path, nil
}

func (fs *FSStore) existingScheduleDir(scheduleID string) (string, error) {
	dir, err := fs.schedulePath(scheduleID)
	if err != nil {
		return "", err
	}

	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return "", &NotFoundError{JobID: scheduleID, Kind: "schedule"}
	}

	if err != nil {
		return "", fmt.Errorf("stat schedule directory: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("refusing non-directory or symlink schedule path")
	}

	return dir, nil
}

// SaveSchedule atomically writes a validated schedule record.
func (fs *FSStore) SaveSchedule(record *ScheduleRecord) error {
	if record == nil {
		return errors.New("schedule record cannot be nil")
	}

	err := record.Validate()
	if err != nil {
		return fmt.Errorf("invalid schedule: %w", err)
	}

	dir, err := fs.schedulePath(record.ScheduleID)
	if err != nil {
		return err
	}

	err = ensureSecureDir(fs.baseDir, dir)
	if err != nil {
		return fmt.Errorf("create schedule directory: %w", err)
	}

	record.UpdatedAt = time.Now().UTC()

	err = fs.atomicWrite(filepath.Join(dir, "schedule.json"), encodeIndented(record))
	if err != nil {
		return fmt.Errorf("save schedule: %w", err)
	}

	slog.Debug("Schedule saved", "schedule_id", record.ScheduleID, "path", dir)

	return nil
}

// LoadSchedule reads and validates a schedule record.
func (fs *FSStore) LoadSchedule(scheduleID string) (*ScheduleRecord, error) {
	dir, err := fs.existingScheduleDir(scheduleID)
	if err != nil {
		return nil, err
	}

	record, err := readScheduleRecord(filepath.Join(dir, "schedule.json"), scheduleID)
	if err != nil {
		return nil, err
	}

	return record, nil
}

// readRegularFile refuses a symlink or other non-regular file before reading
// it, the same guard ArtifactPath applies to checkpoint artifacts: a symlink
// dropped into the store must not turn a schedule read into a read of any
// file the server can reach.
func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular or symlink path %q", filepath.Base(path))
	}

	return os.ReadFile(path)
}

func readScheduleRecord(path, scheduleID string) (*ScheduleRecord, error) {
	data, err := readRegularFile(path)
	if os.IsNotExist(err) {
		return nil, &NotFoundError{JobID: scheduleID, Kind: "schedule"}
	}

	if err != nil {
		return nil, fmt.Errorf("read schedule: %w", err)
	}

	var record ScheduleRecord

	err = json.Unmarshal(data, &record)
	if err != nil {
		return nil, fmt.Errorf("deserialize schedule: %w", err)
	}

	if record.ScheduleID != scheduleID {
		return nil, fmt.Errorf("schedule ID %q does not match %q", record.ScheduleID, scheduleID)
	}

	err = record.Validate()
	if err != nil {
		return nil, fmt.Errorf("invalid schedule: %w", err)
	}

	return &record, nil
}

// ListSchedules returns every readable schedule in identifier order.
func (fs *FSStore) ListSchedules() ([]ScheduleRecord, error) {
	root, err := fs.schedulesDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read schedules directory: %w", err)
	}

	records := make([]ScheduleRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || validateScheduleID(entry.Name()) != nil {
			continue
		}

		record, err := readScheduleRecord(filepath.Join(root, entry.Name(), "schedule.json"), entry.Name())
		if err != nil {
			slog.Warn("Failed to load schedule for listing", "schedule_id", entry.Name(), "error", err)
			continue
		}

		records = append(records, *record)
	}

	sort.Slice(records, func(i, j int) bool { return records[i].ScheduleID < records[j].ScheduleID })

	return records, nil
}

// DeleteSchedule removes a schedule and all of its stage records. The jobs the
// schedule realized are untouched: they are independent records that stay
// readable on their own.
func (fs *FSStore) DeleteSchedule(scheduleID string) error {
	dir, err := fs.existingScheduleDir(scheduleID)
	if err != nil {
		return err
	}

	err = ensureContained(fs.baseDir, dir)
	if err != nil {
		return err
	}

	err = os.RemoveAll(dir)
	if err != nil {
		return fmt.Errorf("remove schedule directory: %w", err)
	}

	slog.Debug("Schedule deleted", "schedule_id", scheduleID, "path", dir)

	return nil
}

// SaveScheduleStage atomically writes one stage record, replacing any earlier
// write for the same index.
func (fs *FSStore) SaveScheduleStage(scheduleID string, stage *ScheduleStageRecord) error {
	if stage == nil {
		return errors.New("stage record cannot be nil")
	}

	err := stage.Validate()
	if err != nil {
		return fmt.Errorf("invalid schedule stage: %w", err)
	}

	if stage.ScheduleID != scheduleID {
		return fmt.Errorf("stage schedule ID %q does not match %q", stage.ScheduleID, scheduleID)
	}
	// A stage without its schedule is exactly the orphan record this phase
	// exists to prevent, so it is refused rather than written.
	dir, err := fs.existingScheduleDir(scheduleID)
	if err != nil {
		return err
	}

	stagesDir := filepath.Join(dir, stagesDirName)

	err = ensureSecureDir(fs.baseDir, stagesDir)
	if err != nil {
		return fmt.Errorf("create schedule stages directory: %w", err)
	}

	stage.UpdatedAt = time.Now().UTC()

	path := filepath.Join(stagesDir, stageFileName(stage.Index))

	err = fs.atomicWrite(path, encodeIndented(stage))
	if err != nil {
		return fmt.Errorf("save schedule stage: %w", err)
	}

	slog.Debug("Schedule stage saved", "schedule_id", scheduleID, "index", stage.Index, "state", string(stage.State))

	return nil
}

// LoadScheduleStages returns the recorded stages in index order.
func (fs *FSStore) LoadScheduleStages(scheduleID string) ([]ScheduleStageRecord, error) {
	dir, err := fs.existingScheduleDir(scheduleID)
	if err != nil {
		return nil, err
	}

	stagesDir, err := existingStagesDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(stagesDir)
	if err != nil {
		return nil, fmt.Errorf("read schedule stages directory: %w", err)
	}

	stages := make([]ScheduleStageRecord, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "stage-") || !strings.HasSuffix(name, ".json") {
			continue
		}

		data, err := readRegularFile(filepath.Join(stagesDir, name))
		if err != nil {
			return nil, fmt.Errorf("read schedule stage: %w", err)
		}

		var stage ScheduleStageRecord

		err = json.Unmarshal(data, &stage)
		if err != nil {
			return nil, fmt.Errorf("deserialize schedule stage: %w", err)
		}

		err = stage.Validate()
		if err != nil {
			return nil, fmt.Errorf("invalid schedule stage %s: %w", name, err)
		}

		if stage.ScheduleID != scheduleID {
			return nil, fmt.Errorf("stage schedule ID %q does not match %q", stage.ScheduleID, scheduleID)
		}

		stages = append(stages, stage)
	}

	sort.Slice(stages, func(i, j int) bool { return stages[i].Index < stages[j].Index })

	return stages, nil
}

// LoadScheduleStage returns one recorded stage by index.
//
// A stage is one file, so this reads one file: the cost of asking for a stage
// does not depend on how many stages the campaign has.
func (fs *FSStore) LoadScheduleStage(scheduleID string, index int) (*ScheduleStageRecord, error) {
	if index < 0 {
		return nil, &ValidationError{Field: "index", Reason: "cannot be negative"}
	}

	dir, err := fs.existingScheduleDir(scheduleID)
	if err != nil {
		return nil, err
	}
	// The stages directory is checked before it is descended into, exactly as
	// the listing checks it: ensureContained answers a lexical question, and a
	// symlink swapped in for the directory would redirect the read out of the
	// store while the path still looks contained.
	stagesDir, err := existingStagesDir(dir)
	if os.IsNotExist(err) {
		return nil, &NotFoundError{JobID: scheduleID, Kind: "schedule stage"}
	}

	if err != nil {
		return nil, err
	}

	path := filepath.Join(stagesDir, stageFileName(index))

	err = ensureContained(fs.baseDir, path)
	if err != nil {
		return nil, err
	}

	data, err := readRegularFile(path)
	if os.IsNotExist(err) {
		return nil, &NotFoundError{JobID: scheduleID, Kind: "schedule stage"}
	}

	if err != nil {
		return nil, fmt.Errorf("read schedule stage: %w", err)
	}

	var stage ScheduleStageRecord

	err = json.Unmarshal(data, &stage)
	if err != nil {
		return nil, fmt.Errorf("deserialize schedule stage: %w", err)
	}

	err = stage.Validate()
	if err != nil {
		return nil, fmt.Errorf("invalid schedule stage %d: %w", index, err)
	}

	if stage.ScheduleID != scheduleID {
		return nil, fmt.Errorf("stage schedule ID %q does not match %q", stage.ScheduleID, scheduleID)
	}

	return &stage, nil
}

// existingStagesDir resolves a schedule's stages directory and refuses one that
// is not a real directory. Both readers go through it, so neither can be walked
// into a symlink that points out of the store: a lexical containment check
// cannot see that, because the path it is given still looks contained.
//
// A missing directory is reported as os.ErrNotExist and left to the caller: a
// campaign with no stage yet is an empty listing, but a single stage that was
// asked for by index is not there.
func existingStagesDir(scheduleDir string) (string, error) {
	stagesDir := filepath.Join(scheduleDir, stagesDirName)

	info, err := os.Lstat(stagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", err
		}

		return "", fmt.Errorf("stat schedule stages directory: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("refusing non-directory or symlink stages path")
	}

	return stagesDir, nil
}

// stageFileName keys a stage by its index, zero padded so the on-disk order and
// the stage order agree for anyone reading the directory by hand.
func stageFileName(index int) string {
	return fmt.Sprintf("stage-%06d.json", index)
}

func encodeIndented(value any) func(io.Writer) error {
	return func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")

		return encoder.Encode(value)
	}
}
