# Changelog

All notable changes will be documented here. This project follows the structure
of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); no stable public
release is declared by this file.

## [Unreleased]

### Added

- An offline preliminary collector and an explicitly non-inferential report for
  the stopped CMA-ES measurement campaign. The preserved subset contains three
  completed jobs and one interrupted IPOP job from the first paired block; the
  remaining multi-day queue was not run.
- Opt-in `enableOptimizerDiagnostics` trace samples. Mayfly jobs record RMS
  pairwise population spread in normalized optimizer space; CMA-ES jobs record
  sigma and covariance condition number at the same iteration boundary as
  cost. The Phase 11 campaign driver under `scripts/cmaes-measurement` submits
  the evaluation-matched five-arm design and produces its paired statistics
  and downsampled mechanism trajectories.
- A pinned CMA-ES adapter at `internal/opt`, with normalized mixed-range
  parameters, repair and nonlinear-constraint mapping, restart-from-best and
  alternative seeds, measured progress, epoch callbacks, early stopping,
  cancellation, and serial/parallel bit equivalence. `optimizer: "cmaes"` now
  selects it from CLI, JSON jobs, schedules, checkpoints, resume, and the web
  creation form. CMA-ES jobs expose normalized `initialSigma`, full, separable,
  or seven-parameter block covariance, `activeCMA`, and shared-budget IPOP/BIPOP
  through `restartStrategy`. IPOP/BIPOP and the consumer's fixed-attempt
  `optimizerRestarts` wrapper are mutually exclusive so restart mechanisms never
  multiply work silently.
- A `convergence` termination reason. CMA-ES stops on its own distribution-aware
  criteria (TolX, TolFun, TolXUp, condition number, no-effect axis and
  coordinate) well below the iteration cap, and reporting those as `completed`
  would have contradicted that value's promise that the budget was consumed and
  would have kept such stages out of `stages_stopped_early`.
- `run --polishing-pop` and the `polishingPopSize` configuration field give
  polishing a population of its own. It used to borrow the job-wide `popSize`,
  which is sized for the whole vector, so a 512-circle run optimized an
  eight-circle active set of 56 parameters with a population of 200. Measured on
  a 64-circle fitted vector, that population costs 5.2x the wall clock of 30 for
  1.28x the error removed, and on `replacement` a larger population reached a
  *worse* final cost than a fifth of it. `popSize` on a `/polish` request and on
  a schedule's polish step now addresses this field, which is what those
  overrides always meant. See
  [docs/polishing-budget-report.md](docs/polishing-budget-report.md).
- Polishing benchmarks for the budget itself: `BenchmarkPolishBudgetShape`,
  `BenchmarkPolishBudgetSweepFalloff`,
  `BenchmarkPolishBudgetShippedConfiguration`, and
  `BenchmarkPolishBudgetProductionShape` report cost removed per second beside
  the quality metrics, and are the measurement the polishing defaults are now
  set from.
- The polishing completion log record and the job detail view report the
  population a sweep ran at, beside the evaluation width.

- Exact float64 AVX2 and SSE2 span compositors for AMD64, the counterparts of
  the existing NEON one. Both are byte-identical to the scalar span, so both are
  on by default with no flag and no reproducibility caveat. AVX2 renders 1.09x
  to 1.43x faster and whole pipelines 1.05x to 1.14x; SSE2, measured on a host
  that genuinely lacks AVX2 rather than one masked with `GODEBUG=cpu.avx2=off`,
  is about 1.07x on the kernel and 1.06x end to end at 256x256 and larger, and
  nothing below its 24-pixel cutoff. AMD64 previously had no vector span
  compositor at any tier, while the span compositor is the largest symbol in
  every profile this repository has taken. See
  [docs/exact-span-compositors.md](docs/exact-span-compositors.md).
- `run --fast-compositing` selects an opt-in float32 SIMD span compositor with
  SSE2 and AVX2 kernels. It is accurate to +/-1 per channel rather than
  byte-identical, and defaults to off. It is kept now that an exact vector
  compositor exists because it is still 2.4x to 4.2x faster than that at
  realistic span lengths - the comparison that decides its fate, rather than the
  comparison against the scalar loop it was originally measured against.
- Startup logs the resolved SIMD tier and both installed compositors alongside
  the thread and evaluation-worker counts, and warns when `--fast-compositing`
  has no kernel on the host or is ignored by a non-CPU backend. The server job
  detail view shows the setting next to the seed. A run's log and its checkpoint
  are now enough to tell whether two runs are comparable.
