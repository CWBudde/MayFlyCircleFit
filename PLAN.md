# MayFlyCircleFit Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Build a high-performance circle-fitting optimization tool with CPU/GPU backends, web UI, and live progress visualization.

**Architecture:** Go-based CLI with Cobra, modular optimizer/renderer interfaces, HTTP server with SSE streaming, templ-based UI, and SIMD-accelerated evaluation kernels.

**Tech Stack:** Go 1.24+, Cobra (CLI), templ (frontend), slog (logging), net/http (server), optional chi (routing), cgo+SIMD (performance), OpenGL/OpenCL (GPU)

> **Production-readiness audit (2026-08-09):** Historical `COMPLETE` markers below describe implementation milestones, not validated production readiness. Phase 14 supersedes affected completion claims until its acceptance checks pass. Remediation code, release-gating CI, and corrected documentation are now present, but no gate is marked passed solely because its workflow was added; final clean-clone and repeated CI verification remain release requirements.

---

## Completed foundation (Phases 1–9) ✅

The original task-by-task history is compacted here; implementation details,
measurements, and caveats live in the linked documentation and git history.

| Phase | Completed outcome |
| --- | --- |
| 1–5 | Domain model, CPU renderer, Mayfly adapter, joint/sequential/batch pipelines, and Cobra CLI. |
| 6 | Trusted-local background server, job lifecycle, REST/image endpoints, SSE progress, CLI integration, and lifecycle coverage. |
| 7 | templ job list/detail/create UI, live metrics and images, sparkline, validation, and UI documentation. Browser/mobile validation continues in Task 12.9. |
| 8 | Atomic filesystem checkpoints, traces, retention, and restart-from-best; corrected semantics are documented in `docs/checkpoint-resume-test-results.md`. |
| 9 | Profiling/benchmark infrastructure, allocation-free CPU fast paths, scanline sharding, and correctness validation. See `docs/benchmarks.md`, `docs/task-9.9-performance-report.md`, and `docs/task-9.10-correctness-validation.md`. |

---

## Phase 10: SIMD/C Intrinsics Research & Implementation (Evaluation Loop)

**Goal:** Recover a large chunk of the original "blazing fast" feel by applying vectorized kernels to the evaluation hot path.

**Status:** Research complete, implementation in progress

### Completed Tasks 10.1–10.19 ✅

- Tasks 10.1–10.10 established the scalar, AVX2, NEON, and SSE2 SSD kernels,
  runtime tier dispatch, production cost integration, native/cross-build
  coverage, and benchmark reports.
- Tasks 10.11–10.15 replaced the original pixel loop with scanline/span
  rendering, exact SIMD compositors, Q16.16 geometry with an exact range
  fallback, and a measured opt-in symmetry prototype. The selected combined
  path and tradeoffs are recorded in
  `docs/task-10.15-combined-optimization-report.md`.
- Task 10.16 shipped exact incremental dirty-span SSD for staged pipelines; its
  arithmetic, crossover, and parity evidence are in
  `docs/task-10.16-incremental-cost-report.md`.
- Tasks 10.17–10.19 completed the AMD64 SSE2 tier and exact SSE2/AVX2
  compositors. Population evaluation parallelism was subsequently implemented
  and measured in `docs/parallel-evaluation-report.md`.

### Task 10.20: Deferred CPU-Kernel Research (P3)

These items were moved out of otherwise completed tasks. They are bounded
research follow-ups, not blockers for the selected production CPU path.

- [ ] Compare signed Q24.8 and normalized Q8.24 geometry with Q16.16, including
  coordinate range, adversarial boundaries, and full-render results.
- [ ] Assess a corresponding ARM64 NEON span-edge implementation without
  compromising the portable geometry layout.
- [ ] Complete native cross-platform precision measurements for fractional and
  tangent boundaries, radii 1 and maximum radius, clipping, batch boundaries,
  randomized circles, and row sharding.
- [ ] If the original Pascal/Delphi source becomes available, document its
  exact cost arithmetic and numeric/SIMD representations; until then,
  `docs/task-10.16-incremental-cost-report.md` remains the Go contract.
- [ ] Hoist the exact compositor's per-span constant block to once per circle
  and remeasure the SSE2/AVX2 crossover before changing production dispatch.

## Phase 11: GPU Backends (Research → Prototype)

**Goal:** Add a pluggable GPU renderer/coster behind the existing `Renderer` interface.

### Completed Tasks 11.1–11.8 ✅

OpenCL was selected and integrated behind the renderer/session abstraction with
`gpu` build tags, device-side compositing and SSD reduction, persistent
buffers, lazy image readback, typed backend selection, and joint/sequential/
batch coverage. PoCL proved functional but slower than CPU in the measured
staged workload; vendor-GPU performance, broader correctness, fallback policy,
and documentation remain below. The former setup and visual-validation
remainders were moved into Tasks 11.10 and 11.12.

