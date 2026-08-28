//go:build arm64

package renderer

import (
	"unsafe"

	"github.com/cwbudde/circlefit/internal/fit"
)

// compositeSpanKernel is the tier whose span compositor is installed.
var compositeSpanKernel = fit.TierScalar

func init() {
	fit.RegisterTierConsumer(func(tier fit.SIMDTier) {
		if tier == fit.TierNEON {
			compositeSpanKernel = fit.TierNEON
			return
		}
		compositeSpanKernel = fit.TierScalar
	})
}

// compositeSpanNEONMinPixels is the span length at which the exact float64 NEON
// kernel starts to beat the scalar span.
//
// The kernel pays for VLD4 byte deinterleaving and three stages of
// widening/narrowing before it composites anything, so short spans lose. 256 is
// the crossover measured on an Apple M5 and is the only measurement behind it:
// it is a single machine's number applied to every ARM64 CPU, which is wrong in
// principle and has not yet been shown to be wrong in practice. Re-measure
// before assuming it transfers to a different ARM implementation.
//
// 256 is now an upper bound rather than the crossover, for the same reason
// compositeSpanSSE2MinPixels is: it was measured with the four blend scalars
// rebuilt per span, and they are now built once per circle. Hoisting only
// removes cost from the vector path, so a post-hoist crossover can only move
// left; leaving the constant where it is keeps some spans on the scalar path
// that the kernel could now win, and regresses nothing. Re-deriving it needs
// ARM64 benchmarking hardware. internal/fit/renderer does now run on the ARM64
// rows of ci-native-simd.yml, and qemu-aarch64-static runs the cross-compiled
// test binary locally, but both establish correctness only - an emulated timing
// is not a throughput measurement, and neither runner is the Apple M5 that
// produced 256. BenchmarkCompositeOpaqueSpanNEONCutoff is the command that
// re-derives it. See docs/exact-span-compositors.md.
const compositeSpanNEONMinPixels = 256

// spanBlend holds the four blend scalars the NEON kernel takes as arguments.
// They are a pure function of the circle's colour and opacity, so the
// per-circle caller builds them once and every span of every row reads them,
// instead of each span recomputing three multiplies and a subtract.
//
// It deliberately carries neither the colour nor the tier, exactly as the amd64
// constant block does. The colour keeps its own route to
// compositeOpaqueSpanScalar, so the scalar fallback's arithmetic is visibly
// untouched by the hoist; the tier and its cutoff must stay read at call time,
// because a snapshot taken when the circle started would survive a tier change
// that every dispatch site is required to follow.
type spanBlend struct {
	fgR, fgG, fgB, bgBlend float64
}

// newSpanBlend derives the blend scalars for one circle's colour and opacity.
//
// The three products are the same expressions compositeOpaqueSpanScalar
// computes, so the kernel and its scalar reference still start from bit-identical
// foregrounds. Nothing here may be reassociated: composite_span.go documents why
// the premultiplied foreground has to be a rounding point of its own.
func newSpanBlend(r, g, b, alpha float64) spanBlend {
	return spanBlend{
		fgR:     r * alpha,
		fgG:     g * alpha,
		fgB:     b * alpha,
		bgBlend: 1 - alpha,
	}
}

func compositeOpaqueSpan(blend *spanBlend, pix []byte, offset, pixels int, r, g, b, alpha float64) {
	if pixels <= 0 {
		return
	}

	vectorPixels := 0
	if compositeSpanKernel == fit.TierNEON && pixels >= compositeSpanNEONMinPixels {
		vectorPixels = pixels &^ 7
	}
	if vectorPixels != 0 {
		compositeOpaqueSpanNEON(
			unsafe.Pointer(&pix[offset]),
			vectorPixels,
			blend.fgR,
			blend.fgG,
			blend.fgB,
			blend.bgBlend,
		)
		offset += vectorPixels * 4
		pixels -= vectorPixels
	}
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(
	blend *spanBlend,
	pix []byte,
	firstOffset, secondOffset, pixels int,
	r, g, b, alpha float64,
) {
	// Retain the measured NEON crossover and exact assembly kernel on ARM64.
	compositeOpaqueSpan(blend, pix, firstOffset, pixels, r, g, b, alpha)
	compositeOpaqueSpan(blend, pix, secondOffset, pixels, r, g, b, alpha)
}

// compositeOpaqueSpanNEON composites a multiple of eight opaque NRGBA pixels.
//
//go:noescape
func compositeOpaqueSpanNEON(pix unsafe.Pointer, pixels int, fgR, fgG, fgB, bgBlend float64)
