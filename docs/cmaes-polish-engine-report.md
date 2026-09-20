# The polish-engine campaign

**Polishing pays, and where you put it does not.** Five arms on the
extend-width winner's ladder, one fixture, twelve paired blocks, four registered
contrasts, two of them rejecting under Holm. A polish sweep after a greedy
extend ladder is worth **`+18.09`** against not polishing at all (`t = +36.58`,
`p = 7.7e-13`, 12 of 12), and **CMA-ES is the better sweep engine by `+5.16`**
(`t = +11.76`, `p = 1.4e-07`, 12 of 12). Spreading the same sweeps across the
ladder instead of spending them at the end is worth **`+1.01`** — `t = +1.20`,
`p = 0.25`, 7 of 12, **retained**.

**The primary is a null, and an unusually tight one.** Its 95% paired interval
is `-0.84` to `+2.85`, against an operator effect of 18 points measured on the
same seeds with the same sweeps. This is a bound, not absence of power:
interleaving cannot be worth more than about three points here, where the
operator itself is worth eighteen. The same question under MayFly returns the
same verdict with the sign reversed (`-0.21`, `t = -0.21`, 8 of 12).

That matters because interleaving was the whole point of the change that made
this campaign possible. An extend **freezes its prefix** — once circle 4 is
committed, a later extend can never move it — and an interleaved sweep is the
only step the schedule format has that can revisit a committed circle. The
campaign measures that revisit and finds it buys nothing the terminal placement
does not recover in one stage.

