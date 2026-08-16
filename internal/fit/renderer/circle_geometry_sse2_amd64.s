// SSE2 float32 circle span-edge search.
//
// This is the 128-bit fallback for amd64 CPUs without AVX2, which would
// otherwise run the fully scalar edge search. Circle distance is monotonic from
// the rounded center, so a comparison mask over eight candidates ordered
// nearest-to-farthest directly identifies the exact edge.
//
// The AVX2 kernel scans with a full eight-lane vector per iteration. A direct
// four-lane SSE2 transliteration of that loop was written and measured first
// and it lost to the scalar search from radius 25 upwards, because the scalar
// search already advances eight pixels per single comparison while a four-lane
// vector needs two iterations plus roughly eighteen instructions to cover the
// same eight pixels. Widening the SSE2 loop to two XMM vectors per iteration
// helped but still lost at large radii for the same instruction-count reason.
//
// This kernel therefore splits the two jobs. The coarse scan keeps the scalar
// eight-pixel stride and its single scalar comparison, so it costs what the
// scalar search costs. SSE2 is then used exactly once per edge, for the part
// the scalar search is bad at: after the coarse scan stops, the eight pixels
// that bracket the edge are compared as one or two four-lane vectors and the
// mask locates the edge directly, replacing up to seven dependent scalar
// iterations. Both edges stay bit-identical to circleSpanFloat32.
//
// Plan 9 SSE mnemonics are two-operand and destructive, so comparisons are
// restructured with explicit MOVAPS copies. Scalar broadcasts use MOVSS plus
// SHUFPS $0x00 because VBROADCASTSS is AVX-only. The offset tables are loaded
// with MOVUPS instead of being used as memory operands so the kernel does not
// depend on 16-byte alignment of the RODATA symbols.
//
// There is deliberately no SSE2 counterpart of circleSpanQ16AVX2Kernel: that
// kernel compares Q32.32 products with VPCMPGTQ, and SSE2 has no 64-bit signed
// compare. See circle_geometry_amd64.go for the measured reasoning.

#include "textflag.h"

// Left refinement offsets relative to the first outside candidate at cx-8.
// Lanes are ordered nearest-to-farthest: cx-1, cx-2, ... cx-8.
DATA ·circleSSE2LeftRefine<>+0(SB)/4, $0x40e00000  // +7 -> cx-1
DATA ·circleSSE2LeftRefine<>+4(SB)/4, $0x40c00000  // +6 -> cx-2
DATA ·circleSSE2LeftRefine<>+8(SB)/4, $0x40a00000  // +5 -> cx-3
DATA ·circleSSE2LeftRefine<>+12(SB)/4, $0x40800000 // +4 -> cx-4
DATA ·circleSSE2LeftRefine<>+16(SB)/4, $0x40400000 // +3 -> cx-5
DATA ·circleSSE2LeftRefine<>+20(SB)/4, $0x40000000 // +2 -> cx-6
DATA ·circleSSE2LeftRefine<>+24(SB)/4, $0x3f800000 // +1 -> cx-7
DATA ·circleSSE2LeftRefine<>+28(SB)/4, $0x00000000 // +0 -> cx-8
GLOBL ·circleSSE2LeftRefine<>(SB), RODATA|NOPTR, $32

// Right refinement offsets relative to the first outside candidate at xEnd+7.
// Lanes are ordered nearest-to-farthest: xEnd+0, xEnd+1, ... xEnd+7.
DATA ·circleSSE2RightRefine<>+0(SB)/4, $0xc0e00000  // -7 -> xEnd+0
DATA ·circleSSE2RightRefine<>+4(SB)/4, $0xc0c00000  // -6 -> xEnd+1
DATA ·circleSSE2RightRefine<>+8(SB)/4, $0xc0a00000  // -5 -> xEnd+2
DATA ·circleSSE2RightRefine<>+12(SB)/4, $0xc0800000 // -4 -> xEnd+3
DATA ·circleSSE2RightRefine<>+16(SB)/4, $0xc0400000 // -3 -> xEnd+4
DATA ·circleSSE2RightRefine<>+20(SB)/4, $0xc0000000 // -2 -> xEnd+5
DATA ·circleSSE2RightRefine<>+24(SB)/4, $0xbf800000 // -1 -> xEnd+6
DATA ·circleSSE2RightRefine<>+28(SB)/4, $0x00000000 // -0 -> xEnd+7
GLOBL ·circleSSE2RightRefine<>(SB), RODATA|NOPTR, $32

DATA ·circleSSE2One<>+0(SB)/4, $0x3f800000
GLOBL ·circleSSE2One<>(SB), RODATA|NOPTR, $4

// func circleSpanFloat32SSE2Kernel(centerX, radiusSquaredMinusDY,
//     roundedCenter float32, width int) (xStart, xEnd int)
TEXT ·circleSpanFloat32SSE2Kernel(SB), NOSPLIT, $0-40
	MOVSS  centerX+0(FP), X0
	SHUFPS $0x00, X0, X0
	MOVSS  radiusSquaredMinusDY+4(FP), X1
	SHUFPS $0x00, X1, X1
	MOVQ   width+16(FP), SI

	MOVSS     roundedCenter+8(FP), X2
	CVTTSS2SQ X2, AX                  // xStart = rounded center

	// The coarse scan keeps its candidate in an integer register and converts
	// per iteration. Carrying a float32 candidate and subtracting eight from it
	// instead would put a four-cycle SUBSS in the loop-carried dependency chain,
	// which measured slower than the scalar search at large radii. The XORPS
	// breaks CVTSQ2SS's false dependency on its destination register, which
	// otherwise serializes the whole float chain across iterations and measured
	// slower still. The loop test sits at the bottom so the back edge is the
	// only extra branch, matching the instruction sequence the Go compiler
	// emits for the scalar batch loop.
	CMPQ AX, $8
	JLT  left_scalar

