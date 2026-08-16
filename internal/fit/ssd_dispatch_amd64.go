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

	ActiveSSDBackend = SSDBackendScalar
	fastSSD = fastSSD_Scalar
	slog.Debug("SSD kernel initialized", "backend", "scalar", "reason", "AVX2 unavailable")
}

// fastSSD_AVX2 calls the amd64 assembly kernel. Dispatch must verify AVX2
// support before selecting this implementation.
func fastSSD_AVX2(a, b []uint8, stride, width, height int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return ssdAVX2(&a[0], &b[0], stride, width, height)
}
