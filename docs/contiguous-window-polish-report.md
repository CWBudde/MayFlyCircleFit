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
removed, and at the then-shipped three-sweep budget it removed no error at all.

> **Scope correction, 2026-08-18.** That conclusion holds for the benchmarks
> here and does not generalize. On a real 1000-circle greedy fit,
> `contiguous-window` sized for total coverage beat `hybrid-overlap` by roughly
> 1.5x per hour and removed more error in one pass than two merit-based passes
> combined. The benchmarks below fit 64 circles of 2%-jittered truth, where no
> circle is systematically worse placed than any other; a greedy fit is the
> opposite. Read the benchmark sections as measurements of that workload, and
> see "A 1000-circle greedy fit reverses the conclusion" before choosing a
> strategy for a fitted vector.

## What the strategy does

Batch polishing bakes the circles before the first active draw slot into a
canvas that is reused for every candidate in a sweep. Only that prefix is
bakeable, so an active set containing circle one bakes nothing and every
candidate rasterizes the whole vector. `replacement`, `hybrid-overlap`, and
`residual-region` select by image-space merit, which scatters the active set
through the draw order and routinely reaches circle one.

`contiguous-window` selects `activeSetSize` consecutive slots. A partial budget
starts as late as the visit counts allow, so its first sweep costs about
`activeSetSize` circle rasterizations per candidate instead of `circles`, and
later sweeps slide toward the front. A budget large enough to cover the vector
starts at the earliest equally visited window instead, where a greedy fit has
the most value. Compatible `polishedFrom` continuations reconstruct and inherit
their ancestors' visit counts from checkpoint lineage.

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

### 3 sweeps, the default when this measurement was taken

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

> **Superseded.** The gate described below was replaced by the non-regression
> rule `sweepKeepsCirclesUseful`, which excuses a non-useful circle outside the
> active set as long as the sweep does not add one. Everything in this section
> describes the behavior in force when these numbers were measured, and the
> measurements above were taken under the old gate: their accepted-sweep columns
> are not a prediction of what the same runs would do today. See
> `docs/behavior-invariants.md` and PLAN task 15.6.

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
it does not target weak circles at all, and at the then-default three sweeps it only
ever offers the last `3 * activeSetSize` draw slots to the optimizer. With 256
circles and the default `activeSetSize` of 5, that is 15 of 256 slots, all at
the end of the draw order.

## A 1000-circle greedy fit reverses the conclusion

**Measured 2026-08-18** on the Ryzen 5 4600H box, against the 1000-circle fit of
`example/Christian_after.jpeg` -- a real campaign, not a synthetic vector. The
benchmarks above cover 64 circles of jittered truth; this is the regime the
report did not test, and `contiguous-window` wins it outright.

The vector was first polished by two `hybrid-overlap` passes, then by three
`contiguous-window` passes sized for total coverage (`activeSetSize` 32,
`maxSweeps` 32, `iters` 700, `epochs` 2, `popSize` 60, `stagnationIters` 500,
`minImprovement` 0.0005, one fresh seed per pass):

| Pass | Strategy | Wall clock | Cost | Gain | Gain/hour |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | `hybrid-overlap`, active set 8 | 101m01s | 106.514 | -1.303 | 0.774 |
| 2 | `hybrid-overlap`, active set 8 | 44m20s | 105.872 | -0.643 | 0.870 |
| 3 | `contiguous-window`, active set 32 | 130m31s | 103.126 | **-2.746** | **1.262** |
| 4 | `contiguous-window`, active set 32 | 124m01s | 100.880 | **-2.245** | **1.086** |
| 5 | `contiguous-window`, active set 32 | 133m31s | 100.091 | -0.790 | 0.355 |

One coverage pass removed more error than both merit-based passes combined
(-2.746 against -1.946) in less wall clock than the first of them. Across all
five the vector went 107.817 -> 100.091, PSNR 27.85 -> 28.13.

### Why position beats merit here

