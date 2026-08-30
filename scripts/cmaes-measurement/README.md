# CMA-ES measurement campaign

This driver owns the registered campaigns behind go-cma-es `PLAN.md` Phase 11
and this repository's Phase 23. It submits jobs through a running server so the
dashboard shows the queue and every active job. It refuses to overwrite a
manifest, which prevents an accidental second submission from corrupting the
paired design.

Six designs are registered, selected with `-design`:

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
- `budget-split` — six arms on a second fixture, 72 jobs, 12 blocks, seeds
  113001-113012. See below.
- `restart-ladder` — seven arms, 84 jobs, 12 blocks, seeds 111013-111024
  (deliberately the same as `stagnation`, so two repeated arms have to
  reproduce that campaign bit for bit and the recorded best cost is inside the
  design). See below.

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

**It returned that null.** Both contrasts retain under Holm, the primary at
`t` = -0.34 with six blocks won of twelve; see
[`docs/cmaes-stagnation-report.md`](../../docs/cmaes-stagnation-report.md). The
criterion stops 68 runs of 106 at `lambda` 20 and buys almost nothing: at
`lambda` 1024 the ladder is capped at three runs either way, and at `lambda` 20
the wasted share rose rather than fell — reversing the pilot's own reclaim
figure, which three blocks were too few to measure.

