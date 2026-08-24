# Incremental dirty-span cost

The evaluation contract for scoring a candidate by the pixels it can actually
change, instead of rescanning the whole image. It is exact — bit-for-bit equal
to a full `FastMSECost` replay — and it is selected automatically, only where it
was measured to win.

This is the Go contract. Read it before changing the selection rule, the span
store, or a delta-SSD kernel.

> Timings are from an AMD Ryzen 5 4600H unless stated otherwise, and are local
> measurements rather than portable guarantees.

## Why it exists, and why it is narrow

Baseline profiling at 256×256, single-threaded, AVX2:

| Evaluation shape | Render | Full-image SSD | Total cost | SSD share of profile |
| --- | ---: | ---: | ---: | ---: |
| Joint K50 | 917.79 µs | 25.19 µs | 946.52 µs | 2.69% |
| Sequential K1 | 51.54 µs | 25.69 µs | 71.70 µs | 31.95% |
| Batch K5 | 204.01 µs | 25.38 µs | 236.54 µs | 10.97% |

Full-image SSD costs the same regardless of how many circles were rendered,
because it always scans all 65,536 pixels. So there is almost no headroom in
joint evaluation and real headroom in retained-canvas staged sessions —
especially sequential single-circle evaluation, where a third of the profile is
a scan of pixels that could not have changed.

## The arithmetic

The renderer records each half-open circle span during the real compositing
traversal, into a flat `height × K` store — one circle contributes at most one
span per row, which is a hard bound, so the store has fixed capacity and no
steady-state allocation. Row-sharded workers write only to disjoint entries.
Spans are sorted and merged once after rendering, so a pixel covered by several
circles is visited once, after its final composited value exists.

For each merged dirty pixel:

```text
delta        += candidatePixelSSD - basePixelSSD
candidateSSD  = baseSSD + delta
candidateMSE  = float64(candidateSSD) / float64(width * height * 3)
```

`fit.ExactSSD` supplies `baseSSD` as an unnormalized `uint64`. The existing
AVX2/NEON/scalar reductions compute an integer total and return it through
`float64`, so `ExactSSD` first proves the worst-case image total is at most
`2^53` — about 46 billion maximum-difference pixels, far beyond any in-memory
image — and then verifies the conversion back to `uint64` is lossless.
Independent strides use a direct `uint64` scalar accumulation.

Each RGB pixel error is at most 195,075, and both complete image totals are
already bounded by `2^53`, so the signed delta and every valid final total fit
in `int64`. Subtraction from and addition to the stored base are checked before
use. Alpha is ignored, and normalization happens exactly once with the same
denominator as `FastMSECost`.

Deltas compare **final NRGBA bytes**, not intermediate blend values, which is
why custom and translucent canvases are supported unchanged.

## Selection rules

Incremental mode is off for direct and joint sessions, always. Eligible
sequential and batch **retained-canvas** sessions select it automatically when
the built-in `FastMSECost` is active and a vectorized delta-SSD kernel is
present.

| Canvas | Rule |
| --- | --- |
| ≥ 128×128 | Preflight rejects candidates whose summed circle area exceeds **30%** of the image, then requires `dirtyPixels*3 + mergedSpans*16 <= totalPixels` after merging. |
| < 128×128 | K1 retained sessions only; preflight caps summed area at **15%**; requires `dirtyPixels*6 + mergedSpans*16 <= totalPixels`. |

Further rules:

- `SetCostFunc` selects an arbitrary cost and disables incremental MSE;
  `UseFastCost` explicitly restores eligibility. Go functions cannot otherwise
  be compared reliably.
- Ordinary child sessions inherit the immutable base total and selection mode. A
  staged retained-canvas session copies the retained pixels and recomputes its
  base. Changing candidate parameters never mutates that base.
- Invalid exact totals, empty or mismatched inputs, and an uneconomical dirty
  union all fall back to the full-image path.
- A `CPURenderer` is still non-concurrent as an object; its internal row workers
  are safe because their image and dirty-span rows are disjoint.

**The selection rule is not a pixel percentage, and that is the point.** A
32-circle candidate measured 30.9% dirty — but across **678 separate intervals**
covering almost every row, where the full-image kernel wins comfortably. Cost is
per span as well as per pixel, so the model prices both. See
[`rejected-optimizations.md`](rejected-optimizations.md).

### Which architectures take the path

AVX2 and SSE2 both do. The crossover constants model native AVX2 measurements,
and extending them to the half-width SSE2 kernel was measured rather than
assumed — `BenchmarkIncrementalCostCrossover`, 256×256, single thread, median of
three 200 ms runs, delta speedup over a full re-render:

| Radius | 4 | 8 | 16 | 32 | 48 | 64 | 80 | 96 | 112 | 128 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| AVX2 (Ryzen 5 4600H) | 3.65× | 3.79× | 2.71× | 1.83× | 1.41× | 1.24× | 0.93× | 0.86× | 0.90× | 1.00× |
| SSE2 (genuine no-AVX2 host) | 3.33× | 3.09× | 2.50× | 1.75× | 1.34× | 1.15× | 1.05× | 0.99× | 0.92× | 0.92× |

Same shape; the SSE2 crossover sits slightly later (radius 96 against roughly
72), so AVX2-tuned constants abandon the staged path a little earlier than SSE2
would prefer. That direction is safe — it gives up a small part of the win
rather than choosing a slower path.

**ARM64 does not take it.** A NEON delta-SSD kernel exists and is tested, but
the staged path was never measured on native ARM64 hardware, and enabling it as
a side effect of the AMD64 work would be an unmeasured behavior change. The
decision and its rationale live in
`internal/fit/renderer/staged_incremental_generic.go`, next to the code.