### Task 11.9: Create GPU Performance Benchmarks
- [ ] Benchmark GPU rendering for various K values (1, 10, 50, 100)
- [ ] Benchmark GPU rendering for various W×H sizes (64x64, 256x256, 512x512, 1024x1024)
- [ ] Benchmark GPU cost computation separately
- [ ] Compare GPU vs CPU performance across scenarios
- [ ] Identify crossover points where GPU becomes beneficial
- [ ] Document performance characteristics

### Task 11.10: Test GPU Correctness and Edge Cases
- [ ] Test GPU detection and initialization on a prepared OpenCL runner.
- [ ] Add golden-image visual comparisons against the CPU renderer.
- [ ] Verify pixel-exact equivalence to CPU (within float tolerance)
- [ ] Test with various circle counts and sizes
- [ ] Test with overlapping circles
- [ ] Test with edge cases (circles outside bounds, zero opacity)
- [ ] Test with different image sizes
- [ ] Validate cost computation accuracy
- [ ] Document any differences or limitations

### Task 11.11: Handle GPU Errors and Fallback
- [ ] Add graceful error handling for GPU initialization failures
- [ ] Provide automatic fallback to CPU if GPU unavailable
- [ ] Add logging for GPU-related errors
- [ ] Test error scenarios (no GPU, driver issues, out of memory)
- [ ] Document common GPU issues and solutions

### Task 11.12: Documentation and Examples
- [ ] Document the macOS Metal/WebGPU gap and driver quirks found during vendor-GPU validation.
- [ ] Update CLAUDE.md with GPU architecture
- [ ] Document GPU requirements and setup
- [ ] Add example commands using GPU backend
- [ ] Document performance comparisons
- [ ] Add troubleshooting section for GPU issues
- [ ] Document when to use GPU vs CPU

### Task 11.13: Optimize OpenCL/PoCL Pipeline Performance

Measured baseline: the uncached 64x64, K=12 pipeline benchmark reports PoCL approximately 3x slower than CPU for joint mode, 190x slower for sequential mode, and 120x slower for batch mode. Sequential creates 14 OpenCL sessions and batch creates 5; their approximately 45 ms/session cost shows that repeated context/program initialization dominates staged execution. These PoCL CPU measurements guide implementation but do not replace vendor-GPU validation.

- [ ] Share OpenCL resources across renderer sessions
  - [ ] Introduce an owned shared device engine for the selected device, context, queue, compiled program, reference buffer, and workgroup configuration
  - [ ] Make staged sessions allocate only their mutable kernels/buffers instead of calling `InitOpenCL` and `clBuildProgram` again
  - [ ] Define safe shared-resource ownership and cleanup for normal completion, partial initialization, and CPU degradation
  - [ ] Add an isolated session-creation benchmark and verify that kernel compilation occurs once per base renderer
- [ ] Add accumulated-canvas support to the OpenCL staged pipeline
  - [ ] Implement `newSessionWithCanvas` and `initialCanvas` so sequential and batch stages evaluate only newly added circles
  - [ ] Accept a packed `uchar4` base-canvas buffer in the render/cost kernel instead of assuming white
  - [ ] Upload the retained canvas at most once per stage as the first implementation
  - [ ] Investigate a device-resident retained-canvas handoff to eliminate stage-boundary readback/upload
- [ ] Reduce per-evaluation synchronization and memory traffic
  - [ ] Add a cost-only execution path that omits full output-buffer writes during optimizer evaluations and materializes the final/best image on demand
  - [ ] Profile a C/OpenCL-owned asynchronous parameter staging path; retain blocking writes unless measurements justify the added ownership complexity
  - [ ] Design a batched objective interface so optimizer populations can share kernel launches and scalar synchronization
- [ ] Optimize the render kernel based on profiling
  - [ ] Precompute radius squared, premultiplied color, opacity, and inverse opacity into aligned circle records
  - [ ] Evaluate constant-memory circle parameters and device-specific preferred workgroup sizes
  - [ ] Investigate order-preserving tile/bin circle lists so pixels skip non-overlapping circles
- [ ] Preserve semantics and fallback behavior
  - [ ] Extend CPU/OpenCL parity tests across joint, sequential, and batch modes after each optimization
  - [ ] Verify cache invalidation, lazy image materialization, cleanup, and permanent CPU degradation paths
- [ ] Re-run the complete backend pipeline benchmark after each tranche
  - [ ] Record PoCL before/after medians, allocations, session counts, and evaluation counts
  - [ ] Run the same benchmark on supported AMD, Intel, and NVIDIA OpenCL devices where available
  - [ ] Document crossover points and retain optimizations only when profiling demonstrates a benefit

Phase 11 is complete only when Tasks 11.9–11.13 establish vendor-GPU
performance and parity, deliberate fallback behavior, and accurate operational
documentation. OpenCL remains experimental until then.

## Phase 12: UX & Visualization Polish

### Completed Tasks 12.1–12.8 and 12.10 ✅

