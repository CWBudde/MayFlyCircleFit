# The restart-shape campaign

**A CMA-ES default should name the budget-filling shape.** Three arms at a
shared cap, one fixture, twenty-four paired blocks, and the two registered
contrasts split cleanly. Filling the budget with cold restarts is
**indistinguishable from an IPOP ladder** — `+3.82` at `t = +0.23`, `p = 0.82`,
12 of 24 — while **beating a fixed count of 32** by `+6.89` at `t = +2.89`,
`p = 0.0083`, which rejects under Holm.

The second result is stronger than its t-statistic reads, because the paired
differences are one-sided by construction: the two cold arms share a seed and
run the same trajectory until the fixed count is exhausted, so **14 blocks are
bit-identical ties, 10 are wins, and none is a loss.** And the mechanism is
exact rather than inferred. In precisely those ten blocks the filling arm took
its block best from a restart index of 32 or higher — an attempt the fixed arm
never ran. The block sets are identical, not merely the same size.

**What tips the recommendation is not the mean but the spread.** IPOP matches
filling on the mean while carrying a standard deviation of 71.36 against 22.31,
and its range spans 325 points against the filling arm's 88. It holds the
campaign's best single result by 60 points and its worst by 178. A default has
to be defensible on a single run, and on that criterion the two arms are not
equivalent even though the primary contrast cannot separate them.

**This is a shape result, not an engine result.** Nothing here compares CMA-ES
against MayFly, and nothing here licenses changing the optimizer default. It
answers the question
[`cmaes-budget-split-report.md`](cmaes-budget-split-report.md) left open — *a
default change must name a restart shape, not just an optimizer* — and it is
the first campaign in this repository to name one with a rejected contrast
behind it.

## Conditions

| | |
| --- | --- |
| binary | `6e8705c` on `feat/restart-shape-campaign`, cross-built and shipped to the host; merged to `main` as `8bff706` with no change to `restartShapeArms` |
| analysis | the same binary, run on the host by the campaign supervisor |
| optimizer | `github.com/CWBudde/go-cma-es v0.1.0` — every arm is CMA-ES; no MayFly code runs in this design |
| fixture | `example/MayFly-512.png`, md5 `76c44ab079154956dfadd481b08204a9`, 8 circles in one batch, 56 dimensions |
| budget | `defaultBudget` = 6,502,400 evaluations, nominal, per arm |
| backend | `cpu`, `evaluationWorkers: 8` |
| host | the 64-core campaign host at `--max-jobs 8`, port 8085 |
| dates | submitted 2026-09-05 13:22:34, finished 2026-09-05 20:13:46 — 06:51 of wall clock, 187,066 job-seconds (52.0 job-hours) |
| seeds | 119001-119024, twenty-four paired blocks |
| jobs | 72 of 72 completed; none failed, none cancelled |

**These costs may be compared against every CMA-ES campaign that ran at
`defaultBudget`** — the nine citable ones listed in AGENTS.md — and **not**
against [`cmaes-deep-hunt-report.md`](cmaes-deep-hunt-report.md) or
[`cmaes-covariance-report.md`](cmaes-covariance-report.md), which ran at 1.94x
this cap.

An earlier submission of this campaign was aborted after about fifty minutes
because its design contained an unarmed BIPOP arm, which go-cma-es runs as a
plain IPOP ladder. No result from it was read, and the guard that would have
caught it — `TestNoRegisteredDesignRunsAnUnarmedBipopArm` — was added before
the relaunch. The data reported here is entirely from the relaunched run.

## The design

Three arms, all full covariance at `lambda` 64, all capped at 6,502,400
evaluations. They differ in one thing: how that cap is spent.

| arm | shape | per-attempt cap | attempts |
| --- | --- | ---: | --- |
| `full-ipop-l64` | IPOP ladder, `lambda` doubling from 64 | 101,600 iterations | one ladder |
| `full-r32-l64` | 32 cold restarts, fixed count | 3,175 iterations | exactly 32 |
| `full-fill-l64` | cold restarts until the budget is gone | 3,175 iterations | as many as fit |

Two choices in that table are load-bearing and were made before any result was
read.

