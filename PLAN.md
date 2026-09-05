# CircleFit Implementation Plan

**This document is the open work only.** Completed phases are not summarized
here; what they decided and measured lives in [`docs/`](docs/README.md), their
tests, and git history. A completed marker in git records implementation for its
revision, not a fresh release-gate result.

Tasks are numbered `1`–`13` in priority order. **The old phase-dotted numbers
were retired with the phases that carried them**; the index below maps each open
task to the number it used to have, so an older citation — in `docs/`, a code
comment, a commit message, or a pull request — still resolves. A dotted number
that does not appear in the "was" column belongs to completed work and keeps its
historical meaning wherever it is cited.

> **Production-readiness caveat (2026-08-09 audit):** the remediation code,
> release-gating CI, and corrected documentation are present, but no gate counts
> as passed because its workflow exists. Task 1 removes this caveat.

## Rules for this plan

- Do not mark a check complete from its presence in code or CI configuration
  alone. Record the exact command or observed CI result for the revision.
- Performance claims carry host, workload, budget, and allocation counts.
- A measurement taken under a superseded dependency pin is not a baseline. See
  the Toolchain section of [`AGENTS.md`](AGENTS.md).
- Findings belong in `docs/`, not here. A task keeps only what is needed to
  decide and do it, plus a link.

## Index

