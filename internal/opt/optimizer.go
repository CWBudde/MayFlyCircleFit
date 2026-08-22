package opt

import "context"

// InequalityConstraint describes a continuous constraint g(params) <= 0.
// Positive values are violations; zero and negative values are feasible.
type InequalityConstraint func([]float64) float64

// Problem describes one bounded minimization problem.
type Problem struct {
	Eval func([]float64) float64
	// Repair canonicalizes a bounded candidate before evaluation and before it
	// is exposed through progress or results. It may be nil.
	Repair       func([]float64)
	Inequalities []InequalityConstraint
	Lower        []float64
	Upper        []float64
	Dim          int
}

// Candidate is a known solution used to seed a continuation run.
type Candidate struct {
	Params []float64
	Cost   float64
}

// Progress is an immutable best-so-far optimizer snapshot.
type Progress struct {
	Iterations  int
	Evaluations int
	BestParams  []float64
	BestCost    float64
}

// Observer consumes synchronous best-so-far snapshots.
type Observer func(Progress)

// ProgressMapper converts optimizer-local progress into the caller's complete
// parameter space. Staged renderers use it to prepend already-retained circles
// before progress reaches monitoring and persistence observers.
type ProgressMapper func(Progress) Progress

// EpochBoundary reports a completed restart epoch with cumulative work for the
// current optimizer invocation.
type EpochBoundary struct {
	Epoch       int
	Progress    Progress
	Termination Termination
}

// EpochObserver consumes a synchronous epoch boundary. Returning an error
// aborts the remaining epochs so persistence failures cannot be ignored.
type EpochObserver func(EpochBoundary) error

// RunOptions controls progress reporting and restart-from-best behavior.
type RunOptions struct {
	Observer       Observer
	ProgressMapper ProgressMapper
	EpochObserver  EpochObserver
	Initial        *Candidate
	// AdditionalSeeds supplies alternative known candidates for a mixed
	// continuation population. Initial remains the incumbent that must not be
	// lost; additional seeds broaden exploration around other promising basins.
	AdditionalSeeds []Candidate
	ResumeCount     int
	// Continuation optionally concentrates a seeded run around its known
	// candidates. Nil preserves the historical half-local, half-global Mayfly
	// population and velocity scale. Active-set polishing uses this to request
	// a genuinely local search without changing fresh/global optimization.
	Continuation *ContinuationProfile
}

// ContinuationProfile controls how a Mayfly continuation population explores
// around Initial and AdditionalSeeds. Values are expressed in the optimizer's
// normalized [0,1] space.
type ContinuationProfile struct {
	// LocalFraction is the fraction of each male/female population supplied as
	// seeded candidates. The remainder is initialized globally at random.
	LocalFraction float64
	// Sigma is the standard deviation of seeded perturbations.
	Sigma float64
	// CoordinateRate is the probability that an individual dimension is
	// perturbed. Sparse perturbations avoid moving every variable at once in a
	// high-dimensional active set.
	CoordinateRate float64
	// MaxVelocity caps Mayfly movement per iteration. Zero retains Mayfly's
	// default 10% of the normalized search range.
	MaxVelocity float64
}

// Termination describes why an optimizer stopped.
type Termination string

const (
	// TerminationCompleted means the optimizer consumed its iteration budget.
	TerminationCompleted Termination = "completed"
	// TerminationCancelled means the run stopped on context cancellation.
	TerminationCancelled Termination = "cancelled"
	// TerminationTargetCost means a configured target cost was reached.
	TerminationTargetCost Termination = "target_cost"
	// TerminationStagnation means the best cost stopped improving within the
	// configured stagnation window.
	TerminationStagnation Termination = "stagnation"
)

// Result is the complete, measured outcome of an optimization run.
type Result struct {
	BestParams  []float64
	BestCost    float64
	Iterations  int
	Evaluations int
	Termination Termination
}

// LifecycleOptimizer supports errors, cooperative cancellation, measured
// progress, and genuine restart-from-best population seeding.
type LifecycleOptimizer interface {
	RunContext(context.Context, Problem, RunOptions) (Result, error)
}

// Optimizer defines an optimization algorithm interface.
type Optimizer interface {
	// Run executes the optimization
	// eval: objective function to minimize
	// lower, upper: parameter bounds
	// dim: dimensionality of parameter space
	// Returns: best parameters and best cost
	Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)
}

// ResumableOptimizer extends Optimizer with the ability to resume from a checkpoint.
// Implementations should seed the population with variations of the provided solution.
type ResumableOptimizer interface {
	Optimizer

	// RunWithInitial executes optimization starting from an initial solution.
	// The initial solution is used to seed the population with random variations.
	// This allows resuming optimization from a checkpoint.
	//
	// initialParams: starting point for optimization (best params from checkpoint)
	// initialCost: cost of the initial solution (for reference)
	// eval: objective function to minimize
	// lower, upper: parameter bounds
	// dim: dimensionality of parameter space
	//
	// Returns: best parameters and best cost
	//
	// Implementation notes:
	//   - Population can be seeded as: 50% clones of initialParams + 50% random
	//   - Or: initialParams + random variations with decreasing perturbation
	//   - The optimizer should respect the same random seed for reproducibility
	//   - Iteration count may reset or continue (implementation-specific)
	RunWithInitial(initialParams []float64, initialCost float64, eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)
}
