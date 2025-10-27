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
// Note: Convergence config is not used for joint mode (all circles optimized at once)
func OptimizeJoint(renderer Renderer, optimizer opt.Optimizer, k int, _ ConvergenceConfig) *OptimizationResult {
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

// OptimizeSequential optimizes circles one at a time (greedy) with adaptive convergence
func OptimizeSequential(renderer Renderer, optimizer opt.Optimizer, totalK int, convergenceConfig ConvergenceConfig) *OptimizationResult {
	slog.Info("Starting sequential optimization",
		"total_circles", totalK,
		"convergence_enabled", convergenceConfig.Enabled,
		"patience", convergenceConfig.Patience,
		"threshold", convergenceConfig.Threshold,
	)

	ref := renderer.Reference()
	allParams := []float64{}

	initialCost := MSECost(
		NewCPURenderer(ref, 0).Render([]float64{}),
		ref,
	)

	// Create convergence tracker
	tracker := NewConvergenceTracker(convergenceConfig)

	actualK := 0
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
		actualK = k

		// Check convergence
		finalCost := currentRenderer.Cost(allParams)
		if tracker.Update(finalCost) {
			slog.Info("Convergence detected - stopping early",
				"circles_used", actualK,
				"circles_requested", totalK,
				"final_cost", finalCost,
			)
			break
		}
	}

	finalRenderer := NewCPURenderer(ref, actualK)
	finalCost := finalRenderer.Cost(allParams)

	slog.Info("Sequential optimization complete",
		"initial_cost", initialCost,
		"final_cost", finalCost,
		"circles_used", actualK,
		"circles_requested", totalK,
	)

	return &OptimizationResult{
		BestParams:  allParams,
		BestCost:    finalCost,
		InitialCost: initialCost,
	}
}

// OptimizeBatch adds batchK circles per pass for multiple passes with adaptive convergence
func OptimizeBatch(renderer Renderer, optimizer opt.Optimizer, batchK, passes int, convergenceConfig ConvergenceConfig) *OptimizationResult {
	slog.Info("Starting batch optimization",
		"batch_size", batchK,
		"passes", passes,
		"convergence_enabled", convergenceConfig.Enabled,
		"patience", convergenceConfig.Patience,
		"threshold", convergenceConfig.Threshold,
	)

	ref := renderer.Reference()
	allParams := []float64{}

	initialCost := MSECost(
		NewCPURenderer(ref, 0).Render([]float64{}),
		ref,
	)

	// Create convergence tracker
	tracker := NewConvergenceTracker(convergenceConfig)

	actualPasses := 0
	for pass := 0; pass < passes; pass++ {
		slog.Info("Batch pass", "pass", pass+1, "of", passes)

		currentK := len(allParams) / paramsPerCircle
		newK := currentK + batchK

		// Optimize batch of circles jointly
		batchRenderer := NewCPURenderer(ref, newK)

		dim := batchK * paramsPerCircle
		lower := make([]float64, dim)
		upper := make([]float64, dim)
		bounds := NewBounds(batchK, ref.Bounds().Dx(), ref.Bounds().Dy())
		copy(lower, bounds.Lower)
		copy(upper, bounds.Upper)

		evalFunc := func(newBatchParams []float64) float64 {
			combined := append(append([]float64{}, allParams...), newBatchParams...)
			return batchRenderer.Cost(combined)
		}

		bestBatch, _ := optimizer.Run(evalFunc, lower, upper, dim)
		allParams = append(allParams, bestBatch...)
		actualPasses = pass + 1

		// Check convergence
		finalCost := batchRenderer.Cost(allParams)
		if tracker.Update(finalCost) {
			slog.Info("Convergence detected - stopping early",
				"passes_used", actualPasses,
				"passes_requested", passes,
				"final_cost", finalCost,
			)
			break
		}
	}

	totalK := len(allParams) / paramsPerCircle
	finalRenderer := NewCPURenderer(ref, totalK)
	finalCost := finalRenderer.Cost(allParams)

	slog.Info("Batch optimization complete",
		"total_circles", totalK,
		"final_cost", finalCost,
		"passes_used", actualPasses,
		"passes_requested", passes,
	)

	return &OptimizationResult{
		BestParams:  allParams,
		BestCost:    finalCost,
		InitialCost: initialCost,
	}
}
