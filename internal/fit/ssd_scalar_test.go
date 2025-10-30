package fit

import (
	"fmt"
	"image/color"
	"math"
	"testing"
)

// ---------------------- Scalar Variant Equivalence Tests ----------------------

// TestScalarVariants_Equivalence ensures all scalar variants produce identical results
func TestScalarVariants_Equivalence(t *testing.T) {
	sizes := []struct {
		width, height int
	}{
		{1, 1},       // Single pixel
		{3, 3},       // Smaller than unroll factor
		{4, 4},       // Exactly one unroll iteration
		{7, 7},       // Not multiple of unroll factor
		{8, 8},       // Multiple of unroll factor
		{64, 64},     // Medium
		{256, 256},   // Large
		{17, 23},     // Non-square
		{100, 100},   // Moderate
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.width, sz.height), func(t *testing.T) {
			img1 := randomNRGBA(sz.width, sz.height, 1111)
			img2 := randomNRGBA(sz.width, sz.height, 2222)

			// Compute with all three scalar variants
			naive := ssdScalarNaive(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			unrolled4 := ssdScalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			unrolled8 := ssdScalarUnrolled8(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)

			// All variants should produce bit-exact identical results
			if naive != unrolled4 {
				t.Errorf("Naive vs Unrolled4 mismatch: naive=%f, unrolled4=%f, diff=%e",
					naive, unrolled4, math.Abs(naive-unrolled4))
			}

			if naive != unrolled8 {
				t.Errorf("Naive vs Unrolled8 mismatch: naive=%f, unrolled8=%f, diff=%e",
					naive, unrolled8, math.Abs(naive-unrolled8))
			}

			if unrolled4 != unrolled8 {
				t.Errorf("Unrolled4 vs Unrolled8 mismatch: unrolled4=%f, unrolled8=%f, diff=%e",
					unrolled4, unrolled8, math.Abs(unrolled4-unrolled8))
			}
		})
	}
}

// TestScalarVariants_EdgeCases tests scalar variants with edge case dimensions
func TestScalarVariants_EdgeCases(t *testing.T) {
	edgeCases := []struct {
		name          string
		width, height int
	}{
		{"1x1", 1, 1},
		{"1xN", 1, 100},
		{"Nx1", 100, 1},
		{"3x3", 3, 3},   // Smaller than unroll
		{"5x5", 5, 5},   // Not divisible by 4 or 8
		{"7x7", 7, 7},   // Not divisible by 4 or 8
		{"9x9", 9, 9},   // Odd square
		{"15x15", 15, 15}, // Not divisible by 4 or 8
	}

	for _, tc := range edgeCases {
		t.Run(tc.name, func(t *testing.T) {
			img1 := randomNRGBA(tc.width, tc.height, 3333)
			img2 := randomNRGBA(tc.width, tc.height, 4444)

			naive := ssdScalarNaive(img1.Pix, img2.Pix, img1.Stride, tc.width, tc.height)
			unrolled4 := ssdScalar(img1.Pix, img2.Pix, img1.Stride, tc.width, tc.height)
			unrolled8 := ssdScalarUnrolled8(img1.Pix, img2.Pix, img1.Stride, tc.width, tc.height)

			if naive != unrolled4 || naive != unrolled8 {
				t.Errorf("Edge case %s: variants disagree (naive=%f, u4=%f, u8=%f)",
					tc.name, naive, unrolled4, unrolled8)
			}
		})
	}
}

// ---------------------- Scalar Implementation Selection Tests ----------------------

