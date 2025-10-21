# Phase 0 — Project scaffolding & conventions

**Goal:** Set up a maintainable repo with single-responsibility packages, logging, CLI scaffolding, and configuration.

**Tasks**

* Initialize module, repo, and directory layout:

  ```
  /cmd/mayflyfit          # cobra CLI entry
  /internal/fit           # rendering, cost, pipelines
  /internal/opt           # mayfly/DE optimizers
  /internal/server        # http server, background jobs, SSE/ws
  /internal/ui            # templ components
  /internal/store         # checkpoints, runs, artifacts
  /internal/pkg           # small utility helpers (atomic images, pooling)
  /assets                 # example reference images
  ```
* Add dependencies: cobra (CLI), templ (frontend), slog (structured logging), net/http (server), optionally chi for routing.
* Logging policy: slog with JSON by default; support `--log-level` and human-readable console output.
* Configuration: TOML/YAML/JSON + env overrides, but **all** options also available via CLI flags.
* Makefiles/scripts: `make run`, `make build`, `make fmt`, `make test`.
* Tooling: go vet, golangci-lint (basic rules), editorconfig.

**Deliverables**

* Compiles, `mayflyfit --help` prints Cobra help.
* `slog` logs hello-world lines at chosen level.
* README with short overview.

**Acceptance checks**

* CI passes lint & build.
* Flags parsed and visible in logs.

---

# Phase 1 — Core domain model (no optimization yet)

**Goal:** Express circles, parameters, bounds, and error metric in a way that’s easy to optimize and test.

**Tasks**

* Define parameter encoding:

  * Per circle: `(x, y, r, cR, cG, cB, opacity)` with ranges

    * `x ∈ [0, W)`, `y ∈ [0, H)`, `r ∈ [1, max(W,H)]`
    * `cR,cG,cB,opacity ∈ [0,1]`
  * Vector layout: `[c1..., c2..., ...]`
* Add helpers:

  * `Params<K>.Decode(i int) -> Circle` and `Encode(Circle) -> slice indices`
  * Clamp/validate and (optionally) penalty strategy for invalids.
* Error metric:

  * Start with **MSE over sRGB** (fast, deterministic).
  * Provide an interface `Cost(current, reference) float64` so we can swap metrics later.

**Deliverables**

* `internal/fit/types.go`: `Circle`, `ParamVector`, bounds helpers.
* `internal/fit/cost.go`: MSE cost + tests (handcrafted image pairs).
* Unit tests that verify decoding/encoding symmetry and cost sanity.

**Acceptance checks**

* Tests proving that:

  * Black vs white image cost > 0; identical images cost == 0.
  * Single red pixel perturbs cost predictably.

---

# Phase 2 — CPU renderer (baseline, clear & correct)

**Goal:** A simple, correct CPU implementation that composites circles over white and computes cost.

**Tasks**

* Renderer interface:

  ```go
  type Renderer interface {
      Render(params []float64) *image.NRGBA // white bg + circles
      Cost(params []float64) float64        // uses reference + metric
      Dim() int
      Bounds() (lo, hi []float64)
  }
  ```
* `CPURenderer`:

  * Premultiplied alpha compositing.
  * Conservative circle rasterization (axis-aligned bounding box scan, inside test).
  * Avoid heap churn: reuse image buffers, stride-aware loops.
* Micro-optimizations later; keep this version clean & obviously correct.

**Deliverables**

* `internal/fit/renderer_cpu.go` with tests (renders of trivial cases).
* Golden images for a couple of tiny cases (e.g., 16×16 circles).

**Acceptance checks**

* Pixel-exact tests pass.
* Cost(white canvas with no circles) == cost of all-white reference (≈0).

---

# Phase 3 — Optimizer (Mayfly baseline) + DE interface

**Goal:** Pluggable optimizer with reproducible runs.

**Tasks**

* Define optimizer interface:

  ```go
  type Optimizer interface {
      Run(eval func([]float64) float64, lo, hi []float64, dim int) (best []float64, bestCost float64)
  }
  ```
