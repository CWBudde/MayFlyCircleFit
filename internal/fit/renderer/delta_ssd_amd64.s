// AVX2 exact signed RGB SSD delta for one interleaved NRGBA span.

#include "textflag.h"

DATA ·deltaSSDRGBMask<>+0(SB)/8, $0x00ffffff00ffffff
DATA ·deltaSSDRGBMask<>+8(SB)/8, $0x00ffffff00ffffff
DATA ·deltaSSDRGBMask<>+16(SB)/8, $0x00ffffff00ffffff
DATA ·deltaSSDRGBMask<>+24(SB)/8, $0x00ffffff00ffffff
GLOBL ·deltaSSDRGBMask<>(SB), RODATA|NOPTR, $32

// func deltaSSDSpanAVX2(candidate, base, reference *byte, pixels int) int64
TEXT ·deltaSSDSpanAVX2(SB), NOSPLIT, $64-40
	MOVQ candidate+0(FP), R8
	MOVQ base+8(FP), R9
	MOVQ reference+16(FP), R10
	MOVQ pixels+24(FP), R11

	XORQ R12, R12                    // scalar signed delta
	VPXOR Y12, Y12, Y12              // candidate uint64 totals
	VPXOR Y13, Y13, Y13              // base uint64 totals
	VMOVDQU ·deltaSSDRGBMask<>(SB), Y15
	XORQ DX, DX                      // x = 0
	MOVQ R11, SI
	ANDQ $-8, SI

vector_loop:
	CMPQ DX, SI
	JGE scalar_tail
	MOVQ DX, DI
	SHLQ $2, DI
	VMOVDQU (R8)(DI*1), Y0
	VMOVDQU (R9)(DI*1), Y1
	VMOVDQU (R10)(DI*1), Y2

	// Absolute candidate/reference and base/reference byte differences.
	VPMINUB Y2, Y0, Y3
	VPMAXUB Y2, Y0, Y4
	VPSUBB Y3, Y4, Y3
	VPAND Y15, Y3, Y3
	VPMINUB Y2, Y1, Y6
	VPMAXUB Y2, Y1, Y7
	VPSUBB Y6, Y7, Y6
	VPAND Y15, Y6, Y6

	// Candidate lower four pixels.
	VPMOVZXBW X3, Y4
	VPMADDWD Y4, Y4, Y4
	VPMOVZXDQ X4, Y5
	VPADDQ Y5, Y12, Y12
	VEXTRACTI128 $1, Y4, X5
	VPMOVZXDQ X5, Y5
	VPADDQ Y5, Y12, Y12
	// Candidate upper four pixels.
	VEXTRACTI128 $1, Y3, X4
	VPMOVZXBW X4, Y4
	VPMADDWD Y4, Y4, Y4
	VPMOVZXDQ X4, Y5
	VPADDQ Y5, Y12, Y12
	VEXTRACTI128 $1, Y4, X5
	VPMOVZXDQ X5, Y5
	VPADDQ Y5, Y12, Y12

	// Base lower four pixels.
	VPMOVZXBW X6, Y4
	VPMADDWD Y4, Y4, Y4
	VPMOVZXDQ X4, Y5
	VPADDQ Y5, Y13, Y13
	VEXTRACTI128 $1, Y4, X5
	VPMOVZXDQ X5, Y5
	VPADDQ Y5, Y13, Y13
	// Base upper four pixels.
	VEXTRACTI128 $1, Y6, X4
	VPMOVZXBW X4, Y4
	VPMADDWD Y4, Y4, Y4
	VPMOVZXDQ X4, Y5
	VPADDQ Y5, Y13, Y13
	VEXTRACTI128 $1, Y4, X5
	VPMOVZXDQ X5, Y5
	VPADDQ Y5, Y13, Y13

	ADDQ $8, DX
	JMP vector_loop

scalar_tail:
	CMPQ DX, R11
	JGE reduce
	MOVQ DX, DI
	SHLQ $2, DI
	LEAQ (R8)(DI*1), AX
	LEAQ (R9)(DI*1), BX
	LEAQ (R10)(DI*1), CX

	MOVBQZX 0(AX), R13
	MOVBQZX 0(CX), R15
	SUBQ R15, R13
	IMULQ R13, R13
	MOVBQZX 1(AX), R14
	MOVBQZX 1(CX), R15
	SUBQ R15, R14
	IMULQ R14, R14
	ADDQ R14, R13
	MOVBQZX 2(AX), R14
	MOVBQZX 2(CX), R15
	SUBQ R15, R14
	IMULQ R14, R14
	ADDQ R14, R13

	MOVBQZX 0(BX), R14
	MOVBQZX 0(CX), R15
	SUBQ R15, R14
	IMULQ R14, R14
	SUBQ R14, R13
	MOVBQZX 1(BX), R14
	MOVBQZX 1(CX), R15
	SUBQ R15, R14
	IMULQ R14, R14
	SUBQ R14, R13
	MOVBQZX 2(BX), R14
	MOVBQZX 2(CX), R15
	SUBQ R15, R14
	IMULQ R14, R14
	SUBQ R14, R13
	ADDQ R13, R12
	INCQ DX
	JMP scalar_tail

reduce:
	VMOVDQU Y12, 0(SP)
	ADDQ 0(SP), R12
	ADDQ 8(SP), R12
	ADDQ 16(SP), R12
	ADDQ 24(SP), R12
	VMOVDQU Y13, 32(SP)
	SUBQ 32(SP), R12
	SUBQ 40(SP), R12
	SUBQ 48(SP), R12
	SUBQ 56(SP), R12
	VZEROUPPER
	MOVQ R12, ret+32(FP)
	RET
