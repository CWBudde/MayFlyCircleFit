# Task 10.13: fixed-point circle geometry (initial AMD64 results)

**Status:** in progress  
**Validated:** 2026-08-12  
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

This is the useful batching property an AVX2 prototype would exploit, but it
needs only one scalar lane. Inspection of the compiled AMD64 kernel shows a
tight batch loop consisting of shift, subtract, `IMULQ`, compare, and branch,
with add-only tail recurrences and no stack frame. Calculating all eight AVX2
lanes and extracting a mask would do more work for the same monotonic proof.
An assembly kernel is therefore not enabled at this stage. Direct SIMD
prototyping remains open if later profiles or another architecture show a
crossover.

Scalar `float32` was also measured. On this host it is effectively tied with
`float64`; Go emits scalar XMM arithmetic for both, so halving the value width
does not create SIMD parallelism by itself.

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
0.00074%. The test caps this at 0.001% and retains the exact `float64` path for
out-of-range geometry. Existing historical renderer-oracle and row-sharding
tests pass with the fixed-point path enabled.

This is a quantified approximation, not a claim of bit-identical geometry.
Adversarial tangent cases, Q24.8/Q8.24 comparisons, and native ARM64
measurements remain before Task 10.13 can be closed. The existing six-target
cross-build matrix passes.

## Reproduction

```sh
go test -run '^TestFixedCircleQ16' -count=1 -v ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCircleSpanGeometry$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCPURendererGeometry$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```
