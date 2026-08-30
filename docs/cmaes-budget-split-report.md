# How a CMA-ES budget should be split, and whether the engine alone wins

**Splitting the budget is what wins; swapping the engine alone is not shown
to.** Separable
CMA-ES run as one long search is statistically indistinguishable from MayFly's
sixteen-restart arm on this fixture (`t = +1.37`, `p = 0.20`, nine blocks won of
twelve). Every significant CMA-ES win in this campaign belongs to an arm that
splits its budget, and splitting an already-CMA-ES budget is itself worth
`t = +4.05` to `+4.79`. **What a default change that swapped the optimizer and
kept one long run would buy is undetermined here**: twelve blocks put the
engine-only gain at `+13.83` with a 95% paired interval of `[-8.40, +36.06]`,
which admits no benefit and a useful one alike. No equivalence margin was
registered, so the failure to reject is an absence of evidence and not evidence
of absence.

The registered primary contrast rejects: separable CMA-ES with IPOP restarts
beats `mayfly-r16` by `+36.36` (`t = +5.23`, `p = 0.00028`, 11/12 blocks) and
survives Holm. That reproduces [`cmaes-report.md`](cmaes-report.md)'s headline
on a **different fixture and a different circle count**, which was the last
standing objection to reading that campaign as being about the optimizer rather
than about one image.

