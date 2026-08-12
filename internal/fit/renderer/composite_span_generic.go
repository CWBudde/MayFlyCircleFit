//go:build !arm64

package renderer

const compositeSpanBackend = "scalar"

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}