| # | Task | P | Was |
| ---: | --- | --- | --- |
| [1](#task-1-final-release-verification-p0) | Final release verification | P0 | 14.13 |
| [2](#task-2-decide-whether-a-restart-strategy-arms-a-stagnation-criterion-p1) | CMA-ES stagnation criterion | P1 | 23.1 |
| [3](#task-3-spend-a-stages-budget-as-restarts-not-one-long-run-p1) | Restarts, not one long run | P1 | 15.11 |
| [4](#task-4-close-the-dirty-region-evaluators-end-to-end-check-p1) | Dirty-region end-to-end check | P1 | 15.7 |
| [5](#task-5-browser-bundle-and-documentation-sign-off-p1) | Dashboard sign-off | P1 | 17.11 |
| [6](#task-6-derive-the-evaluation-width-from-a-measurement-p2) | Evaluation width from measurement | P2 | 15.5 |
| [7](#task-7-refresh-post-fix-polishing-evidence-p2) | Refresh polishing evidence | P2 | 15.10 |
| [8](#task-8-bound-the-restore-path-resident-set-p2) | Bound the restore-path resident set | P2 | 17.12 |
| [9](#task-9-remaining-documentation-examples-and-observability-p3) | Documentation and observability | P3 | 13.15 |
| [10](#task-10-a-second-cma-es-fixture-p3) | A second CMA-ES fixture | P3 | 23.4 |
| [11](#task-11-remaining-opencl-optimization-tranches-p3) | Remaining OpenCL tranches | P3 | 11.13 |
| [12](#task-12-deferred-cpu-kernel-research-p3) | Deferred CPU-kernel research | P3 | 10.20 |
| [13](#task-13-prefix-aware-active-set-selection-p3-effectively-closed) | Prefix-aware active-set selection | P3 | 15.3 |

---

## P0 — release gate

### Task 1: Final release verification (P0)

- [ ] Observe every required CI gate — all supported cross-builds, generation,
      race, vulnerability, GPU compile, and core end-to-end — passing from a
      clean clone on two consecutive runs of the release candidate.
- [ ] Verify repository and release controls prevent producing or publishing a
      release while any required gate fails; document any administrator-enforced
      setting that cannot be expressed in workflow files.
- [ ] On a clean user machine, follow the README verbatim and complete a small
      CLI job and a server/UI job without workspace preparation.
- [ ] After those checks pass, remove the production-readiness caveat above and
      record the release gate as met.

---

## P1

### Task 2: Decide whether a restart strategy arms a stagnation criterion (P1)

[`docs/cmaes-report.md`](docs/cmaes-report.md),
[`docs/cmaes-lambda-report.md`](docs/cmaes-lambda-report.md), and
[`docs/cmaes-stagnation-report.md`](docs/cmaes-stagnation-report.md)
are the evidence. Read them first; their numbers are not repeated here.

**Answered: no.** The twelve-block campaign ran on 2026-08-29 and both
registered contrasts retain their null under Holm — the primary at `t = -0.34`
with six blocks won of twelve. The criterion fires as designed and does not
improve the fit, so arming one by default would be a behaviour change for every
existing CMA-ES restart configuration in exchange for nothing measurable.

The three questions the Phase 21 campaign raised are now all closed, and all
three returned nulls: neither `lambda`, nor separable covariance, nor a
stagnation criterion has a measured case for a default change.

- [x] Analyze the `stagnation` campaign and report it in
      [`docs/cmaes-stagnation-report.md`](docs/cmaes-stagnation-report.md),
      correcting the two registered contrasts together. Both retain. Raw costs,
      trajectories and per-restart records are committed beside it — the first
      non-empty restart files in the repository.
- [x] Decide whether a restart strategy arms a default stagnation criterion
      when the caller sets none. **It does not.** `stopStagnationIters` stays
      available and unchanged for a caller who sets it deliberately.
- [x] Not adopted, so no default to document. The window-only constraint stands
      if this is ever revisited: `stopMinImprovement` is an absolute cost
      threshold and cannot transfer to another reference image.

One methodological correction is worth carrying forward. The window was chosen
by a rule fixed before the pilot's data existed, which was the right procedure —
but it was applied to a quantity that proved unstable at three blocks, and
reclaimed budget reversed sign at `lambda` 20 between the pilot and the
campaign. The campaign's answer is unaffected, because it tests cost and cost is
a null at both levels. A future pilot that selects on a measured quantity needs
more than three blocks to do it.

### Task 3: Spend a stage's budget as restarts, not one long run (P1)

A budget-matched ladder over twelve paired blocks puts splitting a stage's
budget at about 160 cost points, winning every block at eight and sixteen
restarts; four restarts of 64 iterations beat one 2048-iteration run by 88
points on 15% of the compute. Mechanism and caveats are in
[`docs/restart-vs-budget-report.md`](docs/restart-vs-budget-report.md). The
ladder predates the v0.7.1 pin, so re-establish a baseline before comparing
anything new against its figures.

- [ ] Measure `--optimizer-epochs` against cold restarts at a matched budget
      before settling on any API. An epoch already re-initializes substantially:
      it advances to a fresh deterministic seed and, with no continuation
      profile, seeds only half the population around the incumbent. Every ladder
      arm ran `optimizerEpochs: 1`, so the comparison is unmeasured. **Ran
      2026-08-29 as `-design budget-split`** (72 jobs, 12 blocks), asked of
      CMA-ES rather than MayFly, since Task 10's fixture question and the
      default-engine question rode along on the same campaign. Both wrappers are
      engine-agnostic — `internal/server/worker.go` wraps
      `WithRestarts(WithEpochs(...))` around whatever `newStageOptimizer` built,
      and `CMAESAdapter` implements `RunWithInitial` — so no adapter change was
      needed. **The box stays open and the epoch-versus-restart question stays
      unanswered**: the campaign's secondary contrast was changed after
      submission while partial results were visible, so neither version of it
      carries correction. See
      [`docs/cmaes-budget-split-report.md`](docs/cmaes-budget-split-report.md).
      What the campaign *does* establish is that splitting a CMA-ES budget beats
      not splitting it (`p = 0.00056` and `p = 0.0019` against the unsplit arm),
      which is the ladder result reproduced under a different engine. The MayFly
      half of this box also stays open: a MayFly ladder would need its own
      campaign, and `mayfly-single` vs `mayfly-r16` was a null here (`t = -1.11`,
      7/12) on this fixture.
- [ ] Decide the surface once that comparison exists. A full restart differs
      from an epoch in independent re-initialization of the whole population
      plus best-of selection; if epochs already capture most of the gain, tune
      them rather than adding a second mode.
- [ ] Implement restarts for the base stage on the CLI, the job config, and the
      schedule format, keeping determinism per seed. **Audited 2026-09-05 while
      adding the budget-filling shape.** The CLI (`--restarts`), the job config
      (`app.JobConfig.OptimizerRestarts`, bounded by magnitude), the JSON API,
      the create form and its React island all carry the count in both shapes,
      and determinism per seed holds — attempts vary `SeedOffset` on a dimension
      of their own, and the extra attempts a filling schedule creates continue
      that sequence. **The schedule format is the half still missing**: its
      budget overrides are `iters`, `popSize` and `epochs`
      (`internal/app/schedule.go:134`), there is no `restarts` key, and a staged
      run therefore inherits the base config's count with no way to vary it per
      stage. That is what the extend-and-polish box below needs before it can be
      measured.
      **Closed 2026-09-05.** `steps[].restarts` is now an extend override
      carrying both shapes in its sign, applied to `OptimizerRestarts` on the
      staged configuration, so a document can vary the count and the cap per
      stage. `PlannedIterations` already read the magnitude, so the ITERATIONS
      column and the dry run's parameters column follow without further change —
      verified end to end, a step at `8` printing `8 restarts` and one at `-32`
      printing `restarts filling 32 × iters` against an unset step that still
      prints nothing. **It is refused on a polish step rather than accepted**,
      because a polish-only stage takes the branch at
      `internal/server/worker.go:547` and never runs the base optimizer, and the
      polisher is wrapped in `WithEpochs` alone — a restart count written there
      would be inert. So the extend half of the box below is now measurable and
      the polish half still is not: restarting a sweep needs the polisher
      wrapped first, which is a behaviour change to polishing rather than a
      format addition.
- [ ] Re-measure on a second reference image before changing any default. The
      ladder covered one image, `variant` standard, and the eight-circle base
      stage only.
- [ ] Measure whether extend and polish stages benefit. They start from a fitted
      vector rather than a cold population, so the collapse dynamics there are
      unmeasured. **The extend half is now expressible**: `steps[].restarts`
      landed 2026-09-05 in both shapes, so a document can put a ladder on the
      extend stages of a campaign without touching the base. **The polish half
      is not, and not for want of a format key**: the polisher runs under
      `WithEpochs` alone, so measuring restarts on a sweep means wrapping it in
      `WithRestarts` first and deciding what an attempt of a sweep even is — a
      whole sweep chain, or one sweep. Do that deliberately, not as a
      side effect of writing the campaign.
      **The extend half is registered 2026-09-05 as `-design extend-width`**,
      the first staged campaign in this driver and the first CMA-ES measurement
      here that is not a cold eight-circle batch. It seeds the standing record
      as a frozen prefix and asks how the next eight circles should be
      committed: `ext-w8`, `ext-w4`, `ext-w2` and `ext-w1` run one, two, four
      and eight extend stages, and `cold-w16` fits all sixteen from scratch at
      the same cap. Everything else is held: full covariance, `lambda` 64,
      budget-filling cold restarts, and an attempt pinned at 3,175 iterations
      so grouping width is not confounded with restart length. Twelve blocks,
      seeds 120001-120012, 6,502,400 evaluations per arm by construction.
      Primary contrast `ext-w1` against `ext-w8` — the `+1`-versus-`+8`
      question, which `docs/schedule-format.md` answered under a MayFly pin
      that no longer applies and which
      `docs/seed-variance-and-population-report.md` argues the other way.
      Registered alongside them: `ext-w4` and `ext-w2` against `ext-w8`, and
      `ext-w8` against `cold-w16`, which is the one that can invalidate the
      premise rather than answer the primary. **The polish half of this box is
      untouched by that campaign** and stays open for the reason above; a
      CMA-ES schedule cannot contain a polish step at all, because
      `polishingEnabled` is on `JobConfig.mayflyOnlyFields()`.
      **Two facts the campaign's pre-flights established**, both recorded here
      because they are properties of the system rather than of the campaign.
      `initialCircles` quantizes colour to eight bits, so a base seeded from the
      record starts at 728.382406870524 rather than 726.1984354654948 — a
      constant 2.184 that cancels between seeded arms and does not cancel
      against a cold one. And a continuation's evaluation and iteration
      counters are **cumulative**: an eight-stage probe reported 1,625
      evaluations at stage 1 and 12,839 at stage 8, so a campaign's spend is its
      final stage's counter and summing the stages overstates it by 4.5x.
- [x] Settle which restart *shape* a CMA-ES default would name. The budget-split
      screen established that splitting a CMA-ES budget beats not splitting it
      but could not order the three mechanisms, and it found the IPOP ladder
      budget-capped at two or three runs with the last always truncated.
      **Registered 2026-08-30 as `-design restart-ladder`**: four rungs holding
      `lambda * restarts` at 2048 on the shared eight-circle fixture, plus
      `sep-ipop`, `sep-ipop-w60` and the first `bipop` arm this project has
      measured. It runs on the stagnation campaign's seeds so its two IPOP arms
      have to reproduce that campaign bit for bit, and so the best recorded
      eight-circle cost — 752.52 at seed 111018 — is inside the design. Primary
      contrast `sep-r32-l64` against `sep-ipop`; secondary `sep-bipop-w60`
      against `sep-ipop-w60`. **The design is frozen at the commit the campaign
      is submitted from**, which is the procedural lesson the budget-split
      report had to record. **Ran 2026-08-30** (84 jobs, 04:01 of wall clock);
      both registered contrasts retain and the record was matched, not beaten.
      See [`docs/cmaes-restart-ladder-report.md`](docs/cmaes-restart-ladder-report.md).
      **The box stays open**, and for a specific reason the campaign discovered
      rather than assumed: its ladder arms spent only 29-44% of the cap, because
      each cold restart trips `TolFun` early and `optimizerRestarts` is a fixed
      count that cannot express "restart until the budget is gone". So the
      restart-count question is cap-matched but not spend-matched and remains
      unanswered. Closing it needs that shape first — which is a change to the
      restart wrapper, not another campaign on the current one.
      **That wrapper change is now made, and the box stays open because the
      campaign it unblocks has not run.** `optimizerRestarts` carries the shape
      in its sign: a positive count is exactly that many attempts, unchanged and
      bit-for-bit reproducible, while a negative count asks for the same cap —
      `abs(N) * iters` iterations — and spends it, starting a further cold
      attempt whenever a whole one still fits. It never overruns the cap, so it
      leaves only the last partial slot, a residue bounded by one attempt
      against the majority a fixed count can waste. Measured 2026-09-05 on a
      CMA-ES sphere, 4 dimensions, `popSize` 8, `iters` 200, seed 4242, at 32
      cold restarts: spend rises from 4179 of 6400 iterations (65.3%) to 6211
      (97.0%), and 32 attempts become 48. That is a scratch reading, not an
      asserted one; `internal/opt/restart_fill_cmaes_test.go` runs the same
      shape at 4 restarts and asserts the direction — that the engine converges
      early, that filling spends strictly more, and that it never overruns the
      cap — rather than pinning figures a library bump would churn.
      **Every attempt now leaves a restart record**, whichever shape ran it.
      Filling schedules did from the start; a fixed count did not, on the ground
      that its attempt count is recoverable from the configuration — true of the
      count and false of each attempt's cost, work and termination, which the
      covariance-clean campaign then needed and could not get. Corrected
      2026-09-05 alongside that report, which also repaired the trace numbering:
      the offset a trace's restart index is shifted by *is* the number of
      records accumulated, so a shape recording nothing left every sample in
      that campaign reporting restart 0.
      **Registered and run 2026-09-05 as `-design restart-shape`.** Three arms,
      24 blocks, 72 jobs, seeds 119001-119024, every arm capped at
      `defaultBudget` and starting from `lambda` 64: an IPOP ladder (control),
      the ladder campaign's fixed 32 cold restarts, and the same 32 as a filling
      cap. Primary contrast `full-fill-l64` against `full-ipop-l64` — the
      head-to-head between the two shapes that spend their cap, which is the
      choice a default faces; secondary `full-fill-l64` against `full-r32-l64`,
      the one genuinely single-factor pair, measuring what filling buys over
      bounding at one lambda.
      **A BIPOP arm was registered and removed before the campaign produced
      anything**, which is worth recording because the mistake is one this
      driver already knew about. go-cma-es gives a BIPOP schedule's first large
      run the whole schedule budget, so an unarmed bipop job is IPOP under
      another name — documented at `restartLadderArms` and enforced by a test
      that was written for that one design, which the new design walked past.
      Arming it needs a matched pair, and the primary's control has to stay
      unarmed to match the cold arms, so an honest BIPOP question costs two
      further arms, 48 more jobs and a third contrast Holm would charge the
      primary for. The guard is now cross-design. **Full covariance throughout, and that is a constraint rather than
      a preference**: a ladder's top rung is decided at run time, so the only
      way to guarantee no arm meets the rank-mu clamp mid-run is the one mode
      that never clamps — separable from `lambda` 64 would cross on its fourth
      rung and block on its fifth. The covariance-clean campaign measured block
      and separable indistinguishable at this rung, so nothing known is given up
      by pinning it.
      **Ran 2026-09-05, and it answers the task.** 72 of 72 jobs, 06:51 of wall
      clock, 52.0 job-hours. **The shape a default should name is budget-filling
      cold restarts.** Filling beats the fixed count of 32 by `+6.89`
      (`t = +2.89`, `p = 0.0083`), which rejects under Holm, and the primary
      against the IPOP ladder is a null (`+3.82`, `t = +0.23`, 12/24). See
      [`docs/cmaes-restart-shape-report.md`](docs/cmaes-restart-shape-report.md).
      The secondary's mechanism is exact rather than argued: the two cold arms
      share a trajectory, so its paired differences cannot be negative — 14
      bit-identical ties, 10 wins, 0 losses — and the ten winning blocks are
      *identical* to the ten whose best came from a restart index of 32 or
      higher, which is the region a fixed count never reaches. The fixed arm
      spent 51.6% of its cap and the filling arm 97.8%, so the gain is free at
      matched evaluations.
      **Three things this does not close.** What tips the recommendation from
      "either" to "filling" is spread — sd 22.31 against IPOP's 71.36, range 88
      against 325 — and **no dispersion contrast was registered**, so that part
      is a lead rather than a result; the registered primary is underpowered,
      needing roughly 130 blocks to see a 20-point effect. IPOP is not retired:
      it holds the three best single results in the campaign. And the filling
      shape costs 37% more wall clock than the ladder at the same evaluation
      cap, because 99,367 small-population iterations carry more per-iteration
      overhead than 9,713 large ones — a real price the cap does not show.
      **It also opens a sharper question about the ladder.** IPOP reached
      `lambda` 4096 in 12 of 24 blocks and **never once took a block best from
      that rung**; its useful work concentrates at 512-2048 while the top rung
      is the one truncated by the cap. Where to *stop* a ladder is a knob no
      campaign here has varied, and it belongs to the top-rung task below.
      One operational note for a follow-up: `app.MaxOptimizerRestarts` bound the
      filling arm in 1 block of 24, so a filling shape at a smaller `lambda`
      would hit that ceiling routinely and the constant would need raising
      before the shape could be measured at all.
- [ ] Decide what the IPOP ladder's top rung is worth, now that one has been
      reached. **Ran 2026-08-30 as `-design deep-hunt`** (89 of 99 jobs, 09:07 of
      wall clock, 62.9h of optimizer time), a descriptive record hunt rather than
      a test, at `huntBudget` = 12,582,912 evaluations — 1.94x the cap every
      comparative campaign inherits, so **none of its costs is comparable to one**.
      It beat the record: **726.1984354654948**, superseding 752.5220120747884.
      See [`docs/cmaes-deep-hunt-report.md`](docs/cmaes-deep-hunt-report.md). Two
      results bear on this task. The lambda-4096 convergence question the ladder
      left open is discharged — at this budget 4096 converges on `tol_fun` in 32
      of 45 runs where 57 of 57 were previously truncated — but **the constraint
      simply moved up one rung**: lambda 8192, which no earlier campaign reached,
      is truncated in 33 of 33, and it is the rung that set the record, itself
      still cut off by the cap. So "restart until the budget is gone" is now
      wanted at the *top* of the ladder as well as the bottom. And the strongest
      lead the project has is unregistered: `covarianceMode: block` beat the
      separable control in 11 of 11 blocks by a mean of 77.24, with the block
      arm taking its best from the lambda 8192 rung in 6 blocks where the
      separable control never won above 4096. `activeCMA` stays unmeasured —
      ten of its eleven jobs were cancelled while queued.
- [x] Test the covariance lead the hunt could only observe. **Registered
      2026-08-30 as `-design covariance`**, to run the weekend of 2026-09-05:
      three arms, twelve blocks, 36 jobs, seeds 116001-116012, on the shared
      eight-circle fixture at `huntBudget`. Primary contrast `blk-ipop` against
      `sep-ipop`, secondary `sep-ipop-passive` against `sep-ipop`; both
      candidates are single-factor moves against the control, and a test asserts
      that field by field rather than trusting the table. Two things about the
      registration are deliberate and are the reasons to read
      `scripts/cmaes-measurement/README.md` before submitting. The seeds are
      **fresh**, not the hunt's — the opposite of what the restart ladder did,
      because here the arms are the same arms and reusing 114_000 would
      re-report the blocks that produced the lead instead of testing it. And the
      budget stays raised, because the lead came from the lambda 8192 rung and
      that rung exists only at `huntBudget`; the cost of that choice is that the
      campaign's numbers cannot be quoted against any campaign run at
      `defaultBudget`. Sized from the hunt's own rates at roughly 3.5-5h of wall
      clock at `--max-jobs 7`. **The design is frozen at the commit the campaign
      is submitted from.** **Ran 2026-08-30/31** — a week early, on request —
      submitted 21:58 and finished 03:20, 5h22m of wall clock, all 36 jobs
      completed with none cancelled or failed. **The primary contrast rejects**:
      `blk-ipop` beats `sep-ipop` by `+39.12` (`t = +2.72`, `p = 0.020`, 11/12)
      and survives Holm — about half the unregistered lead's size, with a
      standard deviation of 49.85 and one block reversing it by 84.38. The
      mechanism is the ladder: block converges every rung up to lambda 4096 in
      12/12 jobs and takes its block best from lambda 8192 in 7 blocks, where
      the separable control never does. The record was not approached (best
      746.9953 against the standing 726.1984). See
      [`docs/cmaes-covariance-report.md`](docs/cmaes-covariance-report.md).
- [x] Measure `activeCMA`. It was unmeasured for the **second** campaign
      running, and this time the cause was not operational. The covariance
      campaign's secondary contrast is **void, not null**: `sep-ipop-passive`
      returned costs bit-identical to its control in all twelve blocks because
      the knob is arithmetically inert wherever `go-cma-es v0.1.0`'s rank-mu
      clamp binds. The correction clamps the rate to `1 - c1`, which makes
      Hansen's positive-definiteness guard exactly zero, so every negative
      weight is scaled to nothing, and zeroes the covariance decay with it. The
      threshold depends on `lambda`, mode and dimension: separable above
      `lambda` 256 at 56 dimensions and above 512 at 84, block above 1024, full
      never. Fixed upstream in go-cma-es 0.2.0 (`CWBudde/go-cma-es` PR #3);
      **this repository has not taken that upgrade**, and doing so is a
      re-baselining campaign rather than a dependency bump, because it changes
      the update rules for every recorded figure.
      **No upgrade is needed to measure this**, though. Full covariance is clean
      at every `lambda` used here and block mode up to 1024, so a design can run
      on the current pin — but it must hold `lambda` below the threshold for its
      mode and buy restarts **cold rather than by IPOP doubling**, or the ladder
      climbs into the clamped regime and the contrast measures nothing for the
      same reason this one did. That also rules out the obvious shape:
      `blk-ipop` against `blk-ipop-passive` would differ only on its first rung.
      **Registered 2026-09-02 as `-design active-cma`**: two arms, twelve
      blocks, 24 jobs, seeds 111013-111024, on the shared eight-circle fixture
      at `defaultBudget`. `blk-r32-l64` against `blk-r32-l64-passive` — block
      covariance, 32 **cold** restarts at a fixed `lambda` 64, one registered
      contrast which is therefore the whole Holm family. **It is registered as
      cap-matched, not spend-matched**, which is this design inheriting the
      restart-ladder box's own open problem rather than dodging it: a fixed
      `optimizerRestarts` count cannot express "restart until the budget is
      gone", so a run that trips `TolFun` early returns its remainder to
      nobody. The ladder measured the identical `lambda` 64 schedule at 36.7%
      of the cap (34.4-39.9% across twelve blocks), so the expected spend is
      known, and the report must read `finalEvaluations` per arm — active and
      passive can converge at different evaluation counts, and an asymmetry
      larger than the ladder's five-point spread is a finding about the knob
      rather than noise. Every choice follows
      from the two failures: block because it is the mode a default would name
      and the only shippable one clean at a usable `lambda`; cold restarts
      because an IPOP ladder would double past the clamp and dilute the
      contrast to its first rung; `defaultBudget` because the raised cap exists
      for a ladder this design does not run, and the fixed cap is what lets the
      costs be read against the restart ladder's rows.
      **`lambda` 64 is the part the covariance report does not cover.** The
      knob's effect is not constant: its whole magnitude is the `negativeMass`
      scaling the negative weights, and that mass collapses with `lambda` well
      *before* the clamp binds — in block mode at 56 dimensions it is 0.281 at
      64, 0.0554 at 256 and 0.00155 at the shipped `popSize` of 1024. A
      campaign at the default population would be formally live and still apply
      a treatment 180x smaller, returning a null that says nothing. The driver
      now refuses such a rung: `activeCMAArms` computes the treatment and
      rejects anything below a floor, replicating go-cma-es's unexported
      derivation with a test pinning it to the values the covariance report
      read out of the library. One correction falls out of that test — where
      the clamp binds the surviving mass is about **1e-17, not the exact IEEE
      zero** the report describes, a near cancellation from summation rounding.
      It changes nothing that report concluded, and it is why the guard has a
      floor rather than a sign test.
      The seeds are the ladder's, and for a **weaker** reason than the ladder's
      own, stated as such: this design repeats no arm, so it earns no
      bit-for-bit check. What they buy is a by-product — `blk-r32-l64` runs the
      identical blocks as the ladder's committed `sep-r32-l64` cells, so the
      pair reads as block against separable at a rung where *both* modes are
      clean, which is the comparison the covariance campaign could not make.
      Cross-campaign and unregistered: a lead, never a finding. Sized at
      roughly 1-1.5h of wall clock at `--max-jobs 7`. **The design is frozen at
      the commit the campaign is submitted from.** **Ran 2026-09-02/03** —
      submitted 23:32, finished 00:48, 01:16 of wall clock, all 24 jobs
      completed with none cancelled or failed. **The registered contrast
      retains**: switching `activeCMA` off costs 23.79 on the mean and loses 8
      of 12 blocks, at `t = -1.70`, `p = 0.117`, against a paired standard
      deviation of 48.43. See
      [`docs/cmaes-active-cma-report.md`](docs/cmaes-active-cma-report.md).
      **The box closes anyway, because what it asked for was a measurement and
      it got one.** Unlike the covariance campaign's void, every block
      separates — by up to 90.38, in both directions — so the null is a
      measurement of the knob rather than of the clamp. It is absence of
      evidence and not a zero: the 95% paired interval runs from -6.98 to
      +54.56, so it admits a benefit twice the point estimate as readily as
      none, and at this variance an effect of the observed size needs roughly
      four times the blocks. Nothing licenses changing its default. The
      registered spend reading came back inside its own yardstick — 38.96% of
      the cap active against 41.59% passive, a 2.63-point asymmetry inside the
      ladder's 5.5-point spread — but that yardstick is a range from another
      arm, not a paired test, and the paired test on `finalEvaluations` is
      `t = 2.66` in 9 of 12 blocks. It is unregistered, so the spend question
      is open rather than answered in either direction.
      Two things carry forward rather than closing. `activeCMA` in **full**
      covariance mode is still unmeasured at every `lambda`, and full never
      clamps, so it is the other clean place to ask this. And the campaign's
      by-product is the more consequential result: `blk-r32-l64` shares the
      ladder's seeds, rung and budget with its committed `sep-r32-l64` cells,
      so block against separable can finally be read where **both** modes are
      clean — and block leads by only **+7.27** (`t` = 0.54, 7/12) against the
      +39.12 the covariance campaign registered at `lambda` 1024, where its
      separable control was clamped dead. Cross-campaign and unregistered: a
      lead, never a finding, and not a refutation. But it raises the question
      whether part of that +39.12 was separable being crippled rather than
      block being good, and **anyone proposing a covariance default has to
      answer it first.** That question is answered by the entry below.
- [x] Answer whether block covariance's registered win survives when separable
      is allowed to work. **Registered 2026-09-05 as `-design
      covariance-clean`, run the same night, reported in**
      [`docs/cmaes-covariance-clean-report.md`](docs/cmaes-covariance-clean-report.md).
      Four arms, 96 jobs, all completed: covariance mode crossed with a rung
      where both modes are clean (`lambda` 64) and the shipped rung where
      separable is clamped dead (`lambda` 1024), on one seed set and one cap.
      **It does not survive.** The registered primary is **-7.53**
      (`t = -0.82`, `p = 0.42`, 9 of 24) — the sign favours separable — and its
      95% paired interval, -26.55 to +11.49, **excludes the +39.12** the
      covariance campaign registered. Two independent clean-rung readings now
      exist, the active-CMA by-product's +7.27 at twelve blocks and this at
      twenty-four, straddling zero. **A covariance default has no case, and
      this task's blocking question is discharged in the negative.**
      What it does not settle is whether the clamp *explains* the +39.12. The
      interaction was registered before the data were read, precisely so that
      claim would carry its own p instead of being inferred from two verdicts —
      a review of #132 caught that inference as the difference-in-significance
      error while the campaign was still running. It came back inconclusive:
      **+15.11**, interval -33.63 to +63.84, 13 of 24. The cause is variance,
      not sign: the `lambda` 1024 arms carry a paired sd of 115.41 against the
      primary's 45.04, mostly from four blocks where `blk-r2-l1024` returned
      above 1000. An effect this size needs roughly 240 blocks against that
      spread. **A follow-up should cut the variance rather than buy blocks** —
      a rung nearer the clamp boundary still binds the clamp without the
      runaway. The clamp therefore remains the leading explanation on
      arithmetic grounds, and gains no measured support here.
      Three things carry forward. The unregistered lead that 32 cold restarts
      at `lambda` 64 beat 2 at `lambda` 1024 in separable mode by **+30.15**
      (`t = 3.38`, 19 of 24) agrees in direction with the budget-split report,
      and confounds `lambda` with restart count by construction — it belongs to
      the restart-shape question this task already carries, not to this one.
      `blk-r2-l1024` spent only **23.3%** of its cap against its control's
      36.0%, because a fixed restart count cannot refill what an attempt that
      trips `TolFun` leaves behind, so the secondary contrast compares two
      differently sized searches. And the campaign exposed two driver defects,
      both fixed alongside the report: a registered contrast between two
      non-baseline arms was corrected by Holm and then never printed, and a
      fixed restart count recorded nothing about its individual attempts —
      which also left every trace sample in this campaign reporting restart 0.

### Task 4: Close the dirty-region evaluator's end-to-end check (P1)

**Closed 2026-09-04.** Evidence is the dirty-region section of
[`docs/contiguous-window-polish-report.md`](docs/contiguous-window-polish-report.md);
its numbers are not repeated here.

The premise this task was written on was wrong. The 2,111-circle checkpoint was
still under `data/jobs`, and so was one fitted against the committed
`example/MayFly-512.png`, which is now the fixture. Exact parity holds
end to end: one complete sweep through each evaluator, three shapes, identical
budget and seed, bit-identical cost and all 14,777 parameters.

**Do not read that as licence to expect the 3.1x on a real job.** Under the
default `replacement` strategy the evaluator scores no candidate at all — a
merit selector ranks the huge, nearly transparent circles weakest, and those
are the ones that cover the canvas, so every candidate falls back correctly.
The 1.72x end-to-end win belongs to `contiguous-window`, which keeps the active
set positional and small. The original 599 s sweep is not reproduced and its
wall clock is not claimed: it fitted a reference the repository does not carry.

- [x] Preserve an immutable production-shaped checkpoint as a fixture:
      `internal/fit/renderer/testdata/polish-fixture-2111.json`, with its
      provenance and immutability rule in the `testdata/README.md` beside it.
- [x] Re-run that sweep at equal budget, record its wall clock, and confirm the
      cost it reaches is unchanged. `TestPolishFixtureDirtyVsFull` is the
      harness; it fails if no shape exercises the dirty path, because parity
      over a sweep that fell back on every candidate proves only that the
      fallback is exact. It runs for ~21 minutes, so it is opt-in behind
      `CIRCLEFIT_POLISH_FIXTURE=1` — the native-SIMD gates run this package
      without `-short`, and the harness outlives Go's 600 s panic timeout.
- [x] Record per-candidate cost against affected fraction in the report.
      `BenchmarkPolishDirtyCrossover` now sweeps eleven radii and adds a
      shipped-dispatch arm beside the forced one.

Two leads came out of it, neither acted on:

- **The 5% fallback gate is measurably too low at 2,111 circles.** The forced
  arm is still cheaper than a full render at 46% affected, and a fallback costs
  up to 29% more than dispatching straight to full. Raising the constant needs
  its own crossover per canvas size and circle count, and the one sweep that
  engages the evaluator today sits at 0.669% affected and would not benefit.
- **The re-measured absolute per-candidate costs do not reproduce the
  2026-08-21 table** on the same host with the same commands — roughly 2.5-3x
  faster across the suite. The cause was not identified. Compare ratios within
  a run, not figures across the two tables.

### Task 5: Browser, bundle, and documentation sign-off (P1)

- [ ] Capture and add the README dashboard screenshot on a working browser
      runner.
- [ ] Observe `just check` with npm available, including the bundle drift gate,
      and prove it rejects a stale committed bundle.
- [ ] Verify the dashboard shows correct stat tiles, ordered campaign cards,
      running jobs, and an architecture badge matching a forced
      `CIRCLEFIT_SIMD_TIER`.
- [ ] Start a campaign and observe its card move to running with a ticking
      chart.
- [ ] Check chart legibility in auto, forced-light, and forced-dark themes.
- [ ] Exercise all five campaign image modes, overlay opacity, and shortcuts
      `1`–`5`.
- [ ] With JavaScript disabled, verify the dashboard and campaign cost plot
      remain complete and readable.
- [ ] Kill and restart the server while viewing the dashboard; verify the client
      reconnects, refetches, and converges without a navigation.

Safari proper is not covered by CI — Playwright ships WebKit built for Linux.
Use the manual checklist in
[`docs/browser-support.md`](docs/browser-support.md).

---

## P2

### Task 6: Derive the evaluation width from a measurement (P2)

`EvaluationWorkers` defaults to `Threads`, clamped to `GOMAXPROCS`, so an
ordinary run uses one concurrent evaluation per hardware thread. That is the
core count talking, and the one host measured disagrees — see "The shipped
default is the core count, not this measurement" in
[`docs/polishing-throughput-report.md`](docs/polishing-throughput-report.md).

- [ ] Benchmark widths on more than one machine and more than one canvas size.
      One data point on one 12-thread box cannot pick a formula.
- [ ] Establish whether the rule is a fraction of `GOMAXPROCS`, a fixed headroom
      below it, or image-size dependent, and replace the default with it.
- [ ] Document the chosen rule next to `EvaluationWorkers` with the measurement
      behind it, keeping an explicit setting authoritative.

**Acceptance checks:**

- [ ] A benchmark table shows sweep cost against evaluation width on the stated
      machines, and the default the code picks is the width that table
      recommends.
- [ ] An explicitly configured `EvaluationWorkers` is still honored up to the
      `GOMAXPROCS` clamp, with a test covering it.

### Task 7: Refresh post-fix polishing evidence (P2)

- [ ] Re-measure `BenchmarkPolishStrategyQualityAfterBatchFit` after the
      acceptance-gate correction and refresh
      [`docs/contiguous-window-polish-report.md`](docs/contiguous-window-polish-report.md).
      The old ranking was partly determined by which active set happened to
      cover inherited blocker circles.

### Task 8: Bound the restore-path resident set (P2)

The measurement and the mechanism are in the "Security and deployment" section
of [`docs/known-limitations.md`](docs/known-limitations.md): 1.34 GB resident
and a 135 s startup on a 4,358-job data root, proportional to all history rather
than to what is being served.

- [ ] Restore from the `CheckpointInfo` sidecar and load `BestParams` lazily on
      the paths that need a parameter vector (resume, extend, render, artifact
      download).
- [ ] Stop holding full `MetricHistory` for terminal jobs. The chart endpoints
      can read the trace on demand, or a downsampled summary can be persisted
      beside the checkpoint; only running jobs need the live series in memory.
- [ ] Serve `GET /` and `/api/v1/dashboard` from a cached sidecar index
      invalidated by checkpoint writes, rather than re-walking the job tree.

**Acceptance checks:**

- [ ] Resident set after restore is reported for 500, 2,000, and 4,000 persisted
      jobs and is sub-linear in job count; record the host and the data root's
      checkpoint and trace byte totals with the numbers.
- [ ] `GET /` and `/api/v1/dashboard` latency is reported at the same three
      sizes and does not grow with total job count.
- [ ] A restored job still resumes, extends, and renders identically — a resumed
      job reproduces the parent cost exactly.
- [ ] Job detail, campaign charts, and the trace download return the same series
      as before for a terminal job whose history is no longer resident.

---

## P3

### Task 9: Remaining documentation, examples, and observability (P3)

- [ ] Audit structured logging fields and levels, document logging
      configuration, and add measured slow-operation or progress logging where
      useful.
- [ ] Decide whether a disabled-by-default Prometheus endpoint is warranted
      before adding a new public surface.
- [ ] Add a focused `docs/getting-started.md` covering CLI, server, UI, API,
      artifacts, and common configuration from a clean installation.
- [ ] Curate small redistributable examples with documented settings and
      expected qualitative results; add a deterministic example script and an
      appropriate CI smoke test.
- [ ] Decide whether a separate public roadmap or issue-tracker page adds value
      beyond this plan and [`docs/known-limitations.md`](docs/known-limitations.md).

Badges, promotional screenshots, a walkthrough video, source-file copyright
headers, and a code of conduct remain optional publication work rather than
engineering tasks.

### Task 10: A second CMA-ES fixture (P3)

Task 2 is closed, and with it the last of the three objections recorded against
reading Phase 21 as licence to change the default engine: `lambda`, covariance
mode and the stagnation criterion are all nulls, so the budget those arms wasted
was never a recoverable gain and the 12/12 and 11/12 wins stand. **This fixture
question is now the only thing between that evidence and a default change**,
which is why it is being run before the rest of its P3 neighbours.

- [x] Only after Task 2: repeat on a second reference image and a different
      circle count. Everything measured before this was eight circles on one
      512x512 reference. **Ran 2026-08-29** as part of `-design budget-split`:
      `example/Ref-512.png` at twelve circles, carrying `sep-ipop` against
      `mayfly-r16` as its primary contrast. Canvas size was deliberately held at
      512x512 so a changed result is attributable to the image and the circle
      count rather than to three axes at once; generalising across canvas size
      is a separate question. **The primary reproduced**: `+36.36`, `t = +5.23`,
      `p = 0.00028`, 11/12 blocks, rejecting under Holm. See
      [`docs/cmaes-budget-split-report.md`](docs/cmaes-budget-split-report.md).

Phase 21's headline is therefore not specific to one image or one circle count,
and the fixture objection is discharged. **That is not by itself licence to
change the default engine.** The same campaign found the unsplit CMA-ES arm
indistinguishable from `mayfly-r16` (`t = +1.37`, `p = 0.20`), so the advantage
belongs to CMA-ES *with its budget split*, not to the engine. A default change
should therefore name a restart shape — and **that campaign has now run.** This
report recommended fixed-lambda cold restarts over the IPOP ladder on mechanism
but could not establish it at `p = 0.0501` with 7/12 blocks; `-design
restart-shape` asked the question spend-matched at 24 blocks and named the
shape: **budget-filling cold restarts**, which beat a fixed count under Holm and
tie the ladder on the mean at a third of its variance. See
[`docs/cmaes-restart-shape-report.md`](docs/cmaes-restart-shape-report.md). What
that leaves for this line of work is the engine question itself, unchanged, and
a ladder ceiling nobody has varied.

### Task 11: Remaining OpenCL optimization tranches (P3)

OpenCL is integrated, benchmarked, parity-tested, and documented on one vendor
GPU, with deliberate opt-in fallback.
[`docs/gpu-backends.md`](docs/gpu-backends.md) and
[`docs/gpu-performance-report.md`](docs/gpu-performance-report.md) are
authoritative.

**It stays experimental**, and the remaining reasons are coverage, not speed:
parity and throughput are established on one NVIDIA T550, AMD and Intel are
unmeasured for both, and there is no required real-device CI runner — the GPU
gate runs PoCL on a CPU. No optimization answers any of those.

Two tranches shipped: sessions share one device engine, and staged sessions
composite onto a retained canvas. The staged path went from a 26x/84x separated
loss to 2.5–4.8x faster than the CPU at 512², flat in retained depth.

**Everything below was justified by that gap, so each item now needs its own
measurement rather than an inherited one.** Three sub-items were answered and
closed without building: pinned parameter staging (upload is flat at ~10 µs from
K=1 to K=100 and latency-bound), `engine.poison()` (the shared degradation
record already discovers a lost device once per run), and a device-resident
retained-canvas handoff (the host needs the canvas on every stage boundary
regardless, so only the upload could be avoided — one image copy per stage
against the term the second tranche made flat).

A fourth was answered by building the instrument but not the feature: the
batched objective interface. A pipelined generation — every candidate its own
buffers, one host synchronization instead of λ — measures **1.1-1.4x slower**
than one blocking `Cost` per candidate, in all eight cells over two passes,
because the queue is in-order and the driver pays more for λ×3 resident buffers
than the host round trips cost. Measuring the launch floor directly then bounded
every possible batching scheme: at 512² the floor is ~32.6 µs of an 88.8 µs
evaluation, so a batch that removed **all** of it would win 1.58x at the renderer
level and less end to end. See
[`docs/gpu-performance-report.md`](docs/gpu-performance-report.md). This also
retires the reading that tranche 2's 63.1 µs fixed floor was overhead waiting to
be amortized — about half of it is per-pixel work.

Note before benchmarking any of this: whole-pipeline benchmarks fix K at 12 and
run eight evaluations per stage where a real stage runs hundreds, so they cannot
see the effects that matter. Use `BenchmarkStagedEvaluationAtDepth`.

- [ ] Reduce per-evaluation synchronization and memory traffic
  - [ ] Add a cost-only execution path that omits full output-buffer writes
        during optimizer evaluations and materializes the final or best image on
        demand. A 512² session still holds a 1,050,778-byte eager
        `image.NewNRGBA` for `renderImage` — about 14 µs of 220.6 µs — that only
        a `Render` caller needs.
  - [x] Design a batched objective interface so optimizer populations can share
        kernel launches and scalar synchronization. **Declined on measurement**,
        not built: bounded at 1.58x at best and measured slower in the one scheme
        built. The unexported `batchEvaluator` and its two benchmarks stay in
        `internal/fit/renderer/opencl` as the instrument, since the answer is
        device-specific.
- [ ] Optimize the render kernel based on profiling
  - [ ] Precompute radius squared, premultiplied color, opacity, and inverse
        opacity into aligned circle records.
  - [ ] Evaluate constant-memory circle parameters and device-specific preferred
        workgroup sizes.
  - [ ] Investigate order-preserving tile or bin circle lists so pixels skip
        non-overlapping circles.
- [ ] Preserve semantics and fallback behavior
  - [ ] Extend CPU/OpenCL parity across joint, sequential, and batch modes after
        each optimization.
  - [ ] Verify cache invalidation, lazy image materialization, cleanup, and the
        permanent CPU degradation path.
- [ ] Re-run the complete backend pipeline benchmark after each tranche
  - [ ] Record vendor-GPU before/after medians, allocations, session counts, and
        evaluation counts, as interleaved single passes rather than `-count=N`.
        PoCL is for lifecycle and allocation deltas only.
  - [ ] Run the same benchmark on supported AMD, Intel, and NVIDIA devices where
        available.
  - [ ] Document crossover points and retain an optimization only where
        profiling demonstrates a benefit.

### Task 12: Deferred CPU-kernel research (P3)

Bounded research follow-ups, not blockers for the selected production CPU path.
Everything already measured is recorded in
[`docs/rejected-optimizations.md`](docs/rejected-optimizations.md),
[`docs/exact-span-compositors.md`](docs/exact-span-compositors.md),
[`docs/fixed-point-geometry-formats.md`](docs/fixed-point-geometry-formats.md),
and [`docs/renderer-precision-measurements.md`](docs/renderer-precision-measurements.md).

- [ ] Re-derive `compositeSpanSSE2MinPixels` on a host that genuinely lacks
      AVX2. 24 is the pre-hoist crossover and is now a correct upper bound —
      hoisting can only move a crossover left — so it merely leaves some spans
      on scalar. It cannot be measured here: dispatch selects SSE2 only when
      AVX2 is absent, and neither `CIRCLEFIT_SIMD_TIER=sse2` nor
      `GODEBUG=cpu.avx2=off` changes the microarchitecture.
- [ ] Re-derive `compositeSpanNEONMinPixels` on ARM64 benchmarking hardware. 256
      is the pre-hoist crossover, measured on an Apple M5, and is an upper bound
      for the same reason. `BenchmarkCompositeOpaqueSpanNEONCutoff` is the
      command — `scalar`, `neon_hoisted` and `neon_rebuilt` arms at nine
      lengths, so one run yields both the new crossover and the setup the hoist
      removed. The ARM64 rows of `ci-native-simd.yml` cover correctness only,
      and emulated timings do not count.
- [ ] If the original Pascal/Delphi source becomes available, document its exact
      cost arithmetic and numeric/SIMD representations. Until then,
      [`docs/incremental-cost.md`](docs/incremental-cost.md) is the contract.

### Task 13: Prefix-aware active-set selection (P3, effectively closed)

Dirty-region scoring removed the premise. Ordinary evaluations no longer
rasterize the whole suffix or score the whole canvas, and the report's verdict
is to keep selection quality-driven. Reopen only if a new end-to-end profile
shows the prefix mattering again.

- [ ] If reopened: bias selection toward later draw slots when region energy is
      close, and ship it only with a measured quality comparison at equal
      optimizer budget on the same seed — not on the cost argument alone.

---

## Not scheduled

- **A shadcn/Vite SPA rewrite of the frontend.** Decided against and recorded in
  [`docs/frontend-spa-rewrite-decision.md`](docs/frontend-spa-rewrite-decision.md).
  Tailwind and shadcn are adoptable inside an island today.
- **Go→TypeScript read-model generation.** Evaluated against a real `tygo` run
  and rejected; the parity fixtures are the contract. See
  [`docs/typescript-read-model-generation.md`](docs/typescript-read-model-generation.md).
- **A macOS GPU backend.** No OpenCL on Apple Silicon and no Metal backend
  planned. The condition to revisit is an Apple Silicon runner that can gate
  parity; see [`docs/gpu-backends.md`](docs/gpu-backends.md).
- **CMA-ES polishing.** Polishing stays MayFly-only by decision, with the reason
  in [`docs/behavior-invariants.md`](docs/behavior-invariants.md). Reopen only
  if a CMA-ES base stage is measured to beat MayFly at an equal evaluation
  budget.
- **Per-client rate limiting.** Not carried forward for the trusted-local
  server; bounded admission and resource limits are the contract.
- **Dragonfly as anything but an expert-only alternative.** It loses all twelve
  blocks; see [`docs/dragonfly-poc-report.md`](docs/dragonfly-poc-report.md).
