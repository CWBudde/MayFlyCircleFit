// SSE2 float32 circle span-edge search.
//
// This is the 128-bit counterpart of the AVX2 kernel in
// circle_geometry_amd64.s and exists so that amd64 CPUs without AVX2 stop
// falling back to the fully scalar edge search. Candidate pixels are ordered
// nearest-to-farthest inside each vector pair. Circle distance is monotonic
// from the rounded center, so a partial comparison mask directly identifies the
// exact edge. Only clipped tails shorter than eight pixels use scalar float32
// instructions.
//
// Each iteration evaluates eight candidates as two four-lane XMM vectors. A
// single four-lane vector was measured first and lost to the scalar span search
// for radii of 25 pixels and up: the scalar path already advances eight pixels
// per comparison, so a four-pixel stride doubles the iteration count. Keeping
// the eight-pixel stride restores parity with the AVX2 kernel and preserves the
// eight-bit mask arithmetic, with the high vector's four-bit mask shifted into
// lanes 4..7.
//
// Plan 9 SSE mnemonics are two-operand and destructive, so the three-operand
// VEX sequences are restructured with explicit MOVAPS copies. Scalar broadcasts
// use MOVSS plus SHUFPS $0x00 because VBROADCASTSS is AVX-only. The offset
// tables are loaded with MOVUPS instead of being used as memory operands so the
// kernel does not depend on 16-byte alignment of the RODATA symbols.
//
// There is deliberately no SSE2 counterpart of circleSpanQ16AVX2Kernel: that
// kernel compares Q32.32 products with VPCMPGTQ, and SSE2 has no 64-bit signed
// compare. See circle_geometry_amd64.go for the measured reasoning.

#include "textflag.h"

// The eight candidates nearest the rounded center, ordered nearest-to-farthest
// and loaded as two four-lane vectors.
DATA ·circleSSE2LeftOffsets<>+0(SB)/4, $0xbf800000  // -1
DATA ·circleSSE2LeftOffsets<>+4(SB)/4, $0xc0000000  // -2
DATA ·circleSSE2LeftOffsets<>+8(SB)/4, $0xc0400000  // -3
DATA ·circleSSE2LeftOffsets<>+12(SB)/4, $0xc0800000 // -4
DATA ·circleSSE2LeftOffsets<>+16(SB)/4, $0xc0a00000 // -5
DATA ·circleSSE2LeftOffsets<>+20(SB)/4, $0xc0c00000 // -6
DATA ·circleSSE2LeftOffsets<>+24(SB)/4, $0xc0e00000 // -7
DATA ·circleSSE2LeftOffsets<>+28(SB)/4, $0xc1000000 // -8
GLOBL ·circleSSE2LeftOffsets<>(SB), RODATA|NOPTR, $32

DATA ·circleSSE2RightOffsets<>+0(SB)/4, $0x3f800000  // +1
DATA ·circleSSE2RightOffsets<>+4(SB)/4, $0x40000000  // +2
DATA ·circleSSE2RightOffsets<>+8(SB)/4, $0x40400000  // +3
DATA ·circleSSE2RightOffsets<>+12(SB)/4, $0x40800000 // +4
DATA ·circleSSE2RightOffsets<>+16(SB)/4, $0x40a00000 // +5
DATA ·circleSSE2RightOffsets<>+20(SB)/4, $0x40c00000 // +6
DATA ·circleSSE2RightOffsets<>+24(SB)/4, $0x40e00000 // +7
DATA ·circleSSE2RightOffsets<>+28(SB)/4, $0x41000000 // +8
GLOBL ·circleSSE2RightOffsets<>(SB), RODATA|NOPTR, $32

DATA ·circleSSE2One<>+0(SB)/4, $0x3f800000
GLOBL ·circleSSE2One<>(SB), RODATA|NOPTR, $4
DATA ·circleSSE2Eight<>+0(SB)/4, $0x41000000
GLOBL ·circleSSE2Eight<>(SB), RODATA|NOPTR, $4

