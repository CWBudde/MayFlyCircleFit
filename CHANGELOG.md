# Changelog

All notable changes will be documented here. This project follows the structure
of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); no stable public
release is declared by this file.

## [Unreleased]

### Added

- An exact float64 SSE2 span compositor for AMD64, the baseline-tier counterpart
  of the existing NEON one. It is byte-identical to the scalar span, so it is on
  by default with no flag and no reproducibility caveat. Measured on a host that
  genuinely lacks AVX2 rather than one masked with `GODEBUG=cpu.avx2=off`: about
  1.07x on the kernel and 1.06x end to end at 256x256 and larger, nothing below
  its 24-pixel cutoff. The span compositor is the largest symbol in every
  profile this repository has taken, and AMD64 previously had no vector span
  compositor at any tier. An AVX2 host still composites scalar. See
  [docs/task-10.19-sse2-compositor.md](docs/task-10.19-sse2-compositor.md).
- `run --fast-compositing` selects an opt-in float32 SIMD span compositor with
  SSE2 and AVX2 kernels. It is accurate to +/-1 per channel rather than
  byte-identical to the default float64 span, and defaults to off.
- An SSE2 SIMD tier for AMD64. Hand-written Plan 9 kernels for SSD
  (`ssd_sse2_amd64.s`, four NRGBA pixels per batch) and for the dirty-span
  delta-SSD of the incremental cost path give AMD64 hosts without AVX2 a real
  vector path instead of scalar execution. Both accumulate in int32 lanes and
  widen once at the end. Measured on a CPU that genuinely lacks AVX2: SSD is
  5.3x to 6.2x over scalar, delta-SSD 2.25x to 4.45x, and `BenchmarkFit` cost
  5.85x to 6.12x with whole pipelines 1.13x to 1.24x. See
  [docs/task-10.17-sse2-report.md](docs/task-10.17-sse2-report.md).
- A single resolved SIMD tier. `fit.Tier()` decides once which instruction set
  the process uses, and every kernel installs from it through
  `fit.RegisterTierConsumer` instead of reading `x/sys/cpu` itself. This
  replaces nine independent dispatch initializers that recorded their choice in
  four different ways and could disagree without anything noticing.
- `MAYFLY_SIMD_TIER=avx2|sse2|neon|scalar` pins the tier for every kernel on
  every architecture, and rejects an unreachable value with a panic rather than
  falling back. `MAYFLY_DISABLE_SIMD=1` is retained as its scalar alias, and is
  still needed because `golang.org/x/sys/cpu` marks sse2 as required on AMD64,
  so `GODEBUG=cpu.all=off` cannot reach the scalar path there.
- `MAYFLY_REQUIRE_SIMD_TIER` asserts the detected tier without setting one, in
  both `internal/fit` and `internal/fit/renderer`. It replaces
  `MAYFLY_REQUIRE_SSD_BACKEND`, which described a single kernel in a single
  package: a CI step could set it, run the renderer package, and assert nothing
  about the renderer.
- A cross-backend differential test. Every SSD kernel the host can execute is
  called directly and compared for exact equality, over batch boundaries,
  padded strides, non-zero start offsets, scrambled alpha, and a seeded random
  sweep. The per-backend tests it replaces each skipped unless the host had
  already selected that backend, so on an AVX2 development machine the SSE2 and
  NEON kernels had no correctness coverage at all.
- `run --parallel-evaluation` evaluates optimizer population members
  concurrently over independent renderer sessions, each with its own canvas.
  `--evaluation-workers` (job field `evaluationWorkers`) sets how many, clamped
  to `GOMAXPROCS` and defaulting to `--threads`. It is reproducible for a fixed
  seed and worker-count independent, but its trajectory differs from a serial
  run of the same seed. It defaults to off. Evaluation width trades against
  `--threads` rather than adding to it, and narrow pools are slower than the
  default; see [docs/parallel-evaluation-report.md](docs/parallel-evaluation-report.md)
  for measured scaling. Backends without independent sessions decline the
  request with a warning, and transactional polishing rejects a parallel
  optimizer outright because its sweep evaluator is not re-entrant.
