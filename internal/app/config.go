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
	MaxCircles         = 1000
	MaxIterations      = 10000
	MinPopulation      = 20
	MaxPopulation      = 200
	MaxOptimizerEpochs = 32
	MaxBatchSize       = 100
	MaxPolishingSweeps = 32
	MaxImagePixels     = 16_777_216
	MaxImageFileSize   = 64 << 20
	MaxRequestBody     = 1 << 20
	MaxProjectSlugLen  = 64
)

// The polishing budget defaults are a measurement, not an inheritance. Each is
// named rather than written inline because the CLI flag default and the
// configuration default have to agree, and because the number came from a run
// that is worth being able to find: see docs/polishing-budget-report.md.
//
// The short version, on the 64-circle fitted vector that report measures: a
// sweep optimizes one active set of 35 parameters, so a population sized for the
// whole vector buys nothing -- 200 costs 5.2x the wall clock of 30 for 1.28x the
// error removed -- and neither does a long epoch, whose return per second falls
// monotonically past ~100 iterations. Sweeps are the only axis that moves
// polishing onto different circles, and the per-sweep gain was still at full
// rate at sweep four. Together these defaults removed 2.6x the error of the
// previous ones in 56% of the wall clock.
const (
	DefaultPolishingPopSize         = 30
	DefaultPolishingIters           = 200
	DefaultPolishingEpochs          = 2
	DefaultPolishingMaxSweeps       = 8
	DefaultPolishingStagnationIters = 100
)

// Project is a project slug. It is a named string type so the compiler can tell
// a slug apart from the other bare strings that travel beside it — a job ID, a
// reference image path, a termination reason. A named string type marshals to
// and from JSON exactly like a string, so the wire format is unaffected.
//
// The type carries no validation guarantee by itself: a Project produced by
// converting untrusted input is only trustworthy once ValidateProjectSlug has
// accepted it. What it does buy is that such a conversion has to be written
// out, so an unvalidated string cannot drift inward unnoticed.
type Project string

// DefaultProject is the slug used when a job does not name a project. It also
// names the legacy `<data-root>/jobs` tree that predates project support, so
// existing installations keep listing their jobs without a migration.
const DefaultProject Project = "default"

// reservedProjectSlugs are names a project may not take. Two reasons apply:
//
//   - "all" is the wire sentinel for "do not filter". The jobs API reads
//     `?project=all` as a request for every job in every project, so a project
//     actually named "all" would be created and filled normally and then be
//     permanently unfilterable.
//   - "jobs", "projects", and "saved" collide with nothing today, because every
//     project directory lives one level down in `<data-root>/projects/<slug>`.
//     They are held back defensively: they name components of the on-disk layout
//     `<data-root>/projects/<slug>/jobs/<uuid>`, so a path built from them reads
//     as directory structure rather than as a project, and flattening the layout
//     later would turn them into real collisions.
var reservedProjectSlugs = map[Project]bool{
	"all":      true,
	"jobs":     true,
	"projects": true,
	"saved":    true,
}

// NormalizeProject maps the empty slug onto DefaultProject. A job persisted
// before projects existed carries no slug, and several call sites have to read
// that absence as the default project; doing it in one place keeps them from
// drifting apart. It deliberately does not validate: callers that accept a slug
// from a client must still run it through ValidateProjectSlug.
func NormalizeProject(slug Project) Project {
	if slug == "" {
		return DefaultProject
	}
	return slug
}

// ValidateProjectSlug accepts lowercase alphanumerics and dashes only. The
// charset is deliberately narrower than the filesystem allows so a slug can
// never introduce a path separator, a traversal segment, or a leading dot.
func ValidateProjectSlug(slug Project) error {
	if slug == "" {
		return invalid("project", "is required")
	}
	if len(slug) > MaxProjectSlugLen {
		return invalid("project", fmt.Sprintf("must be at most %d characters", MaxProjectSlugLen))
	}
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if !isLower && !isDigit && c != '-' {
			return invalid("project", "must contain only lowercase letters, digits, and dashes")
		}
	}
	first := slug[0]
	last := slug[len(slug)-1]
	if first == '-' || last == '-' {
		return invalid("project", "must start and end with a letter or digit")
	}
	if reservedProjectSlugs[slug] {
		return invalid("project", fmt.Sprintf("%q is reserved", slug))
	}
	return nil
}

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

