# Task 10.16: Incremental cost baseline

**Baseline date:** 2026-08-13  
**Hardware:** AMD Ryzen 5 4600H  
**Backend:** AVX2 SSD, `GOMAXPROCS=1`

This note covers only Task 10.16a's current-cost baseline. No dirty-region
collector, delta arithmetic, or production dispatch has been implemented yet.
The original Pascal/Delphi source is not present in this repository, so its
possible cumulative-cost implementation is deliberately deferred until the
source is available or a Go prototype can be compared with it.

## Benchmark design

`BenchmarkIncrementalCostBaseline` uses the same 256x256 reference for three
single-threaded CPU evaluation shapes:

- joint: 50 candidate circles over the white initial canvas;
- sequential: one candidate circle over a retained 45-circle canvas;
- batch: five candidate circles over the same retained canvas.

Each shape is split into `Render`, standalone full-image `FastMSECost`, and the
current `CPURenderer.Cost` call that performs both operations. The retained
canvas models the sessions created by the staged sequential and batch
pipelines. The benchmark does not include optimizer or session-construction
overhead.

Seven 500 ms samples were collected for each operation. The table reports
medians; independently sampled render and SSD medians are diagnostic components
and are not expected to add exactly to the separately sampled cost median.

| Evaluation shape | Render | Full-image SSD | Current cost | SSD profile share |
| --- | ---: | ---: | ---: | ---: |
| Joint K50 | 917.79 us | 25.19 us | 946.52 us | 2.69% |
| Sequential K1 | 51.54 us | 25.69 us | 71.70 us | 31.95% |
| Batch K5 | 204.01 us | 25.38 us | 236.54 us | 10.97% |

All cases reported zero allocations. Standalone AVX2 SSD throughput was about
20 GB/s. Its duration remains almost constant because the current cost path
always scans all 65,536 pixels regardless of how many candidate circles were
rendered.

Five-second CPU profiles of the complete `Cost` sub-benchmarks agree with the
timing decomposition. `fit.ssdAVX2` accounted for 2.69% of flat samples in
joint K50, 31.95% in sequential K1, and 10.97% in batch K5. The profiles are
kept out of the repository because they contain machine-specific build IDs.

## Initial conclusion

Avoiding the full-image scan has little headroom for this joint K50 workload,
but meaningful headroom for retained-canvas stages, particularly sequential
single-circle evaluation. This supports investigating dirty-area ratios and an
exact incremental path later, while also showing that joint and sufficiently
large batch workloads will need a crossover fallback to full-image AVX2 SSD.

## Dirty coverage measurements

`TestDirtySpanCoverageMetrics` measures the union of the production Q16.16
circle spans. It also renders the same opaque black circles over a white canvas
and requires the actual changed-pixel count to equal the geometric union. This
makes the reported area exact for these deterministic cases rather than a
bounding-box estimate.

Centered single-circle coverage scales as expected with radius:

| Radius | Dirty pixels | Image area | Dirty rows |
| --- | ---: | ---: | ---: |
| 4 | 49 | 0.075% | 3.516% |
| 8 | 197 | 0.301% | 6.641% |
| 16 | 797 | 1.216% | 12.891% |
| 32 | 3,209 | 4.897% | 25.391% |
| 64 | 12,853 | 19.612% | 50.391% |
| 96 | 28,917 | 44.124% | 75.391% |
| 128 | 51,431 | 78.477% | 100.000% |

Clipping reduces the useful dirty area substantially for radius 64:

| Position | Image area | Dirty rows |
| --- | ---: | ---: |
| Center | 19.612% | 50.391% |
| Left edge | 9.904% | 50.391% |
| Top-left corner | 5.002% | 25.391% |

Overlap reduces pixels but also changes the number of intervals that must be
merged. Four radius-32 circles produced:

| Layout | Image area | Merged/raw spans |
| --- | ---: | ---: |
| Coincident | 4.897% | 65/260 |
| Clustered | 12.422% | 107/260 |
| Disjoint | 19.586% | 260/260 |

A deterministic prefix of randomly placed radius-16 circles shows batch
growth and fragmentation:

| Batch | Image area | Dirty rows | Merged/raw spans |
| --- | ---: | ---: | ---: |
| K1 | 1.228% | 12.500% | 32/32 |
| K2 | 2.457% | 20.703% | 64/64 |
| K4 | 4.907% | 35.156% | 128/128 |
| K8 | 9.821% | 43.750% | 256/256 |
| K16 | 17.525% | 69.531% | 390/512 |
| K32 | 30.904% | 94.531% | 678/1,024 |

The K32 result shows why a pixel percentage alone cannot safely select the
backend: only 30.9% of pixels are dirty, but they are split into 678 intervals
across almost every row. The first kernel prototype should therefore benchmark
candidate area cutoffs around 12.5%, 25%, and 50% while measuring an additional
per-span penalty. Falling back above 50% is a conservative initial bound, not a
production threshold; the final crossover remains intentionally unset until
the real scalar and SIMD dirty-span kernels exist.

## Exact retained-base SSD