* Implement **Mayfly** (male/female swarms, attraction/exploration, simple crossover/mutation).
* (Optional) Add DE as a second optimizer behind the same interface.
* Random seed handling for reproducibility; document recommended defaults.

**Deliverables**

* `internal/opt/mayfly.go` (+ config struct).
* `internal/opt/de.go` (stub or later).
* Tests: sphere/rosenbrock/box bounds toy functions to sanity-check convergence.

**Acceptance checks**

* Deterministic with fixed seed.
* Converges on small synthetic functions.

---

# Phase 4 — Pipelines: joint, sequential, batch-n

**Goal:** Provide three modes without changing optimizer code.

**Tasks**

* `OptimizeJoint(renderer, optimizer, K, cfg)`.
* `OptimizeSequential(renderer, optimizer, totalK, cfg)`:

  * Lock previously found circles, optimize one new circle on top of current canvas (greedy).
* `OptimizeBatch(renderer, optimizer, batchK, passes, cfg)`: add `batchK` circles per pass.
* Optional “cost map” hook (stub): keep API space to bias search later.

**Deliverables**

* `internal/fit/pipeline.go` with the three exported functions.
* Tests that check:

  * Joint and sequential produce non-increasing final cost when K increases.
  * Sequential with K=1 matches Joint K=1.

**Acceptance checks**

* CLI (next phase) can choose mode and K.

---

# Phase 5 — CLI with Cobra (log-only UX)

**Goal:** User can run optimization from terminal; logs show progress and “circles per second”.

**Tasks**

* Subcommands:

  * `serve` — starts HTTP server (Phase 6).
  * `run` — single-shot optimization (no server), writes `out.png`, dumps params to JSON, prints cost and throughput.
  * `status` — queries server (Phase 6) and prints a summary.
  * `resume` — resumes from a checkpoint (Phase 7).
* Flags: `--ref`, `--width`, `--height`, `--mode`, `--circles`, `--optimizer`, `--iters`, `--pop`, `--seed`, `--metric`.
* Human progress: per-N-iterations log with best cost delta and estimated **circles/sec** (computed as total circle draws / elapsed).

**Deliverables**

* `cmd/mayflyfit/root.go`, `cmd/mayflyfit/run.go`, `cmd/mayflyfit/serve.go`, etc.
* Sane log formatting using `slog`.

**Acceptance checks**

* `mayflyfit run ...` completes, writes `out.png`, prints cost + cps.

---

# Phase 6 — Background server + job model + live progress

**Goal:** A long-running process that executes optimization in the background and exposes a simple UI.

**Tasks**

* Server:

  * Start with `net/http`, optional chi for routing.
  * Endpoints:

    * `POST /api/v1/jobs` start/queue a job (payload: ref path or upload, canvas size, K, mode, optimizer config).
    * `GET  /api/v1/jobs/:id/status` returns JSON (state, best cost, iterations, cps).
    * `GET  /api/v1/jobs/:id/best.png` returns current best render.
    * `GET  /api/v1/jobs/:id/diff.png` returns ref vs best difference image (false-color heatmap).
    * `GET  /api/v1/jobs/:id/stream` SSE or WebSocket with incremental progress events (cost over time, cps).
* Background worker:

  * Single goroutine per job, cooperative cancellation via `context.Context`.
  * Periodic checkpoints (Phase 7).
  * Atomics or `sync.RWMutex` to read current best for endpoints.
* CLI integration:

  * `serve` command launches server with logs.
  * `status` & `resume` commands call server.
* Security: assume local dev; defer auth for later.

**Deliverables**

* `internal/server/http.go`, `internal/server/job.go` (queue, state machine), `internal/server/stream.go`.
* JSON schemas for status responses.

**Acceptance checks**

* Can start a job, see `/best.png` update while optimizing.
* SSE stream shows cost decreasing events.

---

# Phase 7 — Frontend with templ (reference vs fittest)

**Goal:** Pretty, minimal dashboard that shows the current best vs reference and a small metrics panel.

**Tasks**

* templ pages:

  * `/` — list jobs; click through to a job.
  * `/jobs/:id` — two-pane: **Reference** image and **Current Best** (auto-refresh or live via SSE), cost, cps, iterations, K, mode.
  * Optional sparkline of cost from SSE.
