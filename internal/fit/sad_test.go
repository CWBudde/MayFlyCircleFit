package fit

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/cpu"
)

// TestSAD_IdenticalImages verifies SAD returns 0 for identical images
func TestSAD_IdenticalImages(t *testing.T) {
	sizes := []struct {
		width, height int
	}{
		{1, 1},
		{8, 8},
		{16, 16},
		{64, 64},
		{256, 256},
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.width, sz.height), func(t *testing.T) {
			img := randomNRGBA(sz.width, sz.height, 42)
			cost := FastSAD(img, img)

			if cost != 0.0 {
				t.Errorf("SAD of identical images should be 0.0, got %f", cost)
			}
		})
	}
}

// TestSAD_ScalarEquivalence verifies AVX2 matches scalar implementation
func TestSAD_ScalarEquivalence(t *testing.T) {
	sizes := []struct {
		width, height int
	}{
		{8, 8},
		{64, 64},
		{256, 256},
		{17, 23}, // Non-power-of-2
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.width, sz.height), func(t *testing.T) {
			img1 := randomNRGBA(sz.width, sz.height, 100)
			img2 := randomNRGBA(sz.width, sz.height, 200)

			// Compute with scalar
			scalarCost := fastSAD_Scalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)

			// Compute with active backend (AVX2 or scalar)
			activeCost := fastSAD(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)

			// They should match exactly (no floating point tolerance needed)
			if math.Abs(scalarCost-activeCost) > 1e-9 {
				t.Errorf("SAD mismatch: scalar=%f, active=%f, backend=%s",
					scalarCost, activeCost, ActiveSADBackend)
			}
		})
	}
}

// TestSAD_KnownValues tests SAD with manually computed expected values
func TestSAD_KnownValues(t *testing.T) {
	// Create two 2x2 images with known pixel values
	img1 := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img2 := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	// Set pixel values:
	// img1: all pixels (100, 150, 200, 255)
	// img2: all pixels (110, 160, 210, 255)
	// SAD per pixel = |100-110| + |150-160| + |200-210| = 10 + 10 + 10 = 30
	c1 := color.NRGBA{R: 100, G: 150, B: 200, A: 255}
	c2 := color.NRGBA{R: 110, G: 160, B: 210, A: 255}

	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img1.SetNRGBA(x, y, c1)
			img2.SetNRGBA(x, y, c2)
		}
	}

	// Compute SAD
	cost := FastSAD(img1, img2)

	// Expected: 4 pixels × (30 × (255 + 9×30)) × CScale
	// = 4 × (30 × 525) × CScale
	// = 4 × 15750 × 1.5378700499807766243752402921953E-6
	// = 63000 × 1.5378700499807766243752402921953E-6
	// = 0.09688621314778853...
	expected := 4.0 * (30.0 * (255.0 + 9.0*30.0)) * sadScale

	if math.Abs(cost-expected) > 1e-10 {
		t.Errorf("SAD mismatch: expected=%f, got=%f", expected, cost)
	}
}

// TestSAD_AlphaIgnored verifies alpha channel is ignored
func TestSAD_AlphaIgnored(t *testing.T) {
	img1 := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img2 := image.NewNRGBA(image.Rect(0, 0, 4, 4))

	// Fill with same RGB but different alpha
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img1.SetNRGBA(x, y, color.NRGBA{R: 100, G: 150, B: 200, A: 255})
			img2.SetNRGBA(x, y, color.NRGBA{R: 100, G: 150, B: 200, A: 0}) // Different alpha
		}
	}

	cost := FastSAD(img1, img2)

	// Should be zero since RGB channels match
	if cost != 0.0 {
		t.Errorf("SAD should ignore alpha, expected 0.0, got %f", cost)
	}
}

// ---------------------- SIMD-Specific Tests ----------------------

// TestSAD_AVX2_BatchBoundaries tests AVX2 batch processing with various widths
// AVX2 processes 8 pixels per batch, so we test exact multiples and remainders
func TestSAD_AVX2_BatchBoundaries(t *testing.T) {
	if ActiveSADBackend != SADBackendAVX2 {
		t.Skipf("Skipping AVX2 batch boundary test: active backend is %s, not AVX2", ActiveSADBackend)
	}

	// Test widths that are multiples of 8 (exact batches) and non-multiples (with remainders)
	widths := []int{7, 8, 9, 15, 16, 17, 23, 24, 25, 31, 32, 33, 63, 64, 65}
	height := 10

	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			img1 := randomNRGBA(width, height, 300)
			img2 := randomNRGBA(width, height, 400)

			// Compute with AVX2 backend
			avx2Result := fastSAD(img1.Pix, img2.Pix, img1.Stride, width, height)

			// Compute with scalar reference
			scalarResult := fastSAD_Scalar(img1.Pix, img2.Pix, img1.Stride, width, height)

			// Results should match exactly (or within floating-point tolerance)
			diff := math.Abs(avx2Result - scalarResult)
			tolerance := 1e-9

			if diff > tolerance {
				t.Errorf("AVX2 batch boundary error: width=%d, avx2=%f, scalar=%f, diff=%e",
					width, avx2Result, scalarResult, diff)
			}
		})
	}
}

