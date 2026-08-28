# Behavior that must remain explicit

Observable behavior that is easy to misdescribe or accidentally break. The code
and its tests win if this document goes stale, but changing any item here is a
deliberate decision, not a refactor side effect.

Rendering-side invariants live in
[`rendering-internals.md`](rendering-internals.md).

## Backends and modes

- CPU supports joint, sequential, and batch modes, including a supplied base
  canvas.
- OpenCL is experimental and supports joint, sequential, and batch modes. It
  requires a `gpu` build tag, CGO, OpenCL development headers, and a usable
  runtime and device. Staged modes create same-backend sessions that composite
  onto the retained canvas rather than replaying the prefix, and they must never
  silently fall back to a CPU staged renderer. That accumulated canvas is
  internal to a run and must stay opaque; a *job-level* base canvas
  (`canvasPath`) is still CPU-only and a job that asks for both is refused.
- **A backend that cannot start fails the job.** There is no automatic
  substitution. `backendFallback` is the only way to get one, it accepts only
  `cpu`, and it is unset by default, so every configuration written before it
  existed keeps failing loudly. The reason is arithmetic, not taste: the OpenCL
  path computes in float32 against a float64 CPU path, so a cost recorded under
  one backend is not a baseline for the other, and quietly producing CPU numbers
  under a GPU label would be worse than producing none. A fallback that did fire
  is recorded, not merely logged.
- **A backend that started and then failed keeps running, on the CPU, and says
  so.** The OpenCL renderer degrades permanently to its CPU fallback on a device
  error, because `Cost` and `Render` have no error return and cannot report one.
  That degradation must reach the job as `backendDegraded`, not only the log:
  the run has already spent part of its budget on the device, so its best-so-far
  spans both arithmetics and no reader can tell without being told. `Degraded`
  in `internal/fit/renderer` is the single accessor for this, in the same shape
  as `EvaluationWidth`, and reports false for every backend that cannot degrade.
- **Degradation belongs to the run, not to one renderer value.** The OpenCL
  renderer and every session derived from it share one degradation record. This
  is load-bearing: the staged pipelines evaluate each stage through a session of
  its own, so a per-renderer flag would let a sequential or batch run report a
  clean device while everything after the failure was costed on the CPU.
  Sharing also runs the other way, so a session created after the device is gone
  does not rediscover it, and a lost device costs one timeout per run rather
  than one per stage. That requires such a session to do no device work at all,
  not merely to report the degradation: it is built with no kernels, no buffers
  and no engine reference, because the creation of those is what fails once the
  device is gone, and a failure there would abort the staged run instead of
  letting it finish on the CPU.
- **A job records the backend it ran on, not the one it requested.**
  `effectiveBackend` is written once, where the renderer is built, and is the
  only value a comparison between two runs may use. Neither it nor
  `backendDegraded` is persisted to a checkpoint: they describe one process's
  run, so a restored job reports nothing rather than guessing.
- **What a build carries is not part of a configuration's validity.**
  `app.JobConfig.Validate` accepts `opencl` on every build, so a checkpoint
  written on a GPU host still resumes there. Availability is decided separately,
  from the build alone and without probing a device, by
  `renderer.BackendAvailable`: `serve` refuses an unavailable `--backend` before
  it listens, and a submitted job naming one explicitly is refused at submit.
  A backend inherited from the server default is not refused, because the
  operator was already told at startup, and neither is one that has a fallback.

## SIMD dispatch

- AMD64 SSD dispatch is tiered AVX2, then SSE2, then scalar, each after a
  runtime CPU-feature check; ARM64 SSD dispatch requires ASIMD before selecting
  NEON. Unsupported architectures use the scalar kernel. SAD remains scalar on
  ARM64 and on amd64 hosts without AVX2.
- `CIRCLEFIT_SIMD_TIER` forces any reachable tier for every kernel on every
  architecture, and `CIRCLEFIT_DISABLE_SIMD=1` remains its scalar alias. A forced
  tier is honored by dispatch but never by detection: `Tier()` reports what was
  asked for, and the tests that check detection strip both variables.

## Parallel evaluation

- `--parallel-evaluation` is opt-in and defaults off, because it trades cores
  for throughput rather than because it changes the answer. It leases one
  independent renderer session per concurrent evaluation and reproduces
  bit-identically for a fixed seed at any worker count — and, as of MayFly
  v0.7.0, bit-identically to the *serial* run of that seed as well, for every
  variant, Dragonfly, and CMA-ES. A campaign is therefore comparable across the
  setting. `TestParallelEvaluationMatchesSerial` and
  `TestDragonflyParallelEvaluationMatchesSerial` pin the selectable engines;
  `TestCMAESParallelEvaluationMatchesSerial` independently pins CMA-ES. A library bump that
  reintroduces divergence therefore fails rather than quietly invalidating a
  comparison.
- **Runs recorded before the v0.7.0 pin do not have that property.** Under
  v0.6.0 and earlier the serial male loop updated the global best mid-generation
  and steered the rest of it, while the parallel loop held it fixed; the two
  were different trajectories. A measurement from that era is comparable only
  against runs made with the same setting.
- `--evaluation-workers` sizes that pool and is separate from `--threads` on
  purpose: threads shard the rows of one render, evaluation workers run whole
  independent renders, and a pooled session always renders single-threaded. The
  two compete for the same cores instead of adding up. Measured scaling is in
  [`parallel-evaluation-report.md`](parallel-evaluation-report.md): 2.34× at
  128×128 but only 1.18× at 512×512, and below roughly four workers the flag is
  slower than the default. Do not describe it as an unconditional speedup. Each
  worker above one costs a full canvas and background copy, so the pool is
  clamped to `GOMAXPROCS`.
