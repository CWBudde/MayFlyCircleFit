# AOBLMOA under paper fidelity

MayFly v0.6.0 reimplemented the `aoblmoa` variant to match its source paper.
On the eight-circle base stage the faithful algorithm is **significantly worse
than plain `standard` MayFly under restarts** and indistinguishable from it as
a single long run, while costing 31% more evaluations per iteration to get
there.

At the `r16` restart arm, matched on evaluations, `aoblmoa` loses by 61.74 cost
points with `t = -3.01` over twelve paired blocks, winning 4 of 12. As a single
2048-iteration run it is a null result (`t = -0.43`). There is no arm in this
campaign where it wins.

This report is a verdict on the algorithm's search quality on this problem, not
on the fidelity of the port: the v0.6.0 implementation follows the paper, and
the `standard` confirmation arm below shows the surrounding machinery is
unchanged. See [`restart-vs-budget-report.md`](restart-vs-budget-report.md) for
the `standard` baseline this is measured against.

Source paper: Zhao, Y.; Huang, C.; Zhang, M.; Cui, Y. *Biomimetics* **2023**,
8(4), 381. DOI [10.3390/biomimetics8040381](https://doi.org/10.3390/biomimetics8040381).

## What was run

`MayFly-512.png` (512x512, blank-canvas cost 38732.12), eight circles, `mode`
batch with `batchSize` 8, `popSize` 1024, `optimizerEpochs` 1, `threads` 1,
`parallelEvaluation` with 8 evaluation workers, convergence detection disabled
so early stopping could not distort a budget. `aquilaWeight` was left unset,
which under v0.6.0 selects the paper's deterministic fitness test.

207 jobs on a 64-core box, driven through `serve` so the campaign was
observable while it ran. Binary built from `48c3a7b`.

| Arm | Variant | Shape | Blocks | Runs |
| --- | --- | --- | ---: | ---: |
| `s01` | standard | 1 x 2048 iterations | 3 | 3 |
| `a01` | aoblmoa | 1 x 2048 | 12 | 12 |
| `a16` | aoblmoa | 16 x 128 | 12 | 192 |

Seeds nest against the existing restart ladder as `seed = 2000 + (b-1)*16 + k`,
so `a01`'s block *b* and `a16`'s block *b* share a seed prefix and both pair
against the same `standard` block. An arm's score for a block is the
**minimum** cost over that block's restarts.

## The confirmation arm

The `standard` baselines this report compares against were measured under an
older binary. `s01` re-runs three of them to establish that they are still
reusable.

| Block | Seed | `s01` best cost | Recorded baseline |
| ---: | ---: | ---: | ---: |
| 1 | 2001 | 1293.9384 | 1293.938416 |
| 2 | 2017 | 1058.8507 | 1058.850681 |
| 3 | 2033 | 936.7617 | 936.761693 |

Exact matches, at an identical 6,397,955 evaluations per run. The `standard`
ladder is reusable against this binary.

## Results

Positive delta means `aoblmoa` is better. Twelve paired blocks, `df = 11`,
`t_crit = 2.20`.

| Comparison | aoblmoa | standard | Delta | t | Blocks won |
| --- | ---: | ---: | ---: | ---: | :---: |
| `r16`, evaluation-matched | 951.79 | **890.05** | **-61.74** | **-3.01** | 4/12 |
| `r16`, iteration-matched | 933.69 | 887.77 | -45.91 | -2.30 | 4/12 |
| `r01`, iteration-matched | 1074.12 | 1047.48 | -26.64 | -0.43 | 4/12 |

Per-block, evaluation-matched at `r16`:

| Block | aoblmoa | standard | Delta |
| ---: | ---: | ---: | ---: |
| 1 | 886.72 | 945.63 | +58.91 |
| 2 | 950.88 | 813.56 | -137.31 |
| 3 | 999.43 | 919.66 | -79.77 |
| 4 | 1048.15 | 894.26 | -153.88 |
| 5 | 932.89 | 956.46 | +23.57 |
| 6 | 980.50 | 847.85 | -132.65 |
| 7 | 980.91 | 926.56 | -54.35 |
| 8 | 906.67 | 840.27 | -66.40 |
| 9 | 968.56 | 969.06 | +0.50 |
| 10 | 902.08 | 909.68 | +7.60 |
| 11 | 948.21 | 875.78 | -72.43 |
| 12 | 916.49 | 781.86 | -134.63 |

The four `aoblmoa` wins are small (+0.50 to +58.91); the losses are not
(-54.35 to -153.88). At the level of individual runs rather than block minima,
the same ordering holds: 192 `a16` runs average 1138.70 (sd 122.33) against the
`standard` `r16` ladder's 1072.04 (sd 120.69).

## Why evaluation-matched is the row to cite

`aoblmoa` spends **4,097 evaluations per iteration** against `standard`'s
**3,124**. An iteration-matched comparison therefore hands it 31% more
evaluations across a 16-restart block: 8,421,424 against a nominal 6,428,720.
Against what the old `standard` ladder *actually* spent, 7,198,825 — inflated
by the doubling bug described below — the gap is 17%.

Removing that advantage makes `aoblmoa` *worse*, not better: -45.91 becomes
-61.74. The loss is not an artefact of how the arms were budgeted, and
correcting the budgeting deepens it.

The evaluation-matched row caps both arms at 6,397,955 evaluations — `r01`'s
budget, the same cap used in the restart report — taking restarts in order
until the next one would not fit. Under that cap 12.0 `aoblmoa` restarts fit
against 13.2 `standard` restarts. Recomputing the `standard` side from the raw
ladder reproduces the published 890.05 at 13.2 restarts exactly, which is what
validates the reconstruction.

There is a second reason to prefer the capped row. 23 of the 192 old `standard`
`r16` runs carry a doubled iteration budget from the batch-refill bug fixed in
`48c3a7b`. The evaluation cap absorbs that, because it counts evaluations
actually spent and a doubled run simply consumes two restarts' worth. The
iteration-matched row does not absorb it and mildly flatters `standard`; that
is the likely source of the ~16-point gap between the two `r16` rows.

None of the 207 runs in this campaign exceeded its budget, which is the first
campaign-scale confirmation that the `48c3a7b` fix holds.

## What this does not establish

- **Only this problem and this stage.** Eight circles, batch mode, one
  reference image. The paper's own benchmarks are unrelated functions.
- **Only `popSize` 1024 and one iteration budget.** No interaction between
  variant and population size was measured.
- **`oppositionProbability` was not a factor** — it is inert under v0.6.0.
- **`aquilaWeight` was left unset throughout.** A separate sweep over
  `aquilaWeight` in {0, 0.05, 0.10, 0.25, 0.50} under the pre-v0.6.0
  probabilistic branch returned a null result at every setting, so the
  deprecated override is not a promising direction to re-test.

## Recommendation

Do not make `aoblmoa` a default. It is significantly worse than `standard`
under restarts on this problem and costs more per iteration. Keeping it as a
selectable variant is a separate question this campaign does not settle: it
found no arm where `aoblmoa` wins, but it also only examined one problem.
