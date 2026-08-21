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
  runtime and device. Staged modes create same-backend sessions and replay the
  retained prefix because OpenCL does not support an accumulated custom canvas;
  they must never silently fall back to a CPU staged renderer.

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
  sweep. Do not change a selector to scatter its active set without accounting
  for that cost.
- `contiguous-window` is cheaper per sweep but not better per second, and it is
  not the default. Measured in
  [`contiguous-window-polish-report.md`](contiguous-window-polish-report.md):
  the 2.1×/5.3× render-cost figures describe the first sweep of a coverage cycle
  with the optimizer stubbed out and fall to 1.44× over a full cycle, and at
  equal wall clock the strategy reached a worse cost than `hybrid-overlap` in
  every configuration measured. It only offers the last
  `maxSweeps * activeSetSize` draw slots to the optimizer, so it needs a sweep
  budget of at least `ceil(circles / activeSetSize)` to have seen every circle
  once. Do not describe it as an end-to-end speedup.
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
  model.** Job, dashboard, and campaign islands replace their server-rendered
  fallback after mount and then read `/api/v1/jobs`, job `/status` and
  `/metrics`, `/api/v1/dashboard`, and `/api/v1/campaigns...`. The existing
  `/api/v1/jobs/:id/stream` and `/api/v1/stream` payloads remain compatibility
  surfaces and are not the browser's reconciliation protocol. A terminal state
  or transient stream failure must not reload the page.

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

`mayflycirclefit schedule create --dry-run` prints what a document would run,
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
  times iterations, plus sweeps times epochs times iterations for a polish
  stage. Early stopping and convergence detection spend less than it, while a
  batch stage that leaves circles unplaced may run up to
  `renderer.MaxExtraBatchStages` residual-refill stages beyond its plan and
  spend more. Those refills are excluded from the count because most stages
  never run them, so every place that presents the figure labels it nominal.
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

`/schedules` lists campaigns, `/schedules/:id` shows one, and `mayflycirclefit
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
