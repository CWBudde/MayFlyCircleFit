// ARM64 NEON exact signed RGB SSD delta for one interleaved NRGBA span.

#include "textflag.h"

DATA ·deltaSSDRGBMaskNEON<>+0(SB)/8, $0x00ffffff00ffffff
DATA ·deltaSSDRGBMaskNEON<>+8(SB)/8, $0x00ffffff00ffffff
GLOBL ·deltaSSDRGBMaskNEON<>(SB), RODATA|NOPTR, $16

// func deltaSSDSpanNEON(candidate, base, reference *byte, pixels int) int64
TEXT ·deltaSSDSpanNEON(SB), NOSPLIT, $0-40
	MOVD candidate+0(FP), R0
	MOVD base+8(FP), R1
	MOVD reference+16(FP), R2
	MOVD pixels+24(FP), R3

	BIC $3, R3, R4
	MOVD $0, R5                       // x = 0
	MOVD $0, R6                       // scalar signed delta
	VEOR V14.B16, V14.B16, V14.B16   // candidate uint64 total
	VEOR V13.B16, V13.B16, V13.B16   // base uint64 total
	MOVD $·deltaSSDRGBMaskNEON<>(SB), R10
	VLD1 (R10), [V15.B16]

vector_loop:
	CMP R4, R5
	BEQ scalar_tail
	LSL $2, R5, R10
	ADD R10, R0, R11
	ADD R10, R1, R12
	ADD R10, R2, R13
	VLD1 (R11), [V0.B16]
	VLD1 (R12), [V1.B16]
	VLD1 (R13), [V2.B16]

	// UABD V3.16B,V0.16B,V2.16B and UABD V8.16B,V1.16B,V2.16B.
	WORD $0x6e227403
	WORD $0x6e227428
	VAND V15.B16, V3.B16, V3.B16
	VAND V15.B16, V8.B16, V8.B16

	VUXTL V3.B8, V4.H8
	VUXTL2 V3.B16, V5.H8
	WORD $0x4e649c84                 // MUL V4.8H,V4.8H,V4.8H
	WORD $0x4e659ca5                 // MUL V5.8H,V5.8H,V5.8H
	VUADDLV V4.H8, V6
	VUADDLV V5.H8, V7
	VADD V7, V6, V6
	VADD V6, V14, V14

	VUXTL V8.B8, V9.H8
	VUXTL2 V8.B16, V10.H8
	WORD $0x4e699d29                 // MUL V9.8H,V9.8H,V9.8H
	WORD $0x4e6a9d4a                 // MUL V10.8H,V10.8H,V10.8H
	VUADDLV V9.H8, V11
	VUADDLV V10.H8, V12
	VADD V12, V11, V11
	VADD V11, V13, V13

	ADD $4, R5, R5
	B vector_loop

scalar_tail:
	CMP R3, R5
	BEQ reduce
	LSL $2, R5, R10
	ADD R10, R0, R11
	ADD R10, R1, R12
	ADD R10, R2, R13

	MOVBU 0(R11), R14
	MOVBU 0(R13), R15
	SUB R15, R14, R14
	MUL R14, R14, R14
	MOVBU 1(R11), R15
	MOVBU 1(R13), R16
	SUB R16, R15, R15
	MUL R15, R15, R15
	ADD R15, R14, R14
	MOVBU 2(R11), R15
	MOVBU 2(R13), R16
	SUB R16, R15, R15
	MUL R15, R15, R15
	ADD R15, R14, R14

	MOVBU 0(R12), R15
	MOVBU 0(R13), R16
	SUB R16, R15, R15
	MUL R15, R15, R15
	SUB R15, R14, R14
	MOVBU 1(R12), R15
	MOVBU 1(R13), R16
	SUB R16, R15, R15
	MUL R15, R15, R15
	SUB R15, R14, R14
	MOVBU 2(R12), R15
	MOVBU 2(R13), R16
	SUB R16, R15, R15
	MUL R15, R15, R15
	SUB R15, R14, R14
	ADD R14, R6, R6
	ADD $1, R5, R5
	B scalar_tail

reduce:
	VMOV V14.D[0], R7
	VMOV V13.D[0], R8
	SUB R8, R7, R7
	ADD R7, R6, R6
	MOVD R6, ret+32(FP)
	RET
