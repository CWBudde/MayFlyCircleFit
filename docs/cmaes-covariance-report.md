# The covariance campaign: block covariance tested, and a library defect found

`-design covariance` is the registered test of the deep hunt's strongest lead.
It asked two questions with two contrasts. **One of them is answered: block
covariance beats separable, and the result rejects under Holm.** The other could
not be asked at all, because the knob it moves was inert in the library — and
proving that is the more consequential half of this campaign.

## Conditions

| | |
| --- | --- |
| binary | built from `99e3e9f`, `feat(measurement): register the covariance campaign the deep hunt earned (Task 3)` |
| optimizer | `github.com/CWBudde/go-cma-es v0.1.0` (`optimizerVersion` `v0.1.0` in every row) |
| fixture | `example/MayFly-512.png`, 8 circles, one batch, 56 dimensions |
| budget | `huntBudget` = 12,582,912 evaluations per job, the deep hunt's cap |
| backend | `cpu` on every job, no degradation |
| host | 64-core Linux box, `serve --max-jobs 7`, `threads 1`, `evaluationWorkers 8` per job |
| dates | submitted 2026-08-30 21:58 CEST, last job finished 2026-08-31 03:20 CEST — 5h22m wall, 33.5h of optimizer time |
| seeds | 116001-116012 |
| jobs | 3 arms x 12 blocks = **36 submitted, 36 completed**, none cancelled or failed |

Unlike the deep hunt, this campaign lost nothing: `collect` ran against the
design's own complete manifest, and `analyzeDesign` produced the table below
rather than it being reconstructed by hand.

**These costs may not be compared against any campaign that ran at
`defaultBudget`.** The budget is 1.94x the shared cap, inherited deliberately
from the deep hunt because the lead under test lives on a ladder rung that only
exists at this budget. That is the same restriction
[`cmaes-deep-hunt-report.md`](cmaes-deep-hunt-report.md) carries.

## The registered result

