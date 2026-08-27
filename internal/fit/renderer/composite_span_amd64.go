//go:build amd64

package renderer

import "github.com/cwbudde/circlefit/internal/fit"

// compositeSpanKernel is the tier whose exact span compositor is installed, and
// compositeSpanMinPixels is that kernel's measured crossover against the scalar
// span. They are set together so the two can never disagree.
var (
	compositeSpanKernel    = fit.TierScalar
	compositeSpanMinPixels = 0
)

func init() {
	fit.RegisterTierConsumer(func(tier fit.SIMDTier) {
		switch tier {
		case fit.TierAVX2:
			compositeSpanKernel, compositeSpanMinPixels = fit.TierAVX2, compositeSpanAVX2MinPixels
		case fit.TierSSE2:
			compositeSpanKernel, compositeSpanMinPixels = fit.TierSSE2, compositeSpanSSE2MinPixels
		default:
			compositeSpanKernel, compositeSpanMinPixels = fit.TierScalar, 0
		}
	})
}

// compositeSpanAVX2MinPixels and compositeSpanSSE2MinPixels are the span
// lengths at which each exact kernel starts to beat the scalar span.
//
// Both kernels pay for widening bytes through dwords into float64 lanes and
// narrowing back, so short spans lose. These are measured crossovers, not
// vector widths, and they come from different machines because no single
// machine can produce both.
//
// Both are measured with the constant block hoisted, because that is what the
// dispatcher pays for now: the caller builds the twenty float64s once per circle
// and every span of every row reads them. The pre-hoist numbers were 16 and 24.
//
// AVX2 is 6, from an i7-1255U (Alder Lake-P), median of nine 500 ms runs at
// GOMAXPROCS=1, pinned to one core and measured on both core types because the
// part is hybrid and Gracemont splits a 256-bit operation into two 128-bit uops.
// The kernel has a floor of about 11.5 ns on the P-core that barely moves from 4
// to 6 pixels while the scalar span grows past it, so 4 still loses there
// (0.97x) while 6 wins on both core types (1.26x P, 1.40x E). Where the two
// cores disagree the larger length is taken: dispatch cannot know which one it
// landed on, and a cutoff set too high only leaves some spans on scalar, while
// one set too low loses on every span in the gap. Note that the provenance
// changed with this measurement - the previous 16 came from a Ryzen 5 4600H, so
// this is a differently sourced single-machine constant, not a better-sourced
// one.
//
// SSE2 is still 24, and 24 is now an upper bound rather than the crossover.
// Hoisting only removes cost from the vector path, so a post-hoist crossover can
// only move left; leaving the constant where it is keeps some spans on scalar
// that the kernel could now win, and regresses nothing. It is not re-derived
// here because dispatch selects SSE2 only when AVX2 is absent, and this host has
// AVX2: neither CIRCLEFIT_SIMD_TIER=sse2 nor GODEBUG=cpu.avx2=off changes the
// microarchitecture, and the same kernel is already recorded as crossing at
// about 8 pixels on an AVX2-masked Zen 2 against about 24 on a host that
// genuinely lacks AVX2. Re-deriving it needs that second class of host. The
// setup this hoist removes measures 6 to 7 ns per span for the SSE2 kernel on
// both core types here, which is the estimate of how far left it should move,
// not a constant to ship.
//
// Neither is the ARM64 kernel's 256: that one deinterleaves with VLD4 and
// widens in three stages, so it has a much larger setup cost to amortize, and it
// has not been re-derived because ARM64 has not been hoisted.
//
// See docs/exact-span-compositors.md.
const (
	compositeSpanAVX2MinPixels = 6
	compositeSpanSSE2MinPixels = 24
)

