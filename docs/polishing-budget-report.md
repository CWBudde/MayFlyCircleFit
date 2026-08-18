# Polishing budget report

**Baseline date:** 2026-08-17
**Hardware:** AMD Ryzen 5 4600H, 12 logical cores, `GOMAXPROCS=12`
**Backend:** CPU renderer, AVX2 SSD, one render thread per configuration unless
stated
**Benchmarks:** `BenchmarkPolishBudgetShape`,
`BenchmarkPolishBudgetSweepFalloff`, `BenchmarkPolishBudgetShippedConfiguration`,
`BenchmarkPolishBudgetProductionShape` (`internal/fit/renderer`)

Polishing used to have no budget of its own. It ran `PolishingIters` and
`PolishingEpochs`, both of which are its own, at the job-wide `PopSize`, which is
not: that number is chosen for a whole vector. A run configured for 512 circles
therefore optimized an eight-circle active set -- 56 free parameters -- with a
population of 200. This note measures what the population, the iteration count,
and the epoch count actually buy at the dimensionality polishing works on, and
`polishingPopSize` exists because of what it found.

The comparison metric throughout is **cost removed per second**, not cost removed
per sweep. A larger budget nearly always removes at least as much error per
sweep; the question is whether it removes more than spending the same wall clock
another way.

## What is measured

The fixture is the output of a real batch fit -- 64 circles at 128x128, batch
size 8, 400 iterations at population 30, seed 4242 -- because that is the only
kind of vector `PolishCircleBatchContext` ever sees in production. It is the same
starting vector `BenchmarkPolishStrategyQualityAfterBatchFit` uses, so the two
reports describe the same input. Its starting cost is 625.2.

Every configuration shares seed 4242, `activeSetSize` 5 (the shipped default),
one render thread, and three sweeps unless stated. Two strategies are measured:
`replacement`, which is the shipped default, and `residual-region`, which is what
the long incremental runs use. A default derived from one strategy is only a
default if it also holds for the other.

The axes move one at a time around a fixed centre (population 30, 200 iterations,
one epoch) rather than as a full grid; a full grid at these budgets does not
finish in a sitting. Cross terms are therefore not measured.

Measurements are medians of three unless a section states otherwise. Polishing
is deterministic for a
fixed seed and configuration, so `final_cost`, `reduction_pct` and
`accepted_sweeps` are identical across repetitions and only the wall clock
varies.

## The population axis

200 iterations, one epoch, three sweeps.

| Population | `replacement` | | | `residual-region` | | |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| | Wall clock | Removed | Per second | Wall clock | Removed | Per second |
| 20 | 2.31 s | 0.7% | 1.82 | 3.61 s | 7.9% | 13.64 |
| 30 | 3.12 s | 1.6% | 3.21 | 5.17 s | **11.8%** | **14.31** |
| 50 | 4.72 s | **6.1%** | **8.08** | 7.26 s | 9.3% | 7.99 |
| 100 | 8.54 s | 7.1% | 5.19 | 13.01 s | 10.8% | 5.19 |
| 200 | 16.38 s | 4.6% | 1.74 | 24.67 s | 12.8% | 3.23 |

Wall clock is very close to linear in the population, as it must be: the
population is how many candidates an iteration evaluates. Quality is not. On
`residual-region` a population of 200 costs 4.8x what 30 costs and removes 12.8%
against 11.8% -- one extra percentage point for nearly five times the wall clock.
On `replacement` the largest population is *worse* than a fifth of it, because
acceptance is discrete: a sweep either clears the gate or contributes nothing,
and a different trajectory lands on a different side of it.

That non-monotonicity is the finding that matters most here. "More budget is at
least as good" is false for this optimizer at this dimensionality, so a
population inherited from a whole-vector configuration is not a safe default,
it is a bet.

## The iteration and epoch axes

Population 30, three sweeps. The epoch rows and the 400-iteration row cost
roughly the same wall clock, which is the comparison worth reading.

| Budget | `replacement` | | `residual-region` | |
| --- | ---: | ---: | ---: | ---: |
| | Wall clock | Removed | Wall clock | Removed |
| 50 iters × 1 | 0.91 s | 0.8% | 1.31 s | 5.6% |
| 100 iters × 1 | 1.69 s | 2.7% | 2.46 s | 8.1% |
| 200 iters × 1 | 3.12 s | 1.6% | 5.17 s | 11.8% |
| 400 iters × 1 | 6.15 s | 4.5% | 9.49 s | 9.7% |
| 800 iters × 1 | 12.54 s | 14.2% | 20.25 s | 12.3% |
| 200 iters × 2 | 6.18 s | **10.8%** | 9.14 s | 9.3% |
| 200 iters × 4 | 11.98 s | 8.1% | 18.34 s | 10.9% |

