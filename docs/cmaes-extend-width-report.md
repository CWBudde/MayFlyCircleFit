# The extend-width campaign

**Add circles a few at a time, not all at once.** Five arms at a shared
evaluation cap, one fixture, twelve paired blocks, and all four registered
contrasts reject under Holm. Starting from the standing eight-circle record and
spending one budget on circles 9-16, **eight extends of one circle beat a single
extend of eight by `+39.65`** — `t = +14.94`, `p = 1.2e-08`, 12 of 12 blocks.
Four extends of two are worth `+40.88` (`t = +10.49`) and two extends of four
`+23.96` (`t = +7.49`), both 12 of 12.

This is the largest effect this project has measured. The restart-shape primary
that earned a default was `+6.89` at `t = +2.89`.

**The premise holds, and the control is emphatic.** Building on the record beats
fitting sixteen circles cold by `+128.81` (`t = +20.08`, 12 of 12). The sharper
way to read that number: the cold arm's mean of **743.77 is worse than the best
eight-circle fit ever recorded here** (726.20). At 112 dimensions the joint
search does not merely do less well than a staged one — it fails to reach a cost
that eight well-placed circles already achieve on their own.

**What the campaign does not establish is that narrowest is best.** `ext-w2` and
`ext-w1` are indistinguishable: `+1.24` at `t = +0.72`, 6 blocks each, and that
contrast was not registered. `ext-w2` has the better mean (574.08 against
575.31) and much the better single result (559.59 against 574.00); `ext-w1` has
a standard deviation of 1.66 against 6.02. The measured answer is **narrower
than eight**, with the trend saturating somewhere at or below width two.

## Conditions

| | |
| --- | --- |
| binary | `d421e92` on `feat/extend-width-campaign`, cross-built and shipped to the host |
| analysis | the same binary, run on the host by the campaign supervisor |
| optimizer | `github.com/CWBudde/go-cma-es v0.1.0` — every arm is CMA-ES; no MayFly code runs in this design |
| fixture | `example/MayFly-512.png`, md5 `76c44ab079154956dfadd481b08204a9` |
| base | the standing 8-circle record, seeded through `initialCircles` |
| budget | `defaultBudget` = 6,502,400 evaluations, nominal, per arm, for circles 9-16 |
| covariance | `full` in every arm — the only mode that never clamps |
| restarts | budget-filling cold restarts, `lambda` 64, `optimizerEpochs` 1 |
| backend | `cpu`, `evaluationWorkers: 8` |
| host | the 64-core campaign host at `--max-jobs 8`, port 8087 |
| dates | submitted 2026-09-05 22:32:21, finished 2026-09-06 02:52:40 — 04:20 of wall clock, 598,697 job-seconds (166.3 job-hours) |
| seeds | 120001-120012, twelve paired blocks |
| jobs | 60 of 60 campaigns completed; 252 jobs, none failed, none cancelled |

**A sixteen-circle cost is comparable to nothing else in `docs/`.** Every other
CMA-ES campaign fits eight circles (or twelve, in the budget-split report). The
numbers here are internally comparable across the five arms and with nothing
outside this report.

## The base, and what it costs to seed it

`initialCircles` names colours as 8-bit hex, so the seed is not bit-exactly the
record. The record's 726.1984354654948 becomes **728.382406870524** when
round-tripped through the schedule document — a constant offset of **2.184**,
in colour only; `x`, `y`, `r` and `opacity` pass through as float64.

This was verified on live campaign data rather than only in the pre-flight: a
base stage reported `bestCost` 728.382406870524 at 23 evaluations, bit-identical
to the quantized value. **Every arm therefore starts from the same prefix**, and
the offset is a constant of the campaign rather than a per-arm nuisance. The
base's ~23 evaluations are excluded from the spend column below.

`cold-w16` has no base and no seed; it is a plain from-scratch 16-circle batch
job at 112 dimensions.

## The design

| arm | stages | `additionalCircles` | dims/stage | attempts/stage | stage cap | total cap |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `ext-w8` (baseline) | 1 | 8 | 56 | 20 | 6,502,400 | 6,502,400 |
| `ext-w4` | 2 | 4 | 28 | 20 | 3,251,200 | 6,502,400 |
| `ext-w2` | 4 | 2 | 14 | 20 | 1,625,600 | 6,502,400 |
| `ext-w1` | 8 | 1 | 7 | 20 | 812,800 | 6,502,400 |
| `cold-w16` (control) | — | — | 112 | 32 | 6,502,400 | 6,502,400 |

