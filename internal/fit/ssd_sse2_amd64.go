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
// A row's maximum value is width*3*255*255, so the 32-bit lanes stay exact up
// to 11000 pixels (11000*3*65025 = 2145825000 < 2^31). Wider rows are routed to
// the scalar kernel by fastSSD_SSE2 instead of being widened per iteration.
const ssdSSE2MaxWidth = 11000
