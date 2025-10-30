// Code generated from prototypes/ssd_avx2.c - DO NOT EDIT (except for tuning)
// AVX2 SIMD implementation of SSD (Sum of Squared Differences) for RGBA images
//
// Function signature: func ssdAVX2(a, b *uint8, stride, width, height int) float64
//
// Algorithm:
//   - Process 8 RGBA pixels (32 bytes) per iteration using AVX2
//   - Compute RGB channel differences (ignore alpha)
//   - Square differences and accumulate
//   - Handle remainder pixels with scalar loop
//
// Performance target: 4-6x speedup over scalar baseline
// Expected throughput: 1.2-2.0 Gpixels/sec

#include "textflag.h"

// func ssdAVX2(a, b *uint8, stride, width, height int) float64
TEXT ·ssdAVX2(SB), NOSPLIT, $128-48
    // Stack frame layout:
    // SP+0:     local storage for pixel computations (128 bytes)
    // SP+128:   arguments (stack-based calling convention)
    //   FP+0:   a *uint8 (8 bytes)
    //   FP+8:   b *uint8 (8 bytes)
    //   FP+16:  stride int (8 bytes)
    //   FP+24:  width int (8 bytes)
    //   FP+32:  height int (8 bytes)
    //   FP+40:  return float64 (8 bytes)
    //
    // Total argument+return size: 48 bytes

    // Load parameters from stack to callee-saved registers
    MOVQ a+0(FP), R8       // R8 = a (pointer)
    MOVQ b+8(FP), R9       // R9 = b (pointer)
    MOVQ stride+16(FP), R10   // R10 = stride
    MOVQ width+24(FP), R11    // R11 = width
    MOVQ height+32(FP), R12   // R12 = height

    // Initialize accumulator
    XORQ R13, R13      // R13 = total_sum (int64)

    // Zero out YMM registers for unpacking
    VPXOR Y15, Y15, Y15  // Y15 = 0 (for unpacking)

    // Outer loop: for (y = 0; y < height; y++)
    XORQ R14, R14      // R14 = y = 0

outer_loop:
    CMPQ R14, R12      // if y >= height
    JGE done           // goto done

    // row_start = y * stride
    MOVQ R14, R15
    IMULQ R10, R15     // R15 = row_start = y * stride

    // Inner loop setup: x = 0
    XORQ DX, DX        // DX = x = 0

    // simd_width = (width / 8) * 8
    MOVQ R11, SI
    SHRQ $3, SI        // SI = width / 8
    SHLQ $3, SI        // SI = (width / 8) * 8 = simd_width

