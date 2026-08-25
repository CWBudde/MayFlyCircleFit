// Package app contains dependency-free application types and validation.
package app

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"time"
)

const (
	// MaxCircles was 1000 until a campaign ran into it with gains still
	// accelerating: a one-circle-at-a-time greedy fit of a 512x512 reference
	// reached 1000 circles at cost 96.199 (PSNR 28.299) while the marginal
	// improvement over the previous milestone was still growing, so the cap
	// was the binding constraint on quality rather than a point of diminishing
	// return. Raised again to 3000 on the same evidence: 2000 circles reached
	// 64.602 (PSNR 30.028) with the last hundred still returning 1.85 cost
	// units, more than any polishing pass returns above 200 circles. It bounds request validation at the trusted-local boundary, so
	// it is a memory and wall-clock guard, not a modelling statement: circle
	// parameters cost 28*K bytes and per-circle fit time grows with K.
	MaxCircles    = 3000
	MaxIterations = 10000
	MinPopulation = 20
	// MaxPopulation was 200 until a campaign wanted to spend a very large
	// population on a very small parameter vector. The two are not the same
	// question: 200 was measured as the point of diminishing return for
	// *polishing* an active set (docs/polishing-budget-report.md), where a
	// sweep optimizes ~35 parameters and population 200 cost 5.2x the wall
	// clock of 30 for 1.28x the error removed. A batch base stage fitting 8
	// circles jointly is a 56-parameter global search with no warm start, and
	// nothing here had ever measured whether a population an order of
	// magnitude larger buys a better basin there. Raised to 4096 so that
	// experiment is expressible. Like MaxCircles it bounds request validation
	// at the trusted-local boundary, so it is a memory and wall-clock guard,
	// not a modelling statement: a population costs popSize concurrent
	// candidate vectors and each evaluation renders the whole canvas, so
	// per-iteration time grows linearly with it.
	MaxPopulation = 4096
	// MaxPopulationDimensions bounds popSize multiplied by the dimensionality
	// of the vector the optimizer actually searches, because MaxPopulation
	// alone stopped being a memory guard once it was raised. The adapter sizes
	// both NPop and NPopF from popSize, and every member carries a position, a
	// velocity and a personal best, so the cost of a run grows with the product
	// of the two numbers rather than with either alone. A joint 3000-circle run
	// searches 21,000 dimensions; at popSize 4096 its positions alone would be
	// about 1.3 GiB before anything else the run needs.
	//
	// The dimensionality is the optimizer's, not the canvas's: a joint run
	// searches every circle at once, a batch run only its batch, and a
	// sequential run a single circle. That is why the large population is
	// affordable for the small batch vectors it was raised for and refused for
	// the shapes where it is not.
	//
	// The value is chosen so that no configuration valid under the previous
	// MaxPopulation of 200 becomes invalid: the largest such product is a joint
	// 3000-circle run at popSize 200, or 4,200,000.
	MaxPopulationDimensions = 8_388_608
	// ParametersPerCircle mirrors internal/fit's paramsPerCircle: x, y, r, three
	// colour channels and opacity. It is duplicated rather than imported because
	// dependencies flow from fit toward app and must not be reversed;
	// TestParametersPerCircleMatchesTheRenderer pins the two together.
	ParametersPerCircle = 7
	MaxOptimizerEpochs  = 32
	// MaxOptimizerRestarts bounds independent cold attempts per optimizer run.
	// Measured returns had not flattened at 32; see
	// docs/restart-vs-budget-report.md.
	MaxOptimizerRestarts = 64
	MaxBatchSize         = 100
	MaxPolishingSweeps   = 32
	MaxImagePixels       = 16_777_216
	MaxImageFileSize     = 64 << 20
	MaxRequestBody       = 1 << 20
	MaxProjectSlugLen    = 64

	// MaxCLIResponseBytes bounds what the CLI will decode from a server
	// response. It lives here rather than in cmd because the server is what has
	// to stay under it: an endpoint whose body grows with a campaign's stage
	// count is the reason a 1016-stage campaign could not be printed at all.
	MaxCLIResponseBytes = 1 << 20
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

	for i := range len(slug) {
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
	VariantEOBBMA   Variant = "eobbma"
	VariantGSASMA   Variant = "gsasma"
	VariantMPMA     Variant = "mpma"
	VariantAOBLMOA  Variant = "aoblmoa"
)

// variants lists every Mayfly variant a configuration may select, in the order
// they are reported to the caller. It must stay in step with the variant set
// internal/opt can construct; internal/opt owns a contract test that fails if
// the two drift apart, because app is dependency-free and cannot import it.
var variants = []Variant{
	VariantStandard,
	VariantDESMA,
	VariantOLCE,
	VariantEOBBMA,
	VariantGSASMA,
	VariantMPMA,
	VariantAOBLMOA,
}

// SupportedVariants returns the Mayfly variants a JobConfig may select.
func SupportedVariants() []Variant {
	return slices.Clone(variants)
}

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
	// which bakes nothing and rasterizes the whole image for that candidate. A
	// full-coverage budget starts with the earliest draw slots, while a partial
	// budget keeps the cheaper latest-first traversal.
	PolishingContiguousWindow PolishingStrategy = "contiguous-window"
)

