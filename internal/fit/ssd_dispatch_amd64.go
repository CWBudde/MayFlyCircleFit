//go:build amd64

package fit

import (
	"log/slog"

	"golang.org/x/sys/cpu"
)

func init() {
	if SIMDDisabledByEnv() {
		ActiveSSDBackend = SSDBackendScalar
		fastSSD = fastSSD_Scalar
		slog.Debug("SSD kernel initialized", "backend", "scalar", "reason", simdDisableEnv)
		return
	}

	if cpu.X86.HasAVX2 {
		ActiveSSDBackend = SSDBackendAVX2
		fastSSD = fastSSD_AVX2
		slog.Debug("SSD kernel initialized", "backend", "AVX2", "width", "256-bit")
		return
	}

	if cpu.X86.HasSSE2 {
		ActiveSSDBackend = SSDBackendSSE2
		fastSSD = fastSSD_SSE2
		slog.Debug("SSD kernel initialized", "backend", "SSE2", "width", "128-bit")
		return
	}

	ActiveSSDBackend = SSDBackendScalar
	fastSSD = fastSSD_Scalar
	slog.Debug("SSD kernel initialized", "backend", "scalar", "reason", "AVX2 and SSE2 unavailable")
}

// fastSSD_AVX2 calls the amd64 assembly kernel. Dispatch must verify AVX2
// support before selecting this implementation.
func fastSSD_AVX2(a, b []uint8, stride, width, height int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return ssdAVX2(&a[0], &b[0], stride, width, height)
}

// fastSSD_SSE2 calls the baseline SSE2 assembly kernel. Dispatch must verify
// SSE2 support before selecting this implementation.
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
