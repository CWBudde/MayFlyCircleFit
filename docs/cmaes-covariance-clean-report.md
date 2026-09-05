# The clean-rung covariance campaign

**Block covariance does not beat separable where both modes are clean, and the
interval now says by how little it could.** The registered primary contrast is
`-7.53` at `t = -0.82`, `p = 0.42`, 9 of 24 paired blocks — the sign favours
*separable* — and its 95% paired interval runs from `-26.55` to `+11.49`. That
upper end is the campaign's most useful number: it **excludes the `+39.12`**
[`cmaes-covariance-report.md`](cmaes-covariance-report.md) registered at the
clamped rung. Whatever produced that result, it is not an advantage block
carries into a rung where the comparison is arithmetically fair.

All three registered contrasts retain under Holm. The campaign is a triple null
and, for the question AGENTS.md blocks a covariance default on, that is a
positive outcome rather than a failed one: the corpus now holds two independent
clean-rung readings, `+7.27` unregistered at twelve blocks and `-7.53`
registered at twenty-four, straddling zero from opposite sides.

**What it does not settle is whether the clamp explains the `+39.12`.** The
interaction was registered precisely so that question would get a test instead
of an eyeball, and the test is inconclusive: `+15.11`, 95% interval `-33.63` to
`+63.84`. It points the predicted way and it contains both zero and `+39.12`.
See [Limitations](#limitations) — this is the campaign's real shortfall, and it
was a sizing error rather than a surprise.

## Conditions

| | |
| --- | --- |
| binary | `e466b02f428820fbf9e7717796e70ef70f29752e` — the commit the design was registered at, cross-built and shipped to the host |
| analysis | `fdaf599`, which adds the interaction and the reporting fix below; `covarianceCleanArms` is byte-identical between the two, so no arm, seed or budget differs from what ran |
| optimizer | `github.com/CWBudde/go-cma-es v0.1.0` (every arm is CMA-ES; no MayFly code runs in this design) |
| fixture | `example/MayFly-512.png`, md5 `76c44ab079154956dfadd481b08204a9`, 8 circles in one batch, 56 dimensions |
| budget | `defaultBudget` = 6,502,400 evaluations, nominal, per arm |
| backend | `cpu`, `evaluationWorkers: 8` |
| host | the 64-core campaign host at `--max-jobs 8` |
| dates | submitted 2026-09-05 02:22:42, finished 2026-09-05 05:38:10 — 03:15 of wall clock, 88,691 job-seconds (24.6 job-hours) |
| seeds | 117001-117024, twenty-four paired blocks |
| jobs | 96 of 96 completed; none failed, none cancelled |

**These costs may be compared against an otherwise matching campaign that ran at
`defaultBudget` on this fixture** — the restart ladder and the active-CMA
campaign — and that is the trade the design made deliberately. They may **not**
be compared against [`cmaes-covariance-report.md`](cmaes-covariance-report.md)
or [`cmaes-deep-hunt-report.md`](cmaes-deep-hunt-report.md), which ran at 1.94x
this cap, nor against
[`cmaes-budget-split-report.md`](cmaes-budget-split-report.md), which fits a
different image at twelve circles.

The host also runs this repository's self-hosted GitHub Actions runner, and a CI
browser matrix executed alongside the campaign for part of its first hour. That
stretched wall clock and nothing else: a cell's cost is deterministic in its
seed regardless of how many cores were free.

## The design

| arm | covariance | restarts | lambda | iters | clamped? |
| --- | --- | --- | ---: | ---: | --- |
| `sep-r32-l64` (control, baseline) | separable | 32 cold | 64 | 3175 | no |
| `blk-r32-l64` | block | 32 cold | 64 | 3175 | no |
| `sep-r2-l1024` | separable | 2 cold | 1024 | 3175 | **yes** |
| `blk-r2-l1024` | block | 2 cold | 1024 | 3175 | no |

A 2x2: covariance mode crossed with rung. Each contrast moves the covariance
mode alone; the rung is what the interaction crosses it against.

The rungs are chosen for what `go-cma-es v0.1.0` does at them and not for
realism. At lambda 64 neither mode is clamped, so the contrast measures the
covariance model. At lambda 1024 — `app`'s default `popSize`, and the rung the
covariance campaign registered its `+39.12` on — the rank-mu clamp makes
separable memoryless and leaves block intact, so the contrast measures the
covariance model confounded with a dead control. That confound is the thing
under test, which is why the campaign runs a rung it would never recommend.
`covarianceCleanArms` computes the clamp boundary from Hansen's learning rates
and **refuses to build** if a rung falls on the wrong side of it, so the
condition each arm was registered under cannot drift.

Cold restarts at a fixed lambda rather than IPOP, because an IPOP ladder doubles
its population and would walk an arm across the clamp boundary mid-run, after
which the rung would no longer name a condition.

### The family

Two single-factor contrasts and the interaction between them, corrected together
under Holm at a family-wise 0.05.

The interaction is **registered**, not read off the other two. An earlier
revision of this design left it descriptive on the ground that it is a function
of contrasts already registered. That was wrong, and it was caught in review of
[#132](https://github.com/CWBudde/MayFlyCircleFit/pull/132) before any result
had been read. Concluding "large and significant at 1024, small and null at 64,
therefore the rungs differ" is the difference-in-significance error: two tests
can fall either side of a threshold while the effects they estimate are
indistinguishable. The difference of differences has its own standard error and
answers a question neither contrast asks, so it costs the family a step and is
worth it. The registration was made before the data were read, which is what
separates it from the after-the-fact contrast change
[`cmaes-budget-split-report.md`](cmaes-budget-split-report.md) has to disclose.

## The registered result

| arm | mean | sd | median | best | gain vs `sep-r32-l64` | t (df=23) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| `sep-r32-l64` | 885.28 | 30.24 | 895.81 | 823.36 | control | control | control | control | control |
| `blk-r32-l64` | 892.81 | 29.97 | 896.23 | 826.85 | -7.53 | -0.82 | 0.42114 | retain | 9/24 |
| `sep-r2-l1024` | 915.43 | 52.91 | 924.29 | 810.73 | n/a | n/a | n/a | n/a | n/a |
| `blk-r2-l1024` | 907.85 | 88.71 | 881.14 | 789.41 | n/a | n/a | n/a | n/a | n/a |

Further registered contrasts:

| contrast | gain | t (df=23) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | --- | ---: |
| `blk-r2-l1024` vs `sep-r2-l1024` | +7.58 | +0.31 | 0.76066 | retain | 15/24 |

Registered interaction, (`blk-r2-l1024` vs `sep-r2-l1024`) - (`blk-r32-l64` vs `sep-r32-l64`):

| difference of differences | 95% interval | t (df=23) | p | Holm | blocks won |
| ---: | ---: | ---: | ---: | --- | ---: |
| +15.11 | -33.63 to +63.84 | +0.64 | 0.52772 | retain | 13/24 |

Holm step-down over three paired contrasts at a family-wise alpha of 0.05; the
uncorrected two-sided threshold at df=23 is `t = 2.07` and the Bonferroni one is
`t = 2.58`. Nothing approaches either. The `n/a` cells are honest rather than
broken: those two arms register no contrast against the baseline, and the
contrast they do register is the row underneath.

Positive gain favours block throughout.

### Per block

| block | seed | `sep-r32-l64` | `blk-r32-l64` | primary | `sep-r2-l1024` | `blk-r2-l1024` | secondary | interaction |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 117001 | 894.92 | 916.62 | -21.70 | 945.20 | 1013.09 | -67.88 | -46.18 |
| 2 | 117002 | 867.04 | 930.49 | -63.44 | 827.59 | 1115.40 | -287.81 | -224.37 |
| 3 | 117003 | 891.67 | 893.57 | -1.90 | 810.73 | 1121.44 | -310.71 | -308.81 |
| 4 | 117004 | 884.85 | 898.16 | -13.32 | 974.84 | 849.53 | +125.31 | +138.63 |
| 5 | 117005 | 894.82 | 895.26 | -0.44 | 856.67 | 882.33 | -25.66 | -25.22 |
| 6 | 117006 | 914.33 | 880.69 | +33.64 | 919.24 | 896.54 | +22.70 | -10.93 |
| 7 | 117007 | 911.80 | 899.98 | +11.82 | 967.40 | 826.39 | +141.01 | +129.19 |
| 8 | 117008 | 827.73 | 884.58 | -56.85 | 846.47 | 909.53 | -63.07 | -6.22 |
| 9 | 117009 | 902.85 | 896.72 | +6.14 | 991.20 | 1069.13 | -77.93 | -84.07 |
| 10 | 117010 | 906.99 | 895.74 | +11.24 | 891.10 | 890.60 | +0.50 | -10.74 |
| 11 | 117011 | 900.11 | 847.72 | +52.39 | 956.58 | 897.46 | +59.12 | +6.73 |
| 12 | 117012 | 823.36 | 862.30 | -38.94 | 876.47 | 910.41 | -33.94 | +5.00 |
| 13 | 117013 | 896.69 | 955.72 | -59.02 | 960.77 | 879.94 | +80.84 | +139.86 |
| 14 | 117014 | 840.78 | 887.81 | -47.03 | 845.03 | 948.73 | -103.69 | -56.66 |
| 15 | 117015 | 904.61 | 826.85 | +77.76 | 931.71 | 822.66 | +109.05 | +31.30 |
| 16 | 117016 | 874.30 | 877.91 | -3.61 | 913.45 | 957.83 | -44.37 | -40.76 |
| 17 | 117017 | 848.57 | 901.93 | -53.36 | 916.12 | 859.66 | +56.46 | +109.82 |
| 18 | 117018 | 900.46 | 924.65 | -24.19 | 932.33 | 789.41 | +142.92 | +167.10 |
| 19 | 117019 | 906.62 | 902.53 | +4.09 | 940.73 | 867.00 | +73.73 | +69.64 |
| 20 | 117020 | 909.56 | 921.24 | -11.68 | 996.76 | 862.81 | +133.95 | +145.62 |
| 21 | 117021 | 837.55 | 907.66 | -70.10 | 858.58 | 842.28 | +16.30 | +86.41 |
| 22 | 117022 | 918.25 | 852.52 | +65.73 | 966.15 | 850.66 | +115.48 | +49.75 |
| 23 | 117023 | 926.22 | 845.93 | +80.29 | 915.79 | 870.13 | +45.66 | -34.63 |
| 24 | 117024 | 862.61 | 920.87 | -58.26 | 929.34 | 855.49 | +73.85 | +132.10 |

Every block separates in both contrasts, so neither is a repeat of the
covariance campaign's void, where a clamped arm returned costs bit-identical to
its control. The primary's per-block differences span `-70.10` to `+80.29`
around a mean of `-7.53`: this is a small effect inside a large one, which is
what a paired design is for and what its interval reports.

## What the primary bounds

The useful reading of a null is its interval, and this one is narrow enough to
be worth quoting. At a paired sd of 45.04 over 24 blocks, block's advantage over
separable at a clean rung lies between `-26.55` and `+11.49` with 95%
confidence.

Three consequences follow.

**The `+39.12` is excluded at this rung.** It sits far outside the interval. So
whatever the covariance campaign measured, block does not carry an advantage of
that size into a fair comparison, and a proposal that cites `+39.12` as a
property of the covariance *model* is now contradicted by direct measurement.

**Two clean-rung readings now agree that there is nothing much here.** The
active-CMA campaign's by-product read `+7.27` (`t = 0.54`, 7/12, unregistered,
cross-campaign) at the same rung. This campaign reads `-7.53` registered at
twice the blocks. They disagree in sign and agree in magnitude — which is what
two samples of a near-zero effect look like.

**AGENTS.md's carried question is discharged.** It asks that the block-versus-
separable lead be answered "before proposing a covariance default". It is
answered, in the negative, at the only rung where the answer means anything.

## The interaction is inconclusive, and that is the campaign's shortfall

The interaction is `+15.11`, interval `-33.63` to `+63.84`, 13 of 24 blocks. It
points the predicted way — block's lead is larger where separable is dead — and
it establishes nothing, because the interval contains zero, contains `+39.12`,
and contains a large effect in the opposite direction.

The reason is visible in the per-block column: its paired sd is **115.41**,
against 45.04 for the primary. All of that extra spread comes from the lambda
1024 rung, and most of it from four blocks where `blk-r2-l1024` returned a cost
above 1000 while its control did not (blocks 1, 2, 3 and 9, the worst pair
reversing the secondary contrast by more than 300). An effect of the observed
size against that spread needs roughly ten times the blocks — around 240 — which
is far outside a campaign window.

This was a sizing error and it was foreseeable. The design's own comment
predicted that the lambda 1024 arms would be the erratic ones; it did not
propagate that into the block count, and 24 blocks were chosen for the primary.
The primary got the power it needed and the interaction did not. **A future
design that wants the interaction should not simply add blocks** — it should cut
the variance at the source, either by pairing within a rung more tightly or by
replacing the lambda 1024 arms with a rung nearer the clamp boundary, where the
clamp still binds but the search is less prone to a runaway block.

## Mechanism: `blk-r2-l1024` returns a quarter of its cap

Spend, as `finalEvaluations` against the 6,502,400 cap:

| arm | mean evaluations | share of cap | mean iterations | mean wall clock |
| --- | ---: | ---: | ---: | ---: |
| `sep-r32-l64` | 2,394,006 | 36.8% | 37,406 | 18.6 min |
| `blk-r32-l64` | 2,523,675 | 38.8% | 39,432 | 20.3 min |
| `sep-r2-l1024` | 2,343,086 | 36.0% | 2,288 | 13.8 min |
| `blk-r2-l1024` | 1,515,096 | **23.3%** | 1,480 | 8.9 min |

The design registered this campaign as cap-matched and **not** spend-matched,
and predicted the lambda 64 arms would land near the restart ladder's measured
36.7%. They did, at 36.8% and 38.8%. The prediction held.

What it did not predict is the fourth row. `blk-r2-l1024` spends a third less
than its own control, so **the secondary contrast is not spend-matched even
approximately** and must be read as a comparison of two differently sized
searches. The mechanism is a fixed restart count meeting early convergence: each
arm at this rung is allowed 3175 iterations per attempt and two attempts, and
block uses about 740 of them per attempt against separable's 1144 before
tripping `TolFun`. A fixed count cannot spend what an attempt leaves behind. The
budget-filling shape (`optimizerRestarts` negative) exists for exactly this and
was not used here, because the campaign needed a restart count that was equal
across arms by construction.

It is worth being clear about what this does *not* explain. Within
`blk-r2-l1024`, spend does not predict cost: its four worst blocks span
1.30M-1.68M evaluations and its best block spent 1.57M. The premature
convergence is systematic across the arm and the bad blocks are search variance
on top of it, not runs that stopped sooner than their neighbours.

## Diagnostics

`distributionExtent` — the identifiable `sigma * max(D)` — never exceeds
**1.1020** across 8,891 trace samples, while raw `sigma` in the same rows spans
from 3.01e-05 to 7.67e+32. This reproduces the lambda screen's finding on fresh
seeds and at a rung it did not cover, and it is the third campaign to do so.
Cite that column; **sigma alone remains gauge-dependent and must not be quoted
as evidence of a diverged search.**

The trace's `restart` column is **0 in all 8,891 rows**, which is wrong and is
not a property of the search — see the driver defects below.

## Unregistered by-products

Neither is in the Holm family, so both are leads.

**Many small restarts beat few large ones, in separable mode.** `sep-r32-l64`
beats `sep-r2-l1024` by `+30.15`, 95% interval `+11.69` to `+48.61`, `t = 3.38`,
19 of 24 blocks. It clears the uncorrected threshold comfortably. It also
confounds lambda with restart count by construction — 32 attempts at 64 against
2 at 1024, cap-matched — so it cannot say which of the two is doing the work.
The direction agrees with
[`cmaes-budget-split-report.md`](cmaes-budget-split-report.md), whose whole
finding is that splitting a budget is what wins.

**The same comparison in block mode is a null:** `+15.04`, interval `-22.63` to
`+52.72`, `t = 0.83`, 13 of 24. Read beside the spend table, the likely reason
is that `blk-r2-l1024` never spends the budget it was given, so the comparison
is between 32 full attempts and 2 truncated ones.

## The record is unchanged

The best single cost in the campaign is **789.41**, from `blk-r2-l1024` at block
18. The standing record on this fixture is 726.1984354654948, set by the deep
hunt at 1.94x this cap. Nothing here approaches it and nothing here is comparable
to it.

## What this does and does not license

**Licensed.** Dropping `covarianceMode: block` from consideration as a default
on the strength of the covariance report. That report's effect does not survive
at a rung where its control works, and the interval now bounds what could
survive.

**Not licensed.** Concluding that the rank-mu clamp explains the `+39.12`. The
interaction was registered to test exactly that and came back inconclusive. The
clamp remains the leading explanation on arithmetic grounds — it is proven that
separable at lambda 1024 runs a memoryless update with active adaptation
silently off — but this campaign does not add measured support for it.

**Not licensed.** Treating the secondary contrast's `+7.58` as a failed
replication of `+39.12`. It is a different budget, a different restart shape and
different seeds, and its arms are not spend-matched. It is not a replication
attempt and cannot refute one.

**Not licensed.** Any claim about `activeCMA`, which this design does not vary.
The registered-but-unrun `active-cma-full` design is the campaign for that
question.

## Limitations

- **The interaction is underpowered by roughly a factor of ten.** This is the
  main one; see the section above for why and for what a follow-up should change
  instead of adding blocks.
- **Cap-matched, not spend-matched**, and one arm misses by a third.
- **One fixture, one circle count, one backend.** Everything here is
  `example/MayFly-512.png` at 8 circles on CPU.
- **The clamped rung is not a recommendable configuration**, by design. No row
  in this campaign should be read as advice about running at lambda 1024.
- **No per-restart records exist for this campaign.** The driver wrote a
  header-only restart CSV, so how each individual attempt ended is not
  recoverable from the artifacts. The cause was a driver defect, fixed after the
  fact; the fix cannot retrofit records onto checkpoints already written.

## Two driver defects this campaign exposed

Both were found by running the analysis on real results, and both are fixed in
the same change as this report.

**A registered contrast between two non-baseline arms was never printed.** The
report's tables are organized around the controls — one column against the
baseline, one optional table against a secondary control — so the secondary
contrast here had nowhere to appear and its row printed `n/a`, as though the
lookup had failed. Holm was correcting the family for a contrast the reader
could not see, which is the worse half of the bug. `reportUnprintedContrasts`
now emits any registered contrast the two tables have no column for. The number
in this report was hand-computed before the fix and matches the driver's output
afterwards.

**A fixed restart count recorded nothing about its attempts.** Per-run records
come from the engine when it runs its own restart schedule; the repository's own
`WithRestarts` wrapper synthesized a record per attempt for the budget-filling
shape only, on the documented ground that a fixed count is recoverable from the
configuration. The *count* is; each attempt's best cost, work and termination is
not, and the job-level termination describes the schedule rather than the runs
inside it — every job in this campaign reports `completed`. That is precisely
what was needed to characterize `blk-r2-l1024`'s 23.3%, and it had to be
reconstructed from iteration arithmetic instead.

The second defect had a consequence nobody had noticed. The offset the trace's
restart index is shifted by *is* the number of records accumulated, so a shape
that recorded nothing also left every trace sample reporting restart 0 — which
is why the `restart` column above is uniformly zero across four arms that ran
32, 32, 2 and 2 attempts. Recording the attempts repairs the trace numbering as
a side effect.

## Raw data

- `docs/cmaes-covariance-clean-measurement.csv` — 96 rows, one per job.
- `docs/cmaes-covariance-clean-trajectories.csv` — 8,891 trace samples.
- `docs/cmaes-covariance-clean-restarts.csv` — header only; see above.

## Reproducing it

```sh
go run ./scripts/cmaes-measurement -action submit  -design covariance-clean -server http://localhost:8085
go run ./scripts/cmaes-measurement -action collect -design covariance-clean
go run ./scripts/cmaes-measurement -action analyze -design covariance-clean \
  -results docs/cmaes-covariance-clean-measurement.csv
```

The arms, seeds and cap are fixed by the design and cannot be moved by a flag.
Re-running `analyze` over the committed CSV reproduces every table above; a
fresh `submit` reproduces the costs, because each cell is deterministic in its
seed.