**Full covariance, because it is the only mode that never clamps.** go-cma-es
v0.1.0 clamps the rank-mu rate above a `lambda` threshold, which zeroes the
covariance decay — separable above 256 at 56 dimensions, block above 1024, full
never. An IPOP ladder decides its top rung at run time, so a design that pinned
separable or block could walk into the clamp mid-run and no one would know
which rungs were degenerate. `restartShapeArms` asserts this rather than
trusting it, walking every rung the ladder could reach up to `8 * defaultPop`
and refusing to build the design if any of them clamps. The
[clean-rung covariance campaign](cmaes-covariance-clean-report.md) is what makes
that choice free: with block and separable measured as indistinguishable where
both are clean, pinning full costs nothing in generality.

**`lambda` 64, because it is the rung where the shapes are comparable.** The
cold arms need a per-attempt budget small enough that 32 attempts fit inside the
cap; `ladderWork` = 2048 fixes the product, so `lambda` 64 buys 3,175 iterations
per attempt and exactly 32 of them. It also sits above `app.MinPopulation` and
below every clamp threshold.

### The family

Two contrasts, registered before submission, corrected together with Holm
step-down at a family-wise alpha of 0.05:

| | contrast | role |
| --- | --- | --- |
| 1 | `full-fill-l64` against `full-ipop-l64` | **primary** |
| 2 | `full-fill-l64` against `full-r32-l64` | secondary |

The uncorrected two-sided threshold at `df = 23` is `t = 2.07`; the Bonferroni
one is `t = 2.40`.

## The registered result

| arm | mean | sd | median | best | worst | spend | % of cap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `full-ipop-l64` | 900.82 | 71.36 | 902.15 | **789.80** | 1115.26 | 6,502,403 | 100.0% |
| `full-r32-l64` | 903.89 | 27.45 | 906.75 | 849.27 | 948.08 | 3,353,403 | 51.6% |
| `full-fill-l64` | **897.00** | 22.31 | 900.96 | 849.27 | 937.05 | 6,359,486 | 97.8% |

| contrast | gain | sd | t (df=23) | p | 95% paired interval | blocks | Holm |
| --- | ---: | ---: | ---: | ---: | --- | ---: | --- |
| fill vs `full-ipop-l64` | +3.82 | 80.65 | +0.23 | 0.8186 | `-30.24` to `+37.88` | 12/24 | retain |
| fill vs `full-r32-l64` | +6.89 | 11.69 | +2.89 | 0.0083 | `+1.96` to `+11.83` | 10/24 | **reject** |

Positive means the filling arm is cheaper, which is better.

### Per block

| block | `full-ipop-l64` | `full-r32-l64` | `full-fill-l64` | fill vs ipop | fill vs r32 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 928.3669 | 872.1171 | 872.1171 | +56.25 | 0.00 |
| 2 | 939.8898 | 903.9670 | 903.9670 | +35.92 | 0.00 |
| 3 | **789.7981** | 916.5509 | 915.0165 | -125.22 | +1.53 |
| 4 | 950.7167 | 927.0343 | 919.6700 | +31.05 | +7.36 |
| 5 | 877.8552 | 930.1159 | 915.0661 | -37.21 | +15.05 |
| 6 | 822.5835 | 916.7844 | 916.7844 | -94.20 | 0.00 |
| 7 | 915.6074 | 897.9081 | 887.8133 | +27.79 | +10.09 |
| 8 | 932.5142 | 938.2757 | 910.6546 | +21.86 | +27.62 |
| 9 | 886.0367 | 876.6106 | 876.6106 | +9.43 | 0.00 |
| 10 | 884.0602 | 899.5811 | 899.5811 | -15.52 | 0.00 |
| 11 | 1051.5129 | 927.1566 | 910.0478 | +141.47 | +17.11 |
| 12 | 907.4877 | 912.3834 | 912.3834 | -4.90 | 0.00 |
| 13 | 842.7489 | 896.2070 | 896.2070 | -53.46 | 0.00 |
| 14 | 906.9430 | 912.3667 | 912.3667 | -5.42 | 0.00 |
| 15 | 871.7830 | 937.0543 | 937.0543 | -65.27 | 0.00 |
| 16 | 915.0219 | 928.6358 | 907.1856 | +7.84 | +21.45 |
| 17 | 901.9378 | 901.9378 | 892.4474 | +9.49 | +9.49 |
| 18 | **1115.2608** | 849.2734 | **849.2734** | +265.99 | 0.00 |
| 19 | 931.0762 | 854.1974 | 854.1974 | +76.88 | 0.00 |
| 20 | 902.3667 | 909.5399 | 900.9207 | +1.45 | +8.62 |
| 21 | 811.7593 | 897.0685 | 897.0685 | -85.31 | 0.00 |
| 22 | 853.1025 | 853.1025 | 853.1025 | 0.00 | 0.00 |
| 23 | 833.7506 | 887.4833 | 887.4833 | -53.73 | 0.00 |
| 24 | 847.5028 | 948.0775 | 901.0019 | -53.50 | +47.08 |

