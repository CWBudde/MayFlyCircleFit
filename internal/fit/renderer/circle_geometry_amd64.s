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
	VBROADCASTSS ·circleEight<>(SB), Y5
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
	VBROADCASTSS ·circleEight<>(SB), Y5
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
