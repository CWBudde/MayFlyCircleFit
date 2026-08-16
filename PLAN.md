# MayFlyCircleFit Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Build a high-performance circle-fitting optimization tool with CPU/GPU backends, web UI, and live progress visualization.

**Architecture:** Go-based CLI with Cobra, modular optimizer/renderer interfaces, HTTP server with SSE streaming, templ-based UI, and SIMD-accelerated evaluation kernels.

**Tech Stack:** Go 1.24+, Cobra (CLI), templ (frontend), slog (logging), net/http (server), optional chi (routing), cgo+SIMD (performance), OpenGL/OpenCL (GPU)

> **Production-readiness audit (2026-08-09):** Historical `COMPLETE` markers below describe implementation milestones, not validated production readiness. Phase 14 supersedes affected completion claims until its acceptance checks pass. Remediation code, release-gating CI, and corrected documentation are now present, but no gate is marked passed solely because its workflow was added; final clean-clone and repeated CI verification remain release requirements.

---

## Phase 1: Core Domain Model ✅ COMPLETE

Implemented and tested the circle model, parameter encoding, bounds/clamping, and RGB MSE cost (6 tests).

---

## Phase 2: CPU Renderer ✅ COMPLETE

Implemented and tested the `Renderer` interface and bounding-box CPU renderer with Porter-Duff alpha compositing (2 tests).

---

## Phase 3: Optimizer (Mayfly - Using External Library) ✅ COMPLETE

Implemented and tested the optimizer interface and Mayfly adapter with Standard, DESMA, and OLCE variants (2 tests); the project is pinned to `github.com/cwbudde/mayfly v0.4.0`.

---

## Phase 4: Pipelines (Joint, Sequential, Batch) ✅ COMPLETE

Implemented and tested joint, sequential, and batch optimization pipelines plus `OptimizationResult` (3 tests).

---

## Phase 5: CLI with Cobra (Log-only UX) ✅ COMPLETE

Implemented the Cobra CLI foundation, single-shot `run` workflow, metrics, command stubs, and validation asset.

---

## Phase 6: Background Server + Job Model + Live Progress ✅ COMPLETE

**Goal:** A long-running HTTP server that executes optimizations in the background with real-time progress via SSE.

Implemented the job manager, background execution, hardened HTTP foundation, REST/image endpoints, CLI integration, tests, and documentation. Task 6.5 records the originally deferred SSE work (later implemented in Phase 7).

### Task 6.1: Job Management Core ✅

Completed: thread-safe job state, storage, lifecycle methods, and tests.

### Task 6.2: Background Worker ✅

Completed: context-aware background execution, progress/state updates, error handling, and tests.

### Task 6.3: HTTP Server Foundation ✅

Completed: HTTP server construction, routes, middleware, graceful shutdown, and lifecycle tests.

### Task 6.4: REST API Endpoints ✅

Completed: validated job creation/list/status and best/difference image endpoints with integration coverage.

### Task 6.5: Server-Sent Events (SSE) for Live Progress ✅

Completed as an optional second alternative to polling: `GET
/api/v1/jobs/:id/stream` sends an immediate job snapshot, throttled progress
events (iteration, cost, and circles/second), heartbeats, and one terminal event
before closing. The per-job broadcaster handles concurrent subscribers, slow
clients, cancellation/failure, and disconnect cleanup. Integration and
concurrency tests cover the stream lifecycle; clients that cannot use streaming
HTTP can continue using the status endpoint.

### Task 6.6: CLI Integration - Serve Command ✅

Completed: `serve` flags, server startup, signal handling, graceful shutdown, logging, and manual verification.

### Task 6.7: CLI Integration - Status Command ✅

Completed: list/single-job status queries, output formatting, connection handling, and manual verification.

### Task 6.8: Integration Testing ✅

Completed: job-flow, concurrency, error, graceful-shutdown, and SSE lifecycle integration coverage.

### Task 6.9: Documentation ✅

Completed: server/API/lifecycle documentation and curl examples, including polling and SSE progress alternatives.

---

## Phase 7: Frontend with templ (Reference vs Fittest) ✅ COMPLETE

**Goal:** Pretty, minimal dashboard that shows the current best vs reference and a small metrics panel.

### Task 7.1: Set up templ Infrastructure

Completed: templ tooling, generation workflow, UI structure, ignore rules, and setup documentation.

### Task 7.2: Create Base Layout Template

Completed: base HTML layout, lightweight styling, metadata, navigation, asset serving, and tests.

### Task 7.3: Implement Job List Page (`/`)

Completed: routed job list with status, thumbnails, metrics, creation/detail links, and integration coverage.

### Task 7.4: Implement Job Detail Page (`/jobs/:id`) ✅

Completed: routed two-pane job detail with metrics, refresh/error states, and integration coverage.

### Task 7.5: Integrate SSE for Live Updates ✅

Completed: throttled SSE progress streaming, broadcaster/worker integration, disconnect handling, and tests.

### Task 7.6: Implement Auto-Refreshing Images ✅

Completed: SSE-driven cache-busted image refresh with loading/error states and slow-network testing.

### Task 7.7: Add Optional Cost Sparkline Visualization ✅

Completed: bounded, toggleable SSE cost-history sparkline with pattern tests.

### Task 7.8: Create Job Creation Form UI ✅

Completed: routed job-creation form, server validation, background start/redirect, inline errors, and integration tests.

### Task 7.9: Test End-to-End UI Flow ✅

- [x] Start server, verify job list loads
- [x] Create job via form, verify redirect to detail page
- [x] Confirm images display (reference, best, and diff)
- [x] Verify SSE connection and live metrics updates
- [x] Verify sparkline updates with cost data from SSE
- [x] Test multiple concurrent jobs (created and tracked 5 jobs successfully)
- [x] All server tests pass with context.Background() fix for job execution
- [ ] Validate on different browsers (Chrome, Firefox, Safari) - Manual testing required
- [ ] Test mobile responsiveness - Manual testing required

### Task 7.10: Documentation and Polish ✅

Completed: UI/templ/SSE architecture, conventions, troubleshooting, and project-status documentation.

**Deliverables:**

- Working web UI with job list and detail pages
- Live progress updates via SSE
- Auto-refreshing images
- Cost sparkline visualization
- Job creation form
- Comprehensive test coverage

**Acceptance Checks:**

- [x] With `serve` running, visiting `/` shows job list
- [x] Job detail page visually shows progress (images update, cost ticks)
- [x] SSE updates work without page refresh
- [x] Form creates jobs successfully
- [x] UI works on modern browsers (Chrome, Firefox, Edge)
- [x] Cost sparkline displays and updates in real-time
- [x] All templ components render correctly
- [x] Error handling works gracefully (job not found, validation errors)
- [x] Images auto-refresh with loading states
- [x] Comprehensive documentation covers all features
- [ ] UI tested on Safari (deferred - requires macOS)
- [ ] Mobile responsiveness validated (deferred - requires manual testing)

**Phase 7 Status:** ✅ **COMPLETE** - All core functionality implemented and documented. Optional testing on Safari and mobile devices deferred to Phase 12 (UX polish).

---

## Phase 8: Persistence & Checkpoints (Resume) ✅ COMPLETE

Implemented and tested filesystem-backed atomic checkpoints, trace logging, CLI/server restart-from-best, graceful-shutdown saves, retention utilities, and end-to-end recovery. Detailed results and limitations are recorded in `docs/checkpoint-resume-test-results.md`; Phase 14 later corrected live checkpoint, trace, and resume semantics.

---

## Phase 9: Performance Profiling & Fast Paths (CPU) ✅ COMPLETE

**Goal:** Identify bottlenecks and implement safe, incremental speedups on CPU.

### Task 9.1: Set Up Profiling Infrastructure ✅

Completed: CLI/server CPU and memory profiling, pprof routes, helper scripts, documentation, and validation.

### Task 9.2: Profile Baseline Performance ✅

Completed: small-to-large workload profiles, hotspot analysis, baseline metrics, and `docs/baseline-performance-report.md`.

### Task 9.3: Optimize Circle Rasterization - AABB Precomputation ✅

Completed: AABB precomputation and early rejection with pixel-equivalence benchmarks; 1.42× speedup (41.7% faster).

### Task 9.4: Optimize Memory Allocation in Renderer ✅

Completed: reusable buffers and cached background reset; 1.065× speedup and 98.1% allocation reduction.

### Task 9.5: Optimize Data Layout for Cache Efficiency ✅

Completed: analysis retained AoS as optimal; SoA was projected to regress performance by 10–20%, so no code change was made.

### Task 9.6: Optimize Inner Rendering Loops ✅

Completed: strength reduction, common-subexpression reuse, and offset inlining; 1.395× speedup and 39.5% higher throughput. Cumulative Phase 9 speedup: 2.11×.

### Task 9.7: Add Optional Multi-Threading for Rendering ✅

Completed: the CPU renderer shards disjoint scanline bands across a configurable
worker count, preserves circle compositing order within every band, defaults to
`GOMAXPROCS`, and caps workers at `GOMAXPROCS` and image height. Local runs use
`--threads`; API jobs use the optional `threads` field. Pixel-exact race tests,
session propagation, scaling benchmarks, CPU profiles, and operational guidance
are recorded in `docs/cpu-rendering-threads.md`. On the measured 12-logical-CPU
host, 512×512/100-circle rendering improved 4.40×, while tiny workloads remained
faster with `--threads 1`.

### Task 9.8: Create Comprehensive Benchmarks ✅

Completed: `internal/fit/bench_test.go` provides deterministic CPU rendering,
standalone cost, and fixed-seed joint/sequential/batch pipeline benchmarks over
representative workloads. `just benchmark` records statistically useful
samples, `just benchmark-compare` compares saved runs with pinned `benchstat`,
and report-only CI compares base/head results on the same runner without using
noisy timing changes as a merge gate. Usage and interpretation are documented
in `docs/benchmarks.md`.

### Task 9.9: Measure and Document Performance Improvements ✅

Completed: same-host deterministic benchmarks compare the pre-optimization,
AABB/canvas-reuse, inner-loop, pre-threading, and multi-threading milestones.
The CPU renderer improved 2.09-2.47× on one thread and 6.39× for the large
12-thread case, while timed serial allocations fell to zero. Matched profiles,
flame views, per-optimization findings, reproduction templates, and remaining
bottlenecks are recorded in `docs/task-9.9-performance-report.md`; pprof is now
pinned as a Go tool for reproducible analysis.

### Task 9.10: Validate Correctness After Optimizations ✅

Completed: pixel-exact baseline tests cover single- and multi-threaded CPU
rendering, edge cases, varied workloads, custom canvases, and cost parity. They
also found and fixed the sub-0.001 opacity rejection regression. Results and
tradeoffs are documented in `docs/task-9.10-correctness-validation.md`.

**Deliverables:**
- Profiling infrastructure and scripts
- Comprehensive benchmark suite
- Optimized CPU renderer with measurable speedup
- Performance report with before/after metrics
- Documentation of optimization techniques

**Acceptance Checks:**
- [x] Profiling shows top offenders moved in right direction
- [x] Benchmarks demonstrate improvement without changing outputs
- [x] All existing tests still pass
- [x] Memory allocations reduced significantly
- [x] Rendering throughput increased by measurable amount

---

## Phase 10: SIMD/C Intrinsics Research & Implementation (Evaluation Loop)

**Goal:** Recover a large chunk of the original "blazing fast" feel by applying vectorized kernels to the evaluation hot path.

**Status:** Research complete, implementation in progress

### Task 10.1: Research SIMD Approaches and Design ✅ COMPLETE

