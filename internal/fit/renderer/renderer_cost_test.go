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

func TestCPURendererPrecomputesInitialSSD(t *testing.T) {
	const width, height = 37, 29
	reference := randomNRGBA(width, height, 42)
	tests := []struct {
		name   string
		canvas *image.NRGBA
	}{
		{name: "white"},
		{name: "custom", canvas: randomNRGBA(width, height, 7)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var renderer *CPURenderer
			if test.canvas == nil {
				renderer = NewCPURenderer(reference, 2)
			} else {
				renderer = NewCPURendererWithCanvas(reference, test.canvas, 2)
			}

			want, ok := fit.ExactSSD(renderer.initialCanvas(), reference)
			if !ok {
				t.Fatal("ExactSSD rejected renderer initial canvas")
			}
			if !renderer.initialSSDValid || renderer.initialSSD != want {
				t.Fatalf("precomputed SSD = (%d, %v), want (%d, true)", renderer.initialSSD, renderer.initialSSDValid, want)
			}

			session, cleanup, err := renderer.newSession(1)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			inherited := session.(*CPURenderer)
			if inherited.initialSSD != want || inherited.initialSSDValid != renderer.initialSSDValid {
				t.Fatalf("inherited SSD = (%d, %v), want (%d, true)", inherited.initialSSD, inherited.initialSSDValid, want)
			}
		})
	}

	base := NewCPURenderer(reference, 2)
	retained := base.Render(deterministicParams(2, width, height, 99))
	wantRetained, ok := fit.ExactSSD(retained, reference)
	if !ok {
		t.Fatal("ExactSSD rejected retained canvas")
	}
	stagedSession, cleanup, err := base.newSessionWithCanvas(retained, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	staged := stagedSession.(*CPURenderer)
	if !staged.initialSSDValid || staged.initialSSD != wantRetained {
		t.Fatalf("staged SSD = (%d, %v), want (%d, true)", staged.initialSSD, staged.initialSSDValid, wantRetained)
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

// BenchmarkIncrementalCostBaseline isolates the work that a future dirty-region
// cost path could avoid. Joint evaluates all circles over a white canvas, while
// sequential and batch evaluate one or five new circles over an already
// retained 45-circle canvas. Every case uses the same image dimensions and one
// rendering thread so the full-image SSD cost is directly comparable.
func BenchmarkIncrementalCostBaseline(b *testing.B) {
	const (
		width           = 256
		height          = 256
		retainedCircles = 45
	)

	reference := randomNRGBA(width, height, 42)
	retainedRenderer := NewCPURenderer(reference, retainedCircles)
	retainedRenderer.SetThreads(1)
	retained := retainedRenderer.Render(deterministicParams(retainedCircles, width, height, 99))

	workloads := []struct {
		name       string
		circles    int
		baseCanvas *image.NRGBA
	}{
		{name: "joint_K50", circles: 50},
		{name: "sequential_K1", circles: 1, baseCanvas: retained},
		{name: "batch_K5", circles: 5, baseCanvas: retained},
	}

	b.Logf("Using SSD backend: %s", fit.ActiveSSDBackend)
	for _, workload := range workloads {
		params := deterministicParams(workload.circles, width, height, int64(200+workload.circles))
		newRenderer := func() *CPURenderer {
			var renderer *CPURenderer
			if workload.baseCanvas == nil {
				renderer = NewCPURenderer(reference, workload.circles)
			} else {
				renderer = NewCPURendererWithCanvas(reference, workload.baseCanvas, workload.circles)
			}
			renderer.SetThreads(1)
			return renderer
		}

		b.Run(workload.name+"/Render", func(b *testing.B) {
			renderer := newRenderer()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rendererImageSink = renderer.Render(params)
			}
		})

		b.Run(workload.name+"/FullImageSSD", func(b *testing.B) {
			rendered := newRenderer().Render(params)
			b.SetBytes(int64(width * height * 8))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rendererCostSink = fit.FastMSECost(rendered, reference)
			}
		})

		b.Run(workload.name+"/Cost", func(b *testing.B) {
			renderer := newRenderer()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rendererCostSink = renderer.Cost(params)
			}
		})
	}
}

var (
	rendererCostSink  float64
	rendererImageSink *image.NRGBA
)
