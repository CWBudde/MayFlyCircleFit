// AVX2 float32 circle span-edge search.
//
// Candidate pixels are ordered nearest-to-farthest in each vector. Circle
// distance is monotonic from the rounded center, so a partial comparison mask
// directly identifies the exact edge. Only clipped tails shorter than eight
// pixels use scalar float32 instructions.

#include "textflag.h"

DATA ·circleLeftOffsets<>+0(SB)/4, $0xbf800000
DATA ·circleLeftOffsets<>+4(SB)/4, $0xc0000000
DATA ·circleLeftOffsets<>+8(SB)/4, $0xc0400000
DATA ·circleLeftOffsets<>+12(SB)/4, $0xc0800000
DATA ·circleLeftOffsets<>+16(SB)/4, $0xc0a00000
DATA ·circleLeftOffsets<>+20(SB)/4, $0xc0c00000
DATA ·circleLeftOffsets<>+24(SB)/4, $0xc0e00000
DATA ·circleLeftOffsets<>+28(SB)/4, $0xc1000000
GLOBL ·circleLeftOffsets<>(SB), RODATA|NOPTR, $32

DATA ·circleRightOffsets<>+0(SB)/4, $0x3f800000
DATA ·circleRightOffsets<>+4(SB)/4, $0x40000000
DATA ·circleRightOffsets<>+8(SB)/4, $0x40400000
DATA ·circleRightOffsets<>+12(SB)/4, $0x40800000
DATA ·circleRightOffsets<>+16(SB)/4, $0x40a00000
DATA ·circleRightOffsets<>+20(SB)/4, $0x40c00000
DATA ·circleRightOffsets<>+24(SB)/4, $0x40e00000
DATA ·circleRightOffsets<>+28(SB)/4, $0x41000000
GLOBL ·circleRightOffsets<>(SB), RODATA|NOPTR, $32

DATA ·circleOne<>+0(SB)/4, $0x3f800000
GLOBL ·circleOne<>(SB), RODATA|NOPTR, $4
DATA ·circleEight<>+0(SB)/4, $0x41000000
GLOBL ·circleEight<>(SB), RODATA|NOPTR, $4

// func circleSpanFloat32AVX2Kernel(centerX, radiusSquaredMinusDY,
//     roundedCenter float32, width int) (xStart, xEnd int)
TEXT ·circleSpanFloat32AVX2Kernel(SB), NOSPLIT, $0-40
	VMOVSS centerX+0(FP), X0
	VBROADCASTSS X0, Y0
	VMOVSS radiusSquaredMinusDY+4(FP), X1
	VBROADCASTSS X1, Y1
	VMOVSS roundedCenter+8(FP), X2
	VBROADCASTSS X2, Y2
	VBROADCASTSS ·circleEight<>(SB), Y5
	MOVQ width+16(FP), SI

	// Left candidates are [cx-1, cx-2, ..., cx-8].
	VADDPS ·circleLeftOffsets<>(SB), Y2, Y2
	VMOVSS roundedCenter+8(FP), X3
	VCVTTSS2SIQ X3, AX                 // xStart = rounded center

left_vector:
	CMPQ AX, $8
	JLT left_scalar
	VSUBPS Y0, Y2, Y3
	VMULPS Y3, Y3, Y3
	VCMPPS $2, Y1, Y3, Y4             // candidate squared <= remaining
	VMOVMSKPS Y4, DI
	CMPL DI, $255
	JNE left_partial
	SUBQ $8, AX
	VSUBPS Y5, Y2, Y2
	JMP left_vector

left_partial:
	NOTL DI
	BSFL DI, DI                       // number of consecutive inside lanes
	SUBQ DI, AX
	JMP left_done

left_scalar:
	CMPQ AX, $0
	JLE left_done
	// X2 lane zero is the next candidate and tracks one pixel per iteration.
	VSUBSS X0, X2, X3
	VMULSS X3, X3, X3
	VUCOMISS X1, X3
	JHI left_done
	DECQ AX
	VSUBSS ·circleOne<>(SB), X2, X2
	JMP left_scalar