Completed: selected Plan 9 assembly with runtime dispatch and scalar fallback; documented design, portability, build tags, alignment, and expected AVX2/NEON gains in `docs/simd-design.md`.

### Task 10.2: Design SSD Kernel Interface ✅ COMPLETE

Completed: `fastSSD`/`FastSSD` interfaces, runtime backend dispatch, scalar baseline, backend reporting, and a 27-test validation harness.

### Task 10.3: Implement Scalar Baseline SSD Kernel ✅ COMPLETE

Completed: optimized pure-Go NRGBA SSD baseline with alpha exclusion, equivalence/edge tests, and benchmarks.

### Task 10.4: Implement AVX2 SSD Kernel (x86-64) ✅ COMPLETE

Completed with hand-written Plan 9 assembly and runtime AVX2 dispatch; the build
has no C, cgo, or GoAT dependency. Exact scalar-equivalence tests cover SIMD
boundaries, remainders, padded rows, alpha exclusion, and large accumulators.
Measured throughput is 2.4–2.6 Gpixels/s, approximately 6× the scalar baseline;
details are in `docs/task-10.4-avx2-report.md`. Obsolete C prototypes were
removed; their research results remain documented and recoverable from history.

### Task 10.5: Implement NEON SSD Kernel (ARM64) ✅ COMPLETE

Completed with hand-written Plan 9 assembly and runtime ASIMD dispatch; the
build has no C, cgo, or GoAT dependency. The kernel processes four NRGBA pixels
per batch, ignores alpha, handles scalar remainders, and reduces into a 64-bit
accumulator. Exact tests cover boundaries and totals exceeding 32 bits, and
Linux, macOS, and Windows ARM64 builds cross-compile with `CGO_ENABLED=0`.
Physical Apple Silicon and Linux ARM64 measurements remain explicitly tracked
by Tasks 10.8–10.10; no unmeasured speedup is claimed. See
`docs/task-10.5-neon-report.md`.

### Task 10.6: Implement Runtime Feature Detection and Dispatch ✅ COMPLETE

Completed: architecture-specific initialization uses `x/sys/cpu` to select
AVX2, NEON, or scalar once at startup and logs the active backend. Dispatch
tests cover feature/backend consistency and prove the amd64 scalar fallback in
a fresh process with `GODEBUG=cpu.avx2=off`; a focused direct-versus-function-
pointer benchmark measures dispatch overhead independently of kernel work. A
five-run local sample measured about 0.18 ns mean overhead, below the 2 ns target.

### Task 10.7: Integrate SIMD SSD into Cost Function ✅ COMPLETE

Completed: CPU renderers use the runtime-dispatched `FastMSECost` by default
while retaining `MSECost` as the scalar oracle and custom-cost opt-out. Exact
integration tests cover both constructors and varied SIMD-boundary image sizes;
corrected full-cost benchmarks compare distinct scalar and fast paths. On the
local AVX2 host, direct cost evaluation improved by 9.1–18.3× and SSD accounted
for 0.60% of flat samples in the profiled 512×512/K100 production workload, so
rendering is again the bottleneck. Integration details and measurements are in
`docs/task-10.7-simd-cost-integration.md`.

### Task 10.8: Cross-Platform Testing and Build Validation ✅ COMPLETE

Completed with a reproducible `just cross-build` gate covering all six planned
targets under `CGO_ENABLED=0`. The validator asserts the exact Go/assembly file
selection before building each CLI, including the generic scalar dispatcher on
Linux/386. Native CI now executes the SSD suite and records scalar-versus-SIMD
throughput on Linux AMD64, macOS ARM64, Windows AMD64, and Linux ARM64; each job
requires AVX2 or NEON rather than accepting a silent scalar fallback. Local
Linux/AMD64 validation measured a 6.1–6.5× AVX2 kernel speedup across 64–512
pixel-square inputs. See `docs/task-10.8-cross-platform-validation.md`.

### Task 10.9: Create SIMD Test Matrix ✅ COMPLETE

Completed on native Linux/AMD64 and Apple M5 ARM64 hardware. Required-backend
tests prove AVX2 and NEON selection, fresh-process feature overrides validate
both scalar fallbacks, and exact scalar/SIMD comparisons cover batch boundaries,
remainders, padded strides, concurrency, and large accumulators. Native CI runs
both feature-enabled and `cpu.all=off` suites. See
`docs/task-10.9-simd-test-matrix.md`.

### Task 10.10: Performance Validation and Documentation ✅ COMPLETE

Completed with a zero-allocation 64×64–1024×1024 benchmark matrix. Five-sample
medians measured AVX2 at 6.0–6.3× through 512² before a 1024² cache-related drop,
while Apple M5 NEON sustained about 6.9 Gpixels/s and 5.2× throughout. Hardware
counter access was unavailable without weakening host security, so cache
behavior is documented from working-set and throughput scaling. M5 profiling
also yielded an exact opaque-canvas compositing fast path, reducing the full
512²/K100 workload by 12.3%. See
`docs/task-10.10-simd-performance-report.md`.

### Task 10.11: Circle Rendering Optimization - **PARTIALLY COMPLETE**

Completed scoped work: scanline rasterization was profiled, integrated, and
pixel-equivalence tested, improving the full 256×256/50-circle pipeline by
1.28×. A later profile-guided opaque-canvas fast path improved the M5
512×512/K100 workload by another 1.14×. Further rendering work remains
explicitly open in Tasks 10.12–10.15.

### Task 10.12: SIMD Horizontal Span Compositing ✅ COMPLETE
**Rationale**: Alpha compositing was 72.56% of flat samples in the post-Task
10.10 M5 profile. Moving invariant work out of the pixel loop and selectively
using SIMD can process spans more efficiently.

**Approach:**
- [x] Implement an exact Go scalar span kernel that hoists foreground and blend terms
- [x] Implement an eight-pixel ARM64 NEON kernel in Go Plan 9 assembly
- [x] Handle short spans and remainders with the scalar span kernel
- [x] Gate NEON on ASIMD and a measured 256-pixel crossover threshold
- [x] Preserve the general Porter-Duff path for translucent custom canvases
- [x] Benchmark isolated compositing and the full one-thread renderer on Apple M5
- [x] Validate exact boundaries, randomized pixels, feature-disabled fallback, and renderer parity

**Technical Details:**
```go
if opaqueCanvas {
    compositeOpaqueSpan(img.Pix, offset, pixels, r, g, b, alpha)
} else {
    // Preserve the general translucent-destination path.
    compositePixel(img, x, y, r, g, b, alpha)
}
```

**Measured Outcome:**
- Horizontal span integration: 3.883 ms → 2.015 ms median on the final
  controlled 512×512/K100 M5 render benchmark (1.93×, zero allocations).
- Exact float64 NEON alone crosses the M5 scalar span only on long spans: about
  1.02× at 64–256 pixels, while losing at 8–16 pixels. Production dispatch is
  therefore deliberately conservative rather than applying SIMD everywhere.
- Post-change profile: scalar span 65.01%, scanline traversal 26.47%, gated
  NEON 1.95%. See `docs/task-10.12-neon-span-report.md`.

### Task 10.13: Fixed-Point and Reduced-Precision Circle Geometry - **IN PROGRESS**
**Rationale**: Scanline geometry still performs repeated `float64` conversions,
squares, and comparisons while searching both span edges. Reduced-precision or
fixed-point arithmetic may lower that cost, particularly on AMD64, but must be
judged against whole-render performance and rasterization error rather than an
assumed instruction-level speedup.

**10.13a — Establish the geometry baseline and candidates:**
- [x] Add deterministic geometry-only benchmarks for representative radii,
  fractional centers, clipped circles, and row shards
- [x] Compare the current `float64` oracle with scalar `float32` and signed
  Q16.16 implementations
- [ ] Evaluate signed Q24.8 (wider range) and Pascal-style Q8.24 (higher
  fractional precision, requiring normalization for normal image sizes)
- [x] Record full one-thread render results separately so compositing does not
  hide geometry regressions

**10.13b — Select a fixed-point contract:**
- [x] Quantize `X`, `Y`, and `R` once per decoded circle; keep squared values in
  `int64` (an `int32` Q value cannot safely hold its own square)
- [ ] Prefer Q16.16 when its coordinate range is safe; evaluate Q24.8 as the
  wider-range, lower-precision alternative and retain `float64` as an overflow
  fallback for unusually wide or tall images
- [x] Define rounding, signed-coordinate, clipping, and overflow behavior
- [x] Measure changed coverage against the `float64` oracle with boundary-heavy
  and randomized cases; require byte-identical compositing for unchanged spans

**10.13c — Implement the winning scalar path:**
- [x] Precompute `r²`, per-row `r²-dy²`, and fixed-point center terms
- [x] Use eight-pixel monotonic skips plus addition/subtraction recurrences for
  the scalar tail instead of repeated per-pixel squaring
- [x] Integrate behind an internal geometry dispatcher with zero allocations
- [x] Keep the general `float64` path as the correctness oracle and range fallback

**10.13d — Investigate AMD64 SIMD/assembly:**
- [x] Inspect compiler output for scalar `float32` and fixed-point candidates
- [x] Prototype an AVX2 float32 span-edge search that checks eight candidate X values
  per batch; account for mask extraction, short-span setup cost, and scalar tails
- [x] Benchmark direct kernel calls and full rendering with `x/sys/cpu` feature
  gating; retain Q16.16 for production because AVX2 float32 did not beat it
- [x] Prototype exact AVX2 Q16.16 span edges and compare them with the scalar
  monotonic skip; retain scalar Q16.16 because AVX2 widened products and mask
  interleaving cost 1.4-2.9× more in direct R5-R256 span searches
- [x] Apply the same one-test/eight-pixel monotonic skip to scalar `float32` and
  `float64`; verify 100,000 randomized cases against the one-pixel searches
- [ ] Assess the corresponding ARM64 NEON opportunity without making AMD64-only
  layout choices that prevent a later implementation

**10.13e — Validation and documentation:**
- [ ] Test fractional/tangent boundaries, radii 1 and maximum radius, clipping on
  every image edge, SIMD batch boundaries, randomized circles, and row sharding
- [x] Run native AMD64 tests plus the existing six-target cross-build gate
- [x] Publish initial precision and native AMD64 throughput results in
  `docs/task-10.13-fixed-point-report.md`; complete cross-platform results and
  backend selection before closing the task

**Scaling invariant:** if coordinates use `Q = round(value * 2^F)`, squared
distances all use scale `2^(2F)`:
```go
dxQ := (int64(x) << F) - xQ
dyQ := (int64(y) << F) - yQ
inside := dxQ*dxQ+dyQ*dyQ <= radiusQ*radiusQ
```
Mixing a Q-scaled squared term with a once-shifted value is dimensionally
invalid. Q16.16 therefore uses `int32` coordinates and `int64` products, with a
range check before conversion.

**Success criteria:**
- Geometry microbenchmarks improve by at least 10% on a supported native host
- Full 512×512/K100 one-thread rendering does not regress and preferably improves
  by at least 3% across repeated samples
- The precision report quantifies every deviation from the `float64` coverage
  oracle; exact mode and out-of-range inputs retain the oracle path

**Initial AMD64 outcome (Ryzen 5 4600H):** Q16.16 with monotonic eight-pixel
skips is 2.75× faster for R25 geometry, 5.02× for R100, and 4.51× for a
clipped R256 circle. The controlled one-thread 512×512/K100 renderer improved
from 9.13 ms to 7.98 ms median (1.14×, zero allocations). Q16.16 changed 15
of 2,022,704 randomized intersecting row spans (0.00074%); alternate formats,
adversarial boundaries, and cross-platform validation remain open.

