# Restarts versus budget

> **Measured under MayFly v0.5.1.** The pin is now v0.7.1. v0.6.0 reimplemented
> `aoblmoa` and left the other six variants alone, but v0.7.0 reworked the core
> update rules and changes results for **every** variant, `standard` included,
> so none of the numbers below is comparable to a run made today. The
> conclusion — spend a budget on restarts rather than on one long run — is a
> statement about method and carries over; the figures do not. The measurements
> have not been repeated. This
> report did not measure population size as a quality knob, so it neither
> reproduces nor refutes the figures in
> [`seed-variance-and-population-report.md`](seed-variance-and-population-report.md).

A fixed evaluation budget spent as many short independent runs beats the same
budget spent as one long run, by about 160 cost points on the eight-circle
base stage, with a perfect win record across twelve paired blocks. The
improvement survives a strict budget cap, and the mechanism behind it is
measured rather than inferred: the swarm loses its diversity within roughly
fifteen iterations and everything after that is decided.

This report supersedes the working assumption that a stalled fit is cured by a
larger population, a different variant, or a longer run: none of those delays
the collapse. That is a statement about premature convergence, not a verdict on
population size as a quality knob, which this report did not measure. See *What
was eliminated first*.

## What was run

`example/MayFly-512.png` (512x512, blank-canvas cost 38732.12), eight circles,
`mode` batch with `batchSize` 8, `popSize` 1024, `variant` standard,
`parallelEvaluation` with 8 evaluation workers, convergence detection
disabled so early stopping could not distort a budget.

A budget-matched **restart ladder**: every arm spends 2048 iterations per
block and differs only in how that budget is cut into independent restarts.

| Arm | Shape | Runs per block |
| --- | --- | ---: |
| `r01` | 1 x 2048 iterations | 1 |
| `r02` | 2 x 1024 | 2 |
| `r04` | 4 x 512 | 4 |
| `r08` | 8 x 256 | 8 |
| `r16` | 16 x 128 | 16 |
| `r32` | 32 x 64 | 32 |

Twelve blocks per arm, 756 runs, 493,263,354 evaluations in total. An arm's
score for a block is the **minimum** cost over that block's restarts. Blocks
draw seeds from disjoint pools, and within a block every arm shares the same
seed prefix, so `r02`'s first restart is `r01` truncated at 1024 iterations,
`r04`'s first is that truncated again, and so on. The arms are nested rather
than independent, which pairs them as tightly as the design allows.

Conditions, because a cost is only comparable to a cost from the same
renderer: CPU backend, default exact compositor (`fastCompositing` off), one
server with `--max-jobs 8`, 64-core x86-64 AVX2 host, one binary for every
run. The optimizer is MayFly v0.5.1, which changes the crossover operator and
therefore breaks comparability with any v0.5.0 or v0.4.0 figure — including
those in
[`docs/seed-variance-and-population-report.md`](seed-variance-and-population-report.md).

Statistics are paired t-tests across the twelve blocks, df = 11, t_crit = 2.20.

## Result

| Arm | Iterations per run | Mean | sd | Median | Best | Gain over `r01` | Blocks won | t |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `r01` | 2048 | 1047.48 | 148.55 | 977.09 | 891.90 | — | — | — |
| `r02` | 1024 | 958.75 | 98.48 | 930.54 | 858.47 | +88.73 | 8/12 | +3.15 |
| `r04` | 512 | 926.43 | 79.49 | 896.24 | 861.91 | +121.06 | 9/12 | +2.85 |
| `r08` | 256 | 898.97 | 46.79 | 890.18 | 837.05 | +148.51 | **12/12** | +4.21 |
| `r16` | 128 | 887.77 | 56.96 | 898.32 | **781.86** | +159.71 | **12/12** | **+4.49** |
| `r32` | 64 | 886.84 | 48.55 | 884.94 | 805.31 | +160.64 | 11/12 | +4.22 |

Every arm is significant. `r08` and `r16` win every block.

