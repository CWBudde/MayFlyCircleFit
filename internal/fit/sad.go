package fit

import (
	"image"
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
//   - sad_amd64.s: AVX2 implementation (processes 8 pixels/iteration)
//   - sad_scalar.go: portable fallback for non-amd64 platforms and amd64
//     processors without AVX2

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

// FastSAD computes perceptually-weighted error using SAD + quadratic weighting.
//
// This matches the Delphi ErrorWeightingLoop function:
//
//	For each pixel: Value = |R1-R2| + |G1-G2| + |B1-B2|
//	Weighted cost: Scale × Value × (255 + 9×Value)
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

// fastSAD_NEON computes SAD using NEON SIMD (ARM64)
func fastSAD_NEON(a, b []uint8, stride, width, height int) float64 {
	// This remains a scalar compatibility wrapper until a NEON kernel exists.
	return fastSAD_Scalar(a, b, stride, width, height)
}

// fastSAD_Scalar is the portable scalar fallback.
// Implemented in sad_scalar.go
