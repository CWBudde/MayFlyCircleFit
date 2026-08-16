//go:build arm64

package renderer

import (
	"golang.org/x/sys/cpu"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

var deltaSSDBackend = "scalar"

// deltaSSDNEONEnabled caches the dispatch gate so the per-call check does not
// re-read the environment opt-out.
var deltaSSDNEONEnabled bool

func init() {
	if fit.SIMDDisabledByEnv() {
		return
	}
	if cpu.ARM64.HasASIMD {
		deltaSSDBackend = "neon"
		deltaSSDNEONEnabled = true
	}
}

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	if pixels < 4 || !deltaSSDNEONEnabled {
		return deltaSSDSpanScalar(candidate, base, reference, pixels)
	}
	return deltaSSDSpanNEON(&candidate[0], &base[0], &reference[0], pixels)
}

// deltaSSDSpanNEON computes an exact signed RGB SSD delta for one NRGBA span.
//
//go:noescape
func deltaSSDSpanNEON(candidate, base, reference *byte, pixels int) int64