**AVX2 float32 follow-up:** the hand-written eight-lane kernel is exact relative
to scalar float32 across 100,000 randomized span searches. It decisively beat
the original one-pixel scalar search. After scalar float32 gained the same
monotonic eight-pixel skip, AVX2 is competitive in direct calls through roughly
R100 and loses by about 1.45× at R256; across whole-circle rows scalar is 9-24% faster.
Production dispatch remains Q16.16; AVX2 float32 is retained as a runtime-gated
benchmark/experimental backend.

**Monotonic batching follow-up:** the eight-pixel shortcut is not specific to
integers. Applying it to scalar `float32` and `float64` preserves exact results
relative to their original one-pixel searches. A real AVX2 Q16.16 kernel was
also implemented and proved exact across 100,000 randomized cases, but its two
`VPMULDQ` streams and mask interleaving make it 1.4× slower at R5 and roughly
2.5-2.9× slower at R25-R256 than scalar Q16.16. It therefore remains a tested
prototype rather than the production dispatcher.

Seven-sample full-render medians after all batching changes were 8.09 ms for
exact `float64`, 8.67 ms for scalar `float32`, 7.76 ms for AVX2 `float32`, and
7.66 ms for scalar Q16.16. Q16.16 therefore remains the production path, but
the margin over the newly batched exact oracle is now about 6%, not the earlier
14% measured against the one-pixel float64 search.

### Task 10.14: Circle Symmetry Exploitation ✅ PROTOTYPE COMPLETE
**Rationale:** Determine whether vertically paired rows can reduce Q16.16 span
search and compositing overhead without changing fractional circle coverage or
the renderer's row-sharded ownership model.

**10.14a — Correct the symmetry contract:**
- [x] Require `y1 + y2 == 2*centerY`; integer sampled rows therefore share a
  span only when the Q16.16 Y center is an integer or half-integer
- [x] Keep arbitrary fractional centers on the ordinary row loop instead of
  rounding their geometry and changing coverage
- [x] Treat clipped circles, negative/clipped centers, the single center row,
  and worker-shard edges as first-class correctness cases

**10.14b — Build the exact prototype:**
- [x] Replace the initial skip-based experiment with a true two-ended loop,
  which consumes one row pair per iteration and renders asymmetric shard edges
  independently
- [x] Reuse one Q16.16 span search for each eligible pair and add both rows to
  incremental dirty-span tracking
- [x] Add a paired opaque-span compositor: non-ARM64 scalar builds share blend
  setup and loop control across both rows, while ARM64 retains its measured
  NEON dispatch
- [x] Preserve circle order and pair only rows inside the current worker shard,
  retaining race-free disjoint row ownership

**10.14c — Measure and select:**
- [x] Verify byte parity with ordinary rendering on opaque and translucent
  canvases, including mixed eligible/ineligible circles and 1/4 workers
- [x] Verify exact incremental-cost parity and ordinary/staged session settings
- [x] Benchmark fractional, 100%-eligible R5/R25/mixed-radius, and four-worker
  512×512/K100 fixtures with zero single-worker steady-state allocations
- [x] Keep symmetry opt-in: the AMD64 best case improved 5.7% for mixed large
  radii and 16-17% for R5/R25 with one worker, but four workers showed no win,
  and continuous optimizer centers are eligible only about 1 in 32,768 times

See `docs/task-10.14-circle-symmetry-report.md` for the derivation, benchmark
medians, and production decision.

### Task 10.15: Combined CPU Optimization ✅ COMPLETE
**Rationale:** Validate the optimized renderer as one production path instead
of multiplying isolated speedup estimates. Task 10.12 span compositing, Task
10.13 Q16.16 geometry, and the exact Task 10.16 incremental SSD path have
different crossovers and architecture support. Task 10.14 symmetry must also
preserve fractional-center coverage and row-shard ownership.

**10.15a — Reconcile the component contracts:**
- [x] Verify that opaque-span compositing and Q16.16 geometry compose without
  allocations while translucent canvases and out-of-range geometry retain
  their exact general fallbacks
- [x] Correct the symmetry premise: sampled rows share a span only when the
  Q16.16 Y center is an integer or half-integer; arbitrary fractional centers
  cannot be rounded merely to enable mirroring
- [x] Include staged incremental SSD in end-to-end assessment rather than
  treating full-image cost as unchanged after Task 10.16

**10.15b — Prototype and select the combined path:**
- [x] Implement an exact paired-row Q16.16 prototype for eligible centers,
  including clipped circles and dirty-span tracking
- [x] Keep paired writes inside the current worker's row shard so rendering
  remains race-free and circle compositing order remains unchanged
- [x] Benchmark the old float64 pixel loop, span plus float64, production span
  plus Q16.16, and the symmetry prototype on fractional and eligible centers
- [x] Leave row symmetry disabled in production: the corrected paired
  compositor wins on fully eligible one-worker fixtures, but ordinary optimizer
  centers are almost never eligible and four-worker rendering showed no gain

**10.15c — Combined validation and documentation:**
- [x] Require byte equality between paired and unpaired Q16.16 rendering on
  opaque/translucent canvases with integer, half-integer, fractional, clipped,
  overlapping, single-threaded, and multi-threaded cases
- [x] Require exact incremental-cost parity when symmetric rows contribute to
  the dirty-region union and preserve settings across ordinary/staged sessions
- [x] Confirm zero steady-state allocations, standard Go builds, runtime SIMD
  fallbacks, and supported cross-build targets
- [x] Publish measured native results and selection decisions in
  `docs/task-10.15-combined-optimization-report.md`

**Measured AMD64 outcome (Ryzen 5 4600H):** the production opaque-span plus
Q16.16 renderer reduced the 512×512/K100 fractional-center median from 13.99
ms to 7.38 ms (1.90×, zero allocations). The corrected symmetry prototype wins
5.7-17% on deliberately 100%-eligible one-worker fixtures, but has no measured
four-worker benefit and negligible eligibility for continuous centers, so it
remains experimental. The current 256×256 sequential K1 incremental-cost path
reduced the median from 68.88 µs to 59.20 µs (1.16×) on top of the combined
renderer.

### Task 10.16: Incremental Dirty-Region Cost Accumulation
**Rationale**: `CPURenderer.Cost` currently renders a candidate and then runs
SSD over the complete image. In sequential and small-batch optimization, the
retained base canvas is already available and only the union of the candidate
circle spans can change. Updating the base cost over that dirty region may
avoid most of the full-image difference pass. This is distinct from Task 10.13,
which optimizes only circle coverage geometry.

**10.16a — Verify the legacy algorithm and establish baselines:**
- [ ] Locate and inspect the original Pascal/Delphi implementation (deferred
  until the source is available or the Go prototype is running); determine
  whether its `ErrorWeightingLoop` used a cumulative, delta, bounding-box, or
  span-based cost update rather than assuming equivalence from its name
- [ ] Document the exact legacy arithmetic and any 16.16, 8.24, float32, MMX,
  or SSE representations used for cost accumulation
- [x] Benchmark and profile the current full-image `FastMSECost` separately for
  joint, sequential single-circle, and representative batch evaluations
- [x] Measure changed-pixel and dirty-span ratios by circle radius, clipping,
  overlap, and batch size to bound the crossover search; select the final
  threshold only after the dirty-span kernel can be benchmarked

**10.16b — Design an exact incremental SSD contract:**
- [x] Precompute the retained base canvas SSD in an exact `uint64` accumulator
  and preserve the current NRGBA quantization and alpha-exclusion semantics
- [x] Track the union of changed half-open spans per row so overlapping circles
  and repeated writes never double-count a pixel
- [x] Compute candidate cost as
  `baseSSD + sumDirty(candidateError - baseError)`, using signed delta
  accumulation without intermediate overflow
- [x] Normalize to MSE only once after reduction; retain full-image evaluation
  for custom cost functions and any unsupported or uneconomical case
- [x] Define invalidation rules for retained-canvas changes, joint mode, custom
  canvases, row sharding, and renderer/session reuse

**10.16c — Implement and optimize the dirty-region path:**
- [x] Add an allocation-free dirty-span collector shared with scanline
  rendering, without coupling correctness to Q16.16 geometry
- [x] Implement an exact portable scalar delta-SSD kernel for dirty spans
- [x] Reuse or extend the AVX2 and NEON integer SSD kernels for discontiguous
  spans; benchmark SIMD setup cost and keep scalar handling for short spans
- [x] Integrate the optimized path into sequential and batch `FastMSECost`
  sessions behind a measured crossover; leave `Render` behavior unchanged
- [x] Assess whether calculating cost after each completed row shard improves
  cache locality compared with a separate dirty-span pass

**10.16d — Correctness and performance validation:**
- [x] Require exact equality with a full `FastMSECost` replay across randomized
  circles, tangent and clipped spans, overlapping circles, transparent colors,
  opaque and translucent base canvases, and SIMD batch boundaries
- [x] Cover single-thread and multi-thread row sharding plus AVX2/NEON-disabled
  scalar fallbacks under race testing
- [x] Benchmark end-to-end optimizer throughput and convergence for joint,
  sequential, and batch modes; report results by dirty-area ratio
- [x] Enable the incremental path in production only where repeated samples
  beat the full-image SIMD SSD without changing candidate ordering or cost
- [x] Publish the available arithmetic bounds, crossover policy, and
  native/cross-platform results in a dedicated performance report; explicitly
  defer the legacy comparison to 10.16a until its source becomes available

**Success criteria:**
- Exact cost parity with the existing full-image `FastMSECost` for every
  production-supported case; no float32 or fixed-point approximation is
  accepted unless separately benchmarked and explicitly opt-in
- At least 10% end-to-end improvement in sequential single-circle evaluation
  on a representative native workload, with no material regression after
  crossover fallback in joint or large-batch evaluation
- Zero steady-state allocations and safe scalar behavior on platforms without
  AVX2 or NEON

### Task 10.17: SSE2 SIMD Tier for AMD64 Hosts Without AVX2 🚧 IN PROGRESS
**Rationale:** AMD64 dispatch was AVX2 or scalar, so a CPU without AVX2 lost the
entire Phase 10 speedup rather than part of it. A profile of that configuration
attributes about 80% of flat samples to three symbols — `fit.ssdScalar` 29.96%,
`renderer.compositeOpaqueSpanScalar` 25.17%, and `fit.MSECost` 24.63% — with
`renderer.fixedCircleQ16.span` at only 2.80%. Baseline SSE2 covers those three.

**10.17a — One resolved tier, and the levers around it:**
- [x] Resolve the instruction set once in `fit.Tier()` and have every dispatch
  site install from it through `fit.RegisterTierConsumer`, replacing nine
  independent `init` functions and four different representations of "which
  backend am I on"
- [x] Make AMD64 dispatch AVX2, then SSE2, then scalar, and keep a kernel free
  to be narrower than the tier where it has no implementation - never wider,
  asserted by `TestInstalledKernelsMatchTier` and `TestRendererKernelsMatchTier`
- [x] Add `MAYFLY_SIMD_TIER` to pin any reachable tier, panicking on an
  unreachable one, with `MAYFLY_DISABLE_SIMD=1` retained as its scalar alias;
  `x/sys/cpu` marks sse2 required on AMD64, so `GODEBUG=cpu.all=off` cannot
  reach the scalar kernel there
- [x] Add `MAYFLY_REQUIRE_SIMD_TIER`, which asserts the detected tier without
  setting one, honoured by both `internal/fit` and `internal/fit/renderer`
