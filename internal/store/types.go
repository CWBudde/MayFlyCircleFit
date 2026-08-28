package store

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/opt"
)

// CircleData represents a single optimized circle with its parameters and metadata.
// Used for exporting circle-by-circle data in sequential optimization mode.
type CircleData struct {
	CircleNum int       `json:"circleNum"` // 1-indexed circle number
	X         float64   `json:"x"`         // Horizontal position
	Y         float64   `json:"y"`         // Vertical position
	R         float64   `json:"r"`         // Radius
	CR        float64   `json:"cr"`        // Red channel [0, 1]
	CG        float64   `json:"cg"`        // Green channel [0, 1]
	CB        float64   `json:"cb"`        // Blue channel [0, 1]
	Opacity   float64   `json:"opacity"`   // Alpha [0, 1]
	CostAfter float64   `json:"costAfter"` // Cost after adding this circle
	Timestamp time.Time `json:"timestamp"` // When this circle was optimized
}

// JobConfig holds configuration for an optimization job (checkpoint copy).
// This avoids import cycles with server package.
type JobConfig = app.JobConfig

const (
	// CheckpointSchemaVersion is the checkpoint format written by this version.
	CheckpointSchemaVersion = 2

	TerminationUnknown = "unknown"
	TerminationLegacy  = "legacy"
)