inner_loop_simd:
    CMPQ DX, SI        // if x >= simd_width
    JGE inner_loop_remainder  // goto remainder

    // i = row_start + x * 4
    MOVQ DX, DI
    SHLQ $2, DI        // DI = x * 4
    ADDQ R15, DI       // DI = i = row_start + x * 4

    // Load 8 RGBA pixels (32 bytes) from each image
    // va = _mm256_loadu_si256((__m256i*)&a[i])
    LEAQ (R8)(DI*1), AX    // AX = &a[i]
    VMOVDQU (AX), Y0       // Y0 = va (8 pixels from a)

    // vb = _mm256_loadu_si256((__m256i*)&b[i])
    LEAQ (R9)(DI*1), BX    // BX = &b[i]
    VMOVDQU (BX), Y1       // Y1 = vb (8 pixels from b)

    // Store loaded values for RGB extraction
    // We need to extract individual pixels and compute RGB differences
    // Store Y0 (va) to stack
    VMOVDQU Y0, 0(SP)      // Store va to SP+0
    VMOVDQU Y1, 32(SP)     // Store vb to SP+32

    // Process 8 pixels: compute dr*dr + dg*dg + db*db for each
    XORQ CX, CX            // CX = pixel_sum for this iteration

    // Unroll pixel processing loop
    // For each pixel p in 0..7:
    //   idx = p * 4
    //   dr = a[idx+0] - b[idx+0]
    //   dg = a[idx+1] - b[idx+1]
    //   db = a[idx+2] - b[idx+2]
    //   pixel_sum += dr*dr + dg*dg + db*db

    // Pixel 0
    MOVBQZX 0(SP), AX       // a[0] (R)
    MOVBQZX 32(SP), BX      // b[0] (R)
    SUBQ BX, AX             // dr
    IMULQ AX, AX            // dr*dr
    ADDQ AX, CX

    MOVBQZX 1(SP), AX       // a[1] (G)
    MOVBQZX 33(SP), BX      // b[1] (G)
    SUBQ BX, AX             // dg
    IMULQ AX, AX            // dg*dg
    ADDQ AX, CX

    MOVBQZX 2(SP), AX       // a[2] (B)
    MOVBQZX 34(SP), BX      // b[2] (B)
    SUBQ BX, AX             // db
    IMULQ AX, AX            // db*db
    ADDQ AX, CX

    // Pixel 1
    MOVBQZX 4(SP), AX
    MOVBQZX 36(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 5(SP), AX
    MOVBQZX 37(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 6(SP), AX
    MOVBQZX 38(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    // Pixel 2
    MOVBQZX 8(SP), AX
    MOVBQZX 40(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 9(SP), AX
    MOVBQZX 41(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 10(SP), AX
    MOVBQZX 42(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    // Pixel 3
    MOVBQZX 12(SP), AX
    MOVBQZX 44(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 13(SP), AX
    MOVBQZX 45(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 14(SP), AX
    MOVBQZX 46(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    // Pixel 4
    MOVBQZX 16(SP), AX
    MOVBQZX 48(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 17(SP), AX
    MOVBQZX 49(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 18(SP), AX
    MOVBQZX 50(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    // Pixel 5
    MOVBQZX 20(SP), AX
    MOVBQZX 52(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 21(SP), AX
    MOVBQZX 53(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 22(SP), AX
    MOVBQZX 54(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    // Pixel 6
    MOVBQZX 24(SP), AX
    MOVBQZX 56(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 25(SP), AX
    MOVBQZX 57(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 26(SP), AX
    MOVBQZX 58(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    // Pixel 7
    MOVBQZX 28(SP), AX
    MOVBQZX 60(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 29(SP), AX
    MOVBQZX 61(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    MOVBQZX 30(SP), AX
    MOVBQZX 62(SP), BX
    SUBQ BX, AX
    IMULQ AX, AX
    ADDQ AX, CX

    // Accumulate pixel_sum into total_sum
    ADDQ CX, R13

    // x += 8
    ADDQ $8, DX
    JMP inner_loop_simd

inner_loop_remainder:
    // Process remainder pixels: for (; x < width; x++)
    CMPQ DX, R11       // if x >= width
    JGE next_row       // goto next_row

    // i = row_start + x * 4
    MOVQ DX, DI
    SHLQ $2, DI
    ADDQ R15, DI

    // dr = a[i+0] - b[i+0]
    LEAQ (R8)(DI*1), AX
    MOVBQZX 0(AX), CX
    LEAQ (R9)(DI*1), BX
    MOVBQZX 0(BX), SI
    SUBQ SI, CX
    IMULQ CX, CX       // dr*dr

    // dg = a[i+1] - b[i+1]
    MOVBQZX 1(AX), SI
    MOVBQZX 1(BX), DI
    SUBQ DI, SI
    IMULQ SI, SI       // dg*dg
    ADDQ SI, CX

    // db = a[i+2] - b[i+2]
    MOVBQZX 2(AX), SI
    MOVBQZX 2(BX), DI
    SUBQ DI, SI
    IMULQ SI, SI       // db*db
    ADDQ SI, CX

    // total_sum += dr*dr + dg*dg + db*db
    ADDQ CX, R13

    // x++
    INCQ DX
    JMP inner_loop_remainder

next_row:
    // y++
    INCQ R14
    JMP outer_loop

done:
    // Convert int64 total_sum to float64 and return in X0
    CVTSQ2SD R13, X0

    // DEBUG: Return result via both X0 and stack (for compatibility)
    MOVSD X0, ret+40(FP)
    RET