left_coarse:
	LEAQ     -8(AX), DX
	XORPS    X3, X3
	CVTSQ2SS DX, X3
	SUBSS    X0, X3
	MULSS    X3, X3
	UCOMISS  X1, X3
	JHI      left_refine
	MOVQ     DX, AX
	CMPQ     AX, $8
	JGE      left_coarse
	JMP      left_scalar

left_refine:
	// xStart >= 8 and pixel xStart-8 is outside, so the edge lies in
	// [xStart-7, xStart]. The four nearest candidates resolve it whenever fewer
	// than four of them are inside, which is the common case; only a full mask
	// needs the second vector, whose last lane is known outside.
	CVTSQ2SS DX, X2      // DX is xStart-8
	SHUFPS   $0x00, X2, X2
	MOVAPS   X2, X7
	MOVUPS   ·circleSSE2LeftRefine<>+0(SB), X6
	ADDPS    X6, X2
	SUBPS    X0, X2
	MULPS    X2, X2
	CMPPS    X1, X2, $2  // candidate squared <= remaining
	MOVMSKPS X2, DI
	CMPL     DI, $15
	JEQ      left_refine_far
	NOTL     DI
	BSFL     DI, DI      // number of consecutive inside lanes
	SUBQ     DI, AX
	JMP      left_done

left_refine_far:
	MOVUPS   ·circleSSE2LeftRefine<>+16(SB), X6
	ADDPS    X6, X7
	SUBPS    X0, X7
	MULPS    X7, X7
	CMPPS    X1, X7, $2
	MOVMSKPS X7, DI
	NOTL     DI
	BSFL     DI, DI
	ADDQ     $4, DI
	SUBQ     DI, AX
	JMP      left_done

left_scalar:
	// Fewer than eight pixels remain to the left clip edge.
	LEAQ     -1(AX), DX
	XORPS    X2, X2
	CVTSQ2SS DX, X2  // X2 lane zero is float32(xStart-1)

left_scalar_loop:
	CMPQ    AX, $0
	JLE     left_done
	MOVAPS  X2, X3
	SUBSS   X0, X3
	MULSS   X3, X3
	UCOMISS X1, X3
	JHI     left_done
	DECQ    AX
	SUBSS   ·circleSSE2One<>(SB), X2
	JMP     left_scalar_loop

left_done:
	MOVQ AX, xStart+24(FP)

	MOVSS     roundedCenter+8(FP), X2
	CVTTSS2SQ X2, BX
	INCQ      BX                      // xEnd = rounded center + 1

	LEAQ 7(BX), DX
	CMPQ DX, SI
	JGE  right_scalar

right_coarse:
	XORPS    X3, X3
	CVTSQ2SS DX, X3
	SUBSS    X0, X3
	MULSS    X3, X3
	UCOMISS  X1, X3
	JHI      right_refine
	ADDQ     $8, BX
	LEAQ     7(BX), DX
	CMPQ     DX, SI
	JLT      right_coarse
	JMP      right_scalar

right_refine:
	// xEnd+7 is inside the raster and outside the circle, so the edge lies in
	// [xEnd, xEnd+7] and needs no clamping.
	CVTSQ2SS DX, X2      // DX is xEnd+7
	SHUFPS   $0x00, X2, X2
	MOVAPS   X2, X7
	MOVUPS   ·circleSSE2RightRefine<>+0(SB), X6
	ADDPS    X6, X2
	SUBPS    X0, X2
	MULPS    X2, X2
	CMPPS    X1, X2, $2  // candidate squared <= remaining
	MOVMSKPS X2, DI
	CMPL     DI, $15
	JEQ      right_refine_far
	NOTL     DI
	BSFL     DI, DI      // number of consecutive inside lanes
	ADDQ     DI, BX
	JMP      done

right_refine_far:
	MOVUPS   ·circleSSE2RightRefine<>+16(SB), X6
	ADDPS    X6, X7
	SUBPS    X0, X7
	MULPS    X7, X7
	CMPPS    X1, X7, $2
	MOVMSKPS X7, DI
	NOTL     DI
	BSFL     DI, DI
	ADDQ     $4, DI
	ADDQ     DI, BX
	JMP      done

right_scalar:
	// Fewer than eight pixels remain to the right clip edge.
	XORPS    X2, X2
	CVTSQ2SS BX, X2  // X2 lane zero is float32(xEnd)

right_scalar_loop:
	CMPQ    BX, SI
	JGE     right_clamp
	MOVAPS  X2, X3
	SUBSS   X0, X3
	MULSS   X3, X3
	UCOMISS X1, X3
	JHI     right_clamp
	INCQ    BX
	ADDSS   ·circleSSE2One<>(SB), X2
	JMP     right_scalar_loop

right_clamp:
	CMPQ BX, SI
	JLE  done
	MOVQ SI, BX

done:
	MOVQ BX, xEnd+32(FP)
	RET
