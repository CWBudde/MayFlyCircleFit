//go:build amd64

package fit

// ssdAVX2 computes sum of squared RGB differences using AVX2 SIMD instructions.
//
// This is a hand-written Plan 9 assembly implementation. It processes 8 pixels
// at a time using 256-bit AVX2 registers and has no C or cgo dependency.
//
// Parameters:
//   - a, b: pointers to RGBA image data (interleaved format: R,G,B,A,R,G,B,A,...)
//   - stride: row stride in bytes (typically width * 4)
//   - width: image width in pixels
//   - height: image height in pixels
//
// Returns:
//   - float64: sum of squared differences for RGB channels only (alpha ignored)
//
// Performance: Targets 4-6× speedup over scalar implementation.
func ssdAVX2(a, b *uint8, stride, width, height int) float64
