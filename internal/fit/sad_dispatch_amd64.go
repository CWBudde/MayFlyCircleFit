//go:build amd64

package fit

import (
	"log/slog"

	"golang.org/x/sys/cpu"
)

func init() {
	if cpu.X86.HasAVX2 {
		ActiveSADBackend = SADBackendAVX2
		fastSAD = fastSAD_AVX2
		slog.Debug("SAD kernel initialized", "backend", "AVX2", "instruction", "VPSADBW")
		return
	}

	ActiveSADBackend = SADBackendScalar
	fastSAD = fastSAD_Scalar
	slog.Debug("SAD kernel initialized", "backend", "scalar", "reason", "AVX2 unavailable")
}

// fastSAD_AVX2 calls the amd64 assembly kernel. Dispatch must verify AVX2
// support before selecting this implementation.
func fastSAD_AVX2(a, b []uint8, stride, width, height int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	return sadAVX2(&a[0], &b[0], stride, width, height)
}