// Checkpoint represents a saved optimization state that can be resumed later.
// All fields are serialized to JSON for persistence.
//
// Optimizer State Handling:
//
// The checkpoint saves the BEST PARAMETERS found so far, but does NOT save
// the internal optimizer state (population, velocities, etc.). This design choice
// has important implications for resumption:
//
// SAVED STATE:
//   - BestParams: The circle parameters that achieved the lowest cost
//   - BestCost: The cost value achieved by BestParams
//   - InitialCost: Starting cost for improvement tracking
//   - Iteration: How many iterations have been completed
//   - Config: Job configuration (reference image, mode, circles, etc.)
//
// REINITIALIZED ON RESUME:
//   - Optimizer population: New random population is generated
//   - Optimizer internal state: Velocities, positions, etc. are reset
//   - Random seed: Can be set to same value for reproducibility
//
// RESUME STRATEGY:
// When resuming, the optimizer is restarted with a fresh population, but we can:
//  1. Seed the population with the best parameters + random variations
//  2. Continue iteration count from checkpoint (or reset to 0)
//  3. Use the same random seed if deterministic behavior is desired
//
// IMPLICATIONS:
//   - Resume is not a perfect continuation - there will be some divergence
//   - The best cost should never get worse (we keep best params)
//   - Convergence speed may differ slightly from non-interrupted runs
//   - For most use cases, this is acceptable and keeps implementation simple
//
// ALTERNATIVES NOT IMPLEMENTED:
//   - Saving full population would require optimizer-specific serialization
//   - Different optimizers have different internal state structures
//   - Would significantly increase checkpoint size
//   - Would tie checkpoint format to specific optimizer implementations
type Checkpoint struct {
	// SchemaVersion identifies the persisted checkpoint format.
	SchemaVersion int `json:"schemaVersion"`

	// JobID is the unique identifier for this optimization job
	JobID string `json:"jobId"`

	// BestParams contains the circle parameters (7 per circle: X, Y, R, CR, CG, CB, Opacity)
	// that produced the best (lowest) cost so far
	BestParams []float64 `json:"bestParams"`

	// BestCost is the cost value (MSE) achieved by BestParams
	BestCost float64 `json:"bestCost"`

	// InitialCost is the starting cost (usually white canvas) for tracking improvement
	InitialCost float64 `json:"initialCost"`

	// RequestedCircles is the target number of circles. ActualCircles is the
	// number currently represented by BestParams, which may be lower for a
	// checkpoint taken during sequential optimization.
	RequestedCircles int `json:"requestedCircles"`
	ActualCircles    int `json:"actualCircles"`

	// EffectiveSeed is the resolved, non-zero seed used by the optimizer.
	// ResumeCount records how many times this job has been resumed.
	EffectiveSeed int64 `json:"effectiveSeed"`
	ResumeCount   int   `json:"resumeCount"`

	// Iterations and Evaluations are cumulative optimizer progress counters.
	Iterations  int   `json:"iterations"`
	Evaluations int64 `json:"evaluations"`

	// Termination records why the run stopped (for example completed,
	// converged, cancelled, or failed). In-progress checkpoints use unknown.
	Termination string `json:"termination"`

	// Restarts records each independent run of every restart schedule the job
	// ran, in pipeline-stage then restart order. It is empty for a job whose
	// optimizer had no restart schedule, which is every Mayfly and Dragonfly
	// run and every CMA-ES run configured with restartStrategy none.
	//
	// It exists because Termination cannot answer anything about a restart
	// arm: the schedule reports its budget-exhausted reason whenever the
	// shared evaluation budget is spent, so an arm sized to consume its budget
	// records "completed" however its individual runs ended. Only these
	// records say whether the schedule was harvesting converged runs or paying
	// for runs that had stopped progressing.
	//
	// It is additive and optional, like OptimizerVersion: a checkpoint written
	// before the field existed decodes with it empty, which is also what a
	// non-restart job records, so the schema version does not move.
	Restarts []opt.RestartRun `json:"restarts,omitempty"`

	// Iteration is a deprecated in-memory alias retained for source
	// compatibility. Version 2 JSON uses Iterations.
	Iteration int `json:"-"`

	// Timestamp records when this checkpoint was created
	Timestamp time.Time `json:"timestamp"`

	// ExtendedFrom and PolishedFrom name the completed job this one continued
	// from, and at most one of them is set. They exist so a chain is
	// reconstructible from the job tree alone: the extend and polish endpoints
	// each mint a fresh job id and used to report the parent only in the HTTP
	// response, which left a multi-hour campaign as a pile of unrelated job
	// records that only an external ledger could read back.
	//
	// Both are additive and optional. A checkpoint written before they existed
	// decodes with them empty, which is also what a job started from scratch
	// records, so the schema version does not move.
	ExtendedFrom string `json:"extendedFrom,omitempty"`
	PolishedFrom string `json:"polishedFrom,omitempty"`

	// ScheduleID and StageIndex place the job inside a declarative schedule.
	// They are the reverse of the schedule's own stage records and are what lets
	// a restarting server decide whether an unfinished job already belongs to a
	// campaign. A job created by hand leaves both empty.
	ScheduleID string `json:"scheduleId,omitempty"`
	StageIndex *int   `json:"stageIndex,omitempty"`

	// OptimizerVersion records the version of the optimizer library that
	// produced this checkpoint, which is the library Config.Optimizer names.
	// The optimizer version is a comparability boundary — v0.5.0
	// scales the crossover offspring count with the population, v0.5.1 restores
	// blend crossover — so resuming across a bump continues the run under an
	// algorithm the recorded cost was never measured with. Persisting it lets
	// the resume paths refuse that instead of doing it silently.
	//
	// It is additive and optional: every checkpoint written before the field
	// existed decodes with it empty, which resume treats as unknown rather than
	// as a mismatch, so the schema version does not move.
	OptimizerVersion string `json:"optimizerVersion,omitempty"`

	// Config holds the job configuration, needed for validation during resume.
	// We ensure that resumed jobs use compatible settings (same image, mode, etc.)
	Config JobConfig `json:"config"`
}

// ContinuedFrom reports the job this checkpoint continues, if any, without the
// caller having to know which of the two continuation kinds produced it.
func (c *Checkpoint) ContinuedFrom() (string, bool) {
	switch {
	case c == nil:
		return "", false
	case c.ExtendedFrom != "":
		return c.ExtendedFrom, true
	case c.PolishedFrom != "":
		return c.PolishedFrom, true
	default:
		return "", false
	}
}

