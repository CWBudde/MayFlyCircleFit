# Rendering internals: SIMD SSD and CPU span compositing

This document describes the rendering hot path as it is implemented today: the
architecture-specific SSD kernels, their runtime dispatch, and the CPU span
compositor. It is the reference for anyone changing renderer math or touching
assembly.

Related documents:

- [`renderer-correctness.md`](renderer-correctness.md) — the byte-exact parity
  contract every kernel below has to hold. Read it before relaxing a tolerance.
- [`cpu-performance-history.md`](cpu-performance-history.md) — the measured
  milestones. Timings are machine-specific; do not compare them across hosts.
- [`exact-span-compositors.md`](exact-span-compositors.md) — the exact span
  kernels in detail, and why `--fast-compositing` still exists.
- [`incremental-cost.md`](incremental-cost.md) — the dirty-span cost contract
  and its selection rule.
- [`rejected-optimizations.md`](rejected-optimizations.md) — what was measured
  and *not* shipped. Check it before proposing a rendering optimization.
- [`simd-design.md`](simd-design.md) — the pre-implementation research record.
  Historical; the notes below take precedence where they disagree.
- [`support-matrix.md`](support-matrix.md) — supported platforms and backends.

## Canvas, traversal, and the scalar contract

These are the properties every faster path below has to preserve. They are
cheap to break silently, so they are stated before the kernels.

- **One reusable canvas.** A `CPURenderer` allocates its `image.NRGBA` once at
  construction and resets it with a single `copy(r.canvas.Pix, r.initialBg)`
  from a precomputed background. `initialBg` generalizes to custom and shared
  canvases (`NewCPURendererWithCanvas`, `newCPURendererWithSharedCanvas`). This
  is why the hot path is allocation-free; it is also why the returned buffer is
  mutable and reused, which callers must respect.
- **AABB precomputation and early reject.** Each circle's bounding box is
  computed once and clamped to integer image bounds; circles fully off-canvas
  cost four float comparisons. The opacity reject fires only at **exactly
  zero** — a `0.001` threshold changes an 8-bit channel on non-white canvases,
  which is a regression the correctness matrix caught once already. See
  [`renderer-correctness.md`](renderer-correctness.md).
- **Rounding.** `uint8(x*255 + 0.5)` replaces `math.Round`, and is exactly
  equivalent over the `[0, 255.5]` domain the compositor produces. It removed
  `math.Round` and `math.Float64bits` — together ~20% of the pre-optimization
  profile — from the hot path entirely.
- **The scalar blend is the normative reference.** `compositePixel`
  (`renderer_cpu.go`) is what every vector compositor must reproduce
  bit-for-bit. It was division-bound at seven divisions per pixel; it is now
  one, via a `const inv255` multiply, one hoisted `1/outA` reciprocal, common
  subexpression reuse of `bgA*(1-fgA)`, and an inlined pixel offset. All four
  transforms are algebraic, so output is unchanged. An opaque-destination fast
  path (`img.Pix[i+3] == 255`) handles the common case.
- **Circle parameters stay array-of-structures.** All seven fields are read
  together per circle and reused across thousands of pixels: 56 bytes, one cache
  line. Parameter loading is under 1% of runtime, so no layout change there can
  pay. See [`rejected-optimizations.md`](rejected-optimizations.md).
- **Scanline sharding.** Row workers own disjoint row ranges, so no two
  goroutines write the same pixel and no locking is needed. Threading has real
  overhead and is wrong for tiny workloads; see
  [`cpu-rendering-threads.md`](cpu-rendering-threads.md).

## Circle span geometry

- Representable geometry walks span edges in **scalar Q16.16**; geometry Q16.16
  cannot represent falls back to the exact float64 oracle. Q16.16 is a
  quantified approximation, not bit-identity: it changes about 0.00074% of row
  spans.
- The transferable win is **monotonic eight-pixel batching**, not the number
  format. One farthest-candidate comparison certifies eight pixels at a time,
  and finite differences handle the 0–7 tail. It applies to float32, float64,
  and fixed point alike.
- Neither the AVX2 Q16.16 kernel nor the AVX2 float32 kernel is on the
  production path; both were measured and rejected. The AVX2 kernels remain
  reachable for measurement only. See
  [`rejected-optimizations.md`](rejected-optimizations.md).

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
- Every tier has an exact float64 vector span kernel, and all of them are on by
  default because all are byte-identical to `compositeOpaqueSpanScalar`:
  eight-pixel NEON on ARM64 (256-pixel cutoff, measured on Apple M5), two-pixel
  AVX2 on AMD64 (16-pixel cutoff, measured on a Ryzen 5 4600H), and two-pixel
  SSE2 on AMD64 (24-pixel cutoff, measured on a host that genuinely lacks AVX2).
  The cutoffs are not shared and must not be copied between them; the NEON
  kernel has a much larger setup cost to amortize.
- The SSE2 cutoff is 24 rather than the 8 a masked AVX2 machine suggests,
  because dispatch reaches SSE2 only when AVX2 is absent, so the machine that
  actually runs it sets the constant. It is worth about 1.07x there and roughly
  1.06x end to end on 256x256 and larger canvases - a real but modest win, and
  the reason it ships is that it is byte-identical and needs no flag.
