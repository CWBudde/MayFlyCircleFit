//go:build amd64

package fit

import "log/slog"

func init() {
	RegisterTierConsumer(installSSDKernel)
}

// installSSDKernel selects the SSD kernel for a tier. It is re-run whenever a
// test forces a different tier, so it must be idempotent and must not consult
// the CPU or the environment itself.
func installSSDKernel(tier SIMDTier) {
	switch tier {
	case TierAVX2:
		activeSSDKernel = TierAVX2
		fastSSD = fastSSD_AVX2
	case TierSSE2:
		activeSSDKernel = TierSSE2
		fastSSD = fastSSD_SSE2
	case TierScalar, TierNEON:
		activeSSDKernel = TierScalar
		fastSSD = fastSSD_Scalar
	}
	slog.Debug("SSD kernel installed", "tier", tier, "kernel", activeSSDKernel)
}

// fastSSD_AVX2 calls the amd64 assembly kernel. Dispatch must verify AVX2
// support before selecting this implementation.
func fastSSD_AVX2(a, b []uint8, stride, width, height int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return ssdAVX2(&a[0], &b[0], stride, width, height)
}

// fastSSD_SSE2 calls the baseline SSE2 assembly kernel. It is safe to call on
// any amd64 CPU, which is what lets the parity tests exercise it on an AVX2
// host instead of skipping.
//
// The kernel keeps a row's partial sums in 32-bit lanes, so rows wider than
// ssdSSE2MaxWidth are handed to the scalar kernel rather than risking an
// overflow. That bound is far above any realistic canvas width here.
func fastSSD_SSE2(a, b []uint8, stride, width, height int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if width > ssdSSE2MaxWidth {
		return fastSSD_Scalar(a, b, stride, width, height)
	}
	return ssdSSE2(&a[0], &b[0], stride, width, height)
}
