package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestCPURendererWhiteCanvas(t *testing.T) {
	// Create a white 10x10 reference
	ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			ref.Set(x, y, white)
		}
	}

	renderer := NewCPURenderer(ref, 0) // 0 circles

	// Empty params should render white canvas
	result := renderer.Render([]float64{})

	// Check if result is all white
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			r, g, b, a := result.At(x, y).RGBA()
			if r != 65535 || g != 65535 || b != 65535 || a != 65535 {
				t.Errorf("Pixel (%d,%d) not white: got (%d,%d,%d,%d)", x, y, r, g, b, a)
			}
		}
	}

	cost := renderer.Cost([]float64{})
	if cost != 0 {
		t.Errorf("White canvas vs white reference should have cost 0, got %f", cost)
	}
}

func TestCPURendererSingleCircle(t *testing.T) {
	// Create a white 20x20 reference
	ref := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			ref.Set(x, y, white)
		}
	}

	renderer := NewCPURenderer(ref, 1)

	// Red circle at center
	params := []float64{
		10, 10, 5, // x, y, r
		1.0, 0.0, 0.0, // red
		1.0, // opaque
	}

	result := renderer.Render(params)

	// Center pixel should be red
	r, g, b, _ := result.At(10, 10).RGBA()
	if r != 65535 || g != 0 || b != 0 {
		t.Errorf("Center pixel should be red, got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}

	// Corner pixel should still be white
	r, g, b, _ = result.At(0, 0).RGBA()
	if r != 65535 || g != 65535 || b != 65535 {
		t.Errorf("Corner pixel should be white, got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}
}

func TestCPURendererParallelMatchesSingleThreaded(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		circles       int
		customCanvas  bool
	}{
		{name: "odd dimensions", width: 127, height: 91, circles: 24},
		{name: "more threads than rows", width: 19, height: 3, circles: 8},
		{name: "custom canvas", width: 96, height: 65, circles: 16, customCanvas: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := randomNRGBA(test.width, test.height, 42)
			params := deterministicParams(test.circles, test.width, test.height, 99)
			var single, parallel *CPURenderer
			if test.customCanvas {
				canvas := randomNRGBA(test.width, test.height, 7)
				single = NewCPURendererWithCanvas(ref, canvas, test.circles)
				parallel = NewCPURendererWithCanvas(ref, canvas, test.circles)
			} else {
				single = NewCPURenderer(ref, test.circles)
				parallel = NewCPURenderer(ref, test.circles)
			}
			single.SetThreads(1)
			parallel.SetThreads(runtime.GOMAXPROCS(0) + test.height)

			want := append([]byte(nil), single.Render(params).Pix...)
			got := parallel.Render(params)
			if !bytes.Equal(got.Pix, want) {
				t.Fatal("parallel rendering differs from single-threaded rendering")
			}
			wantThreads := min(runtime.GOMAXPROCS(0), test.height)
			if parallel.Threads() != wantThreads {
				t.Fatalf("effective threads = %d, want %d", parallel.Threads(), wantThreads)
			}
		})
	}
}

func TestCPURendererParallelRenderStable(t *testing.T) {
	const (
		width   = 193
		height  = 129
		circles = 32
	)
	ref := randomNRGBA(width, height, 42)
	renderer := NewCPURenderer(ref, circles)
	renderer.SetThreads(4)
	params := deterministicParams(circles, width, height, 101)
	want := append([]byte(nil), renderer.Render(params).Pix...)

	for i := 0; i < 50; i++ {
		if got := renderer.Render(params); !bytes.Equal(got.Pix, want) {
			t.Fatalf("render %d differs from the first parallel render", i+1)
		}
	}
}

func TestCPURendererSessionsPreserveThreads(t *testing.T) {
	ref := randomNRGBA(32, 32, 42)
	base := NewCPURenderer(ref, 4)
	base.SetThreads(2)

	session, cleanup, err := base.newSession(3)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	cpuSession, ok := session.(*CPURenderer)
	if !ok {
		t.Fatalf("session type = %T, want *CPURenderer", session)
	}
	if cpuSession.Threads() != base.Threads() {
		t.Fatalf("session threads = %d, want %d", cpuSession.Threads(), base.Threads())
	}
}

// TestScanlineCircleRenderingMatchesOriginal verifies scanline method produces identical results
func TestScanlineCircleRenderingMatchesOriginal(t *testing.T) {
	sizes := []struct {
		name    string
		w, h    int
		circles []fit.Circle
	}{
		{
			"single_centered",
			64, 64,
			[]fit.Circle{{X: 32, Y: 32, R: 20, CR: 1.0, CG: 0.5, CB: 0.0, Opacity: 0.8}},
		},
		{
			"multiple_overlapping",
			128, 128,
			[]fit.Circle{
				{X: 40, Y: 40, R: 25, CR: 1.0, CG: 0.0, CB: 0.0, Opacity: 0.6},
				{X: 60, Y: 60, R: 25, CR: 0.0, CG: 1.0, CB: 0.0, Opacity: 0.6},
				{X: 50, Y: 80, R: 20, CR: 0.0, CG: 0.0, CB: 1.0, Opacity: 0.7},
			},
		},
		{
			"edge_clipping",
			64, 64,
			[]fit.Circle{
				{X: 10, Y: 10, R: 15, CR: 1.0, CG: 1.0, CB: 0.0, Opacity: 1.0},
				{X: 55, Y: 10, R: 15, CR: 0.0, CG: 1.0, CB: 1.0, Opacity: 1.0},
				{X: 32, Y: 60, R: 15, CR: 1.0, CG: 0.0, CB: 1.0, Opacity: 1.0},
			},
		},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			// Create two identical white canvases
			original := image.NewNRGBA(image.Rect(0, 0, tc.w, tc.h))
			scanline := image.NewNRGBA(image.Rect(0, 0, tc.w, tc.h))
			for i := range original.Pix {
				original.Pix[i] = 255
				scanline.Pix[i] = 255
			}

			renderer := &CPURenderer{width: tc.w, height: tc.h}

			// Render with original method
			for _, circle := range tc.circles {
				renderer.renderCircle(original, circle)
			}

			// Render with scanline method
			for _, circle := range tc.circles {
				renderer.renderCircleScanline(scanline, circle)
			}

			// Compare pixel-by-pixel
			maxDiff := 0
			diffCount := 0
			for y := 0; y < tc.h; y++ {
				for x := 0; x < tc.w; x++ {
					idx := y*original.Stride + x*4
					for c := 0; c < 4; c++ {
						diff := int(original.Pix[idx+c]) - int(scanline.Pix[idx+c])
						if diff < 0 {
							diff = -diff
						}
						if diff > maxDiff {
							maxDiff = diff
						}
						if diff > 1 { // Allow 1-bit rounding difference
							diffCount++
							if diffCount <= 5 { // Report first few differences
								t.Errorf("Pixel (%d,%d) channel %d differs: original=%d scanline=%d",
									x, y, c, original.Pix[idx+c], scanline.Pix[idx+c])
							}
						}
					}
				}
			}

			if diffCount > 0 {
				t.Errorf("Total differences (>1 bit): %d pixels, max diff: %d", diffCount, maxDiff)
			}
		})
	}
}