- **All exact kernels depend on the Go backend's multiply-add contraction, in
  opposite directions.** arm64 fuses `fg + bg*blend` into FMADDD and the NEON
  kernel must fuse to match; amd64 does not fuse and the AVX2 and SSE2 kernels
  must not. Never introduce an FMA into any of them without re-establishing byte
  parity. `TestCompositeSpanExactFusionContract` pins the amd64 half;
  `composite_span.go` documents the arm64 half. The float32 kernels inherit the
  same constraint, which is also why their scalar oracle is not portable: it
  produces different bytes on the two architectures.
- `compositeSpanExact` dispatches with a switch, never a function pointer.
  Routing the call indirectly defeats `//go:noescape` and heap-allocates the
  160-byte constant block once per span, which costs more than either kernel
  saves. `TestCompositeOpaqueSpanDoesNotAllocate` pins this.
- Translucent custom canvases retain the general per-pixel Porter-Duff path.
  Preserve that split, and the byte-exact span tests, when changing renderer
  math.
- `--fast-compositing` selects an opt-in float32 SIMD span compositor
  (`composite_span_fast*`, SSE2 and AVX2 kernels). It regroups the blend into
  one multiply-add per pixel, is accurate to +/-1 per channel, and is therefore
  not byte-identical to the default. It survives the exact AVX2 compositor
  because it is still 2.4x to 4.2x faster than it at realistic span lengths;
  measure any future change to it against the exact *vector* path, never against
  the scalar loop. Below 16 pixels it is slower as well as less accurate. It has
  no kernel outside amd64, where enabling it is a pure loss and startup warns.
  See [`exact-span-compositors.md`](exact-span-compositors.md).
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
- CPU renderers use `FastMSECost` after parity coverage against `MSECost`. Both
  constructors select it; `MSECost` stays as the readable oracle and as the
  explicit opt-out through `SetCostFunc`, with `UseFastCost` restoring the
  default. Independent image origins and strides, empty images, and
  dimension-mismatch behavior all have dedicated correctness handling and tests.
  Note the scale: `FastMSECost` is 9–18× faster than `MSECost` in isolation but
  moves end-to-end cost by only 1.03–1.29×, because compositing dominates. That
  measurement is why the vector work targets the compositor and not the cost
  kernel.
- Appended CPU batches may start from a verified, already-rendered prefix.
  Every evaluation slot owns its mutable canvas and dirty-span state, but the
  slots share the retained pixels as an immutable reset background. The staged
  accumulator also supplies the final result image, avoiding another complete
  vector replay. See
  [`single-circle-extend-report.md`](single-circle-extend-report.md).
- Active-set polishing adds a second exact delta-SSD use. One immutable
  incumbent image/SSD pair is shared by the sweep's CPU sessions. Each session
  tracks the old/new active-disc union as normalized horizontal spans, restores
  those spans from its baked-prefix background, clips suffix compositing to the
  span set, and reduces only the candidate/incumbent/reference triples in those
  spans. This is not the staged incremental path: its baseline is a complete
  incumbent rather than the session's initial canvas, and it must include both
  old and proposed geometry. Above 5% affected pixels it uses the ordinary full
  renderer, based on the crossover recorded in
  `contiguous-window-polish-report.md`.

## Profile-guided work

The post-Task-10.12 Apple M5 profile assigns 65.01% of flat samples to the
scalar span compositor, 26.47% to scanline traversal, and 1.95% to gated NEON.
Keep further rendering work profile-guided and pixel-equivalent.

Early CPU measurements on a Ryzen 5 4600H show a 2.09–2.47× single-thread
renderer speedup, zero timed allocations after canvas reuse, and a 6.39× median
large-workload gain with 12 scanline workers. See
[`cpu-performance-history.md`](cpu-performance-history.md).

`github.com/google/pprof` is pinned as a Go tool because some Go installations
do not bundle it; use `go tool pprof` for profile analysis.

## Validating changes

- `just cross-build` verifies the selected source set and compiles the CLI and
  the `internal/fit` test binary for every supported CPU target with
  `CGO_ENABLED=0`.
- `CIRCLEFIT_SIMD_TIER=avx2|sse2|neon|scalar` pins the tier for a process, and an
  unparseable or unreachable value panics at init rather than falling back — a
  gate that asks for SSE2 must not pass while measuring AVX2.
  `CIRCLEFIT_DISABLE_SIMD=1` is kept as an alias for `scalar`.
- `CIRCLEFIT_REQUIRE_SIMD_TIER` is the opposite lever: it asserts the detected tier
  without setting it, which is what makes `GODEBUG=cpu.avx2=off` plus
  `CIRCLEFIT_REQUIRE_SIMD_TIER=sse2` a real check. Never pair `CIRCLEFIT_SIMD_TIER=x`
  with `CIRCLEFIT_REQUIRE_SIMD_TIER=x` and call it detection coverage; that
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
- Benchmark workloads must be seeded from a literal, never from the clock.
  `benchmarkParams` takes its seed as an argument for that reason: while it
  seeded from `time.Now()`, two runs of `BenchmarkCPURenderer_Render` rendered
  different circles and the same binary measured a 31% spread on the same
  machine, which is wider than most changes worth measuring. With a fixed seed
  the same case holds 1%.
- A benchmark must exercise the path it is named after. `BenchmarkRenderCircle`
  built `CPURenderer` as a struct literal, leaving `opaqueCanvas` false, so it
  only ever measured the per-pixel compositor; and it refilled the canvas inside
  the timed loop, which cost more than the circle did. Both are now explicit:
  the canvas kind is a benchmark axis and the fill happens before the timer
  starts.
