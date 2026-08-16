//go:build arm64

package renderer

import (
	"unsafe"

	"golang.org/x/sys/cpu"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

var compositeSpanBackend = "scalar"

// compositeSpanNEONEnabled caches the dispatch gate so the per-span check does
// not re-read the environment opt-out.
var compositeSpanNEONEnabled bool

func init() {
	if fit.SIMDDisabledByEnv() {
		return
	}
	if cpu.ARM64.HasASIMD {
		compositeSpanBackend = "neon"
		compositeSpanNEONEnabled = true
	}
}

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	if pixels <= 0 {
		return
	}

	vectorPixels := 0
	// The exact float64 kernel pays for byte deinterleaving and three stages
	// of widening/narrowing. M5 measurements show that scalar wins on short
	// spans; dispatch NEON only once that setup cost is amortized.
	if compositeSpanNEONEnabled && pixels >= 256 {
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
