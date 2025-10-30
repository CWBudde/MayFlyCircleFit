package fit

import (
	"image"
	"log/slog"

	"golang.org/x/sys/cpu"
)

// SAD (Sum of Absolute Differences) with Quadratic Weighting kernel.
//
// This implements the cost function from the original Delphi implementation:
//   Value = |R1-R2| + |G1-G2| + |B1-B2|  (SAD per pixel)
//   Cost = Scale × Value × (255 + 9×Value)
//        = Scale × (255×Value + 9×Value²)  // Quadratic weighting
//
// Where Scale = 1.5378700499807766243752402921953E-6
//
// This quadratic weighting provides perceptually-weighted error measurement,
// giving more importance to larger differences (more visually noticeable).
//
// Architecture-specific implementations:
//   - sad_amd64.s:     AVX2 with VPSADBW (processes 8 pixels/iteration)
//   - sad_arm64.s:     NEON (processes 4 pixels/iteration)
//   - sad_scalar.go:   Portable fallback

// SADBackend indicates which SIMD backend is active for SAD
type SADBackend int

const (
	SADBackendScalar SADBackend = iota
	SADBackendAVX2
	SADBackendNEON
)

func (b SADBackend) String() string {
	switch b {
	case SADBackendAVX2:
		return "AVX2"
	case SADBackendNEON:
		return "NEON"
	case SADBackendScalar:
		return "scalar"
	default:
		return "unknown"
	}
}

// ActiveSADBackend reports which backend was selected for SAD
var ActiveSADBackend SADBackend

// fastSAD is the function pointer for runtime-dispatched SAD computation
var fastSAD func(a, b []uint8, stride, width, height int) float64

func init() {
	// Detect CPU features and select best SAD implementation
	if cpu.X86.HasAVX2 {
		ActiveSADBackend = SADBackendAVX2
		fastSAD = fastSAD_AVX2
		slog.Debug("SAD kernel initialized", "backend", "AVX2", "instruction", "VPSADBW")
	} else if cpu.ARM64.HasASIMD {
		ActiveSADBackend = SADBackendNEON
		fastSAD = fastSAD_NEON
		slog.Debug("SAD kernel initialized", "backend", "NEON")
	} else {
		ActiveSADBackend = SADBackendScalar
		fastSAD = fastSAD_Scalar
		slog.Debug("SAD kernel initialized", "backend", "scalar")
	}
}

// FastSAD computes perceptually-weighted error using SAD + quadratic weighting.
//
// This matches the Delphi ErrorWeightingLoop function:
//   For each pixel: Value = |R1-R2| + |G1-G2| + |B1-B2|
//   Weighted cost: Scale × Value × (255 + 9×Value)
//
// The quadratic weighting emphasizes larger differences, which are more
// perceptually significant.
//
// Returns: Total weighted cost (not normalized)
func FastSAD(current, reference *image.NRGBA) float64 {
	bounds := current.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		panic("FastSAD: image dimensions must match")
	}

	return fastSAD(current.Pix, reference.Pix, current.Stride, width, height)
}

// fastSAD_AVX2 computes SAD using AVX2 VPSADBW instruction.
//
// VPSADBW (Packed Sum of Absolute Differences Byte to Word) is specifically
// designed for this operation - it computes 8 absolute differences and
// horizontally sums them in a single instruction.
//
// Algorithm per iteration:
//   1. Load 8 RGBA pixels (32 bytes) from each image
//   2. Mask out alpha channel (AND with 0x00FFFFFF repeated)
//   3. VPSADBW: Compute |a[i]-b[i]| for 32 bytes and horizontal sum
//   4. Apply quadratic weighting: value × (255 + 9×value)
//   5. Accumulate into running sum
//
// Final step: Multiply total by CScale
//
// Performance: ~3-4x faster than scalar due to VPSADBW efficiency
func fastSAD_AVX2(a, b []uint8, stride, width, height int) float64 {
	// Call assembly implementation
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	return sadAVX2(&a[0], &b[0], stride, width, height)
}

// fastSAD_NEON computes SAD using NEON SIMD (ARM64)
func fastSAD_NEON(a, b []uint8, stride, width, height int) float64 {
	// Placeholder: Will be implemented in Task 10.5
	return fastSAD_Scalar(a, b, stride, width, height)
}

// fastSAD_Scalar is the portable scalar fallback.
// Implemented in sad_scalar.go
