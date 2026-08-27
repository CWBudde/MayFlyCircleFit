//go:build !arm64 && !amd64

package renderer

import "github.com/cwbudde/circlefit/internal/fit"

// compositeSpanKernel is a constant here because only ARM64 and amd64 have
// vector span compositors. Everything else composites scalar.
const compositeSpanKernel = fit.TierScalar

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanPairScalar(pix, firstOffset, secondOffset, pixels, r, g, b, alpha)
}
