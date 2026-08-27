//go:build arm64

package renderer

import "github.com/cwbudde/circlefit/internal/fit"

// deltaSSDKernel is the tier whose kernels deltaSSDSpan may use. See the amd64
// twin; the two files deliberately share the variable name and dispatch shape.
var deltaSSDKernel = fit.TierScalar

func init() {
	fit.RegisterTierConsumer(func(tier fit.SIMDTier) {
		switch tier {
		case fit.TierNEON:
			deltaSSDKernel = fit.TierNEON
		case fit.TierScalar, fit.TierSSE2, fit.TierAVX2:
			deltaSSDKernel = fit.TierScalar
		}
	})
}

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	if deltaSSDKernel == fit.TierNEON && pixels >= 4 {
		return deltaSSDSpanNEON(&candidate[0], &base[0], &reference[0], pixels)
	}
	return deltaSSDSpanScalar(candidate, base, reference, pixels)
}

// deltaSSDSpanNEON computes an exact signed RGB SSD delta for one NRGBA span.
//
//go:noescape
func deltaSSDSpanNEON(candidate, base, reference *byte, pixels int) int64
