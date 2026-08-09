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
  into a newly initialized MayFly v0.3.0 population along with deterministic
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

- CPU is the only backend supporting joint, sequential, and batch pipelines and
  custom base canvases.
- OpenCL is experimental, joint-only, CGO-dependent, and requires local headers,
  a loader, a driver, and a usable device. It is not exercised by the standard
  portable CI matrix.
- The experimental OpenCL renderer contains a CPU compatibility degradation path
  for runtime rendering/cost errors. Inspect warning logs and device-specific
  parity tests when GPU execution matters.
- ARM64 uses the scalar SSD/SAD implementations. NEON constants and compatibility
  wrapper names exist, but there is no native NEON kernel.
- AMD64 AVX2 is selected only when the CPU reports support; scalar execution is
  used otherwise.
- The production CPU renderer uses `FastMSECost`; parity tests cover the scalar
  reference, SIMD dispatch, independent origins/strides, empty images, and
  dimension mismatches. This remains a numeric RGB objective, not a perceptual
  quality metric.
- The image objective compares RGB values and ignores alpha. It is a numeric
  pixel error, not a perceptual color-space metric.
- Circle compositing is a raster approximation without an antialiasing quality
  guarantee. Results can differ from vector renderers.
- Evolutionary optimization is expensive and does not guarantee a global
  optimum. Seed, population, iteration count, mode, and image size materially
  affect runtime and quality.

## CI and release status

- Go 1.24 is the source-compatibility floor, not a promise that an old Go 1.24
  patch is safe for production. Build production binaries with a currently
  supported, security-patched Go release; vulnerability CI currently uses Go
  1.26.5.
- The standard gates cover generation drift, formatting, vet, pinned
  staticcheck, short tests, race short tests, a 50% aggregate coverage floor and
  artifact, ordinary builds, selected CGO-disabled cross-builds, an
  OpenCL-header GPU-tag compile, and pinned `govulncheck`.
- Real-device GPU tests, per-package coverage thresholds, long-running lifecycle
  tests, and performance regression thresholds are not required gates yet.
- Cross-compilation proves that packages compile for a target; it does not test
  runtime behavior on that operating system or architecture.
- The existence of a workflow is not evidence that a revision passed it. Use
  the workflow result and Phase 14 acceptance checks for release decisions.

Track remaining work in the active [Phase 14 plan](../PLAN.md#phase-14-production-readiness-remediation--blocking).
