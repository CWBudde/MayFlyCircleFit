//go:build amd64

package renderer

import (
	"math"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// circleSpanFloat32Kernel is the tier whose float32 span kernel is installed,
// and circleSpanFloat32Selected is that kernel. The two are kept in step by the
// tier consumer below; nothing else may assign either.
var (
	circleSpanFloat32Kernel   = fit.TierScalar
	circleSpanFloat32Selected = circleSpanFloat32
)

func init() {
	fit.RegisterTierConsumer(func(tier fit.SIMDTier) {
		if tier == fit.TierAVX2 {
			circleSpanFloat32Kernel = fit.TierAVX2
			circleSpanFloat32Selected = circleSpanFloat32AVX2Unchecked
			return
		}
		circleSpanFloat32Kernel = fit.TierScalar
		circleSpanFloat32Selected = circleSpanFloat32
	})
}

func circleSpanFloat32AVX2(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	if circleSpanFloat32Kernel != fit.TierAVX2 {
		return circleSpanFloat32(centerX, radiusSquaredMinusDY, width)
	}
	return circleSpanFloat32AVX2Unchecked(centerX, radiusSquaredMinusDY, width)
}

func circleSpanFloat32AVX2Unchecked(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	cx := int(centerX + 0.5)
	return circleSpanFloat32AVX2Kernel(centerX, radiusSquaredMinusDY, float32(cx), width)
}

// Circle-span geometry deliberately has no SSE2 kernel, in either the Q16.16 or
// the float32 form.
//
// The Q16.16 AVX2 kernel compares Q32.32 products with VPCMPGTQ, and SSE2 has
// no 64-bit signed compare, so an SSE2 port would have to emulate one with
// several extra instructions per vector. That cost cannot be recovered:
// BenchmarkCircleSpanQ16AVX2Direct on a Ryzen 5 4600H measures the AVX2 kernel,
// which has the compare in hardware, at 14.4/28.2/62.8/133 ns against
// 9.2/9.8/23.3/44.6 ns for the scalar finite-difference span at radii
// 5.25/25.25/100.25/256.25 - already 1.6x to 3.0x slower. A measured profile of
// the no-AVX2 configuration also attributes only 2.80% of flat samples to
// fixedCircleQ16.span. spanAVX2 therefore falls through to the scalar span on
// non-AVX2 CPUs.
//
// A float32 SSE2 kernel existed briefly and was removed before merge for a
// different reason: circleSpanFloat32Selected is reachable only through
// CPURenderer.forceFloat32Geometry, which is set by no configuration path and
// no CLI flag - only by circle_geometry_test.go. Production geometry runs
// fixedCircleQ16.span or circleSpanFloat64. Adding a hand-written kernel to a
// path the program cannot enter buys nothing and costs a permanent maintenance
// and correctness-testing surface. The AVX2 float32 kernel predates this work
// and is kept because it is the reference the reduced-precision comparison
// tests measure against, but it is in the same position: if float32 geometry is
// ever wanted in production it needs a configuration path first, and that
// decision should come with a profile.
func (g fixedCircleQ16) spanAVX2(y, width int) (xStart, xEnd int, intersects bool) {
	const vectorMarginQ = 8 * circleQ16Scale
	roundedCenterQ := int64(g.centerX) << circleQ16FractionBits
	if circleSpanFloat32Kernel != fit.TierAVX2 || g.centerX < 0 || g.centerX >= width ||
		roundedCenterQ < math.MinInt32+vectorMarginQ || roundedCenterQ > math.MaxInt32-vectorMarginQ {
		return g.span(y, width)
	}

	dyQ := (int64(y) << circleQ16FractionBits) - int64(g.yQ)
	radiusQ := int64(g.radiusQ)
	if dyQ < -radiusQ || dyQ > radiusQ {
		return 0, 0, false
	}

	xStart, xEnd = circleSpanQ16AVX2Kernel(g.xQ, g.centerX, g.radiusSquared-dyQ*dyQ, width)
	return xStart, xEnd, xEnd > xStart
}

// circleSpanFloat32AVX2Kernel finds both half-open span edges using eight
// float32 squared-distance comparisons per AVX2 iteration.
//
//go:noescape
func circleSpanFloat32AVX2Kernel(centerX, radiusSquaredMinusDY, roundedCenter float32, width int) (xStart, xEnd int)

// circleSpanQ16AVX2Kernel finds both half-open span edges using eight Q16.16
// squared-distance comparisons per AVX2 iteration.
//
//go:noescape
func circleSpanQ16AVX2Kernel(centerXQ int32, roundedCenter int, radiusSquaredMinusDY int64, width int) (xStart, xEnd int)
