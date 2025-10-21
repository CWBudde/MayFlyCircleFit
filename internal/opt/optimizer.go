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
