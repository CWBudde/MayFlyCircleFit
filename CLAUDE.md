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
- Resume is restart-from-best: the MayFly v0.4.0 population is seeded with the
  saved best and deterministic nearby variations. It is not an exact restoration
  of optimizer internals. Server restart-from-best for sequential and batch jobs
  is not supported.
- A zero user seed generates and reports an effective seed; a nonzero seed is
  deterministic.
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
