# Task 10.12: NEON horizontal span compositing

**Validated:** 2026-08-12  
**Hardware:** Apple M5 MacBook Air, macOS 26.6.1, ARM64  
**Production result:** 1.93× faster one-thread 512×512/K100 rendering, zero allocations

## Implementation

Opaque NRGBA canvases now composite a whole scanline span at a time. The Go
scalar span computes the foreground and background-blend terms once per span,
instead of once per pixel. Translucent custom canvases retain the existing
general Porter-Duff pixel path; constructors scan custom canvas alpha once and
sessions preserve the result.

ARM64 builds also include a hand-written Go Plan 9 NEON kernel. It:

- deinterleaves eight NRGBA pixels with `LD4`;
- widens each RGB byte channel to float64 lanes;
- uses the same fused multiply-add ordering as the Go scalar expression;
- narrows and interleaves RGB while retaining opaque alpha;
- leaves the remainder to the exact scalar span;
- runs only when `x/sys/cpu` reports ASIMD and the span contains at least 256
  pixels.

The 256-pixel cutoff is measured, not architectural. Exact float64 conversion
and three widening/narrowing stages make short NEON spans slower than the M5's
strong scalar floating-point pipeline.

## M5 measurements

Five 500 ms isolated samples produced these medians. The NEON column measures
the raw eight-pixel kernel before the final conservative cutoff was selected.

| Pixels per span | Scalar span | NEON kernel | NEON/scalar |
| ---: | ---: | ---: | ---: |
| 8 | 8.35 ns | 14.75 ns | 0.57× |
| 16 | 15.33 ns | 17.46 ns | 0.88× |
| 64 | 59.63 ns | 58.19 ns | 1.02× |
| 256 | 235.1 ns | 230.0 ns | 1.02× |

The controlled full-render benchmark compares the previous opaque per-pixel
loop with the integrated horizontal-span path in the same binary and workload.

| 512×512/K100 path | Median | Speedup | Allocations |
| --- | ---: | ---: | ---: |
| Previous per-pixel loop | 3.883 ms | 1.00× | 0 |
| Horizontal span, automatic NEON dispatch | 2.015 ms | **1.93×** | 0 |

Forced-scalar and automatic-NEON full-render runs varied with power and thermal
state more than their small difference. A 64-pixel NEON threshold was 5.3%
slower than the scalar span in one matched matrix, so production uses the
256-pixel threshold and does not claim a full-render SIMD-only speedup. The
large, repeatable gain comes from horizontal span integration and invariant
hoisting; NEON is a measured long-span supplement.

## Correctness and dispatch

Tests compare the selected span path byte-for-byte with `compositePixel` for:

- 0–33-pixel boundaries plus 255/256/257-pixel dispatch boundaries;
- transparent, fractional, half-alpha, and opaque sources;
- 128 deterministic randomized 265-pixel cases;
- prefix offsets and preserved alpha bytes;
- opaque and translucent custom-canvas detection;
- ASIMD-enabled selection and a fresh-process `GODEBUG=cpu.all=off` fallback.

The focused and randomized suites pass natively on the M5. The complete
renderer suite has one known, pre-existing ARM64 historical-oracle difference:
one translucent custom-canvas alpha byte is 205 rather than 206. It occurs in
both the pre-Task-10.12 and current general path and is tracked in
`known-limitations.md`; the new opaque span does not execute for that case.

## Post-change profile

A 5-second one-thread render profile on the M5 reports:

| Function | Flat CPU |
| --- | ---: |
| `compositeOpaqueSpanScalar` | 65.01% |
| `renderCircleScanlineRows` | 26.47% |
| `compositeOpaqueSpanNEON` | 1.95% |
| span dispatch wrapper | 0.36% |

The profile validates both the span optimization and the cutoff: NEON is used
only for uncommon very long spans where its setup cost is amortized.

## Reproduction

```sh
go test -run '^$' -bench '^BenchmarkCompositeOpaqueSpan$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkCPURendererOpaqueSpan$' \
  -benchmem -benchtime=2s -count=5 ./internal/fit/renderer

GODEBUG=cpu.all=off go test -run '^$' \
  -bench '^BenchmarkCPURendererOpaqueSpan$' \
  -benchmem -benchtime=2s -count=5 ./internal/fit/renderer
```

Profiles and cross-compiled test binaries remain untracked because they embed
machine-specific samples and build identifiers.