Two epochs of 200 iterations beat one epoch of 400 at the same wall clock on
`replacement` -- 10.8% against 4.5% -- and draw with it on `residual-region`.
Four epochs are worse than two on both while costing twice as much.
`PolishingEpochs` 2 is therefore kept, now for a measured reason rather than an
inherited one.

Cost removed per second falls monotonically with the iteration count on
`residual-region`: 26.65 at 50 iterations, 20.56 at 100, 14.31 at 200, 6.39 at
400, 3.79 at 800. The knee is far to the left of the 1000 iterations
`PolishingIters` used to default to. What that does *not* on its own license is
lowering the default, because a real run also carries stagnation-based early
stopping, which can truncate a long epoch before it costs what it nominally
costs. The whole-configuration table below carries that rule and settles it.

## Sweep-by-sweep falloff

Population 30, 200 iterations, one epoch, eight sweeps -- at the time of
measurement, twice the sweep budget. Cost removed by each individual sweep, from
the polishing observer:

| Sweep | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `replacement` | 10.02 | — | — | — | 7.24 | — | — | — |
| `residual-region` | 25.18 | 24.92 | 23.87 | **54.97** | 17.85 | 7.73 | 4.04 | 7.61 |

A dash is a sweep that was rejected and removed nothing.

The task this report answers quoted a live run whose second sweep removed a fifth
of what its first did, and asked whether the sweep budget should shrink. On this
fixture, under the current acceptance gate, the opposite holds. `residual-region`
is still removing at full rate at sweep 4 -- its largest single gain of the eight
-- and three sweeps capture 74 of the 166 cost units the eight
sweeps remove, or 44%. `replacement` removes nothing at all in sweeps 2 to 4 and
then removes 7.24 in sweep 5, which three sweeps never reach.

Both curves say the same thing: at this active-set size the budget is better
spent on more sweeps than on a longer or wider sweep. A sweep re-selects its
active set, so it is the only axis that moves the optimizer onto different
circles; population and iterations only refine the same 35 parameters harder.

The earlier 5x falloff was measured under the old absolute acceptance gate, where
most sweeps were vetoed regardless of what they found (PLAN task 15.6). It should
not be compared against these numbers.

## Whole configurations, with early stopping

The axes above run without early stopping so that a budget costs what it says.
A real run configures `PolishingStagnationIters` against
`PolishingMinImprovement` 0.001, so this table repeats three complete
configurations with that rule in force, each carrying its own stagnation window
of half its epoch. "Inherited population" is what a 512-circle job used to hand
polishing through `popSize`.

Medians of three, except the inherited-population row, which is a median of two
because each repetition costs six minutes.

| Configuration | Strategy | Wall clock | Accepted | Final cost | Removed | Per second |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Previous defaults: pop 30, 1000 × 2, 3 sweeps | `replacement` | 36.7 s | 3 / 3 | 574.8 | 8.1% | 1.37 |
| Previous defaults | `residual-region` | 72.5 s | 3 / 3 | 568.9 | 9.0% | 0.78 |
| Inherited population: pop 200, 1000 × 2, 3 sweeps | `residual-region` | 373.9 s | 3 / 3 | 553.4 | 11.5% | 0.19 |
| Current defaults: pop 30, 200 × 2, 8 sweeps | `replacement` | **27.1 s** | 4 / 8 | 555.9 | **11.1%** | **2.56** |
| Current defaults | `residual-region` | **40.3 s** | 7 / 8 | **480.4** | **23.2%** | **3.60** |

Read the rows in pairs. Against the previous defaults, the current ones remove
2.6x the error in 56% of the wall clock on `residual-region`, and 1.4x the error
in 74% of it on `replacement`. Against the previous defaults with the population
a 512-circle job used to hand them, they remove 2.0x the error in 11% of the wall
clock: the inherited population buys 1.28x the error for 5.2x the time, which is
the whole case for `polishingPopSize` in one row.

Early stopping fires in none of these configurations -- the current-defaults rows
report exactly the evaluation count the stagnation-500 variant of the same budget
did -- so no epoch here stagnated for half its length. The stopping rule is
carried anyway because it is what ships, and because a row measured with a
stopping rule copied from a longer epoch would not be the row that ships.

## The production shape

`residual-region` at 512x512 with 256 circles and `activeSetSize` 8 -- the shape
the long incremental runs polish at, and the 56-parameter active set the
inherited population of 200 was being spent on. Rendering uses all 12 threads;
two sweeps; 200 iterations; one epoch.