**The secondary contrast is reported without Holm protection, and the reason is
a process failure rather than a statistical one.** The design's secondary was
changed after submission, while partial results were visible; the [registration
discrepancy](#a-registration-discrepancy) section documents it in full. Under
either version the conservative reading is the same — warm epochs versus cold
restarts is not settled here — but neither version's secondary should be quoted
as a corrected result.

**Ran 2026-08-29** on the 64-core campaign host, driver
`scripts/cmaes-measurement`, design `budget-split` (72 jobs, 6 arms, 12 paired
blocks, 169,739 job-seconds / 47.1 job-hours). Server binary stamped
`5c5b31c366cf1809253c1adabaf406be66f434d6`.

**Pins:** `github.com/CWBudde/go-cma-es v0.1.0` and `github.com/cwbudde/mayfly
v0.7.1` — the current tree. Every row is comparable with
[`cmaes-report.md`](cmaes-report.md),
[`cmaes-lambda-report.md`](cmaes-lambda-report.md) and
[`cmaes-stagnation-report.md`](cmaes-stagnation-report.md), and with a run made
today. **The fixture is not**, deliberately; see below.

## The design

Six arms, one shared 6,502,400-evaluation cap, twelve paired blocks. Block `b`
uses seed `113000 + b` in every arm.

| arm | optimizer | shape |
| --- | --- | --- |
| `mayfly-single` | mayfly `standard` | 1 x 2048 iterations |
| `mayfly-r16` | mayfly `standard` | 16 cold restarts x 128 iterations |
| `sep-single` | cmaes, separable, `none` | 1 x 6350 generations |
| `sep-e5` | cmaes, separable, `none` | 5 **warm epochs** x 1270 generations |
| `sep-r5` | cmaes, separable, `none` | 5 **cold restarts** x 1270 generations |
| `sep-ipop` | cmaes, separable, `ipop` | 6350 generations, shared over the ladder |

The three CMA-ES splitting arms are the three mechanisms the codebase actually
has, and they differ in what carries across the split. `optimizerEpochs`
re-initializes each epoch from the previous incumbent (`RunWithInitial`), so
information carries and the population size is fixed. `optimizerRestarts` runs
independent cold searches and keeps the best, so nothing carries and the
population size is fixed. `restartStrategy: ipop` is the adapter's own ladder,
which carries nothing **and doubles lambda each rung**. That last difference
turns out to be the whole story.

Five splits rather than four because 6350 generations divide by five and not by
four; the driver refuses a budget its splits do not divide exactly, so the arms
are evaluation-matched by construction rather than by rounding. Nothing in this
design locates an optimal split count — five was chosen for divisibility.

**The fixture is deliberately new.** `example/Ref-512.png` (a photographic
portrait, 512², committed with the design in #116) at **12 circles**, against the
graphic `example/MayFly-512.png` at 8 circles that every earlier CMA-ES campaign
fitted. It is `example/Ref.png` halved by an exact 2x2 box average — at a factor
of two each output pixel is the unweighted mean of four inputs, so the result is
independent of any resampling library or version. Changing both the image and
the dimensionality at once means this campaign cannot say *which* of the two
generalized the earlier result; it can only say that the result is not specific
to the exact earlier setup.

## Result

| arm | mean | sd | median | best | gain vs `mayfly-r16` | `t` (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| `mayfly-single` | 732.08 | 48.02 | 715.76 | 687.49 | n/a | n/a | n/a | n/a | n/a |
| `mayfly-r16` | 718.35 | 14.38 | 719.18 | 693.67 | control | control | control | control | control |
| `sep-single` | 704.52 | 37.44 | 701.30 | 652.54 | n/a | n/a | n/a | n/a | n/a |
| `sep-e5` | 678.88 | 25.93 | 673.69 | 652.54 | n/a | n/a | n/a | n/a | n/a |
| `sep-r5` | **662.04** | 20.49 | 659.26 | **631.46** | n/a | n/a | n/a | n/a | n/a |
| `sep-ipop` | 681.99 | 27.29 | 678.57 | 652.20 | **+36.36** | **+5.23** | **0.00028** | **reject** | **11/12** |

| contrast | gain | `t` (df=11) | p | blocks won |
| --- | ---: | ---: | ---: | ---: |
| `sep-ipop` vs `mayfly-r16` (**primary**, both registrations) | +36.36 | +5.23 | 0.00028 | 11/12 |
| `sep-e5` vs `sep-ipop` (secondary as *submitted*) | +3.11 | +0.30 | 0.76977 | 7/12 |
| `sep-e5` vs `sep-r5` (secondary as *merged*) | -16.84 | -2.29 | 0.04287 | 3/12 |

The primary rejects at a family-wise alpha of 0.05 under Holm on either
registration, because it is the smallest p in a family of two and 0.00028 clears
the 0.025 first gate by two orders of magnitude. **No Holm verdict is quoted for
the secondary**; see [the discrepancy section](#a-registration-discrepancy). The
uncorrected two-sided threshold at df=11 is `t = 2.20` and the Bonferroni one
over two is `t = 2.59`. Lower cost is better throughout, and a positive gain
means the candidate beat the control.

### A registration discrepancy

The binary that ran this campaign and the design that was merged into `main` do
not register the same secondary contrast, and the change was made while results
were on the table.

| | |
| --- | --- |
| 2026-08-29 16:52 | `5c5b31c` builds the driver; secondary is `sep-e5` vs `sep-ipop` |
| 2026-08-29 16:53 | 72 jobs submitted from that binary; the manifest is written |
| 2026-08-29 19:47 | `cf7a4a7` merges as #116; secondary is now `sep-e5` vs `sep-r5` |

By 19:47 roughly 31 of the 72 jobs had completed and their per-block costs had
been read, including the ones showing `sep-r5` as the leading arm. The rationale
recorded in `main_test.go` and the driver README is sound on its merits — Task 3
asks whether warm epochs beat equivalent *cold restarts*, and `sep-e5` against
`sep-ipop` answers that against a third mechanism instead, so the merged pair is
the better question. But a design change made after partial outcomes are visible
cannot be shown to be independent of them, whatever its stated reason, and the
merged pair happens to involve the arm those partial outcomes had singled out.

So this report treats **neither** secondary as carrying correction:

- The *submitted* secondary is the one the campaign was actually registered
  under. It retains (`p = 0.77`).
- The *merged* secondary is what `-action analyze` prints today and what a
  reader will reproduce. It clears an uncorrected threshold (`p = 0.043`) but
  wins only 3 blocks of 12 — the same mean-versus-win-count mismatch that
  dissolved in the `lambda` screen and again in the stagnation campaign.

Both readings point the same way: **warm epochs versus cold restarts is not
settled by this campaign.** The primary is untouched by any of it — it is
identical in both registrations and was fixed before submission.

The lesson for the next design is procedural, not statistical: a registered
design must be frozen at the commit the campaign is submitted from, and any
later improvement to it belongs to the *next* campaign. The driver already
enforces write-once on the manifest; it does not yet stamp the design revision
into it, which would have made this collision impossible to introduce silently.

### Per block

| block | `mayfly-single` | `mayfly-r16` | `sep-single` | `sep-e5` | `sep-r5` | `sep-ipop` |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 833.45 | 730.30 | 701.71 | 682.43 | 648.92 | 696.47 |
| 2 | 692.11 | 700.76 | 671.48 | 663.28 | 671.41 | 659.29 |
| 3 | 784.01 | 732.72 | 702.37 | 674.90 | 637.27 | 658.11 |
| 4 | 797.48 | 713.46 | *689.50* | 672.14 | *689.50* | *689.50* |
| 5 | 713.16 | 719.90 | 747.50 | 694.42 | 687.75 | 675.08 |
| 6 | 687.49 | 713.87 | 770.37 | 743.41 | 667.07 | 652.20 |
| 7 | 718.35 | 707.90 | *666.08* | 655.47 | 645.69 | *666.08* |
| 8 | 734.31 | 747.28 | *708.75* | 672.54 | 649.25 | *708.75* |
| 9 | 710.91 | 721.26 | *700.89* | 654.12 | 631.46 | *700.89* |
| 10 | 691.27 | 693.67 | *652.54* | *652.54* | 651.44 | *652.54* |
| 11 | 688.92 | 718.47 | 760.99 | 706.48 | 682.64 | 742.91 |
| 12 | 733.56 | 720.58 | *682.06* | 674.84 | *682.06* | *682.06* |

Italicized cells are bit-identical to another arm in the same block. They are
not a coincidence and they are not a defect; the [mechanism](#mechanism) section
explains them, and they matter because an exact tie contributes a structural
zero to a paired difference rather than a sampled one.

## Exploratory contrasts

**None of the following was registered.** They are uncorrected and two-sided at
df=11, and they are reported because the campaign's most useful reading lives
here, not because they carry the design's inferential weight. Treat them as
hypotheses for a follow-up.

| contrast | gain | `t` | p | blocks won |
| --- | ---: | ---: | ---: | ---: |
| `sep-r5` vs `mayfly-r16` | +56.31 | +7.04 | 0.000022 | **12/12** |
| `sep-e5` vs `mayfly-r16` | +39.47 | +4.96 | 0.00043 | 11/12 |
| `sep-single` vs `mayfly-r16` | +13.83 | +1.37 | 0.198 | 9/12 |
| `sep-e5` vs `sep-single` | +25.64 | +4.79 | 0.00056 | 11/12 |
| `sep-r5` vs `sep-single` | +42.48 | +4.05 | 0.0019 | 10/12 |
| `sep-r5` vs `sep-ipop` | +19.95 | +2.20 | 0.0501 | 7/12 |
| `mayfly-single` vs `mayfly-r16` | -13.74 | -1.11 | 0.291 | 7/12 |

`sep-e5` vs `sep-r5` is in the [result table](#result) instead, because one of
the two registrations names it. It is uncorrected there for the reason given in
that section, so it is no stronger than a row of this table.

Three readings, in decreasing order of how much the data supports them.

**The engine alone is not shown to be the win.** `sep-single` against
`mayfly-r16` is `+13.83` with `p = 0.20` and a 95% paired interval of
`[-8.40, +36.06]`, so twelve blocks cannot separate an engine swap from no
change — nor rule out a gain two-thirds the size of the primary's. What follows
is that the *split* is what this campaign demonstrates, not that the engine
demonstrably does nothing. The registered primary rejects, but the arm that
carries it splits its budget, and so does every other CMA-ES arm that separates
from MayFly. The
two contrasts isolating the split — `sep-e5` and `sep-r5` each against
`sep-single`, same engine, same covariance mode, same cap — are `p = 0.00056`
and `p = 0.0019`. This is the campaign's firmest result and it is the one that
should shape a default.

**IPOP is the weakest of the three splitting mechanisms here**, and it is the
one Phase 21 measured. `sep-r5` beats it by `+19.95` and has the best mean and
best single result in the campaign. But `sep-r5` vs
`sep-ipop` is `p = 0.0501` with seven blocks won of twelve — the same
mean-versus-win-count mismatch that dissolved twice already in this program,
once in the `lambda` screen and once in the stagnation campaign. **Do not act on
this contrast.** Its interest is that the [mechanism](#mechanism) below is
recorded fact rather than inference, which makes it a well-posed question for a
registered follow-up rather than a fishing expedition.

**Restarts-over-budget did not replicate for MayFly on this fixture.**
`mayfly-single` vs `mayfly-r16` is `-13.74` with `t = -1.11` and seven blocks
won of twelve. The sign of the mean is the one that report predicts — the
restart arm is ahead on average — but three blocks carry all of it (1, 3 and 4,
at -103.15, -51.29 and -84.01) while the single long run wins the majority of
blocks, which is the mean-versus-win-count mismatch again and not a
reproduction.
[`restart-vs-budget-report.md`](restart-vs-budget-report.md) is a v0.6.0 result
on the eight-circle graphic; on twelve circles of a photograph under v0.7.1 it
is a null. That report's *method* conclusion stands and its number does not
transfer — which is what its own version caveat already said.

## Mechanism

### IPOP is budget-capped at two or three runs

`docs/cmaes-budget-split-restarts.csv` carries 34 per-restart records across the
twelve `sep-ipop` jobs — the first non-empty restart records for a non-IPOP-vs-IPOP
design in this repository. They say what the cost column cannot.

| runs in the ladder | blocks | best came from restart 0 |
| ---: | ---: | ---: |
| 2 | 2 | 1 of 2 |
| 3 | 10 | 5 of 10 |

The ladder doubles the population each rung — 1024, 2048, 4096 — so the third
run is entitled to four times the first run's evaluations and **terminates on
`maximum_evaluations` in every block that reaches it**, mid-search and without
having converged. The budget therefore buys two completed searches and one
truncated one, never five.

**In six of twelve blocks the best result is still restart 0's** — and since
restart 0 is separable CMA-ES at lambda 1024 from the block's seed, which is
exactly `sep-single`'s configuration, IPOP returns `sep-single`'s number to the
last bit. Those are the italicized `sep-single`/`sep-ipop` ties in blocks 4, 7,
8, 9, 10 and 12. Half the time, the entire restart ladder bought nothing.

`sep-r5` spends the same cap as five independent searches at a fixed lambda of
1024, all five of which run to their own termination. That is the difference
between the two arms, and it is the reason to ask the question properly.

The other italicized ties follow from the same reading: `sep-r5` matches
`sep-single` in blocks 4 and 12 because best-of-five happened to select a run
equal to the single long one, and block 10 is a three-way tie where splitting
changed nothing at all.

### An arm that stops early cannot spend its cap

The cap is shared, but the *spend* is not, and this is the honest qualifier on
"splitting wins".

Two columns are easy to conflate here, so the table below reports only one of
them. The **scoring cap** is the 6,502,400 evaluations the design budgets an
arm, and it is what every arm's `iters * popSize * epochs * restarts` is
constructed to equal. What the table shows is the **work observed**, the
`finalEvaluations` each completed job recorded, which differs from the cap for
two reasons that are accounting rather than search: a completed checkpoint
carries a `+3` offset, so a job that spends its whole allowance records
6,502,403; and `mayfly-r16` finishes every restart attempt it starts rather than
truncating the last one, so it deliberately runs a little past the cap. Its
100.5% is that overrun and not a cap violation.

| arm | mean `finalEvaluations` | min | max | share of the 6,502,400 cap |
| --- | ---: | ---: | ---: | ---: |
| `mayfly-single` | 6,502,403 | 6,502,403 | 6,502,403 | 100% |
| `mayfly-r16` | 6,533,123 | 6,533,123 | 6,533,123 | 100.5% |
| `sep-single` | 1,737,816 | 1,155,075 | 3,315,715 | **27%** |
| `sep-e5` | 4,992,088 | 4,431,875 | 5,646,339 | 77% |
| `sep-r5` | 6,379,182 | 6,038,531 | 6,502,403 | 98% |
| `sep-ipop` | 6,502,403 | 6,502,403 | 6,502,403 | 100% |

`sep-single` converges, trips `TolFun`, and stops after about a quarter of its
allowance. So the mechanism behind "splitting wins" is not that a split does
more with the same work — it is that **a split converts budget the single run
cannot spend into additional searches.** Under a fixed evaluation cap that is a
real and fair advantage, because the cap is what the operator pays for. Under a
fixed *wall-clock* budget the accounting differs, and `sep-single`'s 2.5
job-hours against `sep-r5`'s 8.9 is the figure to reason from instead.

The `sep-r5` vs `sep-ipop` contrast is not affected by any of this: 6.38M
against 6.50M evaluations is a matched comparison.

### Where the budget goes after the last improvement

Evaluations spent after a job's global best was last improved, as a share of
what it spent:

| arm | mean wasted share | worst block |
| --- | ---: | ---: |
| `mayfly-single` | 8.8% | 33.6% |
| `mayfly-r16` | 49.6% | 87.1% |
| `sep-single` | 21.5% | 65.5% |
| `sep-e5` | 13.4% | 73.9% |
| `sep-r5` | 32.3% | 85.9% |
| `sep-ipop` | **54.0%** | 86.3% |

This reproduces, on a third campaign and a new fixture, the pattern
[`cmaes-lambda-report.md`](cmaes-lambda-report.md) recorded: restart arms waste
30-57% and self-terminating arms waste far less. `sep-ipop` is the most wasteful
arm in the design and the best-performing MayFly arm is the second most
wasteful, so **waste does not order the arms by quality** and must not be read
as an available gain — which is exactly what
[`cmaes-stagnation-report.md`](cmaes-stagnation-report.md) established by
measurement when it armed a criterion against that waste and got a null.

### The sigma reading holds a third time

Across 9,663 CMA-ES trajectory samples, `sigma` spans `2.633e-01` to
`2.385e+28` while the identifiable `sigma * max(D)` never exceeds **1.3859**.
That is the third independent campaign to record it. A large recorded sigma is
gauge-dependent and is not evidence of a diverged search; cite
`distributionExtent`, never `sigma`.

## What this licenses, and what it does not

**Supported.** Separable CMA-ES *with its budget split* beats MayFly's restart
arm under an equal evaluation cap, on two different fixtures at two different
circle counts. The three objections
[`AGENTS.md`](../AGENTS.md) recorded against reading Phase 21 as a default
recommendation are now all discharged by measurement — lambda by
[`cmaes-lambda-report.md`](cmaes-lambda-report.md), the unarmed stagnation
criterion by [`cmaes-stagnation-report.md`](cmaes-stagnation-report.md), and the
single fixture by this campaign.

**Not supported.** Swapping the engine while keeping one long run: `p = 0.20`,
95% paired interval `[-8.40, +36.06]`. Not supported is not refuted — the
interval is wide enough to hold a worthwhile gain, and no equivalence margin was
registered that could have ruled one out.
Any claim that IPOP specifically is the right restart strategy — this campaign
found it the weakest of the three splitting mechanisms and explains why, but at
`p = 0.0501` on seven blocks of twelve it did not *establish* that. Any specific
split count: five was a divisibility choice and 1, 5 and the IPOP ladder are the
only points measured. Whether warm epochs or cold restarts is the better way to
spend a split — Task 3's own question — for the registration reason above.

**Recommended.** If a default changes, it should be *separable CMA-ES with cold
restarts at a fixed lambda*, not "CMA-ES". The IPOP-versus-fixed-lambda question
deserves a registered campaign of its own, with the split count as a second
factor, rather than being settled from this campaign's exploratory column.

## Limitations

- One fixture, one circle count, one canvas size, one backend. Twelve blocks.
- The fixture changed image *and* dimensionality together, so the campaign
  cannot attribute the generalization to either.
- Most of the interesting contrasts are exploratory. Only the primary,
  `sep-ipop` vs `mayfly-r16`, carries correction; the secondary was changed
  after submission and is reported uncorrected under both of its versions.
- The design was not frozen at the submitted commit, so the campaign has two
  registrations that differ in their secondary contrast. This does not touch
  the primary or any measured cost, but it is the reason the warm-versus-cold
  question is reported as open rather than answered.
- Six of twelve blocks contain at least one exact tie between arms, which
  reduces the effective information in the paired differences that involve
  `sep-single` or `sep-ipop`.
- `sep-single` spends 27% of the cap. Every contrast involving it is a
  cap-matched, not a spend-matched, comparison.
- Timings are specific to the 64-core campaign host at `--max-jobs 7`.

## Raw data

Three CSVs beside this report, written by `-action collect`:

- [`cmaes-budget-split-measurement.csv`](cmaes-budget-split-measurement.csv) —
  one row per job: arm, block, seed, cost, evaluations, iterations, elapsed.
- [`cmaes-budget-split-trajectories.csv`](cmaes-budget-split-trajectories.csv) —
  per-iteration cost and adaptation traces including `distributionExtent`.
- [`cmaes-budget-split-restarts.csv`](cmaes-budget-split-restarts.csv) — the 34
  `sep-ipop` per-restart records with each run's own termination reason.

`-action analyze` reproduces the result table from the measurement CSV alone,
with no server and no job directories:

```sh
go run ./scripts/cmaes-measurement -action analyze \
  -design budget-split -results docs/cmaes-budget-split-measurement.csv
```

## Reproducing it

```sh
go build -o ./data/budget-split/circlefit .
./data/budget-split/circlefit serve \
  --port 8091 --data-root ./data/budget-split --max-jobs 1 \
  --queue-size 100 --input-root .
```

In another shell — read the design before queueing it, because a manifest may
only be written once:

```sh
go run ./scripts/cmaes-measurement -action plan    -design budget-split
go run ./scripts/cmaes-measurement -action submit  -design budget-split
go run ./scripts/cmaes-measurement -action collect -design budget-split
go run ./scripts/cmaes-measurement -action analyze -design budget-split
```

The arm table, the fixture override and the block and seed bases are in
`scripts/cmaes-measurement/main.go`; `main_test.go` pins the registered shape of
the design, including that its fixture and circle count differ from the default
and that every arm's `iters * popSize * epochs * restarts` equals the cap
exactly. [`scripts/cmaes-measurement/README.md`](../scripts/cmaes-measurement/README.md)
carries the driver's full operating notes.
