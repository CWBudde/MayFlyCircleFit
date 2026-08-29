# CMA-ES measurement campaign

This driver owns the registered campaigns behind go-cma-es `PLAN.md` Phase 11
and this repository's Phase 23. It submits jobs through a running server so the
dashboard shows the queue and every active job. It refuses to overwrite a
manifest, which prevents an accidental second submission from corrupting the
paired design.

Four designs are registered, selected with `-design`:

- `phase21` (the default) — the original five arms: two Mayfly controls and
  three CMA-ES arms, all at `popSize` 1024. 60 jobs, 12 blocks, seeds
  111001-111012.
- `lambda` — the eight-arm screen that crosses both covariance modes with four
  restart-and-population shapes. 96 jobs, 12 blocks, seeds 111001-111012
  (deliberately the same as `phase21`, so three repeated arms measure
  cross-campaign drift instead of assuming it away).
- `stagnation-pilot` — nine arms over 3 blocks at seeds 112001-112003,
  descriptive only. See below.
- `stagnation` — the four-arm campaign that pilot selected, 48 jobs, 12 blocks,
  seeds 111013-111024. See below.

**A design owns its block count, its seed base and its contrast family.** All
three used to be global or flag-driven. `-blocks` and `-seed-base` are now
assertions a caller may make against the registered design, not ways to alter
one: a mistyped seed base would otherwise silently reuse another campaign's
seeds, and a mistyped block count would change `df` after the fact.

Designs are enumerated in code rather than assembled from flags, so a campaign
cannot silently differ from the one that was registered. `-action plan` prints
a design without submitting it; a manifest may only be written once, so it is
worth reading before a campaign is queued.

## Contrast families are registered, not derived

`phase21` and `lambda` derive their family as every arm against each of two
controls. That is how their committed reports were produced and it stays
reproducible — but it is also a trap, and the lambda screen paid for it:
crossing two factors turned eight arms into **thirteen** contrasts, and Holm's
first gate at 0.05/13 retained a `p` of 0.00557. A design that adds an arm to
answer one question raises the bar for every other question it asks.

A design may now name its comparisons instead, and mark at most one primary.
The number of contrasts a design declares is then the multiplicity it pays for,
which is a decision made when the campaign is registered rather than a
consequence of how many arms it happens to run.

Build one identified binary and keep it for the whole campaign:

```sh
commit=$(git rev-parse HEAD)
go build -trimpath \
  -ldflags "-X github.com/cwbudde/circlefit/cmd.commit=$commit" \
  -o ./data/cmaes-phase11/circlefit .

./data/cmaes-phase11/circlefit serve \
  --port 8085 \
  --data-root ./data/cmaes-phase11 \
  --max-jobs 1 \
  --queue-size 100 \
  --input-root .
```

The dashboard is then at <http://localhost:8085/>. In another shell:

```sh
go run ./scripts/cmaes-measurement -action submit
go run ./scripts/cmaes-measurement -action collect
```

`collect` is safe to repeat while the queue runs. It prints state counts and
does not write the committed CSVs until every job in the design completes. Once
complete it writes `docs/cmaes-measurement.csv`, writes the downsampled
optimizer mechanism data to `docs/cmaes-trajectories.csv`, and prints Markdown
statistics. Use `-action analyze` to reproduce the result table from the first
CSV alone.

The campaign was run to completion on 2026-08-28 on a 64-core host, six jobs
at a time, in 4.9 hours of wall clock. Its result is
[`docs/cmaes-report.md`](../../docs/cmaes-report.md) and its raw data is
committed as `docs/cmaes-measurement.csv` and `docs/cmaes-trajectories.csv`.
Re-running `submit` against a fresh data root would produce a second campaign,
not an extension of that one.

The first campaign was stopped by operator request after three completed jobs
and one interrupted job because the calibrated queue duration was several days.
Do not restart its server unless resuming that work is intentional. Its
persisted subset can be collected entirely offline, without touching the queue:

```sh
go run ./scripts/cmaes-measurement \
  -action preliminary \
  -results docs/cmaes-preliminary-results.csv \
  -trajectories docs/cmaes-preliminary-trajectories.csv
```

That action collects only job directories which have checkpoint metadata and
labels a non-completed checkpoint `interrupted`. It deliberately prints no
inferential statistics. See
[`docs/cmaes-preliminary-report.md`](../../docs/cmaes-preliminary-report.md).

## A design owns its artifact paths

