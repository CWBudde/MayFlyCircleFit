# The deep hunt: the eight-circle record, broken

`-design deep-hunt` is the first campaign here that is not a hypothesis test. It
existed to beat one number — **752.5220120747884**, the best eight-circle cost
this repository had recorded on `example/MayFly-512.png` — and it beat it.

**The new best eight-circle cost on that fixture is 726.1984354654948**, from
arm `blk-ipop`, block 7, seed 114007, job
`65b38e2f-e75f-4d80-8ae9-822e8a28ede6`. That is **26.32 below** the previous
record.

The design is descriptive and **registers no contrasts at all**. Nothing below
is a p-value and nothing below should be quoted as one. Every paired difference
in this report is an observed difference over eleven blocks, not a test.

## Conditions

| | |
| --- | --- |
| binary | built from `83cf08a`, `feat(measurement): hunt the record on the CMA-ES knobs nobody has turned (#122)` |
| optimizer | `github.com/CWBudde/go-cma-es v0.1.0` (`optimizerVersion` `v0.1.0` in every row) |
| fixture | `example/MayFly-512.png`, 8 circles, one batch, 56 dimensions |
| budget | `huntBudget` = 12,582,912 evaluations per job (1.94x the cap every earlier design inherited) |
| backend | `cpu` on every job, no degradation |
| host | 64-core Linux box, `serve --max-jobs 7`, `threads 1`, `evaluationWorkers 8` per job |
| dates | submitted 2026-08-30 08:33 CEST, last job finished 17:40 CEST — 9h07m wall, 62.9h of optimizer time |
| seeds | 114001-114011 (campaign), 115001-115003 (follow-up probe) |

## What was actually run, and what was not

The design registers 9 arms x 11 blocks = 99 jobs. **89 of them ran.** The ten
`sep-ipop-passive` jobs for blocks 2-11 were cancelled while still queued at
09:23, with zero evaluations each, to free worker slots for the sigma probe
below.

That leaves **`sep-ipop-passive` at n = 1**, and it is the design's only
single-factor row for `activeCMA`. **The `activeCMA` question is therefore not
answered by this campaign** — one block is one draw, and the arm's block-1 cost
of 812.5142 is bit-identical to `sep-ipop`'s, which is what a shared
deterministic prefix looks like rather than evidence of anything.

Because `collect` refuses a manifest it cannot complete, the CSVs were collected
against a filtered manifest holding the 89 completed jobs
(`manifest-deep-hunt-completed.csv`), and `analyzeDesign` was not run — the
tables here are computed from the committed CSVs instead. The original 99-row
manifest is unchanged.

## The arms

| arm | n | min | mean | median | max | runs under the old record | median scored evals | median hours |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `blk-ipop` | 11 | **726.1984** | 791.09 | 792.65 | 855.46 | 2 | 12,440,576 | 0.93 |
| `sep-warm-e8` | 11 | 742.5513 | 743.16 | 743.02 | 744.37 | **11** | 3,013,632 | 0.38 |
| `sep-ipop` (control) | 11 | 812.5142 | 868.33 | 864.26 | 938.99 | 0 | 4,483,072 | 0.93 |
| `sep-ipop-passive` | **1** | 812.5142 | — | — | — | 0 | 7,975,936 | 1.02 |
| `blk-l4096` | 11 | 817.1991 | 970.77 | 906.31 | 1230.06 | 0 | 2,891,776 | 0.30 |
| `sep-ipop-s050` | 11 | 819.5411 | 856.35 | 845.72 | 943.43 | 0 | 4,922,368 | 0.92 |
| `sep-ipop-s015` | 11 | 819.5551 | 873.48 | 873.29 | 964.62 | 0 | 4,244,480 | 0.91 |
| `sep-l4096` | 11 | 837.7228 | 948.39 | 935.47 | 1069.26 | 0 | 5,410,816 | 0.46 |
| `sep-e8` | 11 | 841.5055 | 949.40 | 918.69 | 1251.16 | 0 | 5,508,096 | 0.49 |

Follow-up probe (not part of the design, see below):

| arm | `initialSigma` | n | min | mean | max |
| --- | ---: | ---: | ---: | ---: | ---: |
| `warm-s100` | 0.10 | 3 | 742.5560 | 742.78 | 743.13 |
| `warm-s200` | 0.20 | 3 | 742.6936 | 745.85 | 751.21 |
| `warm-s020` | 0.02 | 3 | 750.6252 | 750.76 | 750.84 |