// JobConfig is the canonical configuration shared by all application entry
// points and persisted checkpoints.
type JobConfig struct {
	RefPath    string  `json:"refPath"`
	CanvasPath string  `json:"canvasPath,omitempty"`
	Mode       Mode    `json:"mode"`
	Backend    Backend `json:"backend,omitempty"`
	// Optimizer names the optimization library the job runs with. Empty means
	// mayfly, so every configuration and checkpoint written before this field
	// existed keeps its behavior. Read it through ResolvedOptimizer.
	Optimizer Optimizer `json:"optimizer,omitempty"`
	// Variant selects a MayFly algorithm variant. It stays empty for an
	// optimizer that has no variants, and Validate refuses a variant there
	// rather than ignoring it.
	Variant Variant `json:"variant,omitempty"`
	// InitialSigma is CMA-ES's initial step size in the normalized [0,1]
	// search box. Nil leaves the CMA-ES default of 0.3. It is a pointer so an
	// explicitly invalid zero is not confused with omission.
	InitialSigma *float64 `json:"initialSigma,omitempty"`
	// ActiveCMA controls CMA-ES negative rank-mu covariance adaptation. Nil
	// means enabled, the library default; a pointer preserves an explicit false.
	ActiveCMA *bool `json:"activeCMA,omitempty"` //nolint:tagliatelle // Public spelling is fixed by PLAN.md.
	// CovarianceMode and RestartStrategy are CMA-ES-only settings. Empty values
	// resolve to full covariance and no shared-budget restart schedule.
	CovarianceMode  CMAESCovarianceMode  `json:"covarianceMode,omitempty"`
	RestartStrategy CMAESRestartStrategy `json:"restartStrategy,omitempty"`
	// QMCInit selects how MayFly draws its initial population: independent
	// uniform draws, or a low-discrepancy sequence covering the search box
	// more evenly for the same number of evaluations. Empty means uniform,
	// which is what every configuration and checkpoint written before this
	// field existed carries. Read it through ResolvedQMCInit.
	//
	// This is an expert knob, not a recommended default. The library's own
	// study finds a chance-level effect across its benchmark suite, and the
	// mechanism is weakest exactly where this project runs it: the gap a
	// low-discrepancy sample closes is largest when the population is small
	// relative to the dimension, and a popSize of 1024 over 56 dimensions is
	// not that. See docs/known-limitations.md.
	QMCInit         QMCInit `json:"qmcInit,omitempty"`
	Circles         int     `json:"circles"`
	Iters           int     `json:"iters"`
	PopSize         int     `json:"popSize"`
	OptimizerEpochs int     `json:"optimizerEpochs,omitempty"`
	// OptimizerRestarts runs each optimizer invocation as this many
	// independent cold attempts and keeps the best. It is not
	// OptimizerEpochs: an epoch reseeds from the best candidate so far and
	// inherits its basin, while a restart explores from a fresh
	// population. One preserves the historical single-attempt behaviour.
	OptimizerRestarts int `json:"optimizerRestarts,omitempty"`
	// CrossoverCount overrides how many crossover offspring MayFly produces
	// per iteration. Zero leaves the library's own scaling alone, which is
	// one offspring per population member.
	//
	// Measured on the eight-circle base stage: about 64 offspring at a
	// population of 1024 is statistically indistinguishable from the
	// library default while spending 25% fewer evaluations, and cutting it
	// to 2 is significantly worse. See docs/restart-vs-budget-report.md.
	CrossoverCount int `json:"crossoverCount,omitempty"`
	// DanceDamp overrides the per-iteration decay applied to MayFly's
	// nuptial-dance term, whose library default is 0.8. The dance is the
	// random walk the leading male takes on top of its velocity, so this
	// governs how fast the swarm stops exploring. Advanced: the library does
	// not range-check it. Above 1 the dance coefficient grows every iteration;
	// velocity and position clamping keep the run finite, so the bound guards
	// against a saturated random walk rather than against divergence.
	//
	// Nil leaves the library default. It is a pointer because zero is a
	// meaningful setting -- it retires the dance after one iteration -- so
	// omitempty on a plain float64 would erase exactly that configuration.
	DanceDamp *float64 `json:"danceDamp,omitempty"`
	// AquilaWeight overrides the AOBLMOA branch rule with a probability that
	// an individual takes an Aquila step instead of the ordinary MayFly
	// velocity and position update. Applies to the aoblmoa variant only.
	//
	// Deprecated as of MayFly v0.6.0, which made the branch a deterministic
	// fitness test as its source paper defines it. The library default is now
	// the AquilaWeightAuto sentinel; setting a value in [0,1] here restores
	// the pre-v0.6.0 probabilistic behavior.
	//
	// Nil leaves the library default; zero is meaningful (pure MayFly), hence
	// the pointer.
	AquilaWeight *float64 `json:"aquilaWeight,omitempty"`
	// OppositionProbability is retained but inert. Applies to the aoblmoa
	// variant only.
	//
	// MayFly v0.6.0 moved opposition-based learning to the offspring stage,
	// where the paper applies it to every offspring unconditionally, so the
	// library range-checks this value and then never reads it. The field stays
	// because job submission and schedules decode with DisallowUnknownFields
	// and resume reads it back out of existing checkpoints: dropping it would
	// reject configurations that load today.
	//
	// Nil leaves the library default, and the pointer is kept for symmetry
	// with the other knobs and so a stored explicit zero still round-trips.
	OppositionProbability    *float64          `json:"oppositionProbability,omitempty"`
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
	FastCompositing    bool  `json:"fastCompositing,omitempty"`
	Seed               int64 `json:"seed"`
	EffectiveSeed      int64 `json:"effectiveSeed,omitempty"`
	ResumeCount        int   `json:"resumeCount,omitempty"`
	CheckpointInterval int   `json:"checkpointInterval,omitempty"`
	TraceInterval      int   `json:"traceInterval,omitempty"`
	EnableTrace        bool  `json:"enableTrace,omitempty"`
	DisableTrace       bool  `json:"disableTrace,omitempty"`
	// EnableOptimizerDiagnostics adds optimizer-specific search-state metrics
	// to trace entries. It is opt-in because Mayfly must copy both populations
	// to measure their spread. CMA-ES records sigma and covariance condition.
	EnableOptimizerDiagnostics bool `json:"enableOptimizerDiagnostics,omitempty"`
	//nolint:tagliatelle // Public spelling predates the linter.
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
		Optimizer:                OptimizerMayfly,
		Variant:                  VariantStandard,
		Circles:                  10,
		Iters:                    100,
		PopSize:                  30,
		OptimizerEpochs:          1,
		OptimizerRestarts:        1,
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
// optimizerDimensions reports the length of the vector a single optimizer run
// searches, which is what a population is actually multiplied by.
//
// It is not the whole canvas: only a joint run optimizes every circle at once.
// A batch run searches one batch, and a sequential run one circle, which is why
// a population that would be ruinous for a large joint vector is affordable
// for them.
//
// The result is never zero, so a caller validating a configuration whose
// defaults have not been applied cannot divide by it.
func (c *JobConfig) optimizerDimensions() int {
	circles := c.Circles

	switch c.Mode {
	case ModeSequential:
		circles = 1
	case ModeBatch:
		if c.BatchSize > 0 && c.BatchSize < circles {
			circles = c.BatchSize
		}
	case ModeJoint:
	}

	if circles < 1 {
		circles = 1
	}

	return circles * ParametersPerCircle
}

func (c *JobConfig) ApplyDefaults() error {
	defaults := DefaultConfig()
	if c.Mode == "" {
		c.Mode = defaults.Mode
	}

	if c.Backend == "" {
		c.Backend = defaults.Backend
	}

	if c.Optimizer == "" {
		c.Optimizer = defaults.Optimizer
	}

	if c.Optimizer == OptimizerCMAES {
		if c.InitialSigma == nil {
			sigma := DefaultCMAESInitialSigma
			c.InitialSigma = &sigma
		}

		if c.ActiveCMA == nil {
			active := true
			c.ActiveCMA = &active
		}

		if c.CovarianceMode == "" {
			c.CovarianceMode = CMAESCovarianceFull
		}

		if c.RestartStrategy == "" {
			c.RestartStrategy = CMAESRestartNone
		}
	}

	// Only MayFly has variants, so only a MayFly job gets the default one. A
	// Dragonfly job keeps an empty variant, which is what lets Validate tell
	// "no variant was asked for" from "a variant was asked for and this engine
	// cannot honor it".
	if c.Variant == "" && c.Optimizer == OptimizerMayfly {
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

	if c.OptimizerRestarts == 0 {
		c.OptimizerRestarts = defaults.OptimizerRestarts
	}

	if c.BatchSize == 0 {
		c.BatchSize = defaults.BatchSize
		if c.Circles > 0 && c.BatchSize > c.Circles {
			c.BatchSize = c.Circles
		}
		// An authored arrangement is a full vector, and only a full-size batch
		// is one optimizer stage over the whole vector. Leaving the default at
		// five would queue a seeded ten-circle run that the batch dispatch then
		// refuses, so the default follows the seed rather than the constant.
		if len(c.InitialCircles) > 0 && c.Circles > 0 {
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

	// The engine check owns the variant check, because which variants are
	// legal depends on which library runs.
	engineErr := c.validateOptimizerEngine()
	if engineErr != nil {
		return engineErr
	}

	qmcErr := c.validateQMCInit()
	if qmcErr != nil {
		return qmcErr
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

	if dimensions := c.optimizerDimensions(); c.PopSize*dimensions > MaxPopulationDimensions {
		return invalid("popSize", fmt.Sprintf(
			"of %d searches %d dimensions in %s mode, and %d exceeds the limit of %d; "+
				"lower popSize to at most %d for this shape",
			c.PopSize, dimensions, c.Mode, c.PopSize*dimensions, MaxPopulationDimensions,
			MaxPopulationDimensions/dimensions,
		))
	}

	if c.OptimizerEpochs < 1 || c.OptimizerEpochs > MaxOptimizerEpochs {
		return invalid("optimizerEpochs", fmt.Sprintf("must be between 1 and %d", MaxOptimizerEpochs))
	}

	if c.OptimizerRestarts < 1 || c.OptimizerRestarts > MaxOptimizerRestarts {
		return invalid("optimizerRestarts", fmt.Sprintf("must be between 1 and %d", MaxOptimizerRestarts))
	}

	// Zero defers to the library. Two is the smallest count the library
	// accepts, because it draws its mutant pool from the offspring and cannot
	// do that from an empty one. The upper bound is what mating can consume:
	// the library forms pairs from the male and female populations.
	err := c.validateAdvancedOptimizerKnobs()
	if err != nil {
		return err
	}

	if c.CrossoverCount != 0 && (c.CrossoverCount < 2 || c.CrossoverCount > 2*c.PopSize) {
		return invalid("crossoverCount",
			fmt.Sprintf("must be 0 to use the library default, or between 2 and %d", 2*c.PopSize))
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

	if c.EnableOptimizerDiagnostics && !c.EnableTrace {
		return invalid("enableOptimizerDiagnostics", "requires tracing to be enabled")
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
		// The same argument as the mode restriction, one level down: a batch
		// smaller than the circle count optimizes the vector in chunks, so it
		// would seed the first chunk and discard the rest. Refused here rather
		// than at dispatch, where it would surface as a failed run instead of a
		// rejected configuration.
		if c.BatchSize < c.Circles {
			return invalid("initialCircles", fmt.Sprintf("requires batchSize to cover every circle (batchSize %d, circles %d)", c.BatchSize, c.Circles))
		}

		err := c.InitialCircles.Validate()
		if err != nil {
			return err
		}
	}

	return nil
}

// Normalize applies defaults and validates a configuration in one operation.
func Normalize(config JobConfig) (JobConfig, error) {
	err := config.ApplyDefaults()
	if err != nil {
		return JobConfig{}, err
	}

	err = config.Validate()
	if err != nil {
		return JobConfig{}, err
	}

	return config, nil
}

// NormalizeRequest normalizes a configuration that arrived as JSON, refusing any
// field the caller wrote that ApplyDefaults would silently replace.
//
// Once a body is decoded into a value struct, an omitted field and an explicit
// zero are the same thing: `"circles": 0` reaches ApplyDefaults looking exactly
// like a caller who never mentioned circles, and comes back as the default, so
// a request for zero circles runs ten instead. The raw body still knows which
// keys were written, which is the same evidence ParseSchedule uses to keep a
// schedule document from having a field quietly dropped.
//
// body is the request body the configuration was decoded from; keys that name
// no configuration field — a request envelope's own fields, say — are ignored.
func NormalizeRequest(body []byte, config JobConfig) (JobConfig, error) {
	var present map[string]json.RawMessage

	err := json.Unmarshal(body, &present)
	if err != nil {
		return JobConfig{}, invalid("request", "must be a JSON object")
	}

	err = validateNoDefaultOverrides("", present, config)
	if err != nil {
		return JobConfig{}, err
	}

	return Normalize(config)
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