// CheckpointInfo contains metadata about a checkpoint without the full parameter data.
// Used for listing checkpoints efficiently without loading large parameter arrays.
type CheckpointInfo struct {
	SchemaVersion int `json:"schemaVersion"`

	// JobID is the unique identifier for this checkpoint
	JobID string `json:"jobId"`

	// BestCost is the cost achieved at the time of checkpointing
	BestCost float64 `json:"bestCost"`

	// Iteration is the iteration count at checkpoint time
	Iteration int `json:"iteration"`

	Evaluations      int64  `json:"evaluations"`
	RequestedCircles int    `json:"requestedCircles"`
	ActualCircles    int    `json:"actualCircles"`
	EffectiveSeed    int64  `json:"effectiveSeed"`
	ResumeCount      int    `json:"resumeCount"`
	Termination      string `json:"termination"`

	// Timestamp records when this checkpoint was created
	Timestamp time.Time `json:"timestamp"`

	// ExtendedFrom, PolishedFrom, and ScheduleID mirror the checkpoint lineage so
	// a chain can be walked from a listing without loading every checkpoint.
	ExtendedFrom string `json:"extendedFrom,omitempty"`
	PolishedFrom string `json:"polishedFrom,omitempty"`
	ScheduleID   string `json:"scheduleId,omitempty"`

	// OptimizerVersion mirrors the checkpoint's recorded optimizer version so a
	// listing can show which records sit on the far side of a bump. It is empty
	// for a checkpoint written before the field existed.
	OptimizerVersion string `json:"optimizerVersion,omitempty"`

	// Mode is the optimization mode (joint, sequential, batch)
	Mode app.Mode `json:"mode"`

	// Circles is the number of circles (K) being optimized
	Circles int `json:"circles"`

	// RefPath is the reference image path
	RefPath string `json:"refPath"`
}

// NewCheckpoint creates a checkpoint from job state.
// This is a helper for converting runtime job state to a persistable checkpoint.
func NewCheckpoint(jobID string, bestParams []float64, bestCost, initialCost float64, iteration int, config JobConfig) *Checkpoint {
	checkpoint := Checkpoint{
		SchemaVersion:    CheckpointSchemaVersion,
		JobID:            jobID,
		BestParams:       append([]float64(nil), bestParams...),
		BestCost:         bestCost,
		InitialCost:      initialCost,
		RequestedCircles: config.Circles,
		ActualCircles:    len(bestParams) / 7,
		EffectiveSeed:    effectiveSeed(config),
		ResumeCount:      config.ResumeCount,
		Iterations:       iteration,
		Iteration:        iteration,
		Termination:      TerminationUnknown,
		Timestamp:        time.Now(),
		OptimizerVersion: optimizerVersion(config),
		Config:           config,
	}

	return &checkpoint
}

// optimizerVersion reports the version of the library the configuration's
// engine runs with, so a checkpoint records the optimizer that actually
// produced it rather than MayFly's version unconditionally.
func optimizerVersion(config JobConfig) string {
	switch config.ResolvedOptimizer() {
	case app.OptimizerDragonfly:
		return opt.DragonflyLibraryVersion()
	case app.OptimizerCMAES:
		return opt.CMAESLibraryVersion()
	}

	return opt.LibraryVersion()
}

// ToInfo converts a full Checkpoint to CheckpointInfo (metadata only).
func (c *Checkpoint) ToInfo() CheckpointInfo {
	normalized := c.normalizedMetadata()

	return CheckpointInfo{
		SchemaVersion:    normalized.SchemaVersion,
		JobID:            normalized.JobID,
		BestCost:         normalized.BestCost,
		Iteration:        normalized.Iterations,
		Evaluations:      normalized.Evaluations,
		RequestedCircles: normalized.RequestedCircles,
		ActualCircles:    normalized.ActualCircles,
		EffectiveSeed:    normalized.EffectiveSeed,
		ResumeCount:      normalized.ResumeCount,
		Termination:      normalized.Termination,
		Timestamp:        normalized.Timestamp,
		ExtendedFrom:     normalized.ExtendedFrom,
		PolishedFrom:     normalized.PolishedFrom,
		ScheduleID:       normalized.ScheduleID,
		OptimizerVersion: normalized.OptimizerVersion,
		Mode:             normalized.Config.Mode,
		Circles:          normalized.Config.Circles,
		RefPath:          normalized.Config.RefPath,
	}
}