// PolishingStrategy selects how active circles and restart populations are
// chosen during transactional batch polishing.
type PolishingStrategy string

const (
	PolishingReplacement    PolishingStrategy = "replacement"
	PolishingHybridOverlap  PolishingStrategy = "hybrid-overlap"
	PolishingResidualRegion PolishingStrategy = "residual-region"
	// PolishingContiguousWindow polishes a consecutive run of draw slots so the
	// circles before the window can be baked into a reusable canvas. The other
	// strategies select by image-space merit and routinely include circle one,
	// which bakes nothing and rasterizes the whole image for that candidate.
	PolishingContiguousWindow PolishingStrategy = "contiguous-window"
)

// JobConfig is the canonical configuration shared by all application entry
// points and persisted checkpoints.
type JobConfig struct {
	RefPath                  string            `json:"refPath"`
	CanvasPath               string            `json:"canvasPath,omitempty"`
	Mode                     Mode              `json:"mode"`
	Backend                  Backend           `json:"backend,omitempty"`
	Variant                  Variant           `json:"variant,omitempty"`
	Circles                  int               `json:"circles"`
	Iters                    int               `json:"iters"`
	PopSize                  int               `json:"popSize"`
	OptimizerEpochs          int               `json:"optimizerEpochs,omitempty"`
	BatchSize                int               `json:"batchSize,omitempty"`
	PolishingEnabled         bool              `json:"polishingEnabled,omitempty"`
	PolishingOnly            bool              `json:"polishingOnly,omitempty"`
	PolishingStrategy        PolishingStrategy `json:"polishingStrategy,omitempty"`
	PolishingActiveSetSize   int               `json:"polishingActiveSetSize,omitempty"`
	PolishingMaxSweeps       int               `json:"polishingMaxSweeps,omitempty"`
	PolishingEpochs          int               `json:"polishingEpochs,omitempty"`
	PolishingIters           int               `json:"polishingIters,omitempty"`
	PolishingPopSize         int               `json:"polishingPopSize,omitempty"`
	PolishingStagnationIters int               `json:"polishingStagnationIters,omitempty"`
	PolishingMinImprovement  float64           `json:"polishingMinImprovement,omitempty"`
	Threads                  int               `json:"threads,omitempty"`
	// ParallelEvaluation lets the optimizer evaluate population members
	// concurrently over a pool of independent renderer sessions sized by
	// EvaluationWorkers. It is additive and optional: checkpoints written before
	// it existed decode as false, which is also the default. It is opt-in
	// because the parallel optimizer path applies one global best per generation
	// instead of updating it mid-population, so a seed reproduces exactly with
	// the flag held fixed but not across the two settings.
	//
	// Transactional polishing leases from the same pool and carries the same
	// caveat for the same reason: a parallel polish sweep applies one global
	// best per generation, so a seed reproduces with the flag held fixed but not
	// across the two settings. Pooling itself changes nothing -- a width-one
	// pool is byte-identical to the serial sweep -- the trajectory changes only
	// once the optimizer actually runs its parallel path.
	ParallelEvaluation bool `json:"parallelEvaluation,omitempty"`
	// EvaluationWorkers is how many cost evaluations run concurrently when
	// ParallelEvaluation is set. It is deliberately separate from Threads:
	// Threads shards the rows of a single render, while this shards whole
	// independent renders across sessions. The two trade against each other, so
	// a machine is rarely best served by the same number for both.
	//
	// Zero means "use Threads", which is what this setting did before it had its
	// own field, so checkpoints written without it resume unchanged. It has no
	// effect at all unless ParallelEvaluation is true.
	EvaluationWorkers int `json:"evaluationWorkers,omitempty"`
	// FastCompositing selects the reduced-precision float32 SIMD span
	// compositor. It is additive and optional: checkpoints written before it
	// existed decode as false, which is also the default, so the exact
	// compositor stays in charge unless a run asks otherwise.
	FastCompositing      bool    `json:"fastCompositing,omitempty"`
	Seed                 int64   `json:"seed"`
	EffectiveSeed        int64   `json:"effectiveSeed,omitempty"`
	ResumeCount          int     `json:"resumeCount,omitempty"`
	CheckpointInterval   int     `json:"checkpointInterval,omitempty"`
	TraceInterval        int     `json:"traceInterval,omitempty"`
	EnableTrace          bool    `json:"enableTrace,omitempty"`
	DisableTrace         bool    `json:"disableTrace,omitempty"`
	EnableSSIM           bool    `json:"enableSSIM,omitempty"`
	SaveSnapshots        bool    `json:"saveSnapshots,omitempty"`
	ConvergenceEnabled   bool    `json:"convergenceEnabled,omitempty"`
	DisableConvergence   bool    `json:"disableConvergence,omitempty"`
	ConvergencePatience  int     `json:"convergencePatience,omitempty"`
	ConvergenceThreshold float64 `json:"convergenceThreshold,omitempty"`

	// Optimizer-level early stopping. These are per-iteration criteria applied
	// inside a single optimizer run, and are unrelated to the Convergence*
	// fields above, which count whole circles or batches and use a relative
	// improvement ratio. All four default to zero, which disables early
	// stopping and keeps a default run identical to one configured before these
	// fields existed.
	StopTargetCost      float64 `json:"stopTargetCost,omitempty"`
	StopMinImprovement  float64 `json:"stopMinImprovement,omitempty"`
	StopStagnationIters int     `json:"stopStagnationIters,omitempty"`
	StopMinIters        int     `json:"stopMinIters,omitempty"`

	// InitialCircles starts the run from a hand-authored arrangement instead of
	// a random one. It is the only way to supply explicit circle parameters:
	// every other warm start in the system comes from a checkpoint, and a
	// checkpoint is written by a run, not by an operator.
	//
	// It applies to the run that owns it and to nothing downstream. A
	// continuation -- a resume, an extend, a polish, or any scheduled stage
	// after the base -- already carries its parent's parameters, and those win;
	// see the seeding block in the server worker. Schedule expansion clears the
	// field on every stage past the base for the same reason.
	InitialCircles CircleSpecs `json:"initialCircles,omitempty"`
}

