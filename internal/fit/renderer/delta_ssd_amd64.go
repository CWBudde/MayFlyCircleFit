//go:build amd64

package renderer

import (
	"golang.org/x/sys/cpu"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

var deltaSSDBackend = "scalar"

// deltaSSDAVX2Enabled and deltaSSDSSE2Enabled cache the dispatch gates so the
// per-call check does not re-read the environment opt-out.
var (
	deltaSSDAVX2Enabled bool
	deltaSSDSSE2Enabled bool
)

func init() {
	if fit.SIMDDisabledByEnv() {
		return
	}
	switch {
	case cpu.X86.HasAVX2:
		deltaSSDBackend = "avx2"
		deltaSSDAVX2Enabled = true
	case cpu.X86.HasSSE2:
		// SSE2 is architecturally guaranteed on amd64; the feature check keeps
		// dispatch uniform and honours GODEBUG=cpu.sse2=off style overrides.
		deltaSSDBackend = "sse2"
		deltaSSDSSE2Enabled = true
	}
}

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	switch {
	case deltaSSDAVX2Enabled && pixels >= 8:
		return deltaSSDSpanAVX2(&candidate[0], &base[0], &reference[0], pixels)
	case deltaSSDSSE2Enabled && pixels >= 4:
		return deltaSSDSpanSSE2(&candidate[0], &base[0], &reference[0], pixels)
	default:
		return deltaSSDSpanScalar(candidate, base, reference, pixels)
	}
}

// deltaSSDSpanAVX2 computes an exact signed RGB SSD delta for one NRGBA span.
//
//go:noescape
func deltaSSDSpanAVX2(candidate, base, reference *byte, pixels int) int64

// deltaSSDSpanSSE2 computes the same exact signed RGB SSD delta as
// deltaSSDSpanAVX2 using only SSE2, four pixels per vector iteration.
//
//go:noescape
func deltaSSDSpanSSE2(candidate, base, reference *byte, pixels int) int64