// BenchmarkCPURenderer_Render benchmarks pure circle rendering without cost computation
func BenchmarkCPURenderer_Render(b *testing.B) {
	sizes := []struct {
		name    string
		width   int
		height  int
		circles int
	}{
		{"64x64_10circles", 64, 64, 10},
		{"128x128_20circles", 128, 128, 20},
		{"256x256_50circles", 256, 256, 50},
		{"512x512_100circles", 512, 512, 100},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			ref := randomNRGBA(sz.width, sz.height, 42)
			renderer := NewCPURenderer(ref, sz.circles)
			renderer.SetThreads(1)
			params := randomParams(sz.circles, sz.width, sz.height)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = renderer.Render(params)
			}
		})
	}
}

// BenchmarkCPURendererThreadScaling compares scanline sharding against the
// single-threaded renderer. Keep the workload fixed when recording results.
func BenchmarkCPURendererThreadScaling(b *testing.B) {
	for _, workload := range []struct {
		name          string
		width, height int
		circles       int
	}{
		{name: "32x32_4circles", width: 32, height: 32, circles: 4},
		{name: "128x128_20circles", width: 128, height: 128, circles: 20},
		{name: "512x512_100circles", width: 512, height: 512, circles: 100},
	} {
		ref := randomNRGBA(workload.width, workload.height, 42)
		params := deterministicParams(workload.circles, workload.width, workload.height, 99)
		threadCounts := []int{1, 2, 4, runtime.GOMAXPROCS(0)}
		for _, threadCount := range threadCounts {
			b.Run(workload.name+fmt.Sprintf("/threads=%d", threadCount), func(b *testing.B) {
				renderer := NewCPURenderer(ref, workload.circles)
				renderer.SetThreads(threadCount)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = renderer.Render(params)
				}
			})
		}
	}
}