**22 of the 98 runs finished below 752.52.**

## Per block

| block | seed | `sep-ipop` | `blk-ipop` | delta | `sep-ipop-s015` | `sep-ipop-s050` | `sep-e8` | `sep-warm-e8` | `sep-l4096` | `blk-l4096` |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 114001 | 812.51 | 792.65 | -19.86 | 831.36 | 880.79 | 934.62 | 742.56 | 1069.26 | 951.89 |
| 2 | 114002 | 867.17 | 791.76 | -75.41 | 873.29 | 943.43 | 1251.16 | 743.02 | 986.65 | 856.36 |
| 3 | 114003 | 856.86 | 829.72 | -27.14 | 964.62 | 870.15 | 892.60 | 742.58 | 837.72 | 1211.66 |
| 4 | 114004 | 857.37 | 750.51 | -106.86 | 825.15 | 836.99 | 850.78 | 743.06 | 1045.37 | 1230.06 |
| 5 | 114005 | 868.61 | 855.46 | -13.15 | 844.63 | 824.68 | 841.51 | 743.89 | 995.59 | 940.28 |
| 6 | 114006 | 864.26 | 793.08 | -71.19 | 909.35 | 851.66 | 918.69 | 742.55 | 901.31 | 906.31 |
| 7 | 114007 | 860.29 | **726.20** | -134.09 | 890.49 | 821.72 | 962.05 | 744.37 | 935.47 | 839.68 |
| 8 | 114008 | 835.67 | 787.47 | -48.20 | 846.09 | 828.25 | 848.84 | 742.72 | 903.05 | 861.69 |
| 9 | 114009 | 890.79 | 811.40 | -79.39 | 905.95 | 896.96 | 966.58 | 743.81 | 1037.15 | 817.20 |
| 10 | 114010 | 938.99 | 753.38 | -185.61 | 897.76 | 819.54 | 883.48 | 742.83 | 874.31 | 871.41 |
| 11 | 114011 | 899.16 | 810.41 | -88.75 | 819.56 | 845.72 | 1093.11 | 743.38 | 846.42 | 1191.90 |

## Block covariance is the lead

`blk-ipop` moves exactly one thing against the control: `covarianceMode` from
`separable` to `block`, which `app` pins to `ParametersPerCircle`, so it is
eight 7x7 blocks — one circle each. It is **better in 11 of 11 blocks**, by a
mean of 77.24 and a per-block margin of 13.15 to 185.61.

This is the strongest signal the deep hunt produced and it is a **lead, not a
finding**: the design registered no contrast, eleven blocks of one arm against
one control is not a corrected test, and the lambda screen's caution applies —
an undetected effect is not a measured one, and the reverse holds too. **A
registered `blk-ipop` versus `sep-ipop` campaign is the obvious next
measurement**, and it now has an effect size to power itself from.

The two compound arms that also use block covariance (`blk-l4096`) are the
campaign's *worst* mean, so block covariance is not good on its own — it is good
in the IPOP schedule, which is exactly why the single-factor row is the one to
read.

## The record run, and the rung nobody had reached

The record job's own restart ladder is the most useful record in this campaign:

| restart | lambda | iterations | evaluations | best cost | termination |
| ---: | ---: | ---: | ---: | ---: | --- |
| 0 | 1024 | 818 | 837,632 | 865.6779 | `tol_fun` |
| 1 | 2048 | 755 | 1,546,240 | 870.6810 | `tol_fun` |
| 2 | 4096 | 945 | 3,870,720 | 828.2873 | `tol_fun` |
| 3 | **8192** | 773 | 6,328,320 | **726.1984** | `maximum_evaluations` |

Three rungs converged and found nothing better than 828. The record was set on
the fourth rung — **and that run was still cut off by the cap.** It had not
converged when the budget ran out.

This is also the answer to the design's second question. Across the two earlier
committed restart CSVs, 57 of 57 runs at lambda 4096 terminated on
`maximum_evaluations`: the top of the IPOP ladder had never been allowed to
finish. At `huntBudget` it finishes:

