# Known limitations

MayFlyCircleFit is still completing its production-readiness remediation. The
items below are current operational constraints, not a promise that all other
behavior is production-ready.

## Security and deployment

- The HTTP server is trusted-local only. It provides neither authentication nor
  TLS and is not suitable for untrusted networks or multiple mutually untrusted
  users.
- Same-origin enforcement is a browser defense. Non-browser clients without an
  `Origin` header can call the API, as required by the CLI.
- Input roots constrain reference and canvas reads, but files inside those roots
  are visible to any client that can reach the trusted-local service and submit
  a job.
- pprof is opt-in and loopback-only, but profiling can still expose sensitive
  process information to other users able to reach the local listener.
- Jobs are held in memory. Checkpoints and artifacts are persisted, but the
  server is not a durable distributed queue and has no multi-process ownership
  protocol.

## Resume and persistence

- “Resume” is restart-from-best, not exact continuation. The saved best is put
  into a newly initialized MayFly v0.7.0 population along with deterministic
  perturbations. Velocity, mating state, RNG position, and other optimizer
  internals are not restored.
- A continuation seed is derived from the original seed and resume count, so a
  fixed checkpoint/configuration is reproducible, but its trajectory differs
  from an uninterrupted run.
- Server restart-from-best is supported for joint mode only. Sequential and
  batch checkpoint resume currently returns an error.
- Checkpoints are progress snapshots, not transactional snapshots of an entire
  process. Abrupt power loss and filesystem failures still require validation of
  available artifacts after restart.

## Rendering and optimization

- The MayFly initial-population sequence (`qmcInit`: `uniform`, `sobol`,
  `halton`) is an expert knob with no measurement on this problem. The library's
  own study finds a chance-level effect across sixteen benchmark problems — two
  significant results for Sobol, none against, against about 1.6 expected by
  chance, and an earlier run of the same study found two hits on *different*
  problems. Nothing here has been measured on circle fitting.

  The mechanism is also weakest where this project usually runs. What a
  low-discrepancy sample buys is even coverage of the search box, and that gap
  is largest when the population is small relative to the dimension; a `popSize`
  of 1024 over a 56-dimension batch stage is the opposite regime. Where it is
  worth trying is a small population on a short restart, which is where the
  library's evidence and the mechanism agree — not as a default, and not as a
  change to any existing campaign's settings.

  The mechanism is further weakened by how this project seeds its runs. The
  sequence does not initialize the whole population: wherever residual seeding
  succeeds — unconditionally for batch stages, and for cold joint and
  sequential base stages just as for continuations — the seed candidate already
  occupies half of the male and half of the female population, and only the
  remaining slots are drawn from the sequence. An experiment on `qmcInit` is
  therefore measuring a change to half a population, not to a whole one. See
  `docs/behavior-invariants.md` for the exact split.

  Setting it does not make a run irreproducible: the scramble comes from the
  run's seeded generator. It does make the run incomparable to the uniform run
  of the same seed, so a campaign has to pair strategies across seeds rather
  than assume a shared trajectory. See `docs/behavior-invariants.md`.

- The Dragonfly optimizer is a proof of concept. It is selected with
  `"optimizer": "dragonfly"` — the `run --optimizer` flag, the `optimizer` field
  of a job payload, or a schedule document's `base` — and an absent field means
  `mayfly`, which is what every configuration and checkpoint written before the
  field existed carries.

  The resume gap this entry used to record is closed: a checkpoint stores the
  engine in `config.optimizer` and records that engine's library version in
  `optimizerVersion`, and both the CLI and the server resume the engine the
  checkpoint names rather than the one a flag asks for. A resumed run can no
  longer silently change optimizer.

  What Dragonfly still does not support: `variant`, `crossoverCount`,
  `danceDamp`, `aquilaWeight`, `oppositionProbability`, and polishing. Setting
  any of them alongside it is refused — a usage error from the CLI, an
  `invalid_config` envelope from the API, and a parse failure for a schedule
  whose steps include a `polish` — rather than accepted and ignored.
  `parallelEvaluation` and `evaluationWorkers` are supported and behave as they
  do for MayFly: reproducible for a fixed seed, and measured identical to a
  serial run of that seed.
  `TestDragonflyParallelEvaluationMatchesSerial` pins it separately from
  MayFly's, because this is a separate library on a separate pin.

  Its published behavior is to explore well and exploit poorly -- the
  convergence factor reaches zero at the halfway point of a run -- so a worse
  fit than MayFly is the expected outcome. That outcome is now measured: on the
  eight-circle base stage Dragonfly loses every one of twelve blocks, by 431.68
  (`t = -16.81`) even when handed more evaluations than the MayFly baseline. See
  [`docs/dragonfly-poc-report.md`](dragonfly-poc-report.md). It is kept as an
  expert-only alternative for further experiments, not as a candidate default.

