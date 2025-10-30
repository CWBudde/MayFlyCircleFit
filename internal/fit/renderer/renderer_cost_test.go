package renderer

import (
	"image"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// TestCPURenderer_CostFunctionSelection verifies cost function can be switched
func TestCPURenderer_CostFunctionSelection(t *testing.T) {
	// Create a simple reference image
	ref := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := range ref.Pix {
		ref.Pix[i] = 128 // Mid-gray
	}

	// Create renderer with 10 circles
	r := NewCPURenderer(ref, 10)

	// Create some random parameters
	params := make([]float64, 10*7)
	for i := range params {
		params[i] = float64(i * 10)
	}

	// Test default cost function (MSECost)
	cost1 := r.Cost(params)
	if cost1 <= 0 {
		t.Errorf("Default cost should be > 0, got %f", cost1)
	}

	// Switch to fast cost function
	r.UseFastCost()
	cost2 := r.Cost(params)

	// Both should produce similar results (within floating point tolerance)
	diff := math.Abs(cost1 - cost2)
	tolerance := math.Max(cost1, cost2) * 0.001 // 0.1% tolerance
	if diff > tolerance {
		t.Errorf("Cost functions differ too much: MSECost=%f, FastMSECost=%f, diff=%f",
			cost1, cost2, diff)
	}

	t.Logf("MSECost: %f, FastMSECost: %f, diff: %f", cost1, cost2, diff)
}

// TestCPURenderer_FastCostCorrectness verifies FastMSECost produces correct results
func TestCPURenderer_FastCostCorrectness(t *testing.T) {
	// Create reference image with known pattern
	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			idx := y*ref.Stride + x*4
			ref.Pix[idx+0] = uint8(x * 8)   // R varies horizontally
			ref.Pix[idx+1] = uint8(y * 8)   // G varies vertically
			ref.Pix[idx+2] = 128            // B constant
			ref.Pix[idx+3] = 255            // A opaque
		}
	}

	r := NewCPURenderer(ref, 5)

	// Create parameters that render exactly the reference (empty params = white canvas)
	params := make([]float64, 5*7)

	// Test with default cost
	costDefault := r.Cost(params)

	// Test with fast cost
	r.UseFastCost()
	costFast := r.Cost(params)

	// Verify both non-zero (white canvas != patterned reference)
	if costDefault <= 0 || costFast <= 0 {
		t.Errorf("Both costs should be > 0: default=%f, fast=%f", costDefault, costFast)
	}

	// Verify they're close
	relDiff := math.Abs(costDefault-costFast) / costDefault
	if relDiff > 0.01 { // 1% tolerance
		t.Errorf("Costs differ by %.2f%%: default=%f, fast=%f",
			relDiff*100, costDefault, costFast)
	}
}

// TestCPURenderer_SetCostFunc verifies custom cost functions can be set
func TestCPURenderer_SetCostFunc(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	r := NewCPURenderer(ref, 1)

	// Custom cost function that always returns 42
	customCost := func(current, reference *image.NRGBA) float64 {
		return 42.0
	}

	r.SetCostFunc(customCost)
	params := make([]float64, 7)

	cost := r.Cost(params)
	if cost != 42.0 {
		t.Errorf("Custom cost function should return 42.0, got %f", cost)
	}
}

// BenchmarkCPURenderer_Cost_MSE benchmarks rendering with default MSECost
func BenchmarkCPURenderer_Cost_MSE(b *testing.B) {
	ref := randomNRGBA(128, 128, 42)
	r := NewCPURenderer(ref, 20)

	params := make([]float64, 20*7)
	for i := range params {
		params[i] = float64(i * 3)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Cost(params)
	}
}

// BenchmarkCPURenderer_Cost_Fast benchmarks rendering with FastMSECost
func BenchmarkCPURenderer_Cost_Fast(b *testing.B) {
	ref := randomNRGBA(128, 128, 42)
	r := NewCPURenderer(ref, 20)
	r.UseFastCost() // Enable SIMD-accelerated cost

	params := make([]float64, 20*7)
	for i := range params {
		params[i] = float64(i * 3)
	}

	b.Logf("Using SSD backend: %s", fit.ActiveSSDBackend)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Cost(params)
	}
}

// BenchmarkCPURenderer_CostComparison compares MSECost vs FastMSECost
func BenchmarkCPURenderer_CostComparison(b *testing.B) {
	sizes := []struct {
		name   string
		width  int
		height int
		circles int
	}{
		{"64x64_10circles", 64, 64, 10},
		{"128x128_20circles", 128, 128, 20},
		{"256x256_50circles", 256, 256, 50},
	}

	for _, sz := range sizes {
		ref := randomNRGBA(sz.width, sz.height, 42)
		params := make([]float64, sz.circles*7)
		for i := range params {
			params[i] = float64(i * 3)
		}

		b.Run(sz.name+"/MSECost", func(b *testing.B) {
			r := NewCPURenderer(ref, sz.circles)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = r.Cost(params)
			}
		})

		b.Run(sz.name+"/FastMSECost", func(b *testing.B) {
			r := NewCPURenderer(ref, sz.circles)
			r.UseFastCost()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = r.Cost(params)
			}
		})
	}
}
