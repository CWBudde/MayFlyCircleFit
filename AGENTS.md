# Repository guidelines

Concise working context for humans and coding assistants. The code and its tests
take precedence if this document goes stale.

## Read before broad changes

[`docs/README.md`](docs/README.md) indexes everything in `docs/`. These are the
ones that will change what you propose:

- [`docs/architecture.md`](docs/architecture.md) — package ownership and the
  CLI, server, renderer, persistence, schedule, and web-UI data flows.
- [`docs/behavior-invariants.md`](docs/behavior-invariants.md) — observable
  behavior that must stay explicit (backends, SIMD dispatch, parallel
  evaluation, polishing, determinism, early stopping, server trust boundary).
- [`docs/rendering-internals.md`](docs/rendering-internals.md) — the rendering
  hot path as implemented: canvas invariants, span geometry, SSD kernels, tier
  dispatch, and span compositing.
- [`docs/renderer-correctness.md`](docs/renderer-correctness.md) — the
  byte-exact parity contract every renderer change has to hold, and the two
  places where it deliberately does not apply.
- [`docs/rejected-optimizations.md`](docs/rejected-optimizations.md) — what was
  built, measured, and deliberately not shipped. Read before proposing a
  rendering or evaluation optimization; several obvious ideas are already
  measured losses.
- [`docs/restart-vs-budget-report.md`](docs/restart-vs-budget-report.md) — why
  a stage's budget is better spent as several independent cold runs than as one
  long run, and which interventions did **not** delay population collapse
  (population size, `NC`, `DanceDamp`, variant choice, longer budgets). Read
  before proposing a search-quality change.
- **Most measurement reports in `docs/` were taken under MayFly v0.6.0 or
  earlier, and v0.7.0 changed results for every variant, so none of their
  numbers is comparable to a run made today.** Read those for method and for
  what was ruled out; re-measure before citing a figure. See the Toolchain
  section. Ten reports are on the current pins and may be cited directly: the
  QMC screen, and the nine CMA-ES ones — `cmaes-report.md`,
  `cmaes-lambda-report.md`, `cmaes-stagnation-report.md`,
  `cmaes-budget-split-report.md`, `cmaes-restart-ladder-report.md`,
  `cmaes-deep-hunt-report.md`, `cmaes-covariance-report.md`,
  `cmaes-active-cma-report.md` and `cmaes-preliminary-report.md`, run in
  2026-08 and 2026-09 on MayFly v0.7.1 and go-cma-es v0.1.0 — the preliminary
  one on the code-identical pseudo-version that preceded that tag. Each states
  its own pins; trust that line over this one. The deep hunt and the covariance
  campaign are the exceptions to citability *within* that set: both ran at
  1.94x the shared cap, so their costs are not comparable to the other eight,
  though they are comparable to each other.
- [`docs/qmc-initial-population-report.md`](docs/qmc-initial-population-report.md)
  — `qmcInit` measured on the eight-circle batch stage at three population
  sizes. All six comparisons are null and the data bound any effect to about
  ±4-5%; restarts buy 10-20% in every arm and buy it equally. Read before
  proposing `sobol` or `halton` as a default, and for the worked example of an
  interim signal (-7% at fourteen blocks, p = 0.07) that vanished by forty.
- [`docs/aoblmoa-paper-fidelity-report.md`](docs/aoblmoa-paper-fidelity-report.md)
  — the v0.6.0 paper-faithful `aoblmoa` loses significantly to `standard` under
  restarts (evaluation-matched, `t = -3.01` over twelve blocks) and is a null
  result as a single long run, while spending 31% more evaluations per
  iteration. Read before proposing it as a default or re-running a variant
  screen that includes it.
- [`docs/cmaes-report.md`](docs/cmaes-report.md) — the complete twelve-block
  CMA-ES campaign. Separable CMA-ES with IPOP restarts beats MayFly's long run
  in 12/12 blocks (`+210.97`, `t = +5.04`) and its r16 arm in 11/12 (`+90.24`,
  `t = +4.87`), while full-covariance CMA-ES without restarts merely ties r16
  (`t = +0.18`) using 27% of the cap. The report makes seven paired contrasts
  and corrects them together (Holm, family-wise α = 0.05), so only three
  survive: both separable ones and `cmaes-ipop` against the long run.
  Restarts-over-budget reappears on the v0.7.1 pin with the expected sign and
  size (`+120.73`, `t = +2.42`), but at p = 0.034 it does not survive that
  correction — quote it as support, not as confirmation. **Do not read any of it
  as licence to change a default:** both IPOP arms spent about 40% of their
  budget after their last improvement because no stagnation criterion was set,
  the winning arm confounds covariance mode with restart strategy, and `lambda`
  is pinned to `popSize` and ran at 1024 against Hansen's default of 16. The
  `lambda` screen below answers the last two with nulls; the stagnation
  criterion is answered by `cmaes-stagnation-report.md`, also with a null. All
  three carried questions are therefore discharged, and the budget-split report
  discharges the single-fixture one on top of them — but that last campaign
  replaces them with a sharper caveat, that the unsplit CMA-ES arm is a null
  against `mayfly-r16` and the win belongs to splitting the budget. Read it
  before acting on this one. The report's sigma column is **not**
  evidence of a diverged search — sigma alone is gauge-dependent and the
  identifiable `sigma * max(D)` was never recorded; do not cite it.