- `renderer.ParallelEvaluationOption` is the only place allowed to decide
  whether the optimizer runs its parallel path, and it decides from the
  renderer's reported width, never from a requested configuration value. A
  backend without independent sessions (OpenCL) must decline with a warning;
  enabling the optimizer's parallel path against a one-slot pool buys nothing
  and still changes the trajectory.
- Transactional polishing is re-entrant only through its session pool. Each
  sweep builds one pool of baked-suffix sessions, and every evaluation leases a
  slot carrying its own scratch parameter vector, so nothing is shared. The
  fixed prefix behind those sessions is rasterized once per sweep and every slot
  starts from a copy of that canvas; baking per slot would redraw the prefix
  once per worker and eat the throughput the pool exists to win. A width-one
  pool leases the sweep's own session and vector and is byte-identical to the
  serial sweep it replaced. `PolishCircleBatchContext` still rejects an
  optimizer reporting a width above one unless the backend both hands out
  independent sessions and advertises concurrent evaluation. Both are required:
  OpenCL can create sessions, but they share one device engine and one in-order
  command queue, and several of them evaluating at once has never been
  validated, so it withholds the parallel marker and is refused. Polishing
  errors rather than degrading to one slot when the pool comes up short, because
  the optimizer would still call the evaluator from every goroutine. The epoch
  and progress wrappers forward the width so neither check can be bypassed.
- Acceptance stays serial: a sweep is committed only after the merged candidate
  is re-evaluated on the full session and `sweepKeepsCirclesUseful` clears it.
  Pooling applies to candidate evaluation only.
- CPU polishing candidates are scored over the exact union of every active
  circle's incumbent and proposed raster. The incumbent image and its integer
  RGB SSD provide the constant remainder; affected pixels are restored from
  the baked-prefix canvas, suffix circles are recomposited only where they
  intersect the union, and signed delta SSD preserves `FastMSECost` exactly.
  The evaluator falls back to a full render above the measured 5% affected-pixel
  threshold, and an obviously canvas-sized disc is rejected by a conservative
  preflight before its span mask is built. This is CPU-only; unsupported or
  custom-cost sessions retain full evaluation. Acceptance still re-evaluates
  the complete vector independently of this optimization.
- `--fast-compositing` is opt-in and defaults off, because it changes the
  result of a fixed seed. It is accurate to +/-1 per channel, not byte-identical
  to the default compositor. Compare runs only against runs with the same
  settings.

## Polishing strategies

- Batch polishing bakes only the circles before the first active draw slot into
  a reusable canvas, so per-candidate cost is `circles - min(activeSet)`. The
  `replacement`, `hybrid-overlap`, and `residual-region` strategies select by
  image-space merit and routinely include circle one, which bakes nothing and
  rasterizes the whole vector for every candidate. `contiguous-window` selects a
  consecutive run instead, keeping that cost near `activeSetSize` on the first
  sweep of a partial pass. Do not change a selector to scatter its active set
  without accounting for that cost.
- `contiguous-window` is budget-aware and is not the default strategy. A budget
  below `ceil(circles / activeSetSize)` retains latest-first traversal so the
  partial pass stays as cheap as before. A budget that can cover the vector
  breaks equal-visit ties earliest-first, where a greedy fit leaves its early,
  large circles, and costs no more over the complete cycle. Consecutive
  `polishedFrom` stages of the same size and strategy inherit selection visits
  by replaying their checkpoint configurations; selected slots count even when
  a sweep is rejected. The selector remains seedless and deterministic.
  Measurements in
  [`contiguous-window-polish-report.md`](contiguous-window-polish-report.md)
  show why the synthetic jittered vector favors merit selection while the real
  1000-circle greedy fit favors positional coverage. Do not describe either
  result as unconditional or the isolated render-cost figures as an end-to-end
  speedup.
- Polishing has its own population, `polishingPopSize`, and does not inherit
  `popSize`. The two size different problems: `popSize` is chosen for the whole
  vector, while a sweep optimizes one active set — seven parameters per circle,
  35 at the default active-set size. An omitted `polishingPopSize` resolves to
  its own default rather than to `popSize`, which is deliberate and is the one
  place polishing departs from the `evaluationWorkers` compatibility rule: a
  checkpoint written before the field resumes at the polishing default, so a
  fixed seed reproduces such a run only when the field is set explicitly. The
  defaults come from [`polishing-budget-report.md`](polishing-budget-report.md),
  where quality is measured to be non-monotone in the population, so a larger
  inherited one is not a safe bet.
- The polishing acceptance gate is a non-regression rule, not an absolute one.
  `sweepKeepsCirclesUseful` requires every circle **in the active set** to be
  useful after the sweep — valid, changing at least one pixel, and contributing
  more than `minBatchMSEContribution` — and requires the set of non-useful
  circles **outside** the active set not to grow. A circle the sweep never
  touched and never made worse does not block acceptance.

  The rule used to be `allCirclesUseful` over the whole candidate, which also
  demanded that a sweep repair circles it had no agency over: inactive circles
  are copied through a sweep byte for byte. Fitted vectors routinely carry such
  circles, because `PruneCircleBatch` runs per stage against that stage's canvas
  while the gate is global over the final vector, and nothing re-audits the
  assembled result. That made polishing a guaranteed no-op past a certain size —
  measured on a real 64-circle fit as 3 blocking circles against an active set
  of 8 that reseeds one circle per sweep, so 12 of 12 sweeps were rejected for a
  net cost gain of 0.00. Do not restore the absolute form.

  On a vector with no pre-existing blockers the two rules agree exactly, which
  `TestSweepKeepsCirclesUsefulIsANonRegressionRule` asserts. The gate
  deliberately does not require an inherited non-useful circle's contribution to
  improve; those values drift as neighbours move, and demanding monotone
  improvement on uncontrolled circles would restore the stall.
