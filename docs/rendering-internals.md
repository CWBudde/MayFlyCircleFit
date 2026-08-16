# Rendering internals: SIMD SSD and CPU span compositing

This document describes the rendering hot path as it is implemented today: the
architecture-specific SSD kernels, their runtime dispatch, and the CPU span
compositor. It is the reference for anyone changing renderer math or touching
assembly.

Related documents:

- [`simd-design.md`](simd-design.md) — the original Phase 10 research record
  (option analysis, why Plan 9 assembly won). Historical; the implementation
  notes below take precedence.
- [`task-10.10-simd-performance-report.md`](task-10.10-simd-performance-report.md),
  [`task-10.12-neon-span-report.md`](task-10.12-neon-span-report.md),
  [`task-9.9-performance-report.md`](task-9.9-performance-report.md) — measured
  results. Timings are machine-specific; do not compare them across hosts.
- [`support-matrix.md`](support-matrix.md) — supported platforms and backends.

## SSD kernels

- `internal/fit/ssd_amd64.s` processes eight NRGBA pixels per AVX2 batch;
  `internal/fit/ssd_sse2_amd64.s` processes four per SSE2 batch;
  `internal/fit/ssd_arm64.s` processes four per NEON batch. All ignore alpha,
  reduce into a 64-bit total, and leave non-multiple widths to an exact scalar
  tail.
- Both SSE2 kernels accumulate `PMADDWD` results in int32 lanes and widen to
  int64 once at the end rather than per iteration. That is the source of their
  speedup, and it is what bounds their input. The two kernels enforce the bound
  differently, and both derivations live at the constant, not here:
  `ssdSSE2MaxWidth` (11000) routes wider rows to the scalar kernel, while
  `deltaSSDSSE2MaxPixels` (8192) splits longer spans and sums them in int64, so
  the delta kernel has no width cliff. The real limit in both cases is per lane,
  not per row: `PMADDWD` pairwise-adds the widened `R,G,B,0` words, so the
  busiest lane carries `R²+G²`, never the row total.
- The assembly is hand-written in Go Plan 9 syntax. The implemented workflow
  does **not** use GoAT, C sources, cgo, or an external assembler, despite what
  the original design document proposed.
- **A package that uses cgo cannot contain Go assembly.** `cmd/go` hands `.s`
  files in a cgo package to the C compiler and rejects any that carry Plan 9
  directives (`TEXT`/`DATA`/`GLOBL`), so `-tags gpu` fails to build the whole
  package with `package using cgo has Go assembly file …`. This is why the
  OpenCL renderer lives in `internal/fit/renderer/opencl` rather than beside the
  kernels it falls back to. `internal/fit/renderer/renderer_opencl_gpu.go` is
  the gpu-tagged adapter that bridges the two: it injects the CPU renderer as
  `opencl.Fallback` and implements the unexported `newSession` method that
  `rendererSessionFactory` requires and no other package can provide. Keep the
  dependency one-way — `opencl` must never import `renderer`.
- **One tier, resolved once.** `fit.Tier()` is the single source of truth for
  which instruction set this process uses. Dispatch sites do not read
  `x/sys/cpu`; they call `fit.RegisterTierConsumer` at init and install their
  kernel from the tier they are handed. Do not add a dispatch site that decides
  for itself — that is what produced nine independent `init` functions and four
  different spellings of "which backend am I on", none of which could be checked
  against each other. Never call an assembly kernel without passing through that
  dispatch.
- A kernel may be *narrower* than the tier when it has no implementation at that
  tier; it may never be wider. `ActiveSSDKernel`, `ActiveSADKernel`,
  `deltaSSDKernel`, `circleSpanFloat32Kernel`, and `compositeSpanKernel` report
  what was installed, and `TestInstalledKernelsMatchTier` plus
  `TestRendererKernelsMatchTier` assert the relationship in both packages.
- SAD is deliberately not ported to SSE2. `FastSAD` has no non-test callers, and
  its AVX2 kernel depends on `VPMADDUBSW` (SSSE3) and `VPMULLD` (SSE4.1), which
  baseline SSE2 does not provide. SAD remains scalar on ARM64 and on amd64 hosts
  without AVX2.

## CPU span compositing

- Opaque canvases use horizontal span compositing; the scalar span hoists
  foreground and blend invariants out of the per-pixel loop.
- ARM64 includes an exact float64, eight-pixel NEON span kernel. Runtime ASIMD
  detection and a measured 256-pixel cutoff guard it; shorter spans and tails
  use scalar, because that is faster on Apple M5.
- AMD64 includes an exact float64, two-pixel SSE2 span kernel, on by default
  because it is byte-identical to `compositeOpaqueSpanScalar`. Its cutoff is 24
  pixels. An AVX2 host still composites scalar: the exact AVX2 kernel is a
  separate change, and the dispatch switch must not claim a tier it has no
  assembly for.
- The SSE2 cutoff is 24 rather than the 8 a masked AVX2 machine suggests,
  because dispatch reaches SSE2 only when AVX2 is absent, so the machine that
  actually runs it sets the constant. It is worth about 1.07x there and roughly
  1.06x end to end on 256x256 and larger canvases - a real but modest win, and
  the reason it ships is that it is byte-identical and needs no flag. Cutoffs
  are never shared between kernels; the NEON one has a far larger setup cost.
