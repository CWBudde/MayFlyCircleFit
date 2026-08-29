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

// randomNRGBA creates an NRGBA image with random pixel values.
func randomNRGBA(width, height int, seed int64) *image.NRGBA {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for i := 0; i < len(img.Pix); i++ {
		img.Pix[i] = uint8(rng.Intn(256))
	}

	return img
}

// solidColorNRGBA creates an NRGBA image with a solid color.
func solidColorNRGBA(width, height int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, c)
		}
	}

	return img
}

// ---------------------- Correctness Tests ----------------------

// TestFastSSD_IdenticalImages tests that SSD of identical images is zero.
func TestFastSSD_IdenticalImages(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			img := randomNRGBA(sz.width, sz.height, 42)

			// SSD of image with itself should be zero
			ssd := FastSSD(img, img)

			if ssd != 0.0 {
				t.Errorf("SSD of identical images should be 0.0, got %f", ssd)
			}
		})
	}
}

// TestFastSSD_KnownDifference tests SSD with known pixel differences.
func TestFastSSD_KnownDifference(t *testing.T) {
	t.Parallel()

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

// TestFastSSD_MaxDifference tests SSD with maximum possible differences.
func TestFastSSD_MaxDifference(t *testing.T) {
	t.Parallel()

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

// TestFastSSD_AlphaIgnored tests that alpha channel is ignored in SSD computation.
func TestFastSSD_AlphaIgnored(t *testing.T) {
	t.Parallel()

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

// TestFastSSD_ScalarEquivalence tests that active backend matches scalar reference.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestFastSSD_ScalarEquivalence(t *testing.T) {
	if ActiveSSDKernel() == TierScalar {
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

	//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
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
				t.Logf("Active backend: %s", ActiveSSDKernel())
			}
		})
	}
}

// TestFastSSD_CompareImplementations uses the built-in comparison utility.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestFastSSD_CompareImplementations(t *testing.T) {
	if ActiveSSDKernel() == TierScalar {
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

	//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			img1 := randomNRGBA(tc.width, tc.height, tc.seed1)
			img2 := randomNRGBA(tc.width, tc.height, tc.seed2)

			// Every kernel this host can run, compared exactly. The former
			// helper compared only the installed kernel, and did so within a
			// tolerance; see the note in ssd.go.
			want := fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, tc.width, tc.height)
			for _, kernel := range hostSSDKernels() {
				if got := kernel.fn(img1.Pix, img2.Pix, img1.Stride, tc.width, tc.height); got != want {
					t.Errorf("%s (%dx%d): %s = %.0f, scalar = %.0f",
						tc.name, tc.width, tc.height, kernel.tier, got, want)
				}
			}
		})
	}
}

// ---------------------- Edge Case Tests ----------------------

// TestFastSSD_SinglePixel tests SSD with 1x1 images.
func TestFastSSD_SinglePixel(t *testing.T) {
	t.Parallel()

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

// TestFastSSD_ThinImages tests edge case with very thin images.
func TestFastSSD_ThinImages(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

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
	t.Parallel()

	img1 := randomNRGBA(64, 64, 111)

	img2 := randomNRGBA(128, 128, 222) // Different size
	if got := FastSSD(img1, img2); !math.IsInf(got, 1) {
		t.Fatalf("FastSSD mismatch = %v, want +Inf", got)
	}
}

// ---------------------- SIMD-Specific Tests ----------------------

// The former per-backend batch-boundary and accumulator tests lived here. They
// were replaced by ssd_differential_test.go, which calls every executable
// kernel directly instead of gating on the one dispatch installed: each of the
// six skipped itself on any host whose active backend was not its own, so on an
// AVX2 development machine the SSE2 and NEON kernels had no correctness
// coverage at all.

// ---------------------- Concurrency Tests ----------------------

// TestFastSSD_ConcurrentAccess tests thread-safety of SSD computation.
func TestFastSSD_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	img1 := randomNRGBA(256, 256, 111)
	img2 := randomNRGBA(256, 256, 222)

	// Run SSD from multiple goroutines simultaneously
	const goroutines = 10
	const iterations = 100

	results := make([][]float64, goroutines)
	var wg sync.WaitGroup

	for g := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			results[idx] = make([]float64, iterations)
			for i := range iterations {
				results[idx][i] = FastSSD(img1, img2)
			}
		}(g)
	}

	wg.Wait()

	// All results should be identical (no race conditions or shared state issues)
	expected := results[0][0]

	for g := range goroutines {
		for i := range iterations {
			if results[g][i] != expected {
				t.Errorf("Concurrent call mismatch: goroutine %d, iter %d: got %f, want %f",
					g, i, results[g][i], expected)
			}
		}
	}

	t.Logf("Concurrent access test passed: %d goroutines × %d iterations, all results identical", goroutines, iterations)
}

// ---------------------- Large Image Tests ----------------------

// TestFastSSD_LargeImages stress tests with very large images.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
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

	//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
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

