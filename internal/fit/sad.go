package fit

import (
	"image"
	"math"
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

// SAD has no SSE2 and no NEON kernel, so it is the one cost function whose
// installed kernel is routinely narrower than Tier(). The AVX2 kernel uses
// VPMADDUBSW (SSSE3) and VPMULLD (SSE4.1), neither of which baseline SSE2
// provides, and FastSAD has no non-test callers — porting it would mean adding
// SSSE3 and SSE4.1 tiers for an unused cost function. ARM64 has no kernel at
// all. Both cases fall back to fastSAD_Scalar.

// activeSADKernel names the kernel the SAD dispatch installed. It is written
// only from the RegisterTierConsumer callback in the dispatch files.
var activeSADKernel SIMDTier

// ActiveSADKernel reports which kernel SAD dispatch installed.
func ActiveSADKernel() SIMDTier { return activeSADKernel }

// fastSAD is the function pointer for runtime-dispatched SAD computation.
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
// Returns: Total weighted cost (not normalized).
func FastSAD(current, reference *image.NRGBA) float64 {
	bounds := current.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		return math.Inf(1)
	}

	if width == 0 || height == 0 {
		return math.Inf(1)
	}

	if current.Stride == reference.Stride {
		return fastSAD(current.Pix, reference.Pix, current.Stride, width, height)
	}

	return sadIndependentStrides(current, reference, width, height)
}

func sadIndependentStrides(current, reference *image.NRGBA, width, height int) float64 {
	var total float64

	for y := range height {
		for x := range width {
			currentOffset := y*current.Stride + x*4
			referenceOffset := y*reference.Stride + x*4
			value := 0

			for channel := range 3 {
				difference := int(current.Pix[currentOffset+channel]) - int(reference.Pix[referenceOffset+channel])
				if difference < 0 {
					difference = -difference
				}

				value += difference
			}

			total += float64(value * (255 + 9*value))
		}
	}

	return total * sadScale
}

// fastSAD_Scalar is the portable scalar fallback.
// Implemented in sad_scalar.go
