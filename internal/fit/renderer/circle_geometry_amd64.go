//go:build amd64

package renderer

import "golang.org/x/sys/cpu"

var (
	circleSpanFloat32Backend  = "scalar"
	circleSpanFloat32Selected = circleSpanFloat32
)

func init() {
	if cpu.X86.HasAVX2 {
		circleSpanFloat32Backend = "avx2"
		circleSpanFloat32Selected = circleSpanFloat32AVX2Unchecked
	}
}

func circleSpanFloat32AVX2(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	if !cpu.X86.HasAVX2 {
		return circleSpanFloat32(centerX, radiusSquaredMinusDY, width)
	}
	return circleSpanFloat32AVX2Unchecked(centerX, radiusSquaredMinusDY, width)
}

func circleSpanFloat32AVX2Unchecked(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	cx := int(centerX + 0.5)
	return circleSpanFloat32AVX2Kernel(centerX, radiusSquaredMinusDY, float32(cx), width)
}

// circleSpanFloat32AVX2Kernel finds both half-open span edges using eight
// float32 squared-distance comparisons per AVX2 iteration.
//
//go:noescape
func circleSpanFloat32AVX2Kernel(centerX, radiusSquaredMinusDY, roundedCenter float32, width int) (xStart, xEnd int)
