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
  into a newly initialized MayFly v0.4.0 population along with deterministic
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
- AMD64 SSD dispatch is tiered: AVX2 when the CPU reports it, otherwise SSE2,
  otherwise scalar. Each tier is chosen only after a runtime feature check.
- SAD has no SSE2 kernel, so it stays scalar on AMD64 hosts without AVX2.
  `FastSAD` has no non-test callers, and its AVX2 kernel uses `VPMADDUBSW`
  (SSSE3) and `VPMULLD` (SSE4.1), neither of which baseline SSE2 provides.
  Porting it would mean adding SSSE3/SSE4.1 tiers for an unused cost function.
- The SSE2 SSD kernel accumulates a row in int32 lanes, so it accepts rows up to
  11000 pixels wide and hands wider rows to the scalar kernel. That bound is far
  above any canvas size this program produces, but it is a real limit.
- `GODEBUG=cpu.all=off` does not produce scalar execution on AMD64.
  `golang.org/x/sys/cpu` marks sse2 as required on that architecture and ORs the
  requirement back in, so dispatch selects SSE2. Use `MAYFLY_DISABLE_SIMD=1` to
  force the scalar backend on every kernel and architecture.
- The Q16.16 circle-span kernel stays scalar without AVX2. Its vector form
  compares Q32.32 products with `VPCMPGTQ`, and SSE2 has no 64-bit signed
  compare; a measured no-AVX2 profile attributes only 2.80% of flat samples to
  that span, so emulating the compare was not worth its cost.
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
- MayFly's `EnableParallel`/`MaxWorkers` evaluation parallelism is deliberately
  unused. `CPURenderer.Render` composites into one reusable canvas, so
  concurrent cost evaluations would interleave, and CPU rendering already
  parallelizes across row bands. Enabling it would require a concurrency-safe
  cost path and has not been shown to pay for itself.
- MayFly's constraint handling and convergence-curve CSV/JSON export are unused.
  The problem is box-bounded, and trace ownership belongs to the store package.

## CI and release status

- Go 1.24 is the source-compatibility floor, not a promise that an old Go 1.24
  patch is safe for production. Build production binaries with a currently
  supported, security-patched Go release; vulnerability CI currently uses Go
  1.26.5.
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

Track remaining work in the active [Phase 14 plan](../PLAN.md#phase-14-production-readiness-remediation--blocking).
