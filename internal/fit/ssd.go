package fit

import (
	"image"
	"math"
)

// SSD (Sum of Squared Differences) kernel interface for SIMD-accelerated cost computation.
//
// This file defines the interface for computing pixel-wise sum of squared differences
// between two NRGBA images, with runtime dispatch to AVX2 on supported amd64
// processors, NEON on supported ARM64 processors, and a scalar fallback.
//
// Architecture-specific implementations:
//   - ssd_amd64.s: AVX2 implementation (256-bit, processes 8 pixels/iteration)
//   - ssd_arm64.s: NEON implementation (128-bit, processes 4 pixels/iteration)
//   - ssd_dispatch_generic.go: scalar dispatch for other platforms
//
// Performance expectations:
//   - AVX2: approximately 6x measured speedup on AMD Ryzen 5 4600H
//   - NEON: approximately 5.2x measured speedup on Apple M5
//   - Scalar: baseline and portable fallback

// SSDBackend indicates which SIMD backend is active
type SSDBackend int

const (
	SSDBackendScalar SSDBackend = iota // Scalar fallback (no SIMD)
	SSDBackendAVX2                     // AVX2 (x86-64, 256-bit)
	SSDBackendNEON                     // NEON (ARM64, 128-bit)
)

func (b SSDBackend) String() string {
	switch b {
	case SSDBackendAVX2:
		return "AVX2"
	case SSDBackendNEON:
		return "NEON"
	case SSDBackendScalar:
		return "scalar"
	default:
		return "unknown"
	}
}

// ActiveSSDBackend reports which backend was selected at initialization
var ActiveSSDBackend SSDBackend

// fastSSD is the function pointer for runtime-dispatched SSD computation.
// Set by an architecture-specific init function.
var fastSSD func(a, b []uint8, stride, width, height int) float64

const (
	maxRGBSquaredDifference = uint64(3 * 255 * 255)
	maxExactFloat64Integer  = uint64(1 << 53)
)

// ExactSSD returns the unnormalized RGB sum of squared differences as an exact
// integer. The active SIMD kernel currently returns its integer reduction in a
// float64, so this API accepts only image sizes whose worst-case sum is within
// float64's exact-integer range. This covers any practical in-memory image; the
// boolean is false for empty, mismatched, or theoretically oversized inputs.
// Alpha bytes are ignored, matching FastSSD.
func ExactSSD(current, reference *image.NRGBA) (uint64, bool) {
	bounds := current.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 || width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		return 0, false
	}

	unsignedWidth := uint64(width)
	unsignedHeight := uint64(height)
	if unsignedWidth > ^uint64(0)/unsignedHeight {
		return 0, false
	}
	pixelCount := unsignedWidth * unsignedHeight
	if pixelCount > maxExactFloat64Integer/maxRGBSquaredDifference {
		return 0, false
	}

	if current.Stride != reference.Stride {
		return ssdIndependentStridesExact(current, reference, width, height), true
	}
	sum := fastSSD(current.Pix, reference.Pix, current.Stride, width, height)
	exact := uint64(sum)
	return exact, sum == float64(exact)
}

// FastSSD computes sum of squared differences between two NRGBA images.
//
// This is a high-level wrapper around the low-level fastSSD kernel. It handles
// image dimension validation and computes the mean squared error (MSE) over RGB channels.
//
// The alpha channel is ignored (only RGB channels contribute to cost).
//
// Returns: MSE = sum(squared differences) / (width * height * 3)
//
// Performance: Uses runtime-dispatched SIMD kernel (AVX2/NEON/scalar).
func FastSSD(current, reference *image.NRGBA) float64 {
	bounds := current.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		return math.Inf(1)
	}
	if width == 0 || height == 0 {
		return math.Inf(1)
	}

	var sum float64
	if current.Stride == reference.Stride {
		sum = fastSSD(current.Pix, reference.Pix, current.Stride, width, height)
	} else {
		sum = ssdIndependentStrides(current, reference, width, height)
	}

	// Return mean over pixels and channels (3 channels: RGB)
	return sum / float64(width*height*3)
}

