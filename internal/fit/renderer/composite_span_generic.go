//go:build !arm64

package renderer

import "github.com/cwbudde/mayflycirclefit/internal/fit"

// compositeSpanKernel is a constant here because only ARM64 has a vector span
// compositor. Adding one for amd64 is the natural next step and is tracked
// separately; until then the widest amd64 tier still composites scalar.
const compositeSpanKernel = fit.TierScalar

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanPairScalar(pix, firstOffset, secondOffset, pixels, r, g, b, alpha)
}
