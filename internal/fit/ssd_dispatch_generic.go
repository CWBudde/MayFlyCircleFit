//go:build !amd64 && !arm64

package fit

import "log/slog"

func init() {
	RegisterTierConsumer(installSSDKernel)
}

// installSSDKernel has one kernel to choose from on these architectures, but
// still goes through the tier consumer so the invariant test covers every
// build.
func installSSDKernel(tier SIMDTier) {
	activeSSDKernel = TierScalar
	fastSSD = fastSSD_Scalar
	slog.Debug("SSD kernel installed", "tier", tier, "kernel", activeSSDKernel)
}

// fastSSD_AVX2 remains available to architecture-neutral tests, but never
// attempts to execute amd64 assembly on other architectures.
func fastSSD_AVX2(a, b []uint8, stride, width, height int) float64 {
	return fastSSD_Scalar(a, b, stride, width, height)
}

// fastSSD_SSE2 remains available to architecture-neutral tests and benchmarks,
// but never attempts to execute amd64 assembly on other architectures.
func fastSSD_SSE2(a, b []uint8, stride, width, height int) float64 {
	return fastSSD_Scalar(a, b, stride, width, height)
}