// Validate checks if the checkpoint has valid data.
// Returns an error if any required field is missing or invalid.
func (c *Checkpoint) Validate() error {
	if c == nil {
		return &ValidationError{Field: "Checkpoint", Reason: "cannot be nil"}
	}

	normalized := c.normalized()

	err := validateJobID(normalized.JobID)
	if err != nil {
		return &ValidationError{Field: "JobID", Reason: err.Error()}
	}

	if normalized.SchemaVersion != CheckpointSchemaVersion {
		return &ValidationError{Field: "SchemaVersion", Reason: fmt.Sprintf("must be %d", CheckpointSchemaVersion)}
	}

	if normalized.BestParams == nil {
		return &ValidationError{Field: "BestParams", Reason: "cannot be nil"}
	}

	if len(normalized.BestParams) == 0 {
		return &ValidationError{Field: "BestParams", Reason: "cannot be empty"}
	}
	// BestParams should be a multiple of 7 (7 params per circle)
	if len(normalized.BestParams)%7 != 0 {
		return &ValidationError{Field: "BestParams", Reason: "length must be multiple of 7"}
	}

	if normalized.BestCost < 0 {
		return &ValidationError{Field: "BestCost", Reason: "cannot be negative"}
	}

	if normalized.InitialCost < 0 {
		return &ValidationError{Field: "InitialCost", Reason: "cannot be negative"}
	}

	if normalized.Iterations < 0 {
		return &ValidationError{Field: "Iterations", Reason: "cannot be negative"}
	}

	if normalized.Evaluations < 0 {
		return &ValidationError{Field: "Evaluations", Reason: "cannot be negative"}
	}

	if normalized.ResumeCount < 0 {
		return &ValidationError{Field: "ResumeCount", Reason: "cannot be negative"}
	}

	if normalized.Timestamp.IsZero() {
		return &ValidationError{Field: "Timestamp", Reason: "cannot be zero"}
	}

	if normalized.Config.RefPath == "" {
		return &ValidationError{Field: "Config.RefPath", Reason: "cannot be empty"}
	}

	if normalized.Config.Mode == "" {
		return &ValidationError{Field: "Config.Mode", Reason: "cannot be empty"}
	}

	if normalized.Config.Circles <= 0 {
		return &ValidationError{Field: "Config.Circles", Reason: "must be positive"}
	}

	if normalized.Config.Iters <= 0 {
		return &ValidationError{Field: "Config.Iters", Reason: "must be positive"}
	}

	if normalized.Config.PopSize <= 0 {
		return &ValidationError{Field: "Config.PopSize", Reason: "must be positive"}
	}

	if normalized.RequestedCircles != normalized.Config.Circles {
		return &ValidationError{Field: "RequestedCircles", Reason: "must match Config.Circles"}
	}

	if normalized.ActualCircles < 1 || normalized.ActualCircles > normalized.RequestedCircles {
		return &ValidationError{Field: "ActualCircles", Reason: "must be positive and no greater than RequestedCircles"}
	}

	err = normalized.validateLineage()
	if err != nil {
		return err
	}
	// Verify BestParams length matches the actual materialized circles.
	expectedParams := normalized.ActualCircles * 7
	if len(normalized.BestParams) != expectedParams {
		return &ValidationError{
			Field:  "BestParams",
			Reason: fmt.Sprintf("length mismatch: expected %d params for %d actual circles", expectedParams, normalized.ActualCircles),
		}
	}

	return nil
}

// validateLineage keeps a recorded chain trustworthy. A checkpoint that names
// two parents, or names itself, describes a chain that cannot be walked, and a
// stage index without a schedule points at nothing.
func (c Checkpoint) validateLineage() error {
	if c.ExtendedFrom != "" && c.PolishedFrom != "" {
		return &ValidationError{Field: "PolishedFrom", Reason: "cannot be set together with ExtendedFrom"}
	}

	for field, parent := range map[string]string{"ExtendedFrom": c.ExtendedFrom, "PolishedFrom": c.PolishedFrom} {
		if parent == "" {
			continue
		}

		err := validateJobID(parent)
		if err != nil {
			return &ValidationError{Field: field, Reason: err.Error()}
		}

		if parent == c.JobID {
			return &ValidationError{Field: field, Reason: "cannot name the checkpoint's own job"}
		}
	}

	if c.ScheduleID != "" {
		err := validateScheduleID(c.ScheduleID)
		if err != nil {
			return &ValidationError{Field: "ScheduleID", Reason: err.Error()}
		}
	}

	if c.StageIndex != nil {
		if c.ScheduleID == "" {
			return &ValidationError{Field: "StageIndex", Reason: "requires ScheduleID"}
		}

		if *c.StageIndex < 0 {
			return &ValidationError{Field: "StageIndex", Reason: "cannot be negative"}
		}
	}

	return nil
}

// MarshalJSON always emits the current schema, including normalized progress
// metadata. It does not mutate the caller's checkpoint.
func (c Checkpoint) MarshalJSON() ([]byte, error) {
	normalized := c.normalized()
	return json.Marshal(checkpointWireFrom(normalized))
}

