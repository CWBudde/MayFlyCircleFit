# The 233-circle growth run

A production run, not a campaign. It **registers no contrasts and tests
nothing**: it applies the recipe the polish-engine campaign selected and drives
it as deep as a fixed wall-clock window allowed. That campaign's report is not
written yet; its data is in `cmaes-polish-engine-measurement.csv` and the two
CSVs beside it. Read it for the records it sets, for
the two structural ceilings it found, and for the method note about re-seeding.
Do not read any number in it as a comparison — there is no control arm anywhere.

Run 2026-09-06 20:52 to 2026-09-07 05:43 on the 64-core box, fixture
`example/MayFly-512.png` (the same image every CMA-ES campaign fits), CPU
backend, go-cma-es v0.1.0, seed 4242.

## Result

**233 circles at cost 236.07313791910806**, from a sixteen-circle base of
546.1567522684733. 217 circles added over 217 extend/polish pairs, 449 jobs,
61,764,497 evaluations, 1,012,656 iterations.

| circles | cost | | circles | cost |
| ---: | ---: | --- | ---: | ---: |
| 16 | 546.16 | | 100 | 315.29 |
| 20 | 509.59 | | 140 | 284.57 |
| 40 | 409.37 | | 180 | 259.85 |
| 60 | 363.13 | | 220 | 240.59 |
| 80 | 335.65 | | 233 | 236.07 |

Per-circle gain falls steeply and monotonically: circle 17 bought 8.91 points,
circle 81 bought 0.79, circle 233 bought 0.29. The full per-depth series is
[`growth-run-trajectory.csv`](growth-run-trajectory.csv), 218 rows with no gaps.

## Records this run sets

Three records are recorded here for the first time. The first two predate this
run — they come from the polishing pilot and were never written down.

| circles | cost | supersedes |
| ---: | ---: | --- |
| 8 | **725.0288747152** | 726.1984354654948 (`cmaes-deep-hunt-report.md`) |
| 16 | **546.1567522685** | 559.5857671101888 (`cmaes-extend-width-report.md`) |
| 233 | **236.0731379191** | — (no prior fit at this depth) |

Best solution per depth per campaign, 247 of them from 8 to 233 circles, is
indexed in [`growth-run-records.csv`](growth-run-records.csv). The parameter
vectors are in the archive named under Artifacts.

`scripts/cmaes-measurement/main.go`'s `recordCircles()` and `recordCost` still
carry the superseded 752.52 figure, as `cmaes-restart-shape-report.md` already
noted; this run does not change that.

## The recipe

The winning arm of the polish-engine campaign, unchanged: one circle per
extend, a CMA-ES polish sweep after each.

- **extend** — `additionalCircles` 1, `batchSize` 1, λ 64, `iters` 635,
  `restarts` -10 (budget-filling cold attempts), `epochs` 1.
- **polish** — `polishOptimizer: cmaes`, σ 0.02, `hybrid-overlap`,
  `activeSetSize` 8, `maxSweeps` 2, `iters` 400, `popSize` 50, `epochs` 2.

`maxSweeps` is 2 rather than the campaign's 4 because the pilot measured pass 2
at ~1% of pass 1; sweeps 3 and 4 are near-free to skip and the run traded them
for depth.

A pair cost 24-37 s and **did not grow measurably with depth** — an extend
composites its one new circle onto the retained accumulated canvas, so the cost
of the frozen prefix is paid once, not per evaluation. This is the practical
consequence of the staged-pipeline work in Task 11.13 and it is what makes a
233-circle fit reachable in under two hours of compute.

## Two structural ceilings

Both were found by hitting them, and both bound any future staged campaign.

**1. Full covariance caps a seeded base at 73 circles.** A schedule's `base`
declares `covarianceMode` for the *whole* parameter vector and every step
inherits it. `app.MaxCMAESFullDimensions` is 512, so `full` is refused from 74
circles up (74 x 7 = 518). This is the same constant PR #136 guards the
polishing sweep with. `block` has no such cap and costs nothing for this
recipe: block groups exactly the seven parameters of one circle, so an extend
of width one searches a single block that *is* the full 7x7 matrix.

**2. A re-seeded base cannot exceed 100 circles at all.** `initialCircles`
requires `batchSize` to cover every circle, and `app.MaxBatchSize` is 100:

```
base: initialCircles requires batchSize to cover every circle (batchSize 100, circles 104)
```

So the whole idea of chaining schedule documents by re-seeding each one from the
last has a hard 100-circle ceiling built into it, independent of
`MaxScheduleSteps` (256, which alone would cap an interleaved document at 128
pairs).

**What works instead:** chain `POST /api/v1/jobs/:id/extend` and `/polish`
against the parent job. A continuation reads the parent's checkpoint, so there
is no re-seed, no `initialCircles`, no `batchSize` rule, and no covariance
declaration over the full vector. It has no depth ceiling and is lossless by
construction rather than by an exact colour round trip. This is the shape any
future deep run should use; the schedule document remains the right form for a
registered campaign at a fixed, modest depth.

## Method note: the lossless re-seed works

The `rgb` field added in PR #136 was exercised for real here and is confirmed
bit-exact. A wave seeded from a 16-circle solution reproduced
`546.1567522684733`, and one seeded from an 80-circle solution reproduced
`335.6533393859863` — both bit-identical to the parent job's cost. Through the
8-bit hex form the same re-seed would have cost 0.9 to 3.7 points, which is
larger than several measured effects in this repository.

## What this run does not establish

- **Nothing comparative.** One arm, one seed, no control. The recipe was
  selected by the polish-engine campaign; this run assumes it. That campaign's
  own caveat carries over: its engine contrast is confounded with iteration
  count, so nothing here licenses a `polishingOptimizer` default either.
- **Costs compare to nothing.** No earlier campaign fits more than 16 circles.
  A 233-circle cost and a 16-circle cost are not the same quantity.
- **Not an optimum at any depth.** Every circle is placed greedily and its
  prefix is frozen; polishing corrects an 8-circle active set, never the whole
  arrangement. "Best" continues to mean "best among schedules that never
  rearrange".
- **The stopping depth is an artifact of the clock**, not of convergence. Gain
  per circle was still positive (0.2917) at 233 and falling smoothly.

## Cost of the run, honestly

Wall clock 20:52 to 05:43 is 8h 51m; actual compute was **1h 57m**. The
difference is two failures of the driver, both from the ceilings above: the run
sat idle 21:28-03:44 (6h 16m) and again 03:55-04:21 (26m), because the driver
treated a refused document as terminal. The depth reached is therefore a lower
bound on what the window allowed — roughly a third of it was used.

## Artifacts

Archived off the box to `~/backups/circlefit-20260907/` (147 MB, SHA256-verified
against the source): per-campaign tarballs carrying 1,713 checkpoints and 1,713
traces, job and schedule metadata for all five servers, and 247 best-per-depth
parameter vectors. `diff.png` was excluded as regenerable. The final solution is
job `c43c9b96-ca87-4821-a9f1-87e894977a89` in `growth.tar.gz`.
