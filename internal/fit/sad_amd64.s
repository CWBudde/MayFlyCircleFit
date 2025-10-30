// SAD (Sum of Absolute Differences) with Quadratic Weighting - AVX2 Implementation
// Vectorized version processing 8 pixels per iteration
//
// Algorithm: For each pixel, compute value = |R1-R2| + |G1-G2| + |B1-B2|
//            Then apply quadratic weighting: scale × value × (255 + 9×value)

#include "textflag.h"

// func sadAVX2(a, b *uint8, stride, width, height int) float64
TEXT ·sadAVX2(SB), NOSPLIT, $128-48
    // Load parameters from stack
    MOVQ a+0(FP), R8          // R8 = a (pointer)
    MOVQ b+8(FP), R9          // R9 = b (pointer)
    MOVQ stride+16(FP), R10   // R10 = stride
    MOVQ width+24(FP), R11    // R11 = width
    MOVQ height+32(FP), R12   // R12 = height

    // Initialize accumulator for total cost (4 x double)
    VXORPD Y0, Y0, Y0          // Y0 = [0.0, 0.0, 0.0, 0.0]

    // Create alpha mask: 0xFF for RGB, 0x00 for Alpha, repeated 8 times
    // Each quadword contains 2 pixels: 0x00FFFFFF00FFFFFF
    MOVQ $0x00FFFFFF00FFFFFF, AX
    MOVQ AX, 0(SP)
    MOVQ AX, 8(SP)
    MOVQ AX, 16(SP)
    MOVQ AX, 24(SP)
    VMOVDQU 0(SP), Y15         // Y15 = alpha mask

    // Create PMADDUBSW multiplier: [1,1,1,0, 1,1,1,0, ...] (signed bytes)
    // This sums R+G in one word, B+0 in another word per pixel
    MOVQ $0x0001010100010101, AX
    MOVQ AX, 32(SP)
    MOVQ AX, 40(SP)
    MOVQ AX, 48(SP)
    MOVQ AX, 56(SP)

    // Create PMADDWD multiplier: [1,1, 1,1, ...] (signed words)
    // This adds the two words from PMADDUBSW: (R+G) + B
    MOVL $0x00010001, AX
    MOVL AX, 64(SP)
    MOVL AX, 68(SP)
    MOVL AX, 72(SP)
    MOVL AX, 76(SP)
    MOVL AX, 80(SP)
    MOVL AX, 84(SP)
    MOVL AX, 88(SP)
    MOVL AX, 92(SP)

    // Outer loop: for (y = 0; y < height; y++)
    XORQ R14, R14              // R14 = y = 0

outer_loop:
    CMPQ R14, R12              // if y >= height
    JGE done                   // goto done

    // row_start = y * stride
    MOVQ R14, R15
    IMULQ R10, R15             // R15 = row_start

    // x = 0
    XORQ R13, R13              // R13 = x = 0

    // Check if we can process at least 8 pixels
    CMPQ R11, $8
    JL scalar_row              // If width < 8, use scalar

