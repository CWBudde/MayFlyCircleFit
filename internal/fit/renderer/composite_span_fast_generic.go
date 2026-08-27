//go:build !amd64

package renderer

import "github.com/cwbudde/circlefit/internal/fit"

// fastCompositeKernel is a constant here because no non-amd64 target has a
// float32 span kernel. On those targets the fast path is the float32 scalar
// loop, which is both less accurate and slower than the exact float64 span it
// replaces, so enabling it is a pure loss.
const fastCompositeKernel = fit.TierScalar

func compositeOpaqueSpanFast(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanFastScalar(pix, offset, pixels, r, g, b, alpha)
}
