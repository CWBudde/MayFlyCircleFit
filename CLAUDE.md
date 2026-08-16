# MayFlyCircleFit development context

This file is concise repository context for coding assistants. The observable
code and tests take precedence if this document becomes stale.

## Toolchain and generated files

- The module has a Go 1.24 source-compatibility floor (`go 1.24.0` in
  `go.mod`). Production binaries should use a currently supported,
  security-patched Go release; vulnerability CI is pinned to Go 1.26.6.
- MayFly is pinned to `github.com/cwbudde/mayfly v0.4.0`.
- templ is pinned as a Go tool at `github.com/a-h/templ v0.3.960`.
- `internal/ui/*_templ.go` files are generated and committed. After changing a
  `.templ` source, run `go tool templ generate` and include the corresponding
  generated change.
- Prefer the `just` recipes. `just check` covers generation drift, tests, vet,
  formatting, and the ordinary build; CI adds race, pinned static analysis,
  aggregate coverage, vulnerability, GPU-compile, and cross-build jobs.
- CI is split one gate per file. `.github/workflows/ci.yml` is an orchestrator
  that only calls reusable `ci-<concern>.yml` workflows and holds the tag-gated
  `release` job; edit the gate's own file, not `ci.yml`. `needs:` cannot cross
  workflow files, so a gate becomes release-blocking only by being listed in
  `release`'s `needs:`. `benchmarks` is deliberately excluded there because the
  timing comparison is report-only. Reusable gates report as
  `<caller job> / <job name>`, for example `generation / Generated UI is
  current`, so anything that names a check by string must use the prefixed form.
  See `docs/releasing.md`.
- Phase 9 CPU measurements on the Ryzen 5 4600H show a 2.09-2.47×
  single-thread renderer speedup, zero timed allocations after canvas reuse,
  and a 6.39× median large-workload gain when Task 9.7 uses 12 workers. See
  `docs/task-9.9-performance-report.md`; do not compare those absolute timings
  across machines.
- `github.com/google/pprof` is pinned as a Go tool because some Go
  installations do not bundle it. Use `go tool pprof` for profile analysis.

Do not state that a release gate passed unless its command or CI result was
actually observed for the revision being discussed.

## Architecture

- `cmd`: Cobra commands (`run`, `serve`, `status`, `resume`, `checkpoints`,
  `version`).
- `internal/app`: canonical typed configuration, defaults, limits, and seed
  resolution shared by CLI, server, and persistence.
- `internal/fit`: image costs and architecture-specific SIMD dispatch.
- `internal/fit/renderer`: CPU/OpenCL renderers and joint/sequential/batch
  pipelines.
- `internal/opt`: optimizer interfaces and the MayFly v0.4.0 adapter.
- `internal/server`: trusted-local HTTP boundary and background job lifecycle.
- `internal/store`: filesystem checkpoint, trace, and artifact ownership.
- `internal/ui`: templ views plus committed generated Go output.

Keep dependencies flowing toward these lower-level packages; do not reintroduce
application configuration into the store package.

### SIMD SSD architecture

- `ssd_amd64.s` processes eight NRGBA pixels per AVX2 batch;
  `ssd_arm64.s` processes four per NEON batch. Both ignore alpha, reduce into a
  64-bit total, and leave non-multiple widths to an exact scalar tail.
- The assembly is hand-written in Go Plan 9 syntax. The implemented workflow
  does not use GoAT, C sources, cgo, or an external assembler.
- Architecture-specific initialization checks `x/sys/cpu` once and installs an
  AVX2, NEON, or scalar function pointer. Do not call an assembly kernel without
  passing through feature-gated dispatch.
- `just cross-build` verifies the selected source set and compiles the CLI and
  `internal/fit` test binary for every supported CPU target with
  `CGO_ENABLED=0`.
- Use `MAYFLY_REQUIRE_SSD_BACKEND` in native hardware validation and
  `GODEBUG=cpu.all=off` to exercise a complete scalar fallback. Kernel
  benchmarks must retain their result through `ssdBenchmarkSink` and report
  allocations.

### CPU span compositing

- Opaque canvases use horizontal span compositing; the scalar span hoists
  foreground and blend invariants out of the per-pixel loop.
- ARM64 includes an exact float64, eight-pixel NEON span kernel. Runtime ASIMD
  detection and a measured 256-pixel cutoff guard it; shorter spans and tails
  use scalar because that is faster on Apple M5.
- Translucent custom canvases retain the general per-pixel Porter-Duff path.
  Preserve that split and byte-exact span tests when changing renderer math.

## Current behavior that must remain explicit

- CPU supports joint, sequential, and batch modes, including a supplied base
  canvas.
- OpenCL is experimental and supports joint mode only. It requires a `gpu`
  build tag, CGO, OpenCL development headers, and a usable runtime/device.
  Sequential and batch OpenCL requests must fail explicitly, not silently use a
  CPU staged renderer.
- AMD64 SSD dispatch uses AVX2 only after a runtime CPU-feature check; ARM64 SSD
  dispatch similarly requires ASIMD before selecting NEON. Unsupported CPUs and
  architectures use the scalar kernel. SAD remains scalar on ARM64.
- CPU renderers use `FastMSECost` after parity coverage against `MSECost`.
  Independent image origins and strides, empty images, and dimension mismatch
  behavior have dedicated correctness handling/tests.
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
  `docs/parallel-evaluation-report.md`: 2.34x at 128x128 but only 1.18x at
  512x512, and below roughly four workers the flag is slower than the default.
  Do not describe it as an unconditional speedup. Each worker above one costs a
  full canvas and background copy, so the pool is clamped to GOMAXPROCS.
