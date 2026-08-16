//go:build amd64

package fit

import "log/slog"

func init() {
	RegisterTierConsumer(installSADKernel)
}

// installSADKernel selects the SAD kernel for a tier. Every tier below AVX2
// lands on scalar; see the comment in sad.go for why there is no SSE2 kernel.
func installSADKernel(tier SIMDTier) {
	switch tier {
	case TierAVX2:
		activeSADKernel = TierAVX2
		fastSAD = fastSAD_AVX2
	case TierScalar, TierSSE2, TierNEON:
		activeSADKernel = TierScalar
		fastSAD = fastSAD_Scalar
	}
	slog.Debug("SAD kernel installed", "tier", tier, "kernel", activeSADKernel)
}

// fastSAD_AVX2 calls the amd64 assembly kernel. Dispatch must verify AVX2
// support before selecting this implementation.
func fastSAD_AVX2(a, b []uint8, stride, width, height int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return sadAVX2(&a[0], &b[0], stride, width, height)
}
