# Task 10.13: fixed-point circle geometry (initial AMD64 results)

**Status:** in progress  
**Validated:** 2026-08-13
**Hardware:** AMD Ryzen 5 4600H, Linux/AMD64  
**Production result:** 1.14× faster one-thread 512×512/K100 rendering

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

## AVX2 float32 follow-up

The AMD64 prototype is hand-written Plan 9 assembly and runtime-gated with
`golang.org/x/sys/cpu`. It keeps all instructions VEX-encoded until
`VZEROUPPER`; mixing a legacy SSE load into the first prototype caused a severe
AVX-to-SSE transition penalty and was removed. A 100,000-case randomized test
proves the AVX2 span results are exact relative to the scalar float32 helper,
including clipped spans and batch boundaries.

Direct single-row medians show that SIMD does make float32 substantially faster:

| Radius | Scalar float32 | AVX2 float32 | Speedup |
| --- | ---: | ---: | ---: |
| 5.25 | 8.63 ns | 6.63 ns | 1.30× |
| 25.25 | 33.57 ns | 12.56 ns | 2.67× |
| 100.25 | 148.9 ns | 31.57 ns | 4.72× |
| 256.25 | 324.3 ns | 62.08 ns | 5.22× |

The stronger comparison is Q16.16, whose monotonic batch needs one widened
integer multiply rather than eight floating-point lane calculations. Across all
intersecting rows, AVX2 float32 was about 2.0× faster than scalar float32 at R25,
4.1× at R100, and 3.6× for the clipped R256 case, but Q16.16 remained 16–34%
faster over those workloads. R5 is below the SIMD crossover and AVX2 was about
18% slower than scalar float32 there.

With `GOMAXPROCS=1` and two-second samples, the complete 512×512/K100 renderer
measured a 10.04 ms median for AVX2 float32 and 9.32 ms for Q16.16. AVX2 was
therefore about 7.7% slower than Q16.16 despite decisively beating the original
scalar float32 search. It remains a runtime-gated experimental/benchmark
backend; production rendering continues to select Q16.16.

Scalar `float32` was also measured. On this host it is effectively tied with
`float64`; Go emits scalar XMM arithmetic for both, so halving the value width
does not create SIMD parallelism by itself. The explicit AVX2 kernel supplies
that parallelism, but does not overcome Q16.16's cheaper monotonic skip.

## AMD64 benchmarks

Five 500 ms samples produced the following medians. Each geometry operation
computes every intersecting row span for one circle on a 513×389 canvas.

| Geometry | `float64` oracle | Q16.16 | Speedup |
| --- | ---: | ---: | ---: |
| R5 fractional | 103.7 ns | 99.72 ns | 1.04× |
| R25 fractional | 1.592 µs | 0.578 µs | 2.75× |
| R100 fractional | 23.887 µs | 4.762 µs | 5.02× |
| R256 clipped | 62.879 µs | 13.954 µs | 4.51× |

The same binary compares the geometry modes in the complete opaque renderer:

| 512×512/K100, one thread | Median | Speedup | Allocations |
| --- | ---: | ---: | ---: |
| `float64` oracle | 9.134 ms | 1.00× | 0 |
| Q16.16 | 7.982 ms | **1.14×** | 0 |

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

go test -run '^$' -bench '^BenchmarkCircleSpanGeometry$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCPURendererGeometry$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```
