package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"runtime"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestCPURendererWhiteCanvas(t *testing.T) {
	// Create a white 10x10 reference
	ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	white := color.NRGBA{255, 255, 255, 255}

	for y := range 10 {
		for x := range 10 {
			ref.Set(x, y, white)
		}
	}

	renderer := NewCPURenderer(ref, 0) // 0 circles

	// Empty params should render white canvas
	result := renderer.Render([]float64{})

	// Check if result is all white
	for y := range 10 {
		for x := range 10 {
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

	for y := range 20 {
		for x := range 20 {
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

	for i := range 50 {
		if got := renderer.Render(params); !bytes.Equal(got.Pix, want) {
			t.Fatalf("render %d differs from the first parallel render", i+1)
		}
	}
}

func TestCPURendererStagedSessionsShareOnlyImmutableBackground(t *testing.T) {
	reference := randomNRGBA(32, 24, 15_901)
	retained := randomNRGBA(32, 24, 15_902)
	base := NewCPURenderer(reference, 2)

	firstRenderer, firstCleanup, err := base.newSessionWithCanvas(retained, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer firstCleanup()

	secondRenderer, secondCleanup, err := base.newSessionWithCanvas(retained, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer secondCleanup()

	first := firstRenderer.(*CPURenderer)
	second := secondRenderer.(*CPURenderer)

	if &first.initialBg[0] != &retained.Pix[0] || &second.initialBg[0] != &retained.Pix[0] {
		t.Fatal("staged sessions copied the immutable retained background")
	}

	if &first.canvas.Pix[0] == &second.canvas.Pix[0] || &first.canvas.Pix[0] == &retained.Pix[0] {
		t.Fatal("staged sessions share a mutable render canvas")
	}

	retainedBefore := append([]byte(nil), retained.Pix...)
	params := []float64{16, 12, 6, 1, 0, 0, 0.5}
	first.Render(params)

	if !bytes.Equal(retained.Pix, retainedBefore) {
		t.Fatal("rendering a staged session mutated the retained background")
	}

	if got := second.Render(params); bytes.Equal(got.Pix, retained.Pix) {
		t.Fatal("second staged session did not render independently over the shared background")
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

// TestScanlineCircleRenderingMatchesOriginal verifies scanline method produces identical results.
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
					for c := range 4 {
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

// BenchmarkCPURenderer_Render benchmarks pure circle rendering without cost computation.
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

			params := benchmarkParams(sz.circles, sz.width, sz.height, 20260816)

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

// BenchmarkCPURenderer_Cost benchmarks full pipeline (rendering + cost).
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
			params := benchmarkParams(sz.circles, sz.width, sz.height, 20260816)

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = renderer.Cost(params)
			}
		})
	}
}

// BenchmarkCompositePixel benchmarks the alpha compositing operation.
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

func BenchmarkCompositePixelOpaque(b *testing.B) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = 200
		img.Pix[i+1] = 200
		img.Pix[i+2] = 200
		img.Pix[i+3] = 255
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		compositePixel(img, 128, 128, 1.0, 0.0, 0.0, 0.5)
	}
}

func TestCompositePixelOpaqueMatchesGeneralPath(t *testing.T) {
	tests := []struct {
		name       string
		background color.NRGBA
		red        float64
		green      float64
		blue       float64
		alpha      float64
	}{
		{name: "transparent_source", background: color.NRGBA{R: 12, G: 34, B: 56, A: 255}},
		{name: "fractional", background: color.NRGBA{R: 12, G: 127, B: 241, A: 255}, red: 0.13, green: 0.57, blue: 0.91, alpha: 0.37},
		{name: "half", background: color.NRGBA{R: 200, G: 100, B: 50, A: 255}, red: 1, green: 0.5, blue: 0, alpha: 0.5},
		{name: "opaque_source", background: color.NRGBA{R: 1, G: 2, B: 3, A: 255}, red: 0.99, green: 0.01, blue: 0.49, alpha: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			want := image.NewNRGBA(image.Rect(0, 0, 1, 1))

			got.SetNRGBA(0, 0, test.background)
			want.SetNRGBA(0, 0, test.background)

			compositePixel(got, 0, 0, test.red, test.green, test.blue, test.alpha)
			compositePixelGeneral(want, 0, 0, test.red, test.green, test.blue, test.alpha)

			if !bytes.Equal(got.Pix, want.Pix) {
				t.Fatalf("opaque path = %v, general path = %v", got.Pix, want.Pix)
			}
		})
	}
}

func compositePixelGeneral(img *image.NRGBA, x, y int, r, g, b, alpha float64) {
	i := y*img.Stride + x*4
	bgR := float64(img.Pix[i+0]) * inv255
	bgG := float64(img.Pix[i+1]) * inv255
	bgB := float64(img.Pix[i+2]) * inv255
	bgA := float64(img.Pix[i+3]) * inv255
	fgR := r * alpha
	fgG := g * alpha
	fgB := b * alpha

	outA := alpha + bgA*(1-alpha)
	if outA == 0 {
		return
	}

	bgBlend := bgA * (1 - alpha)
	invOutA := 1 / outA
	img.Pix[i+0] = uint8((fgR+bgR*bgBlend)*invOutA*255 + 0.5)
	img.Pix[i+1] = uint8((fgG+bgG*bgBlend)*invOutA*255 + 0.5)
	img.Pix[i+2] = uint8((fgB+bgB*bgBlend)*invOutA*255 + 0.5)
	img.Pix[i+3] = uint8(outA*255 + 0.5)
}

// BenchmarkRenderCircle compares the three circle rasterizers.
//
// Two properties of the earlier version made it useless for comparing builds,
// and both are corrected here.
//
// It built CPURenderer as a struct literal, which leaves opaqueCanvas false, so
// every case measured the per-pixel Porter-Duff path and none of them ever
// reached the span compositor that production uses on an opaque canvas. The
// canvas kind is now an explicit axis, so both paths are measured and neither
// can be selected by accident.
//
// And it refilled the whole 256x256 canvas inside the timed loop, which cost
// more than the circle did: every radius from 5 to 50 landed within a few
// percent of the same number, and comparing two binaries mostly compared the
// code layout of that fill loop. The canvas is now filled once, before the
// timer starts. Compositing the same circle repeatedly drives the covered
// pixels toward the circle color, but none of these paths branch on pixel
// values, so every iteration performs the identical arithmetic.
func BenchmarkRenderCircle(b *testing.B) {
	const width, height = 256, 256

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

	strategies := []struct {
		name   string
		render func(*CPURenderer, *image.NRGBA, fit.Circle)
	}{
		{"Original", (*CPURenderer).renderCircle},
		{"Scanline", (*CPURenderer).renderCircleScanline},
		{"Hybrid", (*CPURenderer).renderCircleHybrid},
	}

	reference := randomNRGBA(width, height, 42)

	for _, canvas := range []struct {
		name   string
		opaque bool
	}{
		// The canvas is white either way; the flag decides which compositor the
		// renderer is allowed to use for it.
		{"opaque_span", true},
		{"pixel_loop", false},
	} {
		for _, tc := range circles {
			for _, strategy := range strategies {
				b.Run(canvas.name+"/"+tc.name+"/"+strategy.name, func(b *testing.B) {
					renderer := NewCPURenderer(reference, 1)
					renderer.SetThreads(1)
					renderer.opaqueCanvas = canvas.opaque

					img := image.NewNRGBA(image.Rect(0, 0, width, height))
					for j := range img.Pix {
						img.Pix[j] = 255
					}

					b.ReportAllocs()
					b.ResetTimer()

					for i := 0; i < b.N; i++ {
						strategy.render(renderer, img, tc.c)
					}
				})
			}
		}
	}
}

// Helper function to create random NRGBA image.
func randomNRGBA(width, height int, seed int64) *image.NRGBA {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for i := 0; i < len(img.Pix); i++ {
		img.Pix[i] = uint8(rng.Intn(256))
	}

	return img
}

// benchmarkParams builds a circle vector for a benchmark workload.
//
// The seed is a parameter, and every caller passes a literal, because this used
// to seed from time.Now().UnixNano(). Two runs of the same benchmark therefore
// rendered different geometry, which put a 10-40% spread on results that are
// routinely compared across builds - large enough to hide the effect being
// measured. Change a seed only together with the numbers recorded against it.
func benchmarkParams(k, width, height int, seed int64) []float64 {
	const paramsPerCircle = 7 // X, Y, R, CR, CG, CB, Opacity
	r := rand.New(rand.NewSource(seed))

	params := make([]float64, k*paramsPerCircle)
	for i := range k {
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
	for i := range k {
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
