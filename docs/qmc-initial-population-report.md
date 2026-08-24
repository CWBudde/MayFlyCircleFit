# Quasi-Monte Carlo initial populations: a null result

`qmcInit` seeds a MayFly run's first generation from a low-discrepancy sequence
instead of independent uniform draws. It arrived with the v0.7.0 pin as an
expert knob that nothing had measured on this problem. This report measures it.

**The answer is that it does nothing here.** Across three population regimes and
two sequences, all six comparisons are null, and the data bound any true effect
to roughly ±4–5% in the two cheap regimes. Uniform stays the default. Do not
size a campaign around `sobol` or `halton`, and do not read a single campaign
that happens to use one as evidence for it.

## What the mechanism predicts

A low-discrepancy sequence buys even coverage of the box. That only matters
while sample points are scarce relative to dimension, and only while the first
generation still shows through the budget. The eight-circle batch stage is 56
dimensions, and campaigns run it at `popSize 1024` — 2048 individuals for 56
dimensions, where the coverage gap a sequence closes is already negligible.

So the design deliberately went *below* the regime anyone runs, to give the
mechanism its best chance. If QMC cannot be shown to help at `popSize 64` over
128 iterations, it will not help at 1024 over 2048.

## Method

Reference `MayFly-512.png`, `mode: batch`, `variant: standard`, 8 circles,
`batchSize 8`, `optimizerEpochs 1`, convergence disabled, `threads 1` with
`parallelEvaluation` across 8 workers.

| regime | `popSize` | `iters` | evaluations per run | blocks |
| --- | --- | --- | --- | --- |
| `scarce` | 64 | 128 | 25,475 | 40 |
| `mid` | 128 | 512 | 203,011 | 40 |
| `large` | 1024 | 512 | 1,627,139 | 16 |

Three arms per regime — `uniform` (the field left unset, so the control arm is
byte-identical to an unconfigured run), `sobol`, `halton`.

Every arm in a block runs the same seed, so the arms are **paired** and the seed
variance that dominates this problem cancels in the difference rather than
swamping it. Within a regime the arms are **evaluation-matched by
construction**: they differ only in how the first generation is drawn, and the
recorded evaluation counts above are identical across all three arms, not merely
close. Submission was block-major so an early stop would still leave balanced
blocks.

288 jobs, every one terminating `completed`, no failures. Cell counts are exactly
40/40/40, 40/40/40, 16/16/16.

Three tests are reported because they fail differently: a paired t-test
(sensitive, assumes roughly symmetric differences), Wilcoxon signed-rank
(rank-based, survives an outlier run), and a sign test (assumes almost nothing).

## Results

Negative differences favour the quasi-random arm.

| regime | arm | mean Δ | Δ as % | paired t | p | Wilcoxon p | sign | 95% CI on Δ |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `scarce` | `sobol` | −11.16 | −0.90% | −0.386 | 0.701 | 0.877 | 18–22 | [−5.59%, +3.80%] |
| `scarce` | `halton` | −1.85 | −0.15% | −0.075 | 0.941 | 0.732 | 22–18 | [−4.16%, +3.86%] |
| `mid` | `sobol` | +29.25 | +2.52% | +0.971 | 0.338 | 0.304 | 16–24 | [−2.73%, +7.78%] |
| `mid` | `halton` | −6.07 | −0.52% | −0.226 | 0.823 | 0.909 | 22–18 | [−5.21%, +4.17%] |
| `large` | `sobol` | −20.62 | −1.82% | −0.494 | 0.629 | 0.737 | 9–7 | [−9.69%, +6.04%] |
| `large` | `halton` | −11.08 | −0.98% | −0.224 | 0.826 | 0.698 | 9–7 | [−10.30%, +8.34%] |

Nothing clears the uncorrected α = 0.05. With six pre-planned comparisons the
Bonferroni bar is α ≈ 0.008, and the smallest p observed is 0.30. Sign tests sit
at coin-flip throughout. The signs are not even consistent: `sobol` is the worst
arm in `mid` and the best in `large`.

Arm distributions, for scale — the spread across seeds is far larger than any
difference between arms:

| regime | arm | mean | median | min | max |
| --- | --- | --- | --- | --- | --- |
| `scarce` | `uniform` | 1243.63 | 1268.18 | 981.73 | 1416.77 |
| `scarce` | `sobol` | 1232.47 | 1259.81 | 963.09 | 1453.93 |
| `scarce` | `halton` | 1241.79 | 1255.65 | 973.18 | 1433.93 |
| `mid` | `uniform` | 1159.84 | 1182.13 | 903.22 | 1368.35 |
| `mid` | `sobol` | 1189.09 | 1212.54 | 939.63 | 1349.45 |
| `mid` | `halton` | 1153.77 | 1178.63 | 929.41 | 1346.45 |
| `large` | `uniform` | 1131.53 | 1159.77 | 859.25 | 1364.13 |
| `large` | `sobol` | 1110.91 | 1122.04 | 888.24 | 1271.22 |
| `large` | `halton` | 1120.45 | 1144.31 | 853.87 | 1273.03 |

## Under restarts