// TestFastSSD_PaddedStride tests handling of non-standard stride (padded images).
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
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

	for y := range height {
		for x := range width {
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

	wantExact := uint64(fastSSD_Scalar(img1.Pix, img2.Pix, stride, width, height))
	if exact, ok := ExactSSD(img1, img2); !ok || exact != wantExact {
		t.Errorf("ExactSSD padded stride = (%d, %v), want (%d, true)", exact, ok, wantExact)
	}

	t.Logf("Padded stride test passed: width=%d, stride=%d, result=%f", width, stride, result)
}

// ---------------------- Tier And Kernel Selection Tests ----------------------

// requiredTierEnv lets native-hardware CI assert which tier detection landed
// on. It is deliberately a different variable from simdTierEnv: that one
// *forces* a tier, so asserting against it would be a tautology. This one
// asserts, which is what makes a step like GODEBUG=cpu.avx2=off plus
// CIRCLEFIT_REQUIRE_SIMD_TIER=sse2 a real check that feature masking still demotes
// the way the documentation claims.
const requiredTierEnv = "CIRCLEFIT_REQUIRE_SIMD_TIER"

// TestRequiredSIMDTier is the CI-facing assertion. It replaces
// MAYFLY_REQUIRE_SSD_BACKEND, which only ever described one kernel in one
// package: the renderer's dispatch was invisible to it, so a CI step could ask
// for SSE2, run the renderer package, and pass while the renderer had silently
// fallen back to scalar.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestRequiredSIMDTier(t *testing.T) {
	required := os.Getenv(requiredTierEnv)
	if required == "" {
		t.Skipf("%s is not set", requiredTierEnv)
	}

	want, ok := ParseSIMDTier(required)
	if !ok {
		t.Fatalf("%s=%q is not a tier name", requiredTierEnv, required)
	}

	if got := Tier(); got != want {
		t.Fatalf("detected tier = %s, required %s", got, want)
	}
}

// TestInstalledKernelsMatchTier is the invariant that the old per-kernel
// dispatch could not express: every kernel in the process agrees with one
// resolved tier, and the exceptions are the documented ones rather than an
// accident of which init ran.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestInstalledKernelsMatchTier(t *testing.T) {
	tier := Tier()
	t.Logf("tier %s: SSD kernel %s, SAD kernel %s", tier, ActiveSSDKernel(), ActiveSADKernel())

	if !tierSupported(tier) {
		t.Fatalf("resolved tier %s is not supported on this build", tier)
	}

	// SSD has a kernel for every tier this architecture offers, so it must
	// match exactly.
	if got := ActiveSSDKernel(); got != tier {
		t.Errorf("SSD kernel = %s, tier = %s", got, tier)
	}

	// SAD has an AVX2 kernel and nothing else; see sad.go.
	wantSAD := TierScalar
	if tier == TierAVX2 {
		wantSAD = TierAVX2
	}

	if got := ActiveSADKernel(); got != wantSAD {
		t.Errorf("SAD kernel = %s, want %s at tier %s", got, wantSAD, tier)
	}

	if fastSSD == nil {
		t.Error("fastSSD function pointer is nil")
	}

	if fastSAD == nil {
		t.Error("fastSAD function pointer is nil")
	}
}

// TestDetectedTierMatchesCPUFeatures checks the other direction: that detection
// did not select a tier this CPU cannot execute. A forced tier is excluded,
// because forcing a narrower tier than the CPU offers is the supported way to
// test a fallback.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestDetectedTierMatchesCPUFeatures(t *testing.T) {
	if os.Getenv(simdTierEnv) != "" || os.Getenv(simdDisableEnv) == "1" {
		t.Skip("tier is pinned by the environment")
	}

	switch tier := Tier(); tier {
	case TierAVX2:
		if !cpu.X86.HasAVX2 {
			t.Error("AVX2 tier selected but the CPU does not report AVX2")
		}
	case TierSSE2:
		if !cpu.X86.HasSSE2 {
			t.Error("SSE2 tier selected but the CPU does not report SSE2")
		}

		if cpu.X86.HasAVX2 {
			t.Error("SSE2 tier selected although AVX2 is available")
		}
	case TierNEON:
		if !cpu.ARM64.HasASIMD {
			t.Error("NEON tier selected but the CPU does not report ASIMD")
		}
	case TierScalar:
		if cpu.X86.HasAVX2 || cpu.ARM64.HasASIMD {
			t.Error("scalar tier selected although a vector kernel is available")
		}
	}

	// Smoke test: identical images cost nothing whatever the tier.
	img := randomNRGBA(16, 16, 42)
	if result := FastSSD(img, img); result != 0.0 {
		t.Errorf("SSD of identical images = %f, want 0", result)
	}
}

// ---------------------- Benchmark Tests ----------------------

// BenchmarkFastSSD_Scalar benchmarks scalar implementation.
func BenchmarkFastSSD_Scalar(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.ResetTimer()

	for range b.N {
		fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	// Report throughput
	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkFastSSD_Active benchmarks whichever kernel dispatch installed.
func BenchmarkFastSSD_Active(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.Logf("Active backend: %s", ActiveSSDKernel())

	b.ResetTimer()

	for range b.N {
		fastSSD(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkFastSSD_HighLevel benchmarks high-level FastSSD wrapper.
func BenchmarkFastSSD_HighLevel(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.ResetTimer()

	for range b.N {
		FastSSD(img1, img2)
	}

	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkFastSSD_Comparison benchmarks scalar vs active backend side-by-side.
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

			for range b.N {
				ssdBenchmarkSink = fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			}

			mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
			b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
		})

		// On non-amd64 builds fastSSD_SSE2 is the scalar stub, so this
		// sub-benchmark simply repeats the scalar measurement there.
		b.Run(sz.name+"_sse2", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				ssdBenchmarkSink = fastSSD_SSE2(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			}

			mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
			b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
		})

		b.Run(sz.name+"_active", func(b *testing.B) {
			b.Logf("Active backend: %s", ActiveSSDKernel())
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				ssdBenchmarkSink = fastSSD(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			}

			mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
			b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
		})
	}
}

// ---------------------- Regression Tests ----------------------

// TestFastMSECost_EquivalentToMSECost tests that FastMSECost matches MSECost.
func TestFastMSECost_EquivalentToMSECost(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

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

// TestSSDBackendDetection validates that the correct backend was selected.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestSSDBackendDetection(t *testing.T) {
	t.Logf("Active SSD backend: %s", ActiveSSDKernel())

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