An extend **freezes its prefix**: circles fitted in an earlier stage are not
revisited. Polishing is the only mechanism that would revisit them, and
`polishingEnabled` is on `JobConfig.mayflyOnlyFields()`, so it is refused for
CMA-ES. Nothing here rearranges an earlier circle.

**The design pins the attempt *count* per stage, not the attempt length.** The
first version pinned length at 3,175 iterations, matching the winning
`full-fill-l64` arm, so that grouping width would not be confounded with attempt
length. The pre-flight probe killed it: at a fixed length the narrower arms
filled 76.5%, 87.8% and 94.7% of their caps at 7, 14 and 28 dimensions, because
a low-dimensional attempt trips `TolFun` long before its iteration allowance is
gone. That is a spend gradient running along exactly the variable under test.
Pinning twenty nominal attempts per stage — lengths 5080/2540/1270/635 —
replaced it with 96.2 / 95.8 / 95.9%.

### The family

Registered in `campaignDesign` and printed by `-action plan` before submit;
recorded in `PLAN.md` in the same commit. Holm step-down, family-wise
α = 0.05, family of four.

- **PRIMARY** — `ext-w1` against `ext-w8`.
- `ext-w2` against `ext-w8`.
- `ext-w4` against `ext-w8`.
- **Secondary** — `ext-w8` against `cold-w16`.

## The registered result

| arm | mean | sd | median | best | worst |
| --- | ---: | ---: | ---: | ---: | ---: |
| `ext-w8` | 614.96 | 8.76 | 617.16 | 601.85 | 627.00 |
| `ext-w4` | 591.00 | 5.51 | 591.99 | 581.83 | 598.38 |
| `ext-w2` | **574.08** | 6.02 | 576.70 | **559.59** | 581.67 |
| `ext-w1` | 575.31 | **1.66** | 574.13 | 574.00 | 579.00 |
| `cold-w16` | 743.77 | 24.81 | 748.08 | 680.84 | 768.46 |

| contrast | mean | paired sd | t (df 11) | p | 95% interval | blocks | Holm |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| **`ext-w1` − `ext-w8`** | **+39.65** | 9.20 | +14.94 | 1.2e-08 | +33.81 to +45.49 | 12/12 | **reject** |
| `ext-w2` − `ext-w8` | +40.88 | 13.50 | +10.49 | 4.6e-07 | +32.31 to +49.46 | 12/12 | **reject** |
| `ext-w4` − `ext-w8` | +23.96 | 11.08 | +7.49 | 1.2e-05 | +16.92 to +31.00 | 12/12 | **reject** |
| **`ext-w8` − `cold-w16`** | **+128.81** | 22.22 | +20.08 | 5.1e-10 | +114.69 to +142.92 | 12/12 | **reject** |

Positive means the first arm reaches the lower cost. Every contrast clears its
Holm threshold by four orders of magnitude or more; the uncorrected two-sided
threshold at df 11 is `t = 2.20`.

### Per block

| block | `ext-w8` | `ext-w4` | `ext-w2` | `ext-w1` | `cold-w16` | best |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 622.27 | 593.73 | 569.66 | 576.51 | 730.50 | `ext-w2` |
| 2 | 602.62 | 587.19 | 577.36 | 574.00 | 680.84 | `ext-w1` |
| 3 | 622.13 | 582.48 | 571.32 | 574.11 | 766.13 | `ext-w2` |
| 4 | 609.64 | 598.38 | 576.88 | 574.11 | 768.46 | `ext-w1` |
| 5 | 601.85 | 591.08 | 581.67 | 576.62 | 727.52 | `ext-w1` |
| 6 | 627.00 | 592.30 | 576.91 | 574.10 | 760.12 | `ext-w1` |
| 7 | 617.49 | 585.82 | 576.52 | 576.52 | 762.84 | `ext-w1` |
| 8 | 605.10 | 591.68 | 580.37 | 574.09 | 752.27 | `ext-w1` |
| 9 | 623.01 | 581.83 | **559.59** | 574.02 | 743.88 | `ext-w2` |
| 10 | 621.62 | 596.04 | 572.07 | 574.14 | 733.43 | `ext-w2` |
| 11 | 616.84 | 596.78 | 569.70 | 576.51 | 763.38 | `ext-w2` |
| 12 | 609.95 | 594.66 | 576.87 | 579.00 | 735.83 | `ext-w2` |