// BenchmarkCPURenderer_Cost benchmarks full pipeline (rendering + cost)
func BenchmarkCPURenderer_Cost(b *testing.B) {
	sizes := []struct {
		name    string
		width   int
		height  int
		circles int
	}{
		{"64x64_10circles", 64, 64, 10},
		{"128x128_20circles", 128, 128, 20},
		{"256x256_50circles", 256, 256, 50},
		{"512x512_100circles", 512, 512, 100},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			ref := randomNRGBA(sz.width, sz.height, 42)
			renderer := NewCPURenderer(ref, sz.circles)
			params := randomParams(sz.circles, sz.width, sz.height)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = renderer.Cost(params)
			}
		})
	}
}

// BenchmarkCompositePixel benchmarks the alpha compositing operation
func BenchmarkCompositePixel(b *testing.B) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	// Fill with semi-transparent white
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = 200
		img.Pix[i+1] = 200
		img.Pix[i+2] = 200
		img.Pix[i+3] = 200
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Composite red semi-transparent pixel at (128, 128)
		compositePixel(img, 128, 128, 1.0, 0.0, 0.0, 0.5)
	}
}

// BenchmarkRenderCircle benchmarks rendering a single circle
func BenchmarkRenderCircle(b *testing.B) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	renderer := &CPURenderer{
		width:  256,
		height: 256,
	}

	circles := []struct {
		name string
		c    fit.Circle
	}{
		{"R5_small", fit.Circle{X: 128, Y: 128, R: 5, CR: 1.0, CG: 0.5, CB: 0.0, Opacity: 0.7}},
		{"R10_small", fit.Circle{X: 128, Y: 128, R: 10, CR: 1.0, CG: 0.5, CB: 0.0, Opacity: 0.7}},
		{"R15_medium", fit.Circle{X: 128, Y: 128, R: 15, CR: 1.0, CG: 0.5, CB: 0.0, Opacity: 0.7}},
		{"R25_large", fit.Circle{X: 128, Y: 128, R: 25, CR: 1.0, CG: 0.5, CB: 0.0, Opacity: 0.7}},
		{"R50_large", fit.Circle{X: 128, Y: 128, R: 50, CR: 1.0, CG: 0.5, CB: 0.0, Opacity: 0.7}},
	}

	for _, tc := range circles {
		b.Run(tc.name+"/Original", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Reset canvas to white
				for j := range img.Pix {
					img.Pix[j] = 255
				}
				renderer.renderCircle(img, tc.c)
			}
		})

		b.Run(tc.name+"/Scanline", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Reset canvas to white
				for j := range img.Pix {
					img.Pix[j] = 255
				}
				renderer.renderCircleScanline(img, tc.c)
			}
		})

		b.Run(tc.name+"/Hybrid", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Reset canvas to white
				for j := range img.Pix {
					img.Pix[j] = 255
				}
				renderer.renderCircleHybrid(img, tc.c)
			}
		})
	}
}

// Helper function to create random NRGBA image
func randomNRGBA(width, height int, seed int64) *image.NRGBA {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for i := 0; i < len(img.Pix); i++ {
		img.Pix[i] = uint8(rng.Intn(256))
	}

	return img
}

// Helper function to create random circle parameters
func randomParams(k, width, height int) []float64 {
	const paramsPerCircle = 7 // X, Y, R, CR, CG, CB, Opacity
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	params := make([]float64, k*paramsPerCircle)
	for i := 0; i < k; i++ {
		offset := i * paramsPerCircle
		params[offset+0] = r.Float64() * float64(width)
		params[offset+1] = r.Float64() * float64(height)
		params[offset+2] = 5 + r.Float64()*float64(width/4)
		params[offset+3] = r.Float64()
		params[offset+4] = r.Float64()
		params[offset+5] = r.Float64()
		params[offset+6] = 0.5 + 0.5*r.Float64()
	}
	return params
}

func deterministicParams(k, width, height int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	params := make([]float64, k*7)
	for i := 0; i < k; i++ {
		offset := i * 7
		params[offset+0] = r.Float64() * float64(width)
		params[offset+1] = r.Float64() * float64(height)
		params[offset+2] = 1 + r.Float64()*float64(max(width, height))/3
		params[offset+3] = r.Float64()
		params[offset+4] = r.Float64()
		params[offset+5] = r.Float64()
		params[offset+6] = r.Float64()
	}
	return params
}
