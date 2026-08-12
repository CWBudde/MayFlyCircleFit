package fit

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"
	"os"
	"sync"
	"testing"

	"golang.org/x/sys/cpu"
)

var ssdBenchmarkSink float64

// ---------------------- Test Utilities ----------------------

// randomNRGBA creates an NRGBA image with random pixel values
func randomNRGBA(width, height int, seed int64) *image.NRGBA {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for i := 0; i < len(img.Pix); i++ {
		img.Pix[i] = uint8(rng.Intn(256))
	}

	return img
}

// solidColorNRGBA creates an NRGBA image with a solid color
func solidColorNRGBA(width, height int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, c)
		}
	}

	return img
}

// ---------------------- Correctness Tests ----------------------

// TestFastSSD_IdenticalImages tests that SSD of identical images is zero
func TestFastSSD_IdenticalImages(t *testing.T) {
	sizes := []struct {
		width, height int
	}{
		{1, 1},     // Single pixel
		{8, 8},     // Small (AVX2 batch size)
		{64, 64},   // Medium
		{256, 256}, // Large
		{17, 23},   // Non-power-of-2
		{255, 255}, // Just under 256
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.width, sz.height), func(t *testing.T) {
			img := randomNRGBA(sz.width, sz.height, 42)

			// SSD of image with itself should be zero
			ssd := FastSSD(img, img)

			if ssd != 0.0 {
				t.Errorf("SSD of identical images should be 0.0, got %f", ssd)
			}
		})
	}
}

// TestFastSSD_KnownDifference tests SSD with known pixel differences
func TestFastSSD_KnownDifference(t *testing.T) {
	// Create two 2x2 images with known differences
	img1 := solidColorNRGBA(2, 2, color.NRGBA{R: 100, G: 150, B: 200, A: 255})
	img2 := solidColorNRGBA(2, 2, color.NRGBA{R: 110, G: 140, B: 210, A: 255})

	// Expected differences per pixel:
	// dr = 110 - 100 = 10  -> dr^2 = 100
	// dg = 140 - 150 = -10 -> dg^2 = 100
	// db = 210 - 200 = 10  -> db^2 = 100
	// sum per pixel = 300
	// total sum = 300 * 4 pixels = 1200
	// MSE = 1200 / (4 pixels * 3 channels) = 100.0

	expectedMSE := 100.0

	mse := FastSSD(img1, img2)

	if math.Abs(mse-expectedMSE) > 1e-9 {
		t.Errorf("Expected MSE = %f, got %f", expectedMSE, mse)
	}
}

// TestFastSSD_MaxDifference tests SSD with maximum possible differences
func TestFastSSD_MaxDifference(t *testing.T) {
	// White vs black: maximum possible difference
	white := solidColorNRGBA(10, 10, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	black := solidColorNRGBA(10, 10, color.NRGBA{R: 0, G: 0, B: 0, A: 255})

	// Expected:
	// dr = 255, dg = 255, db = 255
	// sum per pixel = 255^2 * 3 = 195,075
	// MSE = 195,075 / 3 = 65,025

	expectedMSE := 65025.0

	mse := FastSSD(white, black)

	if math.Abs(mse-expectedMSE) > 1e-9 {
		t.Errorf("Expected MSE = %f, got %f", expectedMSE, mse)
	}
}

// TestFastSSD_AlphaIgnored tests that alpha channel is ignored in SSD computation
func TestFastSSD_AlphaIgnored(t *testing.T) {
	// Create two images with same RGB but different alpha
	img1 := solidColorNRGBA(10, 10, color.NRGBA{R: 100, G: 150, B: 200, A: 255})
	img2 := solidColorNRGBA(10, 10, color.NRGBA{R: 100, G: 150, B: 200, A: 0})

	// SSD should be zero (alpha is ignored)
	ssd := FastSSD(img1, img2)

	if ssd != 0.0 {
		t.Errorf("SSD should ignore alpha channel, expected 0.0, got %f", ssd)
	}
}

// ---------------------- Equivalence Tests (SIMD vs Scalar) ----------------------

// TestFastSSD_ScalarEquivalence tests that active backend matches scalar reference
func TestFastSSD_ScalarEquivalence(t *testing.T) {
	if ActiveSSDBackend == SSDBackendScalar {
		t.Skip("Skipping equivalence test: active backend is scalar")
	}

	sizes := []struct {
		width, height int
	}{
		{8, 8},     // Exactly one AVX2 batch
		{64, 64},   // Multiple batches
		{256, 256}, // Large image
		{17, 23},   // Non-aligned dimensions
		{100, 100}, // Moderate size
		{7, 11},    // Smaller than AVX2 batch (tests remainder handling)
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.width, sz.height), func(t *testing.T) {
			img1 := randomNRGBA(sz.width, sz.height, 12345)
			img2 := randomNRGBA(sz.width, sz.height, 67890)

			// Compute with active backend (AVX2 or NEON)
			simdResult := FastSSD(img1, img2)

			// Compute with scalar reference
			scalarResult := fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height) / float64(sz.width*sz.height*3)

			if simdResult != scalarResult {
				t.Errorf("SIMD result differs from scalar: SIMD=%f, scalar=%f",
					simdResult, scalarResult)
				t.Logf("Active backend: %s", ActiveSSDBackend)
			}
		})
	}
}

