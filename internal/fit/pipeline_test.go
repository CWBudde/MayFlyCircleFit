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

	result := OptimizeJoint(renderer, optimizer, 1)

	if result.BestCost >= result.InitialCost {
		t.Errorf("Optimization did not improve: initial=%f, best=%f", result.InitialCost, result.BestCost)
	}

	if len(result.BestParams) != 7 {
		t.Errorf("Expected 7 parameters for 1 circle, got %d", len(result.BestParams))
	}
}