// func circleSpanFloat32SSE2Kernel(centerX, radiusSquaredMinusDY,
//     roundedCenter float32, width int) (xStart, xEnd int)
TEXT ·circleSpanFloat32SSE2Kernel(SB), NOSPLIT, $0-40
	MOVSS  centerX+0(FP), X0
	SHUFPS $0x00, X0, X0
	MOVSS  radiusSquaredMinusDY+4(FP), X1
	SHUFPS $0x00, X1, X1
	MOVSS  ·circleSSE2Eight<>(SB), X5
	SHUFPS $0x00, X5, X5
	MOVQ   width+16(FP), SI

	MOVSS     roundedCenter+8(FP), X2
	CVTTSS2SQ X2, AX                  // xStart = rounded center
	SHUFPS    $0x00, X2, X2
	MOVAPS    X2, X7

	// Left candidates are [cx-1, ..., cx-4] and [cx-5, ..., cx-8].
	MOVUPS ·circleSSE2LeftOffsets<>+0(SB), X6
	ADDPS  X6, X2
	MOVUPS ·circleSSE2LeftOffsets<>+16(SB), X6
	ADDPS  X6, X7

left_vector:
	CMPQ     AX, $8
	JLT      left_scalar
	MOVAPS   X2, X3
	SUBPS    X0, X3
	MULPS    X3, X3
	MOVAPS   X7, X4
	SUBPS    X0, X4
	MULPS    X4, X4
	CMPPS    X1, X3, $2  // candidate squared <= remaining
	CMPPS    X1, X4, $2
	MOVMSKPS X3, DI
	MOVMSKPS X4, R8
	SHLL     $4, R8
	ORL      R8, DI
	CMPL     DI, $255
	JNE      left_partial
	SUBQ     $8, AX
	SUBPS    X5, X2
	SUBPS    X5, X7
	JMP      left_vector

left_partial:
	NOTL DI
	BSFL DI, DI  // number of consecutive inside lanes
	SUBQ DI, AX
	JMP  left_done

left_scalar:
	CMPQ    AX, $0
	JLE     left_done
	// X2 lane zero is the next candidate and tracks one pixel per iteration.
	MOVAPS  X2, X3
	SUBSS   X0, X3
	MULSS   X3, X3
	UCOMISS X1, X3
	JHI     left_done
	DECQ    AX
	SUBSS   ·circleSSE2One<>(SB), X2
	JMP     left_scalar

left_done:
	MOVQ AX, xStart+24(FP)

	// Right candidates are [cx+1, ..., cx+4] and [cx+5, ..., cx+8].
	MOVSS     roundedCenter+8(FP), X2
	CVTTSS2SQ X2, BX
	INCQ      BX                     // xEnd = rounded center + 1
	SHUFPS    $0x00, X2, X2
	MOVAPS    X2, X7
	MOVUPS    ·circleSSE2RightOffsets<>+0(SB), X6
	ADDPS     X6, X2
	MOVUPS    ·circleSSE2RightOffsets<>+16(SB), X6
	ADDPS     X6, X7

right_vector:
	LEAQ     7(BX), DI
	CMPQ     DI, SI
	JGE      right_scalar
	MOVAPS   X2, X3
	SUBPS    X0, X3
	MULPS    X3, X3
	MOVAPS   X7, X4
	SUBPS    X0, X4
	MULPS    X4, X4
	CMPPS    X1, X3, $2  // candidate squared <= remaining
	CMPPS    X1, X4, $2
	MOVMSKPS X3, DI
	MOVMSKPS X4, R8
	SHLL     $4, R8
	ORL      R8, DI
	CMPL     DI, $255
	JNE      right_partial
	ADDQ     $8, BX
	ADDPS    X5, X2
	ADDPS    X5, X7
	JMP      right_vector

right_partial:
	NOTL DI
	BSFL DI, DI  // number of consecutive inside lanes
	ADDQ DI, BX
	JMP  right_clamp

right_scalar:
	CMPQ    BX, SI
	JGE     right_clamp
	MOVAPS  X2, X3
	SUBSS   X0, X3
	MULSS   X3, X3
	UCOMISS X1, X3
	JHI     right_clamp
	INCQ    BX
	ADDSS   ·circleSSE2One<>(SB), X2
	JMP     right_scalar

right_clamp:
	CMPQ BX, SI
	JLE  done
	MOVQ SI, BX

done:
	MOVQ BX, xEnd+32(FP)
	RET