// TestSetScalarImplementation tests that variant selection works correctly
func TestSetScalarImplementation(t *testing.T) {
	// Save original implementation
	originalImpl := GetScalarImplementation()
	defer SetScalarImplementation(originalImpl)

	img1 := randomNRGBA(64, 64, 5555)
	img2 := randomNRGBA(64, 64, 6666)

	// Test each implementation
	implementations := []scalarImplementation{
		scalarNaive,
		scalarUnrolled4,
		scalarUnrolled8,
	}

	results := make([]float64, len(implementations))

	for i, impl := range implementations {
		SetScalarImplementation(impl)

		if GetScalarImplementation() != impl {
			t.Errorf("SetScalarImplementation(%v) failed, got %v", impl, GetScalarImplementation())
		}

		results[i] = fastSSD_Scalar(img1.Pix, img2.Pix, img1.Stride, 64, 64)
	}

	// All results should be identical
	for i := 1; i < len(results); i++ {
		if results[0] != results[i] {
			t.Errorf("Implementation %d produced different result: %f vs %f",
				i, results[0], results[i])
		}
	}
}

// TestScalarImplementation_String tests string representation
func TestScalarImplementation_String(t *testing.T) {
	tests := []struct {
		impl     scalarImplementation
		expected string
	}{
		{scalarNaive, "naive"},
		{scalarUnrolled4, "unrolled4"},
		{scalarUnrolled8, "unrolled8"},
		{scalarImplementation(999), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.impl.String(); got != tt.expected {
			t.Errorf("%v.String() = %q, want %q", tt.impl, got, tt.expected)
		}
	}
}

// ---------------------- Performance Benchmarks ----------------------

// BenchmarkScalarNaive benchmarks the naive reference implementation
func BenchmarkScalarNaive(b *testing.B) {
	SetScalarImplementation(scalarNaive)
	defer SetScalarImplementation(scalarUnrolled4)

	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ssdScalarNaive(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkScalarUnrolled4 benchmarks the 4-way unrolled implementation (default)
func BenchmarkScalarUnrolled4(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ssdScalar(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkScalarUnrolled8 benchmarks the 8-way unrolled implementation
func BenchmarkScalarUnrolled8(b *testing.B) {
	img1 := randomNRGBA(256, 256, 1)
	img2 := randomNRGBA(256, 256, 2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ssdScalarUnrolled8(img1.Pix, img2.Pix, img1.Stride, 256, 256)
	}

	mpixelsPerSec := BenchmarkSSDBackend(b.N, 256, 256, b.Elapsed().Nanoseconds())
	b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
}

// BenchmarkScalarVariants_Comparison compares all scalar variants side-by-side
func BenchmarkScalarVariants_Comparison(b *testing.B) {
	sizes := []struct {
		name          string
		width, height int
	}{
		{"64x64", 64, 64},
		{"128x128", 128, 128},
		{"256x256", 256, 256},
		{"512x512", 512, 512},
	}

	variants := []struct {
		name string
		fn   func([]uint8, []uint8, int, int, int) float64
	}{
		{"naive", ssdScalarNaive},
		{"unrolled4", ssdScalar},
		{"unrolled8", ssdScalarUnrolled8},
	}

	for _, sz := range sizes {
		img1 := randomNRGBA(sz.width, sz.height, 1)
		img2 := randomNRGBA(sz.width, sz.height, 2)

		for _, variant := range variants {
			name := fmt.Sprintf("%s_%s", sz.name, variant.name)
			b.Run(name, func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					variant.fn(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
				}
				mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
				b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
			})
		}
	}
}

// BenchmarkScalarVsMSECost compares scalar SSD against original MSECost
func BenchmarkScalarVsMSECost(b *testing.B) {
	sizes := []struct {
		name          string
		width, height int
	}{
		{"64x64", 64, 64},
		{"256x256", 256, 256},
		{"512x512", 512, 512},
	}

	for _, sz := range sizes {
		img1 := randomNRGBA(sz.width, sz.height, 1)
		img2 := randomNRGBA(sz.width, sz.height, 2)

		b.Run(sz.name+"_MSECost", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MSECost(img1, img2)
			}
			mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
			b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
		})

		b.Run(sz.name+"_scalarUnrolled4", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ssdScalar(img1.Pix, img2.Pix, img1.Stride, sz.width, sz.height)
			}
			mpixelsPerSec := BenchmarkSSDBackend(b.N, sz.width, sz.height, b.Elapsed().Nanoseconds())
			b.ReportMetric(mpixelsPerSec, "Mpixels/sec")
		})
	}
}