| arm | mean | sd | median | best | gain vs `sep-ipop` | t (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| `sep-ipop` | 861.55 | 20.97 | 859.10 | 828.79 | control | control | control | control | control |
| `blk-ipop` | 822.43 | 40.58 | 817.82 | 747.00 | +39.12 | +2.72 | 0.01997 | **reject** | 11/12 |
| `sep-ipop-passive` | 861.55 | 20.97 | 859.10 | 828.79 | +0.00 | +Inf | 0.00000 | *void* | 0/12 |

Holm step-down over the two registered contrasts at a family-wise alpha of 0.05.
The primary contrast was registered before submission and is the first row that
matters; the second is discussed below and **must not be read as a result**.

### Per block

| block | seed | `sep-ipop` | `blk-ipop` | `sep-ipop-passive` | blk - sep |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 116001 | 851.02 | 815.77 | 851.02 | -35.25 |
| 2 | 116002 | 864.21 | 801.52 | 864.21 | -62.69 |
| 3 | 116003 | 842.04 | 819.87 | 842.04 | -22.17 |
| 4 | 116004 | 854.00 | 844.87 | 854.00 | -9.13 |
| 5 | 116005 | 848.99 | 842.48 | 848.99 | -6.51 |
| 6 | 116006 | 836.91 | **747.00** | 836.91 | -89.91 |
| 7 | 116007 | 881.63 | 797.59 | 881.63 | -84.04 |
| 8 | 116008 | 874.49 | 796.68 | 874.49 | -77.81 |
| 9 | 116009 | 877.57 | 857.31 | 877.57 | -20.26 |
| 10 | 116010 | **828.79** | 913.17 | **828.79** | **+84.38** |
| 11 | 116011 | 884.23 | 827.09 | 884.23 | -57.14 |
| 12 | 116012 | 894.75 | 805.77 | 894.75 | -88.98 |
| **mean** | | **861.55** | **822.43** | **861.55** | **-39.12** |

## Block covariance wins, and by less than the hunt suggested

The primary contrast rejects. It is worth being precise about how much that
licenses, because the effect is both real and considerably softer than the lead
that motivated the campaign.

The deep hunt observed `blk-ipop` beating the separable control in **11 of 11**
blocks by a mean of **77.24**. Here it wins **11 of 12** by a mean of **39.12** —
about half the size, with a standard deviation of 49.85 that exceeds the mean
itself. Block 10 loses by 84.38, the largest single-block difference in either
direction. That shrinkage between an unregistered lead and its registered test is
the ordinary outcome, not a surprise, and it is exactly why the hunt's report
said its own number was not a test.

So: block covariance is better on this fixture at this budget, the direction is
established, and the magnitude is known only to within a wide interval. One
block in twelve going the other way by more than twice the mean effect is a fair
summary of the risk in any single run.

**This is still not licence to change a default.** The campaign fits one
fixture, at 1.94x the standard cap, at one `lambda`, in one restart shape.
`covarianceMode` remains an expert knob.

### Why it wins: the ladder, not the update

The mechanism is visible in the per-restart records, and it is not primarily
about the covariance model being a better fit for the problem. It is about how
fast each rung of the IPOP ladder terminates.

| arm | lambda 1024 | lambda 2048 | lambda 4096 | lambda 8192 |
| --- | ---: | ---: | ---: | ---: |
| `sep-ipop` median spend | 7.8% | 20.8% | **59.2%** | 13.8% (only 8/12 blocks) |
| `blk-ipop` median spend | 6.1% | 12.2% | 34.9% | **47.0%** (12/12 blocks) |

`blk-ipop` converges on `tol_fun` at every rung up to 4096 in **12 of 12** jobs,
spending about a third of the cap to get there. `sep-ipop` needs 59% of the cap
at the 4096 rung alone and is truncated there in 4 of 12 jobs, so it reaches the
top rung in only 8 — and when it does, it has 13.8% of the budget left, which
buys a stub.

The consequence is where each arm finds its answer:

| arm | block best came from lambda 1024 | 2048 | 4096 | 8192 |
| --- | ---: | ---: | ---: | ---: |
| `sep-ipop` | 2 | 4 | 6 | **0** |
| `blk-ipop` | 1 | 2 | 2 | **7** |

`blk-ipop` takes its block best from the 8192 rung in 7 of 12 blocks. `sep-ipop`
never does, in any block. **Block covariance wins mostly by earning more of the
ladder**, which reproduces the deep hunt's reading (6 of 11 there) on fresh
seeds. It is a claim about the restart schedule as much as about the covariance
model, and a design that wanted to separate them would have to spend-match the
rungs rather than cap-match the job.

## The secondary contrast is void, and why

`sep-ipop-passive` differs from `sep-ipop` in one field: `activeCMA` off instead
of on. It returned costs **bit-identical to the control in all twelve blocks, to
the last digit.**

That is not a null result. A null result is a small effect that the design could
not resolve; this is a paired difference with exactly zero variance, which is
what "no treatment was applied" looks like. The `t = +Inf` and `p = 0.00000` in
the table are the arithmetic of dividing by a zero standard deviation, and
**the reject verdict beside them is meaningless.** They are printed here because
the collector printed them, and because the shape of the artifact is the
diagnostic.

The cause is upstream of this repository, in `go-cma-es v0.1.0`, and it is
arithmetic rather than a race or a plumbing slip. The flag does reach
`cmaes.Config.ActiveCMA`; what happens next is that it cannot matter:

1. The separable correction multiplies the rank-mu rate by `(n+2)/(blockDim+2)`,
   here `58/3 = 19.33`, giving `cmu = 2.759`.
2. That is clamped to `1 - c1`, so `cmu` becomes exactly `0.99943564866987478`.
3. Hansen's positive-definiteness guard on the active weights is
   `alpha_posdef = (1 - c1 - cmu*sum(w)) / (n*cmu)`. With the weights normalized
   to `sum(w) = 1` and `cmu` *assigned* the value `1 - c1`, that numerator is
   exactly `0.0` in IEEE arithmetic — not a near-cancellation.
4. Every negative weight is scaled by zero. The active term contributes `0.0` to
   every covariance coordinate, and the trajectory is the passive one.

Measured directly on the campaign's configuration, in each covariance mode:

| mode | cmu | clamped? | negative mass | `activeCMA` |
| --- | ---: | --- | ---: | --- |
| separable | 0.999436 | **yes** | **0** | **inert** |
| block | 0.919587 | no | 0.00155 | live |
| full | 0.142695 | no | 0.107 | live |

The same clamp has a second consequence that matters more than the disabled
flag. The covariance decay factor is that identical numerator, so in separable
mode at this `lambda` it is **exactly zero**: the covariance matrix is rebuilt
from the current generation alone and retains nothing from the generations
before it. The separable arms in this campaign — and in every earlier CMA-ES
campaign in this repository, all of which are separable at `lambda` 1024 — were
running a memoryless covariance update.

This is fixed upstream in `go-cma-es` 0.2.0, which bounds the rank-mu rate away
from that boundary. **This repository has not taken that upgrade**, and the pin
in `AGENTS.md` is unchanged: 0.2.0 changes the update rules, so every recorded
figure here would need re-baselining, and the resume guard will refuse the
version until that is done deliberately.

### What it costs this campaign, and the last one

Three things follow, and the third is the awkward one.

**`activeCMA` is still unmeasured.** This is the second campaign to fail to
measure it — the deep hunt lost the arm to cancelled jobs, and this one lost it
to the library. It is now measurable, but only in `block` or `full` mode, where
the guard leaves a positive budget, and only against a library version that has
the fix.

**The deep hunt's block-1 reading is explained.** That report noted its
`sep-ipop-passive` job returned a cost bit-identical to `sep-ipop`'s and read it
as "what a shared deterministic prefix looks like." That reading was wrong in a
harmless direction: the two runs were identical not for a prefix but for their
whole length, for the reason above. The report's conclusion — that the arm
measured nothing — stands, and is now stronger.

**The primary contrast carries an unplanned confound.** `blk-ipop` had active
adaptation live; its separable control did not. Both arms *requested*
`activeCMA` on, and the design's own test asserts they differ in exactly one
declared field, which they do. But the field's *effect* differs by mode, so the
contrast compares block-with-working-active-updates against
separable-with-inert-ones, and some unknown share of the +39.12 belongs to the
active update rather than to the covariance structure.

The share is probably small — block mode's negative mass is 0.00155, two orders
below full mode's — but "probably small" is an argument, not a measurement. The
contrast that would settle it is `blk-ipop` against `blk-ipop-passive`, on a
library version where both mean something. That is the campaign this one earns.

## Diagnostics

`distributionExtent` — `sigma * max(D)`, the identifiable quantity — peaked at
**1.5851** over 9,381 trajectory samples, while raw `sigma` reached
**7.963e+57**. This reproduces the lambda screen's finding a third time, on
seeds it has never seen, and is the reason
[`cmaes-lambda-report.md`](cmaes-lambda-report.md) says to cite that column and
never sigma. Per arm the maxima are 1.4584 (`sep-ipop`) and 1.5851
(`blk-ipop`); nothing in this campaign diverged.

Every job terminated `completed` having spent its full 12,582,915 evaluations —
three over the cap, which is generation granularity, not an overrun. Median wall
clock was 0.92h (`sep-ipop`), 0.91h (`blk-ipop`) and 0.89h (`sep-ipop-passive`).
`blk-ipop` used a median of 3,304 iterations against the control's 4,302 for the
same evaluation count, which is the ladder difference again: more of its budget
was spent in large populations.

## The record is unchanged

The best cost here is **746.9953** (`blk-ipop`, block 6, seed 116006). The
standing record on this fixture is **726.1984354654948** from the deep hunt, and
this campaign did not approach it. That is expected — the hunt searched nine arms
including rungs and warm starts this design does not carry — and it is recorded
so that no one reads the 747 as a near miss on the record rather than as the best
of twelve honest draws.

## What this does and does not license

**Established.** Block covariance beats separable covariance on this fixture at
this budget under an IPOP ladder, by a mean of 39.12 over twelve paired blocks,
`t = +2.72`, rejecting under Holm at a family-wise alpha of 0.05.

**Established, and worth more than the headline.** `activeCMA` was arithmetically
inert in separable mode in `go-cma-es v0.1.0`, and the separable covariance
update was memoryless at this `lambda`. Every separable measurement in `docs/`
was taken under those conditions.

**Not established.** That `covarianceMode: block` should become a default —
one fixture, one `lambda`, one restart shape, a non-standard budget, and a
confound with the active update. That `activeCMA` does or does not help: still
unmeasured, twice over. That the +39.12 is the effect size; the interval is wide
and one block in twelve reversed it.

**Do not** quote any cost here against a `defaultBudget` campaign, and **do not**
quote the `sep-ipop-passive` row's `t`, `p` or Holm verdict as anything at all.

## Provenance

- `docs/cmaes-covariance-measurement.csv` — 36 rows, one per job.
- `docs/cmaes-covariance-trajectories.csv` — 9,381 diagnostic samples.
- `docs/cmaes-covariance-restarts.csv` — 136 per-restart records.

All three were collected with `cmaes-measurement -action collect -design
covariance` against the complete 36-row manifest, and md5-verified against the
measurement host before it was wiped. The host was provisioned for this campaign
and destroyed after collection, so the checkpoints, traces and rendered images
behind these rows no longer exist; the CSVs are the record.

The upstream fix is `CWBudde/go-cma-es` PR #3, released as 0.2.0.