left_done:
	MOVQ AX, xStart+24(FP)

	// Right candidates are [cx+1, cx+2, ..., cx+8].
	VMOVSS roundedCenter+8(FP), X2
	VCVTTSS2SIQ X2, BX
	INCQ BX                            // xEnd = rounded center + 1
	VBROADCASTSS X2, Y2
	VADDPS ·circleRightOffsets<>(SB), Y2, Y2

right_vector:
	LEAQ 7(BX), DI
	CMPQ DI, SI
	JGE right_scalar
	VSUBPS Y0, Y2, Y3
	VMULPS Y3, Y3, Y3
	VCMPPS $2, Y1, Y3, Y4             // candidate squared <= remaining
	VMOVMSKPS Y4, DI
	CMPL DI, $255
	JNE right_partial
	ADDQ $8, BX
	VADDPS Y5, Y2, Y2
	JMP right_vector

right_partial:
	NOTL DI
	BSFL DI, DI                       // number of consecutive inside lanes
	ADDQ DI, BX
	JMP right_clamp

right_scalar:
	CMPQ BX, SI
	JGE right_clamp
	VSUBSS X0, X2, X3
	VMULSS X3, X3, X3
	VUCOMISS X1, X3
	JHI right_clamp
	INCQ BX
	VADDSS ·circleOne<>(SB), X2, X2
	JMP right_scalar

right_clamp:
	CMPQ BX, SI
	JLE done
	MOVQ SI, BX

done:
	MOVQ BX, xEnd+32(FP)
	VZEROUPPER
	RET

// Q16.16 offsets for the eight candidates nearest the rounded center.
DATA ·circleQ16LeftOffsets<>+0(SB)/4, $0xffff0000
DATA ·circleQ16LeftOffsets<>+4(SB)/4, $0xfffe0000
DATA ·circleQ16LeftOffsets<>+8(SB)/4, $0xfffd0000
DATA ·circleQ16LeftOffsets<>+12(SB)/4, $0xfffc0000
DATA ·circleQ16LeftOffsets<>+16(SB)/4, $0xfffb0000
DATA ·circleQ16LeftOffsets<>+20(SB)/4, $0xfffa0000
DATA ·circleQ16LeftOffsets<>+24(SB)/4, $0xfff90000
DATA ·circleQ16LeftOffsets<>+28(SB)/4, $0xfff80000
GLOBL ·circleQ16LeftOffsets<>(SB), RODATA|NOPTR, $32

DATA ·circleQ16RightOffsets<>+0(SB)/4, $0x00010000
DATA ·circleQ16RightOffsets<>+4(SB)/4, $0x00020000
DATA ·circleQ16RightOffsets<>+8(SB)/4, $0x00030000
DATA ·circleQ16RightOffsets<>+12(SB)/4, $0x00040000
DATA ·circleQ16RightOffsets<>+16(SB)/4, $0x00050000
DATA ·circleQ16RightOffsets<>+20(SB)/4, $0x00060000
DATA ·circleQ16RightOffsets<>+24(SB)/4, $0x00070000
DATA ·circleQ16RightOffsets<>+28(SB)/4, $0x00080000
GLOBL ·circleQ16RightOffsets<>(SB), RODATA|NOPTR, $32

DATA ·circleQ16Eight<>+0(SB)/4, $0x00080000
GLOBL ·circleQ16Eight<>(SB), RODATA|NOPTR, $4

// func circleSpanQ16AVX2Kernel(centerXQ int32, roundedCenter int,
//     radiusSquaredMinusDY int64, width int) (xStart, xEnd int)
//
// VPMULDQ widens four even int32 lanes to int64 products. Shifting each
// 64-bit pair exposes the four odd lanes for a second VPMULDQ. Their two
// comparison masks are interleaved into pixel order before locating the first
// outside candidate.
TEXT ·circleSpanQ16AVX2Kernel(SB), NOSPLIT, $0-48
	MOVL centerXQ+0(FP), R8
	VMOVD R8, X0
	VPBROADCASTD X0, Y0
	MOVQ radiusSquaredMinusDY+16(FP), R10
	VMOVQ R10, X1
	VPBROADCASTQ X1, Y1
	VPBROADCASTD ·circleQ16Eight<>(SB), Y5
	MOVQ width+24(FP), SI

	MOVQ roundedCenter+8(FP), AX
	MOVQ AX, R8
	SHLQ $16, R8
	VMOVD R8, X2
	VPBROADCASTD X2, Y2
	VPADDD ·circleQ16LeftOffsets<>(SB), Y2, Y2