## Dirty coverage, measured

`TestDirtySpanCoverageMetrics` measures the union of the production Q16.16
circle spans, and cross-checks it by rendering the same opaque black circles
over a white canvas and requiring the actual changed-pixel count to equal the
geometric union. The areas below are exact, not bounding-box estimates.

Centered single circle, 256×256:

| Radius | Dirty pixels | Image area | Dirty rows |
| --- | ---: | ---: | ---: |
| 4 | 49 | 0.075% | 3.516% |
| 16 | 797 | 1.216% | 12.891% |
| 32 | 3,209 | 4.897% | 25.391% |
| 64 | 12,853 | 19.612% | 50.391% |
| 96 | 28,917 | 44.124% | 75.391% |
| 128 | 51,431 | 78.477% | 100.000% |

Clipping cuts the useful area sharply (radius 64): centered 19.612%, at the left
edge 9.904%, at the top-left corner 5.002%.

Overlap reduces pixels but changes interval count — four radius-32 circles:
coincident 4.897% over 65 merged spans, clustered 12.422% over 107, disjoint
19.586% over 260.

Batch growth and fragmentation, radius-16 circles:

| Batch | Image area | Dirty rows | Merged/raw spans |
| --- | ---: | ---: | ---: |
| K1 | 1.228% | 12.500% | 32/32 |
| K4 | 4.907% | 35.156% | 128/128 |
| K16 | 17.525% | 69.531% | 390/512 |
| K32 | 30.904% | 94.531% | 678/1,024 |

These tables are expensive to re-derive; that is why they are kept.

## Measured gains

Delta-SSD kernels: AVX2 processes eight pixels per iteration, NEON four, SSE2
four, with scalar handling below one vector. The AVX2 crossover against scalar
is **eight pixels** (11.6 ns vs 27.2 ns median).

256×256 retained canvas, five 750 ms samples, zero steady-state allocations:

| Case | Dirty | Full-image | Production | Change |
| --- | ---: | ---: | ---: | ---: |
| Sequential K1 | 18.54% | 74.18 µs | 59.76 µs | **19.4%** |
| Batch K5 | 72.76% | 230.24 µs | 230.00 µs | −0.1% (preflight → full image) |

256×256 batch K5 sweep, five samples:

| Radius | Dirty | Full-image | Production | Improvement |
| --- | ---: | ---: | ---: | ---: |
| 2% | 0.50% | 2.724 ms | 1.950 ms | **28.4%** |
| 4% | 2.02% | 2.842 ms | 2.188 ms | **23.0%** |
| 8% | 7.83% | 3.431 ms | 3.020 ms | **12.0%** |

64×64 pipeline, five 750 ms samples, eight distinct bounds-scaled candidates per
stage:

| Case | Dirty | Full-image | Production | Change |
| --- | ---: | ---: | ---: | ---: |
| Sequential K1, radius 8% | 1.52% | 519.41 µs | 440.26 µs | **+15.2%** |
| Sequential K1, radius 15% | 5.14% | 597.30 µs | 561.60 µs | +6.0% |
| Sequential K1, radius 25% | 13.24% | 750.01 µs | 759.98 µs | −1.3% (falls back) |
| Joint K12 | 60.09% | 156.97 µs | 154.98 µs | +1.3% (full image both) |

The small-canvas batch and joint entries execute *identical* full-image code;
equal allocation counts confirm no dirty-span session is created, and their
small signed differences are run-order noise, not different algorithms.

Under the real seeded MayFly optimizer with a deliberately broad-radius
population, all modes stay within about 1.5% of full-image and the
outcome-parity test proves identical convergence. That workload triggers many
fallbacks; the ratio-controlled sequential case above is where the path is
actually used.

## Exactness testing

The parity suite compares forced incremental totals against a complete
`FastMSECost` replay and requires exact `float64` equality across: integer and
fractional tangent rows, every clipped edge and a clipped corner, overlapping
and repeated spans, fully transparent circles, opaque/translucent/fully
transparent retained canvases, positive and negative deltas, and 250 randomized
retained-canvas cases — each at one and four render threads. Randomized interval
insertion is checked against a per-pixel boolean union oracle.

SIMD boundary cases cover full-width dirty spans of 1, 2, 3, 4, 7, 8, 9, 15, 16,
17, 31, 32, 33, 63, 64, and 65 pixels. A 96-candidate ordering test in K1 and K5
modes verifies that automatic dispatch preserves every exact cost *and* the
stable ranking — the property an optimizer actually depends on.

Two end-to-end pipeline tests compare full-image against production-policy
results for joint, sequential, and batch, requiring identical best parameters,
exact best and initial costs, final image bytes, evaluation and stage counts,
optimized-circle counts, termination, and early-stop metadata.

```sh
GOMAXPROCS=1 go test ./internal/fit/renderer -run '^$' \
  -bench '^BenchmarkIncrementalCostBaseline$' -benchmem -benchtime=750ms -count=5

GODEBUG=cpu.avx2=off go test -race ./internal/fit/renderer \
  -run '^(TestDeltaSSDSpan|TestDirtySpanSet|TestIncrementalCost|TestIncrementalPipeline)'

go test ./internal/fit/renderer -run '^TestDirtySpanCoverageMetrics$' -count=1 -v
```

## Open

The original Pascal/Delphi source is not in this repository, so no legacy
cumulative or fixed-point algorithm is inferred from it here. A source-backed
comparison of legacy 16.16, 8.24, MMX, or SSE accumulation can be added without
changing this exact integer SSD contract.

Polishing later added a second, different use of the delta-SSD kernels; see
[`contiguous-window-polish-report.md`](contiguous-window-polish-report.md).