// TestFastSSD_CompareImplementations uses the built-in comparison utility
func TestFastSSD_CompareImplementations(t *testing.T) {
	if ActiveSSDBackend == SSDBackendScalar {
		t.Skip("Skipping comparison test: active backend is scalar")
	}

	testCases := []struct {
		name          string
		width, height int
		seed1, seed2  int64
	}{
		{"small_random", 16, 16, 111, 222},
		{"medium_random", 128, 128, 333, 444},
		{"large_random", 512, 512, 555, 666},
		{"non_square", 100, 200, 777, 888},
		{"thin_horizontal", 256, 8, 999, 1000},
		{"thin_vertical", 8, 256, 1001, 1002},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			img1 := randomNRGBA(tc.width, tc.height, tc.seed1)
			img2 := randomNRGBA(tc.width, tc.height, tc.seed2)

			// Use built-in comparison utility (allows floating-point tolerance)
			tolerance := 1e-9
			if !CompareSSDImplementations(img1, img2, tolerance) {
				t.Errorf("Implementation comparison failed for %s (%dx%d)",
					tc.name, tc.width, tc.height)
				t.Logf("Active backend: %s", ActiveSSDBackend)
			}
		})
	}
}

// ---------------------- Edge Case Tests ----------------------

// TestFastSSD_SinglePixel tests SSD with 1x1 images
func TestFastSSD_SinglePixel(t *testing.T) {
	img1 := solidColorNRGBA(1, 1, color.NRGBA{R: 50, G: 100, B: 150, A: 255})
	img2 := solidColorNRGBA(1, 1, color.NRGBA{R: 60, G: 90, B: 160, A: 255})

	// dr = 10, dg = -10, db = 10
	// sum = 100 + 100 + 100 = 300
	// MSE = 300 / 3 = 100

	expectedMSE := 100.0
	mse := FastSSD(img1, img2)

	if math.Abs(mse-expectedMSE) > 1e-9 {
		t.Errorf("Single pixel: expected MSE = %f, got %f", expectedMSE, mse)
	}
}

// TestFastSSD_ThinImages tests edge case with very thin images
func TestFastSSD_ThinImages(t *testing.T) {
	testCases := []struct {
		name          string
		width, height int
	}{
		{"1x100", 1, 100},
		{"100x1", 100, 1},
		{"2x1000", 2, 1000},
		{"1000x2", 1000, 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			img1 := randomNRGBA(tc.width, tc.height, 123)
			img2 := randomNRGBA(tc.width, tc.height, 456)

			// Should not panic or produce NaN
			mse := FastSSD(img1, img2)

			if math.IsNaN(mse) || math.IsInf(mse, 0) {
				t.Errorf("Thin image produced invalid MSE: %f", mse)
			}

			if mse < 0 {
				t.Errorf("MSE should be non-negative, got %f", mse)
			}
		})
	}
}

// TestFastSSD_DimensionMismatch tests safe rejection of mismatched dimensions.
func TestFastSSD_DimensionMismatch(t *testing.T) {
	img1 := randomNRGBA(64, 64, 111)
	img2 := randomNRGBA(128, 128, 222) // Different size
	if got := FastSSD(img1, img2); !math.IsInf(got, 1) {
		t.Fatalf("FastSSD mismatch = %v, want +Inf", got)
	}
}

// ---------------------- SIMD-Specific Tests ----------------------

