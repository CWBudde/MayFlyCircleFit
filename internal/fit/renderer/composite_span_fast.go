package renderer

// The fast compositor rewrites the default blend so no per-channel
// deinterleaving is needed.
//
// The exact path computes, per channel:
//
//	out = (fg + (pix/255)*bgBlend)*255 + 0.5
//
// Distributing the 255 removes the reciprocal entirely:
//
//	out = (fg*255 + 0.5) + pix*bgBlend
//
// so a whole pixel is one multiply-add against two constant vectors. Laid out
// over an interleaved NRGBA span, four float32 lanes hold exactly one pixel's
// R, G, B and A, which is why the SIMD kernels need no shuffles. Giving alpha a
// multiplier of 1 and an addend of 0 passes it through unchanged.
//
// This regrouping and the drop to float32 make the result accurate to +/-1 per
// channel rather than byte-identical to compositeOpaqueSpanScalar, which is why
// the whole path is opt-in.

// fastSpanConstants returns the per-lane addend and multiplier described above.
func fastSpanConstants(r, g, b, alpha float64) (addR, addG, addB, mul float32) {
	bgBlend := float32(1 - alpha)
	return float32(r*alpha)*255 + 0.5,
		float32(g*alpha)*255 + 0.5,
		float32(b*alpha)*255 + 0.5,
		bgBlend
}

// compositeOpaqueSpanFastScalar is the float32 reference: the oracle the SIMD
// kernels must match exactly, and the fallback for spans shorter than a vector
// batch.
//
// It is not portable, and must not be described as such. Go's arm64 backend
// contracts the multiply-add on line 40 into FMADDS while its amd64 backend
// emits MULSS plus ADDSS, so this function produces different bytes on the two
// architectures this repository targets. The amd64 kernels match it only
// because they are also unfused; folding their VMULPS and VADDPS into
// VFMADD231PS - the obvious optimisation - would silently break parity, and the
// symptom would look like a precision artifact rather than a defect. The exact
// float64 path has the mirror-image dependency and documents it in
// composite_span.go; TestCompositeSpanExactFusionContract pins the amd64 half.
func compositeOpaqueSpanFastScalar(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	addR, addG, addB, mul := fastSpanConstants(r, g, b, alpha)

	end := offset + pixels*4
	for i := offset; i < end; i += 4 {
		pix[i+0] = uint8(addR + float32(pix[i+0])*mul)
		pix[i+1] = uint8(addG + float32(pix[i+1])*mul)
		pix[i+2] = uint8(addB + float32(pix[i+2])*mul)
	}
}
