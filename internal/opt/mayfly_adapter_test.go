package opt

import (
	"math"
	"testing"
)

// Sphere function: f(x) = sum(x_i^2), minimum at origin
func sphere(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return sum
}

func TestMayflyAdapterOnSphere(t *testing.T) {
	optimizer := NewMayfly(100, 20, 42) // maxIters, popSize, seed

	dim := 3
	lower := make([]float64, dim)
	upper := make([]float64, dim)
	for i := 0; i < dim; i++ {
		lower[i] = -10
		upper[i] = 10
	}

	best, cost := optimizer.Run(sphere, lower, upper, dim)

	if len(best) != dim {
		t.Fatalf("Expected %d parameters, got %d", dim, len(best))
	}

	// Should converge close to zero
	if cost > 0.1 {
		t.Errorf("Expected cost near 0, got %f", cost)
	}

	// Check that best params are near origin
	for i, v := range best {
		if math.Abs(v) > 1.0 {
			t.Errorf("Parameter %d = %f, expected near 0", i, v)
		}
	}
}

func TestMayflyAdapterDeterministic(t *testing.T) {
	dim := 2
	lower := []float64{-5, -5}
	upper := []float64{5, 5}

	// Run twice with same seed (popSize must be >=20 for mayfly v0.1.0)
	optimizer1 := NewMayfly(50, 20, 123)
	_, cost1 := optimizer1.Run(sphere, lower, upper, dim)

	optimizer2 := NewMayfly(50, 20, 123)
	_, cost2 := optimizer2.Run(sphere, lower, upper, dim)

	if cost1 != cost2 {
		t.Errorf("Non-deterministic: cost1=%f, cost2=%f", cost1, cost2)
	}
}

func TestMayflyAdapter_RunWithInitial(t *testing.T) {
	dim := 3
	lower := make([]float64, dim)
	upper := make([]float64, dim)
	for i := 0; i < dim; i++ {
		lower[i] = -10
		upper[i] = 10
	}

	// Start with a suboptimal initial solution
	initialParams := []float64{5.0, 5.0, 5.0}
	initialCost := sphere(initialParams) // Should be 75.0

	// Cast to ResumableOptimizer
	optimizer := NewMayfly(100, 20, 42)
	resumable, ok := optimizer.(ResumableOptimizer)
	if !ok {
		t.Fatal("MayflyAdapter should implement ResumableOptimizer")
	}

	// Run with initial solution
	best, cost := resumable.RunWithInitial(initialParams, initialCost, sphere, lower, upper, dim)

	// Should improve from initial cost
	if cost >= initialCost {
		t.Errorf("Expected improvement: initial=%f, final=%f", initialCost, cost)
	}

	// Should converge close to zero
	if cost > 0.1 {
		t.Errorf("Expected cost near 0, got %f", cost)
	}

	// Check that best params are near origin
	for i, v := range best {
		if math.Abs(v) > 1.0 {
			t.Errorf("Parameter %d = %f, expected near 0", i, v)
		}
	}
}

func TestMayflyAdapter_RunWithInitial_AlreadyOptimal(t *testing.T) {
	dim := 2
	lower := []float64{-10, -10}
	upper := []float64{10, 10}

	// Start with optimal solution (at origin)
	initialParams := []float64{0.0, 0.0}
	initialCost := sphere(initialParams) // Should be 0.0

	optimizer := NewMayfly(50, 20, 42)
	resumable := optimizer.(ResumableOptimizer)

	// Run with initial solution
	_, cost := resumable.RunWithInitial(initialParams, initialCost, sphere, lower, upper, dim)

	// Should stay at or near optimal
	if cost > 0.01 {
		t.Errorf("Expected cost near 0, got %f", cost)
	}
}

func TestMayflyAdapter_RunWithInitial_VsFromScratch(t *testing.T) {
	dim := 3
	lower := make([]float64, dim)
	upper := make([]float64, dim)
	for i := 0; i < dim; i++ {
		lower[i] = -10
		upper[i] = 10
	}

	// Run from scratch with limited iterations
	optimizer1 := NewMayfly(50, 20, 42)
	_, costFromScratch := optimizer1.Run(sphere, lower, upper, dim)

	// Get intermediate solution after 50 iterations (simulated checkpoint)
	optimizer2 := NewMayfly(50, 20, 42)
	intermediateParams, intermediateCost := optimizer2.Run(sphere, lower, upper, dim)

	// Resume from intermediate solution with more iterations
	optimizer3 := NewMayfly(100, 20, 43) // Different seed for resumed run
	resumable := optimizer3.(ResumableOptimizer)
	_, costResumed := resumable.RunWithInitial(intermediateParams, intermediateCost, sphere, lower, upper, dim)

	// Resumed run should do at least as well as intermediate (may improve)
	if costResumed > intermediateCost*1.1 { // Allow 10% tolerance for stochastic variation
		t.Errorf("Resumed cost (%f) worse than intermediate (%f)", costResumed, intermediateCost)
	}

	t.Logf("From scratch (50 iters): %f", costFromScratch)
	t.Logf("Intermediate (50 iters): %f", intermediateCost)
	t.Logf("Resumed (100 iters): %f", costResumed)
}

func TestMayflyAdapter_RunWithInitial_KeepsCheckpointIfBetter(t *testing.T) {
	dim := 2
	lower := []float64{-10, -10}
	upper := []float64{10, 10}

	// Start with a very good solution
	initialParams := []float64{0.01, 0.01}
	initialCost := sphere(initialParams) // Very close to optimal

	// Use very few iterations so optimizer unlikely to beat initial solution
	optimizer := NewMayfly(5, 20, 42)
	resumable := optimizer.(ResumableOptimizer)

	// Run with initial solution
	best, cost := resumable.RunWithInitial(initialParams, initialCost, sphere, lower, upper, dim)

	// Should keep the initial solution if optimizer didn't find better
	// (Allow either optimizer found better OR kept initial)
	if cost > initialCost {
		t.Errorf("Resume should never worsen: initial=%f, final=%f", initialCost, cost)
	}

	t.Logf("Initial cost: %f, Final cost: %f", initialCost, cost)
	t.Logf("Initial params: %v, Final params: %v", initialParams, best)
}