// TestSAD_NEON_BatchBoundaries tests NEON batch processing with various widths
// NEON processes 4 pixels per batch (128-bit registers), so we test multiples of 4
func TestSAD_NEON_BatchBoundaries(t *testing.T) {
	if ActiveSADBackend != SADBackendNEON {
		t.Skipf("Skipping NEON batch boundary test: active backend is %s, not NEON", ActiveSADBackend)
	}

	// Test widths that are multiples of 4 (exact batches) and non-multiples (with remainders)
	widths := []int{3, 4, 5, 7, 8, 9, 11, 12, 13, 15, 16, 17}
	height := 10

	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			img1 := randomNRGBA(width, height, 300)
			img2 := randomNRGBA(width, height, 400)

			// Compute with NEON backend
			neonResult := fastSAD(img1.Pix, img2.Pix, img1.Stride, width, height)

			// Compute with scalar reference
			scalarResult := fastSAD_Scalar(img1.Pix, img2.Pix, img1.Stride, width, height)

			// Results should match exactly (or within floating-point tolerance)
			diff := math.Abs(neonResult - scalarResult)
			tolerance := 1e-9

			if diff > tolerance {
				t.Errorf("NEON batch boundary error: width=%d, neon=%f, scalar=%f, diff=%e",
					width, neonResult, scalarResult, diff)
			}
		})
	}
}

// ---------------------- Concurrency Tests ----------------------