The UI now has five comparison modes, Turbo/Magma difference heatmaps, live
Cost/PSNR/optional-SSIM metrics, parameter inspection/export, downloadable
images and self-contained reports, richer progress visualization, pause/resume/
cancel/delete actions, and persistent user preferences. See
`docs/advanced-quality-metrics.md`, `docs/difference-heatmaps.md`, and
`docs/html-reports.md`. Optional circle-hover and parameter-sorting ideas were
not carried forward as roadmap commitments.

### Task 12.9: Responsive Design, Accessibility, and Browser Validation (P2)

This task also owns the Safari/mobile checks deferred from Phase 7.

- [ ] Validate phone, tablet, and desktop layouts; add or correct breakpoints so
  images and comparison modes stack cleanly.
- [ ] Audit WCAG 2.1 AA essentials: alt text, contrast, labels, focus order,
  keyboard navigation, and a screen-reader pass.
- [ ] Add missing loading/skeleton and SSE-connecting states.
- [ ] Exercise supported browser sizes and engines, including Safari on macOS.
- [ ] Revalidate all live view modes, downloads, reports, metrics, controls, and
  preferences during the browser pass.

## Phase 13: Robustness, Docs, and Packaging

### Completed or Superseded Work ✅

Error handling, typed validation, safe paths, request logging, the README,
benchmark and limitation documentation, SemVer metadata, changelog, license,
contribution guide, cross-build/release automation, and CI gates are
implemented. Production hardening and final end-to-end release acceptance moved
to Phase 14, which is authoritative where this historical phase overlapped it.
Per-client rate limiting is not carried forward for the documented
trusted-local server; bounded admission and resource limits are the active
contract.

### Task 13.15: Remaining Documentation, Examples, and Observability (P3)

- [ ] Audit structured logging fields/levels and document logging
  configuration; add measured slow-operation/progress logging where useful.
- [ ] Decide whether a disabled-by-default Prometheus endpoint is warranted
  before adding a new public surface.
- [ ] Add a focused `docs/getting-started.md` covering CLI, server, UI, API,
  artifacts, and common configuration from a clean installation.
- [x] Add `docs/architecture.md` with current package boundaries, lifecycle,
  persistence, SSE, schedule, frontend-island, CPU/SIMD, and experimental GPU
  flows.
- [ ] Curate small redistributable examples with documented settings and
  expected qualitative results; add a deterministic example script and an
  appropriate CI smoke test.
- [ ] Decide whether a separate public roadmap/issue-tracker page adds value
  beyond this plan and `docs/known-limitations.md`.

Badges, promotional screenshots, a walkthrough video, source-file copyright
headers, and a code of conduct remain optional publication work rather than
active engineering tasks.

## Phase 14: Production-Readiness Remediation 🚨 RELEASE GATE

The 2026-08-09 audit remediation is implemented: reproducible tooling,
trusted-local browser and filesystem boundaries, canonical typed configuration,
race-safe lifecycle/SSE state, cancellable optimizer execution, corrected
renderer and staged-pipeline semantics, durable snapshots/checkpoints/traces,
safe store ownership, portable SIMD dispatch, typed CLI/API contracts, and
release-gating workflows. Regression coverage and documentation accompany each
area. Historical local and CI observations remain evidence for their recorded
revisions only.

### Task 14.13: Final Release Verification (P0)

These checks were moved from Tasks 14.9, 14.11, and 14.12 so the completed
remediation tasks no longer look partially open.

- [ ] Observe every required CI gate, including all supported cross-builds,
  generation, race, vulnerability, GPU compile, and core end-to-end checks,
  passing from a clean clone on two consecutive runs of the release candidate.
- [ ] Verify repository/release controls prevent producing or publishing a
  release while any required gate fails; document any administrator-enforced
  setting that cannot be expressed in workflow files.
- [ ] On a clean user machine, follow the README verbatim and complete a small
  CLI job and a server/UI job without workspace preparation.
- [ ] After those checks pass, remove the production-readiness caveat at the top
  of this document and mark Phase 14 complete.

## Phase 15: Polishing Throughput

The initial production profile found serial polishing and active-set selection
dominating long incremental runs. Tasks 15.1 and 15.2 removed those two
bottlenecks; the remaining work targets selection quality, evaluation width,
dirty-region scoring, short-stage correctness, and fixed per-extend cost.
Measurements must continue to state host, workload, budget, and allocations.

### Task 15.1: Give Polishing a Session Pool (P1) ✅

Completed: polishing now leases independent renderer sessions per concurrent
evaluation while keeping sweep acceptance transactional and deterministic for a
fixed width. Race/parity coverage and width benchmarks are recorded in the
code and plan history; on the measured 12-thread host, width 8 improved the
production-shaped 256/512-circle sweep estimate by about 4.1–4.3×. See
`docs/polishing-throughput-report.md`.

### Task 15.2: Make Active-Set Selection Cheap (P1) ✅