| lambda | `tol_fun` | `maximum_evaluations` | `condition_number` |
| ---: | ---: | ---: | ---: |
| 1024 | 44 | 0 | 1 |
| 2048 | 45 | 0 | 0 |
| 4096 | 32 | 12 | 1 |
| 8192 | 0 | **33** | 0 |

**The binding constraint moved up exactly one rung.** Lambda 4096 now converges
in 32 of 45 runs; lambda 8192, which no earlier campaign ever reached, is
truncated in 33 of 33. And it is the rung that produced the record: `blk-ipop`
took its block best from the lambda 8192 rung in 6 of 11 blocks, while the
separable control never got a block best above lambda 4096 and reached lambda
8192 at all in only 6 blocks of 11.

So the natural reading is not "the budget was big enough" but "the budget is
still the constraint, one rung further up, and the arm that can use the big
populations is the one with block covariance."

## 752.52 is not where the search stops

`sep-warm-e8` starts from the old record's circles (`initialCircles`, sigma
0.05, eight epochs) and **ended below 752.52 in 11 of 11 blocks**, in a tight
742.55-744.37 band.

That is a statement about this warm start, not about the shape of the old
record, and two things stop it short of "752.52 was a point on a slope". The
run does not begin exactly at the recorded optimum: `warmStartSpecs()` renders
each circle's colour as hex, because `initialCircles` is the only
operator-authored warm start the system has and that format names colours in
eight bits per channel, so every colour coordinate is quantized on the way in.
And CMA-ES at sigma 0.05 samples a whole neighbourhood rather than following a
local descent direction, so leaving a genuine local minimum is well within what
it can do. What the arm establishes is that a warm start from the old record's
neighbourhood reliably finds something better than it -- which is what the
campaign needed from it -- not that the old record had a downhill direction.

The follow-up probe (`submit-probe.py`, 9 jobs, seeds 115001-3) was submitted
mid-campaign once block 1 showed this, cloning job `30ab5fa0`'s config verbatim
and varying only `initialSigma` and the seed. It is **not part of the design**
and carries its own manifest and CSVs. It says initialization matters here and
in which direction: sigma 0.10 is best (742.556 min, 742.78 mean), 0.20 is
noisier (up to 751.21), and 0.02 is clearly too small to escape the starting
point (750.63 min, and all three runs land within 0.21 of each other).

Note what the warm arms did *not* do: none of them approached 726.20. Eleven
independent warm starts converged to ~743, so **the region around the old record
and the region `blk-ipop` found are different basins.**

## Cold-start sigma moved nothing that separates from noise

`sep-ipop-s015` and `sep-ipop-s050` are the design's other true single-factor
rows, moving `initialSigma` alone from the 0.3 default. Neither separates from
the control, and they do not even fall on the same side of it. Paired by block
against `sep-ipop`, sigma 0.15 is worse by 5.14 (`t = -0.34`, better in 4 blocks
of 11) while sigma 0.50 is **better** by 11.98 (`t = +0.72`, 7 of 11) -- lower
cost is better, so the 856.35 mean above is an improvement on the control's
868.33, not a loss. Both differences sit far inside the noise of an eleven-block
paired difference whose standard deviation is about 50, neither had a contrast
registered for it, and neither arm produced a single run under the old record:
the best cold run in the pair is 819.54 against the control's own 812.51. So
nothing here recommends moving the default in either direction, and whatever
initialization buys on this fixture is still bought from a warm start rather
than a cold one.

## Diagnostics

Over 16,949 trajectory samples, `distributionExtent` = `sigma * max(D)` never
exceeds **1.727** (median 0.0357) while raw sigma spans 5.77e-02 to 4.54e+57 and
the condition number reaches 8.60e+13. This reproduces the lambda screen's
finding on new seeds and a new covariance mode: **raw sigma is gauge-dependent
and must not be cited as evidence of a diverged search**; `distributionExtent`
is the identifiable column.

## What this does and does not license

**It does** establish a new best recorded cost on this fixture, 726.1984354654948,
with its full provenance below, and it does discharge the lambda-4096
convergence question the restart ladder left open.

