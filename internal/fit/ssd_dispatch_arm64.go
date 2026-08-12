//go:build arm64

package fit

import (
	"log/slog"

	"golang.org/x/sys/cpu"
)

func init() {
	if cpu.ARM64.HasASIMD {
		ActiveSSDBackend = SSDBackendNEON
		fastSSD = fastSSD_NEON
		slog.Debug("SSD kernel initialized", "backend", "NEON", "width", "128-bit")
		return
	}

	ActiveSSDBackend = SSDBackendScalar
	fastSSD = fastSSD_Scalar
	slog.Debug("SSD kernel initialized", "backend", "scalar", "reason", "NEON unavailable")
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
