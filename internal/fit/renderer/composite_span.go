package renderer

// compositeOpaqueSpanScalar composites pixels from an opaque NRGBA span. The
// arithmetic intentionally matches compositePixel's opaque-destination path.
//
// The float64 conversions are load-bearing and must not be deleted as
// redundant. Go allows an implementation to contract a*b+c into one fused
// multiply-add that rounds once, and the ARM64 backend takes that option while
// amd64 does not, so the same source produced different bytes on the two
// targets. Every explicit conversion here is a rounding point the spec forbids
// contraction from crossing, which is what makes this function architecture-
// independent. Note that fgR/fgG/fgB have to be rounded too: they are products
// themselves, so without the conversion the compiler fuses the outer add
// against r*alpha instead of against bgR*bgBlend.
//
// An earlier version of this comment said the opposite - that keeping each
// multiply-add in one expression was deliberate, so ARM64 would fuse the same
// way the NEON kernel did. That made the scalar path match the kernel at the
// cost of matching neither amd64 nor the correctness oracle, and it is why
// internal/fit/renderer could not run on the ARM64 CI rows. The NEON kernel is
// now unfused to match this function; TestCompositeSpanNEONMatchesScalar pins
// that, and composite_span_fast.go documents the separate opt-in path where
// architecture-dependent rounding is still accepted.
func compositeOpaqueSpanScalar(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	fgR := float64(r * alpha)
	fgG := float64(g * alpha)
	fgB := float64(b * alpha)
	bgBlend := 1 - alpha

	end := offset + pixels*4
	for i := offset; i < end; i += 4 {
		bgR := float64(pix[i+0]) * inv255
		bgG := float64(pix[i+1]) * inv255
		bgB := float64(pix[i+2]) * inv255
		pix[i+0] = uint8(float64((fgR+float64(bgR*bgBlend))*255) + 0.5)
		pix[i+1] = uint8(float64((fgG+float64(bgG*bgBlend))*255) + 0.5)
		pix[i+2] = uint8(float64((fgB+float64(bgB*bgBlend))*255) + 0.5)
	}
}

// compositeOpaqueSpanPairScalar composites two equally sized spans with the
// same source color. Symmetric circle rows have identical coverage, so sharing
// the blend setup and loop control exposes two independent pixel streams to
// the CPU without changing the per-pixel arithmetic.
func compositeOpaqueSpanPairScalar(pix []byte, firstOffset, secondOffset, pixels int, r, g, b, alpha float64) {
	fgR := float64(r * alpha)
	fgG := float64(g * alpha)
	fgB := float64(b * alpha)
	bgBlend := 1 - alpha

	firstEnd := firstOffset + pixels*4

	second := secondOffset
	for first := firstOffset; first < firstEnd; first, second = first+4, second+4 {
		bgR := float64(pix[first+0]) * inv255
		bgG := float64(pix[first+1]) * inv255
		bgB := float64(pix[first+2]) * inv255
		pix[first+0] = uint8(float64((fgR+float64(bgR*bgBlend))*255) + 0.5)
		pix[first+1] = uint8(float64((fgG+float64(bgG*bgBlend))*255) + 0.5)
		pix[first+2] = uint8(float64((fgB+float64(bgB*bgBlend))*255) + 0.5)

		bgR = float64(pix[second+0]) * inv255
		bgG = float64(pix[second+1]) * inv255
		bgB = float64(pix[second+2]) * inv255
		pix[second+0] = uint8(float64((fgR+float64(bgR*bgBlend))*255) + 0.5)
		pix[second+1] = uint8(float64((fgG+float64(bgG*bgBlend))*255) + 0.5)
		pix[second+2] = uint8(float64((fgB+float64(bgB*bgBlend))*255) + 0.5)
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