- Every polishing sweep logs its acceptance decision at `INFO`: `cost` when the
  candidate did not beat the incumbent, `usefulness-gate` with the one-based
  `blocking_circles` when the gate refused it, `invalid-candidate` when the
  optimizer returned an out-of-bounds vector, and `Accepted polishing sweep`
  with `cost_removed` when it committed. A stalled run must stay diagnosable
  from the server log alone; do not reduce that to a single boolean.
- The incumbent's audit is cached across sweeps (`incumbentAuditCache`).
  `AuditCircleBatch` is one full render per omitted circle, both active-set
  selection and the acceptance gate need it, and a rejected sweep leaves the
  incumbent untouched, so it must be computed once per incumbent rather than
  once per consumer or once per sweep. A committing sweep does not drop the
  cache either: the acceptance gate has just audited exactly the vector that
  becomes the new incumbent, so the cache adopts that audit
  (`incumbentAuditCache.adopt`). No vector is ever audited twice as an
  incumbent.
- Active-set selection runs at the renderer's configured `--threads` width and
  must return exactly what it returned serially. The batch audit walks runs of
  the draw order on independent single-threaded sessions, and the
  `residual-region` influence loop stripes its circles across the same kind of
  sessions; both keep OpenCL serial through the concurrent-evaluation marker the
  evaluation pool already uses. Selection is a ranking, not a search: it takes
  no seed and its result is width-independent, so a change that made it depend
  on the worker count would be a bug, not a trade-off.
- The influence loop renders only the rows it reads. A circle writes pixels only
  inside its own raster, so removing it cannot change anything outside that
  raster; the comparison is `region ∩ circleRasterBounds` and a circle whose box
  misses the region is scored zero without rendering. Widening what
  `imageDifferenceEnergy` reads, or making compositing write outside a circle's
  raster, breaks that equivalence.

## Determinism, resume, and termination

- **A job runs the optimizer library its configuration names, and a resumed job
  runs the library its checkpoint names.** `config.optimizer` is `mayfly`,
  `dragonfly`, or `cmaes`, and an absent value is `mayfly`, so every
  configuration and checkpoint written before the field existed keeps its
  behavior. The engine is persisted in the checkpoint together with that engine's library version in
  `optimizerVersion`, and no resume path reads the engine from anywhere else:
  neither `resume --local` nor the resume endpoint can continue a run under a
  different optimizer than produced it. A setting the named engine cannot read
  is refused at validation rather than accepted and ignored, because a dropped
  setting makes the recorded cost impossible to compare. That refusal runs in
  both directions: a MayFly `variant`, `qmcInit`, `crossoverCount`, advanced
  knob, or polishing is refused under `dragonfly` and under `cmaes`, and the
  CMA-ES-only `initialSigma`, `covarianceMode`, `activeCMA`, and
  `restartStrategy` are refused under `mayfly` and under `dragonfly`.
- **`config.qmcInit` decides how a MayFly run draws its first generation, and
  an absent value is `uniform`.** `uniform` takes every coordinate as an
  independent draw from the run's generator; `sobol` and `halton` draw from a
  scrambled low-discrepancy sequence over the unit box the adapter normalizes
  into. It is MayFly-only and refused under `dragonfly` and `cmaes` alongside
  the other MayFly-only settings.
  **The sequence only fills the population slots seeding leaves free, and on
  this problem that is normally half of them.** Whenever the renderer can build
  a residual seed it passes it as `RunOptions.Initial` — unconditionally for a
  batch stage, and for a joint or sequential stage whenever residual seeding
  succeeds, which includes a cold base stage and not only a continuation. The
  adapter then expands that candidate into `ceil(popSize * 0.5)` male and
  `ceil(popSize * 0.5)` female seeds (the continuation profile's
  `LocalFraction`, default `0.5`), and MayFly fills only the remaining slots of
  each sub-population from the chosen sequence. So `sobol` and `halton` change
  how the unseeded half of the population is placed; they never give
  full-population coverage of the box, and the even-coverage argument for them
  applies only to that half. A run reaches the whole-population case only when
  residual seeding fails and the renderer falls back to optimizer
  initialization. Two properties have to hold together: a fixed seed still
  reproduces a run exactly under every strategy, because the sequence's
  scramble is drawn from the run's own generator rather than from the clock;
  and a quasi-random run is *not* comparable to the uniform run of the same
  seed, because a different starting population consumes the generator
  differently from the first iteration onwards. Seeds keep precedence over the
  sequence — a continuation still starts from its incumbent — and polishing is
  unaffected, because the polishing optimizer is constructed without the
  setting at all.
- Resume is restart-from-best: the MayFly v0.7.1 population is seeded with the
  saved best and deterministic nearby variations. It is not an exact restoration
  of optimizer internals. Server restart-from-best for sequential and batch jobs
  is not supported.
- A zero user seed generates and reports an effective seed; a nonzero seed is
  deterministic.
- Every checkpoint records the version of the optimizer that produced it, in
  `optimizerVersion`, and resume refuses to cross that boundary. A checkpoint
  whose recorded version differs from the one the running binary links is
  rejected — exit non-zero for `resume --local`, HTTP 409
  `optimizer_version_mismatch` for the resume endpoint — because the optimizer
  version decides which algorithm the continuation runs and the resulting cost
  is not comparable with the recorded one. The refusal is deliberately
  overridable: `resume --allow-optimizer-mismatch`, or
  `?allowOptimizerMismatch=true` on the endpoint, proceeds and warns instead. A
  checkpoint that records no version at all — every checkpoint written before
  the field existed — is never refused; it warns, naming the running version.
  The same applies when either side reports `unknown`, which is what a build
  without module information reports. Two pairs of versions are exempt, each
  measured to be behaviour-neutral so that a checkpoint written by either member
  resumes silently under the other: MayFly v0.7.0 and v0.7.1, and CMA-ES
  `v0.0.0-20260825143954-e528faf326bf` — the pseudo-version pinned before the
  library carried a tag — and its `v0.1.0` release. The exemption is an explicit
  list of measured pairs, not a rule about minor versions or about tags
  superseding pseudo-versions: every other pair, MayFly v0.6.0 against v0.7.x
  and any older CMA-ES revision against `v0.1.0` included, is still refused. A
  pair belongs on that list only once it has been measured, and it is scoped to
  the library it names — the MayFly exemption does not admit the same version
  strings under another engine.
