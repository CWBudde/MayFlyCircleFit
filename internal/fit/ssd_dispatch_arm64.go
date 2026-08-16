//go:build arm64

package fit

import "log/slog"

func init() {
	RegisterTierConsumer(installSSDKernel)
}

// installSSDKernel selects the SSD kernel for a tier. See the amd64 twin.
func installSSDKernel(tier SIMDTier) {
	switch tier {
	case TierNEON:
		activeSSDKernel = TierNEON
		fastSSD = fastSSD_NEON
	case TierScalar, TierSSE2, TierAVX2:
		activeSSDKernel = TierScalar
		fastSSD = fastSSD_Scalar
	}
	slog.Debug("SSD kernel installed", "tier", tier, "kernel", activeSSDKernel)
}

// fastSSD_NEON calls the ARM64 assembly kernel. Dispatch must verify ASIMD
// support before selecting this implementation.
func fastSSD_NEON(a, b []uint8, stride, width, height int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return ssdNEON(&a[0], &b[0], stride, width, height)
}

// fastSSD_AVX2 remains available to architecture-neutral tests, but never
// attempts to execute amd64 assembly on ARM64.
func fastSSD_AVX2(a, b []uint8, stride, width, height int) float64 {
	return fastSSD_Scalar(a, b, stride, width, height)
}

// fastSSD_SSE2 remains available to architecture-neutral tests and benchmarks,
// but never attempts to execute amd64 assembly on ARM64.
func fastSSD_SSE2(a, b []uint8, stride, width, height int) float64 {
	return fastSSD_Scalar(a, b, stride, width, height)
}