// TestFastSSD_AVX2_BatchBoundaries tests AVX2 batch processing with various widths
// AVX2 processes 8 pixels per batch, so we test exact multiples and remainders
func TestFastSSD_AVX2_BatchBoundaries(t *testing.T) {
	if ActiveSSDBackend != SSDBackendAVX2 {
		t.Skipf("Skipping AVX2 batch boundary test: active backend is %s, not AVX2", ActiveSSDBackend)
	}

	// Test widths that are multiples of 8 (exact batches) and non-multiples (with remainders)
	widths := []int{7, 8, 9, 15, 16, 17, 23, 24, 25, 31, 32, 33, 63, 64, 65}
	height := 10

	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			img1 := randomNRGBA(width, height, 100)
			img2 := randomNRGBA(width, height, 200)

			// Compute with AVX2 backend
			avx2Result := fastSSD(img1.Pix, img2.Pix, img1.Stride, width, height)

			// Compute with scalar reference
			scalarResult := fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, width, height)

			if avx2Result != scalarResult {
				t.Errorf("AVX2 batch boundary error: width=%d, avx2=%f, scalar=%f",
					width, avx2Result, scalarResult)
			}
		})
	}
}

// TestFastSSD_AVX2ExactLargeSum guards against overflowing 32-bit SIMD lanes.
func TestFastSSD_AVX2ExactLargeSum(t *testing.T) {
	if ActiveSSDBackend != SSDBackendAVX2 {
		t.Skipf("Skipping AVX2 accumulator test: active backend is %s, not AVX2", ActiveSSDBackend)
	}

	const width, height = 512, 512
	black := solidColorNRGBA(width, height, color.NRGBA{A: 0})
	white := solidColorNRGBA(width, height, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	got := fastSSD(black.Pix, white.Pix, black.Stride, width, height)
	want := float64(width) * float64(height) * 3 * 255 * 255
	if got != want {
		t.Fatalf("AVX2 large SSD = %.0f, want %.0f", got, want)
	}
	if scalar := fastSSD_Scalar(black.Pix, white.Pix, black.Stride, width, height); got != scalar {
		t.Fatalf("AVX2 large SSD = %.0f, scalar = %.0f", got, scalar)
	}
}

// TestFastSSD_NEON_BatchBoundaries tests NEON batch processing with various widths
// NEON processes 4 pixels per batch (128-bit registers), so we test multiples of 4
func TestFastSSD_NEON_BatchBoundaries(t *testing.T) {
	if ActiveSSDBackend != SSDBackendNEON {
		t.Skipf("Skipping NEON batch boundary test: active backend is %s, not NEON", ActiveSSDBackend)
	}

	// Test widths that are multiples of 4 (exact batches) and non-multiples (with remainders)
	widths := []int{3, 4, 5, 7, 8, 9, 11, 12, 13, 15, 16, 17}
	height := 10

	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			img1 := randomNRGBA(width, height, 100)
			img2 := randomNRGBA(width, height, 200)

			// Compute with NEON backend
			neonResult := fastSSD(img1.Pix, img2.Pix, img1.Stride, width, height)

			// Compute with scalar reference
			scalarResult := fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, width, height)

			if neonResult != scalarResult {
				t.Errorf("NEON batch boundary error: width=%d, neon=%f, scalar=%f",
					width, neonResult, scalarResult)
			}
		})
	}
}

// TestFastSSD_NEONExactLargeSum guards against overflowing SIMD accumulator lanes.
func TestFastSSD_NEONExactLargeSum(t *testing.T) {
	if ActiveSSDBackend != SSDBackendNEON {
		t.Skipf("Skipping NEON accumulator test: active backend is %s, not NEON", ActiveSSDBackend)
	}

	const width, height = 512, 512
	black := solidColorNRGBA(width, height, color.NRGBA{A: 0})
	white := solidColorNRGBA(width, height, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	got := fastSSD(black.Pix, white.Pix, black.Stride, width, height)
	want := float64(width) * float64(height) * 3 * 255 * 255
	if got != want {
		t.Fatalf("NEON large SSD = %.0f, want %.0f", got, want)
	}
	if scalar := fastSSD_Scalar(black.Pix, white.Pix, black.Stride, width, height); got != scalar {
		t.Fatalf("NEON large SSD = %.0f, scalar = %.0f", got, scalar)
	}
}

// ---------------------- Concurrency Tests ----------------------

// TestFastSSD_ConcurrentAccess tests thread-safety of SSD computation
func TestFastSSD_ConcurrentAccess(t *testing.T) {
	img1 := randomNRGBA(256, 256, 111)
	img2 := randomNRGBA(256, 256, 222)

	// Run SSD from multiple goroutines simultaneously
	const goroutines = 10
	const iterations = 100

	results := make([][]float64, goroutines)
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = make([]float64, iterations)
			for i := 0; i < iterations; i++ {
				results[idx][i] = FastSSD(img1, img2)
			}
		}(g)
	}

	wg.Wait()

	// All results should be identical (no race conditions or shared state issues)
	expected := results[0][0]
	for g := 0; g < goroutines; g++ {
		for i := 0; i < iterations; i++ {
			if results[g][i] != expected {
				t.Errorf("Concurrent call mismatch: goroutine %d, iter %d: got %f, want %f",
					g, i, results[g][i], expected)
			}
		}
	}

	t.Logf("Concurrent access test passed: %d goroutines × %d iterations, all results identical", goroutines, iterations)
}

