# The lambda x covariance screen: two hypotheses retired

This is the registered `lambda` screen, run to completion. Eight arms, twelve
paired seed blocks, ninety-six jobs, one shared evaluation budget. It exists to
answer the two questions
[`cmaes-report.md`](cmaes-report.md) raised and its design could not settle:
whether the winning arm's separable covariance was doing the work, and whether
pinning `lambda` to `popSize` = 1024 — against Hansen's default of
`4 + floor(3 ln 56)` = 16 for this 56-dimension search — was costing quality.

**The headline is a null.** Thirteen paired contrasts, none of which survives
Holm's step-down procedure at a family-wise `alpha` = 0.05. The best arm reaches
`p` = 0.00557 against the control and the first Holm gate is 0.05/13 = 0.00385,
so the step-down stops at the first comparison and every null is retained.

That null is informative, because it falls on the two hypotheses the screen was
built to test:

- **`lambda` has no measured effect on the mean.** `cmaes-ipop-l64` scores 1.07
  cost units *worse* than `cmaes-ipop` (`t` = -0.04); `cmaes-ipop-l20` scores
  11.97 better (`t` = +0.33). A sixteen-fold and a fifty-one-fold reduction in
  the initial population move the mean by less than a tenth of its own standard
  deviation.
- **Separable covariance alone does nothing.** `sep-cmaes-single` beats
  `cmaes-single` by 3.97 (`t` = +0.10). The +90.24 that `sep-cmaes-ipop` won in
  Phase 21 is not attributable to the covariance mode on its own.

What the screen did find, outside its registered design, is that `lambda` acts
on **variance** rather than on the mean. That is reported below as exploratory,
because no spread test was registered and none is quoted with a p-value.

Lower cost is better throughout.

## What was run

Reference `example/MayFly-512.png` at 512x512, blank-canvas cost
38732.12245178223. Eight circles in one batch (`mode: batch`, `batchSize` 8), a
56-dimension search. `optimizerEpochs` 1, one render thread, eight parallel
evaluation workers, CPU backend with the exact AVX2 compositor, ordinary stage
convergence disabled, trace and optimizer diagnostics on. Identical to the
Phase 21 campaign in every respect except the arm set.

| Arm | Covariance | Restarts | `lambda` | Iterations |
| --- | --- | --- | ---: | ---: |
| `cmaes-single` | full | none | 1024 | 6350 |
| `cmaes-ipop` | full | IPOP | 1024 | 6350 |
| `cmaes-ipop-l64` | full | IPOP | 64 | 101600 |
| `cmaes-ipop-l20` | full | IPOP | 20 | 325120 |
| `sep-cmaes-single` | separable | none | 1024 | 6350 |
| `sep-cmaes-ipop` | separable | IPOP | 1024 | 6350 |
| `sep-cmaes-ipop-l64` | separable | IPOP | 64 | 101600 |
| `sep-cmaes-ipop-l20` | separable | IPOP | 20 | 325120 |

**Every arm is evaluation-matched by construction, not by post-hoc truncation.**
`lambda` levels are admitted only if they divide the budget exactly, so
`iterations * lambda` = **6,502,400** in all eight rows — the same cap Phase 21
used, and the figure its 2048-iteration MayFly control consumed. Scoring is
still the post-hoc scan of `trace.jsonl` for the lowest cost at or below the
cap, so the two campaigns' rows are read the same way.

`lambda` = 20 is not 16. `app.MinPopulation` is 20, so 20 is the closest this
repository can currently get to Hansen's default without a limits change. That
change was deliberately not made in order to run this screen; see the
recommendation.

Block `b` uses seed prefix `111000 + b` in all eight arms, submission is
block-major, and the driver refuses any block count other than twelve, so every
paired test has `df = 11` and an uncorrected two-sided `t_crit` = 2.20.

**The screen makes thirteen contrasts and pays for all of them.** Seven arms
against the baseline `cmaes-single`, and six against the secondary control
`cmaes-ipop`. They are one family and are corrected together, so crossing two
factors bought four extra arms and a stricter bar at the same time. For
orientation, plain Bonferroni at df = 11 would put the threshold at `t` = 3.65.

## Result

