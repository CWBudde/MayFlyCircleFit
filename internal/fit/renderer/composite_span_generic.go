//go:build !arm64

package renderer

const compositeSpanBackend = "scalar"

func compositeOpaqueSpan(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanScalar(pix, offset, pixels, r, g, b, alpha)
}

func compositeOpaqueSpanPair(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanPairScalar(pix, firstOffset, secondOffset, pixels, r, g, b, alpha)
}
