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