Nothing runs a single cold start.
[`restart-vs-budget-report.md`](restart-vs-budget-report.md) says a stage's
budget is better spent as several independent cold runs, keeping the best. So the
decision-relevant question is not which arm has the better mean but which has
the better *best-of-k*. Expected minimum over k of the observed runs:

| regime | k | `uniform` | `sobol` | `halton` |
| --- | --- | --- | --- | --- |
| `scarce` | 1 | 1243.63 | 1232.47 | 1241.79 |
| `scarce` | 2 | 1182.17 | 1166.41 | 1179.73 |
| `scarce` | 4 | 1126.38 | 1108.21 | 1117.93 |
| `scarce` | 8 | 1078.59 | 1060.52 | 1052.65 |
| `mid` | 1 | 1159.84 | 1189.09 | 1153.77 |
| `mid` | 2 | 1087.79 | 1118.70 | 1087.89 |
| `mid` | 4 | 1022.06 | 1053.23 | 1024.81 |
| `mid` | 8 | 969.18 | 1004.36 | 975.12 |
| `large` | 1 | 1131.53 | 1110.91 | 1120.45 |
| `large` | 2 | 1048.08 | 1045.93 | 1042.53 |
| `large` | 4 | 972.08 | 985.93 | 965.90 |
| `large` | 8 | 909.62 | 934.21 | 904.94 |

Restarts buy 10–20% in every arm — far more than the sequence choice — and they
buy it about equally in all three. There is no arm whose left tail rewards
restarts more.

## An interim signal that was not real

Worth recording because it is the trap this problem sets. In the `mid` regime
`halton` read −11.2% at 6 blocks and −7.3% with p = 0.072 at 14 blocks. At the
planned 40 blocks it is −0.52% with p = 0.823. Had the campaign stopped at the
interesting-looking interim, it would have reported a 7% improvement that does
not exist. This is the same effect
[`seed-variance-and-population-report.md`](seed-variance-and-population-report.md)
describes, and the reason block counts are chosen before the run and not
adjusted while watching.

## Agreement with the library

MayFly's own `docs/qmc-initialization.md` reports a chance-level effect across
sixteen benchmark problems — two significant Sobol results and none against,
versus about 1.6 expected by chance, with an earlier run of the same study
hitting two *different* problems. This campaign is an independent null on a
seventeenth, real problem. The two agree.

## Limitations

- **One reference image, one variant, one stage.** `MayFly-512.png`, `standard`,
  the batch base stage. Nothing here speaks to the other six variants, to
  polishing, or to the sequential and joint pipelines.
- **The `large` regime is not the production regime.** Campaigns run
  `popSize 1024` at `iters 2048`; this ran it at 512 to afford 16 blocks. Its
  interval is also the widest (±10%), so it is a sanity check, not a
  measurement. The `scarce` and `mid` regimes carry the result.
- **The planned power top-up did not run.** A second wave taking `mid` to 120
  blocks and `scarce` to 60 was staged but never launched — the process meant to
  start it was killed by a timeout while still waiting on the first wave. The
  bounds are therefore ±4–5% rather than the ±2.7% intended. This does not
  change the conclusion but it does mean the null is "no effect detected within
  ±4–5%" rather than a tighter equivalence claim.
- **Version.** Measured under MayFly `v0.7.1` (`2ca01ed`), which is
  behaviour-neutral against `v0.7.0`: standard MA on a sphere at seed 4242
  returns bit-identical costs under both, for all three sequences, at 10 and 56
  dimensions. So these numbers are comparable to a `v0.7.0` baseline —
  the one exception to the rule that every recorded figure predates the current
  pin.

## The data

The per-run costs are committed as
[`qmc-initial-population-screen.csv`](qmc-initial-population-screen.csv)
— one row per job, 288 in total. Its columns, in file order, are:

| column | meaning |
| --- | --- |
| `regime` | `scarce`, `mid`, or `prod`. **`prod` is the regime this report calls `large`**; the file predates the rename and was not rewritten. |
| `qmc` | the arm: the `qmcInit` value, `uniform`, `sobol`, or `halton`. |
| `block` | the paired block number, 1-based. |
| `seed` | the *effective* seed the run used, `7000 + block`. There is no separate user-seed column. |
| `iters` | iterations the stage ran. |
| `evals` | objective evaluations the stage spent. |
| `bestCost` | the run's final cost, the only quantity every table above is computed from. |
| `initialCost` | `nan` throughout, because a batch-stage checkpoint does not carry it. It is kept only so the columns match the collector. |
| `termination` | the optimizer's termination reason, `completed` for every row. |

Every table above is derived from that file and nothing else. The checkpoints it
was extracted from no longer exist, so this is the record.

## Reproducing

Campaign and analysis scripts are not committed; the campaign is a submit loop
against a `serve` instance, and the checkpoints hold everything needed:

```sh
mayflycirclefit serve --port 8084 --data-root ./data-qmc --max-jobs 8 \
    --queue-size 100 --input-root .
```

Post one job per cell with `project: "<regime>-<arm>-b<NN>"`, the settings in the
method table, and `seed: 7000 + block`. Read `bestCost` out of each
`data-qmc/projects/<project>/jobs/<uid>/checkpoint-info.json` and pair on the
block number.