// TestSAD_ConcurrentAccess tests thread-safety of SAD computation
func TestSAD_ConcurrentAccess(t *testing.T) {
	img1 := randomNRGBA(256, 256, 333)
	img2 := randomNRGBA(256, 256, 444)

	// Run SAD from multiple goroutines simultaneously
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
				results[idx][i] = FastSAD(img1, img2)
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

// ---------------------- Performance Regression Tests ----------------------

// TestSAD_PerformanceBaseline detects performance regressions by checking throughput
func TestSAD_PerformanceBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	img1 := randomNRGBA(256, 256, 300)
	img2 := randomNRGBA(256, 256, 400)

	// Warmup
	for i := 0; i < 100; i++ {
		fastSAD(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	// Measure throughput
	start := time.Now()
	iterations := 1000
	for i := 0; i < iterations; i++ {
		fastSAD(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}
	elapsed := time.Since(start)

	mpixelsPerSec := float64(iterations*256*256) / 1e6 / elapsed.Seconds()

	// Expected baseline (adjust based on backend)
	// SAD has quadratic weighting so it's slower than SSD
	var expectedMin float64
	var backendName string

	switch ActiveSADBackend {
	case SADBackendAVX2:
		expectedMin = 1000 // 1 Gpixels/sec minimum (lower than SSD due to quadratic formula)
		backendName = "AVX2"
	case SADBackendNEON:
		expectedMin = 800 // 800 Mpixels/sec minimum
		backendName = "NEON"
	case SADBackendScalar:
		expectedMin = 300 // 300 Mpixels/sec minimum
		backendName = "Scalar"
	default:
		expectedMin = 100 // Conservative fallback
		backendName = ActiveSADBackend.String()
	}

	t.Logf("Backend: %s, Throughput: %.1f Mpixels/sec (expected ≥%.1f)", backendName, mpixelsPerSec, expectedMin)

	if mpixelsPerSec < expectedMin {
		t.Errorf("Performance regression detected: %.1f Mpixels/sec (expected ≥%.1f)",
			mpixelsPerSec, expectedMin)
	}
}

// ---------------------- Large Image Tests ----------------------

// TestSAD_LargeImages stress tests with very large images
func TestSAD_LargeImages(t *testing.T) {
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
			img1 := randomNRGBA(sz.width, sz.height, 333)
			img2 := randomNRGBA(sz.width, sz.height, 444)

			// Should not panic or crash
			simdResult := fastSAD(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			scalarResult := fastSAD_Scalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)

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

// TestSAD_PaddedStride tests handling of non-standard stride (padded images)
func TestSAD_PaddedStride(t *testing.T) {
	width, height := 63, 32 // Non-multiple of 8 (tests remainder handling)

	// Create image with padded stride (align to 64 bytes)
	stride := ((width*4 + 63) / 64) * 64
	pix1 := make([]uint8, stride*height)
	pix2 := make([]uint8, stride*height)

	img1 := &image.NRGBA{Pix: pix1, Stride: stride, Rect: image.Rect(0, 0, width, height)}
	img2 := &image.NRGBA{Pix: pix2, Stride: stride, Rect: image.Rect(0, 0, width, height)}

	// Fill with test pattern
	rng := rand.New(rand.NewSource(888))
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
	result := FastSAD(img1, img2)

	// Check for invalid results
	if math.IsNaN(result) || math.IsInf(result, 0) {
		t.Errorf("Padded stride produced invalid result: %f", result)
	}

	if result < 0 {
		t.Errorf("SAD should be non-negative, got %f", result)
	}

	t.Logf("Padded stride test passed: width=%d, stride=%d, result=%f", width, stride, result)
}

// ---------------------- Backend Selection Tests ----------------------

// TestSAD_BackendSelection validates that the correct backend was selected based on CPU features
func TestSAD_BackendSelection(t *testing.T) {
	t.Logf("Active SAD backend: %s", ActiveSADBackend)

	// Verify backend is consistent with CPU features
	if cpu.X86.HasAVX2 {
		if ActiveSADBackend != SADBackendAVX2 {
			t.Logf("Note: AVX2 available but backend is %s (may be disabled via GODEBUG)", ActiveSADBackend)
		} else {
			t.Logf("AVX2 backend correctly selected")
		}
	} else {
		if ActiveSADBackend == SADBackendAVX2 {
			t.Errorf("AVX2 backend selected but CPU doesn't support AVX2")
		}
	}

	// ARM64 NEON check
	if cpu.ARM64.HasASIMD {
		if ActiveSADBackend != SADBackendNEON {
			t.Logf("Note: NEON available but backend is %s", ActiveSADBackend)
		} else {
			t.Logf("NEON backend correctly selected")
		}
	} else {
		if ActiveSADBackend == SADBackendNEON {
			t.Errorf("NEON backend selected but CPU doesn't support NEON")
		}
	}

	// Verify fastSAD function pointer is set
	if fastSAD == nil {
		t.Error("fastSAD function pointer is nil")
	}

	// Smoke test
	img := randomNRGBA(16, 16, 42)
	result := FastSAD(img, img)

	if result != 0.0 {
		t.Errorf("SAD of identical images should be 0.0, got %f", result)
	}

	t.Logf("Backend selection validated: %s", ActiveSADBackend)
}

// BenchmarkSAD_Scalar benchmarks scalar SAD implementation
func BenchmarkSAD_Scalar(b *testing.B) {
	img1 := randomNRGBA(256, 256, 100)
	img2 := randomNRGBA(256, 256, 200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fastSAD_Scalar(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	elapsed := b.Elapsed().Seconds()
	mpixels := float64(b.N*256*256) / 1e6 / elapsed
	b.ReportMetric(mpixels, "Mpixels/sec")
}

// BenchmarkSAD_Active benchmarks active backend (AVX2 or scalar)
func BenchmarkSAD_Active(b *testing.B) {
	img1 := randomNRGBA(256, 256, 100)
	img2 := randomNRGBA(256, 256, 200)

	b.Logf("Active backend: %s", ActiveSADBackend)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fastSAD(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	elapsed := b.Elapsed().Seconds()
	mpixels := float64(b.N*256*256) / 1e6 / elapsed
	b.ReportMetric(mpixels, "Mpixels/sec")
}

// BenchmarkSAD_HighLevel benchmarks high-level FastSAD wrapper
func BenchmarkSAD_HighLevel(b *testing.B) {
	img1 := randomNRGBA(256, 256, 100)
	img2 := randomNRGBA(256, 256, 200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FastSAD(img1, img2)
	}

	elapsed := b.Elapsed().Seconds()
	mpixels := float64(b.N*256*256) / 1e6 / elapsed
	b.ReportMetric(mpixels, "Mpixels/sec")
}

// BenchmarkSADvsSSD compares SAD and SSD implementations
func BenchmarkSADvsSSD(b *testing.B) {
	img1 := randomNRGBA(256, 256, 100)
	img2 := randomNRGBA(256, 256, 200)

	b.Run("SAD_scalar", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			fastSAD_Scalar(img1.Pix, img2.Pix, img1.Stride, 256, 256)
		}
		elapsed := b.Elapsed().Seconds()
		mpixels := float64(b.N*256*256) / 1e6 / elapsed
		b.ReportMetric(mpixels, "Mpixels/sec")
	})

	b.Run("SAD_active", func(b *testing.B) {
		b.Logf("Backend: %s", ActiveSADBackend)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			fastSAD(img1.Pix, img2.Pix, img1.Stride, 256, 256)
		}
		elapsed := b.Elapsed().Seconds()
		mpixels := float64(b.N*256*256) / 1e6 / elapsed
		b.ReportMetric(mpixels, "Mpixels/sec")
	})

	b.Run("SSD_scalar", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, 256, 256)
		}
		elapsed := b.Elapsed().Seconds()
		mpixels := float64(b.N*256*256) / 1e6 / elapsed
		b.ReportMetric(mpixels, "Mpixels/sec")
	})

	b.Run("SSD_active", func(b *testing.B) {
		b.Logf("Backend: %s", ActiveSSDBackend)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			fastSSD(img1.Pix, img2.Pix, img1.Stride, 256, 256)
		}
		elapsed := b.Elapsed().Seconds()
		mpixels := float64(b.N*256*256) / 1e6 / elapsed
		b.ReportMetric(mpixels, "Mpixels/sec")
	})
}

// Note: randomNRGBA helper is defined in ssd_test.go