- [x] Add `fit.SetForcedTier`, which re-runs every dispatch site so one test
  process walks the whole ladder instead of re-execing per configuration
- [x] Extend the cross-build source assertions to require the SSE2 sources on
  AMD64 and reject them everywhere else

**10.17b — Kernels:**
- [x] Implement a four-pixel SSE2 SSD kernel with int32 `PMADDWD` accumulation,
  an exact scalar tail, and an `ssdSSE2MaxWidth` of 11000 that routes wider rows
  to scalar rather than widening per iteration
- [x] Implement an SSE2 delta-SSD kernel for discontiguous dirty spans, with the
  same int32 accumulator rather than the AVX2 kernel's per-iteration widening,
  and a wrapper that splits long spans so there is no width cliff
- [x] Measure whether the AVX2-calibrated staged-incremental crossover transfers
  to SSE2, on a CPU that genuinely lacks AVX2, before gating
  `stagedIncremental` on any vectorized delta kernel
- [x] Make `deltaSSDSpan` a real ladder, so an AVX2 host uses the SSE2 kernel
  for four-to-seven-pixel spans instead of dropping to scalar
- [x] Add an exact float64 SSE2 span compositor, so a no-AVX2 host stops
  compositing scalar in the largest single item of its profile. It returns less
  than that framing implied: about 1.07x on the kernel and 1.06x end to end,
  measured on a host that genuinely lacks AVX2
- [x] Add the exact float64 AVX2 span compositor, the amd64 counterpart of the
  NEON one, byte-identical to the scalar span and therefore on by default with
  no flag. It shares the SSE2 kernel's constant layout
- [x] Add an opt-in float32 SIMD span compositor with SSE2 and AVX2 kernels
  behind `--fast-compositing`, kept after measuring it against the exact vector
  compositor rather than against the scalar loop
- [ ] Hoist the per-span constant block to once per circle. It is the whole
  difference between the SSE2 kernel's 8-pixel crossover measured directly and
  the 24-pixel cutoff the dispatcher has to use, and it would lower the AVX2
  cutoff from 16 to around 4
- [ ] Add opt-in concurrent population evaluation over a pool of independent
  renderer sessions behind `--parallel-evaluation`
- [x] Do not port SAD: `FastSAD` has no non-test callers and its AVX2 kernel
  needs `VPMADDUBSW` (SSSE3) and `VPMULLD` (SSE4.1)
- [x] Do not port the Q16.16 span: it needs `VPCMPGTQ` (64-bit signed compare),
  it is 2.80% of the no-AVX2 profile, and the existing hardware-compare AVX2
  kernel is already 1.6-3.0× slower than the scalar finite-difference span
- [x] Do not add a float32 circle-span kernel: `circleSpanFloat32Selected` is
  reachable only through `CPURenderer.forceFloat32Geometry`, which no
  configuration path sets. An SSE2 kernel for it was written and then removed;
  its test table was retargeted at the AVX2 kernel

**10.17c — Validation and gates:**
- [x] Require bit-exact parity with the scalar oracle across batch boundaries,
  padded strides, non-zero start offsets, alpha-only differences, and a seeded
  random sweep
- [x] Compare every kernel the host can execute, directly and in one process,
  rather than skipping unless the host already selected that backend
- [x] Make the native CI gate assert the detected tier rather than one kernel's
  backend string, and run `./internal/fit/renderer` under it on AMD64
- [x] Publish measurements in `docs/task-10.17-sse2-report.md`, taken on the
  no-AVX2 target rather than on an AVX2 host under GODEBUG

**Measured results (no-AVX2 target, median of three):** SSE2 SSD is
6.03×/6.02×/6.22×/5.72× scalar at 64² through 512² and 5.33× at 1024², with zero
allocations. Delta-SSD gains 2.25× to 4.45× over 4-256 px spans. Against
`origin/master` on the same machine, `BenchmarkFit` cost improved 5.85× at 256²
and 6.12× at 512², and the sequential, batch, and joint pipelines improved
1.24×, 1.20×, and 1.13×. The staged-incremental crossover curve matches the AVX2
one in shape, with the SSE2 crossover at radius 96 against roughly 72.

An earlier revision also recorded a full 32-circle batch at seed 4242 on that
target falling from 300.81 s to 150.52 s at an identical final cost of 1032.75,
flat at 150.33 s with 64 threads instead of 8. That predates the delta-SSD
accumulator change and has not been repeated.

---

## Phase 11: GPU Backends (Research → Prototype)

**Goal:** Add a pluggable GPU renderer/coster behind the existing `Renderer` interface.

### Task 11.1: Research GPU Backend Options
- [x] Create comparison document: `docs/gpu-backends.md` (baseline constraints, candidate analysis, matrix).
- [x] Research OpenGL compute/fragment shader approach — viable for Windows/Linux; macOS limited to 4.1, use fragment path plus CPU reduction; Go bindings via `go-gl` + headless GLFW.
- [x] Research OpenCL approach — best portability across NVIDIA/AMD/Intel, strong Go binding (`github.com/jgillich/go-opencl/cl`), compute-first API suits compositing+SSD, note Apple Silicon gap.
- [x] Research WebGPU approach — modern but bindings immature and require bundling Dawn/WGPU; defer until primary backend stabilizes.
- [x] Research Vulkan compute approach — powerful yet extremely boilerplate-heavy; not ideal for first iteration.
- [x] Create comparison matrix: ease of binding, portability, debuggability, performance (see doc).
- [x] Make recommendation with justification: pursue OpenCL as primary backend, prototype OpenGL fragment fallback for macOS.
- [ ] Track outstanding risks: document macOS (Metal/WebGPU) gap, driver quirks encountered during prototype.

### Task 11.2: Choose GPU Backend and Set Up Infrastructure
- [x] Choose one backend based on research (OpenCL selected; fragment-shader OpenGL fallback deferred to Task 11.6).
- [x] Install required GPU scaffolding (cgo-based OpenCL runtime under `internal/fit/gpu`, no external bindings required).
- [x] Set up GPU context initialization code (context + queue bootstrap, device selection with GPU preference).
- [x] Add build tags for GPU support (`//go:build gpu` mirrored by stub fallback).
- [x] Add `--backend` flag to CLI (values: `cpu`, `opencl` for now) and surface backend choice in logs.
- [x] Document GPU requirements and setup in README (experimental build instructions referencing doc).
- [ ] Test GPU detection and initialization (pending hardware run + automated checks).

### Task 11.3: Design GPU Renderer Architecture ✅

Completed: pluggable OpenCL renderer architecture, compositing and reduction kernel designs, persistent reference storage, and documented device-memory layout and transfers.

### Task 11.4: Implement GPU Circle Compositing Shader/Kernel
- [x] Write shader/kernel for circle rendering
  - [x] Input: circle parameters (X, Y, R, CR, CG, CB, Opacity)
  - [x] Output: composited image on GPU
  - [x] Use Porter-Duff alpha compositing
- [x] Implement shader loading and compilation
- [x] Add error handling for shader compilation failures
- [x] Test with simple single-circle cases (unit test under `//go:build gpu`)
- [ ] Verify visual correctness against CPU renderer (expand tests with golden images)

### Task 11.5: Implement GPU Cost Computation ✅

Completed: quantized per-pixel SSD in the render kernel, portable multi-pass on-device reduction to a four-byte scalar readback, and CPU/OpenCL tolerance-based parity coverage.

### Task 11.6: Implement Memory Transfer Strategy ✅

Completed: persistent reference/parameter/output buffers, hash-aware parameter uploads, four-byte cost readback, lazy cached image materialization, documented PoCL transfer profiling, and packed `uchar4` image buffers that cut pixel storage/readback by 75%; pinned staging remains unjustified without vendor-GPU evidence.

### Task 11.7: Integrate GPU Renderer into Pipeline ✅

Completed: joint, sequential, and batch optimization use same-backend OpenCL sessions without silent CPU degradation; GPU-tagged PoCL tests cover every mode, and an uncached end-to-end CPU/PoCL benchmark documents current pipeline performance and staged-session overhead. Real vendor-GPU characterization remains in Task 11.9.

### Task 11.8: Add GPU Backend Selection to CLI
- [x] Update `run` command to accept `--backend cpu|<gpu>`
- [ ] Update `serve` command to accept `--backend` flag
- [ ] Add validation for GPU availability
- [ ] Provide helpful error messages if GPU unavailable
- [ ] Document backend selection in help text
- [ ] Test backend switching

### Task 11.9: Create GPU Performance Benchmarks
- [ ] Benchmark GPU rendering for various K values (1, 10, 50, 100)
- [ ] Benchmark GPU rendering for various W×H sizes (64x64, 256x256, 512x512, 1024x1024)
- [ ] Benchmark GPU cost computation separately
- [ ] Compare GPU vs CPU performance across scenarios
- [ ] Identify crossover points where GPU becomes beneficial
- [ ] Document performance characteristics

### Task 11.10: Test GPU Correctness and Edge Cases
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

**Deliverables:**
- GPU backend comparison document with recommendation
- Working GPU renderer implementing `Renderer` interface
- Circle compositing shader/kernel
- Cost computation with GPU reduction
- Performance benchmarks comparing GPU vs CPU
- Comprehensive documentation

**Acceptance Checks:**
- [ ] Drop-in selectable with `--backend cpu|<gpu-name>`
- [ ] Identical cost (within float tolerances) vs CPU
- [ ] Performance improvement on supported GPUs
- [ ] Graceful fallback if GPU unavailable
- [ ] All optimization modes work with GPU backend
- [ ] Documentation covers setup and usage

---

## Historical Summary Through Phase 11

This section records the roadmap state before Phases 12-14 were added. The final summary and current execution priority are at the end of this document.

Phases 0-11 use bite-sized, testable tasks. Each task follows TDD principles:
1. Write failing test
2. Run test to verify failure
3. Write minimal implementation
4. Run test to verify pass
5. Commit

**Remaining Phases (12-13)** will follow the same structure:
- **Phase 12**: UX polish and visualization
- **Phase 13**: Documentation and packaging

## Phase 12: UX & Visualization Polish

**Goal:** Make it pleasant to use and reason about results.

### Task 12.1: Implement View Mode Toggles ✅

Completed: accessible Reference, Best, Side-by-Side, and Difference radio
controls default to an equal two-pane comparison, persist the global preference
in browser localStorage, and support keyboard shortcuts 1-4. Single-image views
use the full responsive card width, reference metadata reports decoded dimensions
and original file size, and SSE refreshes recover from initially unavailable
Best or Difference images. The Difference view intentionally uses the existing
false-color endpoint; selectable colormaps and its quantitative legend remain in
Task 12.2.

### Task 12.2: Implement Difference Heatmap Visualization ✅

Completed: `internal/fit/colormap.go` maps normalized errors through Turbo or
Magma, while `diff.png` now visualizes per-pixel mean absolute RGB error on a
fixed 0-255 scale. The Difference view provides a live colormap selector and a
matching labeled legend; the endpoint accepts `?colormap=turbo|magma` and
rejects unsupported values. Unit, image-generation, endpoint, and rendered-UI
tests cover the behavior, and `docs/difference-heatmaps.md` documents the scale,
palette tradeoffs, API, and artifact default.

### Task 12.3: Add Advanced Metrics (PSNR, Optional SSIM) ✅

