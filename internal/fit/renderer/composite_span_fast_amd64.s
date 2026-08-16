// Reduced-precision float32 span compositors for opaque NRGBA canvases.
//
// Both kernels evaluate out = addend + pix*multiplier per channel, where the
// caller supplies addend and multiplier as repeating four-lane groups laid out
// [R, G, B, A]. Because NRGBA is interleaved, one four-lane float32 group is
// exactly one pixel, so no channel deinterleaving is required. Alpha passes
// through unchanged via a multiplier of 1 and an addend of 0.
//
// Conversion back to bytes uses truncation (CVTTPS2PL / VCVTTPS2DQ) to match
// Go's uint8() conversion, and saturating packs, which is safe because the
// blend keeps every result inside [0, 255.5).
//
// Callers pass only whole vector batches; short spans and tails stay in Go.

#include "textflag.h"

// VPERMD index restoring pixel order after the two lane-wise AVX2 packs.
// The packs leave [p0 p2 p4 p6 | p1 p3 p5 p7]; each pixel is one dword.
DATA ·fastSpanPermute<>+0(SB)/4, $0
DATA ·fastSpanPermute<>+4(SB)/4, $4
DATA ·fastSpanPermute<>+8(SB)/4, $1
DATA ·fastSpanPermute<>+12(SB)/4, $5
DATA ·fastSpanPermute<>+16(SB)/4, $2
DATA ·fastSpanPermute<>+20(SB)/4, $6
DATA ·fastSpanPermute<>+24(SB)/4, $3
DATA ·fastSpanPermute<>+28(SB)/4, $7
GLOBL ·fastSpanPermute<>(SB), RODATA|NOPTR, $32

// func compositeSpanFastSSE2(pix *byte, batches int, addend, multiplier *float32)
// Processes batches*4 pixels.
TEXT ·compositeSpanFastSSE2(SB), NOSPLIT, $0-32
	MOVQ pix+0(FP), SI
	MOVQ batches+8(FP), CX
	MOVQ addend+16(FP), AX
	MOVQ multiplier+24(FP), BX

	MOVUPS (AX), X6                  // [addR, addG, addB, 0]
	MOVUPS (BX), X5                  // [mul, mul, mul, 1]
	PXOR   X7, X7

	TESTQ CX, CX
	JZ    done

loop:
	MOVOU (SI), X0                   // four interleaved NRGBA pixels
	MOVOU X0, X1

	// Zero-extend bytes to words, then words to dwords, leaving one pixel
	// per register in lanes [R, G, B, A].
	PUNPCKLBW X7, X0
	PUNPCKHBW X7, X1
	MOVOU     X0, X2
	PUNPCKLWL X7, X0                 // pixel 0
	PUNPCKHWL X7, X2                 // pixel 1
	MOVOU     X1, X3
	PUNPCKLWL X7, X1                 // pixel 2
	PUNPCKHWL X7, X3                 // pixel 3

	CVTPL2PS X0, X0
	CVTPL2PS X2, X2
	CVTPL2PS X1, X1
	CVTPL2PS X3, X3

	MULPS X5, X0
	ADDPS X6, X0
	MULPS X5, X2
	ADDPS X6, X2
	MULPS X5, X1
	ADDPS X6, X1
	MULPS X5, X3
	ADDPS X6, X3

	CVTTPS2PL X0, X0
	CVTTPS2PL X2, X2
	CVTTPS2PL X1, X1
	CVTTPS2PL X3, X3

	PACKSSLW X2, X0                  // pixels 0,1 as words
	PACKSSLW X3, X1                  // pixels 2,3 as words
	PACKUSWB X1, X0                  // pixels 0..3 as bytes

	MOVOU X0, (SI)
	ADDQ  $16, SI
	DECQ  CX
	JNZ   loop

done:
	RET

// func compositeSpanFastAVX2(pix *byte, batches int, addend, multiplier *float32)
// Processes batches*8 pixels. addend and multiplier must each supply eight
// float32 lanes, i.e. the four-lane group repeated twice.
TEXT ·compositeSpanFastAVX2(SB), NOSPLIT, $0-32
	MOVQ pix+0(FP), SI
	MOVQ batches+8(FP), CX
	MOVQ addend+16(FP), AX
	MOVQ multiplier+24(FP), BX

	VMOVUPS (AX), Y6
	VMOVUPS (BX), Y5
	VMOVDQU ·fastSpanPermute<>(SB), Y7

	TESTQ CX, CX
	JZ    avx2_done

avx2_loop:
	// Each VPMOVZXBD widens eight bytes, i.e. two pixels, to eight dwords.
	VPMOVZXBD (SI), Y0
	VPMOVZXBD 8(SI), Y1
	VPMOVZXBD 16(SI), Y2
	VPMOVZXBD 24(SI), Y3

	VCVTDQ2PS Y0, Y0
	VCVTDQ2PS Y1, Y1
	VCVTDQ2PS Y2, Y2
	VCVTDQ2PS Y3, Y3

	VMULPS Y5, Y0, Y0
	VADDPS Y6, Y0, Y0
	VMULPS Y5, Y1, Y1
	VADDPS Y6, Y1, Y1
	VMULPS Y5, Y2, Y2
	VADDPS Y6, Y2, Y2
	VMULPS Y5, Y3, Y3
	VADDPS Y6, Y3, Y3

	VCVTTPS2DQ Y0, Y0
	VCVTTPS2DQ Y1, Y1
	VCVTTPS2DQ Y2, Y2
	VCVTTPS2DQ Y3, Y3

	// The packs work within 128-bit lanes, so pixel order is interleaved
	// until VPERMD restores it.
	VPACKSSDW Y1, Y0, Y0
	VPACKSSDW Y3, Y2, Y2
	VPACKUSWB Y2, Y0, Y0
	VPERMD    Y0, Y7, Y0

	VMOVDQU Y0, (SI)
	ADDQ    $32, SI
	DECQ    CX
	JNZ     avx2_loop

avx2_done:
	VZEROUPPER
	RET
