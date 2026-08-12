//go:build !amd64

package renderer

const circleSpanFloat32Backend = "scalar"

var circleSpanFloat32Selected = circleSpanFloat32

func circleSpanFloat32AVX2(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	return circleSpanFloat32(centerX, radiusSquaredMinusDY, width)
}