`selectHybridOverlapCircles` sorts by visit count, then by *weakest*
`MSEContribution`. An audit of the vector at cost 105.872 shows that tail has
nothing left to give: of 1000 circles exactly 1 has negative contribution, none
is zero, none is invisible, and **the weakest 500 hold 2.2% of the summed
contribution** (27.50 of 1259.74; median 0.132, p90 1.361, max 212.86). The
merit selector spends its budget where there is least to win.

`contiguous-window` selects by draw position instead, and draw position is
where a *greedy* fit hides its error. Gain by quarter of each coverage pass:

| Pass | Q1 | Q2 | Q3 | Q4 | Largest single drop |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 3 | -0.015 | -0.383 | -1.058 | -1.290 | 0.4404 |
| 4 | -0.021 | -0.277 | -0.600 | **-1.348** | 0.6277 |
| 5 | -0.056 | -0.212 | -0.277 | -0.245 | 0.1043 |

The window walks backwards from slot 968, so the last quarter is when it
reaches draw slots below ~250 -- the early, large circles placed against a
nearly empty canvas and never revisited since. That quarter got *bigger* on the
second pass, and pass 4's largest single improvement (0.6277) exceeded pass 3's
(0.4404). The decay that makes merit-based chains not worth continuing
(-1.303 -> -0.643, 51% falloff) did not appear across those two
(-2.746 -> -2.245, 18%).

### The positional advantage is finite, and three passes spend it

Pass 5 is where it ends. Q4 fell from -1.348 to -0.245, the largest single
improvement fell 6x, and **the profile went flat** -- 0.056 / 0.212 / 0.277 /
0.245 against the steep rise of passes 3 and 4. A flat profile means the window
no longer finds the low draw slots any more rewarding than the high ones, which
is the direct measurement that the early circles are no longer systematically
misplaced. The gain fell 65% in one pass, against 18% the pass before.

Read this as the strategy's stopping rule: **watch the shape, not the total.**
A pass whose gain still rises toward Q4 has more to give; a pass whose quarters
are level has finished, and the next one will not surprise you. Three coverage
passes exhausted a 1000-circle greedy fit.

What remains after that is bounded by the placement itself, not by the polisher.
`app.MaxCircles` was 1000 when this was measured, so the vector could not grow,
and no local repair reaches
past where the greedy fit put things. The lesson for the *next* campaign is to
polish while the canvas is small enough for the foundation to still move --
value per hour measured across one campaign runs 2597 cost units/hour at 32
circles against 1.26 at 1000, tracking coverage almost exactly.

The benchmarks above cannot see this because every circle in a 2%-jittered
truth vector is equally close to right, so there is no positional structure for
the window to exploit. **A fitted vector is not a jittered one**, and the
difference is the whole result.

### Early stopping was hiding it

The `hybrid-overlap` passes ran `minImprovement` 0.01 with `stagnationIters`
200 of 400 iters and spent **15,658 of a possible 25,600 optimizer iterations
(61%)**, because at cost ~106 a whole sweep gains about 0.02, so almost no
single iteration clears a 0.01 bar and the counter runs out mid-epoch. At
`minImprovement` 0.0005 the coverage passes spent 44,672 and 44,147 of 44,800
(99.7% and 98.5%). Polishing wires only `MinImprovement` and `StagnationIters`
into `opt.Stop` (`internal/server/worker.go:505`) -- there is no `MinIters`
floor to protect a sweep, unlike the fitting path.

Note that `minImprovement` is an **absolute** cost reduction
(`internal/opt/mayfly_adapter.go:50`). It has to be set against the gains a
sweep can actually produce at the current cost, not left at a default chosen
for a vector 100x further from converged.

### Latest-first ordering costs a full pass its first half

Simulating `selectContiguousWindowCircles` for `circleCount` 1000,
`activeSetSize` 32, 32 sweeps gives starts 968, 936, ..., 40, 8, 0: all 1000
slots covered, 24 of them twice. Summed rasterizations per candidate across the
32 sweeps:

