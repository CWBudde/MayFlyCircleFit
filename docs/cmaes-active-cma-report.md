# The active-CMA campaign

**`activeCMA` is finally measured, and the registered contrast is a null.**
Turning active adaptation off costs 23.79 on the mean and loses 8 of 12 paired
blocks, at `t = -1.70` and `p = 0.117` — which does not clear the unadjusted
0.05 that a single-contrast family makes the whole gate. The direction favours
the knob; the evidence does not establish it.

What the campaign does establish is that the knob is *live*. Two campaigns in a
row failed to measure it — the deep hunt lost its arm to ten cancelled jobs, and
the covariance campaign's secondary contrast returned costs **bit-identical** to
its control in all twelve blocks because `go-cma-es v0.1.0`'s rank-mu clamp makes
active adaptation arithmetically inert wherever it binds. Here every block
differs, in both directions, by up to 90.38. The rung the design was built
around does what the driver's guard predicted, so the null is a measurement of
`activeCMA` rather than a measurement of the clamp. See
[`cmaes-covariance-report.md`](cmaes-covariance-report.md) for the defect
and for the two campaigns this one exists to repair.

## Conditions

| | |
| --- | --- |
| binary | `efda9a009b59a7bac13a141027d2a1f540a004d7` — the commit the design was registered at and submitted from |
| optimizer | `github.com/CWBudde/go-cma-es v0.1.0` (every arm is CMA-ES; no MayFly code runs in this design) |
| fixture | `example/MayFly-512.png`, md5 `76c44ab079154956dfadd481b08204a9`, 8 circles in one batch, 56 dimensions |
| budget | `defaultBudget` = 6,502,400 evaluations, nominal, per arm |
| backend | `cpu`, `evaluationWorkers: 8` |
| host | the 64-core campaign host at `--max-jobs 7` |
| dates | submitted 2026-09-02 23:32:48, finished 2026-09-03 00:48:58 — 01:16 of wall clock, 28,876 job-seconds (8.0 job-hours) |
| seeds | 111013-111024, twelve paired blocks |
| jobs | 24 of 24 completed; none failed, none cancelled |

**These costs may be compared against an otherwise matching campaign that ran
at `defaultBudget`**, and that is deliberate. `defaultBudget` is necessary and
not sufficient: a comparison also needs the same fixture, the same circle count
and the same backend, so these rows may be read against the restart ladder's and
must not be read against the budget-split campaign's, which fits
`example/Ref-512.png` at twelve circles on the same cap. The deep hunt and the
covariance campaign are excluded for the other reason — both ran at 1.94x the
shared cap so an IPOP ladder could finish its top rung. This design runs no
ladder, so it stays at the fixed cap, and staying there is what lets its rows be
read against the ladder's.

## The design

| arm | covariance | restarts | lambda | iters | active |
| --- | --- | --- | ---: | ---: | --- |
| `blk-r32-l64` (control) | block | 32 cold | 64 | 3175 | on |
| `blk-r32-l64-passive` | block | 32 cold | 64 | 3175 | **off** |

Two arms, one registered contrast, and it is primary — so Holm's first gate is
the unadjusted 0.05. Every choice follows from the two failures the campaign
repairs, and the reasoning is in
[`scripts/cmaes-measurement/README.md`](../scripts/cmaes-measurement/README.md)
rather than repeated here. In short: **block** covariance because it is the mode
a default would name and the only shippable one clean at a usable `lambda`;
**cold restarts** because an IPOP ladder doubles its population and would be
inert on every rung above the first; and **`lambda` 64** because the knob's
entire magnitude is the `negativeMass` scaling its negative weights, and that
mass collapses with `lambda` long before the clamp binds — 0.281 at 64, 0.0554
at 256, 0.00155 at the shipped `popSize` of 1024. A campaign at the default
population would have been formally live and still applied a treatment 180 times
smaller.

`activeCMAArms` computes that mass before it registers the arms and refuses a
rung below the floor, so a future edit cannot quietly reproduce the void.