`ext-w8` and `cold-w16` never win a block. `ext-w1` and `ext-w2` win six each.

## The spend gate

The plan made this a hard condition, because
[`cmaes-restart-ladder-report.md`](cmaes-restart-ladder-report.md) and
[`cmaes-covariance-clean-report.md`](cmaes-covariance-clean-report.md) each
carry a contrast weakened by comparing two differently sized searches.

| arm | evaluations spent | % of cap | iterations | mean wall clock |
| --- | ---: | ---: | ---: | ---: |
| `ext-w8` | 6,233,972 | 95.9% | 97,406 | 8,316 s |
| `ext-w4` | 6,226,038 | 95.7% | 97,282 | 10,664 s |
| `ext-w2` | 6,218,996 | 95.6% | 97,172 | 11,700 s |
| `ext-w1` | 6,222,764 | 95.7% | 97,231 | 11,580 s |
| `cold-w16` | 6,284,846 | 96.7% | 98,201 | 7,632 s |

**A 1.1-point spread across arms whose per-stage caps differ eightfold, and
whose dimensions differ sixteenfold.** `cold-w16` — the one arm at a dimension
the probe never covered — came in at the top of the range rather than out of
it. **No spend caveat is needed for any contrast in this report.**

Wall clock is a different matter and is reported as a by-product below.

## Mechanism: each stage keeps buying

The per-stage stage-best means, averaged over the twelve blocks, starting from
the quantized base of 728.38:

