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

// compositeOpaqueSpanPairScalar composites two equally sized spans with the
// same source color. Symmetric circle rows have identical coverage, so sharing
// the blend setup and loop control exposes two independent pixel streams to
// the CPU without changing the per-pixel arithmetic.
func compositeOpaqueSpanPairScalar(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	fgR := r * alpha
	fgG := g * alpha
	fgB := b * alpha
	bgBlend := 1 - alpha

	firstEnd := firstOffset + pixels*4

	second := secondOffset
	for first := firstOffset; first < firstEnd; first, second = first+4, second+4 {
		bgR := float64(pix[first+0]) * inv255
		bgG := float64(pix[first+1]) * inv255
		bgB := float64(pix[first+2]) * inv255
		pix[first+0] = uint8((fgR+bgR*bgBlend)*255 + 0.5)
		pix[first+1] = uint8((fgG+bgG*bgBlend)*255 + 0.5)
		pix[first+2] = uint8((fgB+bgB*bgBlend)*255 + 0.5)

		bgR = float64(pix[second+0]) * inv255
		bgG = float64(pix[second+1]) * inv255
		bgB = float64(pix[second+2]) * inv255
		pix[second+0] = uint8((fgR+bgR*bgBlend)*255 + 0.5)
		pix[second+1] = uint8((fgG+bgG*bgBlend)*255 + 0.5)
		pix[second+2] = uint8((fgB+bgB*bgBlend)*255 + 0.5)
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
