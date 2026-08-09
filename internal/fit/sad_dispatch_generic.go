//go:build !amd64

package fit

import "log/slog"

func init() {
	ActiveSADBackend = SADBackendScalar
	fastSAD = fastSAD_Scalar
	slog.Debug("SAD kernel initialized", "backend", "scalar", "reason", "no native SIMD kernel for architecture")
}

// fastSAD_AVX2 remains available to architecture-neutral tests, but never
// attempts to execute amd64 assembly on other architectures.
func fastSAD_AVX2(a, b []uint8, stride, width, height int) float64 {
	return fastSAD_Scalar(a, b, stride, width, height)
}
