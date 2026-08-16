# Contiguous-window polishing performance report

**Baseline date:** 2026-08-16
**Hardware:** AMD Ryzen 5 4600H, 12 logical cores
**Backend:** CPU renderer, AVX2 SSD, one render thread per configuration
**Benchmarks:** `BenchmarkPolishCircleBatchStrategy`,
`BenchmarkPolishStrategyQuality`,
`BenchmarkPolishStrategyQualityAfterBatchFit` (`internal/fit/renderer`)

`contiguous-window` was added as a rendering optimization and measured as one.
This note measures the thing a user actually chooses a strategy for -- how much
error a polishing run removes per second of wall clock -- and the answer is
narrower than the render-cost measurement suggests. The strategy is genuinely
cheaper per sweep. On these workloads it is not cheaper per unit of error
removed, and at the shipped default sweep budget it removed no error at all.

## What the strategy does

Batch polishing bakes the circles before the first active draw slot into a
canvas that is reused for every candidate in a sweep. Only that prefix is
bakeable, so an active set containing circle one bakes nothing and every
candidate rasterizes the whole vector. `replacement`, `hybrid-overlap`, and
`residual-region` select by image-space merit, which scatters the active set
through the draw order and routinely reaches circle one.

`contiguous-window` selects `activeSetSize` consecutive slots, starting as late
as the visit counts allow. The first sweep therefore costs about
`activeSetSize` circle rasterizations per candidate instead of `circles`, and
later sweeps slide the window toward the front, costing `circles - windowStart`.

## Render cost, isolated

`BenchmarkPolishCircleBatchStrategy` drives a recording stub instead of an
optimizer, so it measures rendering alone. One sweep, `activeSetSize` 3,
128x128:

| Circles | `hybrid-overlap` | `contiguous-window` | Speedup |
| ---: | ---: | ---: | ---: |
| 64 | 32.7 ms | 16.8 ms | 1.94x |
| 256 | 112.1 ms | 27.4 ms | 4.09x |

**These are first-sweep numbers and the first sweep is the strategy's best
case.** Sweep *k* costs roughly `k * activeSetSize` rasterizations, so over a
full `ceil(circles/activeSetSize)` coverage cycle the average is about half the
vector. Measured over 13 sweeps at 64 circles below, the advantage falls to
1.44x.

## Cost removed per second

`BenchmarkPolishStrategyQuality` runs the real MayFly optimizer, 60 iterations
at population 20, seed 4242, `activeSetSize` 5, one render thread. The
reference is rendered from a known 64-circle vector and the starting vector is
that truth jittered by 2%, so all of the error is recoverable. Every strategy
gets the identical optimizer budget and the identical starting vector. Medians
of three; the polishing result itself is deterministic, only the timing varies.

### 3 sweeps, the shipped `--polishing-max-sweeps` default

| Strategy | Wall clock | Accepted sweeps | Final cost | Error removed |
| --- | ---: | ---: | ---: | ---: |
| `replacement` | 1.05 s | 1 / 3 | 549.2 | 5.3% |
| `hybrid-overlap` | 1.07 s | 3 / 3 | 468.3 | 19.2% |
| `residual-region` | 1.15 s | 2 / 3 | 461.4 | 20.4% |
| `contiguous-window` | **0.38 s** | **0 / 3** | 579.8 | **0%** |

### 13 sweeps, `ceil(64/5)`, the budget at which the window has covered every slot

| Strategy | Wall clock | Accepted sweeps | Final cost | Error removed |
| --- | ---: | ---: | ---: | ---: |
| `replacement` | 4.14 s | 1 / 13 | 549.2 | 5.3% |
| `hybrid-overlap` | 4.51 s | 13 / 13 | **55.6** | **90.4%** |
| `residual-region` | 4.95 s | 11 / 13 | 167.1 | 71.2% |
| `contiguous-window` | **3.14 s** | 8 / 13 | 244.5 | 57.8% |

### 19 sweeps, chosen so `contiguous-window` spends the wall clock 13 sweeps cost `hybrid-overlap`

| Strategy | Wall clock | Accepted sweeps | Final cost | Error removed |
| --- | ---: | ---: | ---: | ---: |
| `replacement` | 6.16 s | 2 / 19 | 547.7 | 5.5% |
| `hybrid-overlap` | 6.69 s | 19 / 19 | **46.3** | **92.0%** |
| `residual-region` | 7.18 s | 13 / 19 | 136.0 | 76.5% |
| `contiguous-window` | 4.19 s | 14 / 19 | 113.1 | 80.5% |

**At equal wall clock `hybrid-overlap` wins by roughly 2x on cost.** Nineteen
contiguous-window sweeps cost 4.19 s and reach 113.1; thirteen hybrid-overlap
sweeps cost 4.51 s and reach 55.6. Spending the saved render time on more
sweeps does not recover the ground the selector gives up, because a window
chosen by position spends most of its sweeps on circles that were already
close to right.

## Polishing the output of a real batch fit

