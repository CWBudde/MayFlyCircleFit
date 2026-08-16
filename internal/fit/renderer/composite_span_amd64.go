//go:build amd64

package renderer

import "github.com/cwbudde/mayflycirclefit/internal/fit"

// compositeSpanKernel is the tier whose exact span compositor is installed.
//
// Only SSE2 has one here. An AVX2 host therefore still composites scalar: the
// exact AVX2 kernel is a separate change, and adding a tier to this switch
// without its assembly is what produced the broken revision this replaces.
var compositeSpanKernel = fit.TierScalar

func init() {
	fit.RegisterTierConsumer(func(tier fit.SIMDTier) {
		if tier == fit.TierSSE2 {
			compositeSpanKernel = fit.TierSSE2
			return
		}
		compositeSpanKernel = fit.TierScalar
	})
}

// compositeSpanSSE2MinPixels is the span length at which the exact SSE2 kernel
// starts to beat the scalar span.
//
// The kernel pays for widening bytes through dwords into float64 lanes and
// narrowing back, so short spans lose. This is a measured crossover, not the
// vector width, and it comes from a host that genuinely lacks AVX2 rather than
// one masked with GODEBUG=cpu.avx2=off.
//
// The same kernel crosses over at about 8 pixels on a Zen 2 laptop with AVX2
// masked off, where it is also worth far more. That curve is not used: dispatch
// selects SSE2 only when AVX2 is absent, so the masked measurement describes a
// configuration production never enters, and tuning to it would put the cutoff
// two thirds below where the real target machine breaks even. 24 is the first
// length where it does not lose there.
//
// It is deliberately not the ARM64 kernel's 256 either: that one deinterleaves
// with VLD4 and widens in three stages, so it has a much larger setup cost to
// amortize.
const compositeSpanSSE2MinPixels = 24

// exactSpanConstants lays out the five four-lane constant vectors the kernel
// walks, in the order the blend uses them. The SSE2 kernel reads them as ten
// two-lane vectors; the layout is shared so a later AVX2 kernel needs no second
// one.
//
// Lane 3 is alpha and is arithmetically an identity: ((a*1)*1+0)*1+0.5
// truncates back to a for every byte value. Preserving alpha this way rather
// than masking the store keeps the kernel branch-free and makes the property
// testable against arbitrary alpha bytes instead of resting on the canvas being
// opaque.
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

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	if pixels <= 0 {
		return
	}

	if compositeSpanKernel == fit.TierSSE2 && pixels >= compositeSpanSSE2MinPixels {
		constants := exactSpanConstants(r, g, b, alpha)
		vectorPixels := pixels &^ 1
		compositeSpanExactSSE2(&pix[offset], vectorPixels/2, &constants[0])
		offset += vectorPixels * 4
		pixels -= vectorPixels
	}
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	if compositeSpanKernel != fit.TierSSE2 || pixels < compositeSpanSSE2MinPixels {
		// The scalar pair loop interleaves two pixel streams to expose
		// instruction-level parallelism, which is worth more than anything the
		// vector path can add below its crossover.
		compositeOpaqueSpanPairScalar(pix, firstOffset, secondOffset, pixels, r, g, b, alpha)
		return
	}

	// Above the crossover the two rows are independent vector spans, and the
	// constants are shared, so build them once.
	constants := exactSpanConstants(r, g, b, alpha)
	vectorPixels := pixels &^ 1
	compositeSpanExactSSE2(&pix[firstOffset], vectorPixels/2, &constants[0])
	compositeSpanExactSSE2(&pix[secondOffset], vectorPixels/2, &constants[0])
	if vectorPixels < pixels {
		tail := pixels - vectorPixels
		compositeOpaqueSpanPairScalar(pix,
			firstOffset+vectorPixels*4, secondOffset+vectorPixels*4, tail, r, g, b, alpha)
	}
}

// compositeSpanExactSSE2 blends pairs*2 opaque NRGBA pixels in place, byte for
// byte identically to compositeOpaqueSpanScalar.
//
// It is called directly rather than through a function pointer on purpose.
// Routing it indirectly defeats //go:noescape, which moves the 160-byte
// constant block to the heap and adds a malloc per span that costs more than
// the kernel saves. TestCompositeOpaqueSpanDoesNotAllocate pins that.
//
//go:noescape
func compositeSpanExactSSE2(pix *byte, pairs int, constants *float64)