// UnmarshalJSON accepts schema v2 and migrates legacy v1 checkpoints whose
// schemaVersion is either missing or explicitly 1.
func (c *Checkpoint) UnmarshalJSON(data []byte) error {
	var wire checkpointWire

	err := json.Unmarshal(data, &wire)
	if err != nil {
		return err
	}

	if wire.SchemaVersion < 0 || wire.SchemaVersion > CheckpointSchemaVersion {
		return fmt.Errorf("unsupported checkpoint schema version %d", wire.SchemaVersion)
	}

	if wire.SchemaVersion != 0 && wire.SchemaVersion != 1 && wire.SchemaVersion != CheckpointSchemaVersion {
		return fmt.Errorf("unsupported checkpoint schema version %d", wire.SchemaVersion)
	}

	iterations := wire.Iterations
	if iterations == 0 && wire.Iteration != 0 {
		iterations = wire.Iteration
	}

	*c = Checkpoint{
		SchemaVersion:    CheckpointSchemaVersion,
		JobID:            wire.JobID,
		BestParams:       append([]float64(nil), wire.BestParams...),
		BestCost:         wire.BestCost,
		InitialCost:      wire.InitialCost,
		RequestedCircles: wire.RequestedCircles,
		ActualCircles:    wire.ActualCircles,
		EffectiveSeed:    wire.EffectiveSeed,
		ResumeCount:      wire.ResumeCount,
		Iterations:       iterations,
		Evaluations:      wire.Evaluations,
		Termination:      wire.Termination,
		Restarts:         append([]opt.RestartRun(nil), wire.Restarts...),
		Iteration:        iterations,
		Timestamp:        wire.Timestamp,
		ExtendedFrom:     wire.ExtendedFrom,
		PolishedFrom:     wire.PolishedFrom,
		ScheduleID:       wire.ScheduleID,
		StageIndex:       wire.StageIndex,
		OptimizerVersion: wire.OptimizerVersion,
		Config:           wire.Config,
	}
	legacy := wire.SchemaVersion == 0 || wire.SchemaVersion == 1

	*c = c.normalized()
	if legacy {
		if c.Evaluations == 0 && c.Iterations > 0 && c.Config.PopSize > 0 {
			c.Evaluations = int64(c.Iterations) * int64(c.Config.PopSize)
		}

		if wire.Termination == "" {
			c.Termination = TerminationLegacy
		}
	}

	return nil
}

type checkpointWire struct {
	SchemaVersion    int              `json:"schemaVersion"`
	JobID            string           `json:"jobId"`
	BestParams       []float64        `json:"bestParams"`
	BestCost         float64          `json:"bestCost"`
	InitialCost      float64          `json:"initialCost"`
	RequestedCircles int              `json:"requestedCircles"`
	ActualCircles    int              `json:"actualCircles"`
	EffectiveSeed    int64            `json:"effectiveSeed"`
	ResumeCount      int              `json:"resumeCount"`
	Iterations       int              `json:"iterations"`
	Evaluations      int64            `json:"evaluations"`
	Termination      string           `json:"termination"`
	Restarts         []opt.RestartRun `json:"restarts,omitempty"`
	Iteration        int              `json:"iteration,omitempty"`
	Timestamp        time.Time        `json:"timestamp"`
	ExtendedFrom     string           `json:"extendedFrom,omitempty"`
	PolishedFrom     string           `json:"polishedFrom,omitempty"`
	ScheduleID       string           `json:"scheduleId,omitempty"`
	StageIndex       *int             `json:"stageIndex,omitempty"`
	OptimizerVersion string           `json:"optimizerVersion,omitempty"`
	Config           JobConfig        `json:"config"`
}

func checkpointWireFrom(c Checkpoint) checkpointWire {
	return checkpointWire{
		SchemaVersion:    c.SchemaVersion,
		JobID:            c.JobID,
		BestParams:       c.BestParams,
		BestCost:         c.BestCost,
		InitialCost:      c.InitialCost,
		RequestedCircles: c.RequestedCircles,
		ActualCircles:    c.ActualCircles,
		EffectiveSeed:    c.EffectiveSeed,
		ResumeCount:      c.ResumeCount,
		Iterations:       c.Iterations,
		Evaluations:      c.Evaluations,
		Termination:      c.Termination,
		Restarts:         c.Restarts,
		Timestamp:        c.Timestamp,
		ExtendedFrom:     c.ExtendedFrom,
		PolishedFrom:     c.PolishedFrom,
		ScheduleID:       c.ScheduleID,
		StageIndex:       c.StageIndex,
		OptimizerVersion: c.OptimizerVersion,
		Config:           c.Config,
	}
}

