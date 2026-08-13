# Task 10.16: Incremental cost performance report

**Baseline date:** 2026-08-13  
**Hardware:** AMD Ryzen 5 4600H  
**Backend:** AVX2 SSD, `GOMAXPROCS=1`

This note covers Tasks 10.16a through 10.16d: the current-cost baseline, exact
incremental contract, optimized dirty-region kernels, correctness matrix, and
measured staged-mode dispatch. The original Pascal/Delphi source is not present
in this repository, so its possible cumulative-cost implementation remains
deferred until the source is available or the running Go prototype can be
compared with it.

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

Direct and joint `CPURenderer.Cost` evaluation leaves incremental mode disabled.
On measured AVX2 hosts, retained-canvas sessions created by the sequential and
batch pipelines now use the automatic policy described below. Focused tests
cover maximum RGB differences, alpha exclusion, padded and independent
strides, white and custom initial canvases, inherited sessions, and
retained-canvas sessions.

## Exact incremental contract

Task 10.16b introduced a default-off experimental evaluator. Task 10.16c keeps
joint/direct behavior unchanged but enables measured automatic selection for
the retained-canvas sessions used by sequential and batch optimization.

The renderer records each half-open circle span during the real compositing
traversal. A flat `height × K` store follows the hard bound that one circle can
contribute at most one span per row. Spans are sorted and merged once after
rendering, so a pixel covered by several circles is visited once after its final
composited value is available. The store is reused without steady-state
allocation, and row-sharded workers write only to disjoint entries.

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

- Incremental mode remains disabled for direct and joint production sessions.
  On AVX2, eligible sequential and batch retained-canvas sessions select
  automatic incremental evaluation when the built-in `FastMSECost` is active.
  Canvases below 128×128 admit only K1 staged sessions. NEON and scalar defaults
  remain unchanged until their native crossover is measured.
- An internal forced mode is used only for exact parity tests and diagnostic
  benchmarks.
- On images of at least 128×128, automatic mode first rejects candidates whose
  summed circle area exceeds 30% of the image and then requires
  `dirtyPixels*3 + mergedSpans*16 <= totalPixels`. Smaller K1 sessions use the
  final 15% / six-dirty-pixel policy documented below.
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

## Historical scalar prototype benchmark

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
still costs roughly 1-5%. At that stage the result validated keeping the
experiment disabled in production and made the Task 10.16c objective concrete:
reduce span tracking cost and vectorize or otherwise strengthen the dirty-span
reduction before retuning dispatch.

## Task 10.16c optimized implementation

The collector now uses fixed-capacity flat storage bounded by image height and
the session's circle count. It receives spans from the common scanline
traversal, regardless of whether the edges were calculated with Q16.16,
float32, or the float64 fallback, and normalizes each row only once.

The exact portable scalar reducer is isolated as `deltaSSDSpanScalar`. Native
AMD64 and ARM64 implementations compute candidate-versus-reference and
base-versus-reference RGB errors together, returning their signed integer
difference. The AVX2 kernel processes eight pixels per iteration, NEON four,
and both dispatchers retain scalar handling below one vector. Randomized span
lengths and positive/negative maximum-difference cases match the scalar oracle
exactly on native AVX2. Linux/Darwin ARM64 builds validate the NEON assembly;
native ARM64 timing remains an enablement gate after Task 10.16d.

On the Ryzen 5 4600H, three 500 ms samples put the AVX2 crossover at eight
pixels: the eight-pixel median is 11.6 ns versus 27.2 ns scalar, while lengths
one through four remain scalar. Centered single-circle end-to-end measurements
show the dirty path still winning at 30.64% coverage (about 94 us versus 100 us)
and losing at 44.12% (about 134 us versus 124 us). Production therefore uses a
conservative 30% summed-area preflight plus the large-image union/span model
above. Task 10.16d adds the small-image policy below.

The representative 256×256 sequential K1 case has 18.54% dirty pixels and 122
merged spans. Five 750 ms samples improved the median from 72.50 us to 61.65 us
(15.0%) with zero steady-state allocations. The representative K5 candidate
has 72.76% dirty pixels; preflight sends it directly to full-image AVX2, whose
median remains effectively unchanged at about 231 us.

