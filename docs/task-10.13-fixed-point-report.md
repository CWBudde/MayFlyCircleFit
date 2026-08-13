# Task 10.13: fixed-point circle geometry (initial AMD64 results)

**Status:** in progress  
**Validated:** 2026-08-13
**Hardware:** AMD Ryzen 5 4600H, Linux/AMD64  
**Current production result:** 1.06× faster than batched exact `float64` in the
one-thread 512×512/K100 renderer

## Decision so far

Production CPU rendering now attempts signed Q16.16 geometry for each decoded
circle. Conversion rounds `X`, `Y`, and `R` to an `int32`; squares and finite
differences use `int64` Q32.32 values. A circle that cannot be represented
safely retains the prior `float64` scanline path.

Q16.16 was selected for the first implementation slice because it gives
1/65,536-pixel resolution over the useful signed range for ordinary images.
Pascal-style Q8.24 has excellent fractional precision but only about ±128 pixels
of direct coordinate range; using it for normal images would require normalized
coordinates and extra per-pixel scaling. Q24.8 has much wider range but only
1/256-pixel precision. Both remain explicit follow-up candidates rather than
being silently conflated with Q16.16.

All squared terms use the same Q32.32 scale. The earlier ticket sketch that
shifted an already-squared term only once was dimensionally invalid.

## Scalar batching before SIMD

The first direct fixed-point implementation was slower than `float64` because
it still issued one widened integer multiply per tested pixel. Two changes made
the integer path useful:

1. A center-out circle distance grows monotonically. If the eighth candidate
   is inside, the preceding seven candidates must also be inside, so the search
   advances by eight after one test.
2. The final zero-to-seven-pixel tail updates squared distance with first and
   second finite differences instead of multiplying again.

This is also the batching property used by the AVX2 float32 prototype. The
fixed-point path needs only the farthest scalar lane, while the AVX2 path tests
all eight float32 candidates and extracts a comparison mask to locate a partial
edge. Clipped fragments shorter than eight pixels use scalar VEX-encoded
float32 instructions.

The phrase "needs only the farthest scalar lane" does **not** mean that only
one eighth of the circle is calculated. It means that one comparison certifies
eight pixels: distance from the center is monotonic, so when candidate eight is
inside the circle, candidates one through seven are necessarily inside too.
The complete half-open span is still returned, including an exact zero-to-seven
pixel edge tail.

This shortcut is independent of number format. The scalar `float32` and
`float64` helpers now use it too, guarded below the first possible full batch so
small spans do not pay an unnecessary far-candidate comparison. A deterministic
100,000-case test verifies both batched helpers against their original
one-pixel searches with identical results.

## AVX2 Q16.16 follow-up

A hand-written AVX2 Q16.16 kernel now provides the literal eight-lane integer
comparison requested for evaluation. It is runtime-gated, and 100,000
randomized cases produce exactly the same spans and intersection decisions as
scalar Q16.16.

It is not selected for production on the Ryzen 5 4600H. AVX2 lacks a packed
eight-lane 32-by-32-to-64 signed multiply: `VPMULDQ` widens only the four even
32-bit lanes. The kernel therefore shifts the odd lanes into place, issues a
second multiply and comparison, then interleaves two four-bit masks. Scalar
Q16.16 needs just one farthest-candidate multiply per eight certified pixels.

Five 500 ms direct-span samples produced these medians:

| Radius | Scalar Q16.16 | AVX2 Q16.16 | AVX2/scalar |
| --- | ---: | ---: | ---: |
| 5.25 | 10.23 ns | 14.69 ns | 1.44× slower |
| 25.25 | 10.46 ns | 29.32 ns | 2.80× slower |
| 100.25 | 26.37 ns | 64.79 ns | 2.46× slower |
| 256.25 | 49.46 ns | 141.1 ns | 2.85× slower |

The useful implementation is therefore scalar monotonic batching, not SIMD
for its own sake. The AVX2 kernel remains as a correctness-tested benchmark
prototype and is not called by normal rendering.

## AVX2 float32 follow-up

The AMD64 prototype is hand-written Plan 9 assembly and runtime-gated with
`golang.org/x/sys/cpu`. It keeps all instructions VEX-encoded until
`VZEROUPPER`; mixing a legacy SSE load into the first prototype caused a severe
AVX-to-SSE transition penalty and was removed. A 100,000-case randomized test
proves the AVX2 span results are exact relative to the scalar float32 helper,
including clipped spans and batch boundaries.

Against the original one-pixel scalar search, SIMD was substantially faster.
After applying monotonic batching to scalar float32, the current direct
single-row comparison is:

| Radius | Scalar float32 | AVX2 float32 | Speedup |
| --- | ---: | ---: | ---: |
| 5.25 | ~11.1 ns | ~6.35 ns | AVX2 ~1.7× faster |
| 25.25 | ~10.6 ns | ~11.4 ns | effectively tied |
| 100.25 | ~26.1 ns | ~25.6 ns | effectively tied |
| 256.25 | ~44.2 ns | ~64.0 ns | scalar ~1.45× faster |

Across all intersecting rows, batched scalar float32 was 9-24% faster than its
AVX2 kernel in the measured R5-R256 cases. The direct-call advantage at small
radii is lost to per-row setup and call overhead; at large radii, the scalar
search certifies eight pixels with one farthest-candidate comparison while AVX2
still calculates all eight lanes.

In the complete renderer, compositing dominates and the differences narrow.
Seven one-second `GOMAXPROCS=1` samples measured medians of 8.09 ms for exact
batched float64, 8.67 ms for batched scalar float32, 7.76 ms for AVX2 float32,
and 7.66 ms for scalar Q16.16. Production therefore remains scalar Q16.16;
both float32 paths remain experimental/benchmark backends.

## AMD64 benchmarks

Five 500 ms samples produced the following medians. Each geometry operation
computes every intersecting row span for one circle on a 513×389 canvas.

| Geometry | Batched `float64` oracle | Q16.16 | Comparison |
| --- | ---: | ---: | ---: |
| R5 fractional | 116 ns | 115 ns | tied |
| R25 fractional | 604 ns | 685 ns | float64 1.13× faster |
| R100 fractional | 4.72 µs | 4.79 µs | tied |
| R256 clipped | 14.45 µs | 15.04 µs | float64 1.04× faster |

The same binary compares the geometry modes in the complete opaque renderer:

| 512×512/K100, one thread | Median | Speedup | Allocations |
| --- | ---: | ---: | ---: |
| Batched `float64` oracle | 8.09 ms | 1.00× | 0 |
| Q16.16 | 7.66 ms | **1.06×** | 0 |

## Precision

A deterministic test compared 10,000 random circles over 2,022,704
`float64`-intersecting rows. Q16.16 changed 15 row spans, or approximately
0.00074%; float32 changed 19 spans, or approximately 0.00094%. Both tests cap
the deviation at 0.001%, and Q16.16 retains the exact `float64` path for
out-of-range geometry. Existing historical renderer-oracle and row-sharding
tests pass with the fixed-point path enabled.

This is a quantified approximation, not a claim of bit-identical geometry.
Adversarial tangent cases, Q24.8/Q8.24 comparisons, and native ARM64
measurements remain before Task 10.13 can be closed. The existing six-target
cross-build matrix passes.

## Reproduction

```sh
go test -run '^TestFixedCircleQ16' -count=1 -v ./internal/fit/renderer

go test -run '^TestCircleSpanFloat32' -count=1 -v ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCircleSpanFloat32AVX2Direct$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCircleSpanQ16AVX2Direct$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCircleSpanGeometry$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCPURendererGeometry$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```