- Optimizer termination reasons propagate from the adapter through the pipeline
  to jobs, checkpoints, `status`, and `checkpoints list`. The checkpoint
  `termination` field is free-form, so new reasons need no schema bump, and
  readers reject a version above 2.
- **A restart schedule's job-level termination describes the schedule, not its
  runs, and cannot be read as if it did.** The library ends the schedule with
  its budget-exhausted reason whenever the shared evaluation budget is spent,
  so a job sized to consume its budget records `completed` however its
  individual runs ended. The per-run record is separate and additive: each
  independent run's own reason --- verbatim from the library, so `tol_fun` and
  `condition_number` stay distinct rather than folding into
  `TerminationConvergence` --- with its regime, population, local iteration and
  evaluation counts and its own best cost, tagged with the pipeline stage that
  drove it, reaching `checkpoint.json` as an optional `restarts` field. It is
  absent for every optimizer without a restart schedule and for every
  checkpoint written before the field existed, which is why the schema version
  does not move. Each trace sample's optimizer diagnostics carry the matching
  `restart` index: cumulative counts run straight through a restart boundary,
  so that index is the only thing that says which run produced a sample.
- **A batch run spends the iterations its configuration asked for, and no
  more.** Every stage is a full optimizer run, refills included, so an
  unbudgeted refill is a silent doubling of a run's compute. Two things keep it
  from happening: a batch that improves the image is retained as the optimizer
  produced it, so a weak circle the audit would drop no longer leaves a hole
  that has to be refilled; and a refill of a batch nothing can be kept from is
  only started while the run's own budget --- its planned stages times the
  optimizer's iteration cap --- still covers it. An optimizer that does not
  declare an iteration cap leaves the loop bounded by `MaxExtraBatchStages`
  alone, as before. A run that stops short reports it in `actualCircles` and
  `refill_limit`, which is visible, rather than in an iteration count nobody
  budgeted, which was not.
- A batch that exhausts its bounded refill attempts reports `refill_limit` and
  remains a completed, continuable result at the number of circles it actually
  materialized. Job status and list resources expose both `requestedCircles`
  and `actualCircles`; `config.circles` remains the original target. Extending a
  short result rebases the inherited configuration to `actualCircles`, so an
  extension by N targets `actualCircles + N` instead of preserving the old gap.
  Only `refill_limit` authorizes this short-checkpoint contract; any other batch
  checkpoint whose parameter count does not match its configuration is invalid.
- A CPU batch extension may restore its immutable prefix from the completed
  parent's `best.png` instead of replaying every inherited circle. The artifact
  is only an optimization: its decoded dimensions and exact `FastMSECost` must
  match the checkpoint, and a missing or mismatched artifact falls back to the
  parameter replay. Checkpoint JSON remains the durable parameter source.
- **A restart does not inherit the previous attempt's basin.**
  `optimizerRestarts` runs independent cold attempts and keeps the best, which
  is what separates it from `optimizerEpochs`: an epoch reseeds from the best
  candidate found so far. A caller-supplied initial candidate is still honored
  by every attempt, so a resumed or staged run never discards work it was
  handed. Attempts vary the run seed on a dimension of their own, so epochs
  nested inside an attempt cannot alias onto another attempt's seed, and a
  restarted run stays reproducible for a fixed seed.
- **CMA-ES has two explicit, mutually exclusive restart forms.** With
  `restartStrategy: "none"`, `optimizerRestarts` retains the consumer's fixed
  number of independent cold attempts. With `ipop` or `bipop`,
  `optimizerRestarts` must be one and the CMA-ES library owns a single shared
  `iters * popSize` evaluation budget. A converged internal run releases its
  unused evaluations to a fresh CMA-ES run; IPOP grows the population and BIPOP
  balances large and small regimes. `optimizerEpochs` may repeat the complete
  schedule and therefore multiplies that budget deliberately. Progress and the
  returned iteration/evaluation counts are cumulative across internal runs,
  and stagnation is a per-run restart trigger under IPOP/BIPOP rather than the
  termination of the whole schedule.
- **CMA-ES settings are normalized and engine-specific.** `initialSigma` is a
  fraction of the adapter's [0,1] box, not a pixel or color-channel unit;
  omission means 0.3. Full covariance is the default and is limited to 512
  optimizer dimensions. `block` groups each circle's seven consecutive
  coordinates, while `separable` learns only diagonal variance. `activeCMA`
  defaults true and preserves an explicit false. Continuation-profile sigma
  controls the seeded first internal run; configured sigma remains the cold-run
  value for later IPOP/BIPOP restarts.
- **Polishing is MayFly-only, by decision rather than by omission.** A CMA-ES
  or Dragonfly job that enables polishing, or a schedule for either engine
  containing a polish step, is rejected during configuration validation, and
  the refusal explains the restriction instead of only naming the engine that
  owns the field. No stage silently switches engine.

  A sweep is not the job's optimizer applied to a subset of the circles. It is
  a fixed local search around the incumbent: every sweep hands the optimizer
  the same continuation profile -- `LocalFraction` 1, `Sigma` 0.02,
  `CoordinateRate` 0.2, `MaxVelocity` 0.02 -- and runs a `standard`-variant
  MayFly population with its own size, iteration budget, epoch count and
  stagnation window, whatever variant or engine the job names. `MaxVelocity`
  has no CMA-ES analogue and is not applied, so a CMA-ES polisher would be a
  different local search under an unchanged name, and the figures in
  [`polishing-budget-report.md`](polishing-budget-report.md) and
  [`contiguous-window-polish-report.md`](contiguous-window-polish-report.md)
  would stop describing the stage that ran.

  This is not a claim that CMA-ES could not polish. `PolishCircleBatchContext`
  accepts any `opt.Optimizer`, and its session-pool check reads the renderer
  and the configured evaluation width rather than the engine, so a CMA-ES
  polisher would pass it on exactly the terms the MayFly one does. What is
  missing is a reason to build one: no engine ranking is established on this
  problem (see [`cmaes-preliminary-report.md`](cmaes-preliminary-report.md)),
  so a second polishing engine would add a configuration surface, a cost
  projection and a checkpoint field for a stage nothing has shown needs them.
  The condition for reopening it is a measurement, not a request: a CMA-ES base
  stage that beats MayFly at an equal evaluation budget. Until then the answer
  is no rather than not yet.
