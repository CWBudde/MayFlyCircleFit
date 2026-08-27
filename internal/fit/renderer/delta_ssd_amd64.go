//go:build amd64

package renderer

import (
	"unsafe"

	"github.com/cwbudde/circlefit/internal/fit"
)

// deltaSSDKernel is the tier whose kernels deltaSSDSpan may use. It is set from
// the tier consumer and read once per span, so the per-call cost is a single
// load rather than a chain of feature checks.
var deltaSSDKernel = fit.TierScalar

func init() {
	fit.RegisterTierConsumer(func(tier fit.SIMDTier) {
		switch tier {
		case fit.TierAVX2, fit.TierSSE2:
			deltaSSDKernel = tier
		case fit.TierScalar, fit.TierNEON:
			deltaSSDKernel = fit.TierScalar
		}
	})
}

// deltaSSDSpan dispatches down the tier ladder rather than to a single width.
// An AVX2 host still uses the SSE2 kernel for 4-to-7-pixel spans: the AVX2
// kernel needs eight, and falling straight to scalar there left the shortest
// vectorizable spans on the table.
func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	if deltaSSDKernel == fit.TierAVX2 && pixels >= 8 {
		return deltaSSDSpanAVX2(&candidate[0], &base[0], &reference[0], pixels)
	}

	if deltaSSDKernel != fit.TierScalar && pixels >= 4 {
		return deltaSSDSpanSSE2(&candidate[0], &base[0], &reference[0], pixels)
	}

	return deltaSSDSpanScalar(candidate, base, reference, pixels)
}

// deltaSSDSpanAVX2 computes an exact signed RGB SSD delta for one NRGBA span.
//
//go:noescape
func deltaSSDSpanAVX2(candidate, base, reference *byte, pixels int) int64

// deltaSSDSSE2MaxPixels is the longest span deltaSSDSpanSSE2Kernel may be given
// in one call. Its int32 lanes carry at most pixels*130050 and overflow at
// 16512 pixels; 8192 keeps a 2x margin and is still 2048 vector iterations, so
// the once-per-call widening is amortized just as thoroughly.
//
// Spans this long do not occur at any canvas size this program is used at, but
// app.MaxImagePixels permits a 16-million-pixel-wide image, so the bound has to
// be enforced rather than assumed.
const deltaSSDSSE2MaxPixels = 8192

// deltaSSDSpanSSE2 computes the same exact signed RGB SSD delta as
// deltaSSDSpanAVX2 using only SSE2, four pixels per vector iteration.
//
// It splits the span so the kernel's int32 accumulators cannot overflow, and
// sums the chunks in int64. Spans up to deltaSSDSSE2MaxPixels - every span this
// program actually produces - take exactly one iteration.
func deltaSSDSpanSSE2(candidate, base, reference *byte, pixels int) int64 {
	// The chunking loop is deliberately not inlined into this path. Measured on
	// a Ryzen 5 4600H, folding it in here cost 32% at four pixels - the length
	// circle edges produce constantly - for a case that no canvas this program
	// renders will ever reach.
	if pixels > deltaSSDSSE2MaxPixels {
		return deltaSSDSpanSSE2Chunked(candidate, base, reference, pixels)
	}

	return deltaSSDSpanSSE2Kernel(candidate, base, reference, pixels)
}

// deltaSSDSpanSSE2Chunked handles spans longer than the kernel's accumulators
// can hold, by splitting them and summing the parts in int64.
func deltaSSDSpanSSE2Chunked(candidate, base, reference *byte, pixels int) int64 {
	var total int64

	for pixels > 0 {
		chunk := min(pixels, deltaSSDSSE2MaxPixels)
		total += deltaSSDSpanSSE2Kernel(candidate, base, reference, chunk)

		pixels -= chunk
		if pixels == 0 {
			break
		}

		advance := chunk * 4
		candidate = (*byte)(unsafe.Add(unsafe.Pointer(candidate), advance))
		base = (*byte)(unsafe.Add(unsafe.Pointer(base), advance))
		reference = (*byte)(unsafe.Add(unsafe.Pointer(reference), advance))
	}

	return total
}

// deltaSSDSpanSSE2Kernel is the assembly kernel. It requires
// pixels <= deltaSSDSSE2MaxPixels; call deltaSSDSpanSSE2 instead.
//
//go:noescape
func deltaSSDSpanSSE2Kernel(candidate, base, reference *byte, pixels int) int64