* Image delivery:

  * Make `/best.png` etag or cache-busting (`?t=unix`); or use `<img src="...">` that the SSE updates.
* Styling: keep lightweight (templ + simple CSS).

**Deliverables**

* `internal/ui/pages.templ` (job list + detail).
* Server wires templ handlers.

**Acceptance checks**

* With `serve` running, visiting job page visually shows progress (images update, cost ticks).

---

# Phase 8 — Persistence & checkpoints (resume)

**Goal:** Don’t lose progress; enable pausing/resuming long runs.

**Tasks**

* `internal/store` with two artifacts per job id:

  * `params_best.json` (best vector so far).
  * `best.png` and `diff.png` snapshots.
  * (Optional) `trace.jsonl` with per-tick cost records.
* Periodic checkpoint interval configurable (e.g., every N iterations or N seconds).
* `resume` command attaches to an existing job id and continues from best params.

**Deliverables**

* Store with simple filesystem backend.
* CLI `resume` flows and server `POST /jobs/:id/resume`.

**Acceptance checks**

* Kill server mid-run, restart, resume from checkpoint; cost continues decreasing from previous best.

---

# Phase 9 — Performance profiling & fast paths (CPU)

**Goal:** Identify bottlenecks and implement safe, incremental speedups on CPU.

**Tasks**

* Profiling:

  * Add `-cpuprofile`/`-memprofile` or pprof endpoints in server.
  * Flamegraph typical runs (small/medium W×H, K=1..64).
* Fast paths:

  * Avoid per-pixel bounds checks in inner loops; precompute circle AABBs.
  * Reuse buffers, avoid allocations in render/cost.
  * Early-reject: skip compositing if a circle is fully outside image or opacity≈0.
  * Cache the white background as a prefilled image; reset via `copy()` instead of loops.
* Data layout:

  * Tight SoA vs AoS tradeoffs for params (vectorization friendly).
* Threads:

  * Optional goroutine sharding over scanlines (only if it’s a win; avoid oversubscription).

**Deliverables**

* Benchmarks under `internal/fit/bench_test.go`.
* Documented before/after numbers (no promises in advance, just measured results).

**Acceptance checks**

* Profiling shows top offenders moved in the right direction.
* Benchmarks demonstrate improvement without changing outputs.

---

# Phase 10 — SIMD/C intrinsics research & implementation (evaluation loop)

**Goal:** Recover a large chunk of the original “blazing fast” feel by applying vectorized kernels to the *evaluation* hot path (and, optionally, circle fill).

**What to accelerate first**

* **Cost accumulation**: sum of squared differences (SSD) between two equal-sized RGB buffers.

  * That’s ideal for SIMD: load 32–64 bytes at a time, widen to 16/32-bit lanes, subtract, square (or multiply), horizontally sum.

**Tasks**

1. **Research & design (deliverable document):**

   * Options:

     * **cgo + C with intrinsics** (portable across compilers; AVX2 on x86-64, NEON on arm64).
     * **Go assembly** (plan9 or Go asm) — fastest but more complex to maintain, separate files per arch.
     * **Pure Go with `unsafe` & `golang.org/x/sys/cpu`** feature detection + stubbed vector ops (limited autovec).
   * Runtime dispatch:

     * Use `x/sys/cpu` to detect AVX2/FMA on amd64, NEON/ASIMD on arm64.
     * Provide scalar fallback.
   * Build tags and file layout:

     ```
     internal/fit/ssd_scalar.go
     internal/fit/ssd_avx2.c     // + cgo, intrinsics
     internal/fit/ssd_neon.c     // + cgo, intrinsics
     internal/fit/ssd_avx2.go    // small Go wrapper
     internal/fit/ssd_neon.go
     ```
   * Memory alignment & safety considerations (unaligned loads allowed on x86, stricter on some ARM).
2. **Prototype 1 (cgo AVX2)**:

   * Implement AVX2 SSD kernel for interleaved NRGBA (ignore alpha): process 32 pixels per iteration if possible.
   * Benchmark vs scalar Go; ensure bit-exact equivalence to MSE reference.
