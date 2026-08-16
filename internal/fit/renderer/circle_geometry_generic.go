//go:build !amd64

package renderer

import "github.com/cwbudde/mayflycirclefit/internal/fit"

const circleSpanFloat32Kernel = fit.TierScalar

var circleSpanFloat32Selected = circleSpanFloat32

func circleSpanFloat32AVX2(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	return circleSpanFloat32(centerX, radiusSquaredMinusDY, width)
}

func (g fixedCircleQ16) spanAVX2(y, width int) (xStart, xEnd int, intersects bool) {
	return g.span(y, width)
}