- CPU and OpenCL support joint, sequential, and batch pipelines; only CPU
  supports custom base canvases. Staged OpenCL modes replay all retained circles
  in independent device sessions, so their performance remains uncharacterized
  on vendor GPUs.
- OpenCL is experimental, CGO-dependent, and requires local headers, a loader,
  a driver, and a usable device. It is excluded from the standard portable
  matrix; a dedicated CI job exercises it through the PoCL CPU runtime.
- The experimental OpenCL renderer contains a CPU compatibility degradation path
  for runtime rendering/cost errors. Inspect warning logs and device-specific
  parity tests when GPU execution matters.
- ARM64 uses NEON for SSD when ASIMD is available, with a scalar fallback. SAD
  remains scalar because it has no native ARM64 kernel.
- AMD64 dispatch is tiered: AVX2 when the CPU reports it, otherwise SSE2,
  otherwise scalar. The tier is resolved once, by `fit.Tier()`, and every kernel
  installs from it. A kernel may be narrower than the tier where it has no
  implementation, and the cases below are the whole list; it is never wider.
- SAD has no SSE2 kernel, so it stays scalar on AMD64 hosts without AVX2.
  `FastSAD` has no non-test callers, and its AVX2 kernel uses `VPMADDUBSW`
  (SSSE3) and `VPMULLD` (SSE4.1), neither of which baseline SSE2 provides.
  Porting it would mean adding SSSE3/SSE4.1 tiers for an unused cost function.
- The SSE2 span compositor returns far less than its share of the profile
  suggests: about 1.07x, so roughly 1.06x end to end on the larger canvases and
  nothing below its 24-pixel cutoff. Half its instructions are format
  conversion, and SSE2 has neither `PMOVZXBD` nor `PSHUFB` to shorten that. The
  AVX2 kernel at the same job is worth 1.09x to 1.43x.
- The per-span constant block is rebuilt for every span of every row, though the
  colour is constant for a whole circle. That setup is the entire difference
  between the SSE2 kernel's 8-pixel crossover measured directly and the 24-pixel
  cutoff the dispatcher has to use, and it is why the AVX2 cutoff is 16 rather
  than around 4.
- Both exact vector compositors depend on the Go compiler's multiply-add
  contraction behaviour, in opposite directions on the two architectures. This
  is a real coupling to the toolchain, not a stylistic preference; a Go release
  that changed it would break byte parity on one of them.
- The SSE2 SSD kernel accumulates a row in int32 lanes, so it accepts rows up to
  11000 pixels wide and hands wider rows to the scalar kernel. That bound is far
  above any canvas size this program produces, but it is a real limit. The SSE2
  delta-SSD kernel uses the same accumulator strategy but its wrapper splits
  long spans instead, so it has no equivalent cliff.
- `MAYFLY_SIMD_TIER` pins a tier and rejects an unreachable one with a panic at
  initialization. That is deliberate: quietly substituting the detected tier
  would let a CI gate asking for SSE2 pass while measuring AVX2.
- `GODEBUG=cpu.all=off` does not produce scalar execution on AMD64.
  `golang.org/x/sys/cpu` marks sse2 as required on that architecture and ORs the
  requirement back in, so detection lands on SSE2. Use `MAYFLY_SIMD_TIER=scalar`
  (or its alias `MAYFLY_DISABLE_SIMD=1`) to force the scalar tier on every
  kernel and architecture.
- Circle-span geometry stays scalar without AVX2, in both of its forms. The
  Q16.16 vector form compares Q32.32 products with `VPCMPGTQ`, SSE2 has no
  64-bit signed compare, and a measured no-AVX2 profile attributes only 2.80% of
  flat samples to that span, so emulating the compare was not worth its cost.
  The float32 form is reachable only through `CPURenderer.forceFloat32Geometry`,
  which no configuration path sets, so it has no production effect at any tier.
- `--fast-compositing` is not byte-exact. It computes the opaque span blend in
  float32 with a regrouped multiply-add, which is accurate to +/-1 per channel
  against the default compositor - swept over every byte value against 2010
  colours, where 0.001% of channel writes reached the bound. It is off by
  default, and a run that enables it is not comparable to a run without it:
  a changed channel changes the SSD, so the optimizer takes a different
  trajectory and converges to a different circle set, not merely a slightly
  different picture. Its speed impact is measured; its quality impact is not.
- Below 16 pixels `--fast-compositing` is slower than the exact compositor as
  well as less accurate, because its scalar fallback is slower than the exact
  float64 loop. Circle edges are exactly where short spans occur. The
  measurements behind the cutoffs are in
  `docs/task-10.18-exact-compositor.md`.
