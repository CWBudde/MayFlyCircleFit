//go:build !amd64 && !arm64

package renderer

import "github.com/cwbudde/mayflycirclefit/internal/fit"

// deltaSSDKernel is a constant here because these architectures have no vector
// delta-SSD kernel to install.
const deltaSSDKernel = fit.TierScalar

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	return deltaSSDSpanScalar(candidate, base, reference, pixels)
}
