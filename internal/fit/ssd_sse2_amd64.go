//go:build amd64

package fit

// ssdSSE2 computes sum of squared RGB differences using baseline SSE2
// instructions.
//
// This is a hand-written Plan 9 assembly implementation. It processes 4 pixels
// at a time using 128-bit XMM registers and has no C or cgo dependency. It is
// the fallback for amd64 CPUs without AVX2.
//
// Parameters:
//   - a, b: pointers to RGBA image data (interleaved format: R,G,B,A,R,G,B,A,...)
//   - stride: row stride in bytes (typically width * 4)
//   - width: image width in pixels, which must not exceed ssdSSE2MaxWidth
//   - height: image height in pixels
//
// Returns:
//   - float64: sum of squared differences for RGB channels only (alpha ignored)
func ssdSSE2(a, b *uint8, stride, width, height int) float64

// ssdSSE2MaxWidth is the widest row the SSE2 kernel may process. The kernel
// accumulates PMADDWD results as int32 for a whole row and widens to int64 once
// per row, which is the source of its speedup over a per-iteration widening.
// Wider rows are routed to the scalar kernel by fastSSD_SSE2 instead.
//
// The binding limit is per lane, not per row. PMADDWD pairwise-adds the widened
// R,G,B,0 words, so one lane accumulates R^2+G^2 and its neighbour accumulates
// B^2+0; the busiest lane therefore carries at most width*2*255*255 =
// width*130050 and first exceeds 2^31 at width 16512. The row *total*
// width*3*65025 crosses 2^31 earlier, at width 11009, but no lane ever holds
// the row total, so that figure is not the constraint.
//
// 11000 is kept anyway, as a deliberate ~1.5x margin below the real lane bound
// and just under the row-total figure. It is far above any canvas this program
// produces, so nothing is lost by being conservative, and the cliff into the
// scalar kernel is exercised by TestFastSSD_SSE2MaxWidthDispatchBoundary rather
// than left to arithmetic.
//
// Note the asymmetry this creates: ssdAVX2 widens per iteration and has no
// width limit at all, so the two kernels behind the same fastSSD pointer have
// different domains. fastSSD_SSE2 is the only place that difference is visible.
const ssdSSE2MaxWidth = 11000