Completed: PSNR is derived from the optimizer's RGB MSE and reported by CLI,
status API, SSE, trace history, and the detail page, with perfect matches encoded
as `psnr: null` plus `psnrInfinite: true`. Optional SSIM uses an 11×11 Gaussian
window over RGB, remains off by default, and is sampled initially, at most once
per second following improvement, and finally. The UI provides live metric cards
and bounded selectable Cost/PSNR/SSIM history, while `trace.jsonl` retains the
full persistent history when tracing is enabled. Calculation, configuration,
lifecycle, serialization, UI, and cadence tests cover the feature; formulas and
interpretation are documented in `docs/advanced-quality-metrics.md`.

### Task 12.4: Add Parameter Inspection Tooltip ✅
- [x] Display current best parameters in UI
  - [x] Show all K circles with their properties
  - [x] Format: Circle N: (X, Y, R) RGB(r, g, b) α=opacity
- [x] Add interactive parameter viewer
  - [x] Expandable/collapsible list of circles
  - [ ] Highlight individual circles on hover (optional)
- [x] Add parameter export button
  - [x] Download params.json with current best
  - [x] Include metadata: jobID, cost, iterations, timestamp
- [ ] Add parameter visualization (optional)
  - [ ] Show circles sorted by size or opacity
  - [x] Color-code by properties
- [x] Test parameter display with various circle counts

Completed: the job detail page now exposes the materialized current-best circles
in a native expandable viewer using the requested coordinate, radius, 8-bit RGB,
and opacity format. Color/opacity swatches aid inspection, and an open viewer
refreshes from the live job snapshot without expanding SSE payloads. The new
`params.json` endpoint downloads both the exact flat optimizer vector and a
readable circle representation with job ID, cost, iterations, and UTC snapshot
timestamp. Endpoint, conversion, live-view markup, empty state, and circle-count
tests include one, many, and 1,000-circle cases.

### Task 12.5: Add Download Buttons for Artifacts ✅
- [x] Add "Download Best Image" button
  - [x] Download current best.png
  - [x] Filename: `job-<id>-best.png`
- [x] Add "Download Parameters" button
  - [x] Download params.json
  - [x] Filename: `job-<id>-params.json`
- [x] Add "Download Difference Image" button
  - [x] Download diff.png with colormap
  - [x] Filename: `job-<id>-diff.png`
- [x] Add "Download Report" button
  - [x] Generate HTML report with all artifacts
  - [x] Include: reference, best, diff images, parameters, metrics, metadata
  - [x] Self-contained HTML file (embedded images as base64)
  - [x] Filename: `job-<id>-report.html`
- [x] Cover browser-compatible downloads with response and UI tests
- [x] Add loading states during report generation

Completed: the detail page provides responsive controls for Best, Parameters,
Difference, and Report downloads with deterministic job-specific filenames.
PNG endpoints preserve inline image use while `?download=1` adds standards-based
attachment headers, and the selected heatmap colormap is shared with Difference
and Report exports. Report generation renders one immutable job snapshot into a
self-contained HTML download with three base64 PNGs, metrics, metadata, and the
full parameter table. An accessible asynchronous loading/error state keeps the
page responsive. Endpoint tests validate MIME and attachment headers, embedded
PNG integrity, report self-containment, error behavior, and browser-facing UI
contracts.

### Task 12.6: Generate HTML Report ✅
- [x] Create report template in `internal/ui/report.templ`
  - [x] Header with job metadata (ID, mode, circles, date)
  - [x] Three-column layout: Reference, Best, Difference
  - [x] Metrics table: Cost, PSNR, SSIM, iterations, time
  - [x] Parameters table: All circles with properties
  - [x] Footer with generation timestamp
- [x] Implement report generation endpoint
  - [x] `GET /api/v1/jobs/:id/report.html`
  - [x] Embed images as base64 data URIs
  - [x] Inline CSS for styling
  - [x] No external dependencies
- [x] Add print-friendly CSS styles
  - [x] Page breaks between sections
  - [x] High-contrast colors
- [x] Test report rendering and downloading
- [x] Document report format and customization

Completed: report downloads capture an immutable job snapshot in a standalone
HTML document with header metadata, a metrics table, three embedded PNGs, every
circle parameter, and a timestamped footer. Inline responsive and print styles
provide a three-column screen layout, high-contrast printed output, section page
breaks, and an unbounded parameter table without external dependencies. Template
and endpoint tests cover content, attachment headers, embedded PNG integrity,
print contracts, errors, and active-job timestamps; `docs/html-reports.md`
documents the endpoint, snapshot format, offline behavior, printing, and safe
customization workflow.

### Task 12.7: Improve Metrics Panel Visualization
- [ ] Enhance sparkline chart for cost history
  - [ ] Show X-axis (iterations) and Y-axis (cost) labels
  - [ ] Add hover tooltips with exact values
  - [ ] Show cost improvement rate (delta per iteration)
- [ ] Add circles/sec (throughput) sparkline
  - [ ] Track throughput over time
  - [ ] Display average and current cps
- [ ] Add progress bar for iteration count
  - [ ] Visual indicator: completed / total iterations
  - [ ] Percentage display
- [ ] Add estimated time remaining (ETA)
  - [ ] Calculate based on iteration rate
  - [ ] Display in human-readable format (e.g., "2m 30s remaining")
- [ ] Style metrics panel for clarity
  - [ ] Use color coding for status (running=blue, completed=green, failed=red)
  - [ ] Clear typography and spacing
- [ ] Test with various optimization scenarios

### Task 12.8: Add Job Control Actions
- [ ] Add "Pause" button (if feasible)
  - [ ] Endpoint: `POST /api/v1/jobs/:id/pause`
  - [ ] Checkpoint and suspend worker
  - [ ] Update UI to show paused state
- [ ] Add "Resume" button (for paused jobs)
  - [ ] Endpoint: `POST /api/v1/jobs/:id/resume`
  - [ ] Resume from checkpoint
  - [ ] Update UI to show running state
- [ ] Add "Cancel" button
  - [ ] Endpoint: `POST /api/v1/jobs/:id/cancel`
  - [ ] Gracefully stop worker
  - [ ] Update UI to show cancelled state
- [ ] Add "Delete" button
  - [ ] Endpoint: `DELETE /api/v1/jobs/:id`
  - [ ] Remove job and artifacts
  - [ ] Redirect to job list
- [ ] Add confirmation dialogs for destructive actions
- [ ] Test all control actions end-to-end

### Task 12.9: Improve Responsive Design and Accessibility
- [ ] Test UI on mobile devices (phone, tablet)
  - [ ] Ensure images scale appropriately
  - [ ] Stack side-by-side views vertically on small screens
- [ ] Add responsive breakpoints for layout
  - [ ] Desktop: side-by-side layout
  - [ ] Tablet: stacked layout with full-width images
  - [ ] Mobile: single-column layout
- [ ] Improve accessibility (WCAG 2.1 AA compliance)
  - [ ] Add alt text to all images
  - [ ] Ensure sufficient color contrast
  - [ ] Add ARIA labels to interactive elements
  - [ ] Support keyboard navigation
  - [ ] Test with screen reader
- [ ] Add loading states and skeleton screens
  - [ ] Show placeholders while images load
  - [ ] Indicate when SSE is connecting
- [ ] Test with various browser sizes and devices

### Task 12.10: Add User Preferences and Settings
- [ ] Create settings page or modal
  - [ ] Auto-refresh interval for images (default: SSE-driven)
  - [ ] Default view mode (Reference, Best, Side-by-Side, Diff)
  - [ ] Default colormap for difference visualization
  - [ ] Metrics to display (cost, PSNR, SSIM, cps)
- [ ] Persist preferences in browser localStorage
- [ ] Apply preferences across all jobs
- [ ] Add "Reset to Defaults" button
- [ ] Test preference persistence and application

**Deliverables:**
- View mode toggles (Reference, Best, Side-by-Side, Difference)
- False-color difference heatmap with colormap
- Advanced metrics (PSNR, optional SSIM)
- Parameter inspection and download
- Artifact download buttons (images, params, report)
- HTML report generation
- Enhanced metrics panel with sparklines and ETA
- Job control actions (pause, resume, cancel, delete)
- Responsive design and accessibility improvements
- User preferences and settings

**Acceptance Checks:**
- [ ] All view modes work correctly with live updates
- [ ] Difference heatmap clearly shows error regions
- [ ] PSNR and SSIM calculated and displayed correctly
- [ ] Parameters can be inspected and downloaded
- [ ] All download buttons work on various browsers
- [ ] HTML report is self-contained and print-friendly
- [ ] Metrics panel provides useful real-time information
- [ ] Job control actions work reliably
- [ ] UI works well on mobile and desktop
- [ ] Accessibility requirements met

---

## Phase 13: Robustness, Docs, Packaging

**Goal:** Make this shippable to users.

### Task 13.1: Comprehensive Error Handling
- [ ] Audit all error paths in codebase
  - [ ] Identify missing error checks
  - [ ] Ensure all errors are properly wrapped with context
  - [ ] Use consistent error wrapping (e.g., `fmt.Errorf("context: %w", err)`)
- [ ] Improve server error responses
  - [ ] Consistent JSON error format: `{"error": "message", "code": "ERROR_CODE"}`
  - [ ] Appropriate HTTP status codes (400, 404, 500, etc.)
  - [ ] Detailed error messages for debugging (in dev mode)
  - [ ] Generic error messages for production
- [ ] Add error handling to CLI commands
  - [ ] Clear error messages for common failures
  - [ ] Exit codes: 0=success, 1=error, 2=usage error
  - [ ] Suggest fixes when possible (e.g., "image not found: check path")
- [ ] Test error scenarios systematically
  - [ ] Invalid inputs, missing files, network errors
  - [ ] Out of memory, disk full, permission denied
  - [ ] GPU unavailable, optimizer failures
- [ ] Document common errors and solutions

### Task 13.2: Input Validation and Sanitization
- [ ] Validate all API inputs
  - [ ] refPath: check file exists and is valid image
  - [ ] width, height: positive integers within limits
  - [ ] circles: positive integer, reasonable limit (e.g., < 1000)
  - [ ] iters, popSize: positive integers
  - [ ] mode: must be "joint", "sequential", or "batch"
  - [ ] seed: any integer (or random if not provided)
- [ ] Validate CLI inputs
  - [ ] Same validations as API
  - [ ] Helpful error messages on validation failure
- [ ] Add rate limiting for API endpoints (optional)
  - [ ] Prevent abuse of job creation
  - [ ] Limit concurrent jobs per client
- [ ] Sanitize file paths to prevent directory traversal
- [ ] Write tests for all validation logic

### Task 13.3: Logging and Observability Improvements
- [ ] Audit logging across codebase
  - [ ] Ensure consistent use of slog
  - [ ] Add structured logging fields (jobID, duration, etc.)
  - [ ] Use appropriate log levels (debug, info, warn, error)
- [ ] Add request logging middleware
  - [ ] Log all API requests with method, path, status, duration
  - [ ] Include request ID for tracing
- [ ] Add performance logging
  - [ ] Log optimization progress (every N iterations)
  - [ ] Log slow operations (rendering, cost computation)
- [ ] Add optional metrics export (Prometheus format)
  - [ ] Endpoint: `GET /metrics`
  - [ ] Metrics: job counts, durations, throughput
  - [ ] Optional feature, disabled by default
- [ ] Document logging configuration and best practices

### Task 13.4: Create README.md
- [ ] Write comprehensive README
  - [ ] Project overview and features
  - [ ] Quick start guide
  - [ ] Installation instructions
  - [ ] Usage examples with screenshots
  - [ ] CLI command reference
  - [ ] API endpoint reference
  - [ ] Building from source
  - [ ] Troubleshooting section
  - [ ] License and contribution guidelines
