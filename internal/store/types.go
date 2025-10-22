package store

import (
	"fmt"
	"time"
)

// JobConfig holds configuration for an optimization job (checkpoint copy).
// This avoids import cycles with server package.
type JobConfig struct {
	RefPath            string `json:"refPath"`
	Mode               string `json:"mode"` // joint, sequential, batch
	Circles            int    `json:"circles"`
	Iters              int    `json:"iters"`
	PopSize            int    `json:"popSize"`
	Seed               int64  `json:"seed"`
	CheckpointInterval int    `json:"checkpointInterval,omitempty"` // Checkpoint every N seconds (0 = disabled)
}

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
//   1. Seed the population with the best parameters + random variations
//   2. Continue iteration count from checkpoint (or reset to 0)
//   3. Use the same random seed if deterministic behavior is desired
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
	// JobID is the unique identifier for this optimization job
	JobID string `json:"jobId"`

	// BestParams contains the circle parameters (7 per circle: X, Y, R, CR, CG, CB, Opacity)
	// that produced the best (lowest) cost so far
	BestParams []float64 `json:"bestParams"`

	// BestCost is the cost value (MSE) achieved by BestParams
	BestCost float64 `json:"bestCost"`

	// InitialCost is the starting cost (usually white canvas) for tracking improvement
	InitialCost float64 `json:"initialCost"`

	// Iteration is the current iteration count when this checkpoint was created
	Iteration int `json:"iteration"`

	// Timestamp records when this checkpoint was created
	Timestamp time.Time `json:"timestamp"`

	// Config holds the job configuration, needed for validation during resume.
	// We ensure that resumed jobs use compatible settings (same image, mode, etc.)
	Config JobConfig `json:"config"`
}

// CheckpointInfo contains metadata about a checkpoint without the full parameter data.
// Used for listing checkpoints efficiently without loading large parameter arrays.
type CheckpointInfo struct {
	// JobID is the unique identifier for this checkpoint
	JobID string `json:"jobId"`

	// BestCost is the cost achieved at the time of checkpointing
	BestCost float64 `json:"bestCost"`

	// Iteration is the iteration count at checkpoint time
	Iteration int `json:"iteration"`

	// Timestamp records when this checkpoint was created
	Timestamp time.Time `json:"timestamp"`

	// Mode is the optimization mode (joint, sequential, batch)
	Mode string `json:"mode"`

	// Circles is the number of circles (K) being optimized
	Circles int `json:"circles"`

	// RefPath is the reference image path
	RefPath string `json:"refPath"`
}

// NewCheckpoint creates a checkpoint from job state.
// This is a helper for converting runtime job state to a persistable checkpoint.
func NewCheckpoint(jobID string, bestParams []float64, bestCost, initialCost float64, iteration int, config JobConfig) *Checkpoint {
	return &Checkpoint{
		JobID:       jobID,
		BestParams:  bestParams,
		BestCost:    bestCost,
		InitialCost: initialCost,
		Iteration:   iteration,
		Timestamp:   time.Now(),
		Config:      config,
	}
}

// ToInfo converts a full Checkpoint to CheckpointInfo (metadata only).
func (c *Checkpoint) ToInfo() CheckpointInfo {
	return CheckpointInfo{
		JobID:     c.JobID,
		BestCost:  c.BestCost,
		Iteration: c.Iteration,
		Timestamp: c.Timestamp,
		Mode:      c.Config.Mode,
		Circles:   c.Config.Circles,
		RefPath:   c.Config.RefPath,
	}
}

// Validate checks if the checkpoint has valid data.
// Returns an error if any required field is missing or invalid.
func (c *Checkpoint) Validate() error {
	if c.JobID == "" {
		return &ValidationError{Field: "JobID", Reason: "cannot be empty"}
	}
	if c.BestParams == nil {
		return &ValidationError{Field: "BestParams", Reason: "cannot be nil"}
	}
	if len(c.BestParams) == 0 {
		return &ValidationError{Field: "BestParams", Reason: "cannot be empty"}
	}
	// BestParams should be a multiple of 7 (7 params per circle)
	if len(c.BestParams)%7 != 0 {
		return &ValidationError{Field: "BestParams", Reason: "length must be multiple of 7"}
	}
	if c.BestCost < 0 {
		return &ValidationError{Field: "BestCost", Reason: "cannot be negative"}
	}
	if c.InitialCost < 0 {
		return &ValidationError{Field: "InitialCost", Reason: "cannot be negative"}
	}
	if c.Iteration < 0 {
		return &ValidationError{Field: "Iteration", Reason: "cannot be negative"}
	}
	if c.Timestamp.IsZero() {
		return &ValidationError{Field: "Timestamp", Reason: "cannot be zero"}
	}
	if c.Config.RefPath == "" {
		return &ValidationError{Field: "Config.RefPath", Reason: "cannot be empty"}
	}
	if c.Config.Mode == "" {
		return &ValidationError{Field: "Config.Mode", Reason: "cannot be empty"}
	}
	if c.Config.Circles <= 0 {
		return &ValidationError{Field: "Config.Circles", Reason: "must be positive"}
	}
	if c.Config.Iters <= 0 {
		return &ValidationError{Field: "Config.Iters", Reason: "must be positive"}
	}
	if c.Config.PopSize <= 0 {
		return &ValidationError{Field: "Config.PopSize", Reason: "must be positive"}
	}
	// Verify BestParams length matches expected circles
	expectedParams := c.Config.Circles * 7
	if len(c.BestParams) != expectedParams {
		return &ValidationError{
			Field:  "BestParams",
			Reason: fmt.Sprintf("length mismatch: expected %d params for %d circles", expectedParams, c.Config.Circles),
		}
	}
	return nil
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
			Expected: c.Config.Mode,
			Actual:   config.Mode,
		}
	}
	if c.Config.Circles != config.Circles {
		return &CompatibilityError{
			Field:    "Circles",
			Expected: fmt.Sprintf("%d", c.Config.Circles),
			Actual:   fmt.Sprintf("%d", config.Circles),
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
