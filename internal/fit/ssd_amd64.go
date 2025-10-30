// Code generated from prototypes/ssd_avx2.c - DO NOT EDIT

//go:build amd64

package fit

// ssdAVX2 computes sum of squared RGB differences using AVX2 SIMD instructions.
//
// This is a hand-written Plan9 assembly implementation based on the C prototype
// in prototypes/ssd_avx2.c. It processes 8 pixels at a time using 256-bit AVX2
// registers for improved performance over the scalar baseline.
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