**It does not** license a default change of any kind. The design is descriptive,
the headline is an order statistic from 98 runs, the block-covariance signal is
an unregistered eleven-block difference, and the arm that set the record ran at
a budget 1.94x the one every comparative campaign in this repository uses — so
its cost is not comparable to any figure in `cmaes-report.md`,
`cmaes-lambda-report.md`, `cmaes-stagnation-report.md`,
`cmaes-budget-split-report.md` or `cmaes-restart-ladder-report.md`, and no
MayFly arm was run here at all.

**It does leave one thing stale.** `recordCircles()` and `recordCost` in
`scripts/cmaes-measurement/main.go` still carry 752.5220120747884 and its
circles, and `reportDescriptive`'s `vs record` column is computed against it.
The specs below supersede that solution as the best recorded fit, but replacing
the constant is a decision about a frozen design input that several tests assert
on, and it is deliberately not made in this report.

## Provenance

Committed artifacts:

- `cmaes-deep-hunt-measurement.csv` — 89 campaign jobs
- `cmaes-deep-hunt-trajectories.csv` — per-iteration diagnostics
- `cmaes-deep-hunt-restarts.csv` — 168 per-restart records
- `cmaes-deep-hunt-probe-{measurement,trajectories,restarts}.csv` — the 9 probe
  jobs. The restarts file is a header row and nothing else, and that is correct
  rather than a collection failure: the probe clones a `sep-warm-e8` job, that
  arm runs `restartStrategy: none`, and only the IPOP arms write per-restart
  records at all.

The record fit, job `65b38e2f-e75f-4d80-8ae9-822e8a28ede6`, seed 114007, cost
726.1984354654948, 3291 iterations, 12,582,915 evaluations, `cpu`, under
`covarianceMode: block`, `restartStrategy: ipop`, `popSize` 1024,
`initialSigma` 0.3 (default), `activeCMA` on, in `recordCircles()` shape:

The 12,582,915 in that line is the job's `finalEvaluations` counter, not the
budget, and the two are not the same accounting. `huntBudget` is 12,582,912 and
the optimizer honours it exactly: this job's four per-restart records in
`cmaes-deep-hunt-restarts.csv` are 837,632 + 1,546,240 + 3,870,720 + 6,328,320,
which is 12,582,912 to the evaluation. The job-level counter reports three more,
and the offset is a per-job constant rather than a per-restart one -- every run
in this campaign that reaches the cap reports 12,582,915, whether its ladder
held three restarts or four. Compare a `finalEvaluations` against another
`finalEvaluations`; never against the budget.

```go
{x: -255.9984368618, y: 162.9965102619, r: 453.9864281082, red: 0.3609139228, green: 0.2820853129, blue: 0.0920986151, opacity: 0.7111318709},
{x: 744.4424245310, y: -26.0369045361, r: 505.2796370210, red: 0.3440046884, green: 0.2823678220, blue: 0.0693873643, opacity: 0.8724086185},
{x: 265.0455681973, y: -60.1884457624, r: 173.5574152281, red: 0.2274085173, green: 0.1787557007, blue: 0.0083019424, opacity: 0.9996893742},
{x: 238.4804876773, y: 115.1411171072, r: 123.4125723014, red: 0.0002970486, green: 0.0029787927, blue: 0.0010754562, opacity: 0.3081065940},
{x: 603.0374244715, y: 704.5071940232, r: 497.7213147548, red: 0.3691050888, green: 0.3294344931, blue: 0.1844828750, opacity: 0.9995349479},
{x: -130.9847103652, y: 397.7000301004, r: 378.6305876042, red: 0.1005364754, green: 0.0744961555, blue: 0.0001518657, opacity: 0.8537465786},
{x: 404.5290495013, y: 113.0087727614, r: 35.9797436427, red: 0.9995692756, green: 0.9541192220, blue: 0.7392165928, opacity: 0.9983728139},
{x: 197.2818260205, y: 149.9331262837, r: 347.0495027937, red: 0.2275988185, green: 0.2439870809, blue: 0.1426256606, opacity: 0.4372276652},
```

One circle is worth noticing: the seventh is at (404.5290, 113.0088) with
r 35.9797 and a near-white colour, and the old record carries the same circle at
(404.5303, 113.0090), r 35.9794 — the two solutions agree on that small bright
highlight to within 0.002 in every geometric coordinate, and disagree about
everything else.

The measurement host was wiped after collection; these CSVs and the parameters
above are the whole record.