The preliminary 10.16c 64×64 pipeline benchmark used literal optimizer values
for coordinates and radius rather than scaling them through the supplied
bounds. Its nominal `0.15` radius was therefore 0.15 pixels, not 15% of the
canvas. Task 10.16d replaces those numbers with bounds-scaled, varying
candidates and reports the actual geometric dirty union below.

An experimental reduction immediately after each completed row shard was
effectively tied with the simpler separate post-render span pass (roughly
61-62 us in the representative sequential case). It did not justify extra
coordination or allocation, so production retains the separate pass and public
`Render` behavior is unchanged.

## Task 10.16d validation

### Exactness and optimizer outcomes

The final parity suite compares forced incremental totals with a complete
`FastMSECost` replay and requires exact `float64` equality. It covers integer
and fractional tangent rows, every clipped edge and a clipped corner,
overlapping/repeated spans, fully transparent circles, opaque, translucent, and
fully transparent retained canvases, positive and negative deltas, and 250
randomized retained-canvas cases. Both one-thread and four-thread row sharding
are exercised.

Renderer-level SIMD boundary cases cover full-width dirty spans of 1, 2, 3, 4,
7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, and 65 pixels. The lower-level kernel
suite independently compares randomized AVX2/NEON-dispatch lengths and signed
extremes with the scalar oracle. A 96-candidate ordering test in both K1 and K5
modes verifies that automatic dispatch preserves every exact cost and the
stable ranking.

Two end-to-end pipeline tests compare full-image and production-policy results
for joint, sequential, and batch optimization. The first uses controlled,
varying candidates with stage convergence enabled. The second uses the real
seeded Mayfly optimizer. They require identical best parameters, exact best and
initial costs, final image bytes, evaluation and stage counts, optimized-circle
counts, termination, and early-stop metadata.

The renderer suite passes normally, with `GODEBUG=cpu.avx2=off`, and under the
race detector both with native AVX2 and with AVX2 disabled. Linux and Darwin
ARM64 test binaries compile with the NEON assembly, while Windows AMD64 and
Linux 386 validate AVX2/scalar build selection. Native ARM64 hardware was not
available, so production remains AVX2-only; on ARM64,
`GODEBUG=cpu.asimd=off` selects the tested scalar contract and native NEON
timing remains a prerequisite for enabling it by default.

### Corrected end-to-end benchmarks

Five 750 ms samples on the Ryzen 5 4600H, pinned to one Go execution context,
used eight distinct candidates per optimizer stage. Radius is now derived from
the optimizer bounds, and `% dirty` is the mean merged geometric union across
those candidates.

| 64×64 pipeline | Mean dirty | Full-image median | Production median | Change |
| --- | ---: | ---: | ---: | ---: |
| Sequential K1, radius 8% | 1.52% | 519.41 us | 440.26 us | **+15.2%** |
| Sequential K1, radius 15% | 5.14% | 597.30 us | 561.60 us | +6.0% |
| Sequential K1, radius 25% | 13.24% | 750.01 us | 759.98 us | -1.3% (fallback) |
| Batch K5, radius 4% | 2.00% | 233.46 us | 215.00 us | same full-image code path; timing noise |
| Batch K5, radius 8% | 7.85% | 264.28 us | 266.63 us | -0.9% (full-image) |
| Batch K5, radius 15% | 26.05% | 383.18 us | 390.74 us | -2.0% (full-image) |
| Joint K12 | 60.09% | 156.97 us | 154.98 us | +1.3% (full-image) |

The small-canvas K5 and joint entries intentionally execute identical
full-image production code; equal allocation counts confirm that no dirty-span
session is created. Their small signed differences are run-order and system
noise, not different algorithms.

The representative 256×256 retained-canvas benchmark still shows the larger
image benefit: sequential K1 at 18.54% dirty improves from a 74.18 us median to
59.76 us, or 19.4%, with zero allocations per steady-state evaluation. Batch
K5 at 72.76% dirty preflights to full-image SSD (230.00 us versus 230.24 us,
-0.1%). Joint K50 remains full-image-only.