The variance result is worth as much as the mean result in practice: the
standard deviation across blocks falls from 148.55 for a single long run to
46.79–56.96 for the restart arms. Restarting does not only produce a better
answer, it produces a **predictable** one — the single long run's outcome is
largely a lottery on its initialization.

`r16`'s 781.86 is the best base-stage eight-circle cost this project has
recorded. It does not beat the 752.92 reached with polishing at roughly
sixteen times the compute, but the gap is now small.

## The budget confound, and the capped re-analysis

The arms did **not** consume equal compute as run. A batch stage that fails to
place every circle fires residual-refill stages, and those fired more often in
the short-restart arms:

| Arm | Evaluations per block | Versus `r01` |
| --- | ---: | ---: |
| `r01` | 6,397,955 | — |
| `r02` | 6,400,006 | +0.0% |
| `r04` | 6,404,108 | +0.1% |
| `r08` | 6,879,876 | +7.5% |
| `r16` | 7,198,825 | +12.5% |
| `r32` | 7,824,510 | +22.3% |

So the winning arms had up to 22% more compute than the arm they beat. That
has to be removed before the result means anything.

Re-scoring with a hard cap — restarts are admitted in order and a restart that
would push a block past `r01`'s exact 6,397,955 evaluations is discarded along
with everything after it:

| Arm | Restarts that fit | Evaluations used | Mean | Gain over `r01` | Blocks won | t |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `r01` | 1.0 | 6,397,955 | 1047.48 | — | — | — |
| `r02` | 1.0 | 3,200,003 | 1049.49 | -2.01 | 0/12 | -2.46 |
| `r04` | 3.0 | 4,803,081 | 935.40 | +112.08 | 9/12 | +2.70 |
| `r08` | 6.5 | 5,610,772 | 904.90 | +142.58 | **12/12** | +4.05 |
| `r16` | 13.2 | 5,926,475 | **890.05** | **+157.43** | **12/12** | **+4.41** |
| `r32` | 25.4 | 6,175,473 | 894.67 | +152.81 | 10/12 | +3.87 |

The capped restart arms use **7% fewer** evaluations than `r01` and still beat
it by about 157 with a perfect win record. The confound does not explain the
effect.

## The cleanest comparison in the data

The nested seed design makes `r01` and `r02`'s first restart the same run,
measured at two budgets: 2048 iterations and 1024. The difference is exactly
what the second half of a long run buys.

> **Iterations 1025 to 2048 of a single run: +2.01 cost points**
> (sd 2.83, n = 12).
> **The same iterations spent as additional restarts: about +157 points.**

That is the whole finding in one line. It also shows why the capped `r02` row
above reads -2.01 with 0/12 wins: capped `r02` is a single 1024-iteration run
on half the budget, and it lands within two points of a 2048-iteration run on
the full budget.

## How many restarts are needed

Mean best-of-k over the twelve blocks, `r32` arm:

| Restarts | Mean | Gain | Evaluations | Share of `r01` budget |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1108.27 | — | 252,404 | 4% |
| 2 | 1023.61 | +84.66 | 471,153 | 7% |
| 3 | 993.44 | +30.17 | 706,730 | 11% |
| **4** | **959.29** | +34.15 | 942,307 | **15%** |
| 6 | 946.11 | +13.19 | 1,447,114 | 23% |
| 8 | 928.60 | +17.51 | 1,951,921 | 30% |
| 12 | 922.05 | +6.54 | 2,927,881 | 46% |
| 16 | 913.29 | +8.76 | 3,920,668 | 61% |
| 24 | 894.67 | +18.62 | 5,906,242 | 92% |
| 32 | 886.84 | +7.83 | 7,824,510 | 122% |

Four restarts of 64 iterations beat the single 2048-iteration run by 88 points
using 15% of its compute — roughly a sevenfold efficiency gain. Returns then
decay slowly and do not stop within the range measured.

## Restart length barely matters

Every adjacent arm comparison is null:

