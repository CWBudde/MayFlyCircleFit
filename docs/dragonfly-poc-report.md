# Dragonfly proof-of-concept: measured against MayFly `standard`

**Verdict: Dragonfly loses decisively on this problem.** It wins zero of twelve
blocks in every arm, including one where it is given more evaluations than the
MayFly baseline. It stays in the tree as an expert-only alternative for further
experiments, not as a candidate default.

## What was run

The eight-circle base stage on `example/MayFly-512.png` (512x512, blank-canvas
cost 38732.12), batch mode with `batchSize 8`, `popSize 1024`,
`optimizerEpochs 1`, `threads 1`, `parallelEvaluation true`,
`evaluationWorkers 8`, `disableConvergence true`. The engine is
`github.com/CWBudde/dragonfly` v0.1.0, continuous DA; the baseline is MayFly
v0.6.0 `standard`, reusing the ladder measured in
[`docs/restart-vs-budget-report.md`](restart-vs-budget-report.md).

Twelve blocks, each block scored as the minimum cost over its restarts. Seeds
are nested so a block's restarts are distinct runs of the same block:
`seed = 2000 + (b-1)*16 + k` for the 16-restart ladder, and
`seed = 3000 + (b-1)*32 + j` for the supplementary restarts 17..48. 591 jobs
total, all `completed`, none over its iteration budget.

**Baseline reusability was verified, not assumed.** Three `s01` confirmation
jobs re-ran blocks 1-3 of the MayFly ladder under the current binary and
reproduced their recorded costs exactly (1293.9384, 1058.8507, 936.7617) at an
identical 6,397,955 evaluations.

## The evaluation-matched arm

Dragonfly costs **1,032 evaluations per iteration** against `standard`'s
**3,124** — it is 3x *cheaper* per iteration. An iteration-matched comparison
therefore hands MayFly roughly three times the evaluations, which is the
opposite of the AOBLMOA case and unfair in Dragonfly's favour to correct.

A supplementary arm raised Dragonfly to **48 restarts** per block, against the
**13.2** restarts `standard` fits inside the same evaluation cap
(CAP = 6,397,955, r01's budget). At 48 restarts Dragonfly spends **6,340,752**
evaluations per block against `standard`'s **5,926,475** — it now spends
slightly *more*. This is the row to cite.

## Results

| Arm | dragonfly | standard | delta | t (df=11) | blocks won |
| --- | --- | --- | --- | --- | --- |
| **Evaluation-matched** (48 vs 13.2 restarts) | 1321.73 | **890.05** | **-431.68** | **-16.81** | **0/12** |
| Iteration-matched (16 restarts each) | 1349.82 | 887.77 | -462.04 | -19.49 | 0/12 |
| Single run (2048 iterations) | 1403.17 | 1047.48 | -355.69 | -7.05 | 0/12 |

`t_crit` at df=11 is 2.20. Negative deltas mean Dragonfly is worse.

Per block, evaluation-matched:

| block | dragonfly@48 | standard@cap | delta | | block | dragonfly@48 | standard@cap | delta |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 1352.93 | 945.63 | -407.30 | | 7 | 1194.37 | 926.56 | -267.81 |
| 2 | 1340.18 | 813.56 | -526.61 | | 8 | 1330.63 | 840.27 | -490.36 |
| 3 | 1284.01 | 919.66 | -364.35 | | 9 | 1391.41 | 969.06 | -422.35 |
| 4 | 1377.93 | 894.26 | -483.66 | | 10 | 1315.06 | 909.68 | -405.37 |
| 5 | 1249.78 | 956.46 | -293.32 | | 11 | 1348.99 | 875.78 | -473.21 |
| 6 | 1342.22 | 847.85 | -494.37 | | 12 | 1333.30 | 781.86 | -551.44 |

Across all 576 individual Dragonfly restarts: mean 1497.40, sd 78.87, minimum
1194.37. The best single Dragonfly run out of 576 is still worse than every one
of the twelve `standard` block scores.

## What the restart arm settles

Tripling the restarts moved Dragonfly by 30 points (-462.04 to -431.68) against
a deficit of over 400. The loss is therefore not a budgeting artefact: adding 32
restarts closes almost none of the gap at this budget. What happens beyond 48
draws was not measured, so this rules out the restart count as the explanation
only over the range that was run. This is measured rather than argued, which is
why the supplementary arm was worth its 384 jobs.

The direction matches the library's own documentation: DA explores well and
exploits poorly, its convergence factor reaching zero at the halfway point of a
run. The magnitude was not predictable from that alone.

## What this does not establish

- Only the eight-circle base stage, only batch mode, only `popSize 1024`. No
  claim about later stages, other circle counts, or other populations.
- Dragonfly v0.1.0 only. The adapter maps a fixed set of knobs; a different
  parameterisation of DA was not searched, and the library is early enough that
  its own defects have not been ruled out as a contributor.
- Nothing beyond 48 restarts per block. The distribution of a larger restart
  budget was not sampled, so the report does not say where, if anywhere, more
  draws would close the gap.
- No multi-objective arm. Only continuous DA was measured; BDA and MODA are out
  of scope for this problem.

## Recommendation

Keep `optimizer: dragonfly` available as an expert-only alternative for
experiments. Do not propose it as a default, and do not re-run this screen
against v0.1.0 — re-measure only after the library changes.
