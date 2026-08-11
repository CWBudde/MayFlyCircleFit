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

Implemented and tested the optimizer interface and Mayfly adapter with Standard, DESMA, and OLCE variants (2 tests); the project is pinned to `github.com/cwbudde/mayfly v0.3.0`.

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

### Task 6.5: Server-Sent Events (SSE) for Live Progress ⏭️ DEFERRED

- [ ] Create `internal/server/stream.go` for SSE support
  - [ ] Implement `GET /api/v1/jobs/:id/stream` endpoint
  - [ ] Set SSE headers (text/event-stream)
  - [ ] Create event channel per client connection
  - [ ] Send progress events (iteration, cost, cps) periodically
  - [ ] Handle client disconnect gracefully
  - [ ] Write integration test with SSE client

- [ ] Integrate SSE with worker
  - [ ] Add event broadcaster to JobManager
  - [ ] Emit events from worker during optimization
  - [ ] Throttle events (e.g., max 1 per 500ms)

**Note:** Deferred as polling-based status endpoint provides sufficient functionality.

### Task 6.6: CLI Integration - Serve Command ✅

Completed: `serve` flags, server startup, signal handling, graceful shutdown, logging, and manual verification.

### Task 6.7: CLI Integration - Status Command ✅

Completed: list/single-job status queries, output formatting, connection handling, and manual verification.

### Task 6.8: Integration Testing ✅

Completed: job-flow, concurrency, error, and graceful-shutdown integration coverage; SSE coverage was deferred here.

### Task 6.9: Documentation ✅

Completed: server/API/lifecycle documentation and curl examples; SSE format was deferred here.

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

## Phase 9: Performance Profiling & Fast Paths (CPU)

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

### Task 9.7: Add Optional Multi-Threading for Rendering

- [ ] Implement goroutine sharding over scanlines
- [ ] Add `--threads` flag to control parallelism
- [ ] Avoid oversubscription (default: GOMAXPROCS)
- [ ] Profile multi-threaded performance
- [ ] Measure speedup vs single-threaded baseline
- [ ] Document when threading helps vs hurts
- [ ] Write tests for thread-safe rendering

### Task 9.8: Create Comprehensive Benchmarks
- [ ] Create `internal/fit/bench_test.go` with benchmark suite
- [ ] Benchmark rendering: various K, W, H combinations
- [ ] Benchmark cost computation separately
- [ ] Benchmark full optimization pipeline
- [ ] Add benchmark regression tracking
- [ ] Document how to run benchmarks
- [ ] Add CI integration for benchmark tracking (optional)

### Task 9.9: Measure and Document Performance Improvements
- [ ] Re-run profiling on optimized code
- [ ] Generate new flamegraphs showing improvement
- [ ] Compare before/after benchmarks
- [ ] Document speedup percentages for each optimization
- [ ] Create performance report with graphs
- [ ] Update CLAUDE.md with optimization findings
- [ ] Identify remaining bottlenecks for future work

### Task 9.10: Validate Correctness After Optimizations
- [ ] Run full test suite to verify no regressions
- [ ] Compare optimized outputs with baseline (pixel-exact)
- [ ] Test with various edge cases (small circles, large circles, overlapping)
- [ ] Verify cost computation still accurate
- [ ] Test with different image sizes and circle counts
- [ ] Document any limitations or tradeoffs introduced

**Deliverables:**
- Profiling infrastructure and scripts
- Comprehensive benchmark suite
- Optimized CPU renderer with measurable speedup
- Performance report with before/after metrics
- Documentation of optimization techniques

**Acceptance Checks:**
- [ ] Profiling shows top offenders moved in right direction
- [ ] Benchmarks demonstrate improvement without changing outputs
- [ ] All existing tests still pass
- [ ] Memory allocations reduced significantly
- [ ] Rendering throughput increased by measurable amount

---

## Phase 10: SIMD/C Intrinsics Research & Implementation (Evaluation Loop)

**Goal:** Recover a large chunk of the original "blazing fast" feel by applying vectorized kernels to the evaluation hot path.

**Status:** Research complete, implementation in progress

### Task 10.1: Research SIMD Approaches and Design ✅ COMPLETE

Completed: selected Plan 9 assembly with runtime dispatch and scalar fallback; documented design, portability, build tags, alignment, and expected AVX2/NEON gains in `docs/simd-design.md`.