Completed: leave-one-out audits and residual-region influence work are
parallelized over isolated sessions, influence rendering is clipped to the
region/circle intersection, and incumbent audits are reused after rejection.
Selection remains equivalent to the serial implementation; the measured
512-circle residual-region selection fell from 4.24 s to 0.47 s on the stated
12-thread host, with the transient-memory tradeoff documented in
`docs/polishing-throughput-report.md`.

### Task 15.3: Stop Destroying the Baked Prefix (P2)

Direct evidence, measured on the live 512x512 vector: `min(activeCircles)` under
`residual-region` is 7 of 256 circles and 11 of 512, so the bake covers 3-4% of
the vector. The optimization the comment on `bakedSuffixSession` describes is
therefore nearly inert on the strategy production actually runs. The other half
of the measurement argues against chasing it: per-candidate cost rises only 9%
for a doubling of the circle count (1 881 us at 256, 2 056 us at 512), because
the full-canvas clear and the full-image SSD pass dominate rather than
rasterization, so recovering the prefix recovers much less than the 3-4% figure
suggests. Keep this at P2 and behind a measured quality comparison.

- [x] Measure how much of the per-candidate cost the prefix actually recovers,
      by recording `min(activeCircles)` and per-candidate rasterization count per
      sweep. Measured: prefix 7/256 and 11/512, per-candidate 1 881 us and
      2 056 us, i.e. +9% for twice the circles.
- [ ] Evaluate biasing selection toward later draw slots when region energy is
      close, so the prefix stays large without abandoning merit-based selection.
      This changes what polishing selects, so it ships only with a measured
      quality comparison, not on the cost argument alone.
- [ ] Record the outcome in `docs/contiguous-window-polish-report.md`, which
      currently documents the cost argument for `contiguous-window` but not the
      prefix collapse in the merit-based strategies.

**Acceptance Checks:**

- [ ] A report compares final cost and wall clock for the current and
      prefix-aware selection at equal optimizer budget, on the same seed.

### Task 15.4: Right-Size the Polishing Defaults (P2) ✅

Completed: polishing has its own population setting and measured defaults
(`pop=30`, 200 iterations × 2 epochs, up to 8 sweeps, stagnation 100).
`docs/polishing-budget-report.md` records the sweep behavior and equal-host
comparison; schedule estimates and backward-compatible normalization use the
same canonical constants.

### Task 15.5: Derive the Evaluation Width from a Measurement (P2)

`EvaluationWorkers` resolves to `Threads` when it is zero
(`internal/app/config.go:321`) and `effectiveEvaluationWorkers`
(`internal/fit/renderer/renderer_cpu.go:395`) clamps it to `GOMAXPROCS`, so a
default configuration ends up running as many concurrent evaluations as the
machine has hardware threads. That is the core count talking, not a
measurement, and the measurement disagrees.

On the AMD Ryzen 5 4600H, 12 threads, `GOMAXPROCS=12`, at 512x512 with
`residual-region` and `activeSetSize 8` (the Task 15.1 methodology: fitted over
4 000 and 16 000 candidates, extrapolated to a 690 000-candidate sweep), width
12 was reproducibly slower than width 8: 3.04x against 4.09x at 256 circles and
3.60x against 4.33x at 512. The result held at both circle counts, on both
revisions, and for medians and minima alike. It also costs more memory —
101.8 MB at width 12 against 80.5 MB at width 8, on 16 000 candidates at 512
circles — because a slot holds ~5.3 MB at 512x512. Running one evaluation
goroutine per hardware thread leaves nothing for the runtime and saturates
memory bandwidth.

- [ ] Replace the `EvaluationWorkers = Threads` default with one derived from a
      measurement across widths, not from the core count.
- [ ] Establish whether the right rule is a fraction of `GOMAXPROCS`, a fixed
      headroom below it, or something image-size dependent, by benchmarking
      widths on more than one machine and more than one canvas size. One data
      point on one 12-thread box is not enough to pick a formula.
- [ ] Document the chosen rule next to `EvaluationWorkers`
      (`internal/app/config.go:183`) with the measurement behind it, and keep the
      explicit setting authoritative so an operator can still override it.

**Acceptance Checks:**

- [ ] A benchmark table shows sweep cost against evaluation width on the stated
      machines, and the default the code picks is the width that table
      recommends.
- [ ] An explicitly configured `EvaluationWorkers` is still honored up to the
      `GOMAXPROCS` clamp, with a test covering it.

### Task 15.6: Stop the Acceptance Gate Vetoing Every Sweep (P1) ✅

Completed: sweep acceptance now requires every active circle to remain useful
without increasing the inherited set of non-useful circles, instead of
requiring a sweep to repair untouched circles. Incumbent audits are cached,
decisions are logged, and regression tests cover both legitimate acceptance and
newly killed circles. On the recorded 64-circle stalled checkpoint, the same
budget changed from 0/12 accepted and no gain to 12/12 accepted and 44.64 cost
removed. The post-fix strategy comparison moved to Task 15.10.

### Task 15.7: Score Only the Pixels a Candidate Can Change (P1)

