//go:build arm64

package fit

// ssdNEON computes the sum of squared RGB differences using ARM64 NEON.
//
// The hand-written Plan 9 assembly kernel processes four interleaved NRGBA
// pixels per iteration. Alpha bytes are masked before widening and squaring.
// Scalar code handles the final zero to three pixels in each row.
//
// The implementation has no C, cgo, or transpiler dependency.
func ssdNEON(a, b *uint8, stride, width, height int) float64