vectorized_loop:
    // Check if we have at least 8 pixels remaining
    MOVQ R11, SI
    SUBQ R13, SI               // SI = width - x (remaining pixels)
    CMPQ SI, $8
    JL scalar_remainder        // If < 8 remaining, handle remainder

    // Calculate byte offset: i = row_start + x * 4
    MOVQ R13, DI
    SHLQ $2, DI                // DI = x * 4
    ADDQ R15, DI               // DI = row_start + x*4

    // Load 8 pixels (32 bytes) from each image
    VMOVDQU (R8)(DI*1), Y2     // Y2 = 8 pixels from image a
    VMOVDQU (R9)(DI*1), Y3     // Y3 = 8 pixels from image b

    // Compute absolute differences using min/max trick
    VPMINUB Y3, Y2, Y4         // Y4 = min(Y2, Y3)
    VPMAXUB Y3, Y2, Y5         // Y5 = max(Y2, Y3)
    VPSUBB Y4, Y5, Y6          // Y6 = |a-b|

    // Mask out alpha channel
    VPAND Y15, Y6, Y6          // Y6 = differences with alpha masked

    // Y6 now contains: [R0 G0 B0 00 R1 G1 B1 00 ... R7 G7 B7 00]
    // Sum RGB per pixel using PMADDUBSW
    // Multiplier: [1,1,1,0, 1,1,1,0, ...] gives (R+G), B per pair
    VPMADDUBSW 32(SP), Y6, Y7  // Y7 = [(R0+G0), B0, (R1+G1), B1, ...]

    // Add the pairs: (R+G) + B = R+G+B per pixel
    VPMADDWD 64(SP), Y7, Y8    // Y8 = 8 x 32-bit values (R+G+B per pixel)

    // Y8 now contains 8 dwords: [value0, value1, ..., value7]
    // Apply quadratic: result = value × (255 + 9×value)

    // Compute 9×value
    VPMULLD Y8, Y8, Y9         // Y9 = value²
    MOVQ $9, AX
    VMOVD AX, X10
    VPBROADCASTD X10, Y10      // Y10 = [9, 9, 9, 9, 9, 9, 9, 9]
    VPMULLD Y10, Y9, Y9        // Y9 = 9×value²

    // Compute 255×value
    MOVQ $255, AX
    VMOVD AX, X11
    VPBROADCASTD X11, Y11      // Y11 = [255, 255, ...]
    VPMULLD Y11, Y8, Y12       // Y12 = 255×value

    // Add: 255×value + 9×value²
    VPADDD Y9, Y12, Y13        // Y13 = weighted values (8 x dword)

    // Convert to double and accumulate
    // Process lower 4 dwords
    VEXTRACTI128 $0, Y13, X14
    VCVTDQ2PD X14, Y14         // Y14 = 4 doubles from lower half
    VADDPD Y14, Y0, Y0         // Accumulate into Y0

    // Process upper 4 dwords
    VEXTRACTI128 $1, Y13, X14
    VCVTDQ2PD X14, Y14         // Y14 = 4 doubles from upper half
    VADDPD Y14, Y0, Y0         // Accumulate into Y0

    // Advance by 8 pixels
    ADDQ $8, R13
    JMP vectorized_loop

scalar_remainder:
    // Process remaining pixels (< 8) with scalar code
    CMPQ R13, R11
    JGE next_row

scalar_row:
    // Scalar processing for remainder or small widths
scalar_loop:
    CMPQ R13, R11
    JGE next_row

    // i = row_start + x * 4
    MOVQ R13, DI
    SHLQ $2, DI
    ADDQ R15, DI

    // Load pixel from a
    MOVL (R8)(DI*1), CX

    // Load pixel from b
    MOVL (R9)(DI*1), SI

    // Mask alpha
    ANDL $0x00FFFFFF, CX
    ANDL $0x00FFFFFF, SI

    // Compute absolute differences per channel
    XORQ BP, BP                // BP = sad_value

    // R channel
    MOVL CX, AX
    ANDL $0xFF, AX
    MOVL SI, BX
    ANDL $0xFF, BX
    SUBL BX, AX
    // Absolute value using sign bit trick
    MOVL AX, BX
    SARL $31, BX
    XORL BX, AX
    SUBL BX, AX
    ADDQ AX, BP

    // G channel
    MOVL CX, AX
    SHRL $8, AX
    ANDL $0xFF, AX
    MOVL SI, BX
    SHRL $8, BX
    ANDL $0xFF, BX
    SUBL BX, AX
    MOVL AX, BX
    SARL $31, BX
    XORL BX, AX
    SUBL BX, AX
    ADDQ AX, BP

    // B channel
    MOVL CX, AX
    SHRL $16, AX
    ANDL $0xFF, AX
    MOVL SI, BX
    SHRL $16, BX
    ANDL $0xFF, BX
    SUBL BX, AX
    MOVL AX, BX
    SARL $31, BX
    XORL BX, AX
    SUBL BX, AX
    ADDQ AX, BP

    // Apply quadratic weighting to BP
    TESTQ BP, BP
    JZ skip_scalar

    MOVQ BP, AX
    IMULQ $9, AX
    ADDQ $255, AX
    IMULQ BP, AX
    CVTSQ2SD AX, X1
    ADDSD X1, X0

skip_scalar:
    INCQ R13
    JMP scalar_loop

next_row:
    INCQ R14
    JMP outer_loop

done:
    // Horizontally sum Y0 which contains 4 doubles
    VEXTRACTF128 $1, Y0, X1    // X1 = upper 2 doubles
    VADDPD X1, X0, X0          // X0 = lower 2 doubles + upper 2 doubles
    VHADDPD X0, X0, X0         // X0 = horizontal sum of 2 doubles

    // Apply final scale factor: CScale = 1.5378700499807766243752402921953E-6
    MOVQ $0x3EB9CD1A00809A4E, AX
    MOVQ AX, 96(SP)
    MOVSD 96(SP), X1           // X1 = CScale
    MULSD X1, X0               // X0 *= CScale

    // Store result to return location
    MOVSD X0, ret+40(FP)
    RET