// ---------------------- Large Image Tests ----------------------

// TestFastSSD_LargeImages stress tests with very large images
func TestFastSSD_LargeImages(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large image test in short mode")
	}

	sizes := []struct {
		width, height int
	}{
		{1024, 1024}, // 1M pixels
		{2048, 2048}, // 4M pixels
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.width, sz.height), func(t *testing.T) {
			img1 := randomNRGBA(sz.width, sz.height, 111)
			img2 := randomNRGBA(sz.width, sz.height, 222)

			// Should not panic or crash
			simdResult := fastSSD(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			scalarResult := fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)

			// Check for invalid results
			if math.IsNaN(simdResult) || math.IsInf(simdResult, 0) {
				t.Errorf("Large image produced invalid SIMD result: %f", simdResult)
			}

			if math.IsNaN(scalarResult) || math.IsInf(scalarResult, 0) {
				t.Errorf("Large image produced invalid scalar result: %f", scalarResult)
			}

			// Compare SIMD vs scalar
			relDiff := math.Abs(simdResult-scalarResult) / scalarResult
			if relDiff > 1e-6 {
				t.Errorf("Large image mismatch: simd=%f, scalar=%f, relDiff=%e",
					simdResult, scalarResult, relDiff)
			}

			t.Logf("%dx%d: simd=%f, scalar=%f, relDiff=%e", sz.width, sz.height, simdResult, scalarResult, relDiff)
		})
	}
}

// ---------------------- Padded Stride Tests ----------------------

// TestFastSSD_PaddedStride tests handling of non-standard stride (padded images)
func TestFastSSD_PaddedStride(t *testing.T) {
	width, height := 63, 32 // Non-multiple of 8 (tests remainder handling)

	// Create image with padded stride (align to 64 bytes)
	stride := ((width*4 + 63) / 64) * 64
	pix1 := make([]uint8, stride*height)
	pix2 := make([]uint8, stride*height)

	img1 := &image.NRGBA{Pix: pix1, Stride: stride, Rect: image.Rect(0, 0, width, height)}
	img2 := &image.NRGBA{Pix: pix2, Stride: stride, Rect: image.Rect(0, 0, width, height)}

	// Fill with test pattern
	rng := rand.New(rand.NewSource(777))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := y*stride + x*4
			pix1[i+0] = uint8(rng.Intn(256))
			pix1[i+1] = uint8(rng.Intn(256))
			pix1[i+2] = uint8(rng.Intn(256))
			pix1[i+3] = 255

			pix2[i+0] = uint8(rng.Intn(256))
			pix2[i+1] = uint8(rng.Intn(256))
			pix2[i+2] = uint8(rng.Intn(256))
			pix2[i+3] = 255
		}
	}

	// Should handle padded stride correctly
	result := FastSSD(img1, img2)

	want := fastSSD_Scalar(img1.Pix, img2.Pix, stride, width, height) / float64(width*height*3)
	if result != want {
		t.Errorf("Padded stride result = %f, scalar = %f", result, want)
	}

	t.Logf("Padded stride test passed: width=%d, stride=%d, result=%f", width, stride, result)
}

// ---------------------- Backend Selection Tests ----------------------

// TestFastSSD_RequiredBackend lets native hardware CI require that runtime
// feature detection selected the backend expected for its runner.
func TestFastSSD_RequiredBackend(t *testing.T) {
	required := os.Getenv("MAYFLY_REQUIRE_SSD_BACKEND")
	if required == "" {
		t.Skip("MAYFLY_REQUIRE_SSD_BACKEND is not set")
	}
	if got := ActiveSSDBackend.String(); got != required {
		t.Fatalf("active SSD backend = %s, required %s", got, required)
	}
}