| Comparison | Mean difference | t | Blocks won |
| --- | ---: | ---: | ---: |
| `r04` over `r02` | +32.33 | +1.38 | 6/12 |
| `r08` over `r04` | +27.46 | +1.69 | 6/12 |
| `r16` over `r08` | +11.20 | +0.87 | 5/12 |
| `r32` over `r16` | +0.94 | +0.07 | 5/12 |

Anything from 512 down to 64 iterations per restart performs the same. Only
`r02` — half the budget in a single run — is meaningfully weaker. **The
decision that matters is whether to restart, not how long each restart is.**

One nuance: a single 64-iteration run averages 1108.27 against a single
2048-iteration run's 1047.48, so an individual run does gain about 61 points
from budget. Since 1024 to 2048 is worth only 2 of those, effectively all of
that gain occurs below 1024 iterations.

## Why: the swarm collapses in about fifteen iterations

The restart result is not a lucky configuration. The mechanism was measured
directly by instrumenting the optimizer, with the probe verified to be free —
an instrumented seed reproduced its uninstrumented cost to the decimal.

RMS pairwise distance across the male population, in the adapter's normalized
[0,1] space, where a uniformly random swarm in 56 dimensions scores 3.06:

| Iteration | 0 | 5 | 10 | 20 | 40 | 80 | 160 | 255 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Spread | 2.37 | 0.65 | 0.34 | 0.29 | 0.14 | 0.10 | 0.028 | 0.039 |
| Share of initial | 100% | 27% | 14% | 12% | 5.8% | 4.3% | 1.2% | 1.6% |

Across four seeds, spread falls below 10% of its initial value at iteration 11
to 16, by which point 80% to 96% of the run's total cost gain is already
banked. Final per-circle spread is 1e-3 to 1e-4 in normalized units: on a
512-pixel canvas the entire population of 1024 sits inside a sub-pixel box.

Population size does not delay this. Measured at seed 1:

| Population | Spread below 10% at iteration | Final cost |
| ---: | ---: | ---: |
| 64 | 17 | 1253.34 |
| 256 | 20 | 976.47 |
| 1024 | 15 | 940.50 |
| 4096 | 16 | 965.48 |

Sixty-four times the individuals buys one iteration of extra diversity. That is
the claim this arm supports, and it is the only one: the collapse iteration is
flat in population size.

The final-cost column is **not** a quality result. It is one seed per
population, under MayFly v0.5.1, and the spread it shows — 1253.34 at 64
against 940.50 at 1024, 313 points — is far larger than the differences this
report calls significant elsewhere on twelve paired blocks. A single seed
cannot separate a population effect from the initialization lottery documented
in the *Result* section, where a single long run's sd across blocks is 148.55.
Read the column as evidence about diversity collapse, not about which
population to run.

Nothing here contradicts
[`docs/seed-variance-and-population-report.md`](seed-variance-and-population-report.md),
which measured under MayFly v0.5.0 that quality improves monotonically with
`popSize` to about 1024. That finding stands for the version it was measured
on, it has not been re-measured under v0.5.1, and this report did not attempt
to reproduce or refute it.

A 2048-iteration run at seed 1 reaches mean velocity 0.0000 by iteration 1280
and its best cost is frozen at 936.53 from about iteration 640 onward —
roughly 1.4 million evaluations producing no change at all.

The crossover operator is not at fault. Instrumenting every offspring against
its parents over a 2048-iteration run:

| Iterations | Offspring | Beat both parents | Mean excess over better parent |
| --- | ---: | ---: | ---: |
| 0–19 | 20,480 | 10.3% | +11.5% |
| 160–639 | 491,520 | 9.4% | +0.269% |
| 640–1279 | 655,360 | 1.5% | +0.010% |
| 1280–2047 | 786,432 | **0.0%** | +0.002% |

In the final 786,432 offspring not one beat both parents, and offspring differ
from parents by 0.002% in cost. Crossover is being handed two copies of the
same solution. This also explains why raising the offspring count `NC` beyond
about 6% of the population buys nothing: the extra offspring are duplicates.