- **Restarted progress stays monotonic.** Optimizer progress is best-so-far. A
  fresh attempt's early costs are worse than what an earlier attempt already
  reached, so only improvements are forwarded, and an epoch boundary carries
  the running best rather than the attempt's own result -- an observer that
  persists a checkpoint is never handed a candidate worse than one it already
  stored.
- **`crossoverCount` applies to optimizer stages, not polishing sweeps.**
  Polishing runs its own, smaller population, and an offspring count sized for
  the main population would exceed what that population can mate. Zero leaves
  the library's own scaling alone, so an unset configuration is unchanged.
- **An unset advanced knob is left to the library, and an explicit zero is
  not.** `danceDamp`, `aquilaWeight` and `oppositionProbability` are pointers
  because zero is a meaningful setting for each -- no dance decay, no Aquila
  step, no opposition step -- so a plain `float64` with `omitempty` would erase
  exactly the configuration the knob exists to express. Nil means the library
  default; a checkpoint written before these fields existed carries nil for all
  three and resumes unchanged. This is why they do not follow `crossoverCount`,
  whose zero is free to mean "unset" because the library refuses an offspring
  count of zero outright.
- **An advanced knob no variant would read is rejected, not ignored.**
  `aquilaWeight` and `oppositionProbability` belong to `aoblmoa`, so setting
  either on another variant fails validation. Accepting them would persist a
  setting into a checkpoint and report it back unchanged while it never reached
  the optimizer.
- **`oppositionProbability` is accepted, range-checked, and then ignored.**
  MayFly v0.6.0 moved opposition-based learning to the offspring stage and
  applies it to every offspring unconditionally, so no sampled share remains for
  this knob to set. It is deliberately not removed: job submission and schedules
  decode with `DisallowUnknownFields`, and resume reads the field back out of
  existing checkpoints, so dropping it would reject configurations that load
  today. `aquilaWeight` is likewise deprecated but still live -- an unset weight
  selects the paper's fitness test, and a value in `[0,1]` restores the
  pre-v0.6.0 probabilistic branch.
- **The advanced knobs apply to optimizer stages, not polishing sweeps.**
  Polishing runs its own smaller standard-variant population, which is not what
  an operator is tuning when they reach for these. `danceDamp` is enforced to
  [0, 1] here because the library does not range-check it at all. Above 1 the
  dance coefficient grows each iteration, though velocity and position clamping
  keep the run finite, so the bound guards against a saturated random walk
  rather than against divergence.
- The configured `variant` is honored at every optimizer construction site.
  All seven MayFly variants the adapter can build (`standard`, `desma`, `olce`,
  `eobbma`, `gsasma`, `mpma`, `aoblmoa`) are accepted by `JobConfig`
  validation, and `internal/opt` owns the contract test that keeps the two sets
  from drifting apart.
- MayFly's `optimization_started` and `iteration_completed` events are demoted
  to debug, so `--log-level=debug` emits one record per optimizer iteration.
  Info level stays at one record per optimizer run.

## Early stopping

Two distinct mechanisms exist and must not be conflated.

- **Stage-level convergence** (`--patience`, `--threshold`, `Convergence*`
  config) counts whole circles or batches, uses a relative improvement ratio,
  and applies to sequential and batch only; `OptimizeJointContext` discards it.
  Only this tracker reports `stage_convergence`.
- **Optimizer-internal convergence** is CMA-ES's own distribution-aware set
  (TolX, TolFun, TolXUp, condition number, no-effect axis and coordinate). It is
  always on inside the library and reports `convergence`, which counts as an
  early stop rather than as budget completion. MayFly and Dragonfly have no such
  criteria and never report it.
- **Optimizer-level stopping** (`--stop-*`, `Stop*` config) is evaluated per
  iteration inside one optimizer run, uses an absolute improvement, and applies
  in every mode. It is off by default, and `ApplyDefaults` must never fill those
  fields in, because a default run has to stay reproducible.
- In sequential and batch modes, optimizer-level stopping applies per stage. A
  run can stop early in many stages and still execute all of them; the reported
  termination is then `completed`, with the count in `stages_stopped_early`.

## Server trust boundary

`serve` is trusted-local software, not a network service for hostile clients. It
has no authentication or TLS. The default bind is `localhost`; foreign browser
origins are rejected; input image paths must resolve beneath configured
`--input-root` directories; request and image sizes and the job queue are
bounded. pprof is off by default and `--enable-pprof` requires a loopback bind.

- **Embedded assets on `/static/` are immutable and local-only.** The dashboard
  frontend bundle is compiled in `web`, committed into `internal/ui/static`, and
  served through `go:embed`. An asset that is not present under that embedded
  prefix is a `404`, and no path under `/static/` can escape that boundary into
  the host filesystem. Other routes are unaffected: `/`, `/jobs`, and the API
  endpoints continue to be served by the mux.