Minima of two rather than medians: the widest configurations allocate 0.5 to
0.7 GB per run on a 14 GB machine and the slower repetition of each was up to
1.9x the faster one, which is the machine and not the budget.

| Population | Wall clock | Accepted | Removed |
| ---: | ---: | ---: | ---: |
| 20 | 23.2 s | 1 / 2 | 10.0% |
| 30 | 31.0 s | 0 / 2 | — |
| 50 | 46.8 s | 0 / 2 | — |
| 100 | 84.6 s | 0 / 2 | — |
| 200 | 275.2 s | 0 / 2 | — |

Only the smallest population landed an accepted sweep here, and it is the one
that is not evidence of anything: with two sweeps at this shape, acceptance is
close to a coin toss and one head is not a trend. What the column *does* show
without ambiguity is the cost side. Wall clock is linear in the population --
11.8x from population 20 to 200, for a 10x population -- while the quality it
buys at a fixed 200 iterations is, at this shape, nothing at all for four of the
five. A budget that cannot land a sweep is spent either way, so it should be the
cheap one, and the sweeps it saves should be spent on more sweeps.

The quality conclusions in this report rest on the 128x128 fixture above, where
three sweeps and two strategies give the gate enough chances to be informative.
This section is a cost-scaling measurement.

## Recommendation

The defaults in `app.DefaultConfig` are set from these measurements. Each is
named as a constant in `internal/app/config.go` so the CLI flag and the
configuration cannot drift apart.

| Setting | Was | Is | Because |
| --- | ---: | ---: | --- |
| `polishingPopSize` | inherited `popSize` | **30** | Population 200 costs 5.2x the wall clock for 1.28x the error removed, and on `replacement` a larger population reached a *worse* cost than a fifth of it. |
| `polishingIters` | 1000 | **200** | Error removed per second falls monotonically with the iteration count: 26.7 at 50 iterations against 3.8 at 800. |
| `polishingEpochs` | 2 | **2** | Unchanged, and now measured: two epochs of 200 beat one of 400 at the same wall clock, and four are worse than two at twice the cost. |
| `polishingMaxSweeps` | 3 | **8** | `residual-region` was still removing at full rate at sweep 4 and three sweeps captured 44% of what eight remove; `replacement` removed nothing in sweeps 2 to 4 and 7.24 units in sweep 5. |
| `polishingStagnationIters` | 500 | **100** | Half the epoch, which is the ratio every default has shipped. It follows `polishingIters`; the validator also refuses a stagnation window longer than the epoch it stops. |

Three consequences worth stating plainly:

- **A checkpoint written before `polishingPopSize` existed resumes at the
  polishing default, not at its own `popSize`.** An omitted value resolves to the
  default like every other `Polishing*` field, deliberately unlike
  `evaluationWorkers`, which kept inheriting for compatibility. Set
  `polishingPopSize` explicitly to reproduce an older run exactly.
- **A polish stage now authorizes 3 200 optimizer iterations instead of 6 000**,
  so a schedule's planned-iteration figure drops: the documented 512-circle
  campaign goes from 48 800 to 32 000. It is a budget ceiling, not a prediction.
- **Sweeps are the axis to raise**, if any. Population and iterations refine the
  same active set harder; only a new sweep re-selects which circles are being
  optimized at all.

What this does not establish: one fixture, one image size for the quality
measurements, one seed, and axes swept one at a time. Cross terms are unmeasured,
and `hybrid-overlap` and `contiguous-window` were not measured here at all --
they inherit these defaults on the argument that a budget too large to pay for
itself on two strategies is unlikely to pay for itself on a third.

## Reproducing

```sh
go test -run '^$' -bench BenchmarkPolishBudgetShape -benchtime 1x -count 3 -timeout 120m ./internal/fit/renderer/
go test -run '^$' -bench BenchmarkPolishBudgetSweepFalloff -benchtime 1x -count 3 -timeout 120m ./internal/fit/renderer/
go test -run '^$' -bench BenchmarkPolishBudgetShippedConfiguration -benchtime 1x -count 2 -timeout 240m ./internal/fit/renderer/
go test -run '^$' -bench BenchmarkPolishBudgetProductionShape -benchtime 1x -count 2 -timeout 240m ./internal/fit/renderer/
```

`final_cost`, `reduction_pct`, `accepted_sweeps`, `evaluations` and
`removed_per_s` are reported per run rather than per `b.N`, so they stay
comparable across `-benchtime` values while `ns/op` does not.

These are wall-clock comparisons on one machine, one reference, one fixture and
one seed. Per the repository convention, do not compare these absolute timings
against another machine's report, and re-run the benchmarks before drawing a
conclusion about a different workload.