- [`docs/cmaes-lambda-report.md`](docs/cmaes-lambda-report.md) — the twelve-block
  `lambda` x covariance screen that answers two of those three carried
  questions, and answers both with a null. Eight arms, ninety-six jobs,
  evaluation-matched by construction; all thirteen paired contrasts retain their
  null under Holm. `lambda` at 20, 64 and 1024 is indistinguishable on the mean
  (`lambda` 64 against 1024 is `t` = -0.04), and separable covariance *without*
  restarts is a null against full (`t` = +0.10), so nothing in the screen
  explains Phase 21's winner by its covariance mode alone — an undetected
  effect at twelve blocks, not a measured zero. **Read before proposing a `lambda`
  default or lowering `app.MinPopulation` to Hansen's 16; neither has a measured
  case.** Two by-products matter beyond the screen. Its `distributionExtent`
  column settles the sigma question by measurement rather than inference —
  `sigma * max(D)` never exceeds 1.52 across 17,593 samples while sigma spans
  fifty-two orders of magnitude — so cite that column and never sigma. And its
  three replication arms reproduce all thirty-six Phase 21 cells bit for bit
  across a different binary and a different concurrency setting, which is what
  licenses comparing the two campaigns' rows directly.
- [`docs/cmaes-stagnation-report.md`](docs/cmaes-stagnation-report.md)
  — the window-selection pilot and the twelve-block campaign that tested it.
  **Arming a stagnation criterion on a separable IPOP schedule does not improve
  the fit** — `bipop` was not in this design; it is measured in `cmaes-restart-ladder-report.md`. Both
  registered contrasts retain their null under Holm; the primary is `t = -0.34`
  with six blocks won of twelve, and the secondary's positive mean comes from a
  single outlying control block while it wins only four. The
  criterion fires — 68 stopped runs of 106 at `lambda` 20 — and buys almost
  nothing: at `lambda` 1024 the ladder is budget-capped at three runs either
  way, and at `lambda` 20 the wasted share *rose*. **Read before proposing that
  a restart strategy arm one by default; the 30-57% waste the earlier reports
  measured is not an available gain.** It also carries the first non-empty
  per-restart records in the repository, the `distributionExtent` reading that
  reproduces the lambda screen's sigma finding on new seeds, and a worked case
  of a three-block pilot whose mechanism result reversed sign at twelve.
