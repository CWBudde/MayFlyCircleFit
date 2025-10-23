package opt

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
