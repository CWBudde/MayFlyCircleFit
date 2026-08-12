# Task 10.4: AVX2 SSD Kernel

**Validated:** 2026-08-12  
**Hardware:** AMD Ryzen 5 4600H  
**Implementation:** hand-written Go Plan 9 assembly; no C, cgo, or GoAT build dependency

The prior assembly loaded eight pixels with AVX2 but performed every channel
square with scalar instructions. The revised loop is vectorized end to end:

- compute unsigned byte differences for eight NRGBA pixels;
- mask alpha and widen RGB differences to 16-bit lanes;
- square and pairwise-add with `VPMADDWD`;
- widen partial sums to 64-bit lanes to prevent accumulator overflow;
- process widths not divisible by eight with an exact scalar tail.

Direct comparisons with the pure-Go scalar oracle are bit-exact across aligned
and unaligned widths, padded strides, randomized images, alpha-only differences,
and a 512×512 maximum-difference case whose total exceeds 32-bit lanes.

## Benchmark

`go test ./internal/fit -run '^$' -bench 'BenchmarkFastSSD_Comparison' -benchtime=500ms -count=3`

| Image | Scalar | AVX2 | Approximate speedup |
| --- | ---: | ---: | ---: |
| 64×64 | 379–421 Mpixels/s | 2.33–2.53 Gpixels/s | 6.1× |
| 128×128 | 377–399 Mpixels/s | 2.46–2.57 Gpixels/s | 6.4× |
| 256×256 | 384–417 Mpixels/s | 2.41–2.55 Gpixels/s | 6.2× |
| 512×512 | 384–389 Mpixels/s | 2.33–2.55 Gpixels/s | 6.3× |

Numbers are local measurements, not portable guarantees. Runtime dispatch uses
AVX2 only when `golang.org/x/sys/cpu` reports support and otherwise selects the
portable scalar kernel. `VZEROUPPER` is issued before returning to Go code.