- [ ] Add badges (build status, Go version, license)
- [ ] Include example images (before/after)
- [ ] Link to detailed documentation

### Task 13.5: Create Getting Started Guide
- [ ] Write `docs/getting-started.md`
  - [ ] Installation steps for common platforms
  - [ ] First run: CLI mode (`mayflycirclefit run`)
  - [ ] Starting the server (`mayflycirclefit serve`)
  - [ ] Creating your first job via UI
  - [ ] Creating your first job via API (curl examples)
  - [ ] Viewing results and downloading artifacts
  - [ ] Common CLI flags and configuration
- [ ] Add example reference images in `assets/examples/`
  - [ ] Simple geometric shapes (circle, square, triangle)
  - [ ] Low-resolution photos (64x64, 128x128)
  - [ ] Expected results for each example
- [ ] Include walkthrough video or animated GIF (optional)

### Task 13.6: Create Architecture Documentation
- [ ] Write `docs/architecture.md`
  - [ ] System overview diagram
  - [ ] Component breakdown (fit, opt, server, ui, store)
  - [ ] Renderer interface and implementations (CPU, GPU)
  - [ ] Optimizer interface and implementations (Mayfly, DE)
  - [ ] Pipeline strategies (joint, sequential, batch)
  - [ ] Job lifecycle and state machine
  - [ ] SSE streaming architecture
  - [ ] Checkpoint and resume mechanism
  - [ ] Performance optimization layers (CPU, SIMD, GPU)
- [ ] Add sequence diagrams for key flows
  - [ ] Job creation and execution
  - [ ] Checkpoint and resume
  - [ ] SSE live updates
- [ ] Document design decisions and tradeoffs

### Task 13.7: Create Performance Benchmarks Documentation
- [ ] Write `docs/benchmarks.md`
  - [ ] Hardware test configurations
  - [ ] Benchmark methodology
  - [ ] CPU renderer performance (various K, W, H)
  - [ ] CPU + SIMD performance comparison
  - [ ] GPU renderer performance comparison
  - [ ] Memory usage and allocation metrics
  - [ ] Throughput (circles/sec, images/sec)
  - [ ] Known limitations and bottlenecks
- [ ] Include performance comparison tables
- [ ] Include flamegraph samples for key scenarios
- [ ] Document when to use CPU vs GPU
- [ ] Document scaling characteristics

### Task 13.8: Document Known Limitations and Future Work
- [ ] Create `docs/limitations.md`
  - [ ] Current limitations (e.g., SIMD requires cgo)
  - [ ] Platform-specific issues
  - [ ] GPU driver requirements
  - [ ] Memory constraints for large images
  - [ ] Optimizer convergence characteristics
- [ ] Create `docs/roadmap.md`
  - [ ] Future enhancements (cost maps, adaptive pipelines)
  - [ ] Potential optimizations (WebGPU, better SIMD)
  - [ ] Feature requests and community feedback
- [ ] Link to issue tracker for bugs and features

### Task 13.9: Create Sample Reference Images and Examples
- [ ] Curate example images in `assets/examples/`
  - [ ] `simple-circle.png` - Single red circle
  - [ ] `gradient.png` - Smooth gradient
  - [ ] `geometric.png` - Multiple shapes
  - [ ] `photo-small.png` - Low-res photograph
- [ ] Document expected results for each example
  - [ ] Recommended circle counts
  - [ ] Expected cost values
  - [ ] Convergence time estimates
- [ ] Create shell script: `examples/run-examples.sh`
  - [ ] Runs all examples with sensible defaults
  - [ ] Outputs results to `examples/output/`
- [ ] Add examples to CI to ensure they don't break

### Task 13.10: Versioning and Changelog
- [ ] Choose versioning scheme (Semantic Versioning 2.0)
- [ ] Create `CHANGELOG.md`
  - [ ] Document changes by version
  - [ ] Format: [Added], [Changed], [Fixed], [Removed]
  - [ ] Include links to issues/PRs
- [ ] Add version flag to CLI: `--version`
  - [ ] Display version, commit hash, build date
- [ ] Tag releases in git (e.g., `v1.0.0`)
- [ ] Document release process
  - [ ] Steps for cutting a release
  - [ ] Build and test procedure
  - [ ] Release notes template

### Task 13.11: License and Contributing Guidelines
- [ ] Add `LICENSE` file (e.g., MIT, Apache 2.0)
- [ ] Add `CONTRIBUTING.md`
  - [ ] How to file issues
  - [ ] How to submit pull requests
  - [ ] Code style guidelines
  - [ ] Testing requirements
  - [ ] Review process
- [ ] Add copyright headers to source files (if required by license)
- [ ] Add code of conduct (optional but recommended)

### Task 13.12: Build and Release Artifacts
- [ ] Create build scripts for cross-compilation
  - [ ] `make release` or `just release`
  - [ ] Build for: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
  - [ ] Output binaries to `dist/` directory
- [ ] Create release archives
  - [ ] `.tar.gz` for Unix systems
  - [ ] `.zip` for Windows
  - [ ] Include binary, README, LICENSE, example images
- [ ] Set up GitHub releases (or equivalent)
  - [ ] Automated release creation from tags
  - [ ] Upload build artifacts
  - [ ] Include changelog in release notes
- [ ] Document installation from release binaries

### Task 13.13: CI/CD Pipeline
- [ ] Set up continuous integration (GitHub Actions, GitLab CI, etc.)
  - [ ] Run tests on every commit
  - [ ] Run linters (golangci-lint)
  - [ ] Test on multiple platforms (Linux, macOS, Windows)
  - [ ] Test with and without cgo
- [ ] Add code coverage reporting
  - [ ] Upload to codecov.io or similar
  - [ ] Add coverage badge to README
- [ ] Add automated release builds
  - [ ] Build release artifacts on tag push
  - [ ] Create GitHub release automatically
- [ ] Add benchmark tracking (optional)
  - [ ] Run benchmarks on every commit
  - [ ] Track performance regressions

### Task 13.14: End-to-End Acceptance Testing
- [ ] Verify complete user journeys
  - [ ] New user: install → run first example → see results
  - [ ] Server mode: start server → create job via UI → view progress → download results
  - [ ] API mode: create job via curl → poll status → fetch images
  - [ ] Resume: start job → kill server → restart → resume
- [ ] Test on fresh installations
  - [ ] Clean VM or container
  - [ ] Follow README instructions exactly
  - [ ] Document any missing steps
- [ ] Test with real-world images
  - [ ] Various sizes and formats
  - [ ] Edge cases (very small, very large, monochrome)
- [ ] Verify all documentation is accurate and up-to-date

**Deliverables:**
- Comprehensive error handling and validation
- Complete documentation suite (README, getting-started, architecture, benchmarks)
- Sample images and examples
- Versioning and changelog
- License and contributing guidelines
- Release build artifacts
- CI/CD pipeline
- End-to-end acceptance testing

**Acceptance Checks:**
- [ ] All error paths handled gracefully with clear messages
- [ ] Documentation is complete, accurate, and helpful
- [ ] New user can install and run examples without issues
- [ ] Release artifacts build successfully on all platforms
- [ ] CI pipeline passes all checks
- [ ] License and contributing guidelines in place
- [ ] Project is ready for public release

---

## Phase 14: Production-Readiness Remediation 🚨 BLOCKING

**Goal:** Correct the defects identified by the 2026-08-09 codebase audit before adding more product features or declaring the project shippable.

**Priority policy:**

- **P0:** Security, data races, incorrect output, broken builds, or nonfunctional lifecycle guarantees. These block release and should be completed first.
- **P1:** Reliability, persistence, portability, test coverage, and API consistency required for sustained use.
- **P2:** Performance, maintainability, documentation, and UX improvements that should follow the corrected architecture.
- Existing unchecked Phase 12/13 feature work should pause when it depends on behavior being redesigned here.

### Audit Baseline ✅

Completed local baseline verification: vet, short/race tests, deterministic generated sources, portable cross-build dispatch, benchmark-only performance assertions, lifecycle/progress/checkpoint behavior, and renderer edge-case coverage. Final release-candidate verification remains tracked below.

### Task 14.1: Restore Reproducible Builds and Tooling (P0) ✅

Completed: deterministic pinned templ generation, clean-clone build/test/lint, effective stale-generation/format checks, source formatting, artifact cleanup, and Go 1.24 alignment; all acceptance checks passed.

### Task 14.2: Establish a Trusted Server Security Model (P0)

- [x] Decide and document whether server mode is:
  - [x] Strictly trusted-local-only, with defensive browser-origin protections, or
  - [ ] Network-capable, with authentication and authorization.
- [x] Remove wildcard CORS and default to same-origin for the trusted-local model.
- [x] Add CSRF/origin validation for browser-triggered state-changing requests.
- [x] Restrict `refPath` and `canvasPath` to configured, canonicalized input roots.
- [x] Reject symlink/path escapes after resolving the final path.
- [x] Disable pprof endpoints by default; expose them only behind an explicit profiling flag and trusted bind address.
- [x] Add HTTP server hardening:
  - [x] `ReadHeaderTimeout`
  - [x] `ReadTimeout` or per-handler body deadlines
  - [x] Keep server `WriteTimeout` disabled for long-lived SSE while bounding headers, reads, bodies, admission, and non-streaming work elsewhere.
  - [x] `IdleTimeout`
  - [x] `MaxHeaderBytes`
- [x] Limit JSON/form body size before parsing.
- [x] Validate decoded image dimensions and maximum pixel count before large allocations/work.
- [x] Add job admission controls: bounded queue, maximum concurrent jobs, and configurable resource limits.
- [x] Add a safe production error format that does not reveal filesystem paths or internal details.

**Acceptance Checks:**

- [x] An untrusted browser origin cannot create jobs, read local images, or access profiling data.
- [x] Files outside configured input roots cannot be read, including through symlinks or traversal sequences.
- [x] Oversized bodies, images, and job requests are rejected before expensive work begins.
- [x] Security tests cover CORS/origin behavior, path containment, pprof defaults, and resource limits.

### Task 14.3: Centralize Configuration and Validation (P0)

- [x] Move application configuration types into a dependency-free domain/application package and keep compatibility aliases at persistence boundaries.
- [x] Define typed `Mode`, `Backend`, job state, and optimizer variant values instead of unchecked strings.
- [x] Implement one normalization/default/validation path shared by CLI, API, UI, and checkpoint loading.
- [x] Enforce explicit limits for:
  - [x] Circles
  - [x] Iterations
  - [x] Population size
  - [x] Batch size
  - [x] Convergence patience and threshold
  - [x] Checkpoint/trace intervals
  - [x] Image dimensions and decoded pixel count
- [x] Replace ambiguous optional booleans with explicit enable/disable flags.
- [x] Correct seed semantics:
  - [x] Implement `seed=0` as random and report the generated effective seed, or
  - [ ] Document that zero is deterministic.
- [x] Validate canvas dimensions rather than silently cropping or padding.
- [x] Add checkpoint schema versioning and migration/rejection rules.

**Acceptance Checks:**

- [x] The same input receives the same defaults and validation result through CLI, API, UI, and resume.
- [x] Invalid values return typed errors rather than panics, silent coercion, or delayed worker failure.
- [x] Table-driven tests cover boundaries and omitted-versus-explicit configuration values.

### Task 14.4: Fix Job-State and SSE Concurrency (P0) ✅

Completed: immutable job snapshots, typed transitions, synchronized broadcaster lifecycle/cleanup, and concurrent handler/worker/SSE coverage; race and stress acceptance checks passed.

