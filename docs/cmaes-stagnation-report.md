# CMA-ES stagnation criterion: the pilot and the campaign

**Arming a stagnation criterion on a separable IPOP restart schedule does not
improve the fit.** Both registered contrasts of the twelve-block campaign
retain their null under Holm, the primary at `t = -0.34` with six blocks won
out of twelve. The criterion fires as designed and reshuffles where a restart
ladder spends its budget; it does not change how much useful work the ladder
does. **Do not arm one by default.** Every arm here is IPOP; `bipop` is exposed
to callers too and this campaign says nothing about it either way.

**Ran 2026-08-29** on the 64-core campaign host, driver
`scripts/cmaes-measurement`, designs `stagnation-pilot` (27 jobs, 3 blocks) and
`stagnation` (48 jobs, 12 blocks, 83,480 job-seconds).

**Pins:** `github.com/CWBudde/go-cma-es v0.1.0` and `github.com/cwbudde/mayfly
v0.7.1` — the current tree, not a superseded one. Every arm is separable CMA-ES
with IPOP restarts, so no MayFly code runs in this design and the v0.7.0 result
change does not reach it. These rows are comparable with
[`cmaes-report.md`](cmaes-report.md) and
[`cmaes-lambda-report.md`](cmaes-lambda-report.md), which ran on the same pins,
and with a run made today.

[`cmaes-report.md`](cmaes-report.md) found that both IPOP arms of the Phase 21
campaign spent about 40% of their evaluation budget after their last
improvement, because the campaign set none of the `stop*` fields and a restart
schedule therefore had no way to end a run that had stopped progressing.
[`cmaes-lambda-report.md`](cmaes-lambda-report.md) widened that figure: **six
IPOP arms across three `lambda` levels spend 30-57% of their budget after their
last improvement**, against 16-21% for the non-restarting arms that stop
themselves on `TolFun`. So the waste is a property of restart arms without a
stagnation guard, not a two-arm accident.

Arming one is a behaviour change for every existing CMA-ES restart
configuration, so it needs its own measurement. This pilot does not make that
measurement — it selects the *window* the measurement will use, on mechanism
rather than on cost, so that the campaign which later tests cost is not
selecting on its own outcome.

## What the adapter records now

Neither the Phase 21 campaign nor the `lambda` screen could have priced this
fix: both ran before the adapter recorded the quantities it needs. Three
records were added first, and any arm this decision rests on has to be re-run
rather than re-read.

- **`SearchDiagnostics.DistributionExtent`** carries `sigma * max(D)`, folding
  the dense and per-block covariance representations. The adapter previously
  took sigma and the condition number from the distribution snapshot and dropped
  its eigenvalues, so the extent of the sampling distribution was not
  recoverable from any trace this project had written. Traces written before it
  carry an empty cell, not a zero. This is the column that settles the sigma
  question; see [`cmaes-lambda-report.md`](cmaes-lambda-report.md).
- **`opt.RestartRun`** carries each restart's own `TerminationReason` verbatim —
  `tol_fun`, `condition_number` — with its regime, population, iteration and
  evaluation counts and its own best cost. The library records one per restart
  and the adapter used to discard it and report the schedule-level reason, which
  the restart driver overwrites with max-evaluations whenever the budget is
  spent; `completed` on all sixty Phase 21 jobs is structurally guaranteed for a
  restart arm and carries no information. The pipeline stamps the stage index
  onto each record, because a sequential or batch run drives an independent
  schedule per circle or per batch. The records reach `checkpoint.json` as an
  additive optional field, so an older checkpoint decodes empty and the schema
  version does not move.
- **A `restart` index on each trace sample's optimizer diagnostics.** This is
  the join key. Cumulative iteration and evaluation counts run straight through
  a restart boundary, so without it a trace cannot say which run produced a
  sample, and the evaluations a run spent after its last improvement cannot be
  recovered.

No adapter or app change was needed to *apply* a criterion:
`stopStagnationIters` already reaches `config.Convergence.StagnationIterations`
through `buildEarlyStop` and `WithCMAESEarlyStop`, applied per run inside
`OptimizeWithRestartsContext`.

## Design

Nine separable-IPOP arms over three blocks, seeds 112001-112003, at `lambda` 20
and 1024. Each level carries its own no-criterion baseline and windows at half,
one, and four times Hansen's `120 + 30n/lambda` anchor, plus one exploratory
cell combining the anchor with `stopMinImprovement = 0.1`.

Three blocks is deliberately far too few for a paired test. The design is
registered as descriptive: `analyze` reports costs and budget waste for it and
refuses to print a statistic.