- An SSE2 SIMD tier for AMD64. Hand-written Plan 9 kernels for SSD
  (`ssd_sse2_amd64.s`, four NRGBA pixels per batch) and for the dirty-span
  delta-SSD of the incremental cost path give AMD64 hosts without AVX2 a real
  vector path instead of scalar execution. Both accumulate in int32 lanes and
  widen once at the end. Measured on a CPU that genuinely lacks AVX2: SSD is
  5.3x to 6.2x over scalar, delta-SSD 2.25x to 4.45x, and `BenchmarkFit` cost
  5.85x to 6.12x with whole pipelines 1.13x to 1.24x. See
  [docs/exact-span-compositors.md](docs/exact-span-compositors.md).
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
  clock in every configuration tried, and it only reaches the last
  `maxSweeps * activeSetSize` draw slots, which at the three sweeps then in
  force removed no error at all. Raise `--polishing-max-sweeps` to at least
  `ceil(circles / activeSetSize)` before selecting it. See
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

- The polishing defaults are re-derived from a measurement rather than inherited
  from the batch configuration: `polishingIters` 1000 -> 200,
  `polishingMaxSweeps` 3 -> 8, `polishingStagnationIters` 500 -> 100 (half its
  epoch, as before), `polishingEpochs` unchanged at 2, and `polishingPopSize` 30
  instead of whatever `popSize` happened to be. Together they removed 2.6x the
  error of the previous defaults in 56% of the wall clock on `residual-region`,
  and 1.4x in 74% of it on `replacement`. A sweep is the only axis that
  re-selects which circles are optimized, which is why the budget moved onto it.
  Two consequences: a polish stage authorizes 3 200 optimizer iterations instead
  of 6 000, so the documented 512-circle campaign's planned figure drops from
  48 800 to 32 000; and a checkpoint written before `polishingPopSize` existed
  resumes at the polishing default rather than at its own `popSize`, so a fixed
  seed reproduces such a run only when the field is set explicitly. See
  [docs/polishing-budget-report.md](docs/polishing-budget-report.md).
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

- A batch run no longer silently spends two to four times the iterations its
  configuration asked for. Every batch stage is a full optimizer run at the
  configured iteration count, refills included, so a stage whose circles the
  post-stage audit pruned bought that slot back with a second complete budget:
  `iters: 2048` recorded 4096 iterations and twice the evaluations, as an
  ordinary `completed` job, with no resume and no second job directory. It fired
  more often the shorter the run, because a short run produces weaker circles —
  0% of the 2048-iteration jobs of one campaign against 21% of its 64-iteration
  jobs — which is what left that campaign's arms holding up to 22% more compute
  than the arm they were compared against. A batch that improves the image is
  now kept as the optimizer produced it, weak circles included, so pruning no
  longer opens a hole that has to be refilled; and a refill of a batch nothing
  can be kept from runs only while the run's own budget still covers it. A run
  that stops short reports it where it was always visible, in `actualCircles`
  and `refill_limit`.
- Active-set polishing no longer vetoes every sweep on an incrementally grown
  vector. The acceptance gate required every circle in the candidate to be
  useful, which also required a sweep to repair circles outside its active set —
  circles it copies through unchanged and has no agency over. Such circles are
  the steady state of a long run, because `PruneCircleBatch` runs per stage
  against that stage's canvas while later stages composite on top. On a real
  64-circle fit three occluded circles contributed -0.41, -0.18, and -0.07
  against a 0.01 threshold, and `residual-region` with `activeSetSize 8`
  reseeds one circle per sweep, so the gate was impossible to satisfy: 12 of 12
  sweeps rejected for a net cost gain of 0.00, twice over. The gate is now a
  non-regression rule — every circle in the active set must be useful, and the
  set of non-useful circles outside it may not grow — which agrees exactly with
  the old rule on a vector carrying no pre-existing blockers. Rejected sweeps
  now log why, naming the blocking circles, and the incumbent's audit is cached
  across sweeps instead of recomputed per consumer.
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
  opt-in and off by default. Its +/-1 bound is now swept over every byte value
  against 2010 colours rather than asserted at five points; its speed impact is
  measured and its quality impact is not.
- There is still no exact SSE2 span compositor and no float32 NEON kernel, so a
  no-AVX2 AMD64 host composites scalar and `--fast-compositing` is a pure loss
  outside AMD64.
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