### Task 10.2: Design SSD Kernel Interface ✅ COMPLETE

Completed: `fastSSD`/`FastSSD` interfaces, runtime backend dispatch, scalar baseline, backend reporting, and a 27-test validation harness.

### Task 10.3: Implement Scalar Baseline SSD Kernel

Completed: optimized pure-Go NRGBA SSD baseline with alpha exclusion, equivalence/edge tests, and benchmarks.

### Task 10.4: Implement AVX2 SSD Kernel (x86-64) - **ADAPTED FROM RESEARCH**
**Approach:** Plan9 Assembly via GoAT transpilation (NOT cgo)

**Phase 1: C Prototype (for validation)**
- [x] Create `prototypes/ssd_avx2.c` with AVX2 intrinsics
- [x] Process 8 pixels per iteration using 256-bit registers (not 32 - that's bytes)
- [x] Implement horizontal sum reduction (vhaddps / reduction tree)
- [x] Handle remainder pixels with scalar loop
- [x] Test in C: Verify correctness, benchmark vs scalar C
- [x] Target: 4-6× speedup over scalar C implementation

**Phase 2: GoAT Transpilation**
- [x] Install GoAT: `go install github.com/gorse-io/goat@latest`
- [ ] Transpile: `goat -O3 prototypes/ssd_avx2.c > internal/fit/ssd_amd64.s`
- [ ] Review generated Plan9 assembly, add comments
- [ ] Hand-tune hot loops if needed (compare against Minio HighwayHash patterns)
- [ ] Fix Go calling convention (verify FP offsets for parameters)

**Phase 3: Integration**
- [ ] Create `internal/fit/ssd_amd64.go` with function declaration
- [ ] Add build tag: `//go:build amd64`
- [ ] Wire into runtime dispatch (replace placeholder in ssd.go init())
- [ ] Write tests verifying bit-exact equivalence to scalar
- [ ] Create benchmarks comparing to scalar baseline
- [ ] Document performance improvement (target: 4-6× @ 1.2-2 Gpixels/sec)

### Task 10.5: Implement NEON SSD Kernel (ARM64) - **ADAPTED FROM RESEARCH**
**Approach:** Plan9 Assembly via GoAT transpilation (NOT cgo)
**Note:** Only pursue if Task 10.4 (AVX2) achieves ≥4× speedup

**Phase 1: C Prototype (for validation)**
- [ ] Create `prototypes/ssd_neon.c` with NEON intrinsics (`arm_neon.h`)
- [ ] Process 4 pixels per iteration using 128-bit registers
- [ ] Implement horizontal sum reduction (vpaddq / reduction tree)
- [ ] Handle remainder pixels with scalar loop
- [ ] Test in C: Verify correctness, benchmark vs scalar C
- [ ] Target: 3-4× speedup over scalar C implementation

**Phase 2: GoAT Transpilation**
- [ ] Transpile: `goat -O3 prototypes/ssd_neon.c > internal/fit/ssd_arm64.s`
- [ ] Review generated Plan9 assembly for ARM64, add comments
- [ ] Hand-tune if needed (NEON has different instruction set than AVX2)
- [ ] Verify Go calling convention for ARM64

**Phase 3: Integration**
- [ ] Create `internal/fit/ssd_arm64.go` with function declaration
- [ ] Add build tag: `//go:build arm64`
- [ ] Wire into runtime dispatch (replace placeholder in ssd.go init())
- [ ] Write tests verifying bit-exact equivalence to scalar
- [ ] Create benchmarks comparing to scalar baseline
- [ ] Test on Apple Silicon (M1/M2/M3) and AWS Graviton
- [ ] Document performance improvement (target: 3-4×)

### Task 10.6: Implement Runtime Feature Detection and Dispatch - **MOSTLY COMPLETE**
**Note:** Core dispatch implemented in Task 10.2 (`internal/fit/ssd.go`), remaining work is validation

- [x] Create runtime selection mechanism (function pointer `fastSSD` in init())
- [x] Use `golang.org/x/sys/cpu` for feature detection
- [x] Detect AVX2 support on amd64 (`cpu.X86.HasAVX2`)
- [x] Detect NEON support on arm64 (`cpu.ARM64.HasASIMD`)
- [x] Select fastest available kernel at startup
- [x] Fall back to scalar if no SIMD available
- [x] Add logging to show which kernel was selected (slog.Debug)
- [x] Write tests for dispatch logic (TestSSDBackendDetection)
- [ ] Test fallback behavior with `GODEBUG=cpu.avx2=off` override
- [ ] Add benchmarks comparing dispatch overhead (<2ns expected)

### Task 10.7: Integrate SIMD SSD into Cost Function
- [ ] Replace MSE cost computation with `fastSSD()`
- [ ] Ensure same results as original implementation
- [ ] Add benchmarks for full cost function
- [ ] Profile to verify SSD is no longer bottleneck
- [ ] Test with various image sizes
- [ ] Document integration points

### Task 10.8: Cross-Platform Testing and Build Validation - **ADAPTED FROM RESEARCH**
**Note:** Pure Go build (no cgo), standard `go build` workflow

- [ ] Test cross-compilation with GOARCH override
  - [ ] `GOOS=linux GOARCH=amd64 go build` (AVX2 assembly)
  - [ ] `GOOS=linux GOARCH=arm64 go build` (NEON assembly)
  - [ ] `GOOS=darwin GOARCH=amd64 go build` (AVX2 for Intel Mac)
  - [ ] `GOOS=darwin GOARCH=arm64 go build` (NEON for Apple Silicon)
  - [ ] `GOOS=windows GOARCH=amd64 go build` (AVX2)
  - [ ] `GOOS=linux GOARCH=386 go build` (scalar fallback, 32-bit)
- [ ] Verify build tags select correct implementation
  - [ ] amd64 → ssd_amd64.s
  - [ ] arm64 → ssd_arm64.s
  - [ ] others → ssd_generic.go
- [ ] Test on actual hardware (not just cross-compile)
  - [ ] Linux x86-64 with AVX2
  - [ ] macOS ARM64 (Apple Silicon)
  - [ ] Windows x86-64 with AVX2
  - [ ] Linux ARM64 (AWS Graviton or Raspberry Pi)
- [ ] Document platform-specific performance characteristics
- [ ] Ensure `go build` works without external dependencies (no C compiler required)

### Task 10.9: Create SIMD Test Matrix
- [ ] Test on amd64 with AVX2 support
- [ ] Test on amd64 without AVX2 (scalar fallback)
- [ ] Test on arm64 with NEON support (Apple M-series)
- [ ] Test on arm64 without NEON (scalar fallback)
- [ ] Verify identical results across all platforms
- [ ] Compare performance across platforms
- [ ] Document test matrix results

### Task 10.10: Performance Validation and Documentation - **ADAPTED FROM RESEARCH**
- [ ] Create comprehensive benchmark suite for SIMD kernels
  - [ ] Benchmark matrix: 64×64, 128×128, 256×256, 512×512, 1024×1024
  - [ ] Compare: scalar, AVX2, NEON on respective platforms
  - [ ] Measure throughput (Mpixels/sec) and speedup ratio
- [ ] Measure speedup on various image sizes
  - [ ] Small (64×64): May have overhead, lower speedup
  - [ ] Medium (256×256): Target 4-6× for AVX2
  - [ ] Large (512×512, 1024×1024): Check if memory-bound
- [ ] Create performance comparison table
  - [ ] Columns: Image size, Scalar (Mpixels/s), AVX2, NEON, Speedup
  - [ ] Include: Baseline (316 Mpixels/sec @ 256×256 scalar)
- [ ] Document expected speedup ranges
  - [ ] AVX2: 4-6× (target 1.2-2 Gpixels/sec)
  - [ ] NEON: 3-4× (target 0.9-1.2 Gpixels/sec)
- [ ] Profile memory access patterns (check for cache misses)
- [ ] Ensure no GC pressure (pure Go, no cgo allocations)
- [ ] Update CLAUDE.md with SIMD architecture
  - [ ] Document Plan9 assembly approach
  - [ ] Document GoAT workflow
  - [ ] Document runtime dispatch mechanism
- [ ] Create optimization report: `docs/task-10.10-simd-performance-report.md`

### Task 10.11: Circle Rendering Optimization - **PARTIALLY COMPLETE**

Completed scoped work: scanline rasterization was profiled, integrated, and pixel-equivalence tested, improving the full 256×256/50-circle pipeline by 1.28×. Further rendering work remains explicitly open in Tasks 10.12–10.15.

### Task 10.12: (Optional) SIMD Horizontal Span Compositing
**Rationale**: Alpha compositing is now 36% of rendering time. SIMD can process 4-8 pixels simultaneously.

**Approach:**
- [ ] Create C prototype with AVX2 intrinsics for `compositePixelsSIMD()`
- [ ] Process horizontal spans in 8-pixel chunks (AVX2) or 4-pixel chunks (NEON)
- [ ] Handle remainder pixels with scalar fallback
- [ ] Benchmark isolated compositing vs full rendering pipeline
- [ ] Target: 2-4x on compositing → ~1.3x overall speedup

**Technical Details:**
```c
// Process 8 pixels with AVX2
void compositePixelsSIMD(uint8_t* img, int x, int y,
                        float r, float g, float b, float alpha, int count) {
    __m256 fg_r = _mm256_set1_ps(r * alpha);
    __m256 fg_g = _mm256_set1_ps(g * alpha);
    __m256 fg_b = _mm256_set1_ps(b * alpha);

    // Load 8 pixels, convert to float, blend, convert back
    // Porter-Duff "over" operator in SIMD
}
```

**Expected Outcome:**
- Compositing: 10ns/pixel → 2.5ns/pixel (4x)
- Overall rendering: 1.87ms → 1.45ms (1.3x)

### Task 10.13: (Optional) Integer-Only Circle Math
**Rationale**: Eliminate floating-point operations in circle distance checks.

**Approach:**
- [ ] Replace `float64` distance calculations with fixed-point `int64`
- [ ] Use 8-bit or 16-bit fractional precision
- [ ] Precompute `r2_minus_dy2` in integer space
- [ ] Benchmark distance check overhead in isolation

**Technical Details:**
```go
// Current: if dx*dx + dy2 > r2 (float64 mul + add + compare)
// Optimized: if (dx<<8)*(dx<<8) + (dy2<<8) > (r2<<8) (int64 ops)
```

**Expected Outcome:**
- Distance checks: 18ns → 14-16ns (10-20% reduction)
- Overall rendering: 1.87ms → 1.68ms (1.11x)

### Task 10.14: (Optional) Circle Symmetry Exploitation
**Rationale**: Circles are vertically symmetric - compute upper half, mirror to lower.

**Approach:**
- [ ] Modify scanline loop to iterate from `centerY` to `maxY` only
- [ ] For each row `y`, also render mirrored row `2*centerY - y`
- [ ] Handle odd-height circles (center row rendered once)
- [ ] Verify correctness for edge-clipped circles

**Technical Details:**
```go
centerY := int(c.Y + 0.5)
for y := centerY; y < maxY; y++ {
    // Compute span for row y
    renderSpan(img, y, xStart, xEnd, color)

    // Mirror to opposite row
    mirrorY := 2*centerY - y
    if mirrorY >= minY && mirrorY != y {
        renderSpan(img, mirrorY, xStart, xEnd, color)
    }
}
```

**Expected Outcome:**
- Row iteration: 2R rows → R rows (2x reduction)
- Overall rendering: 1.87ms → 1.65ms (1.13x)
- **Note**: Savings diminished by search overhead (still done twice)

### Task 10.15: (Optional) Combined Optimization
**Rationale**: Stack all three optimizations for maximum performance.

**Expected Combined Speedup:**
- SIMD compositing: 1.3x
- Integer math: 1.11x
- Symmetry: 1.13x
- **Combined**: ~1.64x over current scanline (3.07ms → 1.87ms)
- **Total improvement**: ~1.28x * 1.64x = **2.1x over original** (2.40ms → 1.14ms)

**Deliverables:**
- ✅ SIMD design document with approach comparison (`docs/simd-design.md`)
- ✅ SSD kernel interface with runtime dispatch (`internal/fit/ssd.go`)
- ✅ Comprehensive test harness (`internal/fit/ssd_test.go` - 27 tests passing)
- Scalar baseline SSD kernel with optimization
- AVX2 SSD kernel via GoAT transpilation (Plan9 assembly)
- NEON SSD kernel via GoAT transpilation (Plan9 assembly)
- Performance benchmarks showing 4-6× speedup
- Documentation of SIMD implementation

**Acceptance Checks:**
- [ ] Tests pass across all architectures (amd64, arm64, 386, etc.)
- [ ] Identical results between scalar and SIMD implementations (within float tolerance)
- [ ] Substantial speedup on supported CPUs (4-6× for AVX2, 3-4× for NEON)
- [ ] No GC pressure (pure Go, no cgo allocations)
- [ ] Build works with standard `go build` (no C compiler required)
- [ ] Runtime dispatch selects optimal kernel (AVX2 > NEON > scalar)
- [ ] Cross-compilation works for all platforms (GOOS/GOARCH override)

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

### Task 11.3: Design GPU Renderer Architecture
- [x] Create `internal/fit/renderer_<gpu>.go` skeleton
- [x] Implement `Renderer` interface for GPU backend
- [x] Design shader/kernel for circle compositing
- [x] Design reduction kernel for cost computation
- [x] Plan memory transfer strategy (CPU ↔ GPU)
- [x] Minimize transfers: keep reference image on GPU
- [ ] Document GPU memory layout (add notes in docs/gpu-backends.md)

### Task 11.4: Implement GPU Circle Compositing Shader/Kernel
- [x] Write shader/kernel for circle rendering
  - [x] Input: circle parameters (X, Y, R, CR, CG, CB, Opacity)
  - [x] Output: composited image on GPU
  - [x] Use Porter-Duff alpha compositing
- [x] Implement shader loading and compilation
- [x] Add error handling for shader compilation failures
- [x] Test with simple single-circle cases (unit test under `//go:build gpu`)
- [ ] Verify visual correctness against CPU renderer (expand tests with golden images)

### Task 11.5: Implement GPU Cost Computation
- [x] Write shader/kernel for per-pixel error computation
  - [x] Input: rendered image, reference image
  - [x] Output: per-pixel squared differences
- [x] Implement GPU reduction to scalar cost
  - [x] Option 1: Multi-pass reduction kernel (selected implementation)
  - [x] Option 2: GPU compute + CPU final sum (initial prototype, superseded)
  - [x] Choose based on performance (on-device reduction avoids per-pixel error readback)
- [x] Test cost computation accuracy
- [x] Compare with CPU cost (allow float tolerance)

### Task 11.6: Implement Memory Transfer Strategy
- [x] Upload reference image to GPU once at initialization
- [x] Transfer circle parameters to GPU per evaluation
- [x] Minimize transfer overhead with persistent device buffers and lazy host image readback
- [ ] Consider pinned memory for faster transfers
- [ ] Profile memory transfer overhead
- [ ] Optimize transfer strategy based on profiling

### Task 11.7: Integrate GPU Renderer into Pipeline
- [ ] Update pipeline functions to accept GPU renderer
- [ ] Test joint optimization with GPU backend
- [ ] Test sequential optimization with GPU backend
- [ ] Test batch optimization with GPU backend
- [ ] Verify all modes work correctly
- [ ] Compare performance to CPU backend

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

### Task 12.1: Implement View Mode Toggles
- [ ] Add view mode selector to job detail page
  - [ ] Radio buttons or dropdown: Reference, Best, Side-by-Side, Difference Heatmap
  - [ ] Persist selection in browser localStorage
- [ ] Implement "Reference Only" view
  - [ ] Display reference image at full size
  - [ ] Show image dimensions and file size
- [ ] Implement "Best Only" view
  - [ ] Display current best rendered image
  - [ ] Auto-update from SSE stream
- [ ] Implement "Side-by-Side" view
  - [ ] Two-pane layout with synchronized zoom/pan (optional)
  - [ ] Show reference on left, best on right
  - [ ] Equal sizing for visual comparison
- [ ] Implement "Difference Heatmap" view
  - [ ] Show false-color visualization of pixel differences
  - [ ] Use colormap (turbo, magma, or viridis)
  - [ ] Include legend showing error magnitude
- [ ] Add keyboard shortcuts for view switching (1, 2, 3, 4)
- [ ] Test view transitions and image loading

### Task 12.2: Implement Difference Heatmap Visualization
- [ ] Create colormap utility in `internal/fit/colormap.go`
  - [ ] Implement turbo colormap (recommended)
  - [ ] Implement magma colormap (alternative)
  - [ ] Map error values [0, max_error] to RGB
- [ ] Update diff.png generation to use colormap
  - [ ] Compute per-pixel absolute error
  - [ ] Normalize to [0, 1] range
  - [ ] Apply colormap transformation
- [ ] Add colormap selection to UI
  - [ ] Dropdown to choose colormap
  - [ ] Update diff.png with selected colormap
- [ ] Add color legend to heatmap view
  - [ ] Show gradient bar with labels
  - [ ] Display min/max error values
- [ ] Write tests for colormap functions
- [ ] Document colormap choices and interpretation

### Task 12.3: Add Advanced Metrics (PSNR, Optional SSIM)
- [ ] Implement PSNR (Peak Signal-to-Noise Ratio) calculation
  - [ ] Create `internal/fit/metrics.go`
  - [ ] Formula: PSNR = 20 * log10(255 / sqrt(MSE))
  - [ ] Add to job status response
  - [ ] Display in UI metrics panel
- [ ] Implement optional SSIM (Structural Similarity Index)
  - [ ] Add `--enable-ssim` flag (off by default due to cost)
  - [ ] Implement SSIM calculation over RGB channels
  - [ ] Add to job status response (if enabled)
  - [ ] Display in UI metrics panel (if available)
- [ ] Add metrics history tracking
  - [ ] Store metrics over time in trace.jsonl
  - [ ] Display metrics evolution in UI
- [ ] Write tests for metric calculations
- [ ] Document metrics interpretation and usage

### Task 12.4: Add Parameter Inspection Tooltip
- [ ] Display current best parameters in UI
  - [ ] Show all K circles with their properties
  - [ ] Format: Circle N: (X, Y, R) RGB(r, g, b) α=opacity
- [ ] Add interactive parameter viewer
  - [ ] Expandable/collapsible list of circles
  - [ ] Highlight individual circles on hover (optional)
- [ ] Add parameter export button
  - [ ] Download params.json with current best
  - [ ] Include metadata: jobID, cost, iterations, timestamp
- [ ] Add parameter visualization (optional)
  - [ ] Show circles sorted by size or opacity
  - [ ] Color-code by properties
- [ ] Test parameter display with various circle counts

### Task 12.5: Add Download Buttons for Artifacts
- [ ] Add "Download Best Image" button
  - [ ] Download current best.png
  - [ ] Filename: `job-<id>-best.png`
- [ ] Add "Download Parameters" button
  - [ ] Download params.json
  - [ ] Filename: `job-<id>-params.json`
- [ ] Add "Download Difference Image" button
  - [ ] Download diff.png with colormap
  - [ ] Filename: `job-<id>-diff.png`
- [ ] Add "Download Report" button
  - [ ] Generate HTML report with all artifacts
  - [ ] Include: reference, best, diff images, parameters, metrics, metadata
  - [ ] Self-contained HTML file (embedded images as base64)
  - [ ] Filename: `job-<id>-report.html`
- [ ] Test downloads on various browsers
- [ ] Add loading states during report generation

### Task 12.6: Generate HTML Report
- [ ] Create report template in `internal/ui/report.templ`
  - [ ] Header with job metadata (ID, mode, circles, date)
  - [ ] Three-column layout: Reference, Best, Difference
  - [ ] Metrics table: Cost, PSNR, SSIM, iterations, time
  - [ ] Parameters table: All circles with properties
  - [ ] Footer with generation timestamp
- [ ] Implement report generation endpoint
  - [ ] `GET /api/v1/jobs/:id/report.html`
  - [ ] Embed images as base64 data URIs
  - [ ] Inline CSS for styling
  - [ ] No external dependencies
- [ ] Add print-friendly CSS styles
  - [ ] Page breaks between sections
  - [ ] High-contrast colors
- [ ] Test report rendering and downloading
- [ ] Document report format and customization

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

**Current Status:** Historical feature phases reached Phase 11-era implementation, and the main Phase 14 remediation waves now pass the local generation, build, test, race, static-analysis, 56.1% aggregate coverage, vulnerability, portability, GPU-compile, PoCL runtime, clean-clone recipe, metadata-free export, and release-lifecycle end-to-end gates. **Phase 14 remains active: remote CI must pass twice, release-policy enforcement remains open, and real-GPU vendor/performance validation is still required before promoting the experimental OpenCL backend.**