// EarlyStopEnabled reports whether optimizer-level early stopping is configured.
func (c JobConfig) EarlyStopEnabled() bool {
	return c.StopTargetCost > 0 || c.StopStagnationIters > 0
}

// DefaultConfig returns the canonical defaults. A zero seed is deliberately
// left unresolved until ApplyDefaults so callers can report the chosen seed.
//
// The Stop* early-stopping fields are intentionally absent. They must stay zero
// by default so that an unconfigured run reproduces exactly, and ApplyDefaults
// must not gain a branch that fills them in.
func DefaultConfig() JobConfig {
	return JobConfig{
		Mode:                     ModeJoint,
		Backend:                  BackendCPU,
		Variant:                  VariantStandard,
		Circles:                  10,
		Iters:                    100,
		PopSize:                  30,
		OptimizerEpochs:          1,
		BatchSize:                5,
		PolishingActiveSetSize:   5,
		PolishingStrategy:        PolishingReplacement,
		PolishingMaxSweeps:       DefaultPolishingMaxSweeps,
		PolishingEpochs:          DefaultPolishingEpochs,
		PolishingIters:           DefaultPolishingIters,
		PolishingPopSize:         DefaultPolishingPopSize,
		PolishingStagnationIters: DefaultPolishingStagnationIters,
		PolishingMinImprovement:  0.001,
		Threads:                  runtime.GOMAXPROCS(0),
		EnableTrace:              true,
		ConvergenceEnabled:       true,
		ConvergencePatience:      3,
		ConvergenceThreshold:     0.001,
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
	if c.OptimizerEpochs == 0 {
		c.OptimizerEpochs = defaults.OptimizerEpochs
	}
	if c.BatchSize == 0 {
		c.BatchSize = defaults.BatchSize
		if c.Circles > 0 && c.BatchSize > c.Circles {
			c.BatchSize = c.Circles
		}
	}
	if c.PolishingActiveSetSize == 0 {
		c.PolishingActiveSetSize = defaults.PolishingActiveSetSize
		if c.Circles > 0 && c.PolishingActiveSetSize > c.Circles {
			c.PolishingActiveSetSize = c.Circles
		}
	}
	if c.PolishingStrategy == "" {
		c.PolishingStrategy = defaults.PolishingStrategy
	}
	if c.PolishingMaxSweeps == 0 {
		c.PolishingMaxSweeps = defaults.PolishingMaxSweeps
	}
	if c.PolishingEpochs == 0 {
		c.PolishingEpochs = defaults.PolishingEpochs
	}
	if c.PolishingIters == 0 {
		c.PolishingIters = defaults.PolishingIters
	}
	// The polishing population resolves to its own default rather than to
	// PopSize. Polishing optimizes one active set -- seven parameters per circle,
	// so 35 at the default active-set size -- while PopSize is chosen for the
	// whole vector, and a run sized for 512 circles used to spend a population of
	// 200 on that active set. docs/polishing-budget-report.md measures what that
	// buys: past a point a larger population removes no more error and, at the
	// production shape, removed none at all.
	//
	// This deliberately does not follow the EvaluationWorkers fallback below.
	// That field kept inheriting so old checkpoints would resume unchanged; here
	// inheritance is the defect being removed, so a checkpoint written before the
	// field resumes at the measured default instead of at its own popSize. Set
	// polishingPopSize explicitly to reproduce such a run exactly.
	if c.PolishingPopSize == 0 {
		c.PolishingPopSize = defaults.PolishingPopSize
	}
	if c.PolishingStagnationIters == 0 {
		c.PolishingStagnationIters = defaults.PolishingStagnationIters
	}
	if c.PolishingMinImprovement == 0 {
		c.PolishingMinImprovement = defaults.PolishingMinImprovement
	}
	if c.Threads == 0 {
		c.Threads = defaults.Threads
	}
	// Evaluation width falls back to Threads rather than to a default of its
	// own. That keeps a configuration written before the field existed, and one
	// that only sets --parallel-evaluation, behaving exactly as it did.
	if c.EvaluationWorkers == 0 {
		c.EvaluationWorkers = c.Threads
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
	c.InitialCircles.ApplyDefaults()
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
	if c.OptimizerEpochs < 1 || c.OptimizerEpochs > MaxOptimizerEpochs {
		return invalid("optimizerEpochs", fmt.Sprintf("must be between 1 and %d", MaxOptimizerEpochs))
	}
	if c.BatchSize < 1 || c.BatchSize > MaxBatchSize || c.Mode == ModeBatch && c.BatchSize > c.Circles {
		return invalid("batchSize", "must be positive, within the limit, and no larger than circles")
	}
	if c.PolishingEnabled && c.Mode != ModeBatch {
		return invalid("polishingEnabled", "requires batch mode")
	}
	if c.PolishingOnly && !c.PolishingEnabled {
		return invalid("polishingOnly", "requires polishing to be enabled")
	}
	switch c.PolishingStrategy {
	case PolishingReplacement, PolishingHybridOverlap, PolishingResidualRegion, PolishingContiguousWindow:
	default:
		return invalid("polishingStrategy", "must be replacement, hybrid-overlap, residual-region, or contiguous-window")
	}
	if c.PolishingActiveSetSize < 1 || c.PolishingActiveSetSize > MaxBatchSize || c.PolishingActiveSetSize > c.Circles {
		return invalid("polishingActiveSetSize", "must be positive, within the limit, and no larger than circles")
	}
	if c.PolishingMaxSweeps < 1 || c.PolishingMaxSweeps > MaxPolishingSweeps {
		return invalid("polishingMaxSweeps", fmt.Sprintf("must be between 1 and %d", MaxPolishingSweeps))
	}
	if c.PolishingEpochs < 1 || c.PolishingEpochs > MaxOptimizerEpochs {
		return invalid("polishingEpochs", fmt.Sprintf("must be between 1 and %d", MaxOptimizerEpochs))
	}
	if c.PolishingIters < 1 || c.PolishingIters > MaxIterations {
		return invalid("polishingIters", fmt.Sprintf("must be between 1 and %d", MaxIterations))
	}
	// Zero is the omitted value, not a population of zero: ApplyDefaults fills
	// it with DefaultPolishingPopSize, so every configuration that reaches an
	// optimizer went through Normalize and carries a real number. What still
	// carries zero is a checkpoint written before this field existed, and
	// restore hands that configuration out unchanged — jobFromCheckpoint
	// deliberately does not normalize, because ApplyDefaults would also resolve
	// the seed and invent one the run never used. Rejecting zero here would
	// therefore make `status` fail on a legacy job instead of displaying it.
	// This is the same allowance evaluationWorkers gets below, for the same
	// reason.
	if c.PolishingPopSize != 0 && (c.PolishingPopSize < MinPopulation || c.PolishingPopSize > MaxPopulation) {
		return invalid("polishingPopSize", fmt.Sprintf("must be between %d and %d", MinPopulation, MaxPopulation))
	}
	if c.PolishingStagnationIters < 1 || c.PolishingStagnationIters > c.PolishingIters {
		return invalid("polishingStagnationIters", "must be positive and no larger than polishingIters")
	}
	if math.IsNaN(c.PolishingMinImprovement) || math.IsInf(c.PolishingMinImprovement, 0) || c.PolishingMinImprovement <= 0 {
		return invalid("polishingMinImprovement", "must be finite and positive")
	}
	if c.Threads < 1 {
		return invalid("threads", "must be positive")
	}
	// Zero is meaningful here rather than merely unset: it means "use Threads",
	// so it stays valid even on a configuration that never went through
	// ApplyDefaults. Only an explicit negative is a mistake.
	if c.EvaluationWorkers < 0 {
		return invalid("evaluationWorkers", "cannot be negative")
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
	if math.IsNaN(c.StopTargetCost) || math.IsInf(c.StopTargetCost, 0) || c.StopTargetCost < 0 {
		return invalid("stopTargetCost", "must be finite and non-negative")
	}
	if math.IsNaN(c.StopMinImprovement) || math.IsInf(c.StopMinImprovement, 0) || c.StopMinImprovement < 0 {
		return invalid("stopMinImprovement", "must be finite and non-negative")
	}
	// A minimum improvement only resets the stagnation counter, so on its own it
	// would silently do nothing.
	if c.StopMinImprovement > 0 && c.StopStagnationIters == 0 {
		return invalid("stopMinImprovement", "requires stopStagnationIters to be set")
	}
	// Both windows are meaningless beyond the iteration budget, and the
	// optimizer rejects a minimum above it outright.
	if c.StopStagnationIters < 0 || c.StopStagnationIters > c.Iters {
		return invalid("stopStagnationIters", fmt.Sprintf("must be between 0 and iters (%d)", c.Iters))
	}
	if c.StopMinIters < 0 || c.StopMinIters > c.Iters {
		return invalid("stopMinIters", fmt.Sprintf("must be between 0 and iters (%d)", c.Iters))
	}
	if len(c.InitialCircles) > 0 {
		// Batch mode only, because that is the mode whose optimizer receives the
		// whole vector at once. Sequential and joint runs build their vector as
		// they go, so a full arrangement handed to them would be partly ignored
		// -- worse than refused, because the run would look seeded and not be.
		if c.Mode != ModeBatch {
			return invalid("initialCircles", "requires batch mode")
		}
		if len(c.InitialCircles) != c.Circles {
			return invalid("initialCircles", fmt.Sprintf("must supply exactly circles (%d) entries, got %d", c.Circles, len(c.InitialCircles)))
		}
		if err := c.InitialCircles.Validate(); err != nil {
			return err
		}
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