| arm | after stage 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ext-w8` | 614.96 | | | | | | | |
| `ext-w4` | 647.19 | 591.00 | | | | | | |
| `ext-w2` | 677.71 | 634.12 | 599.03 | 574.08 | | | | |
| `ext-w1` | 703.31 | 680.15 | 657.86 | 638.44 | 620.97 | 605.83 | 589.14 | 575.31 |

`ext-w1`'s per-circle gains are 25.1, 23.2, 22.3, 19.4, 17.5, 15.1, 16.7, 13.8.
They decay, as a greedy schedule's should, but **the sixteenth circle still buys
13.8 points** — this fixture is nowhere near saturated at sixteen circles, and
the marginal value of another circle is still an order of magnitude above the
differences between the narrow arms.

The wider arms are behind at every point where they can be compared. After
half its budget, `ext-w4` sits at 647.19; `ext-w2` reaches 634.12 at the same
half-budget mark with the same evaluations spent, and `ext-w1` reaches 638.44
after four of its eight stages. **The gap does not come from a late collapse in
the wide arms; they are already losing halfway through.**

### Attempts per stage, and the restart ceiling

| arm | attempts per stage (mean, min-max) | terminations |
| --- | ---: | --- |
| `ext-w8` | 62.5 (57-66) | 747 convergence, 3 budget |
| `ext-w4` | 70.4 / 67.5 (62-72) | 1,655 convergence |
| `ext-w2` | 74.1 / 72.8 / 69.8 / 70.6 (67-75) | 3,447 convergence |
| `ext-w1` | 77 to 86 (72-88) | 7,589 convergence |
| `cold-w16` | 30.6 (29-33) | 364 convergence, 3 budget |

Every arm runs many more attempts than its nominal twenty, because a
budget-filling schedule keeps restarting while budget remains and almost every
attempt trips `TolFun` early. The pattern is monotone in dimension: 30.6
attempts at 112 dimensions, 62.5 at 56, and up to 88 at 7.

**This refutes a claim in
[`cmaes-restart-shape-report.md`](cmaes-restart-shape-report.md).** That report
states that `app.MaxOptimizerRestarts = 64` bound one block of twenty-four, and
warns that a filling shape at a smaller `lambda` would hit the ceiling
routinely. It does not. A pre-flight probe ran **69 attempts from a request of
16**, and this campaign ran up to **88 attempts per stage** with the constant
unchanged at 64. The constant bounds the requested restart *magnitude*, not the
number of attempts a filling schedule executes. No arm here was ceiling-bound,
and the earlier report's limitation bullet should be read as withdrawn.

### Where the block best comes from

The share of stages whose best came from an attempt at index 32 or higher — an
attempt a fixed count of 32 would never have run:

| arm | stages | best from index ≥ 32 |
| --- | ---: | ---: |
| `ext-w8` | 12 | 4 (33%) |
| `ext-w4` | 24 | 13 (54%) |
| `ext-w2` | 48 | 30 (62%) |
| `ext-w1` | 96 | 40 (42%) |
| `cold-w16` | 12 | 0 (0%) |

`cold-w16` never reaches index 32 at all, because 112-dimensional attempts are
long enough that thirty of them exhaust the cap. This is an independent
confirmation of the restart-shape report's finding that a fixed count of 32
strands budget, now measured at four more dimensionalities.

## `ext-w1` is nearly deterministic, and that is worth naming

Its twelve costs, sorted:

```
574.00  574.02  574.09  574.10  574.11  574.11  574.14
576.51  576.51  576.52  576.62  579.00
```

Seven values inside 0.14 of each other, across twelve different seeds, and the
whole arm inside a 5.0-point range. These are distinct values, so this is **not**
the shared-trajectory artifact that made the restart-shape secondary one-sided;
twelve independent searches genuinely land in the same place.

The natural reading is that one circle at a time is close to a deterministic
greedy procedure on this fixture: with seven dimensions and eighty restarts per
stage, each stage finds something very near the true best single circle over the
current residual, and the seed barely perturbs it. That is a real property and a
desirable one for a default — but it is the *reason* `ext-w1` wins the
registered primary on its t-statistic rather than on its mean, and it cuts both
ways. `ext-w2`'s larger spread is what reaches 559.59, fifteen points better
than anything `ext-w1` produced.

**Choosing between width one and width two is a risk preference, not a mean
difference, and no dispersion contrast was registered.** For a single run whose
result must be predictable, `ext-w1`. For the best of several, `ext-w2`.

## The cold control, and what it says about dimension

`cold-w16` is the arm that could have invalidated the premise, and it does the
opposite in the strongest terms available:

- It loses 12 of 12 blocks to every extend arm, by `+128.81` against the worst
  of them and `+169.69` against the best.
- Its mean, 743.77, is **worse than the standing eight-circle record of
  726.20** — half the circles, a comparable cap.
- Its best single block, 680.84, is worse than the *worst* block of every
  extend arm.
- It spends the most of any arm (96.7%) and the least wall clock (7,632 s), so
  it is neither starved nor stalled. It simply searches badly at 112 dimensions.

This is consistent with, and sharper than, the collapse dynamics
[`docs/restart-vs-budget-report.md`](restart-vs-budget-report.md) describes. It
should not be read as a general claim about CMA-ES at 112 dimensions on other
problems, and it is not evidence about the *right* prefix — see the limitation
below.

## Diagnostics

`distributionExtent` never exceeds **0.8225** across 15,131 samples, while sigma
spans 1.842e-07 to 1.032e+02. This reproduces on fresh seeds, in full covariance
mode and at four new dimensionalities the finding
[`cmaes-lambda-report.md`](cmaes-lambda-report.md) established: the identifiable
`sigma * max(D)` stays bounded near 1 while sigma alone spans orders of
magnitude. **Cite the extent column; never cite sigma.** Every sample carried a
value.

The restarts CSV carries 13,808 attempt records — six times the previous
largest — all of them from cold arms, readable because of the `attemptRuns` fix
that landed for the restart-shape campaign.

## The sixteen-circle record

There was none before this campaign. It is now
**559.5857671101888**, from `ext-w2` block 9, seed 120009, job
`20ec4e8f-ce24-49df-bbfb-2741d271479f`. The solution is in that job's
checkpoint on the campaign host; it is not yet carried in
`scripts/cmaes-measurement/main.go`.

The eight-circle record is unchanged at **726.1984354654948**
([`cmaes-deep-hunt-report.md`](cmaes-deep-hunt-report.md)). The driver's
`recordCircles()` and `recordCost` were updated to it for this campaign, so the
`vs record` line is correct for the first time; the restart-shape report's note
that it reported against the superseded 752.52 no longer applies.

## Unregistered by-products

**Width two beats width four**, `+16.92`, `t = +8.82`, 12/12 — so the trend is
monotone from eight down to two and only then flattens. **Width one beats width
four** by `+15.68`, `t = +10.24`, 12/12.

**Width two against width one is a null**: `+1.24`, `t = +0.72`, `p = 0.49`,
6/12, interval −2.56 to +5.03. The interval is narrow, so this is closer to a
measured equivalence than most nulls in this corpus — but it is unregistered and
it says nothing about the tails, where the arms visibly differ.

**Wall clock is unmatched in the variable under test.** `ext-w8` takes 8,316 s
against `ext-w2`'s 11,700 s for the same evaluation cap — 41% more for the
winning shape. The cause is the same one the restart-shape report identified:
narrow low-dimensional attempts run far more iterations per evaluation, and
per-iteration overhead dominates. A staged schedule buys its quality with real
time as well as evaluations, and the cap does not show it. `cold-w16` is the
cheapest arm in wall clock and by far the worst in cost.

## What this does and does not license

**It licenses recommending narrow extends for growing a fit.** Width one or two
over width eight is a 40-point effect at `t` above 10 with every block agreeing,
on a spend-matched design, and it is directly actionable in
[`docs/schedule-format.md`](schedule-format.md), whose growth recipe already
says `+1` per extend on much older and non-comparable evidence. This campaign
re-establishes that recommendation on the current pin with the current engine.

**It licenses saying that a good prefix is worth building on.** `+128.81` over a
cold sixteen-circle fit, 12 of 12.

**It does not choose between width one and width two.** That contrast was not
registered, it is a null, and the two arms differ in a way — spread — that no
contrast here measures.

**It does not answer the rearrange question.** An extend freezes its prefix, and
polishing is MayFly-only. Whether revisiting circles 1-8 after adding 9-16
recovers anything is unmeasured, and it is the obvious next campaign. Until it
runs, "narrow extends are best" means "best among schedules that never revisit",
not "optimal".

**It does not say the record is the right base.**
[`docs/seed-variance-and-population-report.md`](seed-variance-and-population-report.md)
Finding 1 is that the best eight-circle base finished *last* at every rung from
32 circles on. This campaign holds the base fixed across all four extend arms,
which is what makes them comparable, and therefore says nothing about whether a
different base would end better at sixteen.

**It does not license changing the optimizer default.** No MayFly arm ran here.

**It does not transfer to another fixture, another `lambda`, or another circle
count.** One image, one population, one base, one budget.

## Limitations

- **Twelve blocks.** Adequate for the effects found — every registered p is
  below 1.3e-05 — and inadequate for anything smaller. The width-one against
  width-two null should be read as "not separated", though its interval of
  −2.56 to +5.03 bounds any effect there to a few points.
- **The spread finding is unregistered**, as it was in the restart-shape
  campaign, and it is again the observation a recommendation would lean on.
- **Wall clock is systematically unmatched**, in the direction that makes the
  winner more expensive. Registered on evaluations, as every prior campaign is.
- **The base is quantized.** All arms share the 2.184-point offset, so it
  cancels in every paired contrast, but the absolute costs are measured from
  728.38 rather than from 726.20.
- **One fixture, one covariance mode, one `lambda`.** Full covariance was pinned
  because it never clamps; that means the result is measured only where the
  clamp cannot bite.
- **`cold-w16` is a single control, not a screen.** It shows that *this* cold
  configuration at 112 dimensions loses badly. A cold sixteen-circle fit with a
  larger `lambda` or an IPOP ladder is unmeasured.

## Raw data

- [`cmaes-extend-width-measurement.csv`](cmaes-extend-width-measurement.csv) —
  60 rows, one per campaign, scored from its final stage.
- [`cmaes-extend-width-restarts.csv`](cmaes-extend-width-restarts.csv) — 13,808
  attempt records.
- [`cmaes-extend-width-trajectories.csv`](cmaes-extend-width-trajectories.csv) —
  15,131 sampled iterations.

The measurement CSV's `finalEvaluations` is read from the **final** stage of
each schedule, because a continuation stage's evaluation counter is cumulative;
summing across stages overstates spend by about 4.5x.

## Reproducing it

```sh
go build -o bin/cmaes-measurement ./scripts/cmaes-measurement
bin/cmaes-measurement -design extend-width -action plan
bin/cmaes-measurement -design extend-width -action submit -data-root ./data
bin/cmaes-measurement -design extend-width -action collect -data-root ./data
```

The design is deterministic in its arms, seeds and budgets; `-action plan`
prints all five before anything is submitted. Seeds 120001-120012 are reserved
for it.
