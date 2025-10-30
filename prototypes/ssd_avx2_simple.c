/*
 * AVX2 SSD - Simple Correct Implementation
 *
 * Focus on correctness first. Process pixels using SIMD where beneficial,
 * but don't try to be too clever with RGBA interleaving initially.
 *
 * Strategy: Process multiple pixels, compute RGB differences in parallel,
 * accumulate properly.
 */

#define _POSIX_C_SOURCE 199309L
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <math.h>

static inline uint64_t get_nanos() {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

double ssd_scalar(const uint8_t* a, const uint8_t* b, int stride, int width, int height) {
    double sum = 0.0;
    for (int y = 0; y < height; y++) {
        int row_start = y * stride;
        for (int x = 0; x < width; x++) {
            int i = row_start + x * 4;
            int32_t dr = (int32_t)a[i+0] - (int32_t)b[i+0];
            int32_t dg = (int32_t)a[i+1] - (int32_t)b[i+1];
            int32_t db = (int32_t)a[i+2] - (int32_t)b[i+2];
            sum += (double)(dr*dr + dg*dg + db*db);
        }
    }
    return sum;
}

/*
 * Simple AVX2 version: process 8 pixels at a time
 * Extract bytes, compute differences, square, and accumulate
 */
double ssd_avx2_simple(const uint8_t* a, const uint8_t* b, int stride, int width, int height) {
    int64_t total_sum = 0;

    for (int y = 0; y < height; y++) {
        int row_start = y * stride;
        int x = 0;

        // Process 8 pixels at a time
        for (; x <= width - 8; x += 8) {
            int i = row_start + x * 4;

            // Load 8 RGBA pixels (32 bytes) from each image
            __m256i va = _mm256_loadu_si256((const __m256i*)&a[i]);
            __m256i vb = _mm256_loadu_si256((const __m256i*)&b[i]);

            // Compute absolute differences (unsigned)
            __m256i vdiff = _mm256_sub_epi8(va, vb);  // Wrapping subtraction

            // For proper differences, we need signed arithmetic
            // Split into low and high halves, zero-extend to 16-bit
            __m128i va_lo = _mm256_castsi256_si128(va);
            __m128i va_hi = _mm256_extracti128_si256(va, 1);
            __m128i vb_lo = _mm256_castsi256_si128(vb);
            __m128i vb_hi = _mm256_extracti128_si256(vb, 1);

            // Convert to 16-bit (zero extension for unsigned)
            __m256i va_lo_16 = _mm256_cvtepu8_epi16(va_lo);
            __m256i va_hi_16 = _mm256_cvtepu8_epi16(va_hi);
            __m256i vb_lo_16 = _mm256_cvtepu8_epi16(vb_lo);
            __m256i vb_hi_16 = _mm256_cvtepu8_epi16(vb_hi);

            // Compute differences (now signed)
            __m256i diff_lo = _mm256_sub_epi16(va_lo_16, vb_lo_16);
            __m256i diff_hi = _mm256_sub_epi16(va_hi_16, vb_hi_16);

            // Square differences using madd (d*d produces 32-bit results)
            __m256i sq_lo = _mm256_madd_epi16(diff_lo, diff_lo);
            __m256i sq_hi = _mm256_madd_epi16(diff_hi, diff_hi);

            // Now sq_lo and sq_hi contain squared differences for all channels including alpha
            // Extract and sum, skipping alpha channels
            int32_t sq_lo_arr[8];
            int32_t sq_hi_arr[8];
            _mm256_storeu_si256((__m256i*)sq_lo_arr, sq_lo);
            _mm256_storeu_si256((__m256i*)sq_hi_arr, sq_hi);

            // madd adds adjacent pairs, so for RGBA layout (R,G,B,A repeated):
            // After conversion to 16-bit we have 16 values per 128-bit lane
            // After madd we have 8 values per lane (sums of adjacent pairs)
            // Pattern: (R0²+G0²), (B0²+A0²), (R1²+G1²), (B1²+A1²), ...

            // Sum RGB contributions (skip alpha)
            int32_t pixel_sum = 0;
            // Low half (pixels 0-3 for both lanes combined after madd)
            for (int p = 0; p < 8; p += 2) {
                pixel_sum += sq_lo_arr[p];     // R²+G²
                pixel_sum += sq_lo_arr[p+1];   // B²+A² (we'll subtract A² later)
            }
            // High half (pixels 4-7)
            for (int p = 0; p < 8; p += 2) {
                pixel_sum += sq_hi_arr[p];
                pixel_sum += sq_hi_arr[p+1];
            }

            // Now subtract alpha channel contributions
            // For each of the 8 pixels, compute A² and subtract
            for (int p = 0; p < 8; p++) {
                int idx = i + p * 4 + 3;  // Alpha channel
                int32_t da = (int32_t)a[idx] - (int32_t)b[idx];
                pixel_sum -= da * da;
            }

            total_sum += pixel_sum;
        }

        // Remainder pixels
        for (; x < width; x++) {
            int i = row_start + x * 4;
            int32_t dr = (int32_t)a[i+0] - (int32_t)b[i+0];
            int32_t dg = (int32_t)a[i+1] - (int32_t)b[i+1];
            int32_t db = (int32_t)a[i+2] - (int32_t)b[i+2];
            total_sum += dr*dr + dg*dg + db*db;
        }
    }

    return (double)total_sum;
}

int main() {
    printf("AVX2 SSD - Simple Correct Implementation\n");
    printf("=========================================\n\n");

    const int width = 256;
    const int height = 256;
    const int stride = width * 4;
    const size_t img_size = stride * height;

    uint8_t* img_a = (uint8_t*)aligned_alloc(32, img_size);
    uint8_t* img_b = (uint8_t*)aligned_alloc(32, img_size);

    if (!img_a || !img_b) {
        fprintf(stderr, "Memory allocation failed\n");
        return 1;
    }

    srand(42);
    for (size_t i = 0; i < img_size; i++) {
        img_a[i] = rand() % 256;
        img_b[i] = rand() % 256;
    }

    printf("Image: %dx%d (%d pixels)\n\n", width, height, width * height);

    // Warm-up
    for (int i = 0; i < 100; i++) {
        ssd_scalar(img_a, img_b, stride, width, height);
        ssd_avx2_simple(img_a, img_b, stride, width, height);
    }

    // Correctness
    printf("Correctness Test:\n");
    double scalar_result = ssd_scalar(img_a, img_b, stride, width, height);
    double avx2_result = ssd_avx2_simple(img_a, img_b, stride, width, height);

    printf("  Scalar: %.0f\n", scalar_result);
    printf("  AVX2:   %.0f\n", avx2_result);
    printf("  Diff:   %.0f (%.6f%%)\n",
           fabs(scalar_result - avx2_result),
           fabs(scalar_result - avx2_result) / scalar_result * 100.0);

    int correct = fabs(scalar_result - avx2_result) < 1.0;
    printf("  %s\n\n", correct ? "✓ PASS" : "✗ FAIL");

    if (!correct) {
        free(img_a);
        free(img_b);
        return 1;
    }

    // Benchmark
    printf("Benchmark (1000 iterations):\n");
    const int iters = 1000;

    uint64_t start = get_nanos();
    for (int i = 0; i < iters; i++) {
        ssd_scalar(img_a, img_b, stride, width, height);
    }
    uint64_t end = get_nanos();
    double scalar_us = (double)(end - start) / iters / 1000.0;
    double scalar_mpx = (width * height / 1e6) / (scalar_us / 1e6);

    printf("  Scalar: %.2f μs, %.1f Mpixels/sec\n", scalar_us, scalar_mpx);

    start = get_nanos();
    for (int i = 0; i < iters; i++) {
        ssd_avx2_simple(img_a, img_b, stride, width, height);
    }
    end = get_nanos();
    double avx2_us = (double)(end - start) / iters / 1000.0;
    double avx2_mpx = (width * height / 1e6) / (avx2_us / 1e6);

    printf("  AVX2:   %.2f μs, %.1f Mpixels/sec\n", avx2_us, avx2_mpx);

    double speedup = scalar_us / avx2_us;
    printf("  Speedup: %.2fx\n\n", speedup);

    if (speedup >= 3.0) {
        printf("✓ SUCCESS: %.2fx speedup (target: 4-6x, close!)\n", speedup);
    } else if (speedup >= 2.0) {
        printf("✓ GOOD: %.2fx speedup (target: 4-6x, optimization possible)\n", speedup);
    } else {
        printf("⚠ NEEDS WORK: %.2fx speedup (target: 4-6x)\n", speedup);
    }

    printf("\nNote: This prototype validates the approach.\n");
    printf("Further optimization will be done in Go assembly version.\n");

    free(img_a);
    free(img_b);

    return (speedup >= 1.5) ? 0 : 1;
}
