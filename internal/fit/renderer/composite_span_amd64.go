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
// machine can produce both: AVX2 on a Ryzen 5 4600H, SSE2 on a host that
// genuinely lacks AVX2 rather than one masked with GODEBUG=cpu.avx2=off.
//
// The SSE2 crossover is deliberately taken from the slower of the two machines
// that can run it. The same kernel crosses over at about 8 pixels on the Zen 2
// laptop with AVX2 masked off and only at about 20 on the no-AVX2 host, where
// it is worth far less overall. Since dispatch selects SSE2 only when AVX2 is
// absent, the masked Zen 2 measurement describes a configuration production
// never reaches, and the honest constant is the one from the machine that will
// actually run this code. 24 is the first length where it does not lose there.
//
// Neither is the ARM64 kernel's 256: that one deinterleaves with VLD4 and
// widens in three stages, so it has a much larger setup cost to amortize.
//
// See docs/exact-span-compositors.md.
const (
	compositeSpanAVX2MinPixels = 16
	compositeSpanSSE2MinPixels = 24
)

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

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	if pixels <= 0 {
		return
	}

	vectorPixels := 0
	if compositeSpanKernel != fit.TierScalar && pixels >= compositeSpanMinPixels {
		vectorPixels = pixels &^ 1
	}

	if vectorPixels != 0 {
		constants := exactSpanConstants(r, g, b, alpha)
		compositeSpanExact(&pix[offset], vectorPixels/2, &constants[0])
		offset += vectorPixels * 4
		pixels -= vectorPixels
	}

	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	if compositeSpanKernel == fit.TierScalar || pixels < compositeSpanMinPixels {
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
	compositeSpanExact(&pix[firstOffset], vectorPixels/2, &constants[0])
	compositeSpanExact(&pix[secondOffset], vectorPixels/2, &constants[0])

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
