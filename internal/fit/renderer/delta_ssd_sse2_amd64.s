// SSE2 exact signed RGB SSD delta for one interleaved NRGBA span.
//
// This is the 128-bit counterpart of delta_ssd_amd64.s and processes four
// pixels per vector iteration. It stays inside the SSE2 instruction set:
// widening uses PUNPCK* against a zeroed register instead of the SSE4.1
// PMOVZX forms, and no VEX encoding or VZEROUPPER is involved.
//
// Accumulator strategy: PMADDWD results are summed as int32 with PADDL and
// widened to int64 exactly once, at the end. This matches ssd_sse2_amd64.s and
// deliberately does not match the AVX2 delta kernel next door, which widens per
// iteration and pays sixteen extra instructions per four pixels for it.
//
// The bound is per lane. PMADDWD pairwise-adds the widened R,G,B,0 words, so
// the busiest lane carries at most pixels*2*255*255 = pixels*130050 and first
// exceeds 2^31 at 16512 pixels. deltaSSDSpanSSE2's Go wrapper splits longer
// spans into deltaSSDSSE2MaxPixels-sized chunks and sums them in int64, so this
// assembly never sees a span that could overflow and there is no width cliff.

#include "textflag.h"

DATA ·deltaSSDRGBMaskSSE2<>+0(SB)/8, $0x00ffffff00ffffff
DATA ·deltaSSDRGBMaskSSE2<>+8(SB)/8, $0x00ffffff00ffffff
GLOBL ·deltaSSDRGBMaskSSE2<>(SB), RODATA|NOPTR, $16

// func deltaSSDSpanSSE2Kernel(candidate, base, reference *byte, pixels int) int64
TEXT ·deltaSSDSpanSSE2Kernel(SB), NOSPLIT, $32-40
	MOVQ candidate+0(FP), R8
	MOVQ base+8(FP), R9
	MOVQ reference+16(FP), R10
	MOVQ pixels+24(FP), R11

	XORQ  R12, R12                        // scalar signed delta
	PXOR  X12, X12                        // candidate int32 totals
	PXOR  X13, X13                        // base int32 totals
	PXOR  X14, X14                        // zero, for PUNPCK widening
	MOVOU ·deltaSSDRGBMaskSSE2<>(SB), X15
	XORQ  DX, DX                          // x = 0
	MOVQ  R11, SI
	ANDQ  $-4, SI

vector_loop:
	CMPQ  DX, SI
	JGE   scalar_tail
	MOVQ  DX, DI
	SHLQ  $2, DI
	MOVOU (R8)(DI*1), X0
	MOVOU (R9)(DI*1), X1
	MOVOU (R10)(DI*1), X2

	// Absolute candidate/reference byte differences into X4.
	MOVOU  X0, X3
	PMINUB X2, X3
	MOVOU  X0, X4
	PMAXUB X2, X4
	PSUBB  X3, X4
	PAND   X15, X4

	// Absolute base/reference byte differences into X6.
	MOVOU  X1, X5
	PMINUB X2, X5
	MOVOU  X1, X6
	PMAXUB X2, X6
	PSUBB  X5, X6
	PAND   X15, X6

	// Candidate lower two pixels.
	MOVOU     X4, X7
	PUNPCKLBW X14, X7
	PMADDWL   X7, X7
	PADDL     X7, X12

	// Candidate upper two pixels.
	PUNPCKHBW X14, X4
	PMADDWL   X4, X4
	PADDL     X4, X12

	// Base lower two pixels.
	MOVOU     X6, X7
	PUNPCKLBW X14, X7
	PMADDWL   X7, X7
	PADDL     X7, X13

	// Base upper two pixels.
	PUNPCKHBW X14, X6
	PMADDWL   X6, X6
	PADDL     X6, X13

	ADDQ $4, DX
	JMP  vector_loop

scalar_tail:
	CMPQ DX, R11
	JGE  reduce
	MOVQ DX, DI
	SHLQ $2, DI
	LEAQ (R8)(DI*1), AX
	LEAQ (R9)(DI*1), BX
	LEAQ (R10)(DI*1), CX

	MOVBQZX 0(AX), R13
	MOVBQZX 0(CX), R15
	SUBQ    R15, R13
	IMULQ   R13, R13
	MOVBQZX 1(AX), R14
	MOVBQZX 1(CX), R15
	SUBQ    R15, R14
	IMULQ   R14, R14
	ADDQ    R14, R13
	MOVBQZX 2(AX), R14
	MOVBQZX 2(CX), R15
	SUBQ    R15, R14
	IMULQ   R14, R14
	ADDQ    R14, R13

	MOVBQZX 0(BX), R14
	MOVBQZX 0(CX), R15
	SUBQ    R15, R14
	IMULQ   R14, R14
	SUBQ    R14, R13
	MOVBQZX 1(BX), R14
	MOVBQZX 1(CX), R15
	SUBQ    R15, R14
	IMULQ   R14, R14
	SUBQ    R14, R13
	MOVBQZX 2(BX), R14
	MOVBQZX 2(CX), R15
	SUBQ    R15, R14
	IMULQ   R14, R14
	SUBQ    R14, R13
	ADDQ    R13, R12
	INCQ    DX
	JMP     scalar_tail

reduce:
	// Widen the four int32 lanes of each accumulator to int64 exactly once, in
	// the vector registers. The lanes are non-negative sums of squares, so
	// unpacking against the zero register is the correct widening.
	//
	// The subtraction happens here rather than after the scalar reduction, so
	// only one 16-byte spill and two scalar adds are needed instead of eight
	// loads. That epilogue is the whole cost at a four-pixel span, where the
	// loop runs exactly once.
	MOVOU     X12, X0
	PUNPCKLLQ X14, X0
	PUNPCKHLQ X14, X12
	PADDQ     X12, X0

	MOVOU     X13, X1
	PUNPCKLLQ X14, X1
	PUNPCKHLQ X14, X13
	PADDQ     X13, X1

	PSUBQ X1, X0
	MOVOU X0, 0(SP)
	ADDQ  0(SP), R12
	ADDQ  8(SP), R12

	MOVQ R12, ret+32(FP)
	RET