### Task 14.5: Redesign Optimizer Execution for Progress, Errors, and Cancellation (P0) ✅

Completed: context-aware optimizer results, bounded cancellation, unified progress snapshots, error propagation, supervised/joined workers, shared lifecycle handling, and shutdown reporting; all acceptance checks passed.

### Task 14.6: Correct Renderer and Pipeline Semantics (P0)

- [x] Preserve the selected renderer backend and starting canvas through joint, sequential, and batch modes.
- [x] Introduce a renderer/session factory capable of creating larger-stage renderers without silently falling back to CPU or white background.
- [x] Render final CLI and server output using the same base canvas/backend semantics used for cost evaluation.
- [x] Change batch optimization to accept `totalCircles` and use `min(batchSize, remaining)` for the final batch.
- [x] Populate `OptimizationResult.Iterations` and evaluation statistics for every mode.
- [x] Preserve the best historical parameter vector; reject or roll back a sequential/batch stage that worsens cost.
- [x] Validate parameter-vector length before rendering instead of panicking.
- [x] Define image-origin and stride requirements:
  - [x] Support independent origins/strides correctly, or
  - [ ] Normalize inputs once and assert the normalized representation.
- [x] Handle empty images and zero-circle cases without division by zero or incorrect zero costs.

**Acceptance Checks:**

- [x] Custom-canvas cost and renderer output describe the same rendered image; CLI/server artifact paths consume that renderer result.
- [x] Sequential and batch modes honor CPU/OpenCL selection or return an explicit unsupported-mode error.
- [x] Requests for 1, 4, 6, and 7 batch-mode circles return exactly that many circles.
- [x] Tests cover custom canvases, mismatched dimensions, non-zero origins, padded strides, short parameter vectors, and zero-size inputs.

### Task 14.7: Repair Snapshots, Checkpoints, Traces, and Resume (P0/P1)

- [x] Preserve `CostAfter` and `Timestamp` for every circle instead of rebuilding previous entries with zero metadata.
- [x] Save circle metadata incrementally and carry previous metadata forward explicitly.
- [x] Make checkpoint creation consume an immutable live optimizer snapshot.
- [x] Ensure fresh jobs can create meaningful periodic checkpoints before completion.
- [x] Save a final checkpoint on successful completion when checkpointing is enabled/documented.
- [x] Make early-converged sequential and partial-batch checkpoints valid by recording actual and requested circle counts separately.
- [x] Define resume semantics honestly:
  - [ ] Persist and restore optimizer population/internal state, or
  - [x] Seed a new population around the previous best with a new deterministic continuation seed, and
  - [x] Describe the operation as `restart-from-best` and document its limitations.
- [x] Do not share a mutable `CPURenderer` between active optimization and checkpoint artifact rendering.
- [x] Make trace finalization ordered and flushed/closed on completion, cancellation, failure, and shutdown.

**Acceptance Checks:**

- [x] Every `circles.json` entry contains its own non-zero timestamp and correct post-circle cost.
- [x] A long job produces observable, valid intermediate checkpoints and trace entries.
- [x] Resume tests explicitly validate deterministic restart-from-best semantics and retention of a better saved candidate.
- [x] Checkpoint artifact generation cannot race with objective evaluation.

### Task 14.8: Make Persistence Safe and Cohesive (P1) ✅

Completed: store-owned artifacts, canonical UUID/path containment, concurrent durable atomic writes, checkpoint validation, restrictive permissions, retention/deletion behavior, and fault-path coverage; documented fault-filesystem exclusions remain.

### Task 14.9: Restore Portability and Integrate Performance Work (P1/P2)

- [x] Split SIMD wrappers by architecture/build tag so unsupported symbols are never referenced.
- [x] Provide a real portable scalar fallback for non-AMD64 targets.
- [x] Accurately label ARM64 as scalar fallback; a native NEON kernel remains future work.
- [x] Give AMD64 hosts without AVX2 a real SIMD path instead of scalar execution, and add an environment opt-out that can still force the complete scalar fallback (see Task 10.17).
- [x] Configure CI to cross-build at minimum (execution still requires a passing workflow run):
  - [x] linux/amd64
  - [x] linux/arm64
  - [x] darwin/amd64
  - [x] darwin/arm64
  - [x] windows/amd64
- [x] Enable `FastMSECost` in production after parity coverage against the reference cost.
- [x] Replace absolute wall-clock unit-test thresholds with benchmarks or relative regression checks on controlled runners.
- [x] Reuse accumulated canvases in sequential/batch mode to avoid repeatedly rendering all prior circles.
- [x] Reuse per-stage parameter buffers and capacity-backed result slices to reduce stage-evaluation allocations.
- [x] Perform OpenCL error reduction on-device and avoid reading the full output image for every cost evaluation.
- [x] Benchmark full sequential and batch optimization pipelines with allocation reporting, not only isolated kernels.

**Acceptance Checks:**

- [ ] All supported target builds compile in CI.
- [x] Scalar and SIMD/GPU cost implementations match the reference across boundary widths, strides, and alpha values.
- [x] Performance measurements are benchmarks, not hardware-dependent pass/fail unit thresholds.
- [x] Benchmark reports include allocation counts and end-to-end improvement, with final-replay/callback-isolation regression coverage.

### Task 14.10: Harden CLI and API Contracts (P1) ✅

Completed: typed DTOs, bounded cancellable clients, escaped IDs, validated logs, strict standardized routing/errors, actual work metrics, cancellation/deletion endpoints, and contract tests; all acceptance checks passed.

### Task 14.11: Build a Release-Gating Test and CI Matrix (P1)

- [x] Add CI jobs for:
  - [x] Clean-checkout generation and build
  - [x] `go vet ./...`
  - [x] Pinned `staticcheck`
  - [x] `go test -short ./...`
  - [x] `go test -race -short ./...`
  - [x] Cross-compilation matrix
  - [x] GPU-tag compile check where OpenCL headers are available
  - [x] Coverage reporting with a justified 50% aggregate floor and uploaded profile
  - [x] `govulncheck` with a pinned tool version
  - [x] Generated-file and formatting consistency
- [x] Add targeted regression tests for the audited P0 defects in this phase.
- [x] Separate fast/long tests with `-short`, performance with `-bench`, and GPU compilation/runtime tests with the `gpu` build tag and explicit CI commands.
- [x] Audit enqueueing tests and ensure background workers are joined through `t.Cleanup`/explicit shutdown.
- [x] Add multi-job lifecycle stress tests and same-job store concurrency tests.
- [x] Add an opt-in clean end-to-end test and dedicated CI job: build → serve → create → observe SSE progress → checkpoint → cancel/restart → resume → fetch artifacts (`just test-e2e`).
- [x] Gate the repository's automated SemVer-tag release job on every required CI job and publish verified portable archives draft-first.

**Acceptance Checks:**

- [ ] All required CI checks pass from a clean clone on two consecutive runs.
- [x] Every addressed audit finding has regression coverage or a documented hardware/fault-environment limitation.
- [ ] No release can be produced while race, vulnerability, generation, cross-build, or core end-to-end checks fail.

### Task 14.12: Correct Documentation and Release Claims (P1/P2)

- [x] Rewrite README quick start for a clean clone (final verbatim acceptance run remains below).
- [x] Document all current CLI commands and remove “coming in later phases.”
- [x] Remove nonexistent packages such as `internal/pkg` from architecture documentation.
- [x] Document the server trust model, bind behavior, input roots, authentication/origin policy, and pprof controls.
- [x] Document exact checkpoint/restart-from-best semantics and limitations.
- [x] Correct claims about canvas continuation, live progress, periodic/final checkpoints, random seeds, SIMD, NEON, and GPU support.
- [x] Distinguish implemented, experimental, configured CI gates, and production-ready features.
- [ ] Update historical phase completion notes when their acceptance checks have genuinely been revalidated.
- [x] Add `LICENSE`, `CONTRIBUTING.md`, changelog, support matrix, and known-limitations documentation before public release.

**Acceptance Checks:**

- [ ] A new user can follow the README on a clean machine and complete a small CLI and server job.
- [x] Every documented feature has corresponding coverage or is clearly marked experimental/limited.
- [x] No document describes checkpoint/restart-from-best or server mode as production-ready while Phase 14 remains open.

### Phase 14 Execution Order

Implement in dependency-aware waves:

1. **Build foundation:** 14.1, then establish the CI skeleton from 14.11.
2. **Safety boundary:** 14.2, 14.3, and 14.4.
3. **Execution model:** 14.5, followed by 14.6.
4. **Durability:** 14.7 and 14.8.
5. **Compatibility and efficiency:** 14.9 and 14.10.
6. **Release gate:** finish 14.11 and 14.12, then rerun every Phase 14 acceptance check.

### Phase 14 Definition of Done

- [x] All locally actionable P0 implementation tasks and acceptance checks are complete.
- [x] `just build`, `just lint`, and `just test` pass from a clean local clone; the final integrated race suite also passes.
- [x] Supported cross-builds and the GPU-tag compile check pass locally; the remote CI run remains a release gate.
- [x] No known data races, path escapes, cross-origin image disclosures, or unbounded job admission paths remain.
- [x] Canvas, backend, batch count, snapshot metadata, progress, checkpoint, cancellation, and restart-from-best semantics are correct and tested.
- [x] Documentation matches observable behavior.
- [x] A fresh end-to-end release-candidate run passes without manual workspace preparation.

---

## Phase 15: Polishing Throughput

Active-set polishing is the only optimizer path in the codebase that is still
single-threaded, and it is now the dominant cost of a long incremental run.

**Measured 2026-08-16** on the Xeon Gold 5520+ (64 vCPU, `GOMAXPROCS=48`),
polishing a 32-circle fit of `Christian_after.jpeg` at 512x512 with
`residual-region`, `activeSetSize 8`, `iters 800`, `epochs 2`, `popSize 200`:

| | Extend stage (+8 circles) | Polish sweep |
| --- | ---: | ---: |
| Wall clock | 135 s | ~480 s |
| Process CPU | ~2000% | **223%** |
| Concurrency | 48 evaluations | 1 |

The machine is 95% idle for the whole polish. On the earlier 32→512 chain the
same imbalance cost 5 h 30 m of a 6 h 55 m run to remove 83 cost units, while
66 minutes of extends removed 710.

Two separate causes, and they compound:

1. `PolishCircleBatchContext` refuses any optimizer configured for concurrent
   evaluation (`internal/fit/renderer/batch_polish.go:125`), so
   `polishBatchResult` (`internal/server/worker.go:490`) builds a serial Mayfly.
   The refusal is correct as written — the sweep evaluator merges every candidate
   into one shared parameter vector and evaluates it on one shared session, which
   is what makes a sweep transactional — but the fix is a session pool, not a
   permanent serial path. The refusal comment already says so.
2. The baked prefix is `min(activeCircles)` circles
   (`batch_polish.go:218`, `pipeline.go:757`), and `residual-region` always drags
   in a low-index circle because `replacementCount = max(1, activeSetSize/5)`
   selects the globally weakest one. Observed active sets on the run above were
   `[2,3,6,7,8,10,14,16]` and `[3,4,6,7,8,26,29,31]`, so the prefix was 2 and 3 of
   32 circles and each candidate rasterized ~30. At 512 circles the prefix would
   still be ~2 and each candidate would rasterize ~510, so per-candidate cost
   grows linearly with the vector for every strategy except `contiguous-window`.

### Task 15.1: Give Polishing a Session Pool (P1)

