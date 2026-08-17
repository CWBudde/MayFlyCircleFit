# Behavior that must remain explicit

Observable behavior that is easy to misdescribe or accidentally break. The code
and its tests win if this document goes stale, but changing any item here is a
deliberate decision, not a refactor side effect.

Rendering-side invariants live in
[`rendering-internals.md`](rendering-internals.md).

## Backends and modes

- CPU supports joint, sequential, and batch modes, including a supplied base
  canvas.
- OpenCL is experimental and supports joint mode only. It requires a `gpu` build
  tag, CGO, OpenCL development headers, and a usable runtime and device.
  Sequential and batch OpenCL requests must fail explicitly, never silently fall
  back to a CPU staged renderer.

## SIMD dispatch

- AMD64 SSD dispatch is tiered AVX2, then SSE2, then scalar, each after a
  runtime CPU-feature check; ARM64 SSD dispatch requires ASIMD before selecting
  NEON. Unsupported architectures use the scalar kernel. SAD remains scalar on
  ARM64 and on amd64 hosts without AVX2.
- `MAYFLY_SIMD_TIER` forces any reachable tier for every kernel on every
  architecture, and `MAYFLY_DISABLE_SIMD=1` remains its scalar alias. A forced
  tier is honored by dispatch but never by detection: `Tier()` reports what was
  asked for, and the tests that check detection strip both variables.

## Parallel evaluation

- `--parallel-evaluation` is opt-in and defaults off, because it changes the
  result of a fixed seed. It leases one independent renderer session per
  concurrent evaluation and reproduces bit-identically for a fixed seed and any
  worker count, but its trajectory differs from a serial run of that seed
  because MayFly holds the global best fixed for a whole parallel generation.
  Compare runs only against runs with the same settings.
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
  OpenCL can create sessions, but each carries its own device state and several
  of them evaluating at once has never been validated, so it withholds the
  parallel marker and is refused. Polishing errors rather than degrading
  to one slot when the pool comes up short, because the optimizer would still
  call the evaluator from every goroutine. The epoch and progress wrappers
  forward the width so neither check can be bypassed.
- Acceptance stays serial: a sweep is committed only after the merged candidate
  is re-evaluated on the full session and `sweepKeepsCirclesUseful` clears it.
  Pooling applies to candidate evaluation only.
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
  sweep. Do not change a selector to scatter its active set without accounting
  for that cost.
- `contiguous-window` is cheaper per sweep but not better per second, and it is
  not the default. Measured in
  [`contiguous-window-polish-report.md`](contiguous-window-polish-report.md):
  the 2.1×/5.3× render-cost figures describe the first sweep of a coverage cycle
  with the optimizer stubbed out and fall to 1.44× over a full cycle, and at
  equal wall clock the strategy reached a worse cost than `hybrid-overlap` in
  every configuration measured. At the default three sweeps it only offers the
  last `3 * activeSetSize` draw slots to the optimizer. Do not describe it as an
  end-to-end speedup.
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

## Determinism, resume, and termination

- Resume is restart-from-best: the MayFly v0.4.0 population is seeded with the
  saved best and deterministic nearby variations. It is not an exact restoration
  of optimizer internals. Server restart-from-best for sequential and batch jobs
  is not supported.
- A zero user seed generates and reports an effective seed; a nonzero seed is
  deterministic.
- Optimizer termination reasons propagate from the adapter through the pipeline
  to jobs, checkpoints, `status`, and `checkpoints list`. The checkpoint
  `termination` field is free-form, so new reasons need no schema bump, and
  readers reject a version above 2.
- The configured `variant` is honored at every optimizer construction site.
- MayFly's `optimization_started` and `iteration_completed` events are demoted
  to debug, so `--log-level=debug` emits one record per optimizer iteration.
  Info level stays at one record per optimizer run.

## Early stopping

Two distinct mechanisms exist and must not be conflated.

- **Stage-level convergence** (`--patience`, `--threshold`, `Convergence*`
  config) counts whole circles or batches, uses a relative improvement ratio,
  and applies to sequential and batch only; `OptimizeJointContext` discards it.
  Only this tracker reports `stage_convergence`.
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

## Schedule execution

A schedule runs inside the server, not in a client. `POST /api/v1/schedules`
persists the campaign and starts it; the client may disconnect immediately.

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