| Traversal | Rasterizations/candidate | Coverage |
| --- | ---: | ---: |
| latest-first (before Task 16.8) | 16,872 | 1000/1000 |
| earliest-first | **16,152** | 1000/1000 |

**Over a full coverage cycle, latest-first is not the cheaper traversal** -- it
is 4.5% more expensive, because it is the same 32 windows in the opposite
order and the arithmetic barely favours starting wide. The docstring's
rationale holds only for a *partial* budget, where a late window bakes a larger
prefix and the run stops before the window slides forward.

The measured cost of that ordering is the Q1/Q2 columns above: the first half
of each pass returned 0.398 and 0.298 against second halves of 2.348 and 1.947.
Before Task 16.8, `visitCounts` was rebuilt per
`PolishCircleBatchContext` call, so every new job restarted at slot 968 and
re-walked the cheap end before reaching the valuable one.

> **Resolved 2026-08-22 (Task 16.8).** A configured budget of at least
> `ceil(circleCount / activeSetSize)` now breaks equal-visit ties earliest-first;
> smaller budgets retain the old latest-first order. Consecutive compatible
> polish continuations replay `polishedFrom` checkpoint configurations to carry
> visit counts across calls. The 1000/32/32 traversal therefore covers all
> slots at 16,152 rasterizations per candidate, while the shipped eight-sweep
> partial budget keeps its previous window sequence and cost.

## Dirty-region candidate scoring

**Measured 2026-08-21** on the same Ryzen 5 4600H host, Linux 7.0.0,
Go 1.26.0, `GOMAXPROCS=12`, AVX2, and one render thread per evaluation. These
measurements cover the dirty-region evaluator, which applies to every CPU
polishing strategy; they are recorded here because dirty scoring removes the
render-cost premise that originally motivated `contiguous-window`.

Each sweep now renders its incumbent once and retains its exact integer RGB
SSD. For a candidate, the evaluator builds a scanline-span union of every
active circle's incumbent and candidate raster. It restores those pixels from
the baked-prefix canvas, recomposites only suffix-circle intersections with
the union, and adds the exact signed SSD delta to the incumbent total. Pixels
outside the union are carried as the constant remainder. The session keeps the
previous union so the next evaluation restores only pixels the previous
candidate touched, rather than copying the complete incumbent canvas.

The fallback threshold is 5% affected pixels. The forced-dirty crossover
benchmark put the observed boundary between 7.90% and 15.76% on both sizes. At
2,111 circles, dirty/full cost was 1.337/3.228 ms at 2.10%, 2.834/3.311 ms at
7.90%, and 3.676/3.355 ms at 15.76%. At 512 circles, it was 0.698/1.728 ms,
1.765/1.800 ms, and 2.874/2.141 ms at the same fractions. Five percent leaves
margin below the narrow 7.90% win rather than treating one timing sample as the
crossover itself. A summed-disc-area preflight also
routes an obviously canvas-sized proposal directly to the full evaluator,
without first walking its scanlines.

`BenchmarkPolishCandidateCost`, 500 ms per case, three samples, reports the
affected fraction and fallback rate alongside allocations. Medians:

| Circles | Evaluator | Affected | Fallback | Time/candidate | Allocations |
| ---: | --- | ---: | ---: | ---: | ---: |
| 512 | dirty region | 0.1720% | 0% | 0.259 ms | 0 |
| 512 | full canvas | 100% | n/a | 1.248 ms | 0 |
| 2,111 | dirty region | 0.1728% | 0% | **0.814 ms** | 0 |
| 2,111 | full canvas | 100% | n/a | 2.523 ms | 0 |
| 512 | large-radius fallback | 97.88% | 100% | 3.097 ms | 0 |
| 512 | same large candidate, direct full | 100% | n/a | 2.884 ms | 0 |
| 2,111 | large-radius fallback | 98.01% | 100% | 2.143 ms | 0 |
| 2,111 | same large candidate, direct full | 100% | n/a | 3.805 ms | 0 |