Measured 2026-08-20 on the live 2 111-circle fit of `Christian_after.jpeg`
(512x512), one `replacement` sweep at `activeSetSize` 100, `polishingEpochs` 2,
`polishingIters` 400, `polishingPopSize` 60:

- the sweep cost **599 s** and returned **0.000**, at **6.24 ms per candidate**
  (96 000 candidates, counting the mayfly's male and female populations);
- the 100 selected circles have a summed disc area of **326.6 px^2 — 0.125% of
  the canvas** — yet every candidate clears and SSD-scores all 262 144 pixels;
- the reported rate implies **~2 521 circle rasterizations per candidate**,
  i.e. the whole vector is redrawn to move 100 circles.

This is the same per-candidate cost Task 15.3 measured at 2 056 us on 512
circles, now 6.24 ms on 2 111 — 3x for 4x the circles. That inverts 15.3's
conclusion: the full-canvas clear and full-image SSD no longer dominate at this
scale, rasterization does, because the baked prefix has collapsed to **10.7% of
the vector** (earliest active draw index 226 of 2 111).

Changing an active circle can only alter pixels inside that circle's disc, under
its old or its candidate parameters. Everything else — including every later
circle drawn over untouched ground — is bit-identical to the incumbent, so both
the recomposite and the SSD can be restricted to that region and the remainder
carried as a precomputed constant. Unlike 15.3's remaining bullet, this changes
no selection and no result, only the work done to reach it.

Two traps the measurement already exposes:

- **A single union bounding box buys nothing.** Merit-based selection scatters
  the active set across the canvas: the union bbox of those same 100 circles is
  **71.4% of the canvas** against 0.125% of true disc area. The dirty region has
  to be a per-circle rect list or a tile mask, not one rectangle.
- **The region is per-candidate, not per-sweep.** `NewBounds` lets a radius grow
  to `max(W, H)`, so a candidate may legitimately propose a 400 px circle where
  the incumbent had a 1 px one. The affected set is the union of the old and new
  discs, computed from the candidate's own parameters, with a full-canvas
  fallback when that union exceeds a break-even fraction.

- [x] Add a dirty-region evaluator: baseline composite plus baseline SSD total,
      per-candidate affected-pixel set from old-disc union new-disc, recomposite
      and rescore only those pixels, and fold in the constant remainder.
- [x] Fall back to full-canvas evaluation when the affected fraction exceeds the
      measured break-even point, so a large-radius candidate cannot be slower
      than it is today.
- [ ] Re-measure the sweep above and record per-candidate cost against affected
      fraction in `docs/contiguous-window-polish-report.md`.
- [x] Re-check whether 15.3's prefix-aware selection is still worth its quality
      risk once evaluation no longer scales with the prefix.

**Acceptance Checks:**

- [x] A test asserts the dirty-region evaluator and the full-canvas evaluator
      return the same cost for the same parameters, across active sets that are
      scattered, clustered, canvas-edge-crossing, and radius-growing.
- [x] A benchmark reports per-candidate cost at 512 and 2 000+ circles for both
      evaluators, including the affected fraction and the fallback rate.
- [ ] The 599 s sweep above is re-run at equal budget and its wall clock
      recorded; the cost it reaches is unchanged.

### Task 15.8: A Short Batch Stage Must Not Strand the Run (P1) ✅

Completed: `refill_limit` remains a successful batch outcome at its produced
size. Job resources report requested and actual counts explicitly, and extend
or polish continuations rebase a short checkpoint to its actual size. A `+N`
extend therefore targets the produced count plus N. The pipeline keeps its seed
stable; callers may deliberately retry with another seed without losing the
usable short checkpoint.

Hit on 2026-08-20 at 2 813 circles. An extend to 2 814 finished
`termination: refill_limit` with `requestedCircles` 2 814 and `actualCircles`
2 813: the batch pipeline pruned the appended circle as not earning its place,
refilled, and exhausted `MaxExtraBatchStages` one circle short. The job is
reported `completed` with a plausible `bestCost`, but every continuation from it
fails `POST /api/v1/jobs/{id}/extend` with

    400 {"error":{"code":"invalid_checkpoint",
                  "message":"extension requires a complete batch checkpoint"}}

so the job is a permanent dead end that looks healthy from the API. A chain
driver retrying the same parent cannot make progress; recovery meant rolling
back to the last complete ancestor and re-running with a different seed, which
succeeded on the first attempt — so the condition was seed-specific, not a
ceiling of the arrangement.

Two defects, worth separating:

- **The state is unreachable through the API.** `GET /jobs/{id}` reports the
  requested circle count from `config.circles`; only the on-disk checkpoint
  carries `actualCircles`. A caller cannot tell a complete job from a stranded
  one without reading the store directly.
- **A pruned refill is treated as success.** Finishing short is a legitimate
  outcome of the pruning gate, but it should not be silently reported as a
  completed extension of the requested size.

- [x] Surface the produced circle count on the job resource, so
      `actualCircles < requestedCircles` is visible to any client.
- [x] Decide and document the contract for a short stage: either fail the job
      with a typed error naming `refill_limit`, or accept it as a complete
      checkpoint at its actual size so continuations remain legal. Silently
      completing at a size that no continuation accepts is the one option to
      rule out.
- [x] Consider reseeding inside the pipeline when a refill exhausts its stages,
      since a fresh seed cleared this immediately.
- [x] Record the failure and its recovery in `docs/troubleshooting.md`, next to
      the existing JSON API error envelope material.

**Acceptance Checks:**

- [x] A test drives the pipeline into `refill_limit` and asserts the chosen
      contract holds — either the typed failure, or an extend that succeeds from
      the short checkpoint.
- [x] A test asserts the job resource distinguishes requested from produced
      circles.

### Task 15.9: Cut the Fixed Cost of a Single-Circle Extend (P2) ✅

Measured 2026-08-20 at 2 000 circles, one `+1` extend per point, `popSize` 30,
`optimizerEpochs` 1:

| `iters` | wall clock |
|---|---|
| 50 | 2.0 s |
| 500 | 6.0 s |
| 1 500 | 12.0 s |

That is 0.0069 s per iteration plus **~1.66 s that does not depend on the
optimizer budget at all** — 28% of a 6 s extend at the shipped `iters` 500.
Growth campaigns run one circle per extend (Task 16.9), so this is paid once per
circle: roughly 28 minutes across a 1 000-circle leg.

The suspected contributors are per-extend rather than per-iteration: rebuilding
the session and its baked prefix, revalidating the full parameter vector, and
writing the checkpoint — which is a single JSON document that grew from 334 KB
at 2 000 circles to 469 KB at 2 813, rewritten in full on every extend.

- [x] Profile one extend at 2 000+ circles and attribute the fixed 1.66 s across
      session setup, validation, checkpoint serialization, and trace append.
- [x] Address whichever term dominates. If it is the checkpoint, consider
      writing parameters in a binary form or appending only the delta, keeping
      the JSON document as an export rather than the hot path.
- [x] Re-measure the table above and record the new intercept.

Implemented: CPU extensions reuse a cost-verified parent `best.png` as their
retained canvas, suffix-pool sessions share its immutable background, and the
pipeline/final checkpoint reuse the accumulated result image. Full-vector
validation, trace sync, and JSON checkpoint persistence were measured but were
not the dominant term, so the lossless JSON format remains authoritative. The
component profile, allocation counts, end-to-end table, intercept, competing-
load caveat, and reproduction commands are in
[`docs/single-circle-extend-report.md`](docs/single-circle-extend-report.md).

**Acceptance Checks:**

- [x] A benchmark reports the fixed and per-iteration terms separately at 500,
      2 000, and 3 000 circles, so the intercept's growth with circle count is
      visible rather than inferred.
- [x] Checkpoint round-trip stays lossless: a resumed job reproduces the parent
      cost exactly.

### Task 15.10: Refresh Post-Fix Polishing Evidence (P2)

- [ ] Re-measure `BenchmarkPolishStrategyQualityAfterBatchFit` after the Task
  15.6 gate correction and refresh `docs/contiguous-window-polish-report.md`;
  the old ranking was partly determined by which active set happened to cover
  inherited blocker circles.

## Phase 16: Declarative Run Schedules

Schedules replace one-off external orchestration with a persisted,
server-owned campaign: validate and estimate a declarative document, execute
base/extend/polish stages in order, survive restarts, and inspect the resulting
chain through the API, CLI, and UI. The format and cost-comparison rules are
authoritative in `docs/schedule-format.md`.

### Completed Tasks 16.1–16.7 ✅

Implemented: versioned schedule models and validation; durable server-side
execution and recovery; declarative stage policy; dry-run estimation; campaign
API/UI/CLI surfaces; migration away from the external Python orchestrator; and
bounded projections/pagination so campaigns up to `MaxScheduleStages` remain
readable without exceeding the CLI response cap.

### Task 16.8: Let a Coverage Pass Start Where the Value Is (P2)

`contiguous-window` sized for total coverage is the strongest polishing recipe
measured on a fitted vector -- see the 2026-08-18 section of
[`docs/contiguous-window-polish-report.md`](docs/contiguous-window-polish-report.md)
-- but it spends the first half of every pass on the cheap end of the vector.
Gain by quarter, two consecutive passes over the 1000-circle fit of
`example/Christian_after.jpeg` (`activeSetSize` 32, 32 sweeps):

| Pass | Q1 | Q2 | Q3 | Q4 | Total |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 3 | -0.015 | -0.383 | -1.058 | -1.290 | -2.746 |
| 4 | -0.021 | -0.277 | -0.600 | -1.348 | -2.245 |

`selectContiguousWindowCircles` scans from the latest start downward and keeps a
strictly better total, so with empty visit counts it picks slot 968 first and
steps back 32 slots per sweep (`internal/fit/renderer/batch_polish.go:645`). The
low draw slots -- the early, large circles placed against a nearly empty canvas
and never revisited -- are reached only in the final quarter, and that is where
essentially all of the gain is.

`visitCounts` is rebuilt per `PolishCircleBatchContext` call
(`batch_polish.go:187`), so **every continuation restarts at slot 968** and
re-walks the low-value end before reaching the high-value one, no matter how
many passes have already covered it.

The docstring justifies latest-first as a render-cost optimization: a window
starting at `s` costs `circleCount - s` rasterizations per candidate because the
prefix is baked, so a late window is cheap. That holds for a partial budget.
**Over a full coverage cycle it is false** -- simulating both traversals for
`circleCount` 1000, `activeSetSize` 32, 32 sweeps:

| Traversal | Rasterizations/candidate | Coverage |
| --- | ---: | ---: |
| latest-first (current) | 16,872 | 1000/1000 |
| earliest-first | 16,152 | 1000/1000 |

Same 32 windows, opposite order, and the current one is 4.5% *more* expensive.
There is no render-cost argument for the current ordering once the budget covers
everything; the ordering is costing gain and buying nothing.

- [ ] Order the traversal by where the budget is worth spending rather than by
      window start. Deciding between "earliest-first when the sweep budget
      covers the vector, latest-first otherwise" and a residual- or
      contribution-weighted window choice is part of the task; the partial-budget
      case must keep its current cheapness.
- [ ] Carry visit counts across a polish continuation so a chained pass does not
      re-walk ground its parent already covered. The counts are per-call today;
      the parent's coverage is derivable from the chain, and `polishedFrom`
      already records the link.
- [ ] Leave the selector deterministic. Identical config, parent, and seed must
      still reproduce bit-for-bit — the property that makes a fresh seed the
      only thing distinguishing one pass from the next.

**Acceptance Checks:**

- [ ] A unit test asserts the traversal covers every draw slot for
      `circleCount` 1000, `activeSetSize` 32, 32 sweeps, and that summed
      rasterizations per candidate do not regress against latest-first.
- [ ] A test pins the partial-budget case: at the shipped default sweep count,
      the chosen windows are no more expensive than they are today.
- [ ] A chained continuation starts on slots its parent did not cover, asserted
      on the selected active sets rather than on cost.
- [ ] Determinism regression: same config, parent, and seed produce an identical
      cost across two runs.

### Task 16.9: Let the Estimator Spend the Scarce Resource (P2)

A campaign run to 3 000 circles on 2026-08-19/20 produced a growth recipe that
the schedule format can express but the estimator gives no guidance on. All
figures are from A/Bs branching from a common parent on
`example/Christian_after.jpeg` (512x512), equal added circles per arm.

- **Granularity.** `+1` per extend beat `+4` by **4.05 cost units** at identical
  population and per-circle budget (202.114 vs 206.163 at 312 circles), and the
  advantage compounded: the arm was 1.2% behind a reference campaign at 312
  circles and 10.8% ahead at 1 000. `additionalCircles: 1` should be the
  documented default for growth, with batching presented as a throughput
  convenience that is not in fact faster per circle.
- **Population is coupled to epochs.** At `optimizerEpochs` 1, raising `popSize`
  30 to 100 moved cost by **0.026** for 2.2x the wall clock; at `epochs` 3 the
  same change was worth **1.94**. A configuration raising one without the other
  is paying full price for nothing.
- **The objective flips with what is scarce.** While the circle budget is open,
  gain per *hour* is the right objective and the cheapest converging settings
  win — `iters` 500 with `epochs` 1 carried 300 to 2 000 circles. Against a
  fixed circle ceiling, gain per *circle* is the objective: at 2 000 circles
  over a 50-circle sample, `iters` 500 returned **1.84x the gain per circle** of
  `iters` 50 (0.0207 vs 0.0113) while returning roughly half the gain per hour
  (6.05 vs 11.3 units/hour).
- **Sample size.** The same comparison over 12 circles ranked `iters` 1 500
  below `iters` 500, and a single-circle probe showed `iters` 50 and 500 landing
  on the same cost. Neither survived a 50-circle sample. Any estimator advice
  derived from short probes needs a stated sample size.

- [ ] Extend `schedule estimate` (Task 16.4) to report projected cost per circle
      and per hour separately, so a campaign against a circle ceiling and one
      against a time budget are not given the same answer.
- [ ] Warn at validation when a document raises `popSize` above the default with
      `optimizerEpochs` 1, naming the measurement.
- [ ] Record the recipe and its evidence in `docs/schedule-format.md`, including
      that `MaxCircles` was raised 1000 -> 3000 over this campaign because the
      cap, not diminishing returns, was the binding constraint each time.

**Acceptance Checks:**

- [ ] The estimator's two projections are asserted against the measured campaign
      figures at 1 000, 2 000, and 3 000 circles
      (96.199 / 64.602 / 46.905, PSNR 28.299 / 30.028 / 31.419).
- [ ] A validation test covers the population-without-epochs warning.

---

## Phase 17: Dashboard Start Page

### Completed Tasks 17.1–17.10 ✅

Implemented: a reproducible esbuild/TypeScript/React-island pipeline with a
committed embedded bundle; host facts, dashboard read models, global progress
and ordered invalidation SSE; the new dashboard and `/jobs` routes; live
campaign/job charts; a shared five-mode image viewer; authoritative REST
reconciliation across disconnects; and server/UI/unit/race/Chromium coverage.
templ remains the complete no-JavaScript fallback, and React progressively
hydrates it. The observable trust-boundary and release-gate changes are
documented in `docs/behavior-invariants.md` and `docs/releasing.md`.

### Task 17.11: Browser, Bundle, and Documentation Sign-Off (P1)

The open acceptance items from Task 17.9 live here so its implemented test and
documentation work can remain compact.

- [ ] Capture and add the README dashboard screenshot on a working browser
  runner.
- [ ] Observe `just check` with npm available, including the bundle drift gate,
  and prove it rejects a stale committed bundle.
- [ ] Verify the dashboard shows correct stat tiles, ordered campaign cards,
  running jobs, and an architecture badge matching a forced
  `MAYFLY_SIMD_TIER`.
- [ ] Start a campaign and observe its card move to running with a ticking chart.
- [ ] Check chart legibility in auto, forced-light, and forced-dark themes.
- [ ] Exercise all five campaign image modes, overlay opacity, and shortcuts
  `1`–`5`.
- [ ] With JavaScript disabled, verify the dashboard and campaign cost plot
  remain complete and readable.
- [ ] Kill and restart the server while viewing the dashboard; verify the client
  reconnects, refetches, and converges without a navigation.

### Task 17.12: Bound the Restore-Path Resident Set (P2)

Task 17.11's restart check passes, but the restart itself is the expensive part.
`restorePersistedJobs` (`internal/server/restore.go`) loads every job on disk
into memory and keeps it there for the process lifetime: `LoadCheckpoint` per
job pulls in the full `BestParams` vector, and `restoreJobTrace` rebuilds
`MetricHistory` from every line of every `trace.jsonl`. The collection endpoints
no longer do this per request — the `checkpoint-info.json` sidecar added in #50
projects a listing without touching parameters — but the restore path was not
part of that change.

Measured 2026-08-21 on the 3 000-circle demo campaign's data root (4 358 jobs,
900 MB of checkpoints, 697 MB of traces):