- [x] Lease a per-evaluation slot in the sweep evaluator instead of sharing one
      `candidateFull` vector and one session. `evaluationSlot{session, combined}`
      and `evaluationPool` in `internal/fit/renderer/evaluation_pool.go` already
      have exactly this shape for the staged pipelines; reuse them rather than
      adding a second pooling mechanism.
- [x] Give each slot its own baked-suffix session so the per-sweep bake is not
      serialized behind the pool (`bakedSuffixSession`, `pipeline.go:757`).
- [x] Replace the blanket refusal at `batch_polish.go:125` with a check that the
      pool width matches the optimizer's evaluation width, keeping the refusal for
      the pool-less case so the failure stays loud rather than silent.
- [x] Keep the sweep transactional: acceptance still evaluates the merged
      candidate on the full session and still gates on
      `allCirclesUseful(audit, minBatchMSEContribution)`.
- [x] Plumb `evaluationWorkers` through `polishBatchResult` so polishing honors
      the same width as the main optimizer, and log the width alongside
      `accepted_sweeps` (`worker.go:605`).
- [x] Document the reproducibility consequence next to `ParallelEvaluation`
      (`internal/app/config.go:168`): parallel polishing applies one global best
      per generation, so a seed reproduces with the setting held fixed but not
      across the two settings — the same caveat the extend path already carries.

**Acceptance Checks:**

- [x] A parity test asserts that pooled polishing with width 1 produces
      byte-identical parameters, cost, and accepted-sweep count to the current
      serial path for a fixed seed.
- [x] A race test (`go test -race`) covers a multi-sweep polish at width > 1.
- [x] A benchmark reports sweep wall clock and allocations at widths 1, 8, and 48
      on the same fixture, with the machine and circle count stated.

### Task 15.2: Make Active-Set Selection Cheap (P2)

- [ ] Parallelize the leave-one-out renders in `AuditCircleBatch`
      (`internal/fit/renderer/batch_audit.go:37`) and the region-influence loop in
      `selectResidualRegionActiveSet` (`batch_polish.go:614`). Both are N
      independent full renders per sweep and both are serial today: ~65 renders
      per sweep at 32 circles, ~1025 at 512.
- [ ] Render only `selection.Region` in the influence loop.
      `imageDifferenceEnergy` reads nothing outside it, but `base.Render(without)`
      paints the whole canvas — on the 4x4 grid (`residualPolishGridSize = 4`)
      that is 16x more pixels than are examined.
- [ ] Skip the selection audit after a rejected sweep. A rejected sweep leaves
      `bestParams` untouched, so the next sweep recomputes an identical audit.

**Acceptance Checks:**

- [ ] Selection returns the same active set, replacement set, and region as the
      serial implementation for a fixed seed and fixture.
- [ ] A benchmark shows selection cost per sweep against circle count at 32, 128,
      and 512 circles, before and after.

### Task 15.3: Stop Destroying the Baked Prefix (P2)

- [ ] Measure how much of the per-candidate cost the prefix actually recovers,
      by recording `min(activeCircles)` and per-candidate rasterization count per
      sweep.
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

### Task 15.4: Right-Size the Polishing Defaults (P2)

- [ ] Re-derive `PolishingIters`, `PolishingEpochs`, and the population used for
      polishing against the active set's actual dimensionality. The defaults reuse
      the job's `popSize` (`worker.go:490`), so a run tuned for a 512-circle
      vector polishes a 56-dimensional active set with 200 members.
- [ ] Give polishing its own population knob rather than inheriting `popSize`,
      and expose it on `polishJobRequest` (`internal/server/server.go:996`).
- [ ] Record the measured sweep-by-sweep drop-off in the report. Observed on the
      run above: sweep 1 removed 85.5 cost units, sweep 2 removed 16.8 — a 5x
      falloff in one step, against a default `PolishingMaxSweeps` of 3 and a
      ceiling of 32.

**Acceptance Checks:**

- [ ] Defaults are justified by a measurement in a committed report, not by
      inheritance from the batch configuration.

---

## Phase 16: Declarative Run Schedules

### Why

Growing a fit incrementally is the project's most valuable workflow and the one
it supports least. There is no way to express "start at 8 circles, append 8 at a
time to 512, polish at these milestones, and stop polishing once it stops
paying." `extend` and `polish` are HTTP-only (`internal/server/server.go:1149`
and `:996`), each call mints a *new* job id, and `extendedFrom`/`polishedFrom`
come back in the response but are **not persisted anywhere**. A multi-hour
incremental run is therefore a chain of unrelated job records that only an
external script knows how to read.

Both incremental runs to date were driven by throwaway Python on the compute
box. That is where the real requirements come from, because it is where the
failures happened:

- **The lineage lived outside the system.** The orchestrator kept its own
  append-only ledger purely because the server does not record which job
  extended which. Nothing in the UI, the store, or the API can reconstruct the
  chain.
- **Bookkeeping drifted from reality.** The first run's `continue.state` pointed
  at a 160-circle job while its own log showed the chain had reached 512 — four
  hours and 352 circles of divergence, with no error anywhere.
- **A crash between "server accepted the stage" and "orchestrator recorded it"
  forks the chain.** The second run hit exactly this: a `KeyError` left an
  orphaned 16-circle extend job running with nothing tracking it. Recovery meant
  hand-adopting it back into the ledger.
- **Every policy decision was hardcoded in the script.** Polish milestones, the
  minimum gain worth continuing for, and the give-up-after-N-barren-stages rule
  are all schedule policy, and all of them were Python constants that required
  killing and restarting the orchestrator to change.
- **Silent field drops cost hours.** `convergenceEnabled: false` is `omitempty`
  and re-enabled by `ApplyDefaults` (`internal/app/config.go:323`); the real
  lever is `disableConvergence`. A schedule document must reject or warn on
  anything it did not actually apply.

The goal of this phase is that the whole campaign is stated once, up front,
and then runs unattended as a single observable entity.

### Task 16.1: Model a Schedule as a First-Class Entity (P1)

- [ ] Define a schedule document: a reference image, a base stage, and an
      ordered list of steps, where each step is `extend` or `polish` with its own
      parameter overrides. Support a generator form (`repeat: 63` with
      `additionalCircles: 8`) so a 64-stage campaign is not 64 stanzas.
- [ ] Persist the schedule and its realized stage lineage in `internal/store`,
      keyed independently of the job records, so the chain survives a server
      restart and can be read back without an external ledger.
- [ ] Record `extendedFrom`/`polishedFrom` on the job checkpoint itself, so a
      chain is reconstructible from the job tree alone even for jobs created
      outside a schedule.
- [ ] Validate the document strictly: unknown fields rejected, and any field
      that `ApplyDefaults` would override reported as an error rather than
      silently dropped.

**Acceptance Checks:**

- [ ] A schedule round-trips through the store and reloads with its lineage
      intact after a server restart.
- [ ] A document setting `convergenceEnabled: false` is rejected with a message
      naming `disableConvergence` as the effective field.

### Task 16.2: Run Schedules Server-Side (P1)

- [ ] Execute the schedule inside the server's job lifecycle
      (`internal/server`), not from a client. The run must survive the client
      disconnecting, and must respect `--max-jobs` so a schedule cannot
      oversubscribe the host.
- [ ] Make the executor crash-safe at the stage boundary: on startup, adopt any
      in-flight stage belonging to a schedule rather than starting a second one.
      This is the orphan-fork failure described above and it must be impossible
      by construction, not by convention.
- [ ] One source of truth for progress. Do not add a second state file that can
      drift from the stage records.
- [ ] Cancel, pause, and resume operate on the schedule as a whole, and cancelling
      a schedule cancels its in-flight stage.

**Acceptance Checks:**

- [ ] Killing the server mid-stage and restarting it resumes the same schedule
      without duplicating or skipping a stage — asserted by a test, not by
      inspection.
- [ ] A schedule and a manually created job cannot exceed `--max-jobs` together.

### Task 16.3: Express Stage Policy Declaratively (P2)

- [ ] Support conditional steps: run a polish only at listed circle counts, and
      stop scheduling polishes after N consecutive stages gained less than a
      threshold. Both were hardcoded Python constants; both are policy.
- [ ] Support per-step budget overrides (`iters`, `epochs`, `popSize`,
      `activeSetSize`, `maxSweeps`) so polish budget can differ from extend
      budget without a second document.
- [ ] Seed handling is explicit: one campaign seed, inherited by every stage, and
      recorded per stage so a single stage can be replayed.

**Acceptance Checks:**

- [ ] A schedule expressing the second run's policy — base 8, `+8` to 512, polish
      at 32/64/96/128/192/256, abort polishing after two stages under 1.0 cost
      units — is representable without custom code, and a table-driven test
      asserts the exact stage sequence it produces.

### Task 16.4: Estimate Before Committing Hours (P2)

- [ ] `--dry-run` prints the full realized stage list with per-stage parameters
      and the total optimizer iteration count, without touching the store.
- [ ] After the first few stages complete, report a projected finish time derived
      from observed stage wall clock. Extend stages are roughly flat in circle
      count because the frozen prefix is baked once
      (`internal/fit/renderer/pipeline.go`), so a projection is meaningful —
      but it must come from measurement, never from an a-priori model.

**Acceptance Checks:**

- [ ] `--dry-run` on the 512-circle campaign lists all stages and reports a
      total iteration count matching a hand computation.

### Task 16.5: Surface the Campaign in the UI and CLI (P2)

- [ ] A schedule view showing the stage table — circles, cost, PSNR, elapsed,
      accepted sweeps for polish stages — as one run rather than N unrelated
      jobs.
- [ ] Plot cost against circle count across the whole chain, which is the one
      view that actually answers "is this schedule better than the last one."
- [ ] A `schedule` CLI command mirroring the HTTP surface, following the existing
      short-imperative-verb convention (`run`, `resume`, `status`).

**Acceptance Checks:**

- [ ] The 96-circle chain already on disk can be imported and rendered as a
      single campaign view.

### Task 16.6: Retire the External Orchestrator (P2)

- [ ] Reproduce a short campaign (base 8, three `+8` extends, one polish) through
      the schedule feature and through the Python orchestrator, and confirm the
      cost sequence matches.
- [ ] Document the schedule format in `docs/`, with the 512-circle campaign as
      the worked example, and note the run-to-run comparability caveats
      (compositor version, SIMD tier, `fastCompositing`).

**Acceptance Checks:**

- [ ] The worked example in the docs is a file the test suite actually parses,
      so the documented format cannot drift from the implemented one.

---

## Summary and Next Steps

This plan covers **Phases 0-14** in complete detail with bite-sized, testable tasks. Each task follows TDD principles:
1. Write failing test
2. Run test to verify failure
3. Write minimal implementation
4. Run test to verify pass
5. Commit

**Implementation Strategy:**
- Execute Phase 14 before continuing feature work in Phases 12-13
- Follow the dependency-aware Phase 14 execution order rather than task number alone
- Complete and verify each remediation wave before starting dependent work
- Use the active task tracker to record progress within each wave
- Update PLAN.md with completion status as you go
- Commit frequently with descriptive messages
- Document learnings and decisions in CLAUDE.md

**Current Status:** Historical feature phases reached Phase 11-era implementation, and the main Phase 14 remediation waves now pass the local generation, build, test, race, static-analysis, 56.1% aggregate coverage, vulnerability, portability, GPU-compile, PoCL runtime, clean-clone recipe, metadata-free export, and release-lifecycle end-to-end gates. Automated SemVer releases are now dependency-gated in the repository workflow, but **Phase 14 remains active: remote CI must pass twice, repository-admin controls must prevent manual release bypass, and real-GPU vendor/performance validation is still required before promoting the experimental OpenCL backend.**
