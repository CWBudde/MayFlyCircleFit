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
  slot carrying its own scratch parameter vector, so nothing is shared. A
  width-one pool leases the sweep's own session and vector and is byte-identical
  to the serial sweep it replaced. `PolishCircleBatchContext` still rejects an
  optimizer reporting a width above one when no pool can be built -- a backend
  without independent sessions, OpenCL today -- and errors rather than degrading
  to one slot when the pool comes up short, because the optimizer would still
  call the evaluator from every goroutine. The epoch and progress wrappers
  forward the width so neither check can be bypassed.
- Acceptance stays serial and unchanged: a sweep is committed only after the
  merged candidate is re-evaluated on the full session and `allCirclesUseful`
  holds for the whole vector. Pooling applies to candidate evaluation only.
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
- A polishing sweep is committed only when `allCirclesUseful` holds for the
  whole candidate vector, so one circle with a negative `MSEContribution` blocks
  every sweep until an active set repairs it. Fitted vectors routinely contain
  such circles, because `PruneCircleBatch` runs per stage against that stage's
  canvas while this gate is global over the final vector, and nothing re-audits
  the assembled result. Polishing a real batch fit was therefore a complete
  no-op for three of the four strategies in the measurements above, and it still
  spends the full optimizer budget before the gate is consulted. This is
  pre-existing behavior, not a property of any one strategy; do not attribute a
  zero-improvement polishing run to the selector without checking
  `accepted_sweeps` first.

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
