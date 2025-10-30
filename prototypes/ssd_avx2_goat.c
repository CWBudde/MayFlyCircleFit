/*
 * AVX2 SSD Kernel - For GoAT transpilation only
 * Minimal dependencies, manual type definitions
 */

#include <immintrin.h>

// Manual type definitions to avoid system headers
typedef unsigned char uint8_t;
typedef int int32_t;
typedef long long int64_t;

double ssd_avx2(const uint8_t* a, const uint8_t* b, int stride, int width, int height) {
    int64_t total_sum = 0;

    for (int y = 0; y < height; y++) {
        int row_start = y * stride;
        int x = 0;

        int simd_width = (width / 8) * 8;

        for (; x < simd_width; x += 8) {
            int i = row_start + x * 4;

            __m256i va = _mm256_loadu_si256((__m256i*)&a[i]);
            __m256i vb = _mm256_loadu_si256((__m256i*)&b[i]);

            __m256i va_lo = _mm256_unpacklo_epi8(va, _mm256_setzero_si256());
            __m256i vb_lo = _mm256_unpacklo_epi8(vb, _mm256_setzero_si256());

            __m256i va_hi = _mm256_unpackhi_epi8(va, _mm256_setzero_si256());
            __m256i vb_hi = _mm256_unpackhi_epi8(vb, _mm256_setzero_si256());

            __m256i diff_lo = _mm256_sub_epi16(va_lo, vb_lo);
            __m256i diff_hi = _mm256_sub_epi16(va_hi, vb_hi);

            __m256i sq_lo = _mm256_madd_epi16(diff_lo, diff_lo);
            __m256i sq_hi = _mm256_madd_epi16(diff_hi, diff_hi);

            int32_t sq_lo_arr[8];
            int32_t sq_hi_arr[8];
            _mm256_storeu_si256((__m256i*)sq_lo_arr, sq_lo);
            _mm256_storeu_si256((__m256i*)sq_hi_arr, sq_hi);

            for (int j = 0; j < 8; j++) {
                total_sum += sq_lo_arr[j];
                total_sum += sq_hi_arr[j];
            }

            uint8_t a_bytes[32];
            uint8_t b_bytes[32];
            _mm256_storeu_si256((__m256i*)a_bytes, va);
            _mm256_storeu_si256((__m256i*)b_bytes, vb);

            int32_t pixel_sum = 0;
            for (int p = 0; p < 8; p++) {
                int idx = p * 4;
                int32_t dr = (int32_t)a_bytes[idx+0] - (int32_t)b_bytes[idx+0];
                int32_t dg = (int32_t)a_bytes[idx+1] - (int32_t)b_bytes[idx+1];
                int32_t db = (int32_t)a_bytes[idx+2] - (int32_t)b_bytes[idx+2];
                pixel_sum += dr*dr + dg*dg + db*db;
            }

            total_sum -= (sq_lo_arr[0] + sq_lo_arr[1] + sq_lo_arr[2] + sq_lo_arr[3] +
                         sq_lo_arr[4] + sq_lo_arr[5] + sq_lo_arr[6] + sq_lo_arr[7] +
                         sq_hi_arr[0] + sq_hi_arr[1] + sq_hi_arr[2] + sq_hi_arr[3] +
                         sq_hi_arr[4] + sq_hi_arr[5] + sq_hi_arr[6] + sq_hi_arr[7]);
            total_sum += pixel_sum;
        }

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
