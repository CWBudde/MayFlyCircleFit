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
const compositeSpanNEONMinPixels = 256

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	if pixels <= 0 {
		return
	}

	vectorPixels := 0
	if compositeSpanKernel == fit.TierNEON && pixels >= compositeSpanNEONMinPixels {
		vectorPixels = pixels &^ 7
	}
	if vectorPixels != 0 {
		fgR := r * alpha
		fgG := g * alpha
		fgB := b * alpha
		bgBlend := 1 - alpha
		compositeOpaqueSpanNEON(
			unsafe.Pointer(&pix[offset]),
			vectorPixels,
			fgR,
			fgG,
			fgB,
			bgBlend,
		)
		offset += vectorPixels * 4
		pixels -= vectorPixels
	}
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	// Retain the measured NEON crossover and exact assembly kernel on ARM64.
	compositeOpaqueSpan(pix, firstOffset, pixels, r, g, b, alpha)
	compositeOpaqueSpan(pix, secondOffset, pixels, r, g, b, alpha)
}

// compositeOpaqueSpanNEON composites a multiple of eight opaque NRGBA pixels.
//
//go:noescape
func compositeOpaqueSpanNEON(pix unsafe.Pointer, pixels int, fgR, fgG, fgB, bgBlend float64)