The first Task 10.16b implementation slice adds `fit.ExactSSD`, which returns
the unnormalized RGB SSD as a `uint64`. For equal strides it reuses the active
AVX2, NEON, or scalar reduction. Those kernels calculate an integer total and
currently return it through `float64`, so `ExactSSD` first proves that the
worst-case image total is at most `2^53`, then verifies that converting the
result back to `uint64` is lossless. The bound permits roughly 46 billion
maximum-difference pixels, well beyond practical in-memory images. Independent
strides use a direct `uint64` scalar accumulation.

CPU renderer constructors now calculate this exact total between their initial
canvas and reference. Ordinary child sessions inherit it, while staged sessions
recompute it for the newly retained canvas. Empty, mismatched, or theoretically
oversized images mark the stored total invalid instead of silently rounding.
Alpha remains excluded exactly as in `FastMSECost`.

Production `CPURenderer.Cost` does not consume this value while incremental
mode remains disabled. The default-off experimental path described below uses
it for parity and performance work. Focused tests cover maximum RGB
differences, alpha exclusion, padded and independent strides, white and custom
initial canvases, inherited sessions, and retained-canvas sessions.

## Exact incremental contract

The remaining Task 10.16b work adds a default-off experimental evaluator. The
normal production mode is unchanged; tests and benchmarks can select forced or
automatic incremental evaluation internally while Task 10.16c establishes a
faster kernel and measured production crossover.

The renderer records each half-open circle span during the real compositing
traversal. Per-row insertion keeps intervals sorted and merges overlap and
adjacency immediately. Consequently, a pixel covered by several circles is
visited once after its final composited value is available. Row-sharded workers
write only to their disjoint row entries.

For each merged dirty pixel, the evaluator calculates exact integer errors for
the retained base and final candidate:

```text
delta        += candidatePixelSSD - basePixelSSD
candidateSSD  = baseSSD + delta
candidateMSE  = float64(candidateSSD) / float64(width * height * 3)
```

Each RGB pixel error is at most 195,075. Because both complete image totals are
already constrained to at most `2^53`, the signed delta and every valid final
total fit safely in `int64`; subtraction from and addition to the stored
`uint64` base are checked before use. Alpha remains ignored, and normalization
occurs exactly once with the same denominator as `FastMSECost`.

### Selection and invalidation rules

- Incremental mode is disabled by default. Joint, sequential, and batch
  production sessions therefore continue to use full-image SIMD SSD.
- An internal forced mode is used only for exact parity tests and diagnostic
  benchmarks.
- The provisional automatic mode estimates scalar work as
  `dirtyPixels*8 + mergedSpans*32` and falls back when that exceeds the full
  pixel count. The weights reflect the current AVX2/scalar gap and are not a
  final production crossover.
- `SetCostFunc` selects an arbitrary cost and disables incremental MSE;
  `UseFastCost` explicitly restores eligibility. Go functions cannot otherwise
  be compared reliably.
- Ordinary child sessions inherit the immutable base total and selection mode.
  A staged retained-canvas session copies the retained pixels and recomputes
  its base total. Changing candidate parameters never mutates that base.
- Invalid exact totals, empty/mismatched inputs, or an uneconomical dirty union
  use the established full-image path. Custom canvases, including translucent
  ones, remain supported because deltas compare final NRGBA bytes rather than
  intermediate blend values.
- A `CPURenderer` remains intentionally non-concurrent as an object; internal
  row workers are safe because their image and dirty-span rows are disjoint.

Tests require exact equality with full-image `FastMSECost` for positive and
negative deltas, transparent, clipped, and overlapping circles, opaque and
translucent retained canvases, 250 randomized cases, and one/four render
threads. Randomized interval insertion is also checked against a per-pixel
boolean union oracle. The focused race test passes.

## Scalar prototype benchmark

Five 300 ms samples compared the established cost, forced scalar delta, and
the provisional automatic policy. Medians are shown below:

| 256x256 evaluation | Full-image cost | Forced delta | Automatic | Decision |
| --- | ---: | ---: | ---: | --- |
| Joint K50 | 909.61 us | 1,272.69 us | 953.43 us | full-image fallback |
| Sequential K1 | 71.71 us | 115.69 us | 72.51 us | full-image fallback |
| Batch K5 | 254.95 us | 516.73 us | 261.00 us | full-image fallback |

All steady-state cases reported zero allocations. Forced scalar delta loses on
these representative parameters because a Go scalar pixel update cannot yet
compete with the approximately 25 us full-image AVX2 reduction. Automatic mode
correctly rejects all three, although collecting spans before that decision
still costs roughly 1-5%. The result validates keeping the experiment disabled
in production and makes the next Task 10.16c objective concrete: reduce span
tracking cost and vectorize or otherwise strengthen the dirty-span reduction
before retuning dispatch.

Reproduce the benchmark with:

```sh
GOMAXPROCS=1 go test ./internal/fit/renderer -run '^$' \
  -bench '^BenchmarkIncrementalCostBaseline$' \
  -benchmem -benchtime=500ms -count=7

go test ./internal/fit/renderer \
  -run '^TestDirtySpanCoverageMetrics$' -count=1 -v
```