- [`docs/cmaes-budget-split-report.md`](docs/cmaes-budget-split-report.md) — six
  arms and seventy-two jobs on a **second fixture**, `example/Ref-512.png` at
  twelve circles instead of the eight-circle graphic every earlier CMA-ES
  campaign fitted. Phase 21's headline reproduces — `sep-ipop` beats
  `mayfly-r16` by `+36.36` (`t = +5.23`, 11/12) and rejects under Holm — which
  discharges the fixture objection and, with `lambda`, covariance and the
  stagnation criterion all nulls, leaves nothing carried against that campaign.
  **It is still not licence to swap the engine.** Separable CMA-ES as one long
  run is indistinguishable from `mayfly-r16` (`t = +1.37`, `p = 0.20`); every
  significant CMA-ES win belongs to an arm that splits its budget, and splitting
  an already-CMA-ES budget is worth `p = 0.00056` and `p = 0.0019` against the
  unsplit arm. **A default change must therefore name a restart shape, not just
  an optimizer** — and which shape is open: the report recommends fixed-lambda
  cold restarts on mechanism (the IPOP ladder doubles lambda, so it is
  budget-capped at two or three runs, the last always truncated, and in six of
  twelve blocks it returns the unsplit arm's cost to the last bit) but at
  `p = 0.0501` with 7/12 blocks it did not establish it. Two further cautions:
  the epoch-versus-cold-restart question — Task 3's own — is **unanswered**,
  because the design's secondary contrast was changed after submission while
  partial results were visible and neither version of it carries correction; and
  restarts-over-budget did *not* replicate for MayFly here (`t = -1.11`, 7/12),
  so `restart-vs-budget-report.md`'s number does not transfer to this fixture.
- [`docs/cmaes-restart-ladder-report.md`](docs/cmaes-restart-ladder-report.md)
  — seven arms, eighty-four jobs, asking whether buying more independent basins
  beats the shape that holds the record. **Both registered contrasts retain**
  (`t = -0.26` and `t = +0.76`) and the record was matched, not beaten. Its
  value is the mechanism, and it is narrower than the four bit-identical
  `lambda` 1024 cells make it look: **those four arms share one deterministic
  trajectory.** They run the same seed, `lambda` and initial sigma, their rows
  are identical through 1,270,784 evaluations, and the record is reached before
  that at 1,245,184 on restart 0 — so it is one trajectory reused, not four
  schedules converging. What the block does establish is one-sided and still
  worth having: that one `lambda` 1024 trajectory found the basin where 8, 32
  and 64 small-population draws on the same seed did not. It is **not** evidence
  that a population of 1024 reaches it reliably — `lambda` 1024 is searched in
  all twelve blocks and returns 752.52 in one. So it is a lead rather than a
  finding, but it means a mean-level `lambda` null does not settle the tail.
  **Read before designing another restart ladder:** these arms spent only
  29-44% of their cap, because each cold restart trips `TolFun` early and a
  fixed `optimizerRestarts` count cannot express "restart until the budget is
  gone", so the primary contrast is cap-matched but not spend-matched and the
  restart-count question is still open. Also carries the first `bipop`
  measurement here — 123 small and 33 large runs, the small regime reached in
  every block, best mean in the campaign and not significant — and the
  twenty-four cells that reproduce the stagnation campaign bit for bit across a
  different binary and `--max-jobs`.
- [`docs/cmaes-deep-hunt-report.md`](docs/cmaes-deep-hunt-report.md) — nine
  arms and 89 jobs that existed only to beat the recorded eight-circle cost, and
  did. **The record on `example/MayFly-512.png` is now 726.1984354654948**, from
  `blk-ipop` — block covariance under IPOP — superseding 752.5220120747884. The
  design is descriptive and **registers no contrasts, so nothing in it is a
  test**, and it ran at `huntBudget` = 12,582,912 evaluations, 1.94x the cap
  every comparative campaign inherits, so **none of its costs may be compared
  against a figure in the other CMA-ES reports**. Three things in it change what
  a proposal should say. `covarianceMode: block` beats the separable control in
  11 of 11 blocks by a mean of 77.24 — the strongest lead this project has, and
  an unregistered one, so read it before proposing either a covariance default
  or the registered campaign that would earn one. The lambda-4096 convergence
  question `cmaes-restart-ladder-report.md` left open is discharged: at this
  budget lambda 4096 converges on `tol_fun` in 32 of 45 runs where 57 of 57 were
  previously truncated, while lambda **8192** — a rung no earlier campaign ever
  reached — is truncated in 33 of 33, and it is the rung that set the record,
  itself still cut off by the cap. And a warm start from the old record beats it
  every time: eleven of them ended in a ~743 band, none of them near the new
  record. Read that as a fact about the warm start, **not** as evidence that the
  old record was a point on a slope — `warmStartSpecs()` quantizes colours to
  eight bits, so the run does not begin exactly there, and CMA-ES at sigma 0.05
  can leave a genuine local minimum. **`activeCMA` remains unmeasured** — its
  arm is n = 1 because ten of its jobs were cancelled while queued.
  `scripts/cmaes-measurement/main.go`'s `recordCircles()` and `recordCost` still
  carry the superseded solution; the report holds the new one.
- [`docs/cmaes-covariance-report.md`](docs/cmaes-covariance-report.md) — three
  arms and 36 jobs, all completed, testing the deep hunt's strongest lead.
  **`covarianceMode: block` beats separable and rejects under Holm** (`+39.12`,
  `t = +2.72`, 11/12) — but at about half the unregistered lead's size, with a
  standard deviation of 49.85 above a mean of 39.12 and one block reversing it
  by 84.38, so the direction is established and the magnitude is not. The
  mechanism is the restart ladder rather than the covariance model: block
  converges every rung up to lambda 4096 in 12/12 jobs, spends 47% of its cap at
  lambda 8192 against the control's 13.8% in only 8 of 12 blocks, and takes its
  block best from that top rung in 7 blocks where the control never does. It
  ran at 1.94x the shared cap, so its costs compare only against the deep hunt.
  **Read it before proposing a covariance default, and read the second contrast
  before trusting any separable measurement in this repository.** That contrast
  is **void, not null**: `sep-ipop-passive` returned costs bit-identical to its
  control in all twelve blocks because `activeCMA` is arithmetically inert in
  separable mode at this `lambda` in `go-cma-es v0.1.0` — the separable
  correction clamps the rank-mu rate to `1 - c1`, which makes Hansen's
  positive-definiteness guard exactly zero. The same clamp zeroes the covariance
  decay. **The clamp is a function of `lambda`, mode and dimension, not of the
  campaign**, and the report carries the verified boundary: separable is
  degenerate above `lambda` 256 at 56 dimensions and above 512 at 84, block
  above 1024, full never. So every separable arm at the default `popSize` 1024
  ran a memoryless update with active adaptation silently off — but the
  fixed-`lambda` arms at 32, 64 and 256 in the restart ladder did not, and
  neither did any full covariance arm. Its `t = +Inf` and Holm verdict are
  artifacts of a zero-variance paired difference and mean nothing. Two
  consequences carry: `activeCMA` is now unmeasured for the second campaign
  running, and is measurable **on the current pin** in full mode or in block
  mode below `lambda` 2048 provided the design does not let an IPOP ladder
  double past the threshold; and the primary contrast confounds covariance mode
  with active adaptation on one rung of four, bounded to 6% of the winning arm's
  budget.
- [`docs/cmaes-active-cma-report.md`](docs/cmaes-active-cma-report.md) — two
  arms and 24 jobs, the campaign that finally measures `activeCMA` after two
  campaigns failed to. **The registered contrast retains** (`-23.79`,
  `t = -1.70`, `p = 0.117`, 8 of 12 blocks favouring the knob), and unlike the
  covariance campaign's void every block separates, by up to 90.38 in both
  directions — so it measures the knob rather than the clamp. It is a bound
  rather than a zero: at a paired sd of 48.43 an effect of the observed size
  needs roughly four times the blocks. **Nothing in it licenses turning
  `activeCMA` on by default**, and the knob stays unmeasured in **full**
  covariance mode, which never clamps and is the other clean place to ask.
  Read it above all for the by-product, which bears directly on the covariance
  result: sharing the restart ladder's seeds, rung and budget, it reads **block
  against separable where both modes are clean** — and block leads by only
  `+7.27` (`t` = 0.54, 7/12) against the `+39.12` the covariance campaign
  registered at `lambda` 1024, where its separable control was clamped dead.
  Cross-campaign and unregistered, so a lead and not a refutation; but answer
  it before proposing a covariance default.
- [`docs/dragonfly-poc-report.md`](docs/dragonfly-poc-report.md) — the
  proof-of-concept Dragonfly v0.1.0 adapter loses all twelve blocks to MayFly
  `standard` in every arm, by 431.68 (`t = -16.81`) even when given more
  evaluations, and tripling its restarts moves it only 30 points. It stays
  available as an expert-only alternative; read before proposing it as a
  default or re-running the screen against v0.1.0.
- [`docs/seed-variance-and-population-report.md`](docs/seed-variance-and-population-report.md),
  [`docs/polishing-throughput-report.md`](docs/polishing-throughput-report.md) —
  base-stage quality does not predict the fit built on it; polishing session-pool
  and active-set tradeoffs. Both carry their own version caveats; heed them
  rather than reusing their numbers.
- [`docs/schedule-format.md`](docs/schedule-format.md) — the schedule document
  format, its worked example, the measured growth recipe (grow `+1` per extend,
  raise `popSize` and `optimizerEpochs` together, and which objective a scarce
  circle budget versus a scarce hour selects), and when two campaigns' costs are
  comparable.
- [`docs/support-matrix.md`](docs/support-matrix.md),
  [`docs/known-limitations.md`](docs/known-limitations.md),
  [`docs/troubleshooting.md`](docs/troubleshooting.md) — supported targets, CLI
  exit statuses, and the JSON API error envelope — and the release-verification
  task (Task 1) of `PLAN.md`.

## Architecture

`main.go` is the entry point and builds to `bin/`.

- `cmd`: Cobra commands (`run`, `serve`, `status`, `resume`, `checkpoints`,
  `version`).
- `internal/app`: canonical typed configuration, defaults, limits, and seed
  resolution shared by CLI, server, and persistence.
- `internal/fit`: image costs and architecture-specific SIMD dispatch.
- `internal/fit/renderer`: CPU renderer, SIMD kernels, and
  joint/sequential/batch pipelines.
- `internal/fit/renderer/opencl`: the cgo OpenCL renderer (`gpu` tag). It is a
  separate package because Go forbids Plan 9 assembly in a package that uses
  cgo; it must never import `internal/fit/renderer`. `internal/fit/gpu`
  enumerates platforms and devices and bootstraps the context;
  `internal/fit/renderer/renderer_opencl_gpu.go` is the gpu-tagged adapter that
  injects the CPU fallback and supplies the unexported session hook, so the
  dependency runs adapter -> opencl -> gpu and never back.
- `internal/opt`: optimizer interfaces; the MayFly v0.7.1 adapter, a
  proof-of-concept Dragonfly v0.1.0 adapter, and the pinned CMA-ES adapter.
  `JobConfig.optimizer` selects MayFly, Dragonfly, or CMA-ES from the CLI,
  server, schedules, and web creation form. CMA-ES exposes normalized initial
  sigma, full/separable/block covariance, active adaptation, and IPOP/BIPOP.
  Polishing remains MayFly-only.
- `internal/server`: trusted-local HTTP boundary and background job lifecycle.
- `internal/store`: filesystem checkpoint, trace, and artifact ownership.
- `internal/ui`: templ views plus committed generated Go output. Every page is
  server-rendered complete; that markup is the no-JavaScript fallback and the
  islands' hydration seed. The only inline script left in a `.templ` file is
  `layout.templ`'s pre-paint theme IIFE, which has to run before the first
  paint; `internal/ui/inline_script_gate_test.go` fails on any other.
- `web/`: the React island sources. Eight islands, registered in one place at
  the bottom of `web/src/dashboard.tsx`: `dashboard`, `job-list`, `job-detail`,
  `campaign-list`, `campaign-detail`, `create-job`, `settings`, and
  `theme-switch`. The image viewer is a shared component
  (`web/src/ImageViewer.tsx`) rendered inside the job-detail and
  campaign-detail islands, not an island of its own; job controls are part of
  the job-detail island.
- `internal/ui/static`: bundled JavaScript asset served from `go:embed`. The
  layout links it on every page, because the theme switch mounts everywhere.

Assets, fixtures, and notes live in `assets/`, `data/`, `docs/`, and
`profiles/`. Keep dependencies flowing toward the lower-level packages; do not
reintroduce application configuration into the store package.

### The GPU backend

Four things about OpenCL change what a proposal should say, and none of them is
visible from the package list:

- **It is experimental, opt-in, and absent from an ordinary build.** Only a
  `-tags gpu`, `CGO_ENABLED=1` binary has it. Without the tag `SupportedBackends`
  omits `opencl`, `serve --backend opencl` refuses to start, and a job naming it
  is rejected at submit — but `app.JobConfig.Validate` still accepts it on every
  build, deliberately, so a checkpoint written on a GPU host resumes there.
- **A GPU cost is not a CPU cost.** The device computes in float32 end to end
  against a float64 CPU path, so it is held to a measured budget (±2 per channel,
  1% relative cost) instead of the byte-exact contract in
  [`docs/renderer-correctness.md`](docs/renderer-correctness.md), and the cost
  bound grows with canvas size because the SSD accumulates in float32. Never
  compare a figure recorded under one backend against the other.
- **The label is not the record.** `Cost` and `Render` have no error return, so a
  device failure degrades the renderer permanently and silently to its CPU
  fallback. `effectiveBackend` and `backendDegraded` on the job are what say what
  ran; both are now persisted to the checkpoint and restored from it, so a job
  read back from disk still says which backend produced its cost.
- **Batching a population into one launch was measured and declined.** A
  pipelined generation — every candidate its own buffers, one host
  synchronization instead of λ — is 1.1-1.4x *slower* than one blocking `Cost`
  per candidate, because the queue is in-order and the driver pays more for the
  extra resident buffers than the host round trips cost. The launch floor was
  then measured directly and bounds every possible scheme: ~32.6 µs of an 88.8 µs
  evaluation at 512², so a perfect batch would win 1.58x at the renderer level
  and less end to end. Do not propose a `BatchObjectiveFunc` without a device
  that measures differently; the instrument to re-measure with is
  `BenchmarkOpenCLGenerationEvaluation`.
- **The staged pipelines are now the faster place to run, on a large enough
  canvas.** Two changes did it. Task 11.13 tranche 1 gave a renderer and every
  session derived from it one shared device engine — runtime, context, queue,
  compiled program, reference buffer and reduction workgroup size — so a stage
  no longer calls `gpu.InitOpenCL` and builds the program again; that moved
  sequential and batch 83.8x and 85.9x, from a separated 26x and 84x loss to
  indistinguishable from the CPU. Tranche 2 then gave staged sessions an
  accumulated canvas, so a stage composites its new circles onto the retained
  canvas instead of replaying every circle behind it. One such evaluation is
  flat in retained depth — 70-74 µs at 512² across a 64-fold depth change —
  which is 2.5-4.8x faster than the CPU's accumulated canvas, separated at every
  measured depth, and up to 22x faster than the replay it replaced. At 128²
  nothing separates: the canvas is small enough that the device is bounded by
  launch latency. Whole-pipeline benchmarks still cannot see any of this,
  because they fix K at 12 and run eight evaluations per stage where a real
  stage runs hundreds. Measured on one NVIDIA T550; AMD and Intel are unmeasured
  for both parity and throughput.

[`docs/gpu-backends.md`](docs/gpu-backends.md) carries setup, example commands,
the when-to-use-which table, the macOS decision, and the device quirks that make
a GPU measurement wrong;
[`docs/gpu-performance-report.md`](docs/gpu-performance-report.md) is the
measurement itself.

## Toolchain

- Go 1.24 source-compatibility floor (`go 1.24.0`). Production binaries should
  use a currently supported, security-patched release; vulnerability CI is
  pinned to Go 1.26.6.
- **MayFly v0.6.0 reimplemented AOBLMOA to match its source paper.** Results
  for a given seed differ from every earlier release, so any recorded `aoblmoa`
  measurement taken before this upgrade describes a different algorithm and
  must not be compared against a current run. The other six variants are
  unaffected. `aquilaWeight` is deprecated (unset now selects the paper's
  fitness test) and `oppositionProbability` is inert but still accepted. The
  faithful variant measures *worse* than `standard` on this problem; see
  [`docs/aoblmoa-paper-fidelity-report.md`](docs/aoblmoa-paper-fidelity-report.md).
- **MayFly v0.7.0 is a correctness release, and it changes results for every
  variant.** The library reworked its core update rules, so a given seed no
  longer reproduces the trajectory it produced under v0.6.0 — verified directly,
  not inferred: standard MA on a 10-dimension sphere at seed 4242 returns
  9.1756e-05 under v0.6.0 and 2.7848e-04 under v0.7.0. **Every measurement
  recorded in `docs/` was taken under v0.6.0 or earlier and is not comparable to
  a run on the current pin.** That includes the AOBLMOA, restart-vs-budget,
  Dragonfly and seed-variance reports. Their *conclusions* about method — spend
  a budget on restarts, do not size a population from v0.4.0 figures — carry
  over; their *numbers* are a different algorithm's. Re-measure a baseline
  before comparing anything new against a recorded figure. Resume enforces this
  on its own: a checkpoint recording v0.6.0 is refused by a v0.7.x binary.
- v0.7.0 also adds HMMA as a separately registered variant. This repository does
  not offer it yet: `app.variants` and `opt.supportedVariants` are unchanged, so
  a job naming it is refused at validation rather than silently run.
- **The MayFly pin carries quasi-random initial populations.** `qmcInit`
  selects `uniform` (the default and every earlier measurement's behavior),
  `sobol`, or `halton`. It is MayFly-only, refused under `dragonfly`, and an
  expert knob, and the sequence only fills the population slots residual
  seeding leaves free — normally half of them. It has now been measured on this
  problem and it is a null: see
  [`docs/qmc-initial-population-report.md`](docs/qmc-initial-population-report.md),
  which agrees with the library's own chance-level benchmark finding. `uniform`
  stays the default; do not read a single campaign as evidence for a sequence.
  See also [`docs/known-limitations.md`](docs/known-limitations.md).
- MayFly is pinned to `github.com/cwbudde/mayfly v0.7.1`. v0.7.1 is a lint and
  readability release and is **behaviour-neutral against v0.7.0**, verified
  directly: standard MA on a sphere at seed 4242 returns bit-identical costs
  under both, for `uniform`, `sobol`, and `halton`, at 10 and 56 dimensions. So
  a v0.7.0 measurement is comparable to a run on the current pin, which is not
  true of any earlier version. The resume guard knows that pair: v0.7.0 and
  v0.7.1 checkpoints resume under either binary, by an explicit allowlist of
  measured pairs in `internal/opt/resume_guard.go` rather than a semver rule.
  Templ is pinned to `github.com/a-h/templ v0.3.960` as a Go tool, and
  `github.com/google/pprof` as a Go tool because some Go installations do not
  bundle it.
- CMA-ES is pinned to `github.com/CWBudde/go-cma-es v0.1.0`, the library's first
  tagged release. It is the same search path as the pseudo-version
  `v0.0.0-20260825143954-e528faf326bf` this repository pinned before the tag
  existed — the intervening commits added a benchmark function suite, the
  WebAssembly demo, and the library's own version constant. Verified directly:
  Rosenbrock at seeds 4242 and 7 returns bit-identical costs, iteration counts
  and evaluation counts under both, in full and separable mode, at 5 and 14
  dimensions. That pair is the guard's second allowlist entry, so a checkpoint
  written before the tag still resumes. Any older CMA-ES revision is refused.
- **v0.1.0 has a measured defect, and the pin stays on it anyway.** Above a
  `lambda` threshold the rank-mu rate is clamped to `1 - c1`, which makes the
  covariance decay exactly zero — the matrix is rebuilt from each generation and
  remembers nothing — and makes Hansen's positive-definiteness guard exactly
  zero, so `activeCMA` is arithmetically inert whatever a job requests. The
  threshold is separable above `lambda` 256 at 56 dimensions and above 512 at
  84, block above 1024, full never. Because `popSize` defaults to 1024, most
  separable measurement in `docs/` was taken under those conditions, which is
  why the corpus is largely internally consistent and why `activeCMA` has never
  been measured here; the fixed-`lambda` restart-ladder arms at 32, 64 and 256
  are the exception and were not degenerate. Proven by measurement and by arithmetic in
  [`docs/cmaes-covariance-report.md`](docs/cmaes-covariance-report.md), and
  fixed upstream in go-cma-es 0.2.0.
  **Do not bump the pin as a tidy-up.** 0.2.0 changes the update rules, so every
  recorded CMA-ES figure in `docs/` becomes incomparable and would need
  re-baselining; the resume guard will refuse the version until the pair is
  added to `internal/opt/resume_guard.go` deliberately. Taking the upgrade is a
  campaign, not a dependency bump.
- `github.com/evanw/esbuild/cmd/esbuild` is installed as a Go tool to compile the
  frontend bundle, while `npm` is only used to fetch TypeScript dependency files.
- `internal/ui/*_templ.go` is generated and committed. After changing a `.templ`
  source, run `go tool templ generate` (or `just templ`) and commit the
  generated change alongside it.

## Commands

Prefer the `just` recipes in `justfile`: `just build`, `just run`, `just fmt`,
`just lint`, `just test`, `just test-coverage`, `just templ`, `just clean`, `just
bundle`.
`just check` covers generation drift, tests, vet, formatting, and the ordinary
build.

`golangci-lint` is configured in `.golangci.toml` (`default = all` minus a small
disable list, mirroring `MeKo/ewws-render`). `just golangci` reports, `just fix`
applies every automatic fix, and `just golangci-install` installs the pinned
version. The CI gate (`.github/workflows/ci-lint.yml`) reports only issues a
pull request introduces while the existing backlog is worked down, so it is not
yet in the release job's `needs:` list.

## Style

Idiomatic Go: gofmt tabs, exported identifiers in PascalCase, unexported helpers
camelCase, SCREAMING_SNAKE_CASE only for grouped constants. Mirror the existing
`internal/*` layering before adding packages. Cobra commands use short
imperative verbs (`resume`, `render`).

## Tests

Keep tests beside their package (`optimizer_test.go`). Favor table-driven cases
for optimizer inputs and assert behavior on CPU and GPU paths where applicable.
Maintain or improve coverage; if it dips, explain the gap and attach fresh
`just test-coverage` results. Put long-running optimizer fixtures in `profiles/`
with documented seeds.

Run the narrowest relevant package test while developing. Before handoff, run
the applicable subset of:

```sh
go tool templ generate
git diff --exit-code -- 'internal/ui/*_templ.go'
test -z "$(gofmt -s -l .)"
go vet ./...
go test -short ./...
go test -race -short ./...
go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
bash scripts/check-cross-build.sh
```

`go build` does not compile `_test.go` files, so none of the build lines above
says anything about a test file behind a `//go:build arm64` guard. Only
`check-cross-build.sh` does, by running `go vet ./...` for each supported target.
Run it whenever a signature crosses an architecture boundary: it is the only
*local* gate that type-checks ARM64 test files.

In CI the ARM64 rows of `ci-native-simd.yml` now run `internal/fit/renderer`
themselves, on Linux ARM64 and macOS ARM64, so an ARM64-only failure is caught
there too - but at run time and only after the branch is pushed. Two things
follow. Arithmetic that has to stay byte-identical across architectures must not
let the compiler contract a multiply-add; see the exact compositors and
[`docs/known-limitations.md`](docs/known-limitations.md). And the macOS ARM64
runner has three processors, so a test that hardcodes four threads or workers
fails there while every amd64 runner passes.

GPU checks require an explicitly prepared runner with OpenCL headers and
runtime; a CGO-disabled portable build is not GPU validation.

**Never describe a check or release gate as passing unless its command or CI
result was actually observed for the revision being discussed.** Include
benchmark conditions and allocation counts when claiming a performance change.

## CI

CI is split one gate per file. `.github/workflows/ci.yml` is an orchestrator
that only calls reusable `ci-<concern>.yml` workflows and holds the tag-gated
`release` job; edit the gate's own file, not `ci.yml`. `needs:` cannot cross
workflow files, so a gate becomes release-blocking only by being listed in
`release`'s `needs:`. `benchmarks` is deliberately excluded there because the
timing comparison is report-only. Reusable gates report as
`<caller job> / <job name>`, for example `generation / Generated UI is current`,
so anything naming a check by string must use the prefixed form. See
[`docs/releasing.md`](docs/releasing.md).

## Commits and pull requests

Use Conventional Commits with a scope where it helps (`feat(server): resume
endpoint`, `fix(renderer): preserve custom canvas`). Keep commits focused; do
not mix formatting or unrelated refactors with logic. Pull requests should link
the relevant `PLAN.md` task or issue, summarize impact, include renders or
metrics when optimization quality shifts, state which checks were run, and flag
reviewer setup needs.

`main` is protected: direct pushes are rejected and every change has to land
through a pull request. Work on a topic branch, commit there, and open the pull
request yourself (`gh pr create`) instead of pushing to `main`. If a commit
already sits on local `main`, move it to a branch and reset `main` back to
`origin/main` before pushing.

### Checking a pull request

`just check` is not the whole gate. Two things it does not cover fail pull
requests regularly, so run both before pushing:

- **golangci-lint.** `just lint` only runs `go vet` and a gofmt check, and
  `just check` does not lint at all, so neither says anything about the gate.
  `.golangci.toml` is `default = all`, and `lll` at 120 columns is the rule a
  new line trips most often. `.github/workflows/ci-lint.yml` runs with
  `only-new-issues: true` while the backlog is worked down, so what it reports
  is exactly the lines the branch adds. `just golangci` lints the whole tree and
  buries those in the backlog; ask it the same question CI asks instead:

  ```sh
  golangci-lint run --config ./.golangci.toml --new-from-merge-base=origin/main
  ```

  Use the pinned binary — `just golangci-install`, kept in step with
  `GOLANGCI_VERSION` in the `justfile` and with `ci-lint.yml`. An older one
  reads the same config differently and can miss what CI sees.
- **The browser matrix.** `just check` does not run Playwright. Run
  `npm run test:e2e` in `web/` when the change touches `internal/ui`, `web/`, or
  a handler that renders a page.

The gates are reusable workflows called from `.github/workflows/ci.yml`, so a
failure names the file to edit: a check reported as `lint / golangci-lint` comes
from `ci-lint.yml`, never from `ci.yml`.

Watch the run rather than assuming it passed, and read the job log rather than
the summary — the failing assertion is in the log:

```sh
gh run watch                       # or: gh run list --branch "$(git branch --show-current)"
gh api repos/{owner}/{repo}/actions/runs/RUN_ID/jobs --jq '.jobs[] | select(.conclusion=="failure") | .id'
gh api repos/{owner}/{repo}/actions/jobs/JOB_ID/logs
```

Keep the pinned action versions current when a run warns about them. Every
action has to run on a Node runtime GitHub still supports, and the deprecation
arrives as a warning on a *passing* run, which is easy to miss: as of the Node
20 retirement that means `actions/checkout@v5`, `actions/setup-go@v6`,
`actions/setup-node@v5`, `actions/upload-artifact@v6` (v5 is still Node 20) and
`golangci/golangci-lint-action@v9`. Bump every workflow file together — a single
lagging file keeps the warning on the run.

## Long-running experiments

Anything larger than a single quick run — a seed sweep, a parameter sweep, a
variant screen, a multi-stage campaign — should be observable while it runs.
Before starting one, offer to drive it through `serve` and hand over the
dashboard link, and say what the alternative costs. Jobs submitted to the
server appear in the web UI; runs launched straight from the CLI do not, so a
CLI-driven sweep is invisible until it finishes and has to be restarted to
become watchable.

Where the machine is reachable only over SSH, that includes setting up the port
forward and quoting the local URL, for example
`ssh -N -L 8642:localhost:8080 <host>` and then `http://localhost:8642/`.

## Runtime notes

The CLI reads reference imagery from `assets/`; pass relative paths in scripts
and docs. GPU acceleration is optional and off by default — note hardware
assumptions and fallbacks when updating those paths, and remember that CPU
fallback is opt-in (`--backend-fallback cpu`), so an unavailable device fails the
run unless the caller asked otherwise. Generated artifacts (`coverage.html`, resume
snapshots, `out*.png`) stay untracked; clean them before publishing branches.