The pilot's ninth arm does not reappear here. `stopMinImprovement` is an
absolute cost threshold, it cannot transfer to a reference image whose costs
differ in scale, and the pilot found it reclaimed nothing anyway (82.1% against
the baseline's 80.8%) despite firing most often. A test enforces that no arm of
this campaign sets one.

```sh
./cmaes-measurement -action plan -design stagnation
./cmaes-measurement -action submit -design stagnation
```

## The budget-split screen

`-design budget-split` asks two questions at once, on a fixture nothing has been
measured on.

**Should CMA-ES be the default engine?** [`docs/cmaes-report.md`](../../docs/cmaes-report.md)
found separable IPOP beating MayFly's long run in 12/12 blocks and its r16 arm
in 11/12, both surviving Holm. Three objections were recorded against reading
that as a default change, and all three have since been answered with nulls:
`lambda` is indistinguishable at 20, 64 and 1024; separable covariance alone is
a null; and arming a stagnation criterion is a null, so the budget those arms
wasted was never a recoverable gain. What remains is that **every one of those
numbers was taken on eight circles of `example/MayFly-512.png`**. This design
repeats the decisive contrast on a photographic reference at twelve circles,
which is Task 10.

**How should a stage's budget be split?** That is Task 3, asked of CMA-ES
rather than of MayFly. A budget can be split three ways and every campaign so
far varied only the third:

| mechanism | field | behaviour |
| --- | --- | --- |
| warm epochs | `optimizerEpochs` | each epoch re-initializes from the incumbent |
| cold restarts | `optimizerRestarts` | independent attempts, scored best-of |
| IPOP | `restartStrategy` | the adapter's own ladder, doubling `lambda` |

Both wrappers are engine-agnostic — `internal/server/worker.go` wraps
`WithRestarts(WithEpochs(...))` around whatever `newStageOptimizer` built, and
`CMAESAdapter` implements `RunWithInitial` — so no adapter change was needed to
measure this, exactly as with the stagnation campaign.

| arm | shape |
| --- | --- |
| `mayfly-single`, `mayfly-r16` | the Phase 21 controls, re-baselined on the new fixture |
| `sep-single` | no splitting |
| `sep-e5` | five warm epochs |
| `sep-r5` | five cold attempts |
| `sep-ipop` | the Phase 21 winner's shape |

**The split count is five, not four.** All four CMA-ES arms must spend the same
budget or the contrast measures the budget instead of the split, and 6,350
generations (`6502400 / 1024`) factor as `2 * 5^2 * 127` — four does not divide
them and five does. The driver refuses a budget its splits do not divide, and a
test pins that every CMA-ES arm's `iters * popSize * epochs * restarts` is
exactly the cap.

Two contrasts are registered, so Holm corrects over the two questions rather
than the fifteen that six arms would otherwise produce: **`sep-ipop` against
`mayfly-r16` is primary** — the default question on a fixture it has never
seen — and **`sep-e5` against `sep-r5`** is Task 3's. That second pair is the
epoch-versus-cold-restart question itself; testing either split arm against
`sep-ipop` would compare it with a third mechanism instead. `sep-single` and
`sep-ipop` still run, as the unsplit and ladder shapes the two split arms are
read against, but neither carries a test of its own.

### The fixture

`example/Ref-512.png` is `example/Ref.png` halved by an exact 2x2 box average:
at a factor of two every output pixel is the unweighted mean of four input
pixels, so the result does not depend on any resampling library or its version.
It is a photographic portrait where `MayFly-512.png` is a graphic, so it carries
much higher spatial-frequency detail — which is the point, since a default that
only holds on one kind of image is not a default.

A design owns its fixture rather than taking it from `-ref`, because a campaign
run on a different image is not poolable with one run on the shared fixture and
a flag that silently changed it would hide that.

```sh
./cmaes-measurement -action plan   -design budget-split
./cmaes-measurement -action submit -design budget-split
```

## The restart ladder

`-design restart-ladder` asks how many independent basins a fixed budget can
buy, and whether buying more of them beats the shape that currently holds the
record on the shared fixture.

The record is **752.52**, set by the stagnation campaign's `sep-ipop` arm at
seed 111018. Two properties of it drive the design. It was reached at
1,224,704 evaluations — 19% of the cap — so four fifths of that run bought
nothing, and across the arm the best arrives at 57% of budget on average. And
IPOP doubles lambda at each rung, so a block affords two or three runs and
twelve blocks are about thirty converged searches in total. The record is the
minimum of roughly thirty draws from the basin distribution, not the product of
a deep search, so the obvious way to beat it is more draws rather than longer
ones.

Four rungs hold `lambda * restarts` at 2048 and give every run
`budget / 2048 = 3175` generations, so each spends the 6,502,400 cap exactly
while trading sampling breadth per generation against the number of independent
searches:

| arm | lambda | cold restarts | independent draws over 12 blocks |
| --- | ---: | ---: | ---: |
| `sep-r2-l1024` | 1024 | 2 | 24 |
| `sep-r8-l256` | 256 | 8 | 96 |
| `sep-r32-l64` | 64 | 32 | 384 |
| `sep-r64-l32` | 32 | 64 | 768 |

The product admits every power of two from 1024 down to 32. The campaign spends
its arms on span rather than resolution -- three rungs a factor of four apart
plus the extreme one -- because an arm costs about 1,740 job-seconds a block on
this fixture and the run had a fixed deadline. The dropped 512 and 128 rungs are
interior points of the same trend and neither carries a registered contrast.

2048 is the largest product that keeps the whole legal width reachable: the
last rung needs exactly `app.MaxOptimizerRestarts` cold restarts, and its
lambda of 32 is above `app.MinPopulation`. Lambda and the restart count
necessarily move together, so a rung difference belongs to the pair; what makes
it readable as the restart count is the lambda screen's existing null, which
found lambda at 20, 64 and 1024 indistinguishable on the mean.

Three restart-strategy arms run beside the ladder at Phase 21's shape.
`sep-ipop` and `sep-ipop-w60` repeat the stagnation campaign exactly, on its
own seeds, so their twelve cells have to reproduce bit for bit — that is the
ladder's validity check, the way the lambda screen's replication arms checked
Phase 21, and it is what licenses reading the two campaigns' rows against each
other. `sep-bipop-w60` is the arm nothing has measured: BIPOP alternates large
runs with small ones at randomized budgets and randomized-down sigma, which is
a mechanism for leaving a basin rather than refining one.

**The criterion on the BIPOP arm is structural, not a re-run of the stagnation
campaign's null.** go-cma-es gives the first large run a budget equal to the
whole schedule and reaches the small regime only after a large run finishes, so
an unarmed `bipop` job is IPOP under another name. `sep-ipop-w60` is the
control that separates the strategy from the criterion, and the window is the
same half-anchor the stagnation campaign selected on mechanism and then
measured, so nothing about it is chosen here. The ladder rungs deliberately
carry no criterion: their restart count is fixed, so ending a dead run early
cannot buy another one and would only leave the budget unspent.

Two contrasts are registered, so Holm corrects over two questions rather than
the twenty-one that seven arms would otherwise produce. **`sep-r32-l64` against
`sep-ipop` is primary** — lambda 64 is four times Hansen's default at this
dimensionality, so covariance still adapts, while 32 restarts is the most
independent draws available at a lambda that adapts reliably. It is named here
rather than picked from the ladder once the costs are in. **`sep-bipop-w60`
against `sep-ipop-w60`** is the strategy question at matched criterion. The
ladder trend and every other pairing stay exploratory.

The record question gets a column of its own — the minimum over blocks per arm,
against 752.52 — and it is an order statistic, not a test. Best-of-N favours
the high-restart arms by construction; that is the mechanism under examination
rather than a bias, but it does not carry a p-value. The design registers the
record, so `report` prints that column and the campaign-best verdict beside the
paired table; registering one is independent of whether a design also registers
contrasts.

Because it reports a record, this design pins its fixture to
`example/MayFly-512.png` and ignores `-ref`, for the reason the budget-split
section gives: a cost is not comparable across reference images, so a flag that
redirected the campaign would leave the record column comparing two different
problems.

```sh
./cmaes-measurement -action plan   -design restart-ladder
./cmaes-measurement -action submit -design restart-ladder
```

## The deep hunt

`-design deep-hunt` is the first design here that is not a hypothesis test. It
exists to beat a number: **752.5220120747884**, the best eight-circle cost this
repository has recorded on `example/MayFly-512.png`. It reports a minimum, and a
minimum is an order statistic, so the design is `descriptive` and **registers no
contrasts at all**. Nothing in its report is a p-value, and nothing in it should
be quoted as one.

### The knobs nobody had turned

`internal/app/cmaes.go` exposes four CMA-ES parameters. Every campaign before
this one varied `restartStrategy`, `covarianceMode` between `full` and
`separable`, lambda, and a stagnation window -- and lambda, covariance mode and
the stagnation window are all now measured nulls on the mean. The rest had never
been set by any campaign in this repository:

- **`covarianceMode: block`.** `app` pins `blockSize` to `ParametersPerCircle`,
  so block mode is eight 7x7 blocks, one per circle. It learns the coupling
  between a circle's own x, y, r, RGB and opacity while treating different
  circles as independent, which is the actual structure of this problem:
  `separable` throws the within-circle coupling away and `full` pays for 56x56
  correlations that mostly do not exist.
- **`initialSigma`.** Default 0.3, validated only as finite and positive. The
  adapter searches a unit box with its mean at 0.5, so sigma decides how much of
  the space generation zero sees -- and generation zero is where the record was
  decided: on seed 111018 restart 0 alone found 752.52 and nothing after it
  improved.
- **`activeCMA`.** Negative rank-mu adaptation, default on. It governs how fast
  the covariance contracts, which is what decides whether a run commits to a
  basin early.

### The arms

Nine arms, eleven blocks, 99 jobs, seeds 114001-114011. `sep-ipop` is the
control -- the configuration that holds the record -- and the other eight rows
divide into two kinds that have to be read differently.

Four are true single-factor rows against it, so an arm that wins names its own
cause: `blk-ipop` moves covariance alone, `sep-ipop-s015` and `sep-ipop-s050`
move `initialSigma` alone, and `sep-ipop-passive` moves `activeCMA` alone.

The other four are compound. `sep-l4096` drops IPOP and raises lambda;
`blk-l4096` does both and changes covariance on top; `sep-e8` drops IPOP and
splits the budget into epochs; `sep-warm-e8` does that and also sets
`initialSigma` and warm-starts from the record. They are exploratory rows: a win
by one of them is a lead for a registered campaign, not a finding about the knob
its name happens to mention.

| arm | covariance | restarts | lambda | iters | epochs | sigma | active | start |
| --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- |
| `sep-ipop` | separable | ipop | 1024 | 12288 | 1 | default | on | residual |
| `blk-ipop` | block | ipop | 1024 | 12288 | 1 | default | on | residual |
| `blk-l4096` | block | none | 4096 | 3072 | 1 | default | on | residual |
| `sep-l4096` | separable | none | 4096 | 3072 | 1 | default | on | residual |
| `sep-ipop-s015` | separable | ipop | 1024 | 12288 | 1 | 0.15 | on | residual |
| `sep-ipop-s050` | separable | ipop | 1024 | 12288 | 1 | 0.5 | on | residual |
| `sep-ipop-passive` | separable | ipop | 1024 | 12288 | 1 | default | off | residual |
| `sep-e8` | separable | none | 1024 | 1536 | 8 | default | on | residual |
| `sep-warm-e8` | separable | none | 1024 | 1536 | 8 | 0.05 | on | record |

### Why the budget is bigger

`huntBudget` is 12,582,912, which is 1.94x the fixed cap every other design
inherits. That cap exists to match Mayfly's 2048-iteration control, and the hunt
runs no Mayfly arm, so it has no reason to keep it. It also has a reason to drop
it: across the two committed restart CSVs, **57 of 57 runs at lambda 4096
terminated on `maximum_evaluations`**, at a mean of 585 generations, when lambda
2048 needs 1450 to converge and lambda 1024 needs 1126. The top of the IPOP
ladder has never once been allowed to finish. 3*2^22 divides exactly by 1024,
2048 and 4096, so every arm stays evaluation-matched by construction.

`-budget` is both the arm sizer and the trace scoring cap, so the design now
owns the value and the flag can only assert it -- exactly as `-blocks` and
`-seed-base` already work. A campaign submitted at one budget and collected at
another would score every job against a cap it never ran under.

### The warm-start arm

`sep-warm-e8` is the only arm that exploits the record rather than trying to
rediscover it. `initialCircles` is the one operator-authored warm start in the
system and it works for a CMA-ES job in batch mode when `batchSize == circles`,
which is this design's shape. The specs are committed in `recordCircles()` with
their provenance -- job `2997714f`, seed 111018, cost 752.5220120747884,
produced under the `sep-ipop` configuration. The comment names the
configuration rather than a campaign on purpose: the stagnation campaign set the
cost and the restart ladder returned it bit for bit, so naming either alone
would be wrong.

Three things about it are worth knowing before reading its column. `app` refuses
an out-of-bounds `initialCircles` rather than clamping it, and two of the
record's values sit exactly on a bound, so a test asserts every one of them is
inside `fit.NewBounds`. Colours go through an 8-bit hex round trip, so the run
starts a hair off the recorded optimum. And it splits its budget with
`optimizerEpochs` rather than cold restarts on purpose: `RunOptions.Initial` is
consumed once, so restarts 2..8 would start cold, whereas each epoch reseeds
from the incumbent and keeps the search anchored near the record.

### Reading the result

Like the ladder, the hunt pins its fixture to `example/MayFly-512.png` and
ignores `-ref`. It has the ladder's reason and a second one: `sep-warm-e8` seeds
`initialCircles` from coordinates bounded by a 512x512 canvas, so a smaller one
would fail bounds validation at submit.

`reportDescriptive` prints a `vs record` column and a campaign-best line when a
design sets `record`. Three outcomes are all reportable: a cost below the record;
the record matched from new seeds, which would make it reproducible rather than
the n=1 it is today; or neither, in which case the `-restarts` CSV still answers
whether lambda 4096 finally converged and the sigma pair still answers whether
initialization matters.

Pairs that invite a test -- `sep-warm-e8` against `sep-e8`, `blk-ipop` against
`sep-ipop` -- are leads for a registered campaign, not findings. The arms are not
exchangeable draws from a common design and the headline is a minimum.

```sh
./cmaes-measurement -action plan   -design deep-hunt
./cmaes-measurement -action submit -design deep-hunt
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
driver refuses a block count that contradicts the design's own; the inferential
campaigns register twelve, so their paired tests have `df=11`. The stagnation
pilot registers three and the deep hunt eleven, and neither computes a t.

The exact 512x512 campaign is roughly 390 million render evaluations. A short
calibration on the documented Ryzen 5 4600H host measured about 1,450
evaluations/second at eight evaluation workers, so the queue takes on the order
of three days there. Keep `--max-jobs 1`: competing jobs do not increase this
six-core host's aggregate throughput and make wall-clock records harder to
interpret.

## The covariance campaign

`-design covariance` is the registered test of the deep hunt's strongest lead.
Three arms, twelve blocks, 36 jobs, seeds 116001-116012, on the same
`example/MayFly-512.png` eight-circle fixture and at the same `huntBudget` the
hunt ran.

| arm | covariance | restarts | lambda | iters | sigma | active |
| --- | --- | --- | ---: | ---: | ---: | --- |
| `sep-ipop` (control) | separable | ipop | 1024 | 12288 | default | on |
| `blk-ipop` | block | ipop | 1024 | 12288 | default | on |
| `sep-ipop-passive` | separable | ipop | 1024 | 12288 | default | **off** |

Both candidates are single-factor moves against the control, so an arm that wins
names its own cause, and a test asserts that directly rather than trusting the
table: each candidate must equal the control in every field except the one its
name claims.

- **Primary:** `blk-ipop` against `sep-ipop`. `covarianceMode: block` is eight
  7x7 blocks, one per circle, because `app` pins `blockSize` to
  `ParametersPerCircle`. The deep hunt found it better in 11 blocks of 11 by a
  mean of 77.24 — but that design registered no contrasts at all, so the
  difference has been observed and never tested.
- **Secondary:** `sep-ipop-passive` against `sep-ipop`. The deep hunt registered
  this exact arm and could not read it: ten of its eleven jobs were cancelled
  while queued, leaving n = 1. The campaign owes `activeCMA` a measurement.

Two arms and two contrasts, deliberately. The lambda screen crossed two factors
and manufactured thirteen contrasts out of eight arms, and Holm at that family
size retained a p of 0.0056; a design that names two pays for two.

### Why fresh seeds, and why the raised budget

The seed base is **116_000**, not the hunt's 114_000, and that is the opposite
of what the restart ladder did. The ladder shared the stagnation campaign's
seeds so its repeated arms would reproduce that campaign bit for bit, which is a
validity check worth having when the *new* arms carry the new evidence. Here the
arms are the same arms. Reusing 114_000 would re-report the eleven blocks that
produced the lead rather than test it, so the campaign draws seeds it has never
seen. 115_001-115_003 are avoided as well: they belong to the deep hunt's
warm-sigma probe, which is not a registered design and therefore invisible to
the seed-disjointness test.

The budget stays at `huntBudget`, 12,582,912. The lead came from the top of the
IPOP ladder — the hunt's block arm took its block best from the lambda 8192 rung
in six of eleven blocks, where the separable control never won above 4096 — and
that rung exists only at this budget. Run at `defaultBudget` the campaign would
be testing a different mechanism from the one that produced the lead. Both arms
share the number, so the contrast is evaluation-matched; it simply **cannot be
quoted against any campaign that ran at `defaultBudget`**, which is what
`docs/cmaes-deep-hunt-report.md` already says of the hunt.

### Running it

Sized from the deep hunt's own rates: its IPOP arms took a median 0.93h per job,
and 99 jobs finished in 09:07 of wall clock at `--max-jobs 7` on a 64-core host.
36 jobs is **roughly 3.5-5h**, so it fits inside a single day with room for the
collection.

```sh
just build && go build -o bin/cmaes-measurement ./scripts/cmaes-measurement
./cmaes-measurement -action plan   -design covariance   # read the table first
./circlefit serve --addr localhost --port 8085 \
  --data-root ./data/cmaes-phase11 --max-jobs 7 --queue-size 100 --input-root . &
./cmaes-measurement -action submit -design covariance
```

Four things the deep hunt learned the hard way are worth carrying over.

**Collect before anything stops the server.** `collect` reads job status over
HTTP, so a deadline guard that kills `serve` also ends the ability to collect;
run the collection first, or be prepared to restart `serve` on the same data
root.

**Do not cancel an arm to free workers.** The hunt cancelled ten queued
`sep-ipop-passive` jobs mid-campaign and lost the arm — which is why this design
has to measure `activeCMA` a second time. A queue is cheaper than a re-run.

**`collect` refuses a manifest it cannot complete**, and it refuses before
writing anything. A campaign that loses even one job has to be collected against
a filtered manifest, and the filtering then has to be stated in the report.

**The design is frozen at the commit the campaign is submitted from.** That is
the budget-split report's procedural lesson, and it applies to the contrasts
above: changing either one after partial results are visible would cost this
campaign its correction, exactly as it cost that one.

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
