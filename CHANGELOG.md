# Changelog

All notable changes will be documented here. This project follows the structure
of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); no stable public
release is declared by this file.

## [Unreleased]

### Added

- An SSE2 SIMD tier for AMD64. Hand-written Plan 9 kernels for SSD
  (`ssd_sse2_amd64.s`, four NRGBA pixels per batch), dirty-span delta-SSD, and
  the float32 circle-span edge search give AMD64 hosts without AVX2 a real
  vector path instead of scalar execution. SSD dispatch is now tiered AVX2, then
  SSE2, then scalar, each behind a runtime `x/sys/cpu` check. On a real no-AVX2
  target the same 32-circle batch workload dropped from 300.81 s to 150.52 s at
  an identical final cost.
- `MAYFLY_DISABLE_SIMD=1` forces the scalar backend for every kernel on every
  architecture. It is needed because `golang.org/x/sys/cpu` marks sse2 as
  required on AMD64, so `GODEBUG=cpu.all=off` cannot reach the scalar path
  there any more.
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
- The staged incremental cost path is gated on any vectorized delta-SSD kernel
  rather than on AVX2 alone, so AMD64 hosts without AVX2 now use it instead of
  falling back to full-image evaluation.
- The AMD64 native SSD CI gate covers four dispatch states: native AVX2,
  `GODEBUG=cpu.avx2=off` for SSE2, `GODEBUG=cpu.all=off` which also selects
  SSE2, and `MAYFLY_DISABLE_SIMD=1` for scalar. The forced-scalar step
  previously used `GODEBUG=cpu.all=off`, which no longer selects scalar on
  AMD64. ARM64 runners gained the `MAYFLY_DISABLE_SIMD=1` scalar step as well.

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

### Known limitations

- OpenCL remains experimental and joint-only.
- Restart-from-best does not restore the full optimizer state, and server resume
  of sequential/batch jobs is unsupported.
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