**Nothing here licenses a `polishingOptimizer` default**, and the reason is
declared rather than discovered: matching the two engines on evaluations forced
MayFly to 128 iterations per sweep against CMA-ES's 400, so the engine contrast
is confounded with iteration count by construction. See
[Why the engine result is not a default](#why-the-engine-result-is-not-a-default).

## Conditions

| | |
| --- | --- |
| binary | `954e8d5` on `feat/polish-engine-campaign`, cross-built and shipped to the host |
| collection | the working tree that became `ce1279d`, which scores a polishing arm against its own spend |
| optimizer, ladder | `github.com/CWBudde/go-cma-es v0.1.0` in every arm — the extend ladder is CMA-ES whatever the sweep engine is |
| optimizer, sweep | `go-cma-es v0.1.0` or `github.com/cwbudde/mayfly v0.7.1`, per arm |
| fixture | `example/MayFly-512.png`, md5 `76c44ab079154956dfadd481b08204a9` |
| base | the eight-circle record 726.1984354654948, seeded through `initialCircles` and quantized to 728.382406870524 |
| budget, ladder | 6,502,400 evaluations nominal per arm for circles 9-16, identical in all five arms |
| budget, sweeps | 1,280,000 evaluations nominal per polishing arm, **additional** to the ladder cap |
| covariance | `full` in every ladder — the only mode that never clamps |
| restarts | budget-filling cold restarts, `lambda` 64, `optimizerEpochs` 1, twenty nominal attempts per stage |
| backend | `cpu`, `evaluationWorkers: 8` |
| host | the 64-core campaign host, `~/xpoleng`, `--data-root ./data-campaign`, `--max-jobs 8`, port 8092 |
| dates | submitted 2026-09-06 13:18:10Z, finished 15:24:33Z — 2h 06m 48s of wall clock |
| seeds | 130001-130012, twelve paired blocks, searched by no earlier campaign |
| jobs | 60 of 60 campaigns completed, 756 stages, none failed, none cancelled |

**A sixteen-circle cost is comparable to almost nothing else in `docs/`.** The
one exception is deliberate and is used below: `pol-none` is the extend-width
campaign's `ext-w1` arm, unchanged, at the same cap on the same base — so the
two campaigns' unpolished rows may be compared, and are. Everything else here is
internally comparable across the five arms and with nothing outside this report.

## The design

Every arm runs the same ladder and differs only in what follows an extend.

| arm | polish placement | sweep engine | polish stages | sweeps/stage | nominal sweeps | iters/sweep |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| `pol-none` (control) | none | — | 0 | — | 0 | — |
| `pol-term-cma` (baseline) | terminal | `cmaes` | 1 | 32 | 32 | 400 |
| `pol-int-cma` | interleaved | `cmaes` | 8 | 4 | 32 | 400 |
| `pol-term-may` | terminal | `mayfly` | 1 | 32 | 32 | 128 |
| `pol-int-may` | interleaved | `mayfly` | 8 | 4 | 32 | 128 |

The ladder is `ext-w1` verbatim: eight extend stages of one circle each, full
covariance at `lambda` 64, budget-filling cold restarts, twenty nominal attempts
per stage, 812,800 evaluations per stage. It won the extend-width campaign by
`+39.65` over `ext-w8`, so it is the ladder a default would ship and the only
one worth asking the polish question about.

The sweep is held at the settings of the pilot that measured the operator worth
measuring: `polishingSigma` 0.02 (`app.DefaultPolishingSigma`), strategy
`hybrid-overlap`, `activeSetSize` 8, `polishingEpochs` 2, `popSize` 50,
`stopMinImprovement` 1e-4, and a stagnation window pinned to each arm's own
iteration count — the widest `app.JobConfig` accepts, and the nearest thing to
leaving the criterion off. Pinning it to a constant instead would have armed it
on the MayFly arms and not on the CMA-ES ones.

**The two placements are matched on nominal sweeps, and that works only because
a sweep's spend does not depend on the circle count.** A sweep is *one*
optimizer run over exactly `activeSetSize` circles, not one run per group of
them, so an interleaved stage polishing nine circles and a terminal stage
polishing sixteen both optimize 8 x 7 = 56 dimensions and both freeze the rest.
That was measured in pre-flight, not assumed: an eight-stage interleaved probe
spent exactly 40,003 evaluations at every stage from nine circles to sixteen.

## What was matched, by measurement

| arm | mean | sd | best | ladder evals | sweep evals | final evals | iterations |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `pol-none` | 575.10 | 1.28 | 574.00 | 6,220,092 | 0 | 6,220,092 | 97,189 |
| `pol-term-cma` | 557.01 | 2.23 | 554.09 | 6,220,157 | 1,280,000 | 7,500,157 | 122,789 |
| `pol-int-cma` | **556.01** | 1.38 | **553.71** | — | — | 7,493,850 | 122,691 |
| `pol-term-may` | 562.18 | 1.65 | 559.37 | 6,220,157 | 1,284,352 | 7,504,509 | 105,381 |
| `pol-int-may` | 562.39 | 3.30 | 558.93 | — | — | 7,501,946 | 105,341 |

Means over twelve blocks. Ladder evaluations for the terminal arms are the
final count less the nominal sweep budget, which is exact there because their
ladders are the control's search unchanged. **For the interleaved arms the split
is not recoverable** and the cells are left empty: a continuation's evaluation
counter is cumulative, a polish stage counts on from the extend before it, and
an interleaved arm's ladder is not the control's — its extends start from
polished vectors and run a different number of cold attempts.

Three readings are worth stating because they were designed and could have
failed:

- **The four polishing arms spent within 0.14% of each other** (7,493,850 to
  7,504,509). The engine contrast is evaluation-matched in fact and not only in
  intent; MayFly overshoots its nominal sweep budget by 0.34% and nothing else
  moves.
- **The ladders agree to 65 evaluations** — 6,220,157 against the control's
  6,220,092 — and, for the two terminal arms and the control, the per-restart
  records are **bit-identical**. Those three arms ran literally the same search
  for circles 9-16; only what follows it differs. That makes
  `pol-term-cma` against `pol-term-may` a clean engine contrast on an identical
  sixteen-circle input, and `pol-term-cma` against `pol-none` a clean
  measurement of the sweep alone.
- **The designed iteration asymmetry came out exact.** Polish iterations are the
  terminal arms' total less the control's: 122,789 - 97,189 = **25,600** for
  CMA-ES, 105,381 - 97,189 = **8,192** for MayFly, a ratio of 3.125 against the
  25/8 the design predicted from MayFly's per-iteration evaluation cost. No
  sweep stopped early — both figures are exactly `32 x epochs x iters`.

The ladder itself spends 95.7% of its 6,502,400 cap, which is the filling
shape's measured behaviour at this width, not a defect.

## The four registered contrasts

Holm step-down over all four at a family-wise α = 0.05. The uncorrected
two-sided threshold at df = 11 is `t = 2.20`.

| contrast | question | gain | t | p | 95% CI | blocks | verdict |
| --- | --- | ---: | ---: | ---: | --- | ---: | --- |
| `pol-int-cma` vs `pol-term-cma` | **primary** — placement, CMA-ES | +1.01 | +1.20 | 0.25454 | `-0.84` to `+2.85` | 7/12 | **retain** |
| `pol-int-may` vs `pol-term-may` | placement, MayFly | -0.21 | -0.21 | 0.83997 | `-2.47` to `+2.05` | 8/12 | **retain** |
| `pol-term-cma` vs `pol-term-may` | engine at equal placement | +5.16 | +11.76 | 1.4e-07 | `+4.20` to `+6.13` | 12/12 | **reject** |
| `pol-term-cma` vs `pol-none` | does the operator pay at all | +18.09 | +36.58 | 7.7e-13 | `+17.00` to `+19.18` | 12/12 | **reject** |

A positive gain favours the first-named arm. Holm rejects the two smallest
p-values at α/4 and α/3 and stops at the primary's 0.25454, which is nowhere
near α/2.

**The fourth contrast is deliberately not evaluation-matched**, and the report
does not dress it up as one. A sweep's budget is additional to the ladder's cap,
because a sweep and a restart ladder do not share a budget in any way the
schedule format can express, and carving the ladder down to pay for the sweep
would have answered a different question. Read it as cost-benefit against the
spend columns above: 18.09 points for 1,280,000 evaluations, a 20.6% increase
over the unpolished arm's spend.

## The primary: placement is a bounded null

Per-block costs, and the paired difference the primary tests:

| block | `pol-none` | `pol-term-cma` | `pol-int-cma` | int - term |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 576.51 | 560.01 | 555.77 | +4.24 |
| 2 | 576.66 | 554.09 | 556.18 | -2.09 |
| 3 | 574.02 | 555.36 | 557.93 | -2.57 |
| 4 | 574.00 | 555.37 | 555.11 | +0.26 |
| 5 | 576.52 | 558.92 | 553.71 | +5.20 |
| 6 | 574.12 | 555.16 | 556.42 | -1.26 |
| 7 | 574.12 | 557.00 | 557.99 | -0.99 |
| 8 | 574.02 | 555.43 | 555.32 | +0.11 |
| 9 | 576.54 | 560.43 | 556.14 | +4.29 |
| 10 | 574.13 | 556.66 | 557.89 | -1.23 |
| 11 | 576.51 | 560.01 | 555.03 | +4.98 |
| 12 | 574.10 | 555.72 | 554.60 | +1.12 |

Seven blocks favour interleaving, five favour the terminal stage, and the
largest swing in either direction is 5.20 against 2.57. The paired standard
deviation is 2.90 on a mean of 1.01.

**This is a bound rather than a shrug.** At this spread, the interval excludes
anything above 2.85 points, which is 16% of what the operator itself buys. A
campaign that wanted to resolve the remaining 1.01 would need about 65 blocks at
80% power — but the useful statement is not that the effect is undetectable, it is
that it is **small enough not to matter next to the decision of whether to
polish at all**.

`pol-int-cma` does carry the better spread (1.38 against 2.23) and the better
single result (553.71 against 554.09), and it wins the three blocks where
`pol-term-cma` does worst. No dispersion contrast was registered, so that is a
lead and not a finding.

## Mechanism: the sweep is a level shift, not a compounding advantage

The trajectories say exactly where the primary's null comes from. Mean cost at
the end of each extend stage, across twelve blocks:

| circles | unpolished ladder | `pol-int-cma` | interleaved lead |
| ---: | ---: | ---: | ---: |
| 9 | 703.18 | 703.18 | 0.00 |
| 10 | 678.99 | 669.76 | 9.23 |
| 11 | 657.86 | 640.38 | 17.48 |
| 12 | 638.44 | 621.03 | 17.41 |
| 13 | 620.97 | 601.80 | 19.17 |
| 14 | 604.30 | 585.67 | 18.63 |
| 15 | 588.39 | 570.34 | 18.05 |
| 16 | 575.10 | 557.71 | 17.39 |

The interleaved arm's lead is built by the first two or three sweeps and then
**stops growing**. From eleven circles to sixteen — five further extends, each
one starting from a polished prefix rather than an unpolished one — the lead
moves from 17.48 to 17.39. An extend on a polished prefix is no better than an
extend on an unpolished one.

So the revisit that interleaving enables is real, and its benefit does not
compound through the ladder. It is a level shift the terminal arm can pay for
in one stage, and does: from 575.10 the terminal arm's 32 sweeps reach 557.01,
recovering the whole 17.39 and 0.70 beyond it, while the interleaved arm's final
four sweeps take 557.71 to 556.01 and buy only 1.70 because its vector has been
polished seven times already.

The same picture under MayFly, whose sweeps are weaker, has the same shape with
smaller numbers: `pol-int-may` reaches 565.94 at sixteen circles before its
final sweep and finishes at 562.39, and its terminal counterpart finishes at
562.18.

**This is the finding the campaign was built to get, and it is a negative one.**
The only step in the schedule format that can move a committed circle does move
it, measurably — and a greedy ladder does not exploit the improvement to place
its later circles any better.

## Why the engine result is not a default

CMA-ES beats MayFly on this stage by `+5.16` at terminal placement (12/12) and,
unregistered, by `+6.38` at interleaved placement (`t = +5.17`, `p = 3.1e-04`,
12/12). Both directions agree, the terminal contrast runs on a bit-identical
sixteen-circle input, and the arms spent within 0.14% of each other. It is a
clean result for *this sweep*.

It is not a licence to change `polishingOptimizer`'s default, for four reasons,
the first of which is structural:

- **The engine is confounded with iteration count by construction.** MayFly
  spends 25/8 of CMA-ES's evaluations per iteration, so matching the arms on
  evaluations forced MayFly to 128 iterations per sweep against CMA-ES's 400 —
  8,192 total iterations against 25,600. Matching on iterations instead would
  have compared a search against one three times its size. Both matchings answer
  a question; neither answers "which engine is better", and this campaign chose
  the one this repository uses everywhere else. The asymmetry is a declared cost
  of the design, not a discovery.
- **σ means different things to the two engines.** The design pins
  `polishingSigma` at 0.02 for both, because it is the one quantity in a sweep
  that both adapters read. For MayFly it is the seed perturbation the
  continuation profile has always used; for CMA-ES it is the initial step size.
  A value that is right for one need not be right for the other, and no σ sweep
  was run for either.
- **One fixture, one sweep configuration, one active-set strategy.** Every
  number here is `hybrid-overlap` at `activeSetSize` 8 on
  `example/MayFly-512.png`.
- **The default is not currently a measured choice either.** MayFly is the
  default because every recorded polishing figure ran it, not because anything
  ranked the two. This campaign is the first ranking that exists; it is one
  campaign, and the registration said in advance that it would not license a
  default whatever it returned.

The honest summary is that **CMA-ES is the better sweep at equal evaluations and
unequal iterations**, and that an engine default needs a campaign that varies
the matching.

## The operator contrast, read as cost-benefit

| arm | mean | gain over `pol-none` | extra evaluations | points per million |
| --- | ---: | ---: | ---: | ---: |
| `pol-term-cma` | 557.01 | +18.09 | 1,280,065 | 14.1 |
| `pol-int-cma` | 556.01 | +19.10 | 1,273,758 | 15.0 |
| `pol-term-may` | 562.18 | +12.93 | 1,284,417 | 10.1 |
| `pol-int-may` | 562.39 | +12.72 | 1,281,854 | 9.9 |

All four are 12 of 12 against the control. The three unregistered rows carry no
correction and are read alongside the registered one, which rejects.

For scale: the extend-width campaign measured the entire choice of ladder
shape — eight extends of one against one extend of eight — at `+39.65` on the
same fixture and the same cap. A polish sweep costing 20.6% more evaluations is
worth about **46% of that**, which makes it the largest single improvement
available to a staged schedule short of changing the ladder itself.

## What the campaign does not establish

- **It sets no record.** The campaign best is `pol-int-cma` at 553.71, which
  beats the extend-width campaign's recorded sixteen-circle figure of
  559.5857671101888 but not the 546.1567522684733 that
  [`growth-run-report.md`](growth-run-report.md) records from the polishing
  pilot at a larger budget. A campaign arm at a pinned cap is not a record
  attempt.
- **It does not vary the sweep budget.** 32 nominal sweeps is one point. Whether
  the operator's 18 points saturate, and where, is unmeasured — and the
  interleaved arm's final four sweeps buying 1.70 where the terminal arm's 32
  buy 18.09 is a hint that they saturate early.
- **It does not vary the active set.** `activeSetSize` 8 out of 9-16 circles
  means an interleaved sweep at nine circles is polishing nearly everything and
  one at sixteen circles is polishing half. That gradient runs along the
  campaign's own variable and is not separated from it.
- **It does not measure restarts on a sweep**, which remain inexpressible: the
  polisher runs under `WithEpochs` alone, so a restart count written on a polish
  step would be inert and is refused rather than accepted.
- **It says nothing about deeper schedules.** Sixteen circles is where it stops.
  [`growth-run-report.md`](growth-run-report.md) applies this campaign's winning
  recipe to 233 circles, but as a single uncontrolled run.

## By-products

**The `elapsedSeconds` column measures contention, not work — in this campaign
and in every staged one before it.** `pol-none` and the extend-width campaign's
`ext-w1` are the same ladder at the same cap on the same base, and they agree to
0.04% on evaluations (6,220,092 against 6,222,764) and to 0.21 points on mean
cost. Their summed stage time differs by **2.83x**: 4,092 seconds here against
11,580 there. This campaign's sixty campaigns sum to 355,037 seconds of stage
time against 7,608 seconds of wall clock — an effective parallelism of **46.7**
on a host configured for eight concurrent jobs — so a stage's measured duration
is dominated by how many other stages were resident while it ran. Within this campaign the column tracks stage count almost
exactly — 511, 610 and 457 seconds per stage for the eight-, nine- and
sixteen-stage arms — which is what a queue-dominated measurement looks like.
**Do not rank arms by it**, here or in `cmaes-extend-width-report.md`, whose
41% wall-clock caveat is subject to the same contamination. The campaign's real
wall clock is 2h 06m 48s for all sixty campaigns together.

**The extend-width winner replicates on fresh seeds.** `ext-w1` returned a mean
of 575.31 on seeds 120001-120012; the identical configuration returns **575.10**
here on seeds 130001-130012, a difference of 0.21 against a within-arm standard
deviation of 1.28. That is the first cross-campaign replication of a staged
result in this repository, and it is what licenses reading `pol-none` as the
extend-width arm rather than as a new control.

**The filling shape runs four times its nominal attempt count, and the
extend-width report's withdrawal holds at a larger number.** Stages here ran
**73 to 91 cold attempts** against twenty nominal, at a mean of 79.0 for the
control's ladder and 81.9 for `pol-int-cma`'s, with `app.MaxOptimizerRestarts`
unchanged at 64 — so the constant bounds the requested restart magnitude and not
the attempts a filling schedule executes, exactly as
[`cmaes-extend-width-report.md`](cmaes-extend-width-report.md) established at 88.
The mechanism is visible in the records: an attempt runs a mean of 152
iterations against the 635 its stage nominally allows, because each cold attempt
trips `TolFun` early and the schedule refills the remainder. All 38,349 attempts
terminated on `convergence`; none was truncated by the cap.

**`distributionExtent` stays bounded on fresh seeds.** Over 138,302 trajectory
samples the identifiable `sigma * max(D)` never exceeds **0.9088**, while sigma
itself reaches 100.6. That reproduces the lambda screen's finding for a fourth
time and on a staged schedule, and it is the column to cite; sigma alone is
gauge-dependent and says nothing about divergence.

**The interleaved arms' extend ladders diverge from the control's at the second
stage**, as they must — a polish changes the incumbent the next extend starts
from. **All five arms' first extend stage is bit-identical** at the per-attempt
record level, which is the check that the divergence is caused by the sweep and
by nothing else in the arm. Their trajectory *rows* for that stage are not
identical, and that is bucketing rather than search.

**The committed trajectory file is sampled at a different density per arm, and
that is a defect in the collector rather than a property of the runs.** A
stage's rows are bucketed over a nominal share of the cap, and the share was
taken as the campaign total divided by the number of scoring stages -- which
counts polish stages, although a polish stage writes no trajectory at all. So
the same eight-extend ladder was divided by eight in `pol-none`, by nine in the
terminal arms and by sixteen in the interleaved ones, and the first extend
stage kept 2,947, 2,770 and about 4,905 rows respectively. **No recorded value
is affected** -- every row is an exact reading off the trace, and the
`distributionExtent` bound below is a maximum over samples, which a denser
sample can only sharpen -- but the row *density* is not comparable across arms,
so do not read one arm's trajectory against another's by row count or by any
statistic weighted by it. `stageShares` now funds the two kinds separately, the
ladder cap across the extends and the sweep allowance across the polishes, so a
future campaign divides each stage by the budget it actually ran within. The
committed file predates that fix and was not regenerated: the collector has
changed in other ways since it was written, so a re-collection today could not
be verified against it, and a file mixing this fix with those changes would have
weaker provenance than the one the campaign actually produced.

## Artifacts

- [`cmaes-polish-engine-measurement.csv`](cmaes-polish-engine-measurement.csv) —
  60 rows, one per campaign: arm, block, seed, job id, cost, scored and final
  evaluations, iterations, summed stage seconds, backend.
- [`cmaes-polish-engine-trajectories.csv`](cmaes-polish-engine-trajectories.csv)
  — 138,302 rows of per-stage trajectory, bucketed over a nominal share of the
  cap, carrying `populationSpread`, `sigma`, `conditionNumber` and
  `distributionExtent`. Extend stages only; a polish stage writes no trajectory.
  The share diluted that nominal by the polish stages, so the density differs
  per arm -- see the bucketing note above before comparing arms row for row.
- [`cmaes-polish-engine-restarts.csv`](cmaes-polish-engine-restarts.csv) —
  38,349 per-attempt records across 480 stages, renumbered onto each campaign's
  stage ordinal.

Reproduce the analysis with:

```sh
go run ./scripts/cmaes-measurement -action analyze -design polish-engine
```
