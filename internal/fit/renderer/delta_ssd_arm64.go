//go:build arm64

package renderer

import "golang.org/x/sys/cpu"

var deltaSSDBackend = "scalar"

func init() {
	if cpu.ARM64.HasASIMD {
		deltaSSDBackend = "neon"
	}
}

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	if pixels < 4 || !cpu.ARM64.HasASIMD {
		return deltaSSDSpanScalar(candidate, base, reference, pixels)
	}
	return deltaSSDSpanNEON(&candidate[0], &base[0], &reference[0], pixels)
}

// deltaSSDSpanNEON computes an exact signed RGB SSD delta for one NRGBA span.
//
//go:noescape
func deltaSSDSpanNEON(candidate, base, reference *byte, pixels int) int64
