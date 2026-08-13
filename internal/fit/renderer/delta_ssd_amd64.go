//go:build amd64

package renderer

import "golang.org/x/sys/cpu"

var deltaSSDBackend = "scalar"

func init() {
	if cpu.X86.HasAVX2 {
		deltaSSDBackend = "avx2"
	}
}

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	if pixels < 8 || !cpu.X86.HasAVX2 {
		return deltaSSDSpanScalar(candidate, base, reference, pixels)
	}
	return deltaSSDSpanAVX2(&candidate[0], &base[0], &reference[0], pixels)
}

// deltaSSDSpanAVX2 computes an exact signed RGB SSD delta for one NRGBA span.
//
//go:noescape
func deltaSSDSpanAVX2(candidate, base, reference *byte, pixels int) int64