q16_left_vector:
	CMPQ AX, $8
	JLT q16_left_scalar
	VPSUBD Y0, Y2, Y3
	VPSRLQ $32, Y3, Y6
	VPMULDQ Y3, Y3, Y4
	VPMULDQ Y6, Y6, Y6
	VPCMPGTQ Y1, Y4, Y4
	VPCMPGTQ Y1, Y6, Y6
	VMOVMSKPD Y4, DI
	VMOVMSKPD Y6, R8

	// Expand the four even/odd bits and interleave them as lanes 0..7.
	MOVL DI, R9
	SHLL $2, R9
	ORL R9, DI
	ANDL $0x33, DI
	MOVL DI, R9
	SHLL $1, R9
	ORL R9, DI
	ANDL $0x55, DI
	MOVL R8, R9
	SHLL $2, R9
	ORL R9, R8
	ANDL $0x33, R8
	MOVL R8, R9
	SHLL $1, R9
	ORL R9, R8
	ANDL $0x55, R8
	SHLL $1, R8
	ORL R8, DI
	TESTL DI, DI
	JNE q16_left_partial
	SUBQ $8, AX
	VPSUBD Y5, Y2, Y2
	JMP q16_left_vector

q16_left_partial:
	BSFL DI, DI
	SUBQ DI, AX
	JMP q16_left_done

q16_left_scalar:
	CMPQ AX, $0
	JLE q16_left_done
	MOVQ AX, R8
	DECQ R8
	SHLQ $16, R8
	MOVLQSX centerXQ+0(FP), R9
	SUBQ R9, R8
	IMULQ R8, R8
	CMPQ R8, R10
	JGT q16_left_done
	DECQ AX
	JMP q16_left_scalar

q16_left_done:
	MOVQ AX, xStart+32(FP)

	MOVQ roundedCenter+8(FP), BX
	INCQ BX
	MOVQ roundedCenter+8(FP), R8
	SHLQ $16, R8
	VMOVD R8, X2
	VPBROADCASTD X2, Y2
	VPADDD ·circleQ16RightOffsets<>(SB), Y2, Y2

q16_right_vector:
	LEAQ 7(BX), DI
	CMPQ DI, SI
	JGE q16_right_scalar
	VPSUBD Y0, Y2, Y3
	VPSRLQ $32, Y3, Y6
	VPMULDQ Y3, Y3, Y4
	VPMULDQ Y6, Y6, Y6
	VPCMPGTQ Y1, Y4, Y4
	VPCMPGTQ Y1, Y6, Y6
	VMOVMSKPD Y4, DI
	VMOVMSKPD Y6, R8

	MOVL DI, R9
	SHLL $2, R9
	ORL R9, DI
	ANDL $0x33, DI
	MOVL DI, R9
	SHLL $1, R9
	ORL R9, DI
	ANDL $0x55, DI
	MOVL R8, R9
	SHLL $2, R9
	ORL R9, R8
	ANDL $0x33, R8
	MOVL R8, R9
	SHLL $1, R9
	ORL R9, R8
	ANDL $0x55, R8
	SHLL $1, R8
	ORL R8, DI
	TESTL DI, DI
	JNE q16_right_partial
	ADDQ $8, BX
	VPADDD Y5, Y2, Y2
	JMP q16_right_vector

q16_right_partial:
	BSFL DI, DI
	ADDQ DI, BX
	JMP q16_right_clamp

q16_right_scalar:
	CMPQ BX, SI
	JGE q16_right_clamp
	MOVQ BX, R8
	SHLQ $16, R8
	MOVLQSX centerXQ+0(FP), R9
	SUBQ R9, R8
	IMULQ R8, R8
	CMPQ R8, R10
	JGT q16_right_clamp
	INCQ BX
	JMP q16_right_scalar

q16_right_clamp:
	CMPQ BX, SI
	JLE q16_done
	MOVQ SI, BX

q16_done:
	MOVQ BX, xEnd+40(FP)
	VZEROUPPER
	RET