- The exact vector kernels depend on the Go backend's multiply-add contraction,
  in opposite directions. arm64 fuses `fg + bg*blend` into FMADDD and the NEON
  kernel must fuse to match; amd64 does not fuse and the SSE2 kernel must not.
  Never introduce an FMA into either without re-establishing byte parity.
  `TestCompositeSpanExactFusionContract` pins the amd64 half.
- `compositeSpanExactSSE2` is called directly, never through a function pointer.
  Routing it indirectly defeats `//go:noescape` and heap-allocates the 160-byte
  constant block once per span, which costs more than the kernel saves.
  `TestCompositeOpaqueSpanDoesNotAllocate` pins this.
- Translucent custom canvases retain the general per-pixel Porter-Duff path.
  Preserve that split, and the byte-exact span tests, when changing renderer
  math.
- `--fast-compositing` selects an opt-in float32 SIMD span compositor
  (`composite_span_fast*`, SSE2 and AVX2 kernels behind the same feature gate).
  It regroups the blend into one multiply-add per pixel, is accurate to +/-1 per
  channel, and is therefore not byte-identical to the default float64 span. It
  defaults off; the exact path stays the default and the oracle.
- Circle-span geometry has no SSE2 kernel in either form. The Q16.16 AVX2 kernel
  compares Q32.32 products with `VPCMPGTQ`, SSE2 has no 64-bit signed compare,
  and a measured no-AVX2 profile attributes only 2.80% of flat samples to
  `fixedCircleQ16.span`; `spanAVX2` falls through to the scalar
  finite-difference span on non-AVX2 CPUs. The float32 form has no SSE2 kernel
  for a different reason: `circleSpanFloat32Selected` is reachable only through
  `CPURenderer.forceFloat32Geometry`, which no configuration path or CLI flag
  sets. Do not add a kernel to a path production cannot enter.
- `stagedIncremental` is gated on `deltaSSDVectorized()`, true for the AVX2 and
  SSE2 delta-SSD kernels on amd64 and false elsewhere. The SSE2 case is measured
  on a genuine no-AVX2 CPU, not on an AVX2 host under GODEBUG; the crossover
  table is in `staged_incremental_amd64.go`. ARM64 has a NEON delta-SSD kernel,
  but the staged path was never profiled there, so it stays off.
- CPU renderers use `FastMSECost` after parity coverage against `MSECost`.
  Independent image origins and strides, empty images, and dimension-mismatch
  behavior all have dedicated correctness handling and tests.

## Profile-guided work

The post-Task-10.12 Apple M5 profile assigns 65.01% of flat samples to the
scalar span compositor, 26.47% to scanline traversal, and 1.95% to gated NEON.
Keep further rendering work profile-guided and pixel-equivalent.

Phase 9 CPU measurements on a Ryzen 5 4600H show a 2.09–2.47× single-thread
renderer speedup, zero timed allocations after canvas reuse, and a 6.39× median
large-workload gain when Task 9.7 uses 12 workers.

`github.com/google/pprof` is pinned as a Go tool because some Go installations
do not bundle it; use `go tool pprof` for profile analysis.

## Validating changes

- `just cross-build` verifies the selected source set and compiles the CLI and
  the `internal/fit` test binary for every supported CPU target with
  `CGO_ENABLED=0`.
- `MAYFLY_SIMD_TIER=avx2|sse2|neon|scalar` pins the tier for a process, and an
  unparseable or unreachable value panics at init rather than falling back — a
  gate that asks for SSE2 must not pass while measuring AVX2.
  `MAYFLY_DISABLE_SIMD=1` is kept as an alias for `scalar`.
- `MAYFLY_REQUIRE_SIMD_TIER` is the opposite lever: it asserts the detected tier
  without setting it, which is what makes `GODEBUG=cpu.avx2=off` plus
  `MAYFLY_REQUIRE_SIMD_TIER=sse2` a real check. Never pair `MAYFLY_SIMD_TIER=x`
  with `MAYFLY_REQUIRE_SIMD_TIER=x` and call it detection coverage; that
  combination only checks that dispatch honored the pin.
- `GODEBUG=cpu.all=off` still cannot reach the scalar tier on amd64: `x/sys/cpu`
  registers sse2 with `Required: runtime.GOARCH == "amd64"` and `processOptions`
  ORs `Required` back in, so `cpu.X86.HasSSE2` stays true and detection lands on
  SSE2.
- `fit.SetForcedTier` re-runs every registered dispatch site, so one test
  process can walk the whole ladder. Prefer it to a subprocess; reserve
  subprocesses for what forcing genuinely cannot cover, which is detection under
  GODEBUG and the env levers themselves.
- Kernel correctness tests must call the kernel directly rather than gating on
  the installed one. `hostSSDKernels` lists every kernel this build and CPU can
  execute, and `ssd_differential_test.go` compares all of them for exact
  equality. A `t.Skip` on the active backend means the kernel is untested on
  every development machine.
- Kernel benchmarks must retain their result through `ssdBenchmarkSink` and
  report allocations.