// ---------------------- Correctness Tests for Unrolled Code ----------------------

// TestScalarUnrolling_RemainderHandling tests that remainder pixels are processed correctly
func TestScalarUnrolling_RemainderHandling(t *testing.T) {
	// Test widths that leave different remainders after unrolling
	widths := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33}

	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			img1 := randomNRGBA(width, 10, 7777)
			img2 := randomNRGBA(width, 10, 8888)

			naive := ssdScalarNaive(img1.Pix, img2.Pix, img1.Stride, width, 10)
			unrolled4 := ssdScalar(img1.Pix, img2.Pix, img1.Stride, width, 10)
			unrolled8 := ssdScalarUnrolled8(img1.Pix, img2.Pix, img1.Stride, width, 10)

			if naive != unrolled4 {
				t.Errorf("Width %d: remainder handling incorrect in unrolled4 (naive=%f, unrolled4=%f)",
					width, naive, unrolled4)
			}

			if naive != unrolled8 {
				t.Errorf("Width %d: remainder handling incorrect in unrolled8 (naive=%f, unrolled8=%f)",
					width, naive, unrolled8)
			}
		})
	}
}

// TestScalarUnrolling_ExactMultiples tests unrolling with exact multiples of unroll factor
func TestScalarUnrolling_ExactMultiples(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{"4x4", 4, 4},     // Exactly 1 unroll4 iteration
		{"8x8", 8, 8},     // Exactly 2 unroll4 iterations or 1 unroll8
		{"16x16", 16, 16}, // Exact multiples of both 4 and 8
		{"32x32", 32, 32}, // Larger exact multiples
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img1 := randomNRGBA(tt.width, tt.height, 9999)
			img2 := randomNRGBA(tt.width, tt.height, 10000)

			naive := ssdScalarNaive(img1.Pix, img2.Pix, img1.Stride, tt.width, tt.height)
			unrolled4 := ssdScalar(img1.Pix, img2.Pix, img1.Stride, tt.width, tt.height)
			unrolled8 := ssdScalarUnrolled8(img1.Pix, img2.Pix, img1.Stride, tt.width, tt.height)

			if naive != unrolled4 || naive != unrolled8 {
				t.Errorf("%s: variants disagree (naive=%f, u4=%f, u8=%f)",
					tt.name, naive, unrolled4, unrolled8)
			}
		})
	}
}

// TestScalarInt32_NoOverflow tests that int32 arithmetic doesn't overflow
func TestScalarInt32_NoOverflow(t *testing.T) {
	// Create worst-case scenario: maximum differences (255 - 0 = 255)
	// Squared: 255^2 = 65,025
	// Per pixel: 3 channels × 65,025 = 195,075
	// Unroll4 accumulates 4 pixels: 4 × 195,075 = 780,300
	// Unroll8 accumulates 8 pixels: 8 × 195,075 = 1,560,600
	// Both fit in int32 max: 2,147,483,647

	white := solidColorNRGBA(100, 100, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	black := solidColorNRGBA(100, 100, color.NRGBA{R: 0, G: 0, B: 0, A: 255})

	// Expected per pixel: 255^2 * 3 = 195,075
	// Total for 100x100: 195,075 * 10,000 = 1,950,750,000
	expectedSum := 195075.0 * 10000.0

	unrolled4 := ssdScalar(white.Pix, black.Pix, white.Stride, 100, 100)
	unrolled8 := ssdScalarUnrolled8(white.Pix, black.Pix, white.Stride, 100, 100)

	if math.Abs(unrolled4-expectedSum) > 1e-6 {
		t.Errorf("Unrolled4 max difference: got %f, want %f", unrolled4, expectedSum)
	}

	if math.Abs(unrolled8-expectedSum) > 1e-6 {
		t.Errorf("Unrolled8 max difference: got %f, want %f", unrolled8, expectedSum)
	}
}
