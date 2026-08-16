# MayFlyCircleFit development context

This file is concise repository context for coding assistants. The observable
code and tests take precedence if this document becomes stale.

## Toolchain and generated files

- The module has a Go 1.24 source-compatibility floor (`go 1.24.0` in
  `go.mod`). Production binaries should use a currently supported,
  security-patched Go release; vulnerability CI is pinned to Go 1.26.5.
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
  timing comparison is report-only.
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
  `ssd_sse2_amd64.s` processes four per SSE2 batch; `ssd_arm64.s` processes four
  per NEON batch. All ignore alpha, reduce into a 64-bit total, and leave
  non-multiple widths to an exact scalar tail.
- The SSE2 kernel accumulates a whole row's `PMADDWD` results in int32 lanes and
  widens to int64 once per row. That is the source of its speedup, and it bounds
  the row: `width*3*255*255` must stay below 2^31, so `ssdSSE2MaxWidth` is 11000
  and `fastSSD_SSE2` routes wider rows to the scalar kernel instead of widening
  per iteration.
- The assembly is hand-written in Go Plan 9 syntax. The implemented workflow
  does not use GoAT, C sources, cgo, or an external assembler.
- Architecture-specific initialization checks `x/sys/cpu` once and installs an
  AVX2, SSE2, NEON, or scalar function pointer. Do not call an assembly kernel
  without passing through feature-gated dispatch.
- `just cross-build` verifies the selected source set and compiles the CLI and
  `internal/fit` test binary for every supported CPU target with
  `CGO_ENABLED=0`.
- Use `MAYFLY_REQUIRE_SSD_BACKEND` in native hardware validation.
  `GODEBUG=cpu.all=off` no longer reaches the scalar kernel on amd64:
  `x/sys/cpu` registers sse2 with `Required: runtime.GOARCH == "amd64"` and
  `processOptions` ORs `Required` back in, so `cpu.X86.HasSSE2` stays true and
  dispatch selects SSE2. Set `MAYFLY_DISABLE_SIMD=1` (`fit.SIMDDisabledByEnv`)
  to force the scalar backend on every kernel and architecture; that is the only
  complete scalar-fallback lever. `GODEBUG=cpu.avx2=off` exercises the SSE2 tier
  on an AVX2 host.
- SAD is deliberately not ported to SSE2. `FastSAD` has no non-test callers, and
  its AVX2 kernel depends on `VPMADDUBSW` (SSSE3) and `VPMULLD` (SSE4.1), which
  baseline SSE2 does not provide.
- Kernel benchmarks must retain their result through `ssdBenchmarkSink` and
  report allocations.

### CPU span compositing

- Opaque canvases use horizontal span compositing; the scalar span hoists
  foreground and blend invariants out of the per-pixel loop.
- ARM64 includes an exact float64, eight-pixel NEON span kernel. Runtime ASIMD
  detection and a measured 256-pixel cutoff guard it; shorter spans and tails
  use scalar because that is faster on Apple M5.
- Translucent custom canvases retain the general per-pixel Porter-Duff path.
  Preserve that split and byte-exact span tests when changing renderer math.
- `--fast-compositing` selects an opt-in float32 SIMD span compositor
  (`composite_span_fast*`, SSE2 and AVX2 kernels behind the same feature gate).
  It regroups the blend into one multiply-add per pixel, is accurate to +/-1 per
  channel, and is therefore not byte-identical to the default float64 span. It
  defaults off; the exact path stays the default and the oracle.
- The Q16.16 circle-span kernel is deliberately not ported to SSE2. It compares
  Q32.32 products with `VPCMPGTQ`, SSE2 has no 64-bit signed compare, and a
  measured no-AVX2 profile attributes only 2.80% of flat samples to
  `fixedCircleQ16.span`. `spanAVX2` falls through to the scalar
  finite-difference span on non-AVX2 CPUs.
- `stagedIncremental` is gated on `deltaSSDVectorized()`, true for the AVX2 and
  SSE2 delta-SSD kernels on amd64 and still false on other architectures. ARM64
  has a NEON delta-SSD kernel, but the staged path was never profiled there.

## Current behavior that must remain explicit

- CPU supports joint, sequential, and batch modes, including a supplied base
  canvas.
- OpenCL is experimental and supports joint mode only. It requires a `gpu`
  build tag, CGO, OpenCL development headers, and a usable runtime/device.
  Sequential and batch OpenCL requests must fail explicitly, not silently use a
  CPU staged renderer.
- AMD64 SSD dispatch is tiered AVX2, then SSE2, then scalar, each after a
  runtime CPU-feature check; ARM64 SSD dispatch requires ASIMD before selecting
  NEON. Unsupported architectures use the scalar kernel. SAD remains scalar on
  ARM64 and on amd64 hosts without AVX2.
- `MAYFLY_DISABLE_SIMD=1` forces the scalar backend for every kernel on every
  architecture. It exists because SSE2 is mandatory on amd64 and cannot be
  masked through `GODEBUG`, so it is the only way to gate the complete scalar
  fallback there.
- CPU renderers use `FastMSECost` after parity coverage against `MSECost`.
  Independent image origins and strides, empty images, and dimension mismatch
  behavior have dedicated correctness handling/tests.
- `--fast-compositing` and `--parallel-evaluation` are both opt-in and both
  default off, because each changes the result of a fixed seed.
  `--fast-compositing` is accurate to +/-1 per channel, not byte-identical to
  the default compositor. `--parallel-evaluation` leases one independent
  renderer session per concurrent evaluation and reproduces bit-identically for
  a fixed seed and any worker count, but its trajectory differs from a serial
  run of that seed because MayFly holds the global best fixed for a whole
  parallel generation. Compare runs only against runs with the same settings.
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
  rasterizes the whole image for every candidate. `contiguous-window` selects a
  consecutive run instead, keeping that cost near `activeSetSize` on the first
  sweep. Do not change a selector to scatter its active set without accounting
  for that cost.
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
