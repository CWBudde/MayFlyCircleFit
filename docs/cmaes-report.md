# CMA-ES measured against MayFly: the complete twelve-block campaign

This is the registered Phase 21 experiment, run to completion. Five arms, twelve
paired seed blocks, sixty jobs, one shared evaluation budget. It replaces
[`cmaes-preliminary-report.md`](cmaes-preliminary-report.md), whose single block
was descriptive only; that report stays in the tree because one of its
conclusions does not survive twelve blocks, and it is worth knowing which.

The headline: **separable CMA-ES with IPOP restarts beat MayFly's single long
run in all twelve blocks and MayFly's sixteen-restart arm in eleven**, by 210.97
and 90.24 cost units respectively. Both clear the `t = 2.20` the design
registered, and both still clear it once corrected for the seven contrasts this
report makes — a correction only one other contrast survives. Lower cost is
better throughout.

The result comes with a caveat attached. Roughly 40% of each IPOP arm's budget
produced no improvement, because no stagnation criterion was configured and a
restart schedule without one cannot end a dead run and reallocate its budget.
The measured gain is therefore real but is a **lower bound on a properly
configured IPOP**, not a clean reading of one.

## What was run

Reference `example/MayFly-512.png` at 512x512, blank-canvas cost
38732.12245178223. Eight circles in one batch (`mode: batch`, `batchSize` 8), a
56-dimension search. Population 1024, `optimizerEpochs` 1, one render thread,
eight parallel evaluation workers, CPU backend with the exact AVX2 compositor,
ordinary stage convergence disabled, trace and optimizer diagnostics on.

| Arm | Optimizer | Shape |
| --- | --- | --- |
| `mayfly-single` | MayFly `standard` | 1 x 2048 iterations |
| `mayfly-r16` | MayFly `standard` | 16 x 128 iterations, cold |
| `cmaes-single` | CMA-ES, full covariance, no restart | up to 6350 generations |
| `cmaes-ipop` | CMA-ES, full covariance, IPOP | 6350 generations, shared budget |
| `sep-cmaes-ipop` | CMA-ES, separable covariance, IPOP | 6350 generations, shared budget |

**The comparison is evaluation-matched, not iteration-matched**, and that is the
whole of its fairness. MayFly at population 1024 spends about 3,175 evaluations
per iteration; CMA-ES spends exactly `lambda` = 1024 per generation. Matching
iterations would have handed MayFly roughly three times the work. Every arm is
instead scored by scanning `trace.jsonl` for the lowest cost recorded at or
below **6,502,400 optimizer evaluations**, the figure the 2048-iteration MayFly
control consumes. An arm that overshoots the cap stays cap-valid because the
score ignores everything past it.

Block `b` uses seed prefix `111000 + b` in all five arms; the twelve prefixes are
disjoint and each restart implementation derives its attempt seeds from only that
block's prefix. Submission is block-major. The driver refuses any block count
other than twelve, so every paired test has `df = 11` and an uncorrected
two-sided `t_crit = 2.20`.

**That threshold is per contrast, and this report makes seven.** The design
registered paired t-tests but did not name a primary contrast, so nothing here
is entitled to spend α = 0.05 seven times over. Every contrast below is
therefore reported with its two-sided p-value and with the decision Holm's
step-down procedure reaches at a family-wise α = 0.05 across all seven. Holm is
the correction the analysis script applies; for orientation, plain Bonferroni
would put the threshold at `t = 3.29`.

Binary built from CircleFit commit `0257b04`, MayFly `v0.7.1`, go-cma-es
`v0.1.0`; all sixty rows record those versions. Host was a 64-core Linux x86-64
machine admitting six jobs concurrently at eight evaluation workers each, 48
cores in use. All sixty jobs terminated `completed`. Wall clock 4.9 hours for
28.5 optimizer-hours of work.

**Confirmation check.** Block 1's seed 111001 is the seed the stopped
preliminary campaign used. All three arms it had recorded reproduced to the
digit: `mayfly-single` 1163.0034344991047, `mayfly-r16` 959.2622375488281,
`cmaes-single` 911.2223281860352. The Phase 11 GPU work left the CPU path
byte-identical.

## Result