- **A written field is used as written; only an omitted one is defaulted.**
  `POST /api/v1/jobs` reads the raw body to see which keys the caller actually
  wrote, and refuses any of them that `ApplyDefaults` would replace — so
  `"circles": 0` is `400 invalid_config` naming `circles`, rather than a
  ten-circle run nobody asked for. It is the rule `ParseSchedule` already
  applies to a document's base stage, for the reason Phase 16 records: a
  silently dropped field costs hours before anyone notices. The corollary is
  that a client must *omit* a field it has no opinion on. Marshalling a
  zero-valued configuration struct writes explicit zeros and is a request for
  them, because nothing on the wire distinguishes the two.
- **The creation page keeps two admission paths, and they store the same
  configuration.** `POST /create` takes the templ form and `POST /api/v1/jobs`
  takes the island's JSON. The form POST handler is retained deliberately: it is
  what makes the page work without JavaScript, which the templ-fallback
  invariant below requires, and removing it would delete that invariant for the
  one page where a user creates work rather than reads it. The cost of keeping
  it is that the two paths represent "no opinion" differently — the form sends
  an empty string, which the handler resolves against the defaults before a
  configuration exists, while the API sends no key at all — and this is not
  allowed to reach the stored configuration. The island therefore omits a field
  the user left blank and omits an explicit zero the defaults would replace,
  such as `batchSize`, while writing an explicit zero the defaults leave alone,
  such as `seed` or `stopMinIters`. It also drops the CMA-ES section for a job
  that does not run CMA-ES, as the form handler does. The equivalence is pinned,
  not asserted: `web/src/create-job-parity.json` states each submission in both
  shapes, `TestCreateJobIslandAndFormStoreTheSameConfiguration` posts both to
  the real handlers and compares the stored `JobConfig`, and
  `web/src/createJobBody.test.ts` checks the island's body builder against the
  same file. The page states no bound of its own either: the fallback's `min`
  and `max` attributes and the island's are written from one
  `ui.CreateJobLimits` projected from `internal/app`, and `app.Validate` remains
  what decides a request.
- **The creation form anticipates a refusal; it never makes one.** Two CMA-ES
  configurations are rejected by a combination of fields rather than by any one
  of them: full covariance above `MaxCMAESFullDimensions`, and a
  `restartStrategy` other than `none` beside an `optimizerRestarts` other than
  1. Discovering either by submitting is a poor way to find out, so the island
  warns as soon as the current values cross them, recomputing the searched
  dimension count the way `optimizerDimensions` does. The warning is advisory
  and is deliberately not a block: the control stays usable and the form stays
  submittable, because `app.Validate` is what decides a request and the page
  must not refuse something `app` would have accepted. The fallback has no
  script and therefore no live check; it states both rules in prose instead,
  composed from the same projected limits. `web/e2e/cmaes-warnings.behavior.spec.ts`
  pins the appearing, the clearing and the still-enabled submit button;
  `internal/server/create_form_cmaes_test.go` pins that the submission is still
  what refuses, carrying `app`'s own message.
- **A CLI flag is never omitted, so its value is never defaulted.** A flag
  carries either its own default or what the operator typed, so `run` keeps the
  typed value and validates it: `--circles 0` fails instead of fitting ten. The
  flags whose zero means "decide for me" — `--batch-size`,
  `--evaluation-workers`, `--seed`, `--stop-*` — are excluded and still resolve
  through the defaults.
- **Global stream is an observable server behavior.** `GET /api/v1/stream`
  emits one snapshot of all currently running jobs, then streams live progress for
  all jobs over one connection. A dropped connection closes that stream, and
  unsubscribing the wildcard client is required to prevent event fan-out
  accumulation.
- **The browser event stream invalidates; REST resources remain authoritative.**
  `GET /api/v1/events` assigns one process-wide, monotonically increasing
  sequence to `job.upsert`, `job.deleted`, and `campaign.changed` envelopes.
  It is intentionally one-way SSE rather than a WebSocket: all browser commands
  still use ordinary HTTP, while native `EventSource` owns reconnect behavior.
  The stream does not replay history. On first connection, reconnect, a sequence
  gap, focus/visibility return, and a 30-second safety interval, an island
  refetches its canonical JSON resource. Events received during that fetch are
  queued and replayed over the response, so a late response cannot erase newer
  progress. Repeated invalidations are coalesced to at most one authoritative
  fetch per second; they are not allowed to build an unbounded chain of fetches
  behind a busy campaign. A subscriber whose 64-event buffer fills is
  disconnected instead of silently losing frames; reconnect reconciliation
  repairs its view.
- **Collection endpoints project jobs instead of cloning optimizer state.**
  `GET /api/v1/jobs`, `/jobs`, and the project counts copy the lifecycle and
  compact configuration fields they report (`refPath`, `mode`, and `circles`),
  but never `BestParams` or `MetricHistory`. Per-job status, metrics, images,
  and continuation endpoints remain the detail surfaces. Supplying `limit`
  opts `GET /api/v1/jobs` into a bounded `{jobs, nextCursor, total}` response;
  cursors are opaque and retain the descending start-time/ID order. The jobs
  page renders only the first 100 records and automatically appends cursor
  pages near the viewport. The no-query array response remains a compatibility
  surface, while the bundled CLI consumes bounded pages.
- **Templ output is the fallback and hydration seed, not a second live state
  model.** Every island — the eight registered in `web/src/dashboard.tsx` —
  replaces its server-rendered fallback after mount, and the data-driven ones
  then read `/api/v1/jobs`, job `/status` and `/metrics`, `/api/v1/dashboard`,
  and `/api/v1/campaigns...`. The existing `/api/v1/jobs/:id/stream` and
  `/api/v1/stream` payloads remain compatibility surfaces and are not the
  browser's reconciliation protocol. A terminal state or transient stream
  failure must not reload the page. The creation page is the one mount point
  whose fallback is a *working* control rather than an inert one: it contains
  the complete `<form>` that posts to `/create`, and the island reads that
  form's current values and option lists before replacing it, so the defaults
  the page ships and the enumerations it offers exist once, in the templ source.
  The settings page and the theme switch also fall back to controls, but inert
  ones — there is nothing on the server to store a browser-local preference in,
  and both say so.