The production-shaped 2,111-circle case is 3.1x faster per candidate. The
large-radius cases are within run-to-run timing noise of direct full renders;
the dispatch does not turn a legal large proposal into a dirty-region worst
case. Setup storage is retained by the session, so the timed loops perform zero
allocations per evaluation.

The original 599 s live sweep cannot be re-run from this checkout: the
2,111-circle checkpoint behind it is no longer present under
`data/jobs` (the live campaign has advanced), and the checkpoint was not
committed as a fixture. Consequently this report does **not** claim a new
equal-budget wall clock or re-assert its 0.000 result. Exact cost parity is
pinned instead across scattered, clustered, edge-clipped, and radius-growing
active sets, including consecutive candidates and the full-canvas fallback.
Preserving the missing checkpoint, or another immutable production fixture, is
required before closing that final acceptance check.

Dirty scoring also changes the prefix-aware selection tradeoff (Task 13 of
[`../PLAN.md`](../PLAN.md)). Prefix-aware active-set
selection would still shorten the suffix traversal, and baked prefixes remain
useful for the fallback, but ordinary dirty evaluations no longer rasterize
the complete suffix or score the complete canvas. The remaining gain is not
large enough here to justify changing which circles the optimizer can improve;
keep selection quality-driven unless a new end-to-end profile shows otherwise.

## Recommendation

- The current default eight sweeps are not enough for `contiguous-window` on
  large vectors. Raise `--polishing-max-sweeps` to at least
  `ceil(circles / activeSetSize)`, the point at which the window has covered
  every slot, or leave the strategy alone. `app.MaxPolishingSweeps` caps the
  budget at 32, so full coverage is unreachable above `32 * activeSetSize`
  circles -- 160 at the default active set size. Raise
  `--polishing-active-set-size` alongside the sweep budget for larger vectors.
- Prefer a merit-based strategy when wall clock is the budget **and the vector
  is not a greedy fit**. `contiguous-window` reached a worse cost than
  `hybrid-overlap` at equal time in every configuration measured on the
  jittered-truth vector. On the 1000-circle fitted vector the result inverts,
  and by a wide margin -- 1.262 and 1.086 cost units per hour against 0.774 and
  0.870. Which regime you are in is the deciding question, not the strategy.
- On a fitted vector, size `contiguous-window` for total coverage in a single
  pass: `activeSetSize >= ceil(circles / 32)` so that 32 sweeps reach every draw
  slot. Below that the pass never reaches the low draw slots, which is where all
  of its value turned out to be.
- Set `--polishing-min-improvement` against the gain a sweep can actually
  produce at the current cost. It is an absolute bar, and the default retired
  39% of the optimizer budget mid-epoch at cost ~106.
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
`docs/cpu-performance-history.md` or any other machine's report, and re-run
the benchmarks before drawing a conclusion about a different workload.

## Reproducing

```sh
go test -run '^$' -bench BenchmarkPolishCircleBatchStrategy -benchtime 5x -count 3 ./internal/fit/renderer/
go test -run '^$' -bench 'BenchmarkPolishStrategyQuality$' -benchtime 1x -count 3 -timeout 60m ./internal/fit/renderer/
go test -run '^$' -bench BenchmarkPolishStrategyQualityAfterBatchFit -benchtime 1x -count 3 -timeout 60m ./internal/fit/renderer/
go test -run '^$' -bench '^BenchmarkPolishCandidateCost$' -benchmem -benchtime 500ms -count 3 ./internal/fit/renderer/
go test -run '^$' -bench '^BenchmarkPolishDirtyCrossover$' -benchmem -benchtime 150ms -count 1 ./internal/fit/renderer/
```

The quality benchmarks report `final_cost`, `reduction_pct`, and
`accepted_sweeps` per run rather than per `b.N`, so those columns stay
comparable across `-benchtime` values while `ns/op` does not.