// spanBlend holds the vector state that is invariant for a whole circle. It is
// built once by the per-circle caller and passed down by pointer, so the twenty
// float64s below are stored once per circle instead of once per span of every
// row.
//
// It deliberately carries neither the colour nor the tier. The colour keeps its
// own route to compositeOpaqueSpanScalar, so the scalar fallback's arithmetic is
// visibly untouched by the hoist; the tier and its cutoff must stay read at call
// time, because a snapshot taken when the circle started would survive a tier
// change that every dispatch site is required to follow.
//
// On architectures without an exact vector span compositor this struct is empty,
// which is what lets the row walkers in renderer_cpu.go and polish_dirty_cost.go
// stay architecture-neutral.
type spanBlend struct {
	constants [20]float64
}

// newSpanBlend derives the constant block for one circle's colour and opacity.
func newSpanBlend(r, g, b, alpha float64) spanBlend {
	return spanBlend{constants: exactSpanConstants(r, g, b, alpha)}
}

// exactSpanConstants lays out the five four-lane constant vectors the kernel
// walks, in the order the blend uses them.
//
// Lane 3 is alpha and is arithmetically an identity: (a*1)*1+0)*1+0.5 truncates
// back to a for every byte value. Preserving alpha this way rather than masking
// the store keeps the kernel branch-free and makes the property testable
// against arbitrary alpha bytes instead of resting on the canvas being opaque.
func exactSpanConstants(r, g, b, alpha float64) [20]float64 {
	bgBlend := 1 - alpha

	return [20]float64{
		inv255, inv255, inv255, 1,
		bgBlend, bgBlend, bgBlend, 1,
		r * alpha, g * alpha, b * alpha, 0,
		255, 255, 255, 1,
		0.5, 0.5, 0.5, 0.5,
	}
}

// compositeSpanExact runs the installed kernel over pairs*2 pixels. The switch
// is a direct call rather than a function pointer so the argument stays
// non-escaping; both kernels take the same arguments and the same constant
// block, so the only thing that varies is which one runs.
func compositeSpanExact(pix *byte, pairs int, constants *float64) {
	if compositeSpanKernel == fit.TierAVX2 {
		compositeSpanExactAVX2(pix, pairs, constants)
		return
	}

	compositeSpanExactSSE2(pix, pairs, constants)
}

func compositeOpaqueSpan(blend *spanBlend, pix []byte, offset, pixels int, r, g, b, alpha float64) {
	if pixels <= 0 {
		return
	}

	vectorPixels := 0
	if compositeSpanKernel != fit.TierScalar && pixels >= compositeSpanMinPixels {
		vectorPixels = pixels &^ 1
	}

	if vectorPixels != 0 {
		compositeSpanExact(&pix[offset], vectorPixels/2, &blend.constants[0])
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
	if compositeSpanKernel == fit.TierScalar || pixels < compositeSpanMinPixels {
		// The scalar pair loop interleaves two pixel streams to expose
		// instruction-level parallelism, which is worth more than anything the
		// vector path can add below its crossover.
		compositeOpaqueSpanPairScalar(pix, firstOffset, secondOffset, pixels, r, g, b, alpha)
		return
	}

	// Above the crossover the two rows are independent vector spans sharing one
	// constant block, which the caller already built for the whole circle.
	vectorPixels := pixels &^ 1
	compositeSpanExact(&pix[firstOffset], vectorPixels/2, &blend.constants[0])
	compositeSpanExact(&pix[secondOffset], vectorPixels/2, &blend.constants[0])

	if vectorPixels < pixels {
		tail := (pixels - vectorPixels)
		compositeOpaqueSpanPairScalar(pix,
			firstOffset+vectorPixels*4, secondOffset+vectorPixels*4, tail, r, g, b, alpha)
	}
}

// compositeSpanExactAVX2 and compositeSpanExactSSE2 blend pairs*2 opaque NRGBA
// pixels in place, byte for byte identically to compositeOpaqueSpanScalar and
// to each other.
//
//go:noescape
func compositeSpanExactAVX2(pix *byte, pairs int, constants *float64)

//go:noescape
func compositeSpanExactSSE2(pix *byte, pairs int, constants *float64)