| | value |
|---|---|
| Startup to `Restored persisted jobs` | ~135 s (incl. one-off sidecar backfill) |
| Resident baseline, idle | 1.34 GB |
| Resident after 10 min of a running polish stage | 1.50 GB |
| `GET /` | 5.3 s |

The baseline is proportional to all history, not to what is being served, and
the campaign adds ~2 900 more jobs at rising circle counts. The preceding OOM
kill was caused by per-request re-parsing and is fixed; this is the floor that
remains under it.

- [ ] Restore from the `CheckpointInfo` sidecar and load `BestParams` lazily on
      the paths that actually need a parameter vector (resume, extend, render,
      artifact download).
- [ ] Stop holding full `MetricHistory` for terminal jobs. The chart endpoints
      can read the trace on demand or a downsampled summary can be persisted
      beside the checkpoint; only running jobs need the live series in memory.
- [ ] Serve `GET /` and `/api/v1/dashboard` from a cached sidecar index
      invalidated by checkpoint writes, rather than re-walking the job tree.

**Acceptance Checks:**

- [ ] Resident set after restore is reported for 500, 2 000, and 4 000 persisted
      jobs and is sub-linear in job count; record the host and the data root's
      checkpoint and trace byte totals with the numbers.
- [ ] `GET /` and `/api/v1/dashboard` latency is reported at the same three
      sizes and does not grow with total job count.
- [ ] A restored job still resumes, extends, and renders identically — a resumed
      job reproduces the parent cost exactly, as in Task 15.9.
- [ ] Job detail, campaign charts, and the trace download return the same series
      as before for a terminal job whose history is no longer resident.

## Summary and Next Steps

Completed implementation history is intentionally summarized above; detailed
design decisions, measurements, and observable contracts belong in `docs/`,
tests, and git history. A completed marker records implementation for its
historical revision, not a fresh release-gate result.

Current open work, in priority order:

1. **Release gate (P0):** Task 14.13.
2. **Correctness and throughput (P1):** Tasks 15.7 and 15.8, followed by the
   remaining Phase 15 measurement and optimization tasks.
3. **Dashboard sign-off (P1):** Task 17.11.
4. **Server memory (P2):** Task 17.12.
5. **Schedule quality (P2):** Tasks 16.8 and 16.9.
6. **UX and supporting documentation (P2/P3):** Tasks 12.9 and 13.15.
7. **Experimental backends/research:** Tasks 11.9–11.13 and 10.20.

Do not mark a check complete from its presence in code or CI configuration
alone. Record the exact command or observed CI result for the revision, and
include host/workload/allocation conditions with performance claims.