**Selection rule, fixed before the data existed:** take the window that
reclaims the most budget while still completing at least two restarts, ties
broken toward the anchor.

A default also has to be **window-only** (`stopMinImprovement = 0`), because
that field is an absolute cost threshold and cannot transfer to a reference
image whose costs differ in scale.

## The pilot: what the rule selected

> **The pilot's headline reclaim did not replicate.** The 19.7 and 25.6
> percentage points below were measured on three blocks, and the twelve-block
> campaign reversed the sign at `lambda` 20. Read this section as the record of
> how the window was chosen, not as a measurement of what the window does.

The rule named **half the anchor at both levels** — 102 generations at
`lambda` 20 and 60 at 1024. Those windows reclaimed **19.7 and 25.6 percentage
points** of the budget spent after the last improvement, and at `lambda` 20 the
criterion bought a ninth restart in all three blocks, against the baseline's
9/8/8.

Three arms are retired by measurement rather than by argument:

- **Four times the anchor never fires.** `sep-ipop-l20-w816` and `sep-ipop-w484`
  reproduced their baselines' costs to the last digit in every block.
- **The absolute threshold reclaims nothing.** The exploratory
  `stopMinImprovement` cell fired most often of any arm and still left 82.1%
  waste against the baseline's 80.8% — which retires it on measurement as well
  as on transferability.
- **The anchor itself failed at `lambda` 20**, raising waste to 84.7%.
  go-cma-es counts iterations without sufficient progress, where Hansen's
  criterion tests a median of fitness histories across the span; half the
  nominal length is what recovers the intended behaviour here.

## What the pilot did not say

**It moved budget without moving cost.** Every criterion arm was nominally
worse than its baseline, all of it inside the `lambda` screen's per-arm standard
deviation of 27-48 at three blocks. The stated prior for the campaign was
therefore another null: reclaimed budget is not yet reclaimed quality.

## The campaign

`-design stagnation`: `sep-ipop-l20` and `sep-ipop`, each with and without its
selected window, 4 arms x 12 blocks at seeds 111013-111024, disjoint from every
earlier campaign. **Two named contrasts** rather than a derived family, so Holm
corrects over the two questions asked rather than the four that four arms would
otherwise produce — the multiplicity trap [`cmaes-lambda-report.md`](cmaes-lambda-report.md)
paid for. `lambda` 20 is primary, because the pilot found it the level where the
criterion bought a restart rather than merely a longer final run.

Every arm is evaluation-matched by construction at 6,502,400 evaluations, and
all 48 jobs completed.

