//go:build !arm64 && !amd64

package renderer

import "github.com/cwbudde/circlefit/internal/fit"

// compositeSpanKernel is a constant here because only ARM64 and amd64 have
// vector span compositors. Everything else composites scalar.
const compositeSpanKernel = fit.TierScalar

// spanBlend is empty here because there is no vector kernel to feed. The row
// walkers still build and pass one, so they need no build tags; a zero-size
// struct costs nothing to construct and nothing to pass.
type spanBlend struct{}

func newSpanBlend(_, _, _, _ float64) spanBlend { return spanBlend{} }

func compositeOpaqueSpan(_ *spanBlend, pix []byte, offset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(
	_ *spanBlend,
	pix []byte,
	firstOffset, secondOffset, pixels int,
	r, g, b, alpha float64,
) {
	compositeOpaqueSpanPairScalar(pix, firstOffset, secondOffset, pixels, r, g, b, alpha)
}
