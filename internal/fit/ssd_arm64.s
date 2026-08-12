// NEON SSD (sum of squared RGB differences) for interleaved NRGBA pixels.
//
// Four pixels are processed per vector iteration. Unsigned byte differences
// are masked to remove alpha, widened to uint16, squared, and horizontally
// reduced before addition to a uint64 accumulator. Widths not divisible by
// four use the scalar tail below.

#include "textflag.h"

DATA ·ssdRGBMaskNEON<>+0(SB)/8, $0x00ffffff00ffffff
DATA ·ssdRGBMaskNEON<>+8(SB)/8, $0x00ffffff00ffffff
GLOBL ·ssdRGBMaskNEON<>(SB), RODATA|NOPTR, $16

// func ssdNEON(a, b *uint8, stride, width, height int) float64
TEXT ·ssdNEON(SB), NOSPLIT, $0-48
	MOVD	a+0(FP), R0
	MOVD	b+8(FP), R1
	MOVD	stride+16(FP), R2
	MOVD	width+24(FP), R3
	MOVD	height+32(FP), R4

	MOVD	$0, R5                         // scalar tail total
	MOVD	$0, R6                         // y = 0
	VEOR	V14.B16, V14.B16, V14.B16     // uint64 vector total
	MOVD	$·ssdRGBMaskNEON<>(SB), R10
	VLD1	(R10), [V15.B16]

row_loop:
	CMP	R4, R6
	BEQ	reduce

	MOVD	$0, R8                         // x = 0
	BIC	$3, R3, R9                      // vectorized width

vector_loop:
	CMP	R9, R8
	BEQ	scalar_tail

	LSL	$2, R8, R10
	ADD	R10, R0, R11
	ADD	R10, R1, R12
	VLD1	(R11), [V0.B16]
	VLD1	(R12), [V1.B16]

	// UABD V3.16B, V0.16B, V1.16B. Go's ARM64 assembler does not
	// currently expose this integer Advanced SIMD mnemonic.
	WORD	$0x6e217403
	VAND	V15.B16, V3.B16, V3.B16

	VUXTL	V3.B8, V4.H8
	VUXTL2	V3.B16, V5.H8

	// MUL V4.8H, V4.8H, V4.8H and MUL V5.8H, V5.8H, V5.8H.
	// Squared byte differences fit exactly in uint16 (maximum 65025).
	WORD	$0x4e649c84
	WORD	$0x4e659ca5

	// Each widened horizontal sum is at most 8*65025. Add the two
	// halves to the uint64 accumulator immediately, avoiding overflow.
	VUADDLV	V4.H8, V6
	VUADDLV	V5.H8, V7
	VADD	V7, V6, V6
	VADD	V6, V14, V14

	ADD	$4, R8, R8
	B	vector_loop

scalar_tail:
	CMP	R3, R8
	BEQ	next_row

	LSL	$2, R8, R10
	ADD	R10, R0, R11
	ADD	R10, R1, R12

	MOVBU	0(R11), R13
	MOVBU	0(R12), R15
	SUB	R15, R13, R13
	MUL	R13, R13, R13

	MOVBU	1(R11), R14
	MOVBU	1(R12), R15
	SUB	R15, R14, R14
	MUL	R14, R14, R14
	ADD	R14, R13, R13

	MOVBU	2(R11), R14
	MOVBU	2(R12), R15
	SUB	R15, R14, R14
	MUL	R14, R14, R14
	ADD	R14, R13, R13

	ADD	R13, R5, R5
	ADD	$1, R8, R8
	B	scalar_tail

next_row:
	ADD	R2, R0, R0
	ADD	R2, R1, R1
	ADD	$1, R6, R6
	B	row_loop

reduce:
	VMOV	V14.D[0], R6
	ADD	R6, R5, R5
	SCVTFD	R5, F0
	FMOVD	F0, ret+40(FP)
	RET
