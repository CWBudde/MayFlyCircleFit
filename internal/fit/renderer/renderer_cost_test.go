package renderer

import (
	"image"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestCPURenderer_DefaultCostMatchesMSE(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		circles       int
		customCanvas  bool
	}{
		{name: "single_pixel", width: 1, height: 1},
		{name: "below_simd_batch", width: 7, height: 5, circles: 2},
		{name: "exact_simd_batch", width: 8, height: 8, circles: 4},
		{name: "simd_remainder", width: 9, height: 13, circles: 5},
		{name: "odd_rectangle", width: 127, height: 91, circles: 24, customCanvas: true},
		{name: "large_rectangle", width: 257, height: 193, circles: 48, customCanvas: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reference := randomNRGBA(test.width, test.height, 42)
			params := deterministicParams(test.circles, test.width, test.height, 99)

			var r *CPURenderer
			if test.customCanvas {
				canvas := randomNRGBA(test.width, test.height, 7)
				r = NewCPURendererWithCanvas(reference, canvas, test.circles)
			} else {
				r = NewCPURenderer(reference, test.circles)
			}
			r.SetThreads(1)

			got := r.Cost(params)
			r.SetCostFunc(fit.MSECost)
			want := r.Cost(params)
			if got != want {
				t.Fatalf("default FastMSECost = %v, MSECost = %v", got, want)
			}

			r.UseFastCost()
			if restored := r.Cost(params); restored != want {
				t.Fatalf("restored FastMSECost = %v, MSECost = %v", restored, want)
			}
		})
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

// BenchmarkCPURenderer_Cost_MSE benchmarks rendering with explicit MSECost.
func BenchmarkCPURenderer_Cost_MSE(b *testing.B) {
	ref := randomNRGBA(128, 128, 42)
	r := NewCPURenderer(ref, 20)
	r.SetThreads(1)
	r.SetCostFunc(fit.MSECost)
	params := deterministicParams(20, 128, 128, 99)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rendererCostSink = r.Cost(params)
	}
}

// BenchmarkCPURenderer_Cost_Fast benchmarks the production FastMSECost default.
func BenchmarkCPURenderer_Cost_Fast(b *testing.B) {
	ref := randomNRGBA(128, 128, 42)
	r := NewCPURenderer(ref, 20)
	r.SetThreads(1)
	params := deterministicParams(20, 128, 128, 99)

	b.Logf("Using SSD backend: %s", fit.ActiveSSDBackend)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rendererCostSink = r.Cost(params)
	}
}

// BenchmarkCPURenderer_CostComparison compares MSECost vs FastMSECost
func BenchmarkCPURenderer_CostComparison(b *testing.B) {
	sizes := []struct {
		name    string
		width   int
		height  int
		circles int
	}{
		{"64x64_10circles", 64, 64, 10},
		{"256x256_50circles", 256, 256, 50},
		{"512x512_100circles", 512, 512, 100},
	}

	for _, sz := range sizes {
		ref := randomNRGBA(sz.width, sz.height, 42)
		params := deterministicParams(sz.circles, sz.width, sz.height, 99)

		b.Run(sz.name+"/MSECost", func(b *testing.B) {
			r := NewCPURenderer(ref, sz.circles)
			r.SetThreads(1)
			r.SetCostFunc(fit.MSECost)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rendererCostSink = r.Cost(params)
			}
		})

		b.Run(sz.name+"/FastMSECost", func(b *testing.B) {
			r := NewCPURenderer(ref, sz.circles)
			r.SetThreads(1)
			b.ReportAllocs()
			b.Logf("Using SSD backend: %s", fit.ActiveSSDBackend)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rendererCostSink = r.Cost(params)
			}
		})
	}
}

var rendererCostSink float64
