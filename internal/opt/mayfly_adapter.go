package opt

import (
	"math/rand"

	"github.com/cwbudde/mayfly"
)

// MayflyAdapter wraps the external Mayfly library to conform to our Optimizer interface
type MayflyAdapter struct {
	maxIters int
	popSize  int
	seed     int64
	variant  string // "standard", "desma", "olce", "eobbma", "gsasma", "mpma", "aoblmoa"
}

// NewMayfly creates a new Mayfly optimizer adapter
func NewMayfly(maxIters, popSize int, seed int64) Optimizer {
	return &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
		variant:  "standard",
	}
}

// NewMayflyDESMA creates a Mayfly optimizer using the DESMA variant
func NewMayflyDESMA(maxIters, popSize int, seed int64) Optimizer {
	return &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
		variant:  "desma",
	}
}

// NewMayflyOLCE creates a Mayfly optimizer using the OLCE-MA variant
func NewMayflyOLCE(maxIters, popSize int, seed int64) Optimizer {
	return &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
		variant:  "olce",
	}
}

// RunWithInitial executes optimization starting from an initial solution.
//
// IMPLEMENTATION NOTE: The external Mayfly library does not support custom
// population initialization. Therefore, this implementation uses a simple
// strategy: run the optimizer and return the better of (optimizer result, initial solution).
//
// This ensures we never lose progress when resuming from a checkpoint:
//   - If the optimizer finds a better solution, we use it
//   - If not, we keep the checkpoint solution
//
// Future improvement: Switch to a different optimizer library that supports
// population seeding, or fork the Mayfly library to add this feature.
func (m *MayflyAdapter) RunWithInitial(initialParams []float64, initialCost float64, eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	// Run the optimizer normally
	bestParams, bestCost := m.Run(eval, lower, upper, dim)

	// Compare with initial solution and return the better one
	if bestCost < initialCost {
		// Optimizer found something better
		return bestParams, bestCost
	}

	// Initial solution is still best - don't lose progress
	return initialParams, initialCost
}

// Run executes the Mayfly optimization using the external library
func (m *MayflyAdapter) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	var config *mayfly.Config

	// Select variant
	switch m.variant {
	case "desma":
		config = mayfly.NewDESMAConfig()
	case "olce":
		config = mayfly.NewOLCEConfig()
	case "eobbma":
		config = mayfly.NewEOBBMAConfig()
	case "gsasma":
		config = mayfly.NewGSASMAConfig()
	case "mpma":
		config = mayfly.NewMPMAConfig()
	case "aoblmoa":
		config = mayfly.NewAOBLMOAConfig()
	default:
		config = mayfly.NewDefaultConfig()
	}

	// Denormalize parameters from [0,1] to actual bounds
	// (mayfly only supports uniform bounds, so we normalize to [0,1])
	denormalize := func(params []float64) []float64 {
		result := make([]float64, len(params))
		for i := range params {
			result[i] = lower[i] + params[i]*(upper[i]-lower[i])
		}
		return result
	}

	// Wrap eval function to handle normalization
	normalizedEval := func(normalizedParams []float64) float64 {
		denormalizedParams := denormalize(normalizedParams)
		return eval(denormalizedParams)
	}

	config.ObjectiveFunc = normalizedEval
	config.ProblemSize = dim
	config.MaxIterations = m.maxIters
	config.NPop = m.popSize
	config.LowerBound = 0.0
	config.UpperBound = 1.0

	// Create RNG for population initialization
	config.Rand = rand.New(rand.NewSource(m.seed))

	result, err := mayfly.Optimize(config)
	if err != nil {
		return make([]float64, dim), eval(make([]float64, dim))
	}

	// Denormalize result before returning
	return denormalize(result.GlobalBest.Position), result.GlobalBest.Cost
}
