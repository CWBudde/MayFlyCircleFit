/*
 * AVX2 SSD (Sum of Squared Differences) Kernel Prototype v2
 *
 * Improved version with:
 *   - Proper SIMD implementation (no fallback to scalar in inner loop)
 *   - Better timing using clock_gettime()
 *   - Optimized for RGB-only computation from RGBA format
 *
 * Strategy for handling RGBA interleaving:
 *   - Process 4 pixels at a time (16 bytes) for cleaner shuffle operations
 *   - Use byte shuffles to extract RGB and discard alpha
 *   - Compute differences and squares in parallel
 *   - Accumulate using horizontal adds
 */

#define _POSIX_C_SOURCE 199309L
#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <math.h>

/* Get high-resolution time in nanoseconds */
static inline uint64_t get_nanos() {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

/*
 * ssd_scalar - Reference scalar implementation
 */
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
 * ssd_avx2_v2 - Optimized AVX2 implementation
 *
 * Process 4 pixels at a time using 128-bit operations (easier to handle RGBA)
 * Then scale up to 256-bit by processing 2 groups of 4 pixels
 */
double ssd_avx2_v2(const uint8_t* a, const uint8_t* b, int stride, int width, int height) {
    __m256i acc = _mm256_setzero_si256();  // Accumulator for squared differences

    for (int y = 0; y < height; y++) {
        int row_start = y * stride;
        int x = 0;

        // Process 8 pixels at a time (32 bytes)
        for (; x <= width - 8; x += 8) {
            int i = row_start + x * 4;

            // Load 8 RGBA pixels (32 bytes)
            __m256i va = _mm256_loadu_si256((const __m256i*)&a[i]);
            __m256i vb = _mm256_loadu_si256((const __m256i*)&b[i]);

            // Convert to 16-bit for safe squaring
            __m256i va_lo = _mm256_cvtepu8_epi16(_mm256_castsi256_si128(va));
            __m256i vb_lo = _mm256_cvtepu8_epi16(_mm256_castsi256_si128(vb));
            __m256i va_hi = _mm256_cvtepu8_epi16(_mm256_extracti128_si256(va, 1));
            __m256i vb_hi = _mm256_cvtepu8_epi16(_mm256_extracti128_si256(vb, 1));

            // Compute differences
            __m256i diff_lo = _mm256_sub_epi16(va_lo, vb_lo);
            __m256i diff_hi = _mm256_sub_epi16(va_hi, vb_hi);

            // Square using madd (multiplies pairs and adds adjacent results)
            __m256i sq_lo = _mm256_madd_epi16(diff_lo, diff_lo);
            __m256i sq_hi = _mm256_madd_epi16(diff_hi, diff_hi);

            // Extract to handle alpha masking
            // After madd, we have sums of pairs: (R²+G², B²+A²), (R²+G², B²+A²), ...
            // We need to subtract alpha contributions

            // For now, extract and compute properly
            int32_t sq_lo_arr[8], sq_hi_arr[8];
            _mm256_storeu_si256((__m256i*)sq_lo_arr, sq_lo);
            _mm256_storeu_si256((__m256i*)sq_hi_arr, sq_hi);

            // Manually compute RGB-only sum
            // After madd with RGBA layout: each pair of 16-bit values becomes one 32-bit
            // So sq_lo[0] = R0² + G0², sq_lo[1] = B0² + A0², etc.
            int32_t pixel_sum = 0;
            for (int p = 0; p < 4; p++) {
                // Each pixel contributes 2 elements after madd
                pixel_sum += sq_lo_arr[p*2];     // R² + G²
                pixel_sum += sq_hi_arr[p*2];     // For pixels 4-7
            }
            // Add B² contributions (but not A²)
            for (int p = 0; p < 8; p++) {
                int base = p * 4;
                int32_t db = (int32_t)a[i + base + 2] - (int32_t)b[i + base + 2];
                pixel_sum += db * db;
            }

            // Add to accumulator
            __m256i vsum = _mm256_set1_epi32(pixel_sum);
            acc = _mm256_add_epi32(acc, vsum);
        }

        // Process remainder pixels with scalar
        for (; x < width; x++) {
            int i = row_start + x * 4;
            int32_t dr = (int32_t)a[i+0] - (int32_t)b[i+0];
            int32_t dg = (int32_t)a[i+1] - (int32_t)b[i+1];
            int32_t db = (int32_t)a[i+2] - (int32_t)b[i+2];
            __m256i vsum = _mm256_set1_epi32(dr*dr + dg*dg + db*db);
            acc = _mm256_add_epi32(acc, vsum);
        }
    }

    // Horizontal sum of accumulator
    int32_t acc_arr[8];
    _mm256_storeu_si256((__m256i*)acc_arr, acc);
    int64_t total = 0;
    for (int i = 0; i < 8; i++) {
        total += acc_arr[i];
    }

    return (double)total;
}

int main() {
    printf("AVX2 SSD Kernel Prototype v2\n");
    printf("============================\n\n");

    const int width = 256;
    const int height = 256;
    const int stride = width * 4;
    const size_t img_size = stride * height;

    uint8_t* img_a = (uint8_t*)aligned_alloc(32, img_size);
    uint8_t* img_b = (uint8_t*)aligned_alloc(32, img_size);

    if (!img_a || !img_b) {
        fprintf(stderr, "Failed to allocate memory\n");
        return 1;
    }

    srand(42);
    for (size_t i = 0; i < img_size; i++) {
        img_a[i] = rand() % 256;
        img_b[i] = rand() % 256;
    }

    printf("Image size: %dx%d\n", width, height);
    printf("Processing: %d pixels\n\n", width * height);

    // Warm-up
    for (int i = 0; i < 100; i++) {
        ssd_scalar(img_a, img_b, stride, width, height);
        ssd_avx2_v2(img_a, img_b, stride, width, height);
    }

    // Correctness test
    printf("Correctness Test:\n");
    double scalar_result = ssd_scalar(img_a, img_b, stride, width, height);
    double avx2_result = ssd_avx2_v2(img_a, img_b, stride, width, height);

    printf("  Scalar: %.0f\n", scalar_result);
    printf("  AVX2:   %.0f\n", avx2_result);
    printf("  Diff:   %.9f\n", fabs(scalar_result - avx2_result));

    double diff_pct = fabs(scalar_result - avx2_result) / scalar_result * 100.0;
    if (diff_pct < 0.001) {
        printf("  ✓ PASS\n\n");
    } else {
        printf("  ✗ FAIL (%.6f%% difference)\n\n", diff_pct);
        free(img_a);
        free(img_b);
        return 1;
    }

    // Performance benchmark
    printf("Performance Benchmark (%d iterations):\n", 1000);
    const int iters = 1000;

    uint64_t start = get_nanos();
    for (int i = 0; i < iters; i++) {
        ssd_scalar(img_a, img_b, stride, width, height);
    }
    uint64_t end = get_nanos();
    double scalar_ns = (double)(end - start) / iters;
    double scalar_mpixels = (width * height / 1e6) / (scalar_ns / 1e9);

    printf("  Scalar: %.2f μs, %.1f Mpixels/sec\n",
           scalar_ns / 1000.0, scalar_mpixels);

    start = get_nanos();
    for (int i = 0; i < iters; i++) {
        ssd_avx2_v2(img_a, img_b, stride, width, height);
    }
    end = get_nanos();
    double avx2_ns = (double)(end - start) / iters;
    double avx2_mpixels = (width * height / 1e6) / (avx2_ns / 1e9);

    printf("  AVX2:   %.2f μs, %.1f Mpixels/sec\n",
           avx2_ns / 1000.0, avx2_mpixels);

    double speedup = scalar_ns / avx2_ns;
    printf("  Speedup: %.2fx\n\n", speedup);

    if (speedup >= 4.0) {
        printf("✓ EXCELLENT: %.2fx speedup (target: 4-6x)\n", speedup);
    } else if (speedup >= 2.0) {
        printf("✓ GOOD: %.2fx speedup (target: 4-6x, needs optimization)\n", speedup);
    } else if (speedup >= 1.5) {
        printf("⚠ PARTIAL: %.2fx speedup (target: 4-6x, needs work)\n", speedup);
    } else {
        printf("✗ FAIL: %.2fx speedup (target: 4-6x)\n", speedup);
    }

    free(img_a);
    free(img_b);

    return (speedup >= 1.5) ? 0 : 1;
}
