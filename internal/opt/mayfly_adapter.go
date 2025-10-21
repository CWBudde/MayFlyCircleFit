package opt

import (
	"math/rand"

	"github.com/CWBudde/mayfly"
)

// MayflyAdapter wraps the external Mayfly library to conform to our Optimizer interface
type MayflyAdapter struct {
	maxIters int
	popSize  int
	seed     int64
}

// NewMayfly creates a new Mayfly optimizer adapter
func NewMayfly(maxIters, popSize int, seed int64) Optimizer {
	return &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
	}
}

// Run executes the Mayfly optimization using the external library
func (m *MayflyAdapter) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	// Create config for external Mayfly library
	config := mayfly.NewDefaultConfig()

	// Configure the optimizer
	config.ObjectiveFunc = eval
	config.ProblemSize = dim
	config.MaxIterations = m.maxIters
	config.NPop = m.popSize

	// Set bounds (external library uses scalar bounds)
	// Assumes all dimensions have same bounds - use first dimension
	config.LowerBound = lower[0]
	config.UpperBound = upper[0]

	// Set random seed for reproducibility
	config.Rand = rand.New(rand.NewSource(m.seed))

	// Run optimization
	result, err := mayfly.Optimize(config)
	if err != nil {
		// Fallback to zero vector if optimization fails
		return make([]float64, dim), eval(make([]float64, dim))
	}

	return result.GlobalBest.Position, result.GlobalBest.Cost
}