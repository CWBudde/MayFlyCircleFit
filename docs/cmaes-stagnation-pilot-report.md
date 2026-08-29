# CMA-ES stagnation criterion: the window-selection pilot

**Ran 2026-08-29** on the 64-core campaign host, driver
`scripts/cmaes-measurement -design stagnation-pilot`.

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

## Result

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

## What the pilot does not say

**It moved budget without moving cost.** Every criterion arm was nominally
worse than its baseline, all of it inside the `lambda` screen's per-arm standard
deviation of 27-48 at three blocks. The honest prior for the campaign that
follows is another null: reclaimed budget is not yet reclaimed quality.

## The campaign the pilot selected

Registered as `-design stagnation`: `sep-ipop-l20` and `sep-ipop`, each with and
without its selected window, 4 arms x 12 blocks at seeds 111013-111024.
**Two named contrasts** rather than a derived family, with `lambda` 20 primary,
because that is the level where the criterion bought a restart rather than
merely a longer final run. Submitted 2026-08-29; open work is Task 2 in
[`../PLAN.md`](../PLAN.md).

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
reproduces the tables from the collected CSV alone, and refuses to print a
statistic for the pilot, which is registered as descriptive. Substitute
`-design stagnation` for the twelve-block campaign.

The arm table, the Hansen anchor, and the block and seed bases are in
`scripts/cmaes-measurement/main.go`; `main_test.go` pins the registered shape of
both designs, and
[`scripts/cmaes-measurement/README.md`](../scripts/cmaes-measurement/README.md)
carries the driver's full operating notes.