The synthetic vector above is not what `PolishCircleBatchContext` sees in
production. `BenchmarkPolishStrategyQualityAfterBatchFit` runs a real
`OptimizeBatch` first -- 64 circles, batch size 8, 400 iterations at population
30 -- and polishes its output.

That fit leaves exactly one circle whose `MSEContribution` has gone negative,
and the result is dominated by that single circle:

| Sweeps | Strategy | Wall clock | Accepted | Final cost | Error removed |
| ---: | --- | ---: | ---: | ---: | ---: |
| 3 | `replacement` | 0.86 s | 0 / 3 | 625.2 | 0% |
| 3 | `hybrid-overlap` | 1.09 s | 0 / 3 | 625.2 | 0% |
| 3 | `residual-region` | 1.38 s | 2 / 3 | 593.7 | 5.0% |
| 3 | `contiguous-window` | 0.29 s | 0 / 3 | 625.2 | 0% |
| 13 | `replacement` | 3.68 s | 0 / 13 | 625.2 | 0% |
| 13 | `hybrid-overlap` | 4.96 s | 0 / 13 | 625.2 | 0% |
| 13 | `residual-region` | 6.11 s | 12 / 13 | 490.7 | 21.5% |
| 13 | `contiguous-window` | 3.01 s | 0 / 13 | 625.2 | 0% |

Three of the four strategies are complete no-ops on this input, and they spend
their entire optimizer budget to achieve it.

### Why

`PolishCircleBatchContext` commits a sweep only when `allCirclesUseful` holds
for the **whole** candidate vector: every circle must be valid, change at least
one pixel, and contribute more than `minBatchMSEContribution` (0.01) to MSE.
A circle whose contribution has gone negative therefore blocks every sweep
until some active set repairs it, and inactive circles are copied through a
sweep unchanged, so only a sweep whose active set contains the offender can
clear it.

Such circles are expected in fitted output. `optimizeBatchContext` prunes with
`PruneCircleBatch` once per stage, against that stage's canvas
(`internal/fit/renderer/pipeline.go:534`). Circles from later stages are
composited on top afterwards and can occlude what an earlier stage judged
useful. Nothing re-audits the assembled vector, so the pruner's per-stage
guarantee does not imply the polisher's global one.

**This gate is pre-existing and independent of `contiguous-window`.** It is
worth recording here because it changes how these results should be read: the
dominant variable is not which strategy renders faster, it is whether a
strategy's active set happens to cover the circles blocking acceptance. That is
also why the ranking is not stable across the two workloads -- `hybrid-overlap`
is best on the synthetic vector and a no-op on the fitted one, while
`residual-region` is the only strategy that repairs the fitted vector.

`contiguous-window` is the worst-placed strategy for this gate by construction:
it does not target weak circles at all, and at the default three sweeps it only
ever offers the last `3 * activeSetSize` draw slots to the optimizer. With 256
circles and the default `activeSetSize` of 5, that is 15 of 256 slots, all at
the end of the draw order.

## Recommendation

- The default three sweeps are not enough for `contiguous-window` to be worth
  selecting. Raise `--polishing-max-sweeps` to at least
  `ceil(circles / activeSetSize)`, the point at which the window has covered
  every slot, or leave the strategy alone. `app.MaxPolishingSweeps` caps the
  budget at 32, so full coverage is unreachable above `32 * activeSetSize`
  circles -- 160 at the default active set size. Raise
  `--polishing-active-set-size` alongside the sweep budget for larger vectors.
- Even at full coverage, prefer a merit-based strategy when wall clock is the
  budget. `contiguous-window` reached a worse cost than `hybrid-overlap` at
  equal time in every configuration measured here.
- `contiguous-window` is worth having where per-sweep latency is the constraint
  rather than total error -- an interactive sweep, a large circle count where a
  merit-based sweep is too slow to run at all, or a UI that shows intermediate
  sweeps -- and its per-candidate cost is the only one of the four that does
  not grow with the whole vector.
- Do not read the 5.3x or 4.09x render-cost figures as an end-to-end speedup.
  They describe the first sweep of a coverage cycle with the optimizer stubbed
  out.

These are wall-clock comparisons on one machine, one reference, and one seed.
Per the repository convention, do not compare these absolute timings against
`docs/task-9.9-performance-report.md` or any other machine's report, and re-run
the benchmarks before drawing a conclusion about a different workload.

## Reproducing

```sh
go test -run '^$' -bench BenchmarkPolishCircleBatchStrategy -benchtime 5x -count 3 ./internal/fit/renderer/
go test -run '^$' -bench 'BenchmarkPolishStrategyQuality$' -benchtime 1x -count 3 -timeout 60m ./internal/fit/renderer/
go test -run '^$' -bench BenchmarkPolishStrategyQualityAfterBatchFit -benchtime 1x -count 3 -timeout 60m ./internal/fit/renderer/
```

The quality benchmarks report `final_cost`, `reduction_pct`, and
`accepted_sweeps` per run rather than per `b.N`, so those columns stay
comparable across `-benchtime` values while `ns/op` does not.