Block 22 is the campaign's curiosity: all three arms return 853.1025. The
paired difference against IPOP is exactly zero there, which is why the primary's
`12/24` is twelve wins, eleven losses and one tie rather than twelve and twelve.

## What the secondary establishes, and why the ties matter

`+6.89` is a small number against a mean of 897 — 0.77% — and on its own the
`t = 2.89` would be an unremarkable rejection. The block pattern is what makes
it worth acting on.

The two cold arms are the same search with the same seed. `full-r32-l64` stops
after 32 attempts; `full-fill-l64` keeps drawing attempts until the budget will
not fit another. Every attempt the fixed arm runs, the filling arm runs
identically. So the paired difference **cannot be negative** — the filling arm
has already seen everything the fixed arm saw — and the only question is whether
the extra attempts find anything.

They did, in 10 blocks of 24. In the other 14 the difference is bit-identical
zero. The distribution of restart indices that produced each block's best says
the same thing from the other side:

| arm | restart index of the block best |
| --- | --- |
| `full-r32-l64` | 0 to 27; never above 27, and it cannot exceed 31 |
| `full-fill-l64` | 0 to 52; **10 blocks take their best from index ≥ 32** |

Those ten blocks are `3, 4, 5, 7, 8, 11, 16, 17, 20, 24` — and the ten blocks
where the filling arm wins are `3, 4, 5, 7, 8, 11, 16, 17, 20, 24`. The sets are
identical. The filling arm wins exactly when, and only when, it finds its best
in an attempt the fixed count never ran. That is as direct a mechanism as this
corpus contains, and it makes the effect a property of the shape rather than of
the seeds.

It also bounds the effect honestly. What the filling shape buys is roughly a
40% chance per block of an improvement averaging 16.5 points. It is not a large
win and it should not be sold as one; it is a free one, because both arms sit
under the same cap and the fixed arm simply leaves 48% of it unspent.

## The primary is a null with a wide interval, and the spread is the reason

`+3.82` with an interval from `-30.24` to `+37.88` does not separate the two
shapes on the mean, and the interval is wide enough that it would not have
detected a 30-point effect either. That width is not sampling bad luck: the
paired standard deviation is 80.65, against the secondary's 11.69, because
IPOP's per-block results scatter across 325 points.

| arm | sd | range | best | worst |
| --- | ---: | ---: | ---: | ---: |
| `full-ipop-l64` | 71.36 | 325.5 | 789.80 | 1115.26 |
| `full-r32-l64` | 27.45 | 98.8 | 849.27 | 948.08 |
| `full-fill-l64` | 22.31 | 87.8 | 849.27 | 937.05 |

This is the tail-versus-mean split
[`cmaes-restart-ladder-report.md`](cmaes-restart-ladder-report.md) ran into,
reproduced on a different design. IPOP reaches results neither fixed-`lambda`
arm reaches — 789.80, 811.76, 822.58, all below the filling arm's best of
849.27 — and it also produces 1115.26 and 1051.51, which no cold-restart arm
comes near. A campaign that reads the mean will keep returning nulls on this
arm.

