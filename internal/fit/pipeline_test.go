package fit

import (
	"image"
	"image/color"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

func TestOptimizeJoint(t *testing.T) {
	// Create simple 10x10 reference with red circle
	ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			ref.Set(x, y, white)
		}
	}

	// Add a red center pixel
	ref.Set(5, 5, color.NRGBA{255, 0, 0, 255})

	renderer := NewCPURenderer(ref, 1)

	optimizer := opt.NewMayfly(50, 20, 42) // maxIters, popSize, seed (popSize must be >=20)

	// Convergence config not used for joint mode
	result := OptimizeJoint(renderer, optimizer, 1, DisabledConvergenceConfig())

	if result.BestCost >= result.InitialCost {
		t.Errorf("Optimization did not improve: initial=%f, best=%f", result.InitialCost, result.BestCost)
	}

	if len(result.BestParams) != 7 {
		t.Errorf("Expected 7 parameters for 1 circle, got %d", len(result.BestParams))
	}
}

func TestOptimizeSequential(t *testing.T) {
	// Create simple reference
	ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			ref.Set(x, y, white)
		}
	}
	ref.Set(5, 5, color.NRGBA{255, 0, 0, 255})

	renderer := NewCPURenderer(ref, 1)

	optimizer := opt.NewMayfly(30, 20, 42) // maxIters, popSize, seed

	// Disable convergence for deterministic test
	result := OptimizeSequential(renderer, optimizer, 2, DisabledConvergenceConfig())

	if result.BestCost >= result.InitialCost {
		t.Errorf("Optimization did not improve")
	}

	if len(result.BestParams) != 14 { // 2 circles * 7 params
		t.Errorf("Expected 14 parameters for 2 circles, got %d", len(result.BestParams))
	}
}

func TestOptimizeBatch(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			ref.Set(x, y, white)
		}
	}
	ref.Set(5, 5, color.NRGBA{255, 0, 0, 255})

	renderer := NewCPURenderer(ref, 1)

	optimizer := opt.NewMayfly(30, 20, 42) // maxIters, popSize, seed

	// 2 passes of 2 circles each = 4 circles total
	// Disable convergence for deterministic test
	result := OptimizeBatch(renderer, optimizer, 2, 2, DisabledConvergenceConfig())

	if len(result.BestParams) != 28 { // 4 circles * 7 params
		t.Errorf("Expected 28 parameters for 4 circles, got %d", len(result.BestParams))
	}
}