Two hypotheses were tested against this data and rejected. **Competing
conventions** — that circles encoded in different orders make blending
meaningless — predicts that aligning the parents' circles before crossover
would help. A greedy permutation match on seven-float blocks moved the
beats-both rate from 8.3% to 7.2%, slightly worse; the parents already agree on
ordering. **A deceptive landscape** predicts offspring far worse than their
parents; the measured mean excess is about 2%.

The diagnosis is premature convergence, and restarts address it directly.

## What was eliminated first

Every one of these was measured on the same reference image and host, and none
of them delayed the diversity collapse or produced a significant improvement in
the respect noted. They are recorded so the same negative result is not
re-derived. Read each row for what it actually rules out; a row that eliminates
an intervention as a fix for premature convergence does not eliminate it as a
knob in general.

| Intervention | Result |
| --- | --- |
| Population size 64 to 4096 | Does not delay the collapse (spread falls below 10% at iteration 11–16 at every population). Whether it improves quality was **not** measured here — one seed per population |
| `NC` beyond about 64 offspring | Null (t = -0.27 at `NC` 64 versus 1024) |
| `DanceDamp` 0.8 / 0.9 / 0.95 / 0.99 / 1.0 | All null; several seeds bit-identical across values |
| MayFly v0.5.0 versus v0.5.1 blend crossover | Null (t = -0.15, 12 paired seeds) |
| Longer budget on a single run | +2.01 points for doubling 1024 to 2048 |
| Permutation-matched crossover | Slightly worse |

The only intervention that previously produced a better eight-circle result
was polishing at roughly sixteen times the compute, which works for the same
reason restarts do: it re-initializes a sub-problem.

## What this report does not say

- It does not measure restarts on stages other than the eight-circle base
  stage. Extend and polish stages start from a fitted vector, not a cold
  population, and the collapse dynamics there are unmeasured.
- It does not establish a best restart length. Everything from 512 down to 64
  iterations is statistically indistinguishable here, and the ladder was not
  extended below 64.
- The measurement is under MayFly v0.5.1 with `variant` standard only. The
  other six variants were not run in this ladder.
- Returns had not flattened at 32 restarts, so the ladder does not locate an
  upper bound on useful restart count.
- It does not compare cold restarts against `--optimizer-epochs`. Every arm ran
  with `optimizerEpochs: 1`, and an epoch already re-initializes half the
  population globally, so the relative value of the two is unmeasured.
- It does not measure whether population size improves quality. The population
  arm was one seed per population and was run to locate the collapse iteration.
  The v0.5.0 finding that quality improves monotonically to about `popSize`
  1024, in
  [`docs/seed-variance-and-population-report.md`](seed-variance-and-population-report.md),
  is untouched by this report.

## What to change

`--optimizer-epochs` already does part of this, and the ladder did not measure
how much. Each epoch advances to a fresh deterministic seed and reseeds from
the previous best result — but with no continuation profile configured,
`seededPopulationFromCandidates` in `internal/opt/mayfly_adapter.go` uses
`localFraction` 0.5, so only half the population is drawn around the incumbent
and the other half is initialized globally. An epoch is therefore a partial
re-initialization, not a pure continuation, and it is not obvious in advance
how much of the restart gain it already captures.

What the ladder measured is cold restarts against a single long run: every arm
in it ran with `optimizerEpochs: 1`. The relative value of epochs versus cold
restarts at a matched budget is **unmeasured**, and it should be measured
before a separate restart surface is designed — a new mode that duplicates the
existing mechanism would be worse than tuning the mechanism. A full restart
does differ in kind: independent re-initialization of the whole population plus
best-of selection across attempts.

Concretely, the smallest change that captures most of the measured gain is to
spend a stage's budget as four or more cold attempts and keep the best, rather
than as one long attempt. On this workload that was 88 points better for 15%
of the compute at four attempts, and about 157 points better at equal budget.

Tracked as Task 15.11.