| arm | mean | sd | median | best | gain | t (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| `sep-ipop-l20` | 854.46 | 34.92 | 848.76 | 816.07 | control | | | | |
| `sep-ipop-l20-w102` | 858.45 | 26.05 | 849.42 | 832.94 | -3.99 | -0.34 | 0.73817 | retain | 6/12 |
| `sep-ipop` | 878.80 | 50.93 | 886.80 | 752.52 | control | | | | |
| `sep-ipop-w60` | 868.95 | 48.28 | 881.89 | 752.52 | +9.84 | +0.82 | 0.43223 | retain | 4/12 |

Holm step-down over both contrasts at a family-wise alpha of 0.05: the first
gate is 0.025 and neither contrast approaches it. The uncorrected two-sided
threshold at `df=11` is `t = 2.20`.

**The primary contrast is a coin flip**, not a near miss: six blocks won out of
twelve, a mean of -3.99 against a paired standard deviation of 40.34, and a
median difference of -0.21. The secondary shows the pattern that most often
misleads — a positive mean, +9.84, from an arm that won only four blocks of
twelve, with a median difference of -2.19. Its mean is one block: block 1's
control returned 979.71 against that same arm's 752.52-898.70 elsewhere, a
+107.35 difference that no other block comes near.

| block | `sep-ipop-l20` | `-w102` | `sep-ipop` | `-w60` |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 816.10 | 841.11 | 979.71 | 872.37 |
| 2 | 911.85 | 889.72 | 886.78 | 893.49 |
| 3 | 828.76 | 832.94 | 852.73 | 877.00 |
| 4 | 861.74 | 843.39 | 886.78 | **886.78** |
| 5 | 844.59 | 840.84 | 872.22 | 904.40 |
| 6 | 852.92 | 885.67 | 752.52 | **752.52** |
| 7 | 826.11 | 833.64 | 891.30 | 907.59 |
| 8 | 878.46 | 865.64 | 854.40 | 817.40 |
| 9 | 822.14 | 849.27 | 890.99 | 869.03 |
| 10 | 886.15 | 849.58 | 892.58 | 827.68 |
| 11 | 908.61 | 852.89 | 898.70 | 927.98 |
| 12 | 816.07 | 916.73 | 886.81 | 891.20 |

## Mechanism: the criterion fires and buys almost nothing

| arm | runs/block | per-run terminations | budget after last improvement |
| --- | ---: | --- | ---: |
| `sep-ipop-l20` | 8.2 | tol_fun 86, maximum_evaluations 12 | 65.6% |
| `sep-ipop-l20-w102` | 8.8 | **stagnation 68**, tol_fun 26, maximum_evaluations 12 | **77.8%** |
| `sep-ipop` | 3.0 | tol_fun 24, maximum_evaluations 12 | 42.4% |
| `sep-ipop-w60` | 3.0 | **stagnation 24**, maximum_evaluations 12 | **35.5%** |

Three things in that table explain the null.

**At `lambda` 20 the criterion made the waste worse** — 77.8% against 65.6%,
the opposite of the pilot's 61.1% against 80.8%. It stops 68 runs and converts
them into restarts eight blocks out of twelve (8.8 runs per block against 8.2),
but those extra runs arrive at the top of an IPOP ladder whose population has
already doubled to 2,560 and 5,120, and they stagnate in turn. Ending a dead
run early is not the same as starting a live one.

**At `lambda` 1024 it buys no restarts at all.** Both arms complete exactly
three runs in all twelve blocks, and every block's last run ends on
`maximum_evaluations`. The ladder is capped by the evaluation budget, not by
how its runs terminate, so the criterion only moves budget between three fixed
runs. It reclaims a real 42.4% to 35.5% doing so, and that reclaimed budget
buys nothing measurable.

**Two of twelve blocks at `lambda` 1024 are bit-identical** — blocks 4 and 6,
886.7820510864258 and 752.5220120747884 to the last digit. The 60-generation
window never triggered in those runs, so the criterion arm *is* the control arm
there and contributes a structural zero to the paired difference. That contrast
is therefore partly measuring a window that sometimes does nothing. (Block 2's
886.78 is a two-decimal coincidence between different runs, 886.7835 against
886.7821, not a tie.)

## What did not replicate, and what that costs

The window was selected by a rule fixed before the pilot's data existed, which
is the right procedure and is not the problem here. The problem is that the
rule was applied to a quantity that turned out not to be stable at three
blocks: **reclaimed budget reversed sign at `lambda` 20 between the pilot and
the campaign.** A three-block pilot was too small for the job it was given.

That does not change the campaign's answer — the campaign tests cost, and cost
is a null at both levels regardless of which window it ran. It does mean the
pilot's reclaim figures must not be cited on their own, and it is the reason
this document keeps them under an explicit warning rather than deleting them:
the selection they justified is what the campaign actually ran.

Two of the pilot's findings *do* survive, because the campaign reproduces their
direction. Four times the anchor never fires — the campaign's own two
bit-identical blocks are the same effect at a shorter window. And at `lambda`
1024 the criterion does reclaim budget, 42.4% to 35.5%, just as the pilot said.

## A by-product: the sigma reading holds

The campaign's trajectories carry 8,538 samples of `distributionExtent`, the
identifiable `sigma * max(D)`. **It never exceeds 1.451 while sigma spans
1.170e-05 to 8.447e+55.** That independently reproduces
[`cmaes-lambda-report.md`](cmaes-lambda-report.md)'s finding on different seeds
and a different design. Cite this column; never cite sigma, which is
gauge-dependent because CMA-ES identifies only `sigma^2 * C` and go-cma-es does
not renormalize `C`.

## Recommendation

**Do not arm a default stagnation criterion for restart strategies.** The
behaviour change is real — it stopped 68 of the treatment arm's 106 runs at
`lambda` 20 — and it buys nothing measurable, so it is cost without benefit on
the configurations tested.

Those are separable IPOP at `lambda` 20 and at `lambda` 1024, and nothing else.
`bipop` is equally available to a caller and follows a different restart
schedule — it halves the population as readily as it doubles it, so a criterion
that merely feeds a runaway IPOP ladder there might not — and it was not in the
design. The no-default decision stands for want of any measured case, not
because BIPOP was tested and found null.

`stopStagnationIters` remains available and unchanged for a caller who wants
it; nothing here argues against setting one deliberately. What has no measured
case is turning it on for people who did not ask.

If this is ever revisited, the thing to change is not the window. The campaign
says the budget was never the binding constraint on quality here, so the
question worth asking next is what is.

## Raw data

Both designs' records are committed, and every table above is reproducible from
them without a server:

| design | costs | trajectories | per-restart records |
| --- | --- | --- | --- |
| `stagnation` | `cmaes-stagnation-measurement.csv` | `cmaes-stagnation-trajectories.csv` | `cmaes-stagnation-restarts.csv` |
| `stagnation-pilot` | `cmaes-stagnation-pilot-measurement.csv` | `cmaes-stagnation-pilot-trajectories.csv` | `cmaes-stagnation-pilot-restarts.csv` |

These are the first campaigns in this repository whose restart files are not
empty. Every earlier one — Phase 21 and the `lambda` screen — ran before the
adapter recorded per-run terminations, so their restart CSVs carry a header and
nothing else.

**`-action analyze` reads the measurement CSV alone.** It prints the arm table
with its contrasts, Holm decisions and blocks won — for the pilot, the
descriptive table instead — and it opens neither the trajectory nor the restart
file. The three remaining tables come from those two files directly, and each is
one command:

```sh
# the per-block table: costs by arm and block
LC_ALL=C awk -F, 'NR>1 {printf "%s block %s: %.2f\n", $1, $2, $8}' \
  docs/cmaes-stagnation-measurement.csv

# runs per block and per-run terminations
LC_ALL=C awk -F, 'NR>1 {runs[$1]++; term[$1" "$11]++}
  END {for (a in runs) printf "%s: %d runs, %.1f/block\n", a, runs[a], runs[a]/12
       for (k in term) print k, term[k]}' docs/cmaes-stagnation-restarts.csv

# budget spent after the last improvement, as a ratio of totals
LC_ALL=C awk -F, 'NR>1 {final[$1] += $10; wasted[$1] += $10 - $9}
  END {for (a in final) printf "%s: %.1f%%\n", a, wasted[a]/final[a]*100}' \
  docs/cmaes-stagnation-measurement.csv

# the sigma reading: sample count, max distributionExtent, sigma range
LC_ALL=C awk -F, 'NR>1 {n++; e=$10+0; s=$8+0; if (e>x) x=e
    if (lo=="" || s<lo) lo=s; if (s>hi) hi=s}
  END {printf "%d samples, max extent %.3f, sigma %g..%g\n", n, x, lo, hi}' \
  docs/cmaes-stagnation-trajectories.csv
```

`LC_ALL=C` is not decoration: under a comma decimal locale `awk` truncates
these fields at the separator and the extent maximum silently reads 7 instead
of 1.451. The waste shares are ratios of totals rather than means of per-job
ratios, matching what the driver computes for a descriptive design — averaging
per-job fractions would weight a short run like a long one.

## Reproducing

Both designs submit through a running server, so the queue and every active job
stay visible on the dashboard — 27 jobs for the pilot and 48 for the campaign.
`-action` defaults to `collect`, so naming a design alone collects rather than
runs it. Build one identified binary and keep it for the whole campaign:

```sh
commit=$(git rev-parse HEAD)
go build -trimpath \
  -ldflags "-X github.com/cwbudde/circlefit/cmd.commit=$commit" \
  -o ./data/cmaes-phase11/circlefit .

./data/cmaes-phase11/circlefit serve \
  --port 8085 --data-root ./data/cmaes-phase11 \
  --max-jobs 1 --queue-size 100 --input-root .
```

The dashboard is then at <http://localhost:8085/>. In another shell, read the
design before queueing it — a manifest may only be written once:

```sh
go run ./scripts/cmaes-measurement -action plan     -design stagnation-pilot
go run ./scripts/cmaes-measurement -action submit   -design stagnation-pilot
go run ./scripts/cmaes-measurement -action collect  -design stagnation-pilot
go run ./scripts/cmaes-measurement -action analyze  -design stagnation-pilot
```

`collect` is safe to repeat while the queue runs; it prints state counts and
writes its CSVs only once every job in the design has completed. `analyze`
prints the arm table from the collected measurement CSV alone, and refuses to
print a statistic for the pilot, which is registered as descriptive; the block,
termination and sigma tables come from the restart and trajectory CSVs by the
commands under [Raw data](#raw-data). Substitute `-design stagnation` for the
twelve-block campaign.

The arm table, the Hansen anchor, and the block and seed bases are in
`scripts/cmaes-measurement/main.go`; `main_test.go` pins the registered shape of
both designs, and
[`scripts/cmaes-measurement/README.md`](../scripts/cmaes-measurement/README.md)
carries the driver's full operating notes.