- Non-AMD64 targets have no float32 kernel, so `--fast-compositing` there is a
  pure loss. Startup warns rather than failing, because a checkpoint written on
  AMD64 can legitimately be resumed elsewhere.
- The production CPU renderer uses `FastMSECost`; parity tests cover the scalar
  reference, SIMD dispatch, independent origins/strides, empty images, and
  dimension mismatches. This remains a numeric RGB objective, not a perceptual
  quality metric.
- The image objective compares RGB values and ignores alpha. It is a numeric
  pixel error, not a perceptual color-space metric.
- Circle compositing is a raster approximation without an antialiasing quality
  guarantee. Results can differ from vector renderers.
- On ARM64, the historical pre-optimization renderer oracle differs from the
  current renderer by one alpha unit for one covered translucent custom-canvas
  pixel. Current single-threaded and parallel rendering agree, and the issue
  predates the opaque-canvas fast path. Cross-architecture byte identity for
  this floating-point rounding boundary is not claimed.
- Evolutionary optimization is expensive and does not guarantee a global
  optimum. Seed, population, iteration count, mode, and image size materially
  affect runtime and quality.
- Optimizer-level early stopping (`--stop-*`) applies per stage in sequential
  and batch modes. A run can stop early in many stages and still execute every
  stage, in which case the reported termination is `completed` and the count
  appears in the `stages_stopped_early` log field. Only the stage-level tracker
  reports `stage_convergence`.
- Early stopping changes which solution a given seed produces. It is disabled by
  default for that reason; a run that enables it is reproducible for a fixed
  configuration but is not comparable to a run without it.
- `--log-level=debug` now emits one MayFly record per optimizer iteration.
  Long runs produce correspondingly large logs.
- MayFly's `EnableParallel`/`MaxWorkers` evaluation parallelism is opt-in
  through `--parallel-evaluation` and the `parallelEvaluation` job field, and is
  off by default. The pipeline then leases one independent renderer session per
  concurrent evaluation, each with its own canvas and its own single rendering
  thread, so `CPURenderer.Render` never composites into a shared canvas. The
  default still evaluates serially over one session. `resume` takes the same
  leased-session path through `renderer.NewConcurrentEvaluator`.
- The evaluation width is set by `--evaluation-workers` and clamped to
  `GOMAXPROCS`, matching the documented `--threads` contract. It defaults to
  `--threads` when unset, which is what it was before it had its own flag. Each
  worker above one holds an extra full-size canvas and background copy, so an
  unclamped request would be an out-of-memory hazard rather than extra
  throughput. A measured twelve-worker pool at 512x512 holds 2.19x the memory of
  the serial default.
- Evaluation width and `--threads` are competing uses of the same cores, not
  additive ones: a pooled session renders single-threaded, so raising evaluation
  width gives up row sharding. Measured on a 12-core Ryzen 5 4600H, the flag is
  worth 2.34x at 128x128 but only 1.18x at 512x512, and configurations below
  about four workers are *slower* than the default -- 0.61x at 512x512 with two
  workers. See [parallel-evaluation-report.md](parallel-evaluation-report.md).
  Do not assume the flag is a speedup without measuring the intended workload.
- Backends that do not advertise safe concurrent evaluation -- OpenCL today --
  cannot serve parallel evaluation. OpenCL can create independent staged
  sessions, but simultaneous device evaluation has not been validated. A
  request is declined with a warning and the run evaluates serially, rather
  than paying for an altered search trajectory without a validated throughput
  gain.
- Parallel evaluation is reproducible *and*, since the MayFly v0.7.0 pin,
  equivalent to a serial run of the same seed. Evaluation order does not leak
  into the result: MayFly advances its RNG only from serial phase code and
  breaks ties in a parallel batch by population index, so a seed reproduces
  bit-identically and the result does not depend on the worker count. v0.7.0
  additionally gave the serial and parallel modes the same proposal and commit
  semantics, so the two now walk one trajectory; this is measured for all seven
  variants and for Dragonfly, and pinned by
  `TestParallelEvaluationMatchesSerial`. Runs recorded under v0.6.0 and earlier
  predate that and stay comparable only against runs with the same setting,
  because the older serial loop steered a generation from its own mid-generation
  best while the parallel loop did not.
- Transactional polishing (`--polishing`) uses the configured parallel-
  evaluation width on a backend that provides and advertises independent
  concurrent sessions. Every slot owns its renderer and scratch vector; only
  the final merged-candidate check and commit remain serial. A backend without
  that capability is still allowed for a serial optimizer, but polishing
  refuses a concurrent optimizer rather than sharing mutable state or silently
  reducing its width. The measured speedup, pool memory cost, and break-even
  point are in
  [`polishing-throughput-report.md`](polishing-throughput-report.md).
