// Code generated for SAD with quadratic weighting - DO NOT EDIT

//go:build amd64

package fit

// sadAVX2 computes SAD with quadratic weighting using AVX2 instructions.
//
// This matches the Delphi ErrorWeightingLoopSSE/MMX functions using the
// VPSADBW instruction (AVX2 version of PSADBW).
//
// Algorithm:
//   For each pixel:
//     value = |R1-R2| + |G1-G2| + |B1-B2|  (computed via VPSADBW or manually)
//     cost = scale × value × (255 + 9×value)
//
// The VPSADBW instruction computes 8 absolute differences and horizontal sums
// in a single instruction, making this significantly faster than the SSD approach.
//
// Parameters:
//   - a, b: pointers to RGBA image data
//   - stride: row stride in bytes
//   - width: image width in pixels
//   - height: image height in pixels
//
// Returns:
//   - float64: total weighted cost (scaled)
func sadAVX2(a, b *uint8, stride, width, height int) float64
