// AVX2 SSD (sum of squared RGB differences) for interleaved NRGBA pixels.
//
// Eight pixels are processed per vector iteration. The alpha byte is masked,
// unsigned byte differences are widened to words, and VPMADDWD squares and
// pairwise-adds the channel differences. Partial sums are widened to uint64 so
// large images cannot overflow a 32-bit SIMD lane. Widths not divisible by
// eight use the scalar tail below.

#include "textflag.h"

DATA ·ssdRGBMask<>+0(SB)/8, $0x00ffffff00ffffff
DATA ·ssdRGBMask<>+8(SB)/8, $0x00ffffff00ffffff
DATA ·ssdRGBMask<>+16(SB)/8, $0x00ffffff00ffffff
DATA ·ssdRGBMask<>+24(SB)/8, $0x00ffffff00ffffff
GLOBL ·ssdRGBMask<>(SB), RODATA|NOPTR, $32

// func ssdAVX2(a, b *uint8, stride, width, height int) float64
TEXT ·ssdAVX2(SB), NOSPLIT, $32-48
	MOVQ a+0(FP), R8
	MOVQ b+8(FP), R9
	MOVQ stride+16(FP), R10
	MOVQ width+24(FP), R11
	MOVQ height+32(FP), R12

	XORQ R13, R13                    // scalar/reduced total
	VPXOR Y14, Y14, Y14              // four uint64 vector accumulators
	VMOVDQU ·ssdRGBMask<>(SB), Y15
	XORQ R14, R14                    // y = 0

row_loop:
	CMPQ R14, R12
	JGE reduce

	MOVQ R14, R15
	IMULQ R10, R15                   // row byte offset
	XORQ DX, DX                      // x = 0
	MOVQ R11, SI
	ANDQ $-8, SI                     // vectorized width

vector_loop:
	CMPQ DX, SI
	JGE scalar_tail

	MOVQ DX, DI
	SHLQ $2, DI
	ADDQ R15, DI
	VMOVDQU (R8)(DI*1), Y0
	VMOVDQU (R9)(DI*1), Y1

	// Absolute unsigned byte differences, with alpha excluded.
	VPMINUB Y1, Y0, Y2
	VPMAXUB Y1, Y0, Y3
	VPSUBB Y2, Y3, Y3
	VPAND Y15, Y3, Y3

	// Lower four pixels: widen bytes, square adjacent word pairs, widen
	// the eight dword partial sums, and accumulate in uint64 lanes.
	VPMOVZXBW X3, Y4
	VPMADDWD Y4, Y4, Y4
	VPMOVZXDQ X4, Y5
	VPADDQ Y5, Y14, Y14
	VEXTRACTI128 $1, Y4, X5
	VPMOVZXDQ X5, Y5
	VPADDQ Y5, Y14, Y14

	// Upper four pixels use the same widening pipeline.
	VEXTRACTI128 $1, Y3, X4
	VPMOVZXBW X4, Y4
	VPMADDWD Y4, Y4, Y4
	VPMOVZXDQ X4, Y5
	VPADDQ Y5, Y14, Y14
	VEXTRACTI128 $1, Y4, X5
	VPMOVZXDQ X5, Y5
	VPADDQ Y5, Y14, Y14

	ADDQ $8, DX
	JMP vector_loop

scalar_tail:
	CMPQ DX, R11
	JGE next_row

	MOVQ DX, DI
	SHLQ $2, DI
	ADDQ R15, DI

	LEAQ (R8)(DI*1), AX
	LEAQ (R9)(DI*1), BX

	MOVBQZX 0(AX), CX
	MOVBQZX 0(BX), SI
	SUBQ SI, CX
	IMULQ CX, CX

	MOVBQZX 1(AX), SI
	MOVBQZX 1(BX), DI
	SUBQ DI, SI
	IMULQ SI, SI
	ADDQ SI, CX

	MOVBQZX 2(AX), SI
	MOVBQZX 2(BX), DI
	SUBQ DI, SI
	IMULQ SI, SI
	ADDQ SI, CX

	ADDQ CX, R13
	INCQ DX
	JMP scalar_tail

next_row:
	INCQ R14
	JMP row_loop

reduce:
	// Reduce four uint64 lanes, add scalar tails, and return an exact
	// float64 value. Practical image sizes remain below float64's 53-bit
	// exact-integer limit.
	VMOVDQU Y14, 0(SP)
	ADDQ 0(SP), R13
	ADDQ 8(SP), R13
	ADDQ 16(SP), R13
	ADDQ 24(SP), R13
	VZEROUPPER
	CVTSQ2SD R13, X0
	MOVSD X0, ret+40(FP)
	RET
