package fit

import (
	"log/slog"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// OptimizationResult holds the output of an optimization run
type OptimizationResult struct {
	BestParams  []float64
	BestCost    float64
	InitialCost float64
	Iterations  int
}

// OptimizeJoint optimizes all K circles simultaneously
func OptimizeJoint(renderer Renderer, optimizer opt.Optimizer, k int) *OptimizationResult {
	slog.Info("Starting joint optimization", "circles", k)

	dim := k * paramsPerCircle
	lower, upper := renderer.Bounds()

	// Ensure bounds match dimension
	if len(lower) < dim || len(upper) < dim {
		// Extend bounds if needed (renderer created with fewer circles)
		newRenderer := NewCPURenderer(renderer.Reference(), k)
		renderer = newRenderer
		lower, upper = renderer.Bounds()
	}

	// Trim to actual dimension
	lower = lower[:dim]
	upper = upper[:dim]

	// Initial cost (white canvas)
	initialParams := make([]float64, dim)
	initialCost := renderer.Cost(initialParams)

	// Run optimizer
	bestParams, bestCost := optimizer.Run(renderer.Cost, lower, upper, dim)

	slog.Info("Joint optimization complete", "initial_cost", initialCost, "best_cost", bestCost)

	return &OptimizationResult{
		BestParams:  bestParams,
		BestCost:    bestCost,
		InitialCost: initialCost,
	}
}