- **The fallback has to survive a broken bundle, not only a disabled one.**
  These are different failure modes and a page can pass one and fail the other:
  with JavaScript off nothing runs and `<noscript>` shows, while a bundle that
  404s or throws leaves scripts enabled, `<noscript>` hidden, and every mount
  point sitting there with its server-rendered children and no island coming.
  `web/e2e/fallback.behavior.spec.ts` asserts all three modes across every
  surface in `web/e2e/fixtures/surfaces.ts`, including that no island root is
  left empty. A control that only works when the island mounts must be rendered
  disabled, or be a plain link; `internal/ui/inline_script_gate_test.go` refuses
  the third option, an inline `onclick`, which looks alive and is not.

These controls do not make the server multi-user or internet-ready. Do not add
documentation suggesting otherwise.

## Job completion

A job reports `completed` only once its final checkpoint is on disk. The worker
records the measured outcome while the job is still `running`, writes the
checkpoint and its artifacts, and publishes the terminal state last. This is
what makes `completed` mean "there is a checkpoint to continue from", which is
the precondition `extend`, `polish`, and every schedule stage read. A
cancellation that lands while the result is being written wins: the job stays
cancelled rather than being resurrected as a completed one.

The ordering is the invariant; a successful write is not. If the final
checkpoint write itself fails, the job is still published as `completed` and
carries `error: failed to persist final result`. A continuation issued
afterwards therefore resumes from the last periodic checkpoint, or is refused
outright when there is none.

## Schedule execution

A schedule runs inside the server, not in a client. `POST /api/v1/schedules`
persists the campaign and starts it; the client may disconnect immediately. The
document format itself is [`schedule-format.md`](schedule-format.md).

- **A schedule is the endpoints, not a second optimizer.** Driving a campaign
  through the executor produces the same cost sequence as issuing the same
  stages by hand against `POST /api/v1/jobs`, `/extend` and `/polish`. Exactly
  the same, with no tolerance, once the seed, `threads`, evaluation width and
  compositor are pinned — asserted by
  `TestScheduleReproducesTheHandDrivenCampaign`.
- **Stages are ordinary jobs.** Each stage is created and queued through the
  same path a hand-issued `POST /api/v1/jobs` uses, so a schedule and manual
  jobs share `--max-jobs` and a campaign cannot oversubscribe the host. The
  executor goroutine itself holds no worker slot.
- **The stage records are the only progress.** There is no cursor file and no
  authoritative in-memory index. The executor re-derives the next stage from the
  persisted records on every iteration, so nothing can drift from what actually
  ran.
- **A stage is recorded before it can start.** The job identifier is chosen
  first, written into the stage record as `running`, and only then used to
  create the job. A crash therefore leaves an adoptable record, never a running
  job no record names.
- **Restart adopts, it does not restart the campaign.** On startup every
  schedule still in `running` resumes. Jobs are restored before schedules, so
  the executor can see how the interrupted attempt ended: a stage whose job is
  restored as completed — the crash landed between the terminal checkpoint and
  the outcome record — is settled from that job and not re-run. Only an attempt
  that did not complete is re-run under the identifier its record already names,
  and only then is its partial checkpoint discarded, because neither `extend`
  nor `polish` can continue from anything but a completed batch checkpoint.
- **Pause is a stage boundary.** `POST /api/v1/schedules/:id/pause` lets the
  in-flight stage finish and stops before the next one; `resume` continues from
  the first stage the records do not show as completed. `cancel` is terminal and
  does cancel the in-flight stage.
- **The durable intent is re-read at the boundary it protects.** Pause and
  cancel are persisted before the executor is touched, and the executor re-reads
  that state immediately before it writes a stage record — so a paused campaign
  starts no further stage — and again once the job exists, so a cancel that
  arrived while the job was being created is replayed against it instead of
  being lost. A driver that stops also re-reads the record as it deregisters, so
  a `resume` that raced with the stop cannot leave a `running` schedule with no
  executor.
- **Schedules run in the default project.** They are keyed independently of jobs
  and the store does not know about projects.
- **Expansion is unconditional; policy is a separate layer.** A document expands
  to the same stage list whatever has happened, so a plan can be printed before
  anything runs. A polish step may carry a `when` object — listed circle counts,
  and a `minGain`/`abortAfterBarren` pair that abandons polishing once it stops
  paying — and the executor evaluates it as a pure function of the plan and the
  recorded stage outcomes when the stage comes up. No streak counter is stored:
  it is recomputed from the records every time, so it cannot disagree with them.
  Whether a stage measured a cost is carried explicitly beside the number, so a
  perfect fit's cost of exactly zero counts as a measurement and its zero gain
  ends polishing like any other barren stage; only a stage that never settled
  leaves the gain unknown. Conditions are refused on extend steps, because
  skipping an extend would move the circle count of every later stage.
- **A declined stage is recorded, not omitted.** Policy writes a `skipped` stage
  record carrying its reason, and the decision is never revisited. The chain
  then continues from the last stage that actually ran.

## Plan estimates

`circlefit schedule create --dry-run` prints what a document would run,
and `schedule status` projects when a running campaign will finish. Both are
read-only, and both refuse to state anything they cannot derive.

- **A dry run reaches nothing.** Expansion needs no runtime state, so the dry
  run never opens a socket: no schedule directory, no stage record, no job, and
  no reference image is read.
- **An omitted seed is reported as automatic, never as seed zero.** A document
  that names its seed expands identically every time. One that omits it has no
  seed yet — `JobConfig.ApplyDefaults` draws a fresh one on each expansion, and
  the real submission draws another — so the dry run says the seed is resolved
  at submission instead of printing the document's zero or the throwaway value
  this expansion happened to draw.
