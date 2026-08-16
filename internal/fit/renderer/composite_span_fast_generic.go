//go:build !amd64

package renderer

const fastCompositeBackend = "scalar"

func compositeOpaqueSpanFast(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	compositeOpaqueSpanFastScalar(pix, offset, pixels, r, g, b, alpha)
}
