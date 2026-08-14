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

// RunOptions controls progress reporting and restart-from-best behavior.
type RunOptions struct {
	Observer    Observer
	Initial     *Candidate
	ResumeCount int
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

// Optimizer defines an optimization algorithm interface
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