A separate five-sample 256×256 K5 pipeline sweep covers the large batch
sessions that remain production-eligible:

| 256×256 batch K5 | Mean dirty | Full-image median | Production median | Improvement |
| --- | ---: | ---: | ---: | ---: |
| Radius 2% | 0.50% | 2.724 ms | 1.950 ms | **28.4%** |
| Radius 4% | 2.02% | 2.842 ms | 2.188 ms | **23.0%** |
| Radius 8% | 7.83% | 3.431 ms | 3.020 ms | **12.0%** |

Together with the 72.76%-dirty fallback, this supports enabling large-canvas
batch sessions only through the same preflight and merged-union policy.

Five 500 ms samples with the real seeded Mayfly optimizer (two iterations,
population ten, six requested circles) produced medians of 5.48/5.56 ms for
joint, 6.47/6.40 ms for sequential, and 5.90/5.98 ms for batch (full/production).
This deliberately broad-radius population triggers many fallbacks; all modes
remain within about 1.5%, and the outcome-parity test proves identical
convergence. The ratio-controlled sequential workload above provides the
required greater-than-10% end-to-end win where the incremental path is used.

### Final production crossover

Production selection remains restricted to retained-canvas `FastMSECost`
sessions on AVX2. Direct and joint evaluation, custom cost functions, scalar,
and NEON defaults continue to use full-image evaluation.

For images of at least 128×128, the preflight accepts at most 30% summed circle
area and the merged-union model requires
`dirtyPixels*3 + mergedSpans*16 <= totalPixels`. Corrected small-image
measurements found that applying this model unchanged could accept a losing
64×64 K1 case, while merely preflighting K5 was measurable. Below 128×128,
production therefore:

- enables automatic incremental evaluation only for K1 retained sessions;
- caps preflight summed area at 15%; and
- requires `dirtyPixels*6 + mergedSpans*16 <= totalPixels` after union.

This keeps the 15.2% measured K1 pipeline win, sends the 13.24%-dirty losing
case back to full-image SSD, and avoids any preflight overhead for small batch
sessions. Steady-state incremental evaluation remains allocation-free; the
extra pipeline allocations in accepted K1 runs are the per-stage reusable span
stores, not per-evaluation allocation.

### Legacy status

The Pascal/Delphi source is still not present in this repository. Per the
explicit deferral in Task 10.16a, this report does not infer a legacy cumulative
or fixed-point algorithm from `ErrorWeightingLoop`. The exact modern arithmetic
bounds and Q16.16 geometry independence are documented above; a source-backed
comparison of legacy 16.16, 8.24, MMX, or SSE accumulation remains tracked in
10.16a and can be added without changing this exact integer SSD contract.

Reproduce the benchmark with:

```sh
GOMAXPROCS=1 go test ./internal/fit/renderer -run '^$' \
  -bench '^BenchmarkIncrementalCostBaseline$' \
  -benchmem -benchtime=750ms -count=5

GOMAXPROCS=1 go test ./internal/fit/renderer -run '^$' \
  -bench '^BenchmarkOptimize(Joint|Sequential|Batch)Pipeline$' \
  -benchmem -benchtime=750ms -count=5

GOMAXPROCS=1 go test ./internal/fit/renderer -run '^$' \
  -bench '^BenchmarkOptimizeBatchPipeline256$' \
  -benchmem -benchtime=500ms -count=5

GOMAXPROCS=1 go test ./internal/fit/renderer -run '^$' \
  -bench '^BenchmarkMayflyIncrementalPipelines$' \
  -benchmem -benchtime=500ms -count=5

GODEBUG=cpu.avx2=off go test -race ./internal/fit/renderer \
  -run '^(TestDeltaSSDSpan|TestDirtySpanSet|TestIncrementalCost|TestIncrementalPipeline)'

go test ./internal/fit/renderer \
  -run '^TestDirtySpanCoverageMetrics$' -count=1 -v
```
