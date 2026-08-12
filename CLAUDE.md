# MayFlyCircleFit development context

This file is concise repository context for coding assistants. The observable
code and tests take precedence if this document becomes stale.

## Toolchain and generated files

- The module has a Go 1.24 source-compatibility floor (`go 1.24.0` in
  `go.mod`). Production binaries should use a currently supported,
  security-patched Go release; vulnerability CI is pinned to Go 1.26.5.
- MayFly is pinned to `github.com/cwbudde/mayfly v0.3.0`.
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
- `internal/opt`: optimizer interfaces and the MayFly v0.3.0 adapter.
- `internal/server`: trusted-local HTTP boundary and background job lifecycle.
- `internal/store`: filesystem checkpoint, trace, and artifact ownership.
- `internal/ui`: templ views plus committed generated Go output.

Keep dependencies flowing toward these lower-level packages; do not reintroduce
application configuration into the store package.

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
- Resume is restart-from-best: the MayFly v0.3.0 population is seeded with the
  saved best and deterministic nearby variations. It is not an exact restoration
  of optimizer internals. Server restart-from-best for sequential and batch jobs
  is not supported.
- A zero user seed generates and reports an effective seed; a nonzero seed is
  deterministic.
- Current CPU profiles identify pixel compositing as the remaining dominant
  renderer hotspot (83.48% of flat samples in the documented 12-thread
  profile); scanline traversal is secondary. Keep further rendering work
  profile-guided and pixel-equivalent.

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