## The registered result

| arm | mean | sd | median | best | gain vs `blk-r32-l64` | `t` (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| `blk-r32-l64` | 875.19 | 40.50 | 894.31 | 810.73 | control | control | control | control | control |
| `blk-r32-l64-passive` | 898.98 | 25.68 | 900.37 | 840.01 | -23.79 | -1.70 | 0.11687 | retain | 4/12 |

Holm step-down over the single registered contrast at a family-wise alpha of
0.05; with one contrast the corrected threshold is the uncorrected one, `t` =
2.20 at df = 11. Lower cost is better.

**The sign convention reads backwards in this design and is worth stating
plainly.** The driver's gain is control minus candidate, and here the
*candidate* is the arm with the knob switched **off**. So the negative gain
above means the passive arm is *worse* by 23.79 — active adaptation is ahead —
and the "blocks won" column counts the four blocks in which switching it off
helped. In the per-block table below the difference is given in the readable
direction instead.

### Per block

| block | seed | active | passive | passive - active | active evals | passive evals |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 111013 | 905.69 | 943.48 | +37.79 | 2,538,499 | 2,732,099 |
| 2 | 111014 | 913.80 | 904.15 | -9.66 | 2,541,187 | 2,644,611 |
| 3 | 111015 | 853.88 | 916.77 | +62.89 | 2,599,171 | 2,546,499 |
| 4 | 111016 | 899.88 | 917.01 | +17.13 | 2,435,587 | 2,876,355 |
| 5 | 111017 | 898.02 | 892.43 | -5.59 | 2,541,571 | 2,771,331 |
| 6 | 111018 | 891.04 | 917.81 | +26.77 | 2,348,099 | 2,799,939 |
| 7 | 111019 | 816.56 | 894.36 | +77.80 | 2,693,123 | 2,604,035 |
| 8 | 111020 | **810.73** | 901.11 | +90.38 | 2,577,923 | 2,633,091 |
| 9 | 111021 | 918.99 | 840.01 | **-78.97** | 2,433,731 | 2,776,643 |
| 10 | 111022 | 897.59 | 877.01 | -20.58 | 2,403,011 | 2,823,427 |
| 11 | 111023 | 814.17 | 884.02 | +69.85 | 2,850,307 | 2,617,667 |
| 12 | 111024 | 881.97 | 899.63 | +17.67 | 2,439,683 | 2,625,987 |

A positive difference means active adaptation won that block. The paired
difference has a mean of **+23.79** and a standard deviation of **48.43** —
twice the effect it surrounds — and it reverses in four blocks, once by 78.97.

## The knob is live, and the answer is a null

The covariance campaign's secondary contrast was **void, not null**: its two
arms returned the same bits, so there was nothing to interpret. The distinction
matters here because this campaign's headline looks similar from a distance — a
retained contrast on a knob that was supposed to do something — and it is a
different result entirely.

Every one of the twelve blocks separates. The spread runs from -78.97 to +90.38
and the arms diverge in their evaluation counts, their iteration counts and the
point at which each reaches its own best. Active adaptation is doing arithmetic
work at this rung; the campaign measured its effect on the fit and found that
effect indistinguishable from zero at twelve blocks against a paired standard
deviation of 48.43.

That is absence of evidence, not evidence of absence, and the interval says how
little the campaign narrows the question: the 95% paired interval on the
difference runs from **-6.98 to +54.56** cost units. It contains zero, so the
contrast retains; it also contains a benefit more than twice the point estimate,
so nothing here bounds the knob to a modest gain. No equivalence margin was
registered, and a retained two-sided test could not have established one. An
effect of the size the mean suggests would need roughly four times the blocks to
separate at this variance. **The honest reading is that this campaign does not
establish what `activeCMA` is worth on this problem**, and that nothing here
recommends changing its default either way.

## The spend reading, which the design registered in advance

The design is **cap-matched, not spend-matched**, because `WithRestarts` runs a
fixed count of attempts and consults no evaluation budget: a run that trips
`TolFun` early returns its remainder to nobody. That is the open problem
`PLAN.md`'s restart-ladder box records, inherited rather than dodged. So
`finalEvaluations` has to be read per arm and never taken as the cap.

| arm | mean `finalEvaluations` | % of the 6,502,400 cap | range | mean iterations (ceiling 101,600) |
| --- | ---: | ---: | --- | ---: |
| `blk-r32-l64` | 2,533,491 | **38.96%** | 36.11-43.83% | 39,586 |
| `blk-r32-l64-passive` | 2,704,307 | **41.59%** | 39.16-44.24% | 42,255 |

The yardstick was fixed before the campaign ran: the restart ladder measured
this identical `lambda` 64 schedule at 34.4-39.9% of the cap, a spread of 5.5
points. The asymmetry between these arms is 2.63 points, inside that spread, so
**by the reading registered in advance the design remained cap-matched**.

**That reading is weak, and the committed data contradict the stronger claim it
invites.** A min-to-max range taken from a different arm is not a test of a
paired difference, and the paired difference here is not small. Passive minus
active `finalEvaluations` has a mean of **+170,816** and a standard deviation of
**222,831** over the same twelve blocks, giving `t = 2.66` against the df = 11
threshold of 2.20, with the passive arm spending more in **nine of twelve**
blocks. So the arms do differ in spend by their own paired test. That contrast
was **not registered** — the design registered the range comparison and
nothing else — so read it as an exploratory spend signal, not as a result, and
do not report it as though it had passed a gate. What it rules out is the
reassurance: the spend asymmetry is *not* established as seed noise.

Two observations sit alongside it and are equally exploratory. The direction of
the asymmetry runs with active adaptation reaching `TolFun` *earlier* and still
returning the better cost — its mean `scoredEvaluations`, the count at which
each job reached its own minimum, is 1,281,893 against the passive arm's
1,463,387. And both arms sit above the ladder's separable 36.7%, so block
covariance appears to spend more per cold restart than separable does at the
same rung. Neither is registered; read them as descriptions of these 24 jobs.

## Block against separable, at a rung where both are clean

The seed base is the stagnation campaign's and the ladder's, and this design
shares it for a **weaker reason than the ladder's**, stated as such at
registration: it repeats no arm, so it earns no bit-for-bit replication check.
What the shared seeds buy is a by-product, and it is the most interesting number
in the campaign.

`blk-r32-l64` runs the identical blocks as the ladder's committed `sep-r32-l64`
cells, at the identical rung, budget and seeds. The covariance campaign could
not make this comparison — its separable arm was clamped dead at `lambda` 1024 —
so this is the one place the corpus can ask whether block's win survives when
separable is allowed to work.

| | mean | sd | best |
| --- | ---: | ---: | ---: |
| `sep-r32-l64` (restart ladder) | 882.46 | 22.59 | 840.82 |
| `blk-r32-l64` (this campaign) | 875.19 | 40.50 | 810.73 |

Paired over the twelve shared seeds: block leads by a mean of **+7.27** with a
standard deviation of 46.73, `t` = 0.54, winning **7 of 12** blocks.

The registered covariance campaign put block ahead of separable by **+39.12**
(`t` = +2.72, 11/12) — but it did so at `lambda` 1024, where its separable
control was running a memoryless update with active adaptation silently off.
Here, where both modes are clean, the gap is about a fifth of that and
indistinguishable from zero.

**This is cross-campaign and unregistered: a lead, never a finding.** It is not
a refutation of the covariance result, which measured what it measured under
conditions it stated. What it does is narrow that result's scope — it raises the
possibility that some of the +39.12 was separable being crippled rather than
block being good, and it is the kind of question that has to be registered and
run rather than argued. Anyone proposing a covariance default should read this
section before quoting the +39.12.

## Diagnostics

2,558 trajectory samples. `distributionExtent` — the identifiable
`sigma * max(D)` — never exceeds **0.4090** while sigma spans 3.84e-05 to
0.9165. That reproduces on new arms what the lambda screen established across
17,593 samples: sigma alone is gauge-dependent and says nothing about a diverged
search, and this column is the one to cite.

The `restart` column is **0 in all 2,558 rows**, and
`docs/cmaes-active-cma-restarts.csv` is **header-only**. Neither is a collection
failure. A cold-restart arm wraps 32 *single* CMA-ES runs, and the adapter
populates `Result.Restarts` only on the `OptimizeWithRestartsContext` path, so
`WithRestarts` accumulates nothing and does not renumber the diagnostics. The
ladder's `sep-r32-l64` behaves identically. Per-run mechanism for a cold-restart
arm is visible only through `finalEvaluations` and `iterations`.

## The record is unchanged

The best cost in the campaign is **810.73** (`blk-r32-l64`, block 8, seed
111020), which beats the ladder's best at this rung (840.82) and comes nowhere
near the standing eight-circle record of **726.1984354654948**, set by `blk-ipop`
in the deep hunt. Nothing in this campaign supersedes it, and none of these arms
was shaped to try.

## What this does and does not license

**Established.** `activeCMA` is measurable on the current pin, and the design
that measures it is the one registered here: block covariance, cold restarts, a
`lambda` below the clamp threshold for its mode. The knob's effect on the fit is
a null at twelve blocks, with a 95% paired interval of -6.98 to +54.56 — an
interval that contains zero and a benefit twice the point estimate alike, so it
is absence of evidence and not a bound on the effect.

**Not established, and it was reported as if it were.** That the arms spend the
same. The registered range comparison says the design stayed cap-matched, but
the paired difference on `finalEvaluations` is `t = 2.66` in nine of twelve
blocks. That test is unregistered, so it establishes nothing either; the state
of the question is open, not settled in either direction.

**Not established.** That `activeCMA` helps. The mean favours it and eight of
twelve blocks favour it, and neither clears the gate. Do **not** turn it on by
default on this evidence, and do not report the +23.79 as an effect size — it is
a point estimate inside an interval that comfortably contains zero.

**Not established, and the more important one.** That block covariance beats
separable at a rung where separable works. The +7.27 here and the +39.12 there
are not the same measurement, and the honest state of the question is that the
registered win was taken under conditions that handicapped its control.

**Do not** cite `docs/cmaes-active-cma-restarts.csv` as evidence of anything: it
is empty by construction.

## Limitations

- One fixture, one circle count, one rung. `lambda` 64 in block mode is the only
  place the knob was measured; its effect at other populations is unmeasured and
  the `negativeMass` table says it should differ.
- Twelve blocks against a paired sd of 48.43 is underpowered for an effect of
  the size observed. This is a bound, not a zero.
- Cap-matched, not spend-matched: both arms returned about 60% of the nominal
  budget unspent. A design that could express "restart until the budget is gone"
  would be measuring a different schedule, and might well answer differently.
- The block-against-separable comparison is cross-campaign, unregistered, and
  carries no correction. Two campaigns, two binaries, one shared seed range.
- `activeCMA` in **full** covariance mode is still unmeasured, at every
  `lambda`. Full never clamps, so it is the other clean place to ask this
  question and no campaign has.

## Raw data

- `docs/cmaes-active-cma-measurement.csv` — 24 rows, one per job.
- `docs/cmaes-active-cma-trajectories.csv` — 2,558 downsampled trajectory rows.
- `docs/cmaes-active-cma-restarts.csv` — header only, by construction.

## Reproducing it

```sh
go run ./scripts/cmaes-measurement -action plan    -design active-cma
go run ./scripts/cmaes-measurement -action analyze -design active-cma \
  -results docs/cmaes-active-cma-measurement.csv
```

`-budget` may only assert the registered 6,502,400; the driver refuses any other
value for this design.
