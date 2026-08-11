// Package app contains dependency-free application types and validation.
package app

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"time"
)

const (
	MaxCircles       = 1000
	MaxIterations    = 10000
	MinPopulation    = 20
	MaxPopulation    = 200
	MaxBatchSize     = 100
	MaxImagePixels   = 16_777_216
	MaxImageFileSize = 64 << 20
	MaxRequestBody   = 1 << 20
)

// Mode selects how circles are added to the canvas.
type Mode string

const (
	ModeJoint      Mode = "joint"
	ModeSequential Mode = "sequential"
	ModeBatch      Mode = "batch"
)

// Backend selects a rendering implementation.
type Backend string

const (
	BackendCPU    Backend = "cpu"
	BackendOpenCL Backend = "opencl"
)

// Variant selects a Mayfly algorithm variant.
type Variant string

const (
	VariantStandard Variant = "standard"
	VariantDESMA    Variant = "desma"
	VariantOLCE     Variant = "olce"
)

// JobConfig is the canonical configuration shared by all application entry
// points and persisted checkpoints.
type JobConfig struct {
	RefPath              string  `json:"refPath"`
	CanvasPath           string  `json:"canvasPath,omitempty"`
	Mode                 Mode    `json:"mode"`
	Backend              Backend `json:"backend,omitempty"`
	Variant              Variant `json:"variant,omitempty"`
	Circles              int     `json:"circles"`
	Iters                int     `json:"iters"`
	PopSize              int     `json:"popSize"`
	BatchSize            int     `json:"batchSize,omitempty"`
	Threads              int     `json:"threads,omitempty"`
	Seed                 int64   `json:"seed"`
	EffectiveSeed        int64   `json:"effectiveSeed,omitempty"`
	ResumeCount          int     `json:"resumeCount,omitempty"`
	CheckpointInterval   int     `json:"checkpointInterval,omitempty"`
	TraceInterval        int     `json:"traceInterval,omitempty"`
	EnableTrace          bool    `json:"enableTrace,omitempty"`
	DisableTrace         bool    `json:"disableTrace,omitempty"`
	SaveSnapshots        bool    `json:"saveSnapshots,omitempty"`
	ConvergenceEnabled   bool    `json:"convergenceEnabled,omitempty"`
	DisableConvergence   bool    `json:"disableConvergence,omitempty"`
	ConvergencePatience  int     `json:"convergencePatience,omitempty"`
	ConvergenceThreshold float64 `json:"convergenceThreshold,omitempty"`
}

// DefaultConfig returns the canonical defaults. A zero seed is deliberately
// left unresolved until ApplyDefaults so callers can report the chosen seed.
func DefaultConfig() JobConfig {
	return JobConfig{
		Mode:                 ModeJoint,
		Backend:              BackendCPU,
		Variant:              VariantStandard,
		Circles:              10,
		Iters:                100,
		PopSize:              30,
		BatchSize:            5,
		Threads:              runtime.GOMAXPROCS(0),
		EnableTrace:          true,
		ConvergenceEnabled:   true,
		ConvergencePatience:  3,
		ConvergenceThreshold: 0.001,
	}
}

// ApplyDefaults fills omitted values and resolves seed zero to an effective
// random seed. Explicit disable flags distinguish false from an omitted bool.
func (c *JobConfig) ApplyDefaults() error {
	defaults := DefaultConfig()
	if c.Mode == "" {
		c.Mode = defaults.Mode
	}
	if c.Backend == "" {
		c.Backend = defaults.Backend
	}
	if c.Variant == "" {
		c.Variant = defaults.Variant
	}
	if c.Circles == 0 {
		c.Circles = defaults.Circles
	}
	if c.Iters == 0 {
		c.Iters = defaults.Iters
	}
	if c.PopSize == 0 {
		c.PopSize = defaults.PopSize
	}
	if c.BatchSize == 0 {
		c.BatchSize = defaults.BatchSize
		if c.Circles > 0 && c.BatchSize > c.Circles {
			c.BatchSize = c.Circles
		}
	}
	if c.Threads == 0 {
		c.Threads = defaults.Threads
	}
	if !c.EnableTrace && !c.DisableTrace {
		c.EnableTrace = defaults.EnableTrace
	}
	if c.DisableTrace {
		c.EnableTrace = false
	}
	if !c.ConvergenceEnabled && !c.DisableConvergence {
		c.ConvergenceEnabled = defaults.ConvergenceEnabled
	}
	if c.DisableConvergence {
		c.ConvergenceEnabled = false
	}
	if c.ConvergencePatience == 0 {
		c.ConvergencePatience = defaults.ConvergencePatience
	}
	if c.ConvergenceThreshold == 0 {
		c.ConvergenceThreshold = defaults.ConvergenceThreshold
	}
	if c.EffectiveSeed == 0 {
		if c.Seed != 0 {
			c.EffectiveSeed = c.Seed
		} else {
			seed, err := randomSeed()
			if err != nil {
				return fmt.Errorf("resolve random seed: %w", err)
			}
			c.EffectiveSeed = seed
		}
	}
	return nil
}

