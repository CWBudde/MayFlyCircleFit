//go:build amd64

package renderer

import (
	"golang.org/x/sys/cpu"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

var deltaSSDBackend = "scalar"

// deltaSSDAVX2Enabled caches the AVX2 gate so the per-call check does not
// re-read the environment opt-out.
var deltaSSDAVX2Enabled bool

func init() {
	if fit.SIMDDisabledByEnv() {
		return
	}
	if cpu.X86.HasAVX2 {
		deltaSSDBackend = "avx2"
		deltaSSDAVX2Enabled = true
	}
}

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	if pixels < 8 || !deltaSSDAVX2Enabled {
		return deltaSSDSpanScalar(candidate, base, reference, pixels)
	}
	return deltaSSDSpanAVX2(&candidate[0], &base[0], &reference[0], pixels)
}

// deltaSSDSpanAVX2 computes an exact signed RGB SSD delta for one NRGBA span.
//
//go:noescape
func deltaSSDSpanAVX2(candidate, base, reference *byte, pixels int) int64