- A `contiguous-window` polishing strategy. It selects a consecutive run of draw
  slots instead of scattering the active set by image-space merit, so the
  circles ahead of the window can be baked into a reusable canvas. Only the
  circles before the first active slot are bakeable, and the existing strategies
  routinely select circle one, which bakes nothing and rasterizes the whole
  vector for every candidate. Visit counts slide the window toward the front on
  later sweeps, covering every circle in `ceil(circles/activeSetSize)` sweeps.
  Per-candidate cost is `circles - windowStart` rather than always `circles`, so
  unlike the other strategies it does not grow with the whole circle count.
  It is not an unconditional improvement, and it is not the default. The
  isolated render-cost gain, measured on a Ryzen 5 4600H with `activeSetSize` 3
  and the optimizer stubbed out, is the first sweep of a coverage cycle:
  36.9 ms to 17.6 ms at 64 circles (2.1x) and 111.7 ms to 21.1 ms at 256
  circles (5.3x), falling to 1.44x averaged over a 13-sweep cycle. Measured
  against error actually removed, it lost to `hybrid-overlap` at equal wall
  clock in every configuration tried, and at the default
  `--polishing-max-sweeps` of 3 it only reaches the last `3 * activeSetSize`
  draw slots and removed no error at all. Raise `--polishing-max-sweeps` to at
  least `ceil(circles / activeSetSize)` before selecting it. See
  [docs/contiguous-window-polish-report.md](docs/contiguous-window-polish-report.md).
- Optimizer-level early stopping, disabled by default. `--stop-target-cost`,
  `--stop-stagnation-iters`, `--stop-min-improvement`, and `--stop-min-iters`
  (and their `stop*` job-configuration fields) apply per iteration inside a
  single optimizer run, in every mode. They are distinct from the existing
  stage-level `--patience`/`--threshold` convergence detection, which counts
  whole circles or batches and uses a relative ratio; the new minimum
  improvement is absolute. Leaving them unset keeps a run bit-identical to one
  configured before they existed.
- `run` gained `--variant` for selecting the MayFly algorithm variant.
- Structured MayFly lifecycle logging. Each optimizer run emits one info record
  carrying its measured work and termination reason; MayFly's per-iteration and
  run-start events are demoted to debug, so `--log-level=debug` now emits one
  record per optimizer iteration.
- Optimizer termination reasons propagate end to end. `opt.Termination` gains
  `target_cost` and `stagnation`, staged pipelines report `stage_convergence`
  when the stage-level tracker stops a run, and the reason is shown by `status`,
  the `checkpoints list` table, and the job detail page.

- Context-aware MayFly optimization with measured progress and cancellation.
- Seeded restart-from-best populations using MayFly `v0.4.0`.
- Trusted-local server controls for same-origin browser requests, canonical input
  roots, bounded admission, request/image limits, and opt-in loopback pprof.
- Portable scalar SSD/SAD dispatch for non-AMD64 targets and AVX2 runtime
  detection on AMD64.
- Release-gating CI for generation drift, formatting, vet, short and race tests,
  pinned static analysis, aggregate coverage, ordinary and GPU-tag builds,
  selected cross-builds, and vulnerability scanning.
- Support-matrix, known-limitations, contribution, and license documentation.
- Concurrent multi-job lifecycle stress coverage and atomic persistence fault
  injection for partial-write/rename recovery.
- End-to-end sequential and batch pipeline benchmarks with allocation reporting.
- A SemVer tag-driven release pipeline with CI dependencies, portable archives,
  build metadata, and SHA-256 manifests.

### Changed

- templ is pinned as a Go tool and generated UI Go files are committed.
- CPU joint, sequential, and batch pipelines preserve their supplied base canvas;
  staged OpenCL requests report an unsupported-mode error.
- The CPU renderer uses parity-tested `FastMSECost` by default.
- Batch optimization treats circle count as an exact total and uses a smaller
  final batch where necessary.
- Staged optimization keeps the best historical parameters when a later stage
  worsens the cost.