- `renderer.ParallelEvaluationOption` is the only place allowed to decide
  whether the optimizer runs its parallel path, and it decides from the
  renderer's reported width, never from a requested configuration value. A
  backend without independent sessions (OpenCL) must decline with a warning;
  enabling the optimizer's parallel path against a one-slot pool buys nothing
  and still changes the trajectory.
- Transactional polishing is the one optimizer-driven objective here that is not
  re-entrant: its sweep evaluator merges into a shared candidate vector and a
  shared session. `PolishCircleBatchContext` rejects an optimizer reporting a
  width above one, and the epoch and progress wrappers forward that width so the
  guard cannot be bypassed. Do not wire parallel evaluation into a polisher
  without first giving polishing its own session pool.
- Resume is restart-from-best: the MayFly v0.4.0 population is seeded with the
  saved best and deterministic nearby variations. It is not an exact restoration
  of optimizer internals. Server restart-from-best for sequential and batch jobs
  is not supported.
- A zero user seed generates and reports an effective seed; a nonzero seed is
  deterministic.
- Two distinct early-stopping mechanisms exist and must not be conflated.
  Stage-level convergence (`--patience`, `--threshold`, `Convergence*` config)
  counts whole circles or batches, uses a relative improvement ratio, and
  applies to sequential and batch only; `OptimizeJointContext` discards it.
  Optimizer-level stopping (`--stop-*`, `Stop*` config) is evaluated per
  iteration inside one optimizer run, uses an absolute improvement, and applies
  in every mode. Optimizer-level stopping is off by default and `ApplyDefaults`
  must never fill those fields in, because a default run has to stay
  reproducible.
- In sequential and batch modes, optimizer-level stopping applies per stage. A
  run can stop early in many stages and still execute all of them; the reported
  termination is then `completed`, with the count in `stages_stopped_early`.
  Only the stage-level tracker reports `stage_convergence`.
- Optimizer termination reasons propagate from the adapter through the pipeline
  to jobs, checkpoints, `status`, and `checkpoints list`. The checkpoint
  `termination` field is free-form, so new reasons need no schema bump, and
  readers reject a version above 2.
- Batch polishing bakes only the circles before the first active draw slot into
  a reusable canvas, so per-candidate cost is `circles - min(activeSet)`. The
  `replacement`, `hybrid-overlap`, and `residual-region` strategies select by
  image-space merit and routinely include circle one, which bakes nothing and
  rasterizes the whole vector for every candidate. `contiguous-window` selects a
  consecutive run instead, keeping that cost near `activeSetSize` on the first
  sweep. Do not change a selector to scatter its active set without accounting
  for that cost.
- `contiguous-window` is cheaper per sweep but not better per second, and it is
  not the default. Measured in `docs/contiguous-window-polish-report.md`: the
  2.1x/5.3x render-cost figures describe the first sweep of a coverage cycle
  with the optimizer stubbed out and fall to 1.44x over a full cycle, and at
  equal wall clock the strategy reached a worse cost than `hybrid-overlap` in
  every configuration measured. At the default three sweeps it only offers the
  last `3 * activeSetSize` draw slots to the optimizer. Do not describe it as an
  end-to-end speedup.
- A polishing sweep is committed only when `allCirclesUseful` holds for the
  whole candidate vector, so one circle with a negative `MSEContribution`
  blocks every sweep until an active set repairs it. Fitted vectors routinely
  contain such circles, because `PruneCircleBatch` runs per stage against that
  stage's canvas while this gate is global over the final vector, and nothing
  re-audits the assembled result. Polishing a real batch fit was therefore a
  complete no-op for three of the four strategies in the measurements above,
  and it still spends the full optimizer budget before the gate is consulted.
  This is pre-existing behavior, not a property of any one strategy; do not
  attribute a zero-improvement polishing run to the selector without checking
  `accepted_sweeps` first.
- The configured `variant` is honored at every optimizer construction site.
- MayFly's `optimization_started` and `iteration_completed` events are demoted
  to debug, so `--log-level=debug` emits one record per optimizer iteration.
  Info level stays at one record per optimizer run.
- The post-Task-10.12 M5 profile assigns 65.01% of flat samples to the scalar
  span compositor, 26.47% to scanline traversal, and 1.95% to gated NEON.
  Keep further rendering work profile-guided and pixel-equivalent.

## Server trust boundary

`serve` is trusted-local software, not a network service for hostile clients.
It has no authentication or TLS. The default bind is `localhost`; foreign
browser origins are rejected; input image paths must resolve beneath configured
`--input-root` directories; request/image sizes and the job queue are bounded.
pprof is off by default and `--enable-pprof` requires a loopback bind.

These controls do not make the server multi-user or internet-ready. Do not add
documentation suggesting otherwise.

## Verification

For focused changes, run the narrowest relevant package test first. Before
handoff, use the applicable subset of:

```sh
go tool templ generate
git diff --exit-code -- 'internal/ui/*_templ.go'
test -z "$(gofmt -s -l .)"
go vet ./...
go test -short ./...
go test -race -short ./...
go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
```

GPU checks require an explicitly prepared runner with OpenCL headers/runtime;
do not treat a CGO-disabled portable build as GPU validation.

Read `AGENTS.md`, `docs/support-matrix.md`, `docs/known-limitations.md`, and the
active Phase 14 section of `PLAN.md` before broad changes.