- **A conditional stage is printed as conditional.** A dry run has no outcomes
  and therefore cannot decide a `when` clause. Every planned stage is listed,
  and a conditional one is marked with its condition stated in full, rather than
  being silently included or excluded. The iteration total is split the same
  way, into the part that runs regardless and the part that depends on what the
  campaign measures.
- **The iteration count is the nominal plan, not a prediction and not a
  bound.** It is what the configuration lays out: batch stages times epochs
  times restarts times iterations, plus sweeps times epochs times iterations
  for a polish stage. Early stopping and convergence detection spend less than
  it. Nothing spends more: residual-refill stages used to, by a whole stage
  each, which is what made the arms of two campaigns incomparable, and they are
  now bounded by the same nominal figure. It remains a plan rather than a
  prediction because a run may still stop under it.
- **The finish projection is measured, never modelled.** It divides observed
  stage wall clock by completed stages of the same kind and multiplies by the
  stages of that kind still planned. Kinds are never blended: an extend is
  roughly flat in circle count because its inherited prefix is baked once, while
  a polish grows with the canvas, so a polish estimate is reported as a lower
  bound. Below two completed stages of a kind the projection reports
  insufficient data, and no finish time is given until every remaining kind has
  a rate. A running stage counts as fully remaining, because discounting it
  would need exactly the per-stage progress model this avoids.
- **Only a running campaign gets a finish time.** The projection anchors at the
  current clock, which is a claim only a campaign the server is advancing can
  support. A completed, failed, or cancelled campaign is reported as one that
  will not advance and gets no projection at all; a paused one still gets its
  per-kind rates and a remaining workload, but no timestamp, because when it
  resumes is unknowable.

## Campaign views

`/schedules` lists campaigns, `/schedules/:id` shows one, and `circlefit
schedule` mirrors the same endpoints from a terminal. A campaign view is a read
model: it stores nothing, so it cannot drift from the stage records.

- **A chain is reconstructible without a schedule.** `/chains/:jobID` and
  `GET /api/v1/chains/:jobID` walk the `extendedFrom` and `polishedFrom` lineage
  on each checkpoint back to the root of the chain, so a campaign driven by hand
  through the extend and polish endpoints still reads as one run. Checkpoints
  that already name a schedule are left out of the discovery listing, because
  the schedule view knows strictly more than a reconstruction does.
- **An unmeasured column stays empty.** A stage that has not completed shows no
  cost and no PSNR rather than a zero, which would read as a perfect fit. PSNR
  is derived from the stage cost. Elapsed comes from the stage record's
  `startedAt`/`completedAt` and is therefore absent on an imported chain, since
  a checkpoint records when it was written and not how long its job ran.
- **An imported stage is stated the way a restored job is.** A checkpoint's
  `termination` is mapped by the same rule `jobFromCheckpoint` applies, in the
  listing card as well as on the detail page. An unknown or legacy termination
  is a job that never recorded how it ended, so it reads as cancelled rather
  than as a completed campaign.
- **Chain discovery covers every project.** `/chains/:jobID` resolves a job
  through its own project store, so the campaign listing walks every registered
  project rather than the default one alone. Each store is discovered on its own
  because lineage never crosses a project boundary.
- **Chain listings read a persisted metadata index, not parameter vectors.** A
  checkpoint save writes `checkpoint-info.json` beside the authoritative
  `checkpoint.json`. Listings use that compact projection; checkpoints from an
  older build are decoded into a metadata-only type that validates but never
  retains `bestParams`, then receive a sidecar for later scans. The merged
  cross-project chain discovery is shared by the dashboard, campaign, and
  chain-list endpoints and is invalidated by job creation, terminal
  transitions, and deletion instead of expiring on a timer.
- **The stage listing is a projection, and it is bounded by the reader.**
  `GET /api/v1/schedules/:id` carries index, kind, state, circles, cost, elapsed,
  job and reason per stage — what the table prints and what the finish estimate
  reads — and not the stage's `JobConfig`. Carrying the configuration cost about
  1.2 kB a stage and put the response past the CLI's `MaxCLIResponseBytes` at
  roughly 865 of the 4096 stages a schedule may legally expand to, which made
  most legal campaigns unreadable from a terminal. `GET /api/v1/chains/:jobID` is
  the same shape for the same reason. Elapsed travels as nanoseconds rather than
  as the two timestamps it comes from, so the estimate computed from the listing
  is identical to the one computed from the records. Raising the cap is not the
  fix: a body that grows without bound with stage count moves the wall rather
  than removing it. The document travels in full, because the projection needs
  the plan it expands to, so it is bounded as well — `MaxScheduleDocumentBytes`
  and `MaxScheduleNameLen` — and the two bounds together are what make the
  response fit for every campaign the format allows, not merely for the ones
  with short names.
- **A stage's configuration is retrievable one stage at a time.**
  `GET /api/v1/schedules/:id/stages/:index` answers the whole stage record,
  configuration included, because replaying a single stage is what that record is
  for. The equivalent for an imported chain is the job itself,
  `GET /api/v1/jobs/:id/status`.
- **The campaign seed is reported or declared absent, never zero.** A document
  that omits `seed` leaves the record's `campaignSeed` at the resolve-me
  sentinel; the seed that actually ran is read back from the first stage record,
  and before any stage has run both the view and the CLI say the seed is
  unresolved instead of printing the zero. The web view reads the fallback off
  the stage records it already holds; the CLI reads the campaign's own seed,
  because its listing carries no stage configuration and every stage inherits
  that one seed anyway.
- **Accepted polishing sweeps are not persisted.** The batch polisher reports
  the count to the log only, so the column reports it as unrecorded on every
  stage. Populating it needs a stage-record field, not a view change.
- **The plot ships no assets.** Cost against circle count is inline SVG built
  server-side from the stage list. The UI is served locally and has no CDN, so a
  chart that needs one is a chart that does not render.