**The spread comparison is unregistered.** The design registered two contrasts
on the mean and no test of dispersion, so the sd column is a description of
what happened, not a result. It is a lead for a design that registers a
variance or a quantile contrast, and it is the reason the recommendation below
is stated as a judgement about defaults rather than as a measured finding.

## Mechanism: IPOP's top rung produces nothing

The restart records explain both halves of IPOP's behaviour, and they are the
first records this repository has for the cold arms at all — see
[Records for every attempt](#records-for-every-attempt).

`full-ipop-l64` runs 6 or 7 attempts per block, mean 6.5, doubling `lambda`
from 64. Across 156 attempts, 131 end on `tol_fun`, one on `condition_number`,
and 24 — exactly one per block, always the last — on `maximum_evaluations`. The
ladder reaches `lambda` 4096 in 12 of 24 blocks.

**In none of the 24 blocks did the block best come from `lambda` 4096.**

| rung | blocks whose best came from it |
| ---: | ---: |
| 64 | 3 |
| 128 | 1 |
| 256 | 3 |
| 512 | 4 |
| 1024 | 5 |
| 2048 | 8 |
| 4096 | **0** |

So in the twelve blocks that reach it, the largest and most expensive rung is
also the one that is truncated by the cap, and it never repays that spend. The
useful work concentrates at 512 to 2048. This is a sharper version of the same
observation
[`cmaes-covariance-report.md`](cmaes-covariance-report.md) made about its top
rung, and it suggests the productive question about IPOP is not whether to use
it but **where to stop the ladder** — a rung ceiling is a knob nothing in this
corpus has varied.

The cold arms tell a simpler story. `full-r32-l64` runs exactly 32 attempts in
every block, 744 of 768 ending on `convergence`, and spends 51.6% of its cap:
a fixed count cannot refill what an attempt that trips `TolFun` leaves behind,
which is exactly the defect
[`cmaes-covariance-clean-report.md`](cmaes-covariance-clean-report.md) flagged
in `blk-r2-l1024`. `full-fill-l64` runs 58 to 64 attempts, mean 61.0, and
recovers the budget to 97.8%.

**The filling arm hit `app.MaxOptimizerRestarts` once.** Block 3 ran exactly 64
attempts, the ceiling, so in that one block the shape was bounded by the limit
rather than by the budget. It is 1 block of 24 and the arm still won that block,
so nothing here turns on it — but a filling shape at a smaller `lambda` would
hit the ceiling routinely, and the constant would then need raising before the
shape could be measured at all.

## Diagnostics

`distributionExtent` never exceeds **1.0821** across 14,519 samples, while
sigma reaches 1.342e+07. This reproduces, on fresh seeds and in full covariance
mode, the finding
[`cmaes-lambda-report.md`](cmaes-lambda-report.md) established and
[`cmaes-covariance-clean-report.md`](cmaes-covariance-clean-report.md) confirmed:
the identifiable `sigma * max(D)` stays bounded near 1 while sigma alone spans
orders of magnitude. **Cite the extent column; never cite sigma.** Every sample
carried a value — no row was missing the diagnostic.

Per arm, the maxima are 1.0821 for IPOP, 0.5816 for the filling arm and 0.4990
for the fixed one, which is consistent with the ladder searching at larger
scales without ever diverging.

## Unregistered by-products

### Records for every attempt

This is the first campaign to run after the fix in
`internal/opt/restart_optimizer.go` that synthesizes a restart record for every
attempt rather than only for budget-filling schedules. It works: the restarts
CSV carries 2,387 rows, of which 2,231 come from the two cold arms, where
earlier campaigns recorded nothing at all. The exact-mechanism result above is
only readable because of it.

Two fields are empty in the synthesized records. `population` is written as `0`
and `regime` as the empty string for all 2,231 cold-arm rows, because the
wrapper does not know either — it sees only the inner optimizer's `Result`.
Neither is wrong for a fixed-`lambda` cold restart, where the population is the
job's `popSize` and there is no regime, but a reader joining the restarts CSV
against the measurement CSV has to supply them. Worth filling in from the job
configuration if the wrapper is touched again.

### Wall-clock cost of each shape

| arm | mean job | spend |
| --- | ---: | ---: |
| `full-r32-l64` | 1,826 s | 51.6% |
| `full-ipop-l64` | 2,515 s | 100.0% |
| `full-fill-l64` | 3,453 s | 97.8% |

The filling arm costs 37% more wall clock than IPOP at 2% *less* nominal spend,
because a cold restart at `lambda` 64 runs 99,367 iterations against the
ladder's 9,713, and per-iteration overhead dominates at small populations. A
default that names the filling shape is buying its lower variance with real
time, not only with evaluations. That is a fair price at these numbers, but it
is a cost the evaluation cap does not show.

## The record is unchanged

The campaign best is **789.7981224060**, from `full-ipop-l64` in block 3. The
standing record on this fixture is **726.1984354654948**, from
[`cmaes-deep-hunt-report.md`](cmaes-deep-hunt-report.md), so the campaign misses
it by 63.60 — and the comparison is not meaningful anyway, because the deep
hunt ran at 1.94x this cap.

**The driver's own record line is wrong and was wrong before this campaign.**
It reported `did not beat it by +37.2761103312` against 752.5220120748, which is
the record the deep hunt superseded. `recordCost` and `recordCircles()` in
`scripts/cmaes-measurement/main.go` still carry the old solution, as AGENTS.md
already notes. Nothing in this report depends on that line, but it will keep
misreporting until the constants are updated.

## What this does and does not license

**It licenses naming budget-filling cold restarts as the CMA-ES restart shape**
in documentation and in a schedule's recommended defaults. It is the only shape
in the corpus with a rejected contrast behind it, it strictly dominates a fixed
count, it matches the IPOP ladder on the mean, and it does so with a third of
the ladder's variance.

**It does not license changing the optimizer default from MayFly.** No MayFly
arm ran here. The engine question rests where
[`cmaes-budget-split-report.md`](cmaes-budget-split-report.md) left it.

**It does not license retiring IPOP.** The primary is a null, not a win, and
IPOP holds the three best single results in the campaign. For a user who will
run the search once and take the best of it, the ladder is not the wrong choice;
for a default that has to behave predictably, it is.

**It does not transfer to another fixture or another `lambda`.** One image, one
population, 56 dimensions. The mechanism — that a fixed count strands budget an
early `TolFun` trip leaves behind — is general arithmetic and should transfer;
the size of the gain is not.

## Limitations

- **The spread finding is unregistered.** It is the observation the
  recommendation leans on hardest and the one with no p-value attached. A design
  that registers a dispersion contrast would settle it; this one cannot.
- **The primary is underpowered against IPOP.** At a paired sd of 80.65,
  detecting a 20-point effect would need roughly 130 blocks. The null should be
  read as "not separated at this sample size", not as "equal".
- **One fixture, one `lambda`, one covariance mode.** Full covariance was pinned
  for a good reason, but it means the shape result is measured only where the
  clamp cannot bite.
- **The two cold arms are not independent.** They share a trajectory by design,
  which is what makes the secondary's mechanism readable and also means its
  paired sd of 11.69 is not comparable to the primary's.
- **`app.MaxOptimizerRestarts` bound the filling arm in one block.** See above.
- **The filling arm's wall-clock cost is 37% above IPOP's.** The design matched
  evaluations, not time.

## Raw data

- [`cmaes-restart-shape-measurement.csv`](cmaes-restart-shape-measurement.csv) —
  72 rows, one per job.
- [`cmaes-restart-shape-restarts.csv`](cmaes-restart-shape-restarts.csv) — 2,387
  attempt records.
- [`cmaes-restart-shape-trajectories.csv`](cmaes-restart-shape-trajectories.csv)
  — 14,519 sampled iterations.

## Reproducing it

```sh
go build -o bin/cmaes-measurement ./scripts/cmaes-measurement
bin/cmaes-measurement -design restart-shape -action plan
bin/cmaes-measurement -design restart-shape -action submit -data-root ./data
bin/cmaes-measurement -design restart-shape -action collect -data-root ./data
```

The design is deterministic in its arms, seeds and budgets; `-action plan`
prints all three before anything is submitted. Seeds 119001-119024 are reserved
for it.
