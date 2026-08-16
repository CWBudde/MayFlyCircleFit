// SSE2 SSD (sum of squared RGB differences) for interleaved NRGBA pixels.
//
// Four pixels are processed per 128-bit vector iteration. The alpha byte is
// masked, unsigned byte differences are widened to words, and PMADDWL squares
// and pairwise-adds the channel differences. Widths not divisible by four use
// the scalar tail below.
//
// Only baseline SSE2 instructions are used: no PMOVZXBW/PMOVZXDQ (SSE4.1) and
// no VEX encodings. Byte->word and dword->qword widening therefore go through
// PUNPCK* against a PXOR-zeroed register.
//
// Accumulator strategy: PMADDWL results are summed as int32 with PADDD for a
// whole row and widened to int64 exactly once per row. A row's maximum value is
// width*3*65025, which stays below 2^31 for width <= 11000. The Go wrapper
// fastSSD_SSE2 enforces that bound and routes wider images to the scalar
// kernel, so this assembly never sees a row that could overflow.

#include "textflag.h"

DATA ·ssdRGBMaskSSE2<>+0(SB)/8, $0x00ffffff00ffffff
DATA ·ssdRGBMaskSSE2<>+8(SB)/8, $0x00ffffff00ffffff
GLOBL ·ssdRGBMaskSSE2<>(SB), RODATA|NOPTR, $16

// func ssdSSE2(a, b *uint8, stride, width, height int) float64
TEXT ·ssdSSE2(SB), NOSPLIT, $16-48
	MOVQ a+0(FP), R8
	MOVQ b+8(FP), R9
	MOVQ stride+16(FP), R10
	MOVQ width+24(FP), R11
	MOVQ height+32(FP), R12

	XORQ R13, R13                    // scalar/reduced total
	PXOR X12, X12                    // constant zero for PUNPCK widening
	PXOR X14, X14                    // two uint64 accumulators
	MOVOU ·ssdRGBMaskSSE2<>(SB), X15
	XORQ R14, R14                    // y = 0

row_loop:
	CMPQ R14, R12
	JGE reduce

	MOVQ R14, R15
	IMULQ R10, R15                   // row byte offset
	XORQ DX, DX                      // x = 0
	MOVQ R11, SI
	ANDQ $-4, SI                     // vectorized width
	PXOR X13, X13                    // per-row uint32 accumulators

vector_loop:
	CMPQ DX, SI
	JGE row_reduce

	MOVQ DX, DI
	SHLQ $2, DI
	ADDQ R15, DI
	MOVOU (R8)(DI*1), X0
	MOVOU (R9)(DI*1), X1

	// Absolute unsigned byte differences, with alpha excluded.
	MOVOU X0, X2
	PMINUB X1, X2                    // X2 = min(a, b)
	MOVOU X0, X3
	PMAXUB X1, X3                    // X3 = max(a, b)
	PSUBB X2, X3                     // X3 = |a - b| per byte
	PAND X15, X3                     // drop the alpha byte of every pixel

	// Lower two pixels: widen bytes to words, then square and pairwise-add.
	MOVOU X3, X4
	PUNPCKLBW X12, X4
	PMADDWL X4, X4
	PADDD X4, X13

	// Upper two pixels use the same pipeline.
	MOVOU X3, X5
	PUNPCKHBW X12, X5
	PMADDWL X5, X5
	PADDD X5, X13

	ADDQ $4, DX
	JMP vector_loop

row_reduce:
	// Widen the four uint32 row partials to uint64 once per row. The values
	// are non-negative and below 2^31, so the unsigned zero-extension done
	// by PUNPCK against zero is exact.
	MOVOU X13, X6
	PUNPCKLLQ X12, X6
	PADDQ X6, X14
	MOVOU X13, X7
	PUNPCKHLQ X12, X7
	PADDQ X7, X14

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
	// Reduce two uint64 lanes, add scalar tails, and return an exact float64
	// value. Practical image sizes remain below float64's 53-bit exact-integer
	// limit.
	MOVOU X14, 0(SP)
	ADDQ 0(SP), R13
	ADDQ 8(SP), R13
	CVTSQ2SD R13, X0
	MOVSD X0, ret+40(FP)
	RET
