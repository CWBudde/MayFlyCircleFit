# Changelog

All notable changes will be documented here. This project follows the structure
of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); no stable public
release is declared by this file.

## [Unreleased]

### Added

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
- Real-device GPU runtime, long-running end-to-end, per-package coverage, and
  performance-regression gates remain outside the required CI matrix. See
  [docs/known-limitations.md](docs/known-limitations.md).