// Validate returns a field-specific error for unsafe or inconsistent values.
func (c JobConfig) Validate() error {
	if c.RefPath == "" {
		return invalid("refPath", "is required")
	}
	switch c.Mode {
	case ModeJoint, ModeSequential, ModeBatch:
	default:
		return invalid("mode", "must be joint, sequential, or batch")
	}
	switch c.Backend {
	case BackendCPU, BackendOpenCL:
	default:
		return invalid("backend", "must be cpu or opencl")
	}
	switch c.Variant {
	case VariantStandard, VariantDESMA, VariantOLCE:
	default:
		return invalid("variant", "is unsupported")
	}
	if c.Circles < 1 || c.Circles > MaxCircles {
		return invalid("circles", fmt.Sprintf("must be between 1 and %d", MaxCircles))
	}
	if c.Iters < 1 || c.Iters > MaxIterations {
		return invalid("iters", fmt.Sprintf("must be between 1 and %d", MaxIterations))
	}
	if c.PopSize < MinPopulation || c.PopSize > MaxPopulation {
		return invalid("popSize", fmt.Sprintf("must be between %d and %d", MinPopulation, MaxPopulation))
	}
	if c.BatchSize < 1 || c.BatchSize > MaxBatchSize || c.Mode == ModeBatch && c.BatchSize > c.Circles {
		return invalid("batchSize", "must be positive, within the limit, and no larger than circles")
	}
	if c.Threads < 1 {
		return invalid("threads", "must be positive")
	}
	if c.CheckpointInterval < 0 {
		return invalid("checkpointInterval", "cannot be negative")
	}
	if c.TraceInterval < 0 {
		return invalid("traceInterval", "cannot be negative")
	}
	if c.ConvergencePatience < 1 || c.ConvergencePatience > 100 {
		return invalid("convergencePatience", "must be between 1 and 100")
	}
	if math.IsNaN(c.ConvergenceThreshold) || math.IsInf(c.ConvergenceThreshold, 0) || c.ConvergenceThreshold < 0 || c.ConvergenceThreshold > 1 {
		return invalid("convergenceThreshold", "must be finite and between 0 and 1")
	}
	if c.ResumeCount < 0 {
		return invalid("resumeCount", "cannot be negative")
	}
	return nil
}

// Normalize applies defaults and validates a configuration in one operation.
func Normalize(config JobConfig) (JobConfig, error) {
	if err := config.ApplyDefaults(); err != nil {
		return JobConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return JobConfig{}, err
	}
	return config, nil
}

// ValidateImageDimensions rejects empty images and dimensions that exceed the
// decoded-pixel budget before application-owned image buffers are allocated.
func ValidateImageDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return invalid("image", "dimensions must be positive")
	}
	if width > MaxImagePixels/height {
		return invalid("image", fmt.Sprintf("exceeds the %d pixel limit", MaxImagePixels))
	}
	return nil
}

// ValidationError identifies a rejected configuration field without exposing
// internal filesystem or implementation details.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + " " + e.Reason }

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

func randomSeed() (int64, error) {
	var data [8]byte
	if _, err := cryptorand.Read(data[:]); err != nil {
		return 0, err
	}
	seed := int64(binary.LittleEndian.Uint64(data[:]) & math.MaxInt64)
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	if seed == 0 {
		return 0, errors.New("random source produced a zero seed")
	}
	return seed, nil
}