// TestFastSSD_BackendSelection validates that the correct backend was selected based on CPU features
func TestFastSSD_BackendSelection(t *testing.T) {
	t.Logf("Active SSD backend: %s", ActiveSSDBackend)

	// Verify backend is consistent with CPU features
	if cpu.X86.HasAVX2 {
		if ActiveSSDBackend != SSDBackendAVX2 {
			t.Logf("Note: AVX2 available but backend is %s (may be disabled via GODEBUG)", ActiveSSDBackend)
		} else {
			t.Logf("AVX2 backend correctly selected")
		}
	} else {
		if ActiveSSDBackend == SSDBackendAVX2 {
			t.Errorf("AVX2 backend selected but CPU doesn't support AVX2")
		}
	}

	// ARM64 NEON check
	if cpu.ARM64.HasASIMD {
		if ActiveSSDBackend != SSDBackendNEON {
			t.Logf("Note: NEON available but backend is %s", ActiveSSDBackend)
		} else {
			t.Logf("NEON backend correctly selected")
		}
	} else {
		if ActiveSSDBackend == SSDBackendNEON {
			t.Errorf("NEON backend selected but CPU doesn't support NEON")
		}
	}

	// Verify fastSSD function pointer is set
	if fastSSD == nil {
		t.Error("fastSSD function pointer is nil")
	}

	// Smoke test
	img := randomNRGBA(16, 16, 42)
	result := FastSSD(img, img)

	if result != 0.0 {
		t.Errorf("SSD of identical images should be 0.0, got %f", result)
	}

	t.Logf("Backend selection validated: %s", ActiveSSDBackend)
}

// ---------------------- Benchmark Tests ----------------------

// BenchmarkFastSSD_Scalar benchmarks scalar implementation
func BenchmarkFastSSD_Scalar(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	// Report throughput
	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkFastSSD_Active benchmarks currently active backend (AVX2/NEON/scalar)
func BenchmarkFastSSD_Active(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.Logf("Active backend: %s", ActiveSSDBackend)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fastSSD(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkFastSSD_HighLevel benchmarks high-level FastSSD wrapper
func BenchmarkFastSSD_HighLevel(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FastSSD(img1, img2)
	}

	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkFastSSD_Comparison benchmarks scalar vs active backend side-by-side
func BenchmarkFastSSD_Comparison(b *testing.B) {
	sizes := []struct {
		name          string
		width, height int
	}{
		{"64x64", 64, 64},
		{"128x128", 128, 128},
		{"256x256", 256, 256},
		{"512x512", 512, 512},
		{"1024x1024", 1024, 1024},
	}

	for _, sz := range sizes {
		img1 := randomNRGBA(sz.width, sz.height, 1)
		img2 := randomNRGBA(sz.width, sz.height, 2)

		b.Run(sz.name+"_scalar", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ssdBenchmarkSink = fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			}
			mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
			b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
		})

		b.Run(sz.name+"_active", func(b *testing.B) {
			b.Logf("Active backend: %s", ActiveSSDBackend)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ssdBenchmarkSink = fastSSD(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			}
			mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
			b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
		})
	}
}

// ---------------------- Regression Tests ----------------------

// TestFastMSECost_EquivalentToMSECost tests that FastMSECost matches MSECost
func TestFastMSECost_EquivalentToMSECost(t *testing.T) {
	sizes := []struct {
		width, height int
	}{
		{8, 8},
		{64, 64},
		{128, 128},
		{256, 256},
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.width, sz.height), func(t *testing.T) {
			img1 := randomNRGBA(sz.width, sz.height, 9999)
			img2 := randomNRGBA(sz.width, sz.height, 8888)

			// Compute with original MSECost
			originalMSE := MSECost(img1, img2)

			// Compute with new FastMSECost
			fastMSE := FastMSECost(img1, img2)

			// Allow small tolerance for floating-point rounding
			tolerance := 1e-9
			diff := math.Abs(originalMSE - fastMSE)

			if diff > tolerance {
				t.Errorf("FastMSECost differs from MSECost: original=%f, fast=%f, diff=%e",
					originalMSE, fastMSE, diff)
			}
		})
	}
}

// ---------------------- Validation Test ----------------------

// TestSSDBackendDetection validates that the correct backend was selected
func TestSSDBackendDetection(t *testing.T) {
	t.Logf("Active SSD backend: %s", ActiveSSDBackend)

	// Validate that backend selection is consistent
	if fastSSD == nil {
		t.Error("fastSSD function pointer is nil")
	}

	// Smoke test: ensure backend doesn't crash
	img := randomNRGBA(16, 16, 42)
	result := FastSSD(img, img)

	if result != 0.0 {
		t.Errorf("SSD of identical images should be 0.0, got %f", result)
	}

	t.Logf("Backend smoke test passed: FastSSD(img, img) = %f", result)
}
