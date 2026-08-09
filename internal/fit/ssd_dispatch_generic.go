//go:build !amd64

package fit

import "log/slog"

func init() {
	ActiveSSDBackend = SSDBackendScalar
	fastSSD = fastSSD_Scalar
	slog.Debug("SSD kernel initialized", "backend", "scalar", "reason", "no native SIMD kernel for architecture")
}

// fastSSD_AVX2 remains available to architecture-neutral tests, but never
// attempts to execute amd64 assembly on other architectures.
func fastSSD_AVX2(a, b []uint8, stride, width, height int) float64 {
	return fastSSD_Scalar(a, b, stride, width, height)
}