func (c Checkpoint) normalized() Checkpoint {
	c = c.normalizedMetadata()

	c.BestParams = append([]float64(nil), c.BestParams...)
	if c.StageIndex != nil {
		index := *c.StageIndex
		c.StageIndex = &index
	}

	return c
}

// normalizedMetadata applies schema migration without copying the parameter
// vector. Metadata-only readers must not pay O(circles) merely to report a
// checkpoint in a listing.
func (c Checkpoint) normalizedMetadata() Checkpoint {
	if c.SchemaVersion == 0 || c.SchemaVersion == 1 {
		c.SchemaVersion = CheckpointSchemaVersion
	}

	if c.Iterations == 0 && c.Iteration != 0 {
		c.Iterations = c.Iteration
	}

	c.Iteration = c.Iterations
	if c.RequestedCircles == 0 {
		c.RequestedCircles = c.Config.Circles
	}

	if c.ActualCircles == 0 && len(c.BestParams)%7 == 0 {
		c.ActualCircles = len(c.BestParams) / 7
	}

	if c.EffectiveSeed == 0 {
		c.EffectiveSeed = effectiveSeed(c.Config)
	}

	if c.ResumeCount == 0 {
		c.ResumeCount = c.Config.ResumeCount
	}

	if c.Termination == "" {
		c.Termination = TerminationUnknown
	}

	c.Config.EffectiveSeed = c.EffectiveSeed
	c.Config.ResumeCount = c.ResumeCount

	return c
}

func effectiveSeed(config JobConfig) int64 {
	if config.EffectiveSeed != 0 {
		return config.EffectiveSeed
	}

	return config.Seed
}

// ValidationError represents a checkpoint validation error.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return "validation error: " + e.Field + " " + e.Reason
}

// IsCompatible checks if this checkpoint can be resumed with the given config.
// Returns an error if the configs are incompatible.
func (c *Checkpoint) IsCompatible(config JobConfig) error {
	if c.Config.RefPath != config.RefPath {
		return &CompatibilityError{
			Field:    "RefPath",
			Expected: c.Config.RefPath,
			Actual:   config.RefPath,
		}
	}

	if c.Config.Mode != config.Mode {
		return &CompatibilityError{
			Field:    "Mode",
			Expected: string(c.Config.Mode),
			Actual:   string(config.Mode),
		}
	}

	if c.Config.Circles != config.Circles {
		return &CompatibilityError{
			Field:    "Circles",
			Expected: strconv.Itoa(c.Config.Circles),
			Actual:   strconv.Itoa(config.Circles),
		}
	}

	return nil
}

// CompatibilityError represents a checkpoint compatibility error.
type CompatibilityError struct {
	Field    string
	Expected string
	Actual   string
}

func (e *CompatibilityError) Error() string {
	return "compatibility error: " + e.Field + " mismatch (expected " + e.Expected + ", got " + e.Actual + ")"
}

// ParamVectorToCircles decomposes a flat parameter vector into individual CircleData structs.
// The params slice should contain 7 values per circle: X, Y, R, CR, CG, CB, Opacity.
// Returns a slice of CircleData with circleNum starting from 1.
// The costAfter and timestamp fields are left at zero values and should be populated by the caller.
func ParamVectorToCircles(params []float64) ([]CircleData, error) {
	if len(params)%7 != 0 {
		return nil, fmt.Errorf("invalid params length %d: must be multiple of 7", len(params))
	}

	numCircles := len(params) / 7
	circles := make([]CircleData, numCircles)

	for i := range numCircles {
		offset := i * 7
		circles[i] = CircleData{
			CircleNum: i + 1, // 1-indexed
			X:         params[offset+0],
			Y:         params[offset+1],
			R:         params[offset+2],
			CR:        params[offset+3],
			CG:        params[offset+4],
			CB:        params[offset+5],
			Opacity:   params[offset+6],
			// CostAfter and Timestamp are zero values - caller should populate
		}
	}

	return circles, nil
}