- Transactional polishing can still be a no-op that spends its whole optimizer
  budget, though no longer for the reason recorded here before. A sweep is
  committed only when it improves the cost and keeps every circle in its active
  set useful without adding a non-useful circle outside it
  (`sweepKeepsCirclesUseful`); a circle the sweep never touched and never made
  worse no longer blocks it. What remains is that a sweep which finds nothing, or
  whose candidate fails the gate, contributes nothing while costing its full
  budget. Check the accepted-sweep count in the polishing log record before
  concluding that a strategy or a sweep budget was at fault.
- A polishing budget is not monotone in quality. Measured in
  [`polishing-budget-report.md`](polishing-budget-report.md), a larger population can reach a *worse*
  final cost than a smaller one at several times the wall clock, because
  acceptance is discrete: a sweep either clears the gate or contributes nothing,
  and a different search trajectory lands on a different side of it. Raise
  `--polishing-max-sweeps` before raising `--polishing-pop` or
  `--polishing-iters`; sweeps are the only axis that moves polishing onto
  different circles.
- `--polishing-strategy=contiguous-window` is cheaper per sweep, not better per
  second. It only offers the last `maxSweeps * activeSetSize` draw slots to the
  optimizer, so at the default sweep budget it never sees the front of the draw
  order on a large vector, and at equal wall clock it
  reached a worse cost than `hybrid-overlap` in every configuration measured in
  `docs/contiguous-window-polish-report.md`. Raise the sweep budget to at least
  `ceil(circles / activeSetSize)` before selecting it.
- Not every MayFly phase runs parallel even when the flag is on: GSASMA's
  opposition-based learning on the global best has no parallel branch upstream.
  For the AOBLMOA variant, enabling parallel evaluation also changes the
  reported evaluation total, because MayFly's serial path estimates that count
  while its parallel path reports the exact one. Both variants are selectable
  through `JobConfig`, so this affects any caller, not only direct
  `internal/opt` ones.
- MayFly's constraint handling and convergence-curve CSV/JSON export are unused.
  The problem is box-bounded, and trace ownership belongs to the store package.

## CI and release status

- Go 1.24 is the source-compatibility floor, not a promise that an old Go 1.24
  patch is safe for production. Build production binaries with a currently
  supported, security-patched Go release; vulnerability CI currently uses Go
  1.26.6.
- The standard gates cover generation drift, formatting, vet, pinned
  staticcheck, short tests, race short tests, a 50% aggregate coverage floor and
  artifact, ordinary builds, selected CGO-disabled cross-builds, an
  OpenCL/PoCL GPU-tag compile and focused runtime suite, and pinned
  `govulncheck`.
- A dedicated opt-in E2E gate builds the CLI and exercises the real server
  process through create, SSE progress, checkpoint, cancellation, restart,
  resume, and artifact retrieval. Run it locally with `just test-e2e`; it is
  intentionally separate from the short suite.
- Real-device GPU tests, per-package coverage thresholds, broader load and
  fault-injection lifecycle tests, vendor-driver coverage, and performance
  regression thresholds are not required gates yet. PoCL CI results do not
  establish actual-GPU performance.
- Cross-compilation proves that packages compile for a target; it does not test
  runtime behavior on that operating system or architecture.
- `internal/fit/renderer` has no ARM64 CI coverage. The `native-simd` matrix
  supplies the only ARM64 runners in the repository and it runs `./internal/fit`
  alone; every other job is `ubuntu-latest`. Running the renderer package on the
  Linux ARM64 and macOS ARM64 runners was tried and fails:

      --- FAIL: TestCPURendererMatchesPreOptimizationBaseline/fractional_overlaps
          renderer_correctness_test.go:106: pixel (4,11) channel 3 = 205, baseline = 206

  It reproduces at `threads_1` and `threads_4` and does not reproduce on amd64.
  The case is a 31x23 custom canvas, which is well below the 256-pixel NEON span
  cutoff, and channel 3 is alpha, which the span compositor never writes, so the
  gated NEON span kernel is unlikely to be the cause. The defect predates the
  SSE2 work and needs real ARM64 hardware to diagnose; it is not reproducible by
  cross-compiling. Until it is fixed, ARM64 renderer output is unverified.
- The existence of a workflow is not evidence that a revision passed it. Use
  the workflow result and Phase 14 acceptance checks for release decisions.
- Valid SemVer tags run the complete required matrix before the repository's
  automated release job can publish portable CPU archives. This dependency gate
  does not prevent a sufficiently privileged GitHub user from creating a tag or
  release manually; repository roles and rules remain an administrative release
  requirement.
- Release archives are not signed and do not yet include provenance attestations
  or an SBOM. The published SHA-256 manifest detects accidental or post-download
  corruption but is not a substitute for signature verification.

Track remaining work in the active
[Phase 14 release gate](../PLAN.md#phase-14-production-readiness-remediation--release-gate).