`-manifest`, `-results`, `-trajectories` and `-restarts` default to the
selected design's own files rather than to Phase 21's. `phase21` keeps
`manifest.csv` in the data root and `docs/cmaes-measurement.csv`,
`docs/cmaes-trajectories.csv`, `docs/cmaes-restarts.csv`; every other design
carries its name, so `-design stagnation-pilot` collects into
`docs/cmaes-stagnation-pilot-measurement.csv` and its siblings. Collecting a
second campaign with nothing but `-design` therefore cannot write over the
first one's committed record, which is the same refusal submission already
makes for an existing manifest. Passing any of the four flags still overrides
the default.

## The lambda screen

`-design lambda` answers the two questions `docs/cmaes-report.md` left open.
The CMA-ES adapter sets `Lambda = popSize`, so every Phase 21 arm searched 56
dimensions with a population of 1024 against Hansen's default of
`4 + floor(3 ln 56)` = 16. The screen visits 1024, 64 and 20 — 20 being the
floor `app.MinPopulation` permits — crossed with full and separable covariance,
under IPOP restarts.

| | full | separable |
| --- | --- | --- |
| no restarts, lambda 1024 | `cmaes-single` | `sep-cmaes-single` |
| IPOP, lambda 1024 | `cmaes-ipop` | `sep-cmaes-ipop` |
| IPOP, lambda 64 | `cmaes-ipop-l64` | `sep-cmaes-ipop-l64` |
| IPOP, lambda 20 | `cmaes-ipop-l20` | `sep-cmaes-ipop-l20` |

Three cells repeat Phase 21 at the same seeds, so cross-campaign drift is
measured rather than assumed, and `sep-cmaes-single` is the cell Phase 21 never
ran: without it nothing attributes `sep-cmaes-ipop`'s win to covariance mode
rather than to restarts (Task 23.2).

Every arm is evaluation-matched by construction — each lambda level divides the
6,502,400-evaluation budget exactly, and the driver refuses a level that does
not. `iters` is therefore a budget encoding, not a prediction: an IPOP arm
doubles its population on restart and reaches the evaluation cap long before
the nominal generation count.

Small populations cost wall clock at equal evaluations, because a generation is
a synchronisation barrier. Measured on the 64-core host at seven concurrent
jobs and eight evaluation workers, lambda 64 ran at 0.67x and lambda 20 at
0.61x the evaluation rate of lambda 1024.

## The stagnation pilot

`stagnation-pilot` answers the open stagnation question, Task 2 of `PLAN.md`:
should a restart strategy
arm a default stagnation criterion when the caller sets none? Six IPOP arms
across three `lambda` levels spend **30-57%** of their budget after their last
improvement, because no criterion is configured, `Stop.enabled()` is false, and
a schedule cannot end a run that has stopped progressing and hand its budget to
the next restart.

Nine arms, all separable IPOP, at the two population sizes the registered
campaign will use — `lambda` 20, where the measured waste is worst and where an
IPOP ladder stays inside the population range the screen found unremarkable,
and `lambda` 1024, the shape Phase 21 ran. Each level contributes its own
no-criterion baseline, so reclaimed budget is read inside the pilot's own three
blocks rather than against the lambda screen's different seeds.

Windows are half, one and four times Hansen's `120 + 30n/lambda` — 204
generations at `lambda` 20 and 121 at 1024, for the 56-dimension search. That
is an **anchor, not a fidelity claim**: go-cma-es stops a run after N
iterations without sufficient progress, while Hansen's criterion tests a median
of fitness histories across that span.

**The pilot is descriptive and `analyze` refuses to print a statistic for it.**
Three blocks cannot support a paired test. It reports each arm's cost, the
budget it spent and the share of that budget falling after its last
improvement; the restart counts and per-run termination reasons in the
`-restarts` CSV are what select the window. The selection rule is fixed before
the data exists: **take the window that reclaims the most budget while still
completing at least two restarts, breaking ties toward the Hansen anchor.**
Selecting it on cost and then testing cost would be selecting on the outcome.

One arm raises `stopMinImprovement` off zero, at `lambda` 20's anchor. The
committed lambda traces show 30.9% of that arm's recorded improvements are
smaller than 0.1 cost units and the smallest is 2.7e-05, so whether trivial
improvements keep resetting the counter is worth one arm. It stays exploratory:
an absolute cost threshold cannot become a shipped default, because it does not
transfer to a reference image whose costs differ in scale. The shippable shape
is window-only, and that is what the other arms measure.

```sh
./cmaes-measurement -action plan -design stagnation-pilot
./cmaes-measurement -action submit -design stagnation-pilot
```

## The stagnation campaign