| arm | mean | sd | median | best | gain vs `cmaes-single` | t (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| `cmaes-single` | 955.24 | 113.92 | 911.31 | 853.24 | control | control | control | control | control |
| `cmaes-ipop` | 892.39 | 95.65 | 875.09 | **774.65** | +62.85 | +3.13 | 0.00965 | retain | 8/12 |
| `cmaes-ipop-l64` | 893.46 | 29.40 | 898.20 | 839.87 | +61.78 | +1.72 | 0.11275 | retain | 7/12 |
| `cmaes-ipop-l20` | 880.43 | 44.95 | 884.64 | 800.84 | +74.82 | +1.75 | 0.10807 | retain | 8/12 |
| `sep-cmaes-single` | 951.27 | 81.36 | 923.59 | 849.75 | +3.97 | +0.10 | 0.92256 | retain | 6/12 |
| `sep-cmaes-ipop` | 871.13 | 48.39 | 858.81 | 825.28 | +84.11 | +2.39 | 0.03586 | retain | 10/12 |
| `sep-cmaes-ipop-l64` | 868.82 | 31.29 | 857.20 | 817.73 | +86.42 | +2.49 | 0.03001 | retain | 10/12 |
| `sep-cmaes-ipop-l20` | **849.47** | **27.11** | **837.17** | 815.63 | **+105.77** | **+3.44** | 0.00557 | retain | **11/12** |

Against the secondary control `cmaes-ipop`:

| arm | gain vs `cmaes-ipop` | t (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | --- | ---: |
| `cmaes-ipop-l64` | -1.07 | -0.04 | 0.97061 | retain | 4/12 |
| `cmaes-ipop-l20` | +11.97 | +0.33 | 0.74569 | retain | 6/12 |
| `sep-cmaes-single` | -58.88 | -1.50 | 0.16272 | retain | 2/12 |
| `sep-cmaes-ipop` | +21.27 | +0.78 | 0.45302 | retain | 7/12 |
| `sep-cmaes-ipop-l64` | +23.57 | +0.84 | 0.41711 | retain | 6/12 |
| `sep-cmaes-ipop-l20` | +42.92 | +1.72 | 0.11400 | retain | 9/12 |

Six of the seven arms beat the baseline on the mean and four clear the
uncorrected `t` = 2.20. None clears the corrected bar. **Read the table as
"nothing was established here", not as a ranking with weak support.**

### Per block

| block | `cmaes-single` | `cmaes-ipop` | `cmaes-ipop-l64` | `cmaes-ipop-l20` | `sep-cmaes-single` | `sep-cmaes-ipop` | `sep-cmaes-ipop-l64` | `sep-cmaes-ipop-l20` |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 911.22 | 857.46 | 899.95 | 890.14 | 1026.37 | 863.42 | 903.36 | **841.79** |
| 2 | 864.44 | 864.44 | 902.49 | 911.88 | 930.10 | 848.40 | 918.17 | **828.47** |
| 3 | 1021.83 | 887.93 | 912.45 | **800.84** | 928.81 | 845.08 | 900.92 | 832.27 |
| 4 | 1219.25 | 1158.14 | 909.31 | **828.77** | 906.50 | 875.39 | 884.67 | 884.43 |
| 5 | 956.19 | 956.19 | 861.19 | 895.16 | 1014.86 | 1014.86 | 845.84 | **832.55** |
| 6 | 1129.95 | 900.56 | 852.12 | 855.58 | 1042.78 | 845.38 | **842.55** | 867.30 |
| 7 | 883.56 | 820.92 | 893.12 | 884.29 | 849.75 | 849.75 | 860.93 | **815.63** |
| 8 | 869.06 | **774.65** | 896.45 | 931.42 | 1118.43 | 882.07 | 848.92 | 824.96 |
| 9 | 911.39 | 911.39 | 890.98 | **840.72** | 908.15 | 865.77 | 853.48 | 884.26 |
| 10 | 902.32 | 885.73 | 922.05 | 884.98 | 916.96 | 883.92 | 849.42 | **828.62** |
| 11 | 853.24 | 853.24 | 941.60 | 964.58 | 918.37 | **825.28** | 899.83 | 893.88 |
| 12 | 940.44 | 838.06 | 839.87 | 876.75 | 854.20 | 854.20 | **817.73** | 859.48 |
| **mean** | 955.24 | 892.39 | 893.46 | 880.43 | 951.27 | 871.13 | 868.82 | **849.47** |
| **sd** | 113.92 | 95.65 | 29.40 | 44.95 | 81.36 | 48.39 | 31.29 | **27.11** |

## Cross-campaign drift is zero

Three of the eight arms — `cmaes-single`, `cmaes-ipop` and `sep-cmaes-ipop` —
repeat Phase 21 exactly. They were bought so that drift between the two
campaigns would be measured rather than assumed, and the measurement is as clean
as it can be: **all thirty-six cells reproduce the committed
[`cmaes-measurement.csv`](cmaes-measurement.csv) values bit for bit**, to the
last digit of the float.

That holds across a different binary — the screen ran a build carrying the
raised iteration cap and bounded trace sampling, the `distributionExtent`
diagnostic, and the screen driver itself, none of which existed when Phase 21
ran — and across a different concurrency setting on the same 64-core host
(seven simultaneous jobs against six). So the parallel-evaluation path is
reproducible under load, and Phase 21's rows and this screen's rows may be
compared directly.

It also settles a question that was open while the screen was running: because
these code changes are search-neutral in fact and not merely by inspection, a
cell can be re-run under a later binary purely to harvest a diagnostic it did
not record, and the re-run reproduces the original trajectory rather than
replacing it.

## Mechanism: what `lambda` actually buys

The registered contrasts are null on the mean. The descriptive spread is not
subtle:

| arm | `lambda` | sd | range | worst block |
| --- | ---: | ---: | ---: | ---: |
| `cmaes-ipop` | 1024 | 95.65 | 383.50 | 1158.14 |
| `cmaes-ipop-l64` | 64 | 29.40 | 101.72 | 941.60 |
| `cmaes-ipop-l20` | 20 | 44.95 | 163.74 | 964.58 |
| `sep-cmaes-ipop` | 1024 | 48.39 | 189.58 | 1014.86 |
| `sep-cmaes-ipop-l64` | 64 | 31.29 | 100.44 | 918.17 |
| `sep-cmaes-ipop-l20` | 20 | 27.11 | **78.25** | **893.88** |

Variance ratios against the matching `lambda` = 1024 arm run 2.39x, 3.19x, 4.53x
and 10.59x, all four in the same direction. **This is exploratory.** No spread
test was registered, these ratios are quoted without p-values, and an F-ratio on
twelve blocks is a weak instrument in any case. It is reported because it
explains the mean table rather than competing with it: `cmaes-ipop` holds both
the single best result of the entire screen (774.65, block 8) and its second
worst (1158.14, block 4, behind only `cmaes-single`'s 1219.25 in the same
block). A large initial population buys lottery tickets. The
mean is unmoved because the tail pays for the wins.

The reason is countable. `cmaes-ipop` finished **exactly** equal to its own
no-restart counterpart `cmaes-single` in four of twelve blocks (2, 5, 9, 11),
and `sep-cmaes-ipop` did the same in three (5, 7, 12). An IPOP schedule's first
run is the no-restart run at the same seed, so an identical cost to the last
digit means the entire restart ladder returned nothing. Seven of twenty-four
`lambda` = 1024 IPOP runs were, in effect, no-restart runs that spent the rest of
the budget confirming it — because doubling from 1024 to 2048 leaves the shared
6,502,400-evaluation budget room for barely one restart.

**The small-`lambda` arms have no no-restart counterpart in this design**, so the
same statistic cannot be computed for them. That is a gap in the arm set, not a
finding; a future screen that wants to make the restart-count argument
quantitatively needs `cmaes-single-l64` and `cmaes-single-l20` cells.

## Where the budget went

| arm | mean final evaluations | % of cap | mean % spent after last improvement |
| --- | ---: | ---: | ---: |
| `cmaes-single` | 1,783,384 | 27.4% | 21.0% |
| `cmaes-ipop` | 6,502,403 | 100.0% | 46.1% |
| `cmaes-ipop-l64` | 6,502,403 | 100.0% | 46.2% |
| `cmaes-ipop-l20` | 6,502,403 | 100.0% | 30.2% |
| `sep-cmaes-single` | 1,048,408 | 16.1% | 15.9% |
| `sep-cmaes-ipop` | 6,502,403 | 100.0% | 41.1% |
| `sep-cmaes-ipop-l64` | 6,502,403 | 100.0% | 40.4% |
| `sep-cmaes-ipop-l20` | 6,502,403 | 100.0% | **56.7%** |

Phase 21 reported roughly 40% waste on its two IPOP arms. Six IPOP arms at three
`lambda` levels confirm it and widen the range: **30% to 57% of every restart
arm's budget falls after its last improvement.** The best-scoring arm in the
screen is also the most wasteful one.

The non-restarting arms stop early on `TolFun` — at 27.4% and 16.1% of cap — and
their waste is correspondingly small. The contrast is the whole of the argument
for the open box of Task 23.1: a restart schedule with no stagnation criterion
cannot end a run that has stopped progressing and hand its budget to the next
restart, so it holds the budget open and spends it on nothing. Neither this
screen nor Phase 21 carries the per-restart records that would price that waste
per run rather than per job; both were run before the adapter wrote them.

## The recorded sigma is settled, by measurement

Phase 21 argued that its 1e43 sigma readings were a gauge artefact rather than a
diverged search, and flagged that argument as inference because the identifiable
quantity had never been recorded. It is recorded now.
`SearchDiagnostics.DistributionExtent` carries `sigma * max(D)`, and this screen
is the first campaign to emit it. Across all **17,593** trajectory samples:

| quantity | min | max |
| --- | ---: | ---: |
| `sigma` | 7.801e-09 | **4.149e+43** |
| `sigma * max(D)` | 2.278e-08 | **1.516** |

Sigma spans fifty-two orders of magnitude. The identifiable extent of the
sampling distribution never exceeds 1.52 and is usually between 1e-3 and 1e-1.
Block 1 of `sep-cmaes-single` makes the compensation explicit: sigma rises from
0.287 to 6.837e+15 while the extent *falls* from 0.087 to 0.0022, and the
incumbent improves throughout. The covariance matrix deflates by exactly what
sigma inflates by, because go-cma-es does not renormalize `C`.

**A large recorded sigma is not evidence of anything.** Separable mode reaches
4.149e+43 and full covariance only 4.055e+07, so the difference between the two
columns is a representation difference, not a stability difference. Cite
`distributionExtent`; do not cite sigma.

## What this does not establish

- **Nothing, inferentially.** Every one of the thirteen contrasts retains its
  null. `sep-cmaes-ipop-l20` has the best mean, the lowest standard deviation
  and eleven of twelve blocks, and none of that is licence to call it the
  winner or to change a default.
- **A null is not equivalence.** The screen bounds the `lambda` effect on the
  mean loosely, not tightly: the `cmaes-ipop-l20` contrast has a standard error
  of about 36 cost units, so an effect would have had to reach roughly 80 units
  to clear even the uncorrected `t` = 2.20, and a true effect of that size
  would have been detected only about half the time. "No measured effect at
  twelve blocks" is the claim; "no effect" is not.
- **`lambda` = 16 was not tested.** `app.MinPopulation` is 20. The screen tests
  20, which is close to Hansen's 16 and nowhere near 1024, so it speaks to the
  order of magnitude and not to the exact default.
- **The variance finding is unregistered.** It emerged from the data, it has no
  pre-declared test, and it should be confirmed by a design that registers one
  before it is treated as a result.
- **One fixture.** Eight circles on one 512x512 reference, as with everything
  else measured on this problem. Task 23.4 remains open.
- **MayFly is not in this screen.** Its baseline is `cmaes-single`. Nothing here
  revises the Phase 21 comparison against MayFly in either direction.

## Recommendation

**Change no default.** The screen was run to test two candidate explanations for
Phase 21's result and both came back null, which is a reason to leave the
configuration alone rather than to adjust it toward the best-scoring cell.

Specifically:

- **Leave `lambda` pinned to `popSize`.** The worry that 1024 against Hansen's 16
  was costing quality is not supported: a fifty-one-fold reduction moved the mean
  by `t` = 0.33. Decoupling `lambda` from `popSize` is still defensible as a
  surface-completeness change, but it should not be justified by a measured gain,
  because there isn't one.
- **Do not lower `app.MinPopulation` to 16.** The open box in Task 23.3 asked
  whether the limit should reach Hansen's default. Nothing measured here wants
  it: `lambda` = 20 and `lambda` = 64 are indistinguishable from `lambda` = 1024
  on the mean, so 16 has no case that 20 does not already fail to make.
- **Do not read `sep-*` as the recommended covariance mode on its own.**
  `sep-cmaes-single` is a null against `cmaes-single`. The Phase 21 winner's
  advantage, whatever its size, is not attributable to the covariance mode
  independently of the restart strategy.
- **The stagnation criterion is now the highest-value open question.** It is the
  one intervention with a large, measured, consistent quantity behind it — 30%
  to 57% of six arms' budgets, across three `lambda` levels — rather than a
  difference that vanishes under correction. It is a behaviour change for every
  existing CMA-ES restart configuration and needs its own registered campaign,
  which should carry the per-restart records neither this screen nor Phase 21
  has.

## Reproduction and raw data

Raw per-job costs are in
[`cmaes-lambda-measurement.csv`](cmaes-lambda-measurement.csv), one row per arm
and block, with the evaluation counts each score was taken at. Mechanism
trajectories are in
[`cmaes-lambda-trajectories.csv`](cmaes-lambda-trajectories.csv): 17,593 samples
carrying `bestCost`, `populationSpread`, `sigma`, `conditionNumber` and
`distributionExtent` against cumulative iterations and evaluations.

The design is registered in code and cannot be varied by flag. To see it:

```sh
go run ./scripts/cmaes-measurement -action plan -design lambda
```

To reproduce the tables above from the committed data:

```sh
go run ./scripts/cmaes-measurement -action analyze -design lambda \
  -results docs/cmaes-lambda-measurement.csv \
  -trajectories docs/cmaes-lambda-trajectories.csv
```

The campaign itself was submitted to a `serve` instance on a 64-core host at
seven concurrent jobs and ran from 2026-08-29 00:00 to 06:11 CEST, all
ninety-six jobs completing. The variance ratios, tie counts and budget-waste
percentages in this report are computed from the two CSVs above and are not
emitted by `-action analyze`.
