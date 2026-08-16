//go:build amd64

package renderer

import (
	"math"

	"golang.org/x/sys/cpu"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

var (
	circleSpanFloat32Backend  = "scalar"
	circleSpanFloat32Selected = circleSpanFloat32
)

// circleSpanAVX2Enabled and circleSpanSSE2Enabled cache the feature gates
// together with the environment opt-out so per-call checks stay a single
// boolean load. At most one of them is ever true.
var (
	circleSpanAVX2Enabled bool
	circleSpanSSE2Enabled bool
)

func init() {
	if fit.SIMDDisabledByEnv() {
		return
	}
	switch {
	case cpu.X86.HasAVX2:
		circleSpanFloat32Backend = "avx2"
		circleSpanFloat32Selected = circleSpanFloat32AVX2Unchecked
		circleSpanAVX2Enabled = true
	case cpu.X86.HasSSE2:
		circleSpanFloat32Backend = "sse2"
		circleSpanFloat32Selected = circleSpanFloat32SSE2Unchecked
		circleSpanSSE2Enabled = true
	}
}

func circleSpanFloat32AVX2(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	if !circleSpanAVX2Enabled {
		return circleSpanFloat32(centerX, radiusSquaredMinusDY, width)
	}
	return circleSpanFloat32AVX2Unchecked(centerX, radiusSquaredMinusDY, width)
}

func circleSpanFloat32AVX2Unchecked(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	cx := int(centerX + 0.5)
	return circleSpanFloat32AVX2Kernel(centerX, radiusSquaredMinusDY, float32(cx), width)
}

func circleSpanFloat32SSE2(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	if !circleSpanSSE2Enabled {
		return circleSpanFloat32(centerX, radiusSquaredMinusDY, width)
	}
	return circleSpanFloat32SSE2Unchecked(centerX, radiusSquaredMinusDY, width)
}

func circleSpanFloat32SSE2Unchecked(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	cx := int(centerX + 0.5)
	return circleSpanFloat32SSE2Kernel(centerX, radiusSquaredMinusDY, float32(cx), width)
}

// Q16.16 geometry has no SSE2 kernel. The AVX2 version compares Q32.32
// products with VPCMPGTQ, and SSE2 has no 64-bit signed compare. Emulating one
// costs several extra instructions per vector, while a measured profile of the
// no-AVX2 configuration attributes only 2.80% of flat samples to
// fixedCircleQ16.span. spanAVX2 therefore falls through to the scalar
// finite-difference span on non-AVX2 CPUs.
func (g fixedCircleQ16) spanAVX2(y, width int) (xStart, xEnd int, intersects bool) {
	const vectorMarginQ = 8 * circleQ16Scale
	roundedCenterQ := int64(g.centerX) << circleQ16FractionBits
	if !circleSpanAVX2Enabled || g.centerX < 0 || g.centerX >= width ||
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

// circleSpanFloat32SSE2Kernel finds both half-open span edges using four
// float32 squared-distance comparisons per SSE2 iteration.
//
//go:noescape
func circleSpanFloat32SSE2Kernel(centerX, radiusSquaredMinusDY, roundedCenter float32, width int) (xStart, xEnd int)

// circleSpanQ16AVX2Kernel finds both half-open span edges using eight Q16.16
// squared-distance comparisons per AVX2 iteration.
//
//go:noescape
func circleSpanQ16AVX2Kernel(centerXQ int32, roundedCenter int, radiusSquaredMinusDY int64, width int) (xStart, xEnd int)
