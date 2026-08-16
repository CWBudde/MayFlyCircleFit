// SSE2 exact signed RGB SSD delta for one interleaved NRGBA span.
//
// This is the 128-bit counterpart of delta_ssd_amd64.s and processes four
// pixels per vector iteration. It stays inside the SSE2 instruction set:
// widening uses PUNPCK* against a zeroed register instead of the SSE4.1
// PMOVZX forms, and no VEX encoding or VZEROUPPER is involved.

#include "textflag.h"

DATA ·deltaSSDRGBMaskSSE2<>+0(SB)/8, $0x00ffffff00ffffff
DATA ·deltaSSDRGBMaskSSE2<>+8(SB)/8, $0x00ffffff00ffffff
GLOBL ·deltaSSDRGBMaskSSE2<>(SB), RODATA|NOPTR, $16

// func deltaSSDSpanSSE2(candidate, base, reference *byte, pixels int) int64
TEXT ·deltaSSDSpanSSE2(SB), NOSPLIT, $32-40
	MOVQ candidate+0(FP), R8
	MOVQ base+8(FP), R9
	MOVQ reference+16(FP), R10
	MOVQ pixels+24(FP), R11

	XORQ  R12, R12                        // scalar signed delta
	PXOR  X12, X12                        // candidate uint64 totals
	PXOR  X13, X13                        // base uint64 totals
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
	MOVOU     X7, X8
	PUNPCKLLQ X14, X8
	PADDQ     X8, X12
	PUNPCKHLQ X14, X7
	PADDQ     X7, X12

	// Candidate upper two pixels.
	PUNPCKHBW X14, X4
	PMADDWL   X4, X4
	MOVOU     X4, X8
	PUNPCKLLQ X14, X8
	PADDQ     X8, X12
	PUNPCKHLQ X14, X4
	PADDQ     X4, X12

	// Base lower two pixels.
	MOVOU     X6, X7
	PUNPCKLBW X14, X7
	PMADDWL   X7, X7
	MOVOU     X7, X8
	PUNPCKLLQ X14, X8
	PADDQ     X8, X13
	PUNPCKHLQ X14, X7
	PADDQ     X7, X13

	// Base upper two pixels.
	PUNPCKHBW X14, X6
	PMADDWL   X6, X6
	MOVOU     X6, X8
	PUNPCKLLQ X14, X8
	PADDQ     X8, X13
	PUNPCKHLQ X14, X6
	PADDQ     X6, X13

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
	MOVOU X12, 0(SP)
	ADDQ  0(SP), R12
	ADDQ  8(SP), R12
	MOVOU X13, 16(SP)
	SUBQ  16(SP), R12
	SUBQ  24(SP), R12
	MOVQ  R12, ret+32(FP)
	RET