3. **Prototype 2 (NEON)**:

   * Implement NEON SSD kernel for arm64 (Apple M-series is a great target).
4. **Integration**:

   * `Cost()` uses `fastSSD()` if available; else falls back to scalar.
   * Keep **render** on Go for now; only accelerate **cost** to keep surface area small.
5. **(Optional) Circle fill kernel**:

   * Harder to vectorize due to inside tests; only pursue if profiling shows it dominates after SSD is fast.

**Deliverables**

* Design doc (markdown) summarizing choices and test matrix (amd64, arm64).
* Benchmarks demonstrating substantial SSD speedup on supported CPUs.
* Guarded runtime dispatch with scalar fallback; identical results.

**Acceptance checks**

* Tests pass across architectures.
* No GC pressure or leaks from cgo; build works with/without cgo.

---

# Phase 11 — GPU backends (research → prototype)

**Goal:** Add a pluggable GPU renderer/coster behind the existing `Renderer` interface.

**Approach (research first, then a thin prototype)**

* **Research deliverable** comparing:

  * **OpenGL fragment/compute** path (widely available; easy circle loop in shader).
  * **OpenCL** compute path (portable across GPUs; good for SSD + compositing).
  * **WebGPU** via native bindings (modern, still maturing in Go).
  * **Vulkan compute** (powerful but heavy to integrate).
* Criteria to evaluate:

  * Ease of binding from Go (maintenance burden).
  * Driver portability (Windows/Linux/macOS; NVIDIA/AMD/Intel/Apple).
  * Debuggability and stability.
* **Prototype choice:** implement **one** backend end-to-end:

  * Circle compositing in shader/kernel, write per-pixel error, reduce to scalar (GPU or CPU reduction).
  * Same `Renderer` methods; the rest of the pipeline stays untouched.

**Deliverables**

* Comparison doc + “pick one” recommendation.
* `internal/fit/renderer_<gpu>.go` + kernels/shaders.
* Benchmarks vs CPU for several K/W/H combos.

**Acceptance checks**

* Drop-in selectable with `--backend cpu|<gpu-name>`.
* Identical cost (within float tolerances) vs CPU.

---

# Phase 12 — UX & visualization polish

**Goal:** Make it pleasant to use and reason about results.

**Tasks**

* UI: toggle between **Reference**, **Best**, **Side-by-Side**, **Difference heatmap**.
* Metrics: PSNR, optional SSIM (off by default due to cost).
* Tooltip showing current best params.
* Download buttons for `params.json`, `out.png`, `diff.png`, and a tiny HTML report.

**Deliverables**

* Additional templ components & styles.
* Small utility to colorize diff (e.g., turbo or magma colormap).

**Acceptance checks**

* Everything updates live; no refresh needed if SSE is enabled.

---

# Phase 13 — Robustness, docs, packaging

**Goal:** Make this shippable.

**Tasks**

* Error handling & clear failure states in server/CLI.
* Readme + “Getting Started” + “Architecture” pages.
* Sample references & example commands.
* Benchmarks documented; known limitations listed.
* Versioning, changelog, license.

**Deliverables**

* Comprehensive docs under `/docs`.
* Release build artifacts (where applicable).

**Acceptance checks**

* A new contributor can clone, run `serve`, visit the UI, and see progress without reading the source.

---

## Notes on concurrency, safety, and testability

* **Threading model:** one worker goroutine per job, communicates via channels; reader endpoints use read locks/atomics to fetch the latest best.
* **Determinism:** optimizer accepts a seed; tests pin the seed.
* **Reproducibility:** costs depend only on params + ref; decouple any timing from the outcome.
* **Test seams:** each phase adds pure functions with unit tests (cost, bounds, encode/decode, SSD kernels).

---

## What you’ll have at each “stop”

* After **Phase 2–5**: Run in terminal, get outputs and logs.
* After **Phase 6–7**: Start a server, watch live progress in the browser (templ), see best vs ref images update.
* After **Phase 10**: Big CPU speedup on supported CPUs from SIMD SSD.
* After **Phase 11**: Optional GPU backend with drop-in switch.