- Configuration, limits, and zero-seed resolution are centralized across entry
  points.
- Sequential and batch CPU stages evaluate only newly added circles over the
  retained canvas, reducing replay work and per-evaluation allocations.
- The SSE2 delta-SSD kernel now accumulates in int32 and widens once, matching
  the SSD kernel instead of the AVX2 delta kernel it was transliterated from.
  Worth 1.11x to 1.45x over spans of eight pixels and up, A/B in one binary on a
  Ryzen 5 4600H. Its Go wrapper splits spans longer than 8192 pixels so the
  narrower accumulator cannot overflow, which the previous width-capped design
  would have needed a scalar cliff for.
- `deltaSSDSpan` dispatches down the tier ladder rather than to a single width,
  so an AVX2 host now uses the SSE2 kernel for four-to-seven-pixel spans instead
  of dropping to scalar.
- The staged incremental cost path is enabled at the SSE2 tier. Its crossover
  constants model AVX2 measurements, so the extension was measured on a no-AVX2
  CPU rather than assumed; the curves match in shape and the SSE2 crossover sits
  slightly later, so the AVX2-tuned constants are conservative there.
- The AMD64 native SSD CI gate covers five states: native AVX2,
  `GODEBUG=cpu.all=off` asserting that feature masking demotes to SSE2, the SSE2
  tier pinned, the scalar tier pinned, and the legacy `MAYFLY_DISABLE_SIMD=1`
  alias. The AMD64 steps also run `./internal/fit/renderer`, which now honours
  the same assertion. ARM64 runners gained the pinned-scalar and legacy-alias
  steps; they still exclude the renderer package for the pre-existing reason in
  `docs/known-limitations.md`.

### Fixed

- Architecture-specific SIMD references no longer prevent non-AMD64 builds.
- SSD/MSE handling supports independent image origins and strides and defines
  empty-image and mismatched-dimension behavior.
- Job snapshots and progress updates no longer expose shared mutable state to
  callers.
- Completed server jobs report the optimizer's actual termination reason. The
  worker previously recorded `completed` for every finished job because the
  pipeline discarded the reason on the way out of the optimizer.
- `run` computes throughput from the measured evaluation count instead of an
  `iters * popSize` estimate, which overstated the work whenever a run stopped
  before its iteration budget.
- The configured MayFly `variant` is now applied. It was accepted, defaulted,
  validated, and persisted in checkpoints, but every optimizer construction site
  hardcoded the standard variant, so `desma` and `olce` silently ran standard.
  `run` also gained the `--variant` flag that makes the setting reachable from
  the CLI.

### Removed

- `circle_geometry_sse2_amd64.s`. The float32 circle-span kernel it added was
  unreachable in production: `circleSpanFloat32Selected` is gated on
  `CPURenderer.forceFloat32Geometry`, which no configuration path or CLI flag
  sets. Its test table was kept and retargeted at the AVX2 kernel, which had a
  weaker one.
- `CompareSSDImplementations`. It had no callers, compared only the installed
  kernel, and used a floating-point tolerance for kernels that reduce an integer
  sum exactly. The differential test replaces it.

### Known limitations

- OpenCL remains experimental and joint-only.
- Restart-from-best does not restore the full optimizer state, and server resume
  of sequential/batch jobs is unsupported.
- `--fast-compositing` changes the output of a fixed seed and is therefore
  opt-in and off by default.
- SAD and the Q16.16 circle-span kernel have no SSE2 port. Both need
  instructions above baseline SSE2, and neither carries enough measured cost to
  justify emulating them.
- `--parallel-evaluation` reproduces bit-identically for a fixed seed, but takes
  a different search trajectory from a serial run of that seed, so runs are only
  comparable to runs with the same setting. It is opt-in and off by default for
  that reason. Its speedup is workload-dependent and can be negative; measure
  before enabling it.
- Real-device GPU runtime, long-running end-to-end, per-package coverage, and
  performance-regression gates remain outside the required CI matrix. See
  [docs/known-limitations.md](docs/known-limitations.md).