func ssdIndependentStrides(current, reference *image.NRGBA, width, height int) float64 {
	var sum float64
	for y := range height {
		for x := range width {
			currentOffset := y*current.Stride + x*4
			referenceOffset := y*reference.Stride + x*4
			for channel := range 3 {
				difference := float64(current.Pix[currentOffset+channel]) - float64(reference.Pix[referenceOffset+channel])
				sum += difference * difference
			}
		}
	}
	return sum
}

func ssdIndependentStridesExact(current, reference *image.NRGBA, width, height int) uint64 {
	var sum uint64
	for y := range height {
		for x := range width {
			currentOffset := y*current.Stride + x*4
			referenceOffset := y*reference.Stride + x*4
			for channel := range 3 {
				difference := int64(current.Pix[currentOffset+channel]) - int64(reference.Pix[referenceOffset+channel])
				sum += uint64(difference * difference)
			}
		}
	}
	return sum
}

// fastSSD_Scalar is the portable scalar fallback implementation.
//
// Implementation: ssd_scalar.go (optimized with loop unrolling and int32 arithmetic)
//
// This is the reference implementation used when SIMD is unavailable or for
// validation testing. The actual implementation provides multiple variants:
//   - ssdScalarNaive:     Simple reference (no optimizations)
//   - ssdScalar:          4-way unrolled (default, 1.35x faster than naive)
//   - ssdScalarUnrolled8: 8-way unrolled (experimental, 1.48x faster than naive)
//
// See ssd_scalar.go for implementation details and performance characteristics.
//
// Note: This function is declared here but implemented in ssd_scalar.go

// ---------------------- Alternative Cost Function Using SSD Kernel ----------------------

// FastMSECost is a drop-in replacement for MSECost using the SIMD-accelerated SSD kernel.
//
// This function has the same signature as MSECost and can be used as a CostFunc.
// It uses the fastest supported SSD kernel and falls back to portable scalar
// code when the current CPU has no native implementation.
//
// CPURenderer selects this cost by default. After installing a custom cost
// function, restore it with:
//
//	renderer.UseFastCost()
func FastMSECost(current, reference *image.NRGBA) float64 {
	return FastSSD(current, reference)
}

// ---------------------- Testing and Validation Utilities ----------------------

// CompareSSDImplementations validates SIMD implementations against scalar reference.
//
// This utility function compares the output of all available SSD implementations
// (scalar, AVX2, NEON) to ensure bit-exact equivalence (within floating-point tolerance).
//
// Useful for:
//   - Unit tests (Task 10.4/10.5)
//   - Regression testing (ensure SIMD and scalar produce same results)
//   - Performance benchmarking (measure speedup vs scalar baseline)
//
// Returns: true if all implementations match within tolerance, false otherwise
//
// Example usage in tests:
//
//	if !CompareSSDImplementations(imgA, imgB, 1e-9) {
//	    t.Error("SIMD implementation differs from scalar reference")
//	}
func CompareSSDImplementations(a, b *image.NRGBA, tolerance float64) bool {
	width := a.Bounds().Dx()
	height := a.Bounds().Dy()
	stride := a.Stride

	// Compute with scalar reference
	scalarResult := fastSSD_Scalar(a.Pix, b.Pix, stride, width, height)

	// Compute with current backend (may be AVX2, NEON, or scalar)
	activeResult := fastSSD(a.Pix, b.Pix, stride, width, height)

	// Check if results match within tolerance
	diff := scalarResult - activeResult
	if diff < 0 {
		diff = -diff
	}

	return diff <= tolerance
}

// BenchmarkSSDBackend measures throughput of a specific SSD backend.
//
// Returns: throughput in megapixels/second
//
// Example usage in benchmarks:
//
//	func BenchmarkSSDScalar(b *testing.B) {
//	    img := randomImage(256, 256)
//	    b.ResetTimer()
//	    for i := 0; i < b.N; i++ {
//	        fastSSD_Scalar(img.Pix, img.Pix, img.Stride, 256, 256)
//	    }
//	    b.ReportMetric(BenchmarkSSDBackend(b, 256, 256), "Mpixels/sec")
//	}
func BenchmarkSSDBackend(iterations int, width, height int, durationNs int64) float64 {
	totalPixels := float64(iterations) * float64(width) * float64(height)
	seconds := float64(durationNs) / 1e9
	return (totalPixels / 1e6) / seconds // Megapixels per second
}
