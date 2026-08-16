//go:build amd64

package renderer

import "github.com/cwbudde/mayflycirclefit/internal/fit"

// fastCompositeKernel is the tier whose fast span kernel is installed.
var fastCompositeKernel = fit.TierScalar

// Minimum span lengths worth entering a vector kernel for, measured as exactly
// one vector batch. Higher cutoffs were tried and lost: the scalar float32
// fallback is slower than the exact float64 loop it replaces, so any span left
// to it is a regression. Entering the kernel as soon as one full batch exists
// keeps short spans ahead of the exact path too.
const (
	fastCompositeSSE2MinPixels = 4
	fastCompositeAVX2MinPixels = 8
)

func init() {
	fit.RegisterTierConsumer(func(tier fit.SIMDTier) {
		switch tier {
		case fit.TierAVX2, fit.TierSSE2:
			fastCompositeKernel = tier
		case fit.TierScalar, fit.TierNEON:
			fastCompositeKernel = fit.TierScalar
		}
	})
}

func compositeOpaqueSpanFast(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	if pixels <= 0 {
		return
	}

	addR, addG, addB, mul := fastSpanConstants(r, g, b, alpha)
	// Alpha keeps its byte value: multiplier 1, addend 0. The group repeats so
	// the same arrays serve both the four-lane and eight-lane kernels.
	addend := [8]float32{addR, addG, addB, 0, addR, addG, addB, 0}
	multiplier := [8]float32{mul, mul, mul, 1, mul, mul, mul, 1}

	vectorPixels := 0
	switch {
	case fastCompositeKernel == fit.TierAVX2 && pixels >= fastCompositeAVX2MinPixels:
		vectorPixels = pixels &^ 7
		compositeSpanFastAVX2(&pix[offset], vectorPixels/8, &addend[0], &multiplier[0])
	case fastCompositeKernel != fit.TierScalar && pixels >= fastCompositeSSE2MinPixels:
		vectorPixels = pixels &^ 3
		compositeSpanFastSSE2(&pix[offset], vectorPixels/4, &addend[0], &multiplier[0])
	}

	if vectorPixels < pixels {
		compositeOpaqueSpanFastScalar(pix, offset+vectorPixels*4, pixels-vectorPixels, r, g, b, alpha)
	}
}

// compositeSpanFastSSE2 blends batches*4 pixels in place.
//
//go:noescape
func compositeSpanFastSSE2(pix *byte, batches int, addend, multiplier *float32)

// compositeSpanFastAVX2 blends batches*8 pixels in place.
//
//go:noescape
func compositeSpanFastAVX2(pix *byte, batches int, addend, multiplier *float32)
