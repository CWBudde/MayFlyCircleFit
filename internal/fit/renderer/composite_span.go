package renderer

// compositeOpaqueSpanScalar composites pixels from an opaque NRGBA span. The
// arithmetic intentionally matches compositePixel's opaque-destination path;
// in particular, keeping each multiply-add in one expression lets ARM64 use
// the same fused operations as the NEON implementation.
func compositeOpaqueSpanScalar(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	fgR := r * alpha
	fgG := g * alpha
	fgB := b * alpha
	bgBlend := 1 - alpha

	end := offset + pixels*4
	for i := offset; i < end; i += 4 {
		bgR := float64(pix[i+0]) * inv255
		bgG := float64(pix[i+1]) * inv255
		bgB := float64(pix[i+2]) * inv255
		pix[i+0] = uint8((fgR+bgR*bgBlend)*255 + 0.5)
		pix[i+1] = uint8((fgG+bgG*bgBlend)*255 + 0.5)
		pix[i+2] = uint8((fgB+bgB*bgBlend)*255 + 0.5)
	}
}

func pixelsAreOpaque(pix []byte) bool {
	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 255 {
			return false
		}
	}
	return true
}