`-design stagnation` tests on cost what the pilot selected on mechanism. Two
pairs, one per population size — each level's no-criterion baseline against the
same level under a window of **half** the Hansen anchor, 102 generations at
`lambda` 20 and 60 at `lambda` 1024.

| | no criterion | half anchor |
| --- | --- | --- |
| lambda 20 (primary) | `sep-ipop-l20` | `sep-ipop-l20-w102` |
| lambda 1024 | `sep-ipop` | `sep-ipop-w60` |

**The window was selected by the rule, not by the costs.** The pilot's rule was
fixed before its data existed, and at both levels it named the half-anchor: it
reclaimed 19.7 and 25.6 percentage points of the budget spent after the last
improvement, where the anchor itself reclaimed nothing at `lambda` 20 (waste
rose to 84.7%) and four times the anchor never fired at all — `sep-ipop-l20-w816`
and `sep-ipop-w484` returned their baselines' costs to the last digit in all
three blocks, which is what a criterion that never triggers looks like.

`lambda` 20 is primary because it is the level where the criterion bought
another *restart*: the pilot's `-w102` completed nine runs in all three blocks
against the baseline's 9/8/8, while every `lambda` 1024 arm completed exactly
three however it terminated, the ladder being capped by the evaluation budget.
At 1024 the reclaimed budget lengthens the final run instead, which is a
different mechanism and is why it is a secondary question rather than a second
answer to the same one.

The design names **two** contrasts, so Holm corrects over two: the first gate is
at 0.025 and `t` at `df=11` is about 2.59. Deriving the family from the arms
would have cost four contrasts for the same two answers.

**Go in expecting a null.** The pilot moved budget without moving cost — every
criterion arm was nominally worse than its baseline, all of it inside the lambda
screen's per-arm sd of 27-48 at three blocks. That the mechanism fires is
measured; that it is worth anything is exactly what these twelve blocks decide.

The pilot's ninth arm does not reappear here. `stopMinImprovement` is an
absolute cost threshold, it cannot transfer to a reference image whose costs
differ in scale, and the pilot found it reclaimed nothing anyway (82.1% against
the baseline's 80.8%) despite firing most often. A test enforces that no arm of
this campaign sets one.

```sh
./cmaes-measurement -action plan -design stagnation
./cmaes-measurement -action submit -design stagnation
```

## Budget and pairing

The current Mayfly v0.7.1 pin consumes 6,502,400 optimizer evaluations in the
2048-iteration control. CMA-ES therefore receives 6,350 generations at
lambda=1024. The r16 arm intentionally runs all sixteen 128-iteration attempts;
the collector scores its trace at the same 6,502,400-evaluation cap, excluding
the small tail beyond the control budget.

Submission is block-major. Block `b` uses seed prefix `111000+b` in every arm
of the design; the twelve prefixes are disjoint, and the restart
implementations derive their attempt seeds from only that block's prefix. The
driver refuses any block count other than twelve, so the paired test always has
`df=11`.

The exact 512x512 campaign is roughly 390 million render evaluations. A short
calibration on the documented Ryzen 5 4600H host measured about 1,450
evaluations/second at eight evaluation workers, so the queue takes on the order
of three days there. Keep `--max-jobs 1`: competing jobs do not increase this
six-core host's aggregate throughput and make wall-clock records harder to
interpret.

## Restart records

`-action collect` writes a third file, `-restarts` (the selected design's own
path, `docs/cmaes-restarts.csv` for `phase21`), holding one row per
independent run of every restart schedule in the campaign: `arm, block, seed, stage, restart, regime,
population, iterations, evaluations, bestCost, termination`.

It exists because the result CSV's `termination` column cannot describe a
restart arm. The schedule reports its own budget-exhausted reason whenever the
shared evaluation budget is spent, which for an arm sized to consume that
budget is always, so every restart arm records `completed` however its
individual runs ended. Only the per-run reasons distinguish a schedule that is
harvesting converged runs — `tol_fun`, `tol_x`, `condition_number` — from one
paying for runs that stopped progressing long before they ended.

The trajectory CSV carries the matching `restart` column, taken from each trace
sample's optimizer diagnostics. It is the join key. Cumulative iteration and
evaluation counts run straight through a restart boundary, so without it a
trace cannot say which run produced a sample, and the evaluations a run spent
after its last improvement — the 40% of two Phase 21 arms' budgets that
motivated the stagnation question — cannot be attributed to a run at all.

Both are empty for a campaign whose checkpoints predate the adapter recording
them, which includes the Phase 21 campaign and the lambda screen. The restarts
file is still written, header only: an empty file says "this campaign has no
per-run record" where a missing one would be indistinguishable from a
collection that failed.