| arm | mean | sd | median | best | gain vs `mayfly-single` | t (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| `mayfly-single` | 1082.10 | 164.08 | 1100.31 | 864.57 | control | control | control | control | control |
| `mayfly-r16` | 961.37 | 30.62 | 959.43 | 923.97 | +120.73 | +2.42 | 0.03385 | retain | 8/12 |
| `cmaes-single` | 955.24 | 113.92 | 911.31 | 853.24 | +126.86 | +2.01 | 0.06960 | retain | 9/12 |
| `cmaes-ipop` | 892.39 | 95.65 | 875.09 | 774.65 | +189.71 | +3.29 | 0.00716 | **reject** | 11/12 |
| `sep-cmaes-ipop` | 871.13 | 48.39 | 858.81 | 825.28 | **+210.97** | **+5.04** | **0.00038** | **reject** | **12/12** |

Against `mayfly-r16`, the stronger of the two MayFly allocations:

| arm | gain vs `mayfly-r16` | t (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | --- | ---: |
| `cmaes-single` | +6.13 | +0.18 | 0.85909 | retain | 9/12 |
| `cmaes-ipop` | +68.98 | +2.40 | 0.03546 | retain | 10/12 |
| `sep-cmaes-ipop` | +90.24 | +4.87 | 0.00049 | **reject** | 11/12 |

`Holm` is the decision over all seven contrasts jointly at a family-wise
α = 0.05: `reject` rejects the null, so the arm's advantage stands under the
correction, and `retain` leaves the null in place. Three of the seven reject —
both `sep-cmaes-ipop` contrasts and `cmaes-ipop` against the control — and four
retain. Two of those four clear the uncorrected 2.20 first and lose it to the
correction, `mayfly-r16` at p = 0.034 and `cmaes-ipop` against r16 at p = 0.035;
both are flagged below wherever they are read.

Four readings, in decreasing order of confidence.

**Separable CMA-ES with IPOP is the best configuration measured on this
problem.** Twelve of twelve against the control and eleven of twelve against
r16, at `t` values no plausible reading of twelve blocks overturns: it is the
only arm whose two contrasts both survive the seven-way correction, and it
survives with an order of magnitude to spare (p = 0.00038 and 0.00049 against a
step-down threshold of 0.00714 and 0.00833). It also has the second-lowest
spread in the table (sd 48.39 against the control's 164.08), so it is both
better and steadier.

**`cmaes-single` is a tie with `mayfly-r16`, not a win.** Its `t = +2.01`
against the control falls short of even the uncorrected 2.20, and against r16 it
is `+0.18` — noise. Reading its 9/12 as a win would be exactly the error the
paired test exists to prevent. What it *does* establish is efficiency: it stopped itself on `TolFun`
after a mean 1,783,384 evaluations, **27.4% of the cap**, and 1.94 hours against
r16's 7.76. Equal quality for a quarter of the work is a real result even though
equal quality is a null one.

**Restarts still beat one long run for MayFly — supported on the current pin,
but not confirmed at this report's own standard.** `mayfly-r16` gains +120.73 at
`t = +2.42`, p = 0.034. That clears the uncorrected 2.20 and does not survive
the seven-way correction, whose step-down threshold for it is 0.0125, so it is a
directionally consistent result rather than a family-wise significant one.
[`restart-vs-budget-report.md`](restart-vs-budget-report.md) established the
finding under MayFly v0.5.1 and it has never been re-measured since; v0.7.0
changed results for every variant, so that conclusion had been carrying an open
version caveat. This campaign narrows the caveat rather than closing it: the
effect reappears on v0.7.1 with the same sign and a comparable magnitude, and a
design aimed at *that* question — one preregistered contrast rather than seven —
would settle it. Its variance claim replicates independently of any threshold:
sd falls 164.08 to 30.62.

**Predictability and quality rank differently.** `mayfly-r16` has the *lowest*
spread of any arm, 30.62, while sitting 90 cost units behind on the mean. It is
the arm to choose if a caller needs a narrow distribution more than a good
answer; it is not the arm to choose otherwise.

### Per block

| block | `mayfly-single` | `mayfly-r16` | `cmaes-single` | `cmaes-ipop` | `sep-cmaes-ipop` | winner |
| ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 1 | 1163.00 | 959.26 | 911.22 | **857.46** | 863.42 | `cmaes-ipop` |
| 2 | 864.57 | 970.44 | 864.44 | 864.44 | **848.40** | `sep-cmaes-ipop` |
| 3 | 914.74 | 1035.06 | 1021.83 | 887.93 | **845.08** | `sep-cmaes-ipop` |
| 4 | 908.07 | 965.64 | 1219.25 | 1158.14 | **875.39** | `sep-cmaes-ipop` |
| 5 | 1319.94 | **925.05** | 956.19 | 956.19 | 1014.86 | `mayfly-r16` |
| 6 | 1042.06 | 923.97 | 1129.95 | 900.56 | **845.38** | `sep-cmaes-ipop` |
| 7 | 1158.55 | 957.51 | 883.56 | **820.92** | 849.75 | `cmaes-ipop` |
| 8 | 968.99 | 955.87 | 869.06 | **774.65** | 882.07 | `cmaes-ipop` |
| 9 | 1255.10 | 984.64 | 911.39 | 911.39 | **865.77** | `sep-cmaes-ipop` |
| 10 | 1208.95 | 974.19 | 902.32 | 885.73 | **883.92** | `sep-cmaes-ipop` |
| 11 | 915.98 | 925.20 | 853.24 | 853.24 | **825.28** | `sep-cmaes-ipop` |
| 12 | 1265.27 | 959.60 | 940.44 | **838.06** | 854.20 | `cmaes-ipop` |

Block wins: `sep-cmaes-ipop` 7, `cmaes-ipop` 4, `mayfly-r16` 1.

Two things the aggregate hides. Full-covariance CMA-ES fails badly and
specifically: blocks 4 and 6 put `cmaes-single` at 1219.25 and 1129.95, well
above the MayFly control on the same seed, which is where its 113.92 standard
deviation comes from. And in blocks 2, 5, 9 and 11 `cmaes-ipop` scores exactly
what `cmaes-single` scores — a third of the time IPOP's restarts never improved
on what the first run already had.

**Lowest cost recorded: 774.65**, `cmaes-ipop` block 8. For scale, the two
figures [`restart-vs-budget-report.md`](restart-vs-budget-report.md) records for
this fixture are 781.86 for a base stage and 752.92 with polishing at roughly
sixteen times the compute. Read that as scale and not as a result, because the
comparison is only as good as the assumption that both numbers score the same
function, and that assumption is verified in part:

- **Verified.** The reference, canvas and cost kernel agree. Today's
  blank-canvas cost is 38732.12245178223, which is the 38732.12 the older
  reports quote.
- **Not verified.** A blank canvas exercises no compositing, so that figure says
  nothing about the circle rendering that actually turns a parameter vector into
  a 774.65 or a 781.86. The byte-exact parity contract in
  [`renderer-correctness.md`](renderer-correctness.md) is what is supposed to
  keep that path fixed across every change since v0.5.1, and a contract is not a
  measurement. No v0.5.1-era configuration was re-run on the current pin, and
  this campaign's own reproduction check reaches back only to the stopped
  preliminary campaign, not to that report.

So this is not a record claim, and it is emphatically not a comparison of
optimizer versions — AGENTS.md forbids that reading of any pre-v0.7 figure and
it is the right prohibition. Establishing that 774.65 is the best cost this
project has reached needs the old configuration re-run on the current pin, which
this campaign did not do.

## Mechanism

Both optimizers were instrumented. MayFly reports the RMS pairwise population
spread in normalized parameter space; CMA-ES reports sigma and the covariance
condition number.

**MayFly's population collapses early, in every block.** Spread starts near 2.0
and falls below 10% of its iteration-1 value at iteration 32 to 56 across the
twelve blocks, median 40 — inside the first 2% of a 2048-iteration budget. That
is the collapse the restart report described under v0.5.1, reproduced here under
v0.7.1 with twelve seeds instead of one, and a little later than the 11-16
iterations measured then.

**The preliminary report over-read what that collapse means, and this is the
correction.** From seed 111001 alone it concluded MayFly "spent 75% of its
iteration budget to improve by only 0.30 cost points". Across twelve blocks the
mean improvement after iteration 64 — long after spread has collapsed — is
**13.6%**, ranging from 3.5% to 33.2%. Seed 111001 sat at the bottom of that
range. So low RMS pairwise spread does not by itself mean the search has
stopped: a tight population still descends, sometimes a long way. The
diagnostic measures diversity, and diversity is not progress. The restart
report's *recommendation* survives — restarts win, and this campaign confirms it
independently — but the specific "collapsed therefore frozen" mechanism claim
does not, and should not be repeated from that one seed.

**CMA-ES learns an anisotropic metric instead.** The covariance condition number
starts near 1.1 and reaches 4.2e10 to 1.4e12 across the `cmaes-single` blocks.
That is the intended behaviour and is what distinguishes the two optimizers:
MayFly contracts an isotropic population toward a point, CMA-ES reshapes the
distribution while keeping its step size adaptive. Full-covariance single runs
stop themselves on `TolFun` at 1,408 to 2,471 generations, having peaked at
modest step sizes — max sigma per block 1.46 to 161.

## Where the IPOP budget went

Both IPOP arms reach their best score around the middle of their run and
improve on it never again. The two arms peak at a mean 3,502,762 and 3,828,309
evaluations of the 6,502,400 available: **46% and 41% of those runs produced no
further improvement.** The comparable figure for `cmaes-single`, which stops
itself on `TolFun`, is 21%.

**No stagnation criterion was configured**, and that is the mechanism. The
campaign set none of the `stop*` fields, so `Stop.enabled()` is false and
`config.Convergence.StagnationIterations` is never armed. A restart schedule
without a stagnation guard has no way to end a run that has stopped making
progress, so a dead run holds the shared budget until it is spent instead of
handing it to the next restart. `cmaes-single` does not have this problem
because a single run that reaches `MaxIterations` simply ends.

That is a configuration finding, not a library defect, and it is cheap to act
on: setting `stopStagnationIters` on a restart arm converts wasted budget into
additional restarts.

### What the recorded sigma does and does not show

The trajectories record a striking number, and it is worth writing down why it
is *not* the finding, because the obvious reading of it is wrong.

| arm | max sigma per block | blocks with max sigma > 1e3 |
| --- | --- | ---: |
| `cmaes-single` | 1.46 – 161 (median 8.3) | 0/12 |
| `cmaes-ipop` | 2.3e3 – 4.1e7 (median 4.0e4) | 12/12 |
| `sep-cmaes-ipop` | 4.3e16 – 4.2e43 (median 1.3e23) | 12/12 |

A step size of 1e23 in a normalized unit box looks like a diverged search. It
is not, and the campaign's own data says so: in the block-1 `cmaes-ipop` trace,
sigma rises 242-fold across one restart (0.346 to 83.8) **while the incumbent
improves**, 911.22 to 875.63. A sampling radius that had genuinely grown
242-fold in a unit box could not still be refining a fraction-of-a-percent
improvement.

The reason is that **sigma alone is not an identifiable quantity.** CMA-ES
identifies only `sigma^2 * C`; the split between the scalar and the matrix is a
gauge freedom, and go-cma-es does not renormalize `C`. Sigma can therefore
inflate by many orders of magnitude while the distribution's axis lengths
deflate by the same factor, leaving the actual sampling extent unchanged.
That is exactly what the library's `TolXUp` guard measures — `sigma * max(D)`,
not sigma — which is why it never fired and was right not to.

Separable mode drifts furthest because it has the least to stop it. Every
Hansen criterion here is gauge-invariant except the condition-number bound, and
in separable mode the diagonal *is* the whole covariance, so a uniform shrink
leaves the condition number at 1 and passes unseen. In full covariance the same
drift instead spreads the eigenspectrum, which the spectrum-sensitive criteria
eventually catch — which is why full-covariance sigma tops out around 1e7 and
separable reaches 1e43.

**The identifiable quantity was not recorded, so this account is inference, not
measurement.** `SearchDiagnostics` keeps sigma and the condition number and
drops the distribution's eigenvalues, so `sigma * max(D)` cannot be recovered
from these traces. Recording it is the first thing the follow-up work should
do; until then, a large recorded sigma on this problem should be read as an
unmeasured gauge, not as evidence of anything.

### Termination is uninformative for the restart arms

All sixty jobs record `completed`. For an IPOP arm that is structurally
guaranteed rather than observed: the library's restart driver overwrites the
schedule-level reason with its max-evaluations reason whenever the budget is
spent, and the adapter maps that to `completed`. Per-restart termination
reasons exist in the library and are discarded by the adapter, so nothing in a
checkpoint distinguishes a run that converged from one that stagnated. The
opt-in trajectory trace was the only reason any of this was visible.

## What this does not establish

- **One image, one circle count, one stage.** Eight circles jointly on a single
  512x512 reference. Nothing here speaks to other references, other circle
  counts, sequential or joint mode, or later stages of a schedule.
- **Nothing about polishing.** Polishing remains MayFly-only regardless of what
  the base stage used.
- **`sep-cmaes-ipop` confounds two changes.** It varies covariance mode *and*
  restart strategy against `cmaes-single`, and the design has no
  separable-without-restarts arm. Whether separability, IPOP, or their
  interaction produces the +90.24 is unanswered. Given that separable is the
  winner, this is now the most important gap in the design.
- **`lambda` was never varied.** The adapter sets `Lambda = popSize` and
  `Mu = popSize/2`, so every CMA-ES arm ran `lambda = 1024`. Hansen's default
  for 56 dimensions is `4 + floor(3 ln 56)` = **16**, sixty-four times smaller.
  CMA-ES won at 64x its own recommended population; whether it wins by more at a
  sane one is unmeasured, and a smaller `lambda` converts the same budget into
  far more generations of metric learning. This is the single most promising
  untested knob.
- **The IPOP arms were under-configured.** Both won decisively while roughly
  40% of their budget produced nothing, for want of a stagnation criterion the
  design never set. What a properly configured IPOP scores here is unmeasured
  and can only be higher, so the gains above are a floor.
- **The recorded sigma establishes nothing.** Sigma alone is gauge-dependent
  and the identifiable quantity, `sigma * max(D)`, was not recorded. Do not
  cite the sigma column of `cmaes-trajectories.csv` as evidence of anything
  until the diagnostics carry the eigenvalues.
- **The IPOP arms and the MayFly arms do not fail the same way.** `mayfly-r16`
  also reaches its best score early — a mean 1,903,641 evaluations, 71% of the
  run following it. For r16 that is the design working as intended: the later
  restarts are independent attempts that happened not to win. For a diverged
  IPOP restart it is budget spent on a distribution that cannot win. The two
  numbers look alike and mean opposite things.
- **Seven contrasts, no preregistered primary one.** The design registered
  paired t-tests without naming which contrast the campaign existed to settle,
  so every one of them is corrected together and each carries a step-down
  threshold well inside 0.05. That is the conservative choice and it costs two
  results, `mayfly-r16` and `cmaes-ipop`-against-r16, both of which are
  directionally clear and neither of which this design is entitled to call
  significant. A follow-up should name its primary contrast in advance.
- **No comparison to a pre-v0.7 figure survives.** See the record paragraph
  above: the objective's blank-canvas cost is unchanged, the compositing path is
  covered by a contract rather than a re-measurement, and nothing here re-ran an
  older configuration.
- **Trace diagnostics add observation overhead.** The recorded intervals are
  operational records, not throughput benchmarks.

## Recommendation

For an eight-circle 512x512 base stage on the current pins, **separable CMA-ES
with IPOP restarts is the configuration to use**, and the evidence for that is
as strong as this project has produced for any optimizer choice: twelve blocks,
twelve wins, `t = +5.04`, and the only contrasts in the campaign that survive
its correction for seven comparisons.

That is not yet an argument for changing any default. The result is one fixture,
the winning arm confounds two variables, and every restart arm ran without the
stagnation criterion that would let it use its whole budget. The order of work
that would turn it into one:

1. Record `sigma * max(D)` and the per-restart termination reason, so the
   distribution's actual extent and the reason each restart ended are
   measurable rather than inferred.
2. Re-run the IPOP arms with a stagnation criterion set, so a dead run
   releases its budget to the next restart instead of holding it.
3. Add the `sep-cmaes-single` arm the design is missing, and separate
   covariance mode from restart strategy.
4. Screen `lambda` — the campaign's 1024 against values near Hansen's 16.
   `app.MinPopulation` is 20, so 16 itself is currently unreachable, and
   `app.MaxIterations` has to admit the generation count a small `lambda` needs
   to reach the cap.
5. Only then, a second fixture.

## Reproduction and raw data

- [`cmaes-measurement.csv`](cmaes-measurement.csv) — sixty rows: arm, block,
  seed, job id, state, termination, optimizer version, best cost, scored and
  final evaluation counts, iterations, elapsed seconds.
- [`cmaes-trajectories.csv`](cmaes-trajectories.csv) — downsampled mechanism
  traces to the common cap: population spread for the MayFly arms, sigma and
  condition number for the CMA-ES arms.

The result table reproduces from the first CSV alone, with no server and no
access to the campaign host:

```sh
go run ./scripts/cmaes-measurement -action analyze -results docs/cmaes-measurement.csv
```

Full-resolution traces and checkpoints, including `bestParams` for the 774.65
run, are archived outside the repository; the campaign host was reclaimed after
collection.
