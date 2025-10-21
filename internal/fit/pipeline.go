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

// OptimizeSequential optimizes circles one at a time (greedy)
func OptimizeSequential(renderer Renderer, optimizer opt.Optimizer, totalK int) *OptimizationResult {
	slog.Info("Starting sequential optimization", "total_circles", totalK)

	ref := renderer.Reference()
	allParams := []float64{}

	initialCost := MSECost(
		NewCPURenderer(ref, 0).Render([]float64{}),
		ref,
	)

	for k := 1; k <= totalK; k++ {
		slog.Info("Optimizing circle", "index", k, "of", totalK)

		// Create renderer with k circles
		currentRenderer := NewCPURenderer(ref, k)

		// Objective: optimize only the new circle, keeping previous ones fixed
		dim := paramsPerCircle
		lower := make([]float64, dim)
		upper := make([]float64, dim)
		bounds := NewBounds(1, ref.Bounds().Dx(), ref.Bounds().Dy())
		copy(lower, bounds.Lower)
		copy(upper, bounds.Upper)

		evalFunc := func(newCircleParams []float64) float64 {
			// Combine previous circles + new circle
			combined := append(append([]float64{}, allParams...), newCircleParams...)
			return currentRenderer.Cost(combined)
		}

		bestNew, _ := optimizer.Run(evalFunc, lower, upper, dim)
		allParams = append(allParams, bestNew...)
	}

	finalRenderer := NewCPURenderer(ref, totalK)
	finalCost := finalRenderer.Cost(allParams)

	slog.Info("Sequential optimization complete", "initial_cost", initialCost, "final_cost", finalCost)

	return &OptimizationResult{
		BestParams:  allParams,
		BestCost:    finalCost,
		InitialCost: initialCost,
	}
}
