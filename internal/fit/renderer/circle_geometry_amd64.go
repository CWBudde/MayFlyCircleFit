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

// circleSpanAVX2Enabled caches the AVX2 gate together with the environment
// opt-out so per-call checks stay a single boolean load.
var circleSpanAVX2Enabled bool

func init() {
	if fit.SIMDDisabledByEnv() {
		return
	}
	if cpu.X86.HasAVX2 {
		circleSpanFloat32Backend = "avx2"
		circleSpanFloat32Selected = circleSpanFloat32AVX2Unchecked
		circleSpanAVX2Enabled = true
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

// circleSpanQ16AVX2Kernel finds both half-open span edges using eight Q16.16
// squared-distance comparisons per AVX2 iteration.
//
//go:noescape
func circleSpanQ16AVX2Kernel(centerXQ int32, roundedCenter int, radiusSquaredMinusDY int64, width int) (xStart, xEnd int)
