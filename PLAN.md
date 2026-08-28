# CircleFit Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Build a high-performance circle-fitting optimization tool with CPU/GPU backends, web UI, and live progress visualization.

**Architecture:** Go-based CLI with Cobra, modular optimizer/renderer interfaces, HTTP server with SSE streaming, templ-based UI, and SIMD-accelerated evaluation kernels.

**Tech Stack:** Go 1.24+, Cobra (CLI), templ (frontend), slog (logging), net/http (server), optional chi (routing), cgo+SIMD (performance), OpenGL/OpenCL (GPU)

> **Production-readiness audit (2026-08-09):** Historical `COMPLETE` markers below describe implementation milestones, not validated production readiness. Phase 14 supersedes affected completion claims until its acceptance checks pass. Remediation code, release-gating CI, and corrected documentation are now present, but no gate is marked passed solely because its workflow was added; final clean-clone and repeated CI verification remain release requirements.

---

## Completed foundation (Phases 1–9) ✅

The original task-by-task history is compacted here; implementation details,
measurements, and caveats live in the linked documentation and git history.

| Phase | Completed outcome |
| --- | --- |
| 1–5 | Domain model, CPU renderer, Mayfly adapter, joint/sequential/batch pipelines, and Cobra CLI. |
| 6 | Trusted-local background server, job lifecycle, REST/image endpoints, SSE progress, CLI integration, and lifecycle coverage. |
| 7 | templ job list/detail/create UI, live metrics and images, sparkline, validation, and UI documentation. Browser/mobile validation continues in Task 12.9. |
| 8 | Atomic filesystem checkpoints, traces, retention, and restart-from-best; corrected semantics are documented in `docs/checkpoint-resume-test-results.md`. |
| 9 | Profiling/benchmark infrastructure, allocation-free CPU fast paths, scanline sharding, and correctness validation. See `docs/benchmarks.md`, `docs/cpu-performance-history.md`, and `docs/renderer-correctness.md`. |

---

## Phase 10: SIMD/C Intrinsics Research & Implementation (Evaluation Loop)

**Goal:** Recover a large chunk of the original "blazing fast" feel by applying vectorized kernels to the evaluation hot path.

**Status:** Research complete, implementation in progress

### Completed Tasks 10.1–10.19 ✅

- Tasks 10.1–10.10 established the scalar, AVX2, NEON, and SSE2 SSD kernels,
  runtime tier dispatch, production cost integration, native/cross-build
  coverage, and benchmark reports.
- Tasks 10.11–10.15 replaced the original pixel loop with scanline/span
  rendering, exact SIMD compositors, Q16.16 geometry with an exact range
  fallback, and a measured opt-in symmetry prototype. The selected combined
  path and tradeoffs are recorded in
  `docs/cpu-performance-history.md`.
- Task 10.16 shipped exact incremental dirty-span SSD for staged pipelines; its
  arithmetic, crossover, and parity evidence are in
  `docs/incremental-cost.md`.
- Tasks 10.17–10.19 completed the AMD64 SSE2 tier and exact SSE2/AVX2
  compositors. Population evaluation parallelism was subsequently implemented
  and measured in `docs/parallel-evaluation-report.md`.

### Task 10.20: Deferred CPU-Kernel Research (P3)

These items were moved out of otherwise completed tasks. They are bounded
research follow-ups, not blockers for the selected production CPU path.

- [x] Compare signed Q24.8 and normalized Q8.24 geometry with Q16.16, including
  coordinate range, adversarial boundaries, and full-render results. Q16.16
  stays; production geometry is unchanged and the comparison lives in the
  test-only harness `internal/fit/renderer/circle_geometry_formats_test.go`,
  which implements both alternates next to the production path and decides every
  precision question with a `big.Rat` oracle rather than another float. Q24.8's
  only advantage is range Q16.16 already covers 40x over - a binary search over
  the `fit.NewBounds` box puts the fully representable square canvas at 21845
  for Q16.16, 5592405 for Q24.8 and 127 for Q8.24 - and the eight fraction bits
  it gives up cost 58x the disagreement rate against the oracle (58 of 17751
  rows against 1 of 17748, 400 circles on 513x389, seed 20260828) and 24-32
  differing bytes per rendered corpus with channel deltas up to 82, because a
  displaced edge decides a whole compositing step rather than perturbing a
  pixel. Normalized Q8.24 stores the center as an offset from an integer anchor,
  which frees the center range but not the radius: it cannot represent r >= 128
  at any center, 50.7% of bounds-legal circles on 256x256 and 76.0% on 512x512,
  and where it does apply it is more accurate but not exact - it fails at its own
  2^-24 boundary the way Q16.16 fails at 2^-16 - which under the byte-parity
  contract is a migration cost rather than a gain. No throughput case for either: i7-1255U,
  `GOMAXPROCS=1`, pinned with `taskset`, median of nine 500 ms runs on a P-core
  and an E-core, zero allocations per operation on every arm, Q24.8 at
  1.01x-1.08x and Q8.24 at 0.85x-1.02x against Q16.16 - the same integer
  sequence with different constants. Full write-up in
  [`docs/fixed-point-geometry-formats.md`](docs/fixed-point-geometry-formats.md),
  with the decision recorded in
  [`docs/rejected-optimizations.md`](docs/rejected-optimizations.md).
- [x] Assess a corresponding ARM64 NEON span-edge implementation without
  compromising the portable geometry layout. **Not worth building, and the
  ceiling is the reason.** The portable layout is not the obstacle and would not
  have to change: `fixedCircleQ16` already stores `xQ` as `int32` (the operand
  form `SMULL` wants), `radiusSquared` as `int64`, and `centerX` as the search
  origin, and `spanAVX2` is written against that struct without adding a field.
  NEON is also better suited to the arithmetic than either amd64 tier —
  `SMULL`/`SMULL2` supply the full signed 32×32→64 widening multiply whose
  absence forced the AVX2 kernel's shuffles, and `CMGT` on `.2D` supplies the
  64-bit signed compare SSE2 lacks. The payoff is what fails. `fixedCircleQ16.span`
  is 13.41%/15.67%/12.25% of flat samples at `BenchmarkFit/Render` 64×64/K4,
  256×256/K50 and 512×512/K100 on an i7-1255U, `GOMAXPROCS=1`, pinned to `cpu0`,
  10 s `-cpuprofile` captures; forcing the scalar compositor — the shape ARM64
  has below its 256-pixel cutoff — moves 256×256/K50 to 10.01%, so the share
  shrinks exactly where ARM64 sits. An infinitely fast kernel therefore buys at
  most 12–16% of render time. Against that, `BenchmarkCircleSpanQ16AVX2Direct`
  re-measured on this host (median of nine pinned 500 ms runs at `GOMAXPROCS=1`)
  puts the AVX2 kernel at 7.65/19.09/52.75/116.60 ns against 7.24/8.23/19.74/39.82
  ns scalar at radii 5.25/25.25/100.25/256.25 — 1.06× to 2.93× **slower**, and
  1.06× to 3.73× slower on `cpu4`. Halving the lane count does not recover that,
  because the scalar walk already spends one multiply per eight pixels through
  monotonic eight-pixel batching and runs its tail on int64 finite differences,
  while a vector kernel would compute eight independent distances and then pay a
  vector-to-GPR reduction for a decision the scalar code makes with one `CMP`.
  `spanAVX2` has no production call site either, so a NEON sibling would be dead
  on arrival on a path whose parity is only quantified, not byte-exact. Full
  write-up in
  [`docs/rejected-optimizations.md`](docs/rejected-optimizations.md).
- [x] Complete native cross-platform precision measurements for fractional and
  tangent boundaries, radii 1 and maximum radius, clipping, batch boundaries,
  randomized circles, and row sharding.
  `internal/fit/renderer/precision_boundaries_test.go` carries the matrix: 35
  named scenes as individual subtests (6 fractional, including coordinates whose
  Q16.16 conversion lands exactly on a `.5` tie in both signs; 5 tangent, an
  integer radius and one Q16.16 unit either side of it; 7 radius, from
  `fit.MinCircleRadius` through the `max(width, height)` bound to the first
  radius and the first center past the signed Q16.16 range, which take the
  float64 fallback; 9 clipping, partly and wholly beyond each edge plus one
  circle larger than the canvas in both dimensions; 8 batch, the three widths
  that straddle the `xEnd+7 < width` stride guard and `circleBatchMinSquare`
  reached exactly at a quarter- and at a half-pixel center), plus a 512-draw
  randomized sweep over the full `fit.NewBounds` range from seed
  `0x5150_1020_5EED`, a 13-count row-shard sweep, and a batch-pipeline case
  comparing the serial `AuditCircleBatch` against the chunked parallel plan and
  `compositeParams` prefixes against a full render. The oracle is both halves of
  the byte-exact contract: within a process every case is re-rendered at every
  tier `fit.SupportedTiers()` (new) reports and at every shard count and
  compared byte for byte; across processes each case carries a recorded SHA-256
  of its pixel buffer plus its exact cost, generated from a local SplitMix64 so
  the constants depend on nothing outside the file. No test names a worker
  count. Measured pass at amd64 scalar/SSE2/AVX2 and at arm64 scalar/NEON, the
  latter cross-built and run under `qemu-aarch64-static` 8.2.2 on the same host,
  which reports `neon` and executes the real kernels; every digest is identical
  on both architectures and the whole package exits 0 there. Emulation
  establishes arithmetic only — no timing in this item is a measurement, and
  ARM64 crossovers still need hardware. One finding: the center-out span search
  starts at `int(centerX+0.5)` and never tests that pixel, so every row the disc
  touches paints its nearest sample even where the pre-optimization rasterizer
  paints nothing — 1,110 of 12,466,238 intersecting rows (0.0089%), identical on
  both architectures, never under-covering. It is a property of the search, not
  of Q16.16 or of any architecture, so it is pinned by
  `TestSpanSearchAlwaysCoversNearestSample` and `TestSpanSearchOverCoverageRate`
  rather than changed; changing it would move every recorded cost in `docs/`.
  Full write-up in
  [`docs/renderer-precision-measurements.md`](docs/renderer-precision-measurements.md).
- [ ] If the original Pascal/Delphi source becomes available, document its
  exact cost arithmetic and numeric/SIMD representations; until then,
  `docs/incremental-cost.md` remains the Go contract.
- [x] Hoist the exact compositor's per-span constant block to once per circle
  and remeasure the SSE2/AVX2 crossover before changing production dispatch.
  The block is built by `renderCircleScanlineRowsTracked` and
  `compositeCircleDirtyRows` and threaded down as a `*spanBlend`, which carries
  neither the colour nor the tier; it is an empty struct off amd64, so the row
  walkers need no build tags. `compositeSpanAVX2MinPixels` moves 16 -> 6.
  Measured on an i7-1255U (Alder Lake-P, hybrid), pinned with `taskset` at
  `GOMAXPROCS=1`, median of nine 500 ms runs on both a P-core and an E-core,
  zero allocations per operation on every arm:
  `BenchmarkCompositeSpanExactHoistedCutoff` against
  `BenchmarkCompositeSpanExactCutoff` puts the removed setup at 6-9 ns per AVX2
  span and 3-8 ns per SSE2 span; 4 pixels still loses on the P-core (0.97x) and
  6 wins on both (1.26x P, 1.40x E), so 6 is the larger of the two core types'
  answers. `BenchmarkCompositeOpaqueSpanBlend` confirms every length from 6
  upward through the real dispatcher. End to end, `BenchmarkFit` against base
  `e32b907`, six interleaved rounds of `-count=2 -benchtime=300ms` pinned at
  `GOMAXPROCS=1`: `Render/64x64/K4` -25.9% and `Render/128x128/K20` -22.1%
  (both p = 0.000), the two larger canvases -5.5% and -6.1%, against a `Cost`
  control whose noisiest arm moved 6.8%. Output stays byte-identical
  (`TestCPURendererMatchesPreOptimizationBaseline`), and
  `TestRenderCircleRowsDoesNotAllocate` and `TestSpanBlendSurvivesTierChange`
  are new guards for the frame that now owns the block and for the tier not
  being cached into it. Full write-up in
  [`docs/exact-span-compositors.md`](docs/exact-span-compositors.md).
- [ ] Re-derive `compositeSpanSSE2MinPixels` on a host that genuinely lacks
  AVX2. 24 is the pre-hoist crossover and is now an upper bound: hoisting can
  only move a crossover left, so it stays correct and merely leaves some spans
  on scalar. It cannot be measured here, because dispatch selects SSE2 only when
  AVX2 is absent and neither `CIRCLEFIT_SIMD_TIER=sse2` nor
  `GODEBUG=cpu.avx2=off` changes the microarchitecture.
- [x] Hoist the ARM64 NEON blend scalars the same way. `spanBlend` on ARM64 is
  no longer an empty struct: it carries `fgR`, `fgG`, `fgB` and `bgBlend`, built
  once per circle by the same two frames that build the amd64 constant block
  (`renderCircleScanlineRowsTracked` and `compositeCircleDirtyRows`) and threaded
  down as a `*spanBlend`, so `compositeOpaqueSpan` reads them instead of
  recomputing three multiplies and a subtract per span. It carries neither the
  colour nor the tier, for the same two reasons amd64's does not.
  `composite_span_arm64.s` is unchanged — the hoist is entirely in Go because the
  kernel already took the four scalars as arguments — so the deliberately
  *unfused* `FMUL`+`FADD` pairs that keep it byte-identical to
  `compositeOpaqueSpanScalar` are untouched. `TestRenderCircleRowsDoesNotAllocate`
  and `TestSpanBlendSurvivesTierChange` now exist on ARM64 too, over the scalar
  and NEON tiers, and the whole `internal/fit/renderer` short suite passes on a
  cross-compiled binary under `qemu-aarch64-static`, which reports NEON and runs
  the kernel; `TestCompositeSpanNEONMatchesScalar` did not skip. amd64 is
  untouched and its full suite passes. **No ARM64 timing was measured and an
  emulated one would not count.**
- [ ] Re-derive `compositeSpanNEONMinPixels` on ARM64 benchmarking hardware. 256
  is the pre-hoist crossover, measured on an Apple M5, and is now an upper bound:
  hoisting can only move a crossover left, so it stays correct and merely leaves
  some spans on scalar. `BenchmarkCompositeOpaqueSpanNEONCutoff` is the command —
  `scalar`, `neon_hoisted` and `neon_rebuilt` arms at nine lengths, so one run
  yields both the new crossover and the setup the hoist removed. The ARM64 rows
  of `ci-native-simd.yml` cover correctness only, and emulation covers less.
- [x] Block multiply-add contraction so the CPU renderer is byte-identical on
  every target, and run `internal/fit/renderer` on both ARM64 rows of
  `ci-native-simd.yml`. The recorded diagnosis was wrong on three counts, and
  the corrected one is in
  [`docs/known-limitations.md`](docs/known-limitations.md). It named
  `compositeScalarPixel`, which does not exist - the alpha store is in
  `compositePixel` - and blocking that store changes nothing, because the oracle
  computes `outputAlpha` with the same expression shape and fuses with it. It
  claimed ARM64 hardware was needed; `qemu-aarch64-static` plus binfmt runs the
  cross-compiled test binary directly, reports NEON, and reproduces
  `pixel (4,11) channel 3 = 205, baseline = 206` exactly. And it recorded one
  failing test where there are two: `TestPolishCircleBatchPoolWidthParity` also
  fails, against goldens recorded on amd64. Compiling the *unmodified* tree with
  `-gcflags=all=-d=fmahash=<pattern>` makes the whole ARM64 suite pass, which
  bounds the cause to contraction alone, and `bisect -compile=fma` named
  `composite_span.go:19:24` as the decisive site. Two traps: an explicit
  conversion rounds only what it wraps, so `fgR + float64(bgR*bgBlend)` still
  fuses against `r*alpha` unless the premultiplied foreground is rounded too;
  and the scalar span compositor was deliberately fused to match the NEON
  kernel, so the kernel was unfused with it - a 1:1 swap of `VMOV`+`VFMLA` for
  `FMUL`+`FADD`, hand-encoded because Go's arm64 assembler has no vector
  mnemonic for them. Nothing had ever compared that kernel against its scalar
  reference: both ARM64 span tests force the scalar tier.
  `TestCompositeSpanNEONMatchesScalar` now does, and its k/255 colour sampling
  is load-bearing - uniform random floats found zero mismatches in 51.2 million
  evaluations against a reference that was wrong, where k/255 colours expose it
  in about half a percent of bytes. amd64 output is unchanged: it emits no
  `VFMADD` before or after, and the full amd64 suite passes. ARM64 throughput
  was not measured and emulated timings would not count.

## Phase 11: GPU Backends (Research → Prototype)

**Goal:** Add a pluggable GPU renderer/coster behind the existing `Renderer` interface.

### Completed Tasks 11.1–11.8 ✅

OpenCL was selected and integrated behind the renderer/session abstraction with
`gpu` build tags, device-side compositing and SSD reduction, persistent
buffers, lazy image readback, typed backend selection, and joint/sequential/
batch coverage. PoCL proved functional but slower than CPU in the measured
staged workload; vendor-GPU performance, broader correctness, fallback policy,
and documentation remain below. The former setup and visual-validation
remainders were moved into Tasks 11.10 and 11.12.

### Task 11.9: Create GPU Performance Benchmarks
- [x] Benchmark GPU rendering for various K values (1, 10, 50, 100)
- [x] Benchmark GPU rendering for various W×H sizes (64x64, 256x256, 512x512, 1024x1024)
- [x] Benchmark GPU cost computation separately
- [x] Compare GPU vs CPU performance across scenarios
- [x] Identify crossover points where GPU becomes beneficial
- [x] Document performance characteristics

`BenchmarkRendererBackendMatrix` crosses all four circle counts with all four
image sizes in a `cost` and a `cost_then_render` arm, measured on an NVIDIA T550
in [`docs/gpu-performance-report.md`](docs/gpu-performance-report.md). These are
the first vendor-GPU numbers; every PoCL timing on this path is superseded.

The evaluation path is a clear GPU win — 6-14x from 256² upward, with twenty of
thirty-two cells separated in the GPU's favour by disjoint sample ranges — and the `cost` arm has no
crossover anywhere in the measured range. The one regime the GPU loses is
materializing an image per evaluation at low K: at 512² and 1024² alike the CPU
is separated ahead at K=1, the two are indistinguishable at K=10, and the GPU is
separated ahead from K=50. That is a transfer, not a compute, result. Parameter upload is flat at about 10 µs
from K=1 to K=100, so pinning it would buy nothing, but the image readback runs
at 0.5-0.7 GB/s and costs 5.9 ms at 1024², over three times a complete
evaluation there. The pinned-memory condition recorded in `gpu-backends.md`
named the wrong transfer; reconsider pinning for the readback, not the
parameters.

Two things the measurement cost more than the result did. The host is a
contended interactive desktop under `powersave`, so only disjoint ranges support
a verdict and ten cells stay undecided; absolute times are depressed and belong
to this machine alone. And Go runs a cell's `-count` repetitions back-to-back,
which on such a host let one burst of load corrupt every sample of a single cell
— the first attempt reported K=50 slower than K=100. Eight separate passes of
the whole matrix at `-count=1` removed the inversions. Use passes, not `-count`,
on any machine you are also using.

The benchmark fails rather than skips under `CIRCLEFIT_REQUIRE_OPENCL=1` and
asserts either side of the measured loop that the renderer has not degraded to
its CPU fallback, because a degraded OpenCL renderer answers silently from the
CPU and would publish CPU timings under a GPU label.

The staged pipelines were measured on the same device and are the vendor-GPU
evidence Task 11.13 was waiting for: joint measured 1.3x *faster* on the GPU --
0.8x its time -- while sequential was 26x and batch 84x slower, all three
separated. Joint was also the only pipeline that did not create a session per
stage. PoCL had reported 190x and 120x; the ratios moved and the conclusion did
not. Task 11.13 tranche 1 has since acted on this evidence and the two staged
figures no longer hold; the record above is what it measured at the time.

### Task 11.10: Test GPU Correctness and Edge Cases
- [x] Test GPU detection and initialization on a prepared OpenCL runner.
- [x] Add golden-image visual comparisons against the CPU renderer.
- [x] Verify pixel-exact equivalence to CPU (within float tolerance)
- [x] Test with various circle counts and sizes
- [x] Test with overlapping circles
- [x] Test with edge cases (circles outside bounds, zero opacity)
- [x] Test with different image sizes
- [x] Validate cost computation accuracy
- [x] Document any differences or limitations

`renderer_opencl_correctness_gpu_test.go` holds the suite: a circle-count ×
canvas-size matrix, degenerate canvases, a named edge-case catalog (outside each
bound, straddling each edge and the corner, zero and negative radius, zero
opacity, canvas-covering radius, concentric and coincident stacks, a subpixel
centre walk), a compositing-order check, and a randomized deviation sweep. The
CPU render is the golden image; a committed PNG would only duplicate the oracle
and go stale against it, so a failing scene instead writes the CPU render, the
GPU render, and an amplified difference map to `$CIRCLEFIT_GPU_ARTIFACTS`.

**It found a real bug, and the bug is the interesting part.** The kernel tested
`dx*dx + dy*dy > radius*radius` per pixel — the pre-optimization disc test —
while the CPU span search starts at `int(centerX+0.5)` and never tests that
pixel, so every row the disc touches paints its nearest sample. The kernel
therefore dropped both tangent rows of every circle. That rule was written down
in `renderer-correctness.md` only after the CPU renderer was measured, and
nothing carried it to the kernel.

It had survived because it is rare and total rather than small and widespread,
which is the shape a tolerance-based parity test is worst at catching: about
0.0005% of pixels, each wrong by up to 226 of 255. Its cost effect is entirely
scene-dependent — 0.01% on a patterned reference, **a factor of two** on a
sparse one (a radius-1 black circle at `(10.5, 10)` on a 24×24 white canvas:
451.5625 on the CPU, 225.78125 on the device). The kernel now applies the CPU
predicate exactly, and both CPU geometry paths agree on it, so this matches the
renderer rather than one of its implementations. On the same catalog the worst
channel deviation went from **73 to 1**. Two smaller divergences went with it:
the kernel skipped `opacity < 0.001` where the CPU skips only exact zero, and it
did not reject rows before columns, which is what makes a zero radius agree.

What is left is arithmetic. The device is float32 end to end against a float64
CPU path, so parity is a budget — ±2 per channel, 1% relative cost — measured at
1 channel and 0.021% on the T550. The cost bound is the binding one and grows
with canvas size, because the SSD is accumulated in float32; a GPU cost is
therefore not comparable with a recorded CPU cost. `TestOpenCLDeviationBudget`
re-measures and reports both numbers on every run.

`TestOpenCLDeviceReportsAPreparedDevice` is what makes "prepared runner" mean
something: `InitOpenCL` falls back to a CPU device, so a PoCL-only machine
passes every parity test while validating no GPU at all. It fails under
`CIRCLEFIT_REQUIRE_GPU_DEVICE=1` unless the selected device is of type GPU.
That is deliberately a second switch: `CIRCLEFIT_REQUIRE_OPENCL=1` only means
"do not skip", and `ci-gpu-compile.yml` sets it while running on PoCL's CPU
device on purpose. Overloading the one flag made that CI job fail, which is
exactly the confusion the split now prevents.

Review found the second way this suite could report success while testing
nothing, and it is the mirror of the first. A device that fails *after*
initialization degrades the renderer permanently and silently, so a parity
assertion made afterwards compares the CPU oracle against the CPU fallback and
passes. `newOpenCLTestRenderer` now fails the test at teardown if the renderer
it handed out ended up degraded, which covers every test built through it rather
than the ones that remembered to ask -- the same guard the matrix benchmark
already applied around its measured loop.

Validated on an NVIDIA T550 (driver 580.178.04, OpenCL 3.0 CUDA). AMD and Intel
remain unmeasured, for parity as well as throughput.

### Task 11.11: Handle GPU Errors and Fallback
- [x] Add graceful error handling for GPU initialization failures
- [x] Provide fallback to CPU if GPU unavailable (opt-in, not automatic)
- [x] Add logging for GPU-related errors
- [x] Test error scenarios (no GPU, driver issues, out of memory)
- [x] Document common GPU issues and solutions

**"Automatic fallback to CPU" was the one item that had to change, and the
reason is Task 11.10's result.** The device computes in float32 against a
float64 CPU path, so a cost recorded under one backend is not a baseline for the
other. A fallback that fired by default would answer a `--backend opencl` run
with CPU numbers under a GPU label, which is worse than answering with nothing.
So the default stays "the job fails", and `backendFallback` — accepted only as
`cpu`, unset by default, so every configuration and checkpoint written before it
existed keeps failing loudly — is how a caller says the run matters more than
the device. When it fires, the job records `effectiveBackend`, so the
substitution is in the record rather than only in a log line.

Three failures were conflated before and are now separate, because callers
answer them differently. *Not built*: no `gpu` tag, decided from the build
alone. *Not available*: built, but no runtime, no device, or a context that
would not initialise. *Failed mid-run*: the device started and then stopped
working.

The first is now refused before it costs anything. `SupportedBackends` is
build-tag aware, so a portable binary stops advertising `opencl` in
`GET /api/v1/system`; `serve --backend opencl` refuses to start rather than
failing every job that names no backend of its own; and a job naming the backend
explicitly is rejected at submit instead of on a worker minutes later. The check
is `renderer.BackendAvailable`, which reads the build and deliberately probes no
device: `app.JobConfig.Validate` still accepts `opencl` everywhere, so a
checkpoint written on a GPU host resumes there. Two exemptions, both deliberate
— a backend inherited from the server default is not the submitter's doing and
the operator was already told at startup, and a configured fallback is exactly
the statement that this backend is not required.

**The mid-run case is the one that was actually invisible, and it is the reason
this task was worth doing.** `Cost` and `Render` have no error return, so on a
device error the OpenCL renderer sets `degraded`, logs one warning, and answers
everything afterwards from its CPU fallback. `Degraded()` existed but was called
only by tests and benchmarks — it reached no DTO, no trace, no UI — so a run
that degraded was reported as a normal OpenCL run. It now surfaces through
`renderer.Degraded`, a free function in the shape of `EvaluationWidth` that
returns false for any backend that cannot degrade, and lands on the job as
`backendDegraded` beside `effectiveBackend`, in the CLI status output and on the
job detail page. That matters more than a label usually would: the run spent
part of its budget on each backend, so its best-so-far spans two arithmetics and
the cost is comparable with neither a clean GPU run nor a clean CPU one.

Neither field is persisted, matching `EvaluationWidth`. They describe one
process's run, so a job restored from a checkpoint reports nothing rather than a
stale value.

What the tests do not cover is honest to state: there is no automated mid-run
device failure. Inducing one needs a fault-injection hook in the cgo path or a
card that can be made to fail on demand, so what is tested is the accessor and
the whole reporting path, while the device event that triggers them is not. Out
of memory is documented rather than tested, because a test would have to
allocate gigabytes on the developer's machine to provoke it. The build-level
unavailable case *is* tested, and without a GPU: the `!gpu` suites pin that
`SupportedBackends` omits OpenCL, that `BackendAvailable` separates "not built"
from "unknown name", that an unavailable backend fails by default and falls back
only when asked, that a misspelled backend never falls back, and that a job
which fell back records `cpu` while keeping `opencl` as its request.

Review caught the one thing the first cut got wrong, and it was the part that
mattered. `NewSession` builds a fresh renderer, so a per-renderer `degraded`
flag meant a sequential or batch run -- where every circle is costed on an
independent session and the base renderer may never evaluate at all -- reported
a clean device no matter what happened. The flag is now one record shared by a
renderer and every session derived from it. Joint mode was never affected:
OpenCL withholds the concurrent-evaluation marker, so joint evaluates on the
base renderer itself and creates no sessions.

Sharing it runs both ways, which removes what would otherwise have been a
residual for Task 11.13: a session created after the device is gone starts
degraded instead of rediscovering it, so a lost device costs one timeout for the
run rather than one per stage. The `engine.poison()` that tranche was going to
need is no longer required for this.

### Task 11.12: Documentation and Examples
- [x] Document the macOS Metal/WebGPU gap and driver quirks found during vendor-GPU validation.
- [x] Update CLAUDE.md with GPU architecture
- [x] Document GPU requirements and setup
- [x] Add example commands using GPU backend
- [x] Document performance comparisons
- [x] Add troubleshooting section for GPU issues
- [x] Document when to use GPU vs CPU

Two of these bullets were decisions rather than write-ups, and they are the part
worth recording.

**macOS is now a decision, not a gap.** `gpu-backends.md` had framed the missing
macOS path as pending a Metal or WebGPU backend, and its recommendation section
still asked for an OpenGL fragment-shader fallback that was never built. Both are
now recorded as closed: **there is no GPU backend on macOS and none is planned.**
Apple deprecated OpenCL and Apple Silicon ships no implementation without a
third-party ICD, so there is nothing to target; a Metal backend would be a third
renderer with its own kernels, its own float32 parity budget against the float64
CPU path, and its own CI runner, for a platform where no measurement says the GPU
would win; and the one pipeline OpenCL currently wins is joint mode, so porting
this state to a second API buys a second copy of the same problem. The condition
to revisit is written down: Task 11.13 has made the staged path competitive on
hardware that already exists, *and* an Apple Silicon runner can gate parity.

**OpenCL stays experimental, and the reason is now specific rather than a
posture.** Parity and throughput are established on one vendor GPU; AMD and Intel
are unmeasured for both; there is no required real-device CI runner, because the
GPU gate runs PoCL on a CPU; and the staged pipelines are 26x and 84x slower than
the renderer they are meant to accelerate. `support-matrix.md` now says that in
one place rather than leaving "experimental" to be inferred.

That fourth reason did not survive Task 11.13 tranche 1, which is recorded
below. The verdict did: the other three are about coverage rather than speed,
and no optimization answers them.

The rest is documentation of what 11.9-11.11 established. `gpu-backends.md`
gained the operational half it never had -- requirements split into the three
things that fail differently (the build tag, the headers and loader, the device),
per-platform setup, worked commands for the CLI, `serve`, the API and a
benchmark, and a when-to-use-which table driven by the measured report. Its
design sections are now explicitly the selection record rather than the reading
order. `AGENTS.md` gained a GPU architecture section stating the four things that
change what a proposal should say and are invisible from the package list; both
it and `architecture.md` had claims that vendor-GPU characterization was still
open, which stopped being true with Task 11.9. `troubleshooting.md` gained the
build-failure case and the "it is slower than the CPU" case, which is usually not
a fault but a staged-mode run. `benchmarks.md` says it is the CPU suite and where
the GPU benchmarks are.

Two rules are now stated wherever a reader could form a wrong comparison, because
each has already been got wrong once: **a GPU cost is not a CPU cost** (float32
against float64, a budget rather than byte-equality, and the bound grows with
canvas size), and **the requested backend is not the record** -- `effectiveBackend`
and `backendDegraded` are.

Review found two places where the documentation described a guarantee the code
did not provide, and both were fixed in the code rather than softened in the
prose.

`CIRCLEFIT_REQUIRE_GPU_DEVICE` was inert in exactly the command this page tells
you to run. Only `TestOpenCLDeviceReportsAPreparedDevice` read it, and a
benchmark invocation passes `-run '^$'`, so it executes no test: the documented
measurement loop could complete on a PoCL-only host and publish CPU OpenCL
timings as a GPU measurement. The benchmarks now read the switch themselves.

And a one-shot CLI run had no provenance at all. `effectiveBackend` and
`backendDegraded` are fields on a *job*; `circlefit run` creates none, logged
only a fallback warning at startup, and never consulted `renderer.Degraded` on
completion -- so the entry point most likely to be used for a quick GPU
comparison was the one that could not say which backend produced the number.
`run` now names the backend that actually ran, and says why the cost is not
comparable, whenever that is not the backend that was asked for.

Deliberately not done: no per-vendor setup instructions beyond naming the ICD
packages, because only two combinations have actually been run here (Ubuntu CI on
PoCL, and Linux on an NVIDIA T550) and anything else would be documentation of
something untried.

### Task 11.13: Optimize OpenCL/PoCL Pipeline Performance

Measured baseline this task started from, on an NVIDIA T550 (`docs/gpu-performance-report.md`); tranche 1 has since moved the two staged figures, and what they became is recorded after the bullets. The uncached 64x64, K=12 pipeline benchmark reported joint at 0.8x the CPU's time -- the GPU winning -- while sequential was 26x and batch 84x slower, all three separated. Joint creates no session; sequential creates 5 and batch 9. `BenchmarkOpenCLSessionCreation` priced one at 0.36-0.65 s, three orders of magnitude above any per-evaluation cost in the package, and the arms did not separate `NewSession` from a cold `New` -- which is what `NewSession` calling `newRenderer` verbatim predicts. Repeated context and program initialization was therefore the whole of the staged loss, and removing it is what tranche 1 did.

This paragraph previously carried the PoCL figures (3x / 190x / 120x, 14 and 5 sessions, ~45 ms/session). Those were superseded by Task 11.9 and are kept as a dated record in `docs/gpu-backends.md`; they describe a CPU pretending to be a device and must not be compared against a run made today.

- [x] Share OpenCL resources across renderer sessions
  - [x] Introduce an owned shared device engine for the selected device, context, queue, compiled program, reference buffer, and workgroup configuration
  - [x] Make staged sessions allocate only their mutable kernels/buffers instead of calling `InitOpenCL` and `clBuildProgram` again
  - [x] Define safe shared-resource ownership and cleanup for normal completion, partial initialization, and CPU degradation
  - [x] Add an isolated session-creation benchmark and verify that kernel compilation occurs once per base renderer
- [ ] Add accumulated-canvas support to the OpenCL staged pipeline
  - [ ] Implement `newSessionWithCanvas` and `initialCanvas` so sequential and batch stages evaluate only newly added circles
  - [ ] Accept a packed `uchar4` base-canvas buffer in the render/cost kernel instead of assuming white
  - [ ] Upload the retained canvas at most once per stage as the first implementation
  - [ ] Investigate a device-resident retained-canvas handoff to eliminate stage-boundary readback/upload
- [ ] Reduce per-evaluation synchronization and memory traffic
  - [ ] Add a cost-only execution path that omits full output-buffer writes during optimizer evaluations and materializes the final/best image on demand
  - [ ] Profile a C/OpenCL-owned asynchronous parameter staging path; retain blocking writes unless measurements justify the added ownership complexity
  - [ ] Design a batched objective interface so optimizer populations can share kernel launches and scalar synchronization
- [ ] Optimize the render kernel based on profiling
  - [ ] Precompute radius squared, premultiplied color, opacity, and inverse opacity into aligned circle records
  - [ ] Evaluate constant-memory circle parameters and device-specific preferred workgroup sizes
  - [ ] Investigate order-preserving tile/bin circle lists so pixels skip non-overlapping circles
- [ ] Preserve semantics and fallback behavior
  - [ ] Extend CPU/OpenCL parity tests across joint, sequential, and batch modes after each optimization
  - [ ] Verify cache invalidation, lazy image materialization, cleanup, and permanent CPU degradation paths
- [ ] Re-run the complete backend pipeline benchmark after each tranche
  - [ ] Record vendor-GPU before/after medians, allocations, session counts, and evaluation counts, as interleaved single passes rather than `-count=N`; PoCL only for lifecycle and allocation deltas
  - [ ] Run the same benchmark on supported AMD, Intel, and NVIDIA OpenCL devices where available
  - [ ] Document crossover points and retain optimizations only when profiling demonstrates a benefit

**Tranche 1 is done, and it did what it was supposed to do.** `engine` owns the
runtime, context, queue, compiled program, reference buffer and reduction
workgroup size; `NewSession` takes a reference to its parent's engine instead of
calling `InitOpenCL` and `clBuildProgram` again, and allocates only its own
kernel pair and the buffers its circle count needs. Session creation went from
182.8 ms to 12.1 us at 64x64 and from 213.9 ms to 220.6 us at 512x512, at 48 to
12 allocations. **Those are corrected figures.** The originally recorded 4.38 ms
and 4.04 ms were a benchmark defect, not a measurement: the arm held the base
renderer with `defer release()`, so tearing down the program, context, queue and
runtime ran inside the timed region and `-benchtime=20x` divided that one-time
cost by twenty and reported it as per-session. The arm now stops the timer
first, and is stable across `b.N`. The correction runs in the conservative
direction -- tranche 1 was roughly two orders of magnitude better than claimed.
Measured before and after as alternating single passes between two worktrees,
sequential moved 83.8x and batch 85.9x, both separated; the CPU control arms
moved 0.98x and 0.69x, which is what makes the OpenCL arms believable. Session
counts are unchanged at 0/5/9. See
[`docs/gpu-performance-report.md`](docs/gpu-performance-report.md).

**What it did not do is make the GPU win.** Eight further passes put sequential
at 1.12x the CPU and batch at 1.08x, with no cell separated -- the staged path
went from a decisive, separated loss to indistinguishable, which removes a
disqualification rather than establishing an advantage. This host cannot
separate joint either (1.00x), so the run neither confirms nor contradicts the
0.8x recorded under Task 11.9. Do not quote either number as a current win.

**That settles the remaining tranches, and mostly against doing them.** They
were justified by a 26x/84x gap that no longer exists, so each now needs its own
measurement rather than an inherited one. Two are already answered and should be
closed rather than built: the pinned parameter-staging investigation, because
parameter upload is flat at about 10 microseconds from K=1 to K=100 and is
latency-bound; and the `engine.poison()` the design anticipated, because Task
11.11's shared degradation record already discovers a lost device once per run.
Tranche 2, the accumulated canvas, is the only one with a standing rationale --
the staged modes still replay every retained circle, so sequential runs 121
evaluations to the CPU's 121 while rendering strictly more per evaluation -- but
it should be opened by a profile of where the remaining time goes, not by the
old ratio.

**That profile has now been taken, and it justifies tranche 2 decisively.** The
argument is not the old ratio; it is that the CPU renderer implements
`accumulatedSessionFactory` and the OpenCL renderer does not, so staged CPU work
grows with the circle count while staged OpenCL work grows with its square.
`BenchmarkStagedEvaluationAtDepth` measures one evaluation that appends a circle
to D retained ones, in three arms so that backend is separated from technique.
The CPU's accumulated arm is flat in D (30.7-60.5 us at 128 square, 218.6-444.4
us at 512 square, across a 64-fold change in depth); both replay arms grow
linearly. At 512 square, D=512 the GPU beats the CPU by 6.1x on the *same*
replay work and still loses to the CPU's accumulated canvas by 8.1x, separated.
The crossover sits between D=32 and D=128. Campaigns in
[`docs/schedule-format.md`](docs/schedule-format.md) run to 1000-3000 circles
with `additionalCircles: 1`, which is exactly the deep-prefix, one-new-circle
shape that is furthest behind.

**No existing benchmark could have shown this, and that is itself a finding.**
Every pipeline benchmark in the package fixes K at 12, the one regime where the
two growth rates cannot separate. `BenchmarkOptimizeStagedGrowth` sweeps whole
pipelines to K=128 and still reports a flat 1.3-1.4x, because its stages run
eight evaluations each and per-stage setup dominates them; a real stage runs
hundreds. So tranche 1's "staged OpenCL is indistinguishable from the CPU" is a
statement about K=12 and does not survive to campaign depths. It is not
withdrawn -- it is bounded.

One observation from the same profile, recorded and not proposed: what remains
in a 512 square session is a 1,050,778-byte eager `image.NewNRGBA` for
`renderImage`, not device work, which a phase breakdown puts at about 14 us of
the 220.6 us. A session only needs that image if something calls `Render`.

**Two defects surfaced that the benchmark would never have shown.** Sharing a
queue means a session waits on a handle it does not own, so `releaseOwn` needed
a `clFinish` -- the first in the package -- and clearing the borrowed handles
afterwards, without which a second teardown called `clFinish` on a released
queue and died with `rip 0x0`. And `ProgramBuilds() == 1` is not by itself
evidence of a single compile: a per-session engine has its own counter and also
reports 1. It is evidence only alongside the runtime-pointer identity check, and
both are now asserted, in the unit test and end to end through the pipelines.
Separately, the GPU gate selects tests by name, and the one test asserting a
cross-session invariant did not match the selector and had never run; a new step
now fails the build if any gpu-tagged test name falls outside it.

Phase 11 is complete only when Tasks 11.9–11.13 establish vendor-GPU
performance and parity, deliberate fallback behavior, and accurate operational
documentation. OpenCL remains experimental until then.

## Phase 12: UX & Visualization Polish

### Completed Tasks 12.1–12.8 and 12.10 ✅

The UI now has five comparison modes, Turbo/Magma difference heatmaps, live
Cost/PSNR/optional-SSIM metrics, parameter inspection/export, downloadable
images and self-contained reports, richer progress visualization, pause/resume/
cancel/delete actions, and persistent user preferences. See
`docs/advanced-quality-metrics.md`, `docs/difference-heatmaps.md`, and
`docs/html-reports.md`. Optional circle-hover and parameter-sorting ideas were
not carried forward as roadmap commitments.

### Task 12.9: Responsive Design, Accessibility, and Browser Validation (P2)

This task also owns the Safari/mobile checks deferred from Phase 7.

- [x] Validate phone, tablet, and desktop layouts; add or correct breakpoints so
  images and comparison modes stack cleanly. Shared wrap/scroll utilities live in
  `layout.templ`; every auto-fit grid uses `minmax(min(N, 100%), 1fr)` so a track
  can never exceed its container. A spec asserts no page scrolls sideways at 320,
  375, 768, 1024 and 1440px.
- [x] Audit WCAG 2.1 AA essentials: alt text, contrast, labels, focus order,
  keyboard navigation, and a screen-reader pass. `@axe-core/playwright` sweeps
  every page in both themes on each engine. Contrast was the substantive finding:
  the accent tokens stayed light across both palettes while the foreground token
  flipped, so dark-mode buttons measured 2.54:1 and 1.51:1.
- [x] Add missing loading/skeleton and SSE-connecting states. `useLiveResource`
  now reports `connecting`/`connected`/`reconnecting` through one shared
  `role="status"` region instead of five copies that all claimed "reconnecting"
  on a healthy first paint.
- [x] Exercise supported browser sizes and engines, including Safari on macOS.
  `ci-web` runs Chromium, WebKit, iPhone and iPad. **Safari proper is covered by
  the manual checklist in `docs/browser-support.md`, not by CI** -- Playwright
  ships WebKit built for Linux, and one open finding turns on that difference;
  see `docs/known-limitations.md`.
- [x] Revalidate all live view modes, downloads, reports, metrics, controls, and
  preferences during the browser pass. Automated where it can be; the rest --
  real downloads, printing, VoiceOver, zoom reflow -- is in the manual checklist.

## Phase 13: Robustness, Docs, and Packaging

### Completed or Superseded Work ✅

Error handling, typed validation, safe paths, request logging, the README,
benchmark and limitation documentation, SemVer metadata, changelog, license,
contribution guide, cross-build/release automation, and CI gates are
implemented. Production hardening and final end-to-end release acceptance moved
to Phase 14, which is authoritative where this historical phase overlapped it.
Per-client rate limiting is not carried forward for the documented
trusted-local server; bounded admission and resource limits are the active
contract.

### Task 13.15: Remaining Documentation, Examples, and Observability (P3)

- [ ] Audit structured logging fields/levels and document logging
  configuration; add measured slow-operation/progress logging where useful.
- [ ] Decide whether a disabled-by-default Prometheus endpoint is warranted
  before adding a new public surface.
- [ ] Add a focused `docs/getting-started.md` covering CLI, server, UI, API,
  artifacts, and common configuration from a clean installation.
- [x] Add `docs/architecture.md` with current package boundaries, lifecycle,
  persistence, SSE, schedule, frontend-island, CPU/SIMD, and experimental GPU
  flows.
- [ ] Curate small redistributable examples with documented settings and
  expected qualitative results; add a deterministic example script and an
  appropriate CI smoke test.
- [ ] Decide whether a separate public roadmap/issue-tracker page adds value
  beyond this plan and `docs/known-limitations.md`.

Badges, promotional screenshots, a walkthrough video, source-file copyright
headers, and a code of conduct remain optional publication work rather than
active engineering tasks.

## Phase 14: Production-Readiness Remediation 🚨 RELEASE GATE

The 2026-08-09 audit remediation is implemented: reproducible tooling,
trusted-local browser and filesystem boundaries, canonical typed configuration,
race-safe lifecycle/SSE state, cancellable optimizer execution, corrected
renderer and staged-pipeline semantics, durable snapshots/checkpoints/traces,
safe store ownership, portable SIMD dispatch, typed CLI/API contracts, and
release-gating workflows. Regression coverage and documentation accompany each
area. Historical local and CI observations remain evidence for their recorded
revisions only.

### Task 14.13: Final Release Verification (P0)

These checks were moved from Tasks 14.9, 14.11, and 14.12 so the completed
remediation tasks no longer look partially open.

- [ ] Observe every required CI gate, including all supported cross-builds,
  generation, race, vulnerability, GPU compile, and core end-to-end checks,
  passing from a clean clone on two consecutive runs of the release candidate.
- [ ] Verify repository/release controls prevent producing or publishing a
  release while any required gate fails; document any administrator-enforced
  setting that cannot be expressed in workflow files.
- [ ] On a clean user machine, follow the README verbatim and complete a small
  CLI job and a server/UI job without workspace preparation.
- [ ] After those checks pass, remove the production-readiness caveat at the top
  of this document and mark Phase 14 complete.

## Phase 15: Polishing Throughput

The initial production profile found serial polishing and active-set selection
dominating long incremental runs. Tasks 15.1 and 15.2 removed those two
bottlenecks; the remaining work targets selection quality, evaluation width,
dirty-region scoring, short-stage correctness, and fixed per-extend cost.
Measurements must continue to state host, workload, budget, and allocations.

### Task 15.1: Give Polishing a Session Pool (P1) ✅

Completed: polishing now leases independent renderer sessions per concurrent
evaluation while keeping sweep acceptance transactional and deterministic for a
fixed width. Race/parity coverage and width benchmarks are recorded in the
code and plan history; on the measured 12-thread host, width 8 improved the
production-shaped 256/512-circle sweep estimate by about 4.1–4.3×. See
`docs/polishing-throughput-report.md`.

### Task 15.2: Make Active-Set Selection Cheap (P1) ✅

Completed: leave-one-out audits and residual-region influence work are
parallelized over isolated sessions, influence rendering is clipped to the
region/circle intersection, and incumbent audits are reused after rejection.
Selection remains equivalent to the serial implementation; the measured
512-circle residual-region selection fell from 4.24 s to 0.47 s on the stated
12-thread host, with the transient-memory tradeoff documented in
`docs/polishing-throughput-report.md`.

### Task 15.3: Stop Destroying the Baked Prefix (P2)

Direct evidence, measured on the live 512x512 vector: `min(activeCircles)` under
`residual-region` is 7 of 256 circles and 11 of 512, so the bake covers 3-4% of
the vector. The optimization the comment on `bakedSuffixSession` describes is
therefore nearly inert on the strategy production actually runs. The other half
of the measurement argues against chasing it: per-candidate cost rises only 9%
for a doubling of the circle count (1 881 us at 256, 2 056 us at 512), because
the full-canvas clear and the full-image SSD pass dominate rather than
rasterization, so recovering the prefix recovers much less than the 3-4% figure
suggests. Keep this at P2 and behind a measured quality comparison.

- [x] Measure how much of the per-candidate cost the prefix actually recovers,
      by recording `min(activeCircles)` and per-candidate rasterization count per
      sweep. Measured: prefix 7/256 and 11/512, per-candidate 1 881 us and
      2 056 us, i.e. +9% for twice the circles.
- [ ] Evaluate biasing selection toward later draw slots when region energy is
      close, so the prefix stays large without abandoning merit-based selection.
      This changes what polishing selects, so it ships only with a measured
      quality comparison, not on the cost argument alone.
- [ ] Record the outcome in `docs/contiguous-window-polish-report.md`, which
      currently documents the cost argument for `contiguous-window` but not the
      prefix collapse in the merit-based strategies.

**Acceptance Checks:**

- [ ] A report compares final cost and wall clock for the current and
      prefix-aware selection at equal optimizer budget, on the same seed.

### Task 15.4: Right-Size the Polishing Defaults (P2) ✅

Completed: polishing has its own population setting and measured defaults
(`pop=30`, 200 iterations × 2 epochs, up to 8 sweeps, stagnation 100).
`docs/polishing-budget-report.md` records the sweep behavior and equal-host
comparison; schedule estimates and backward-compatible normalization use the
same canonical constants.

### Task 15.5: Derive the Evaluation Width from a Measurement (P2)

`EvaluationWorkers` resolves to `Threads` when it is zero
(`internal/app/config.go:321`) and `effectiveEvaluationWorkers`
(`internal/fit/renderer/renderer_cpu.go:395`) clamps it to `GOMAXPROCS`, so a
default configuration ends up running as many concurrent evaluations as the
machine has hardware threads. That is the core count talking, not a
measurement, and the measurement disagrees.

On the AMD Ryzen 5 4600H, 12 threads, `GOMAXPROCS=12`, at 512x512 with
`residual-region` and `activeSetSize 8` (the Task 15.1 methodology: fitted over
4 000 and 16 000 candidates, extrapolated to a 690 000-candidate sweep), width
12 was reproducibly slower than width 8: 3.04x against 4.09x at 256 circles and
3.60x against 4.33x at 512. The result held at both circle counts, on both
revisions, and for medians and minima alike. It also costs more memory —
101.8 MB at width 12 against 80.5 MB at width 8, on 16 000 candidates at 512
circles — because a slot holds ~5.3 MB at 512x512. Running one evaluation
goroutine per hardware thread leaves nothing for the runtime and saturates
memory bandwidth.

- [ ] Replace the `EvaluationWorkers = Threads` default with one derived from a
      measurement across widths, not from the core count.
- [ ] Establish whether the right rule is a fraction of `GOMAXPROCS`, a fixed
      headroom below it, or something image-size dependent, by benchmarking
      widths on more than one machine and more than one canvas size. One data
      point on one 12-thread box is not enough to pick a formula.
- [ ] Document the chosen rule next to `EvaluationWorkers`
      (`internal/app/config.go:183`) with the measurement behind it, and keep the
      explicit setting authoritative so an operator can still override it.

**Acceptance Checks:**

- [ ] A benchmark table shows sweep cost against evaluation width on the stated
      machines, and the default the code picks is the width that table
      recommends.
- [ ] An explicitly configured `EvaluationWorkers` is still honored up to the
      `GOMAXPROCS` clamp, with a test covering it.

### Task 15.6: Stop the Acceptance Gate Vetoing Every Sweep (P1) ✅

Completed: sweep acceptance now requires every active circle to remain useful
without increasing the inherited set of non-useful circles, instead of
requiring a sweep to repair untouched circles. Incumbent audits are cached,
decisions are logged, and regression tests cover both legitimate acceptance and
newly killed circles. On the recorded 64-circle stalled checkpoint, the same
budget changed from 0/12 accepted and no gain to 12/12 accepted and 44.64 cost
removed. The post-fix strategy comparison moved to Task 15.10.

### Task 15.7: Score Only the Pixels a Candidate Can Change (P1)

Measured 2026-08-20 on the live 2 111-circle fit of `Christian_after.jpeg`
(512x512), one `replacement` sweep at `activeSetSize` 100, `polishingEpochs` 2,
`polishingIters` 400, `polishingPopSize` 60:

- the sweep cost **599 s** and returned **0.000**, at **6.24 ms per candidate**
  (96 000 candidates, counting the mayfly's male and female populations);
- the 100 selected circles have a summed disc area of **326.6 px^2 — 0.125% of
  the canvas** — yet every candidate clears and SSD-scores all 262 144 pixels;
- the reported rate implies **~2 521 circle rasterizations per candidate**,
  i.e. the whole vector is redrawn to move 100 circles.

This is the same per-candidate cost Task 15.3 measured at 2 056 us on 512
circles, now 6.24 ms on 2 111 — 3x for 4x the circles. That inverts 15.3's
conclusion: the full-canvas clear and full-image SSD no longer dominate at this
scale, rasterization does, because the baked prefix has collapsed to **10.7% of
the vector** (earliest active draw index 226 of 2 111).

Changing an active circle can only alter pixels inside that circle's disc, under
its old or its candidate parameters. Everything else — including every later
circle drawn over untouched ground — is bit-identical to the incumbent, so both
the recomposite and the SSD can be restricted to that region and the remainder
carried as a precomputed constant. Unlike 15.3's remaining bullet, this changes
no selection and no result, only the work done to reach it.

Two traps the measurement already exposes:

- **A single union bounding box buys nothing.** Merit-based selection scatters
  the active set across the canvas: the union bbox of those same 100 circles is
  **71.4% of the canvas** against 0.125% of true disc area. The dirty region has
  to be a per-circle rect list or a tile mask, not one rectangle.
- **The region is per-candidate, not per-sweep.** `NewBounds` lets a radius grow
  to `max(W, H)`, so a candidate may legitimately propose a 400 px circle where
  the incumbent had a 1 px one. The affected set is the union of the old and new
  discs, computed from the candidate's own parameters, with a full-canvas
  fallback when that union exceeds a break-even fraction.

- [x] Add a dirty-region evaluator: baseline composite plus baseline SSD total,
      per-candidate affected-pixel set from old-disc union new-disc, recomposite
      and rescore only those pixels, and fold in the constant remainder.
- [x] Fall back to full-canvas evaluation when the affected fraction exceeds the
      measured break-even point, so a large-radius candidate cannot be slower
      than it is today.
- [ ] Re-measure the sweep above and record per-candidate cost against affected
      fraction in `docs/contiguous-window-polish-report.md`.
- [x] Re-check whether 15.3's prefix-aware selection is still worth its quality
      risk once evaluation no longer scales with the prefix.

**Acceptance Checks:**

- [x] A test asserts the dirty-region evaluator and the full-canvas evaluator
      return the same cost for the same parameters, across active sets that are
      scattered, clustered, canvas-edge-crossing, and radius-growing.
- [x] A benchmark reports per-candidate cost at 512 and 2 000+ circles for both
      evaluators, including the affected fraction and the fallback rate.
- [ ] The 599 s sweep above is re-run at equal budget and its wall clock
      recorded; the cost it reaches is unchanged.

### Task 15.8: A Short Batch Stage Must Not Strand the Run (P1) ✅

Completed: `refill_limit` remains a successful batch outcome at its produced
size. Job resources report requested and actual counts explicitly, and extend
or polish continuations rebase a short checkpoint to its actual size. A `+N`
extend therefore targets the produced count plus N. The pipeline keeps its seed
stable; callers may deliberately retry with another seed without losing the
usable short checkpoint.

Hit on 2026-08-20 at 2 813 circles. An extend to 2 814 finished
`termination: refill_limit` with `requestedCircles` 2 814 and `actualCircles`
2 813: the batch pipeline pruned the appended circle as not earning its place,
refilled, and exhausted `MaxExtraBatchStages` one circle short. The job is
reported `completed` with a plausible `bestCost`, but every continuation from it
fails `POST /api/v1/jobs/{id}/extend` with

    400 {"error":{"code":"invalid_checkpoint",
                  "message":"extension requires a complete batch checkpoint"}}

so the job is a permanent dead end that looks healthy from the API. A chain
driver retrying the same parent cannot make progress; recovery meant rolling
back to the last complete ancestor and re-running with a different seed, which
succeeded on the first attempt — so the condition was seed-specific, not a
ceiling of the arrangement.

Two defects, worth separating:

- **The state is unreachable through the API.** `GET /jobs/{id}` reports the
  requested circle count from `config.circles`; only the on-disk checkpoint
  carries `actualCircles`. A caller cannot tell a complete job from a stranded
  one without reading the store directly.
- **A pruned refill is treated as success.** Finishing short is a legitimate
  outcome of the pruning gate, but it should not be silently reported as a
  completed extension of the requested size.

- [x] Surface the produced circle count on the job resource, so
      `actualCircles < requestedCircles` is visible to any client.
- [x] Decide and document the contract for a short stage: either fail the job
      with a typed error naming `refill_limit`, or accept it as a complete
      checkpoint at its actual size so continuations remain legal. Silently
      completing at a size that no continuation accepts is the one option to
      rule out.
- [x] Consider reseeding inside the pipeline when a refill exhausts its stages,
      since a fresh seed cleared this immediately.
- [x] Record the failure and its recovery in `docs/troubleshooting.md`, next to
      the existing JSON API error envelope material.

**Acceptance Checks:**

- [x] A test drives the pipeline into `refill_limit` and asserts the chosen
      contract holds — either the typed failure, or an extend that succeeds from
      the short checkpoint.
- [x] A test asserts the job resource distinguishes requested from produced
      circles.

### Task 15.9: Cut the Fixed Cost of a Single-Circle Extend (P2) ✅

Measured 2026-08-20 at 2 000 circles, one `+1` extend per point, `popSize` 30,
`optimizerEpochs` 1:

| `iters` | wall clock |
|---|---|
| 50 | 2.0 s |
| 500 | 6.0 s |
| 1 500 | 12.0 s |

That is 0.0069 s per iteration plus **~1.66 s that does not depend on the
optimizer budget at all** — 28% of a 6 s extend at the shipped `iters` 500.
Growth campaigns run one circle per extend (Task 16.9), so this is paid once per
circle: roughly 28 minutes across a 1 000-circle leg.

The suspected contributors are per-extend rather than per-iteration: rebuilding
the session and its baked prefix, revalidating the full parameter vector, and
writing the checkpoint — which is a single JSON document that grew from 334 KB
at 2 000 circles to 469 KB at 2 813, rewritten in full on every extend.

- [x] Profile one extend at 2 000+ circles and attribute the fixed 1.66 s across
      session setup, validation, checkpoint serialization, and trace append.
- [x] Address whichever term dominates. If it is the checkpoint, consider
      writing parameters in a binary form or appending only the delta, keeping
      the JSON document as an export rather than the hot path.
- [x] Re-measure the table above and record the new intercept.

Implemented: CPU extensions reuse a cost-verified parent `best.png` as their
retained canvas, suffix-pool sessions share its immutable background, and the
pipeline/final checkpoint reuse the accumulated result image. Full-vector
validation, trace sync, and JSON checkpoint persistence were measured but were
not the dominant term, so the lossless JSON format remains authoritative. The
component profile, allocation counts, end-to-end table, intercept, competing-
load caveat, and reproduction commands are in
[`docs/single-circle-extend-report.md`](docs/single-circle-extend-report.md).

**Acceptance Checks:**

- [x] A benchmark reports the fixed and per-iteration terms separately at 500,
      2 000, and 3 000 circles, so the intercept's growth with circle count is
      visible rather than inferred.
- [x] Checkpoint round-trip stays lossless: a resumed job reproduces the parent
      cost exactly.

### Task 15.10: Refresh Post-Fix Polishing Evidence (P2)

- [ ] Re-measure `BenchmarkPolishStrategyQualityAfterBatchFit` after the Task
  15.6 gate correction and refresh `docs/contiguous-window-polish-report.md`;
  the old ranking was partly determined by which active set happened to cover
  inherited blocker circles.

### Task 15.11: Spend a Stage's Budget as Restarts, Not One Long Run (P1)

A budget-matched restart ladder over twelve paired blocks measured the base
stage's budget spent as one long run against the same budget spent as several
independent cold runs, keeping the best. Splitting it is worth about 160 cost
points, wins every block at eight and sixteen restarts, and survives a hard
evaluation cap; four restarts of 64 iterations beat one 2048-iteration run by
88 points on 15% of the compute. The mechanism is measured: population spread
falls below 10% of its initial value by iteration 11-16, after which 80-96% of
the run's gain is already banked. See
[`docs/restart-vs-budget-report.md`](docs/restart-vs-budget-report.md).

- [ ] Measure `--optimizer-epochs` against cold restarts at a matched budget
  before settling on any API. An epoch advances to a fresh deterministic seed
  and, with no continuation profile, seeds only half the population around the
  incumbent while re-initializing the rest, so it already performs substantial
  re-initialization. Every ladder arm ran with `optimizerEpochs: 1`, so this
  comparison is unmeasured.
- [ ] Decide the surface, once that comparison exists. A full restart differs
  from an epoch in independent re-initialization of the whole population plus
  best-of selection; if epochs already capture most of the gain, tune them
  rather than adding a second mode.
- [ ] Implement restarts for the base stage on the CLI, the job config, and
  the schedule format, keeping determinism per seed.
- [ ] Re-measure on a second reference image before changing any default; the
  ladder covered one image, `variant` standard, and the eight-circle base
  stage only.
- [ ] Measure whether extend and polish stages benefit. They start from a
  fitted vector rather than a cold population, so the collapse dynamics there
  are unmeasured.

## Phase 16: Declarative Run Schedules ✅

Schedules replace one-off external orchestration with a persisted,
server-owned campaign: validate and estimate a declarative document, execute
base/extend/polish stages in order, survive restarts, and inspect the resulting
chain through the API, CLI, and UI. The format and cost-comparison rules are
authoritative in `docs/schedule-format.md`.

### Completed Tasks 16.1–16.8 ✅

Implemented: versioned schedule models and validation; durable server-side
execution and recovery; declarative stage policy; dry-run estimation; campaign
API/UI/CLI surfaces; migration away from the external Python orchestrator; and
bounded projections/pagination so campaigns up to `MaxScheduleStages` remain
readable without exceeding the CLI response cap.

### Task 16.8: Let a Coverage Pass Start Where the Value Is (P2)

`contiguous-window` sized for total coverage is the strongest polishing recipe
measured on a fitted vector -- see the 2026-08-18 section of
[`docs/contiguous-window-polish-report.md`](docs/contiguous-window-polish-report.md)
-- but it spends the first half of every pass on the cheap end of the vector.
Gain by quarter, two consecutive passes over the 1000-circle fit of
`example/Christian_after.jpeg` (`activeSetSize` 32, 32 sweeps):

| Pass | Q1 | Q2 | Q3 | Q4 | Total |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 3 | -0.015 | -0.383 | -1.058 | -1.290 | -2.746 |
| 4 | -0.021 | -0.277 | -0.600 | -1.348 | -2.245 |

`selectContiguousWindowCircles` scans from the latest start downward and keeps a
strictly better total, so with empty visit counts it picks slot 968 first and
steps back 32 slots per sweep (`internal/fit/renderer/batch_polish.go:645`). The
low draw slots -- the early, large circles placed against a nearly empty canvas
and never revisited -- are reached only in the final quarter, and that is where
essentially all of the gain is.

`visitCounts` is rebuilt per `PolishCircleBatchContext` call
(`batch_polish.go:187`), so **every continuation restarts at slot 968** and
re-walks the low-value end before reaching the high-value one, no matter how
many passes have already covered it.

The docstring justifies latest-first as a render-cost optimization: a window
starting at `s` costs `circleCount - s` rasterizations per candidate because the
prefix is baked, so a late window is cheap. That holds for a partial budget.
**Over a full coverage cycle it is false** -- simulating both traversals for
`circleCount` 1000, `activeSetSize` 32, 32 sweeps:

| Traversal | Rasterizations/candidate | Coverage |
| --- | ---: | ---: |
| latest-first (current) | 16,872 | 1000/1000 |
| earliest-first | 16,152 | 1000/1000 |

Same 32 windows, opposite order, and the current one is 4.5% *more* expensive.
There is no render-cost argument for the current ordering once the budget covers
everything; the ordering is costing gain and buying nothing.

- [x] Order the traversal by where the budget is worth spending rather than by
      window start. Deciding between "earliest-first when the sweep budget
      covers the vector, latest-first otherwise" and a residual- or
      contribution-weighted window choice is part of the task; the partial-budget
      case must keep its current cheapness.
- [x] Carry visit counts across a polish continuation so a chained pass does not
      re-walk ground its parent already covered. The counts are per-call today;
      the parent's coverage is derivable from the chain, and `polishedFrom`
      already records the link.
- [x] Leave the selector deterministic. Identical config, parent, and seed must
      still reproduce bit-for-bit — the property that makes a fresh seed the
      only thing distinguishing one pass from the next.

**Acceptance Checks:**

- [x] A unit test asserts the traversal covers every draw slot for
      `circleCount` 1000, `activeSetSize` 32, 32 sweeps, and that summed
      rasterizations per candidate do not regress against latest-first.
- [x] A test pins the partial-budget case: at the shipped default sweep count,
      the chosen windows are no more expensive than they are today.
- [x] A chained continuation starts on slots its parent did not cover, asserted
      on the selected active sets rather than on cost.
- [x] Determinism regression: same config, parent, and seed produce an identical
      cost across two runs.

### Task 16.9: Let the Estimator Spend the Scarce Resource (P2) ✅

A campaign run to 3 000 circles on 2026-08-19/20 produced a growth recipe that
the schedule format can express but the estimator gives no guidance on. All
figures are from A/Bs branching from a common parent on
`example/Christian_after.jpeg` (512x512), equal added circles per arm.

- **Granularity.** `+1` per extend beat `+4` by **4.05 cost units** at identical
  population and per-circle budget (202.114 vs 206.163 at 312 circles), and the
  advantage compounded: the arm was 1.2% behind a reference campaign at 312
  circles and 10.8% ahead at 1 000. `additionalCircles: 1` should be the
  documented default for growth, with batching presented as a throughput
  convenience that is not in fact faster per circle.
- **Population is coupled to epochs.** At `optimizerEpochs` 1, raising `popSize`
  30 to 100 moved cost by **0.026** for 2.2x the wall clock; at `epochs` 3 the
  same change was worth **1.94**. A configuration raising one without the other
  is paying full price for nothing.
- **The objective flips with what is scarce.** While the circle budget is open,
  gain per *hour* is the right objective and the cheapest converging settings
  win — `iters` 500 with `epochs` 1 carried 300 to 2 000 circles. Against a
  fixed circle ceiling, gain per *circle* is the objective: at 2 000 circles
  over a 50-circle sample, `iters` 500 returned **1.84x the gain per circle** of
  `iters` 50 (0.0207 vs 0.0113) while returning roughly half the gain per hour
  (6.05 vs 11.3 units/hour).
- **Sample size.** The same comparison over 12 circles ranked `iters` 1 500
  below `iters` 500, and a single-circle probe showed `iters` 50 and 500 landing
  on the same cost. Neither survived a 50-circle sample. Any estimator advice
  derived from short probes needs a stated sample size.

- [x] Extend `schedule estimate` (Task 16.4) to report projected cost per circle
      and per hour separately, so a campaign against a circle ceiling and one
      against a time budget are not given the same answer.
- [x] Warn at validation when a document raises `popSize` above the default with
      `optimizerEpochs` 1, naming the measurement.
- [x] Record the recipe and its evidence in `docs/schedule-format.md`, including
      that `MaxCircles` was raised 1000 -> 3000 over this campaign because the
      cap, not diminishing returns, was the binding constraint each time.

Implemented: `app.ProjectScheduleCost` derives both rates from the stage records
the finish projection already reads, and hangs off `ScheduleProjection.Cost` so
one call cannot answer the two questions from different measurements. Forward
projections extrapolate the **trailing half** of the measured legs rather than
the campaign average, because the per-circle return decays -- on the recorded
campaign 1000 -> 2000 removed 31.597 cost units and 2000 -> 3000 removed 17.697,
so the average over-predicts the next leg by 1.79x -- and the projection says so
in a note when the trailing rate falls a quarter below the average.
`ScheduleDocument.Advisories()` is a separate pure query rather than a branch in
`Validate`, because the document is valid: it reports the population/epoch pair
for base, extend **and** polish stages, reading the thresholds from
`DefaultConfig()` so a moved default moves the check, deduplicating a repeated
step into one note, and saying explicitly that the polish figure was borrowed
from extends. Both reach the CLI (`!` lines), the API (`warnings`, `projection`)
and the campaign page; `internal/app` stays dependency-free, so PSNR is derived
by each surface through the existing `fit.PSNR` wrappers.

**Acceptance Checks:**

- [x] The estimator's two projections are asserted against the measured campaign
      figures at 1 000, 2 000, and 3 000 circles
      (96.199 / 64.602 / 46.905, PSNR 28.299 / 30.028 / 31.419).
      `internal/app.TestProjectScheduleCostMeasuresTheSpanAndEachLeg` and
      `TestProjectScheduleCostProjectsFromTheTrailingWindow` pin the costs and
      the 1.7854x over-prediction; `cmd.TestScheduleStatusProjectsBothScarceResources`
      pins the printed PSNRs. The per-leg wall clock in the fixture is
      **plausible, not recorded** -- the campaign's costs are the measurement,
      its stage timings were not kept -- and the fixture says so.
- [x] A validation test covers the population-without-epochs warning.
      `internal/app.TestScheduleDocumentAdvisories` and its five siblings.

Checks observed on this revision: `gofmt -s -l .` (clean), `go vet ./...`,
`go test -short ./...`, `go test -race -short ./...`, `go build ./...`,
`CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...`, `go tool templ generate`
followed by `git diff --exit-code -- 'internal/ui/*_templ.go'` (clean),
`just bundle`, and in `web/`: `npm run typecheck` and `npm run test:unit`
(117 passing). End-to-end against `serve`: the advisory prints on
`schedule create --dry-run` and disappears at `optimizerEpochs` 3, the server
logs it as `Schedule advisory`, and a running campaign's `schedule status`,
`GET /api/v1/schedules/:id` and campaign page each report the two projections
with different answers, while a settled campaign omits the projection entirely.

---

## Phase 17: Dashboard Start Page

### Completed Tasks 17.1–17.10 ✅

Implemented: a reproducible esbuild/TypeScript/React-island pipeline with a
committed embedded bundle; host facts, dashboard read models, global progress
and ordered invalidation SSE; the new dashboard and `/jobs` routes; live
campaign/job charts; a shared five-mode image viewer; authoritative REST
reconciliation across disconnects; and server/UI/unit/race/Chromium coverage.
templ remains the complete no-JavaScript fallback, and React progressively
hydrates it. The observable trust-boundary and release-gate changes are
documented in `docs/behavior-invariants.md` and `docs/releasing.md`.

### Task 17.11: Browser, Bundle, and Documentation Sign-Off (P1)

The open acceptance items from Task 17.9 live here so its implemented test and
documentation work can remain compact.

- [ ] Capture and add the README dashboard screenshot on a working browser
  runner.
- [ ] Observe `just check` with npm available, including the bundle drift gate,
  and prove it rejects a stale committed bundle.
- [ ] Verify the dashboard shows correct stat tiles, ordered campaign cards,
  running jobs, and an architecture badge matching a forced
  `CIRCLEFIT_SIMD_TIER`.
- [ ] Start a campaign and observe its card move to running with a ticking chart.
- [ ] Check chart legibility in auto, forced-light, and forced-dark themes.
- [ ] Exercise all five campaign image modes, overlay opacity, and shortcuts
  `1`–`5`.
- [ ] With JavaScript disabled, verify the dashboard and campaign cost plot
  remain complete and readable.
- [ ] Kill and restart the server while viewing the dashboard; verify the client
  reconnects, refetches, and converges without a navigation.

### Task 17.12: Bound the Restore-Path Resident Set (P2)

Task 17.11's restart check passes, but the restart itself is the expensive part.
`restorePersistedJobs` (`internal/server/restore.go`) loads every job on disk
into memory and keeps it there for the process lifetime: `LoadCheckpoint` per
job pulls in the full `BestParams` vector, and `restoreJobTrace` rebuilds
`MetricHistory` from every line of every `trace.jsonl`. The collection endpoints
no longer do this per request — the `checkpoint-info.json` sidecar added in #50
projects a listing without touching parameters — but the restore path was not
part of that change.

Measured 2026-08-21 on the 3 000-circle demo campaign's data root (4 358 jobs,
900 MB of checkpoints, 697 MB of traces):

| | value |
|---|---|
| Startup to `Restored persisted jobs` | ~135 s (incl. one-off sidecar backfill) |
| Resident baseline, idle | 1.34 GB |
| Resident after 10 min of a running polish stage | 1.50 GB |
| `GET /` | 5.3 s |

The baseline is proportional to all history, not to what is being served, and
the campaign adds ~2 900 more jobs at rising circle counts. The preceding OOM
kill was caused by per-request re-parsing and is fixed; this is the floor that
remains under it.

- [ ] Restore from the `CheckpointInfo` sidecar and load `BestParams` lazily on
      the paths that actually need a parameter vector (resume, extend, render,
      artifact download).
- [ ] Stop holding full `MetricHistory` for terminal jobs. The chart endpoints
      can read the trace on demand or a downsampled summary can be persisted
      beside the checkpoint; only running jobs need the live series in memory.
- [ ] Serve `GET /` and `/api/v1/dashboard` from a cached sidecar index
      invalidated by checkpoint writes, rather than re-walking the job tree.

**Acceptance Checks:**

- [ ] Resident set after restore is reported for 500, 2 000, and 4 000 persisted
      jobs and is sub-linear in job count; record the host and the data root's
      checkpoint and trace byte totals with the numbers.
- [ ] `GET /` and `/api/v1/dashboard` latency is reported at the same three
      sizes and does not grow with total job count.
- [ ] A restored job still resumes, extends, and renders identically — a resumed
      job reproduces the parent cost exactly, as in Task 15.9.
- [ ] Job detail, campaign charts, and the trace download return the same series
      as before for a terminal job whose history is no longer resident.

## Phase 18: Frontend Island Transition ✅

Phase 17 established the island pipeline and stopped after the pages it needed.
A 2026-08-22 audit of the frontend found the transition half-finished: five
mount points across four of the nine templ pages (`dashboard`, `job-list`,
`campaign-list`, `campaign-detail`, and `job-controls` — the last covering only
the button strip on the job detail page), against **1 481 lines of hand-written
inline JavaScript still living inside `.templ` files** and 2 136 lines of
TypeScript under `web/src` (excluding tests). Measured in the working tree:

| Inline script | Lines |
| --- | --- |
| `internal/ui/detail.templ:633-1448` | 816 |
| `internal/ui/image_viewer.templ:440-881` | 442 |
| `internal/ui/settings.templ:136-312` | 177 |
| `internal/ui/layout.templ:393-426` (theme toggle) | 34 |
| `internal/ui/layout.templ:60-71` (pre-paint theme IIFE) | 12 |

The quick-win cleanup that preceded this phase is done in the working tree: the
unregistered `campaign-cost` island was removed, the duplicated job-action
handlers were deleted from `detail.templ`, the stale `/api/v1/stream` link was
corrected, source maps are emitted and served, `web/src/format.ts` carries the
shared formatters with vitest coverage, seed and endpoint parity tests were
extended, the dark palette was de-duplicated, and `just check` now gates
`web-typecheck` and `web-unit`.

This phase finishes the transition **without** changing the rendering model.
templ stays the shell and the progressive-enhancement fallback, honoring
`docs/behavior-invariants.md:280-287` ("templ output is the fallback and
hydration seed, not a second live state model"). Tailwind and shadcn/ui may be
adopted *inside* the islands, which own their DOM entirely; see the deferred
alternative at the end of this phase for why the full SPA rewrite is not
scheduled.

Estimated 1.5–3 weeks. Tasks are listed in dependency order.

### Completed Tasks 18.1–18.7 ✅

Delivered. Every `.templ` file outside `layout.templ` is script-free, and
`layout.templ` carries only the pre-paint theme IIFE and the bodyless bundle
link; four tests in `internal/ui/inline_script_gate_test.go` hold that line,
allowlisting the exception by position rather than by filename. The islands are
`dashboard`, `job-list`, `job-detail`, `create-job`, `campaign-list`,
`campaign-detail`, `settings`, and `theme-switch`: `job-controls` and
`image-viewer` were absorbed rather than kept beside their pages, and one
`ImageViewer` component now serves the detail and campaign pages.

Three contracts are pinned as language-neutral fixtures that Go and TypeScript
each check themselves against, rather than against each other:
`web/src/state-badge-parity.json`, `web/src/create-job-parity.json`, and
`web/src/job-detail-parity.json`. Task 18.6 evaluated generating the read models
against a real `tygo` run and rejected it; see
[`docs/typescript-read-model-generation.md`](docs/typescript-read-model-generation.md).
Task 18.4 kept the server-side form POST as the create page's fallback, recorded
in `docs/behavior-invariants.md` and `docs/architecture.md`.

The fallback invariant is now asserted in both of its degraded modes —
JavaScript disabled, and the bundle present but 404ing or throwing — by 22
Playwright cases. The page without JavaScript is better than before the phase:
cost improvement rate, average and current CPS, and the ETA are computed in Go
now, because the before/after-mount equivalence test required it.

Regenerating the committed bundle in Task 18.7 exposed two defects no earlier
task could have seen, both fixed there: the job-detail island threw on every
load and left `<main>` empty, and the metric canvas drove a runaway Chart.js
resize loop. The shadcn SPA rewrite below stays a deferred alternative.

### Task 18.1: Port the Job Detail Page to an Island (P1) ✅

The largest single piece: 816 lines at `internal/ui/detail.templ:633-1448`
covering metric history, a hand-rolled SVG sparkline with hover and tooltip,
the parameter viewer, ETA/throughput arithmetic, and report download. The
existing `job-controls` island (`internal/ui/detail.templ:147`) is absorbed by
the new one rather than kept beside it.

- [x] Add a `JobDetailIsland` that mounts over the server-rendered detail body
      and reads job `/status` and `/metrics` through the existing
      `web/src/live.ts` refetch loop.
- [x] Reuse `web/src/charts.ts` for the sparkline instead of porting the
      hand-rolled SVG; keep hover and tooltip behavior.
- [x] Expose `refWidth`/`refHeight`/`refSize` on an API response. They exist
      only as view-model fields today
      (`internal/server/ui_handlers.go:168`, `:234`) and no JSON endpoint
      carries them, so the island cannot render the reference-image facts.
- [x] Fold `job-controls` into the detail island and drop the separate mount
      point and its registry entry.
- [x] Delete the inline script and confirm the templ fallback still renders a
      complete, readable page with JavaScript disabled.

**Acceptance Checks:**

- [x] A server test asserts the new reference-image fields are present and
      typed on the job resource, and a TypeScript type mirrors them.
- [x] The detail page renders identical metric, ETA, throughput, and parameter
      values before and after mount for a running and a terminal job.
- [x] With JavaScript disabled the detail page still shows state, metrics,
      images, parameters, and the report link.

### Task 18.2: Unify the Image Viewer in One Island (P1) ✅

`internal/ui/image_viewer.templ` is 903 lines carrying a 442-line inline
script (`:440-881`), while `web/src/ImageViewer.tsx` (107 lines) already
implements the same five-mode viewer for the campaign pages. Two
implementations of one component is the duplication this phase exists to
remove.

- [x] Extend `web/src/ImageViewer.tsx` to cover everything the inline script
      does — all five comparison modes, overlay opacity, difference heatmaps,
      and the `1`–`5` shortcuts — and mount it as an island from the templ
      partial.
- [x] Keep the templ markup as the no-JavaScript fallback: a static
      side-by-side view that needs no script.
- [x] Delete the inline script and the now-duplicated mode-switching CSS.

**Acceptance Checks:**

- [x] The detail page and the campaign pages resolve to the same component;
      a test asserts one viewer implementation is registered.
- [x] All five modes, overlay opacity, and shortcuts `1`–`5` behave identically
      on both pages (this overlaps Task 17.11's browser pass; record it once).

### Task 18.3: Port the Settings Page to an Island (P2) ✅

`internal/ui/settings.templ:136-312` is 177 lines of self-contained
`localStorage` handling with no server involvement, which makes it the lowest
risk port in the phase.

- [x] Add a `SettingsIsland` owning the preference reads and writes.
- [x] Move the theme-toggle handler at `internal/ui/layout.templ:393-426` into
      a small shared island or module so the page chrome stops carrying script.
      The pre-paint IIFE at `:60-71` stays inline by design — it must run
      before first paint, ahead of the bundle.
- [x] Cover the preference read/write helpers with vitest cases beside
      `web/src/format.test.ts`.

**Acceptance Checks:**

- [x] Preferences set before the port are still honored after it; the storage
      keys and value shapes are unchanged.
- [x] With JavaScript disabled the settings page still renders and explains
      that preferences require JavaScript.

### Task 18.4: Port the Create Page to an Island (P2) ✅

`internal/ui/create.templ` posts a form today. An island would post
`POST /api/v1/jobs` instead, and the two paths do not agree on defaults.

The hazard to decide deliberately rather than discover: the JSON API
distinguishes an omitted field from an explicit zero by reading the raw body,
while the form path defaults empty strings through helpers such as
`formIntOrDefault` (`internal/server/ui_handlers.go:591`). The behavior is
documented at `docs/behavior-invariants.md:234` and must not drift.

- [x] Decide and record whether the server-side form POST handler stays as the
      no-JavaScript fallback. Keeping it means keeping two admission paths with
      different omitted-versus-zero semantics; removing it means the create
      page stops working without JavaScript, which the fallback invariant
      currently forbids.
- [x] Add a `CreateJobIsland` that builds the JSON body explicitly, omitting
      fields the user left blank rather than sending zeros.
- [x] Keep client-side validation aligned with the server's typed validation;
      do not introduce a second set of limits.

**Acceptance Checks:**

- [x] A test asserts a job created through the island and the same job created
      through the form produce identical stored configuration, including for
      fields left blank and fields set explicitly to zero.
- [x] The recorded fallback decision is reflected in
      `docs/behavior-invariants.md` and `docs/architecture.md`.

### Task 18.5: Consolidate the Five State-Badge Implementations (P2) ✅

There are five independent renderings of a job or stage state badge, and they
disagree on three axes — the fallthrough class (`""` versus `badge-info`),
label casing, and `skipped`, which only the schedule variant handles:

| Implementation | Location |
| --- | --- |
| `stateClass`/`stateLabel` | `web/src/format.ts:16`, `:33` |
| `JobList` badge | `web/src/JobList.tsx:265` |
| `JobControls` badge | `web/src/JobControls.tsx:147` |
| `Campaigns` badge | `web/src/Campaigns.tsx:117` |
| `StateBadge` | `internal/ui/list.templ:136` |
| `ScheduleStateBadge` | `internal/ui/schedule.templ:330` |

- [x] Pick one behavior per axis, including what an unknown state renders as,
      and state it in the shared helper's doc comment.
- [x] Reduce the TypeScript side to the single `web/src/format.ts` pair and
      have every island use it.
- [x] Reduce the templ side to one `StateBadge` covering the schedule states,
      including `skipped`.
- [x] Extend `web/src/format.test.ts` and the templ tests to assert the Go and
      TypeScript renderings agree for every state, including the unknown case.

**Acceptance Checks:**

- [x] A parity test enumerates the known states and asserts the templ badge and
      the TypeScript helper produce the same class and label for each.

### Task 18.6: Decide Whether to Generate the TypeScript Read Models (P2) ✅

Roughly 25 Go↔TypeScript type and formatter pairs are kept in sync by
convention. The seed and endpoint parity tests added in the preceding cleanup
catch drift after the fact; generation would prevent it.

- [x] Evaluate generating the read-model interfaces from the Go structs against
      the constraint that `go build ./...` must never need node or npm
      (`internal/ui/static.go:18-20`) and that generated output would be
      committed, as `internal/ui/*_templ.go` and the bundle already are.
- [x] If adopted, add the generator as a Go tool and a drift gate alongside
      `templ-check` and `bundle-check` in `just check`.
- [x] If not adopted, record the decision and keep the parity tests as the
      contract.

### Task 18.7: Retire the Inline-Script Surface (P1) ✅

The phase's closing gate. After Tasks 18.1–18.4, only `report.templ` (a
self-contained download artifact, deliberately script-free and asset-free) and
`layout.templ` (the shell) should remain pure templ.

- [x] Add a test or lint step asserting no `.templ` file outside
      `layout.templ`'s pre-paint IIFE contains a `<script>` body.
- [x] Consolidate the CSS the deleted scripts depended on; remove rules no
      longer reachable.
- [x] Update `docs/architecture.md`'s frontend data-flow section and the
      island list in `AGENTS.md` to match the finished set.

**Acceptance Checks:**

- [x] Zero hand-written inline JavaScript remains in `internal/ui/*.templ`
      except `layout.templ:60-71`.
- [x] Every templ page still renders complete and readable with JavaScript
      disabled and with the bundle deliberately broken, as
      `docs/behavior-invariants.md:280-287` requires.
- [x] The committed bundle is regenerated and the bundle drift gate observed
      passing.

### Deferred alternative: a shadcn SPA rewrite (not scheduled)

Recorded here as a decision, not as a commitment. The reviewed alternative was
to rewrite the frontend as a Vite + React Router + Tailwind + shadcn/ui single
page application, reducing templ to one index shell — estimated 4–8 weeks. It
is not carried forward as a phase because it is mutually exclusive with the
work above and its stated benefit is obtainable inside it.

The design-system debt it names is real and measured in the working tree: **403
inline `style=` attributes across `internal/ui/*.templ` against 147 `class=`
usages, and 136 across `web/src/*.tsx` against 27 `className=` usages.** There
is no CSS file at all; every rule lives in the `<style>` block at
`internal/ui/layout.templ:72-353`, and the 27 custom properties defined there
already map onto shadcn's CSS-variable theming, with dark mode working the way
shadcn expects. shadcn would supply Table with real sorting, Badge, Card,
Dialog, and Form+zod for the ~30-field create page.

Adopting Tailwind and shadcn *inside* Phase 18's islands captures that benefit
without the five constraints the rewrite would have to pay for, because an
island already owns its DOM entirely:

1. **It deletes a documented invariant.** `docs/behavior-invariants.md:280-287`
   guarantees pages stay readable with JavaScript off or the bundle broken.
   That guarantee would have to be revised deliberately, not worked around.
2. **The asset namespace is flat.** `internal/ui/static.go` 404s any asset name
   containing `/`, so a stock Vite build emitting `assets/index-*.js` does not
   serve. Output would have to be flattened or the handler reworked.
3. **There is no catch-all route.** `internal/server/server.go:228` registers
   `mux.HandleFunc("/", s.handleDashboardPage)`, which 404s every path but
   exactly `/`. Deep links need an HTML fallback that does not shadow
   `/api/v1/` or `/static/`.
4. **Same-origin is enforced, not advisory.** The CORS middleware
   (`internal/server/server.go:1298-1310`, `sameOrigin` at `:1311`) 403s any
   request whose `Origin` host differs from `Host`, and there are no CSRF
   tokens anywhere: that check plus the loopback bind is the entire defense. A
   Vite dev server on another port only works if its proxy rewrites `Origin`.
5. **No CDN, no external assets, no node in `go build`.**
   `docs/behavior-invariants.md:481` and `internal/ui/static.go:18-20` are
   binding on any new pipeline.

Revisit only if client-side routing or retiring the dual rendering path become
goals in themselves. Note for both directions: SSE cannot carry a
client-rendered UI on its own. `/api/v1/events` is deliberately an invalidation
channel — `campaign.changed` carries no payload — so the refetch loop in
`web/src/live.ts` stays either way.

## Phase 19: CMA-ES Adapter ✅

**Goal:** Add the CMA-ES library at the established optimizer seam without yet
expanding the CLI, server, or schedule configuration surface.

- [x] Add `internal/opt/cmaes_adapter.go` with `CMAESAdapter`, `NewCMAES`,
      logging, optimizer-level early stopping, and bounded parallel evaluation.
- [x] Implement `Optimizer`, `ResumableOptimizer`, `LifecycleOptimizer`, and
      `IterationBudgetOptimizer`; map initial and additional candidates, resume
      and restart seed dimensions, progress mapping, and epoch observers.
- [x] Canonicalize `Problem.Repair` before every objective and inequality
      callback and map inequalities to CMA-ES feasibility ranking.
- [x] Pin `github.com/CWBudde/go-cma-es` to
      `v0.0.0-20260825113115-96b7c9adff3a`; report the linked version from
      build information and cover CMA-ES with the generic checkpoint version
      mismatch guard.
- [x] Keep `optimizer_contract_test.go` and `parallel_evaluation_test.go`
      unchanged; add focused adapter coverage and
      `TestCMAESParallelEvaluationMatchesSerial`.
- [x] Use the consumer's fixed-attempt `WithRestarts` wrapper for this adapter.
      Do not nest IPOP/BIPOP: those schedules require a shared evaluation
      budget and belong with the next phase's explicit CMA-ES configuration.

**Rationale:** The optimizer contracts already isolate the rendering pipelines
from library-specific state. Landing the adapter first makes continuation,
constraints, determinism, parallel evaluation, accounting, and persistence
observable under focused tests. Configuration can then expose sigma,
covariance mode, and restart strategy without mixing algorithm integration with
every user-facing surface.

---

## Phase 20: CMA-ES Configuration ✅

**Goal:** Expose the completed CMA-ES adapter through the canonical
configuration and every engine-selection surface.

- [x] Add `cmaes` to `JobConfig.optimizer`; carry it through CLI, JSON jobs,
      schedules, checkpoints, resume routing, and the web creation form.
- [x] Add normalized `initialSigma`, `covarianceMode`, `activeCMA`, and
      `restartStrategy` fields with CMA-only defaults and engine-specific
      refusal.
- [x] Support full, separable, and seven-coordinate block covariance; refuse
      full covariance above 512 optimizer dimensions.
- [x] Run IPOP/BIPOP under one `iters * popSize` evaluation budget and require
      `optimizerRestarts=1`, while retaining fixed attempts for
      `restartStrategy=none`.
- [x] Keep polishing MayFly-only and reject CMA-ES schedules containing a
      polish stage.
- [x] Advance the CMA-ES pin to
      `v0.0.0-20260825143954-e528faf326bf`, which includes block covariance,
      and update the support matrix, behavior invariants, known limitations,
      architecture, changelog, and contributor guidance.

**Rationale:** Engine selection now has one typed path from every entry point
to optimizer construction and persistence. Adaptive restart schedules replace,
rather than silently multiply, the consumer's fixed-attempt mechanism.

---

## Phase 21: CMA-ES Measurement

**Goal:** Measure whether CMA-ES fixes the premature-convergence failure that
motivated the adapter, under the same evidentiary standard as the existing
restart report.

- [x] Add opt-in trace diagnostics for normalized Mayfly population spread and
      CMA-ES sigma/condition number without charging ordinary jobs for Mayfly
      population snapshots.
- [x] Add an observable server-driven campaign/collector for twelve paired
      blocks, disjoint seed pools, a shared within-block seed prefix, and the
      five required arms.
- [x] Re-establish the Mayfly v0.7.1 single-run and r16 baseline on the
      eight-circle 512x512 workload.
- [x] Complete the full, separable, single-run, and IPOP CMA-ES arms under the
      same 6,502,400-evaluation cap.
- [x] Commit the raw costs and mechanism trajectories; report paired t-tests
      (`df=11`), blocks won, and explicit limitations.
- [x] Preserve and report the operator-stopped preliminary subset offline:
      three completed jobs and one interrupted job from block 1, with raw
      downsampled trajectories and no inferential statistics.

**Rationale:** A new optimizer is useful here only if its learned metric and
step-size adaptation change the measured collapse, not merely if one seed ends
well. The raw paired blocks and opt-in distribution traces make both claims
auditable. The first campaign was intentionally stopped on 2026-08-25 after its
several-day runtime became clear; its one-block descriptive result is recorded
in [`docs/cmaes-preliminary-report.md`](docs/cmaes-preliminary-report.md). The
campaign was re-run to completion on a 64-core host on 2026-08-28 and all
sixty jobs finished; see [`docs/cmaes-report.md`](docs/cmaes-report.md).

Separable CMA-ES with IPOP restarts beat the MayFly control in twelve of twelve
blocks (`+210.97`, `t = +5.04`) and the r16 arm in eleven (`+90.24`,
`t = +4.87`); full-covariance CMA-ES without restarts ties r16 (`t = +0.18`)
while using 27% of the cap. The seven paired contrasts are corrected together
(Holm, family-wise `alpha = 0.05`) and three survive: both separable ones and
`cmaes-ipop` against the control. The restart-over-budget finding reappears on
the v0.7.1 pin with the expected sign and size (`+120.73`, `t = +2.42`) but at
p = 0.034 does not survive that correction, so it is supported rather than
confirmed.

Three findings from the campaign open work rather than closing it, and are
carried by Phase 23 rather than reopening the boxes above: both IPOP arms spent
about 40% of their budget after their last improvement, for want of a stagnation
criterion the design never set; the design has no
separable-without-restarts arm, so the winning configuration confounds
covariance mode with restart strategy; and the design registered paired t-tests
without naming a primary contrast, so all seven are corrected together and the
two marginal ones are spent. A follow-up should preregister the contrast it
exists to settle. `lambda` is pinned to `popSize` and ran at 1024 against
Hansen's default of 16 for this dimensionality.

---

## Phase 22: CMA-ES Surface Parity and Project Identity

Phases 19–21 landed the adapter, its configuration, and one stopped
measurement. A 2026-08-26 audit of the working tree found the plumbing complete
and the product surface asymmetric: CMA-ES is already a peer in configuration,
validation, persistence, and contract tests, but not in the two places a user
meets it.

Already at parity, and not to be redone here:

- Typed configuration with `Resolved*` accessors and engine-scoped refusal in
  both directions — a CMA-ES job carrying `variant` and a MayFly job carrying
  `initialSigma` are each rejected rather than ignored
  (`internal/app/cmaes.go`, `internal/app/optimizer.go`).
- Nineteen focused adapter tests against MayFly's twenty-eight, covering
  lifecycle, determinism, continuation seeds, repair and constraints, progress
  and cancellation, epochs, the restart wrapper, and
  `TestCMAESParallelEvaluationMatchesSerial`.
- `internal/opt/optimizer_contract_test.go` fails if `app.SupportedOptimizers`
  drifts from the engines `internal/opt` can construct.
- Checkpoints record a per-engine `optimizerVersion`
  (`internal/store/types.go:247`), and the resume guard holds CMA-ES versions to
  the same measured-pair standard as MayFly.
- A `cmaes` base carrying a `polish` step is refused at document validation,
  because `Validate` expands the plan (`internal/app/schedule.go:359`), not
  part-way through a campaign.

### Task 22.1: Expose the CMA-ES knobs in the creation form (P1) ✅

When this task was written the form offered the engine and then admitted the
gap: "CMA-ES uses its default full covariance, active adaptation, and no
internal restart schedule here." All four settings reached the CLI, JSON job
payloads, and a schedule `base`; none reached the form, while the detail page
rendered them read-only — so the dashboard reported a configuration it could not
produce.

That mattered more than a missing input usually would. `AGENTS.md` requires long
experiments to be offered through `serve` so they stay watchable, and Phase 21's
open blocks are exactly such experiments; a `separable` or `bipop` campaign had
to be submitted as JSON.

- [x] Add `initialSigma`, `covarianceMode`, `activeCMA`, and `restartStrategy`
      to the creation form, revealed when `optimizer` is `cmaes` and omitted
      otherwise, so a MayFly submission still sends no CMA-ES-only field and
      keeps passing `refuseCMAESOnlyFields`. Landed as the CMA-ES fieldset and
      `parseCMAESForm` in `508dbe3` (#83); `7c07216` (#85) then gave the island
      the reveal, which the fallback deliberately does not have — it carries no
      script, so it renders the section for every engine and the handler drops
      it. `web/src/createJobBody.ts` drops the same keys.
- [x] Surface the two configuration-level refusals in the form rather than only
      as a rejected submission: full covariance above `MaxCMAESFullDimensions`,
      and a `restartStrategy` other than `none` with `optimizerRestarts != 1`.
      The second was unreachable until now because the form had no
      `optimizerRestarts` input at all; it has one, bounded by
      `app.MaxOptimizerRestarts`, which also makes Phase 21's `r16` arm
      startable from the dashboard. Both warnings are advisory `role="status"`
      regions in the island, composed from `ui.CreateJobLimits`; the fallback
      states both rules in prose from the same projection. `app.Validate` still
      decides the request.
- [x] Cover the round trip with a server test asserting that a submission naming
      each covariance mode and restart strategy reaches `JobConfig` intact,
      mirroring `internal/server/optimizer_engine_test.go:121`. Landed in
      `508dbe3` as `TestCreateFormRoundTripsCMAESSettings`; extended here with
      `TestCreateFormRoundTripsOptimizerRestarts` and
      `TestCreateFormRefusesRestartsBesideAnInternalSchedule`.
- [x] Regenerate `internal/ui/*_templ.go` and observe the generation gate.

The detail page reports the restart count it can now be given, so the dashboard
does not gain a configuration it cannot read back: `optimizerSchedule` renders
`16 restarts × 2 × 500 iterations` and drops the clause at a single attempt, in
both renderers, pinned by `web/src/job-detail-parity.json`. The dimension rule
is duplicated in `web/src/createJobBody.ts` so the browser can anticipate the
covariance refusal, and the two copies are pinned by matching tables in
`internal/app/config_test.go` and `web/src/createJobBody.test.ts`.

Observed on this revision: `go tool templ generate` with no drift,
`gofmt -s -l .`, `go vet ./...`, `go build ./...`, `go test -short ./...`,
`go test -race -short ./internal/server/... ./internal/ui/... ./internal/app/...`,
the `CGO_ENABLED=0 linux/arm64` cross-build, `just bundle`, `npm run typecheck`,
the 251-case `npm run test:unit` suite, and Playwright chromium over
`e2e/cmaes-warnings.behavior.spec.ts` (3 passed) plus
`e2e/accessibility.a11y.spec.ts` and `e2e/fallback.behavior.spec.ts`
(38 passed). End to end against a running `serve`: a fallback-form MayFly
submission with `optimizerRestarts=16` and the equivalent JSON submission both
store 16 and both report `16 restarts × 1 × 2 iterations`, while
`restartStrategy=ipop` beside `optimizerRestarts=2` is refused with app's own
message.

### Task 22.2: Document the optimizer engines in the README (P2) — complete

The README had no optimizer-engine section. `--optimizer` appeared once in
passing, CMA-ES once, and `--initial-sigma`, `--covariance-mode`, `--active-cma`
and `--restart-strategy` only in `--help`. The surrounding sections — variants,
initial population, restarts, crossover count, advanced parameters — were all
MayFly's, and nothing said so.

- [x] Add an "Optimizer engines" section covering the three engines, what
      selects each, and which knobs belong to which; nest the MayFly-specific
      sections under it so `--variant` and `--qmc-init` stop reading as global.
      `## Optimizer engines` now opens with what `--optimizer` selects, the four
      admission paths that carry it, a two-row table splitting the MayFly-only
      from the CMA-ES-only flags, and the rule that naming one alongside another
      engine is refused rather than ignored. `### MayFly` nests the variant
      sentence and the former `### Initial population`, `### Crossover count`
      and `### Advanced MayFly parameters` sections, now `####`.
- [x] Document the four CMA-ES flags with their defaults, the 512-dimension
      full-covariance limit, and the shared IPOP/BIPOP evaluation budget.
      `### CMA-ES` tabulates the four flags with their resolved defaults (0.3,
      `full`, `true`, `none`), states the 512-dimension refusal as a per-search
      bound with the 73-circle joint figure and the batch/sequential cases, and
      separates the shared `iters * popSize` IPOP/BIPOP budget from
      `--restarts`, including why the two cannot be combined.
- [x] State plainly that no engine ranking is established, linking
      [`docs/cmaes-preliminary-report.md`](docs/cmaes-preliminary-report.md) for
      what the stopped campaign does and does not support. The engines section
      says so in bold, names the one block, the operator stop, the censored IPOP
      figure and the missing separable arm, and contrasts it with the settled
      Dragonfly result.

`--restarts` and `--optimizer-epochs` are engine-agnostic, so the former
`### Restarts` section was promoted to a top-level `## Restarts and epochs`
rather than nested under MayFly, carrying a pointer to the different thing
`--restart-strategy` means for CMA-ES. Every claim was read out of the tree
rather than from an earlier report: the flag defaults from `cmd/run.go`, the
refusal rules from `internal/app/optimizer.go` and `internal/app/cmaes.go`, the
per-search dimension count from `JobConfig.optimizerDimensions`, and the
IPOP/BIPOP schedule semantics from the pinned `go-cma-es` v0.1.0 source.
Documentation only: no Go, templ, or TypeScript source changed.

### Task 22.3: Settle CMA-ES polishing as a decision, not a gap (P2) — complete

Polishing is MayFly-only by construction, so a CMA-ES campaign cannot take the
base/extend/polish shape a MayFly campaign can, and `MaxVelocity` in a
continuation profile has no CMA-ES analogue and is silently not applied
(`docs/known-limitations.md:116`). Both are recorded; neither is recorded as
permanent.

- [x] Decide and record one of: polishing stays MayFly-only, with the reason
      stated in [`docs/behavior-invariants.md`](docs/behavior-invariants.md), or
      a CMA-ES polishing stage is scoped as its own phase. Do not leave it
      implicit. **Decided: polishing stays MayFly-only.** The invariant now
      carries the reason rather than only the rule. A sweep is not the job's
      optimizer applied to a subset of the circles: every sweep hands the
      optimizer one fixed continuation profile (`LocalFraction` 1, `Sigma`
      0.02, `CoordinateRate` 0.2, `MaxVelocity` 0.02,
      `internal/fit/renderer/batch_polish.go:425`) and runs a
      `standard`-variant MayFly population with its own size, iteration budget,
      epoch count and stagnation window whatever the job names
      (`internal/server/worker.go:653`, `cmd/run.go:605`). `MaxVelocity` has no
      CMA-ES analogue, so a CMA-ES polisher would be a different local search
      under an unchanged name and the polishing reports would stop describing
      the stage that ran. The invariant also records what is *not* claimed --
      `PolishCircleBatchContext` takes any `opt.Optimizer` and its session-pool
      check reads the renderer and the evaluation width rather than the engine
      -- and names
      the condition for reopening it: a CMA-ES base stage measured to beat
      MayFly at an equal evaluation budget, which Phase 21 does not yet have.
- [x] Either way, make a CMA-ES job requesting polishing explain the
      restriction in its rejection, rather than reporting only that the field
      belongs to another engine. `engineOnlyField` gained an optional `detail`
      that only `polishingEnabled` carries, so its refusal continues into "a
      polishing sweep runs its own MayFly population whatever engine the job
      names, so this is a decision rather than a missing feature: run the base
      stage under `mayfly`, or leave polishing off", while every other
      engine-only refusal stays as brief as it was. The `/polish` endpoint was
      the path that hid it worst: it inherits the completed job's engine, so a
      CMA-ES parent always fails `app.Normalize`, and the handler reported a
      fixed `invalid_request: "invalid polishing configuration"`. It now
      returns the validation message under `invalid_config`, the same
      disclosure job creation already makes at the same trust boundary.

Two surfaces were made to say it before the refusal rather than after. The
creation form warns when the polishing box is ticked under a non-MayFly engine,
reusing the advisory `Warning` component Task 22.1 added -- advisory in the same
sense, since `app.Validate` still decides the request -- and it names whichever
engine is selected, because Dragonfly is refused for the same reason. The templ
fallback and the island now carry the same help text, stating the restriction
where the checkbox is. The job detail page needed nothing: `canPolish` already
required `ResolvedOptimizer() == OptimizerMayfly`
(`internal/server/ui_handlers.go:216`), so the control was never offered.

Tests: `TestPolishingRefusalExplainsTheRestriction` (both non-MayFly engines)
and `TestEngineOnlyRefusalsWithoutPolishingStayBrief` in `internal/app`,
`TestPolishEndpointExplainsTheEngineRestriction` in `internal/server`, and a
fifth case in `web/e2e/cmaes-warnings.behavior.spec.ts`.

### Task 22.4: Rename the project (P2)

The repository is named for one of the three engines it now hosts, and that name
reaches the module path, the binary, the Cobra root command, the version
template, and the CLI's error hints. `CircleFit` is the proposed name;
`github.com/cwbudde/circlefit` with a `circlefit` binary is its corresponding
form.

Measured surface in the working tree: 145 files reference the module path
`github.com/cwbudde/mayflycirclefit`, 25 documentation and CI lines name the
binary, and 27 lines carry the `MayFlyCircleFit` spelling. The user-visible ones
are `cmd/root.go:17`, `cmd/version.go:31`, `main.go:54`, and `main.go:118-122`.

**There is no tag, locally or on `origin`.** Nothing imports this module and
nothing is cached in `proxy.golang.org`, so the module path can change today for
the cost of a mechanical rewrite. The first release tag ends that permanently --
pressure in the opposite direction from waiting on Phase 21.

The two need not be sequenced, because the rename does not depend on the
measurement. A repository hosting MayFly, CMA-ES, and Dragonfly is misnamed
whether or not CMA-ES wins; a ranking decides which engine is the *default*, not
whether the project should be named after one of them. Rename on that argument,
before the first tag, and leave the default-engine question with Phase 21.

- [x] Confirm the name, then rename the GitHub repository and rewrite the module
      path, imports, binary name, Cobra `Use`, version template, error hints,
      `justfile` recipes, CI workflow references, and documentation in one
      commit that changes nothing else. **Confirmed as `CircleFit`.** The module
      is `github.com/cwbudde/circlefit` (245 occurrences across 150 `.go` and
      `.templ` files, plus `go.mod`, `scripts/build-release.sh`, and the
      measurement driver's README), the binary and Cobra `Use` are `circlefit`,
      and release archives are `circlefit_<version>_<os>_<arch>`. `cmd/root.go`'s
      `Short` also lost "with mayfly optimization", which the engine seam had
      already made wrong. Three surfaces the task list did not name turned out to
      carry the identity and moved with it:
      - The six `localStorage` keys, from `mayflycirclefit.` to `circlefit.`,
        **deliberately without a migration** — decided rather than overlooked, so
        each preference falls back to its default once. `web/src/prefs.ts`'s
        header comment recorded a standing "renaming a key needs a migration"
        policy; it now records this one break and restates the rule for the next.
        The key is duplicated in `layout.templ`'s pre-paint IIFE, and
        `window.mayflyTheme` — a contract between the inline script and the
        bundle — became `window.circlefitTheme`; both sides moved together.
      - The `MAYFLY_*` operator environment variables, **as a hard cut** to
        `CIRCLEFIT_*`, with no alias. Verified directly and stated as the cost:
        `CIRCLEFIT_SIMD_TIER=scalar` reports `"simd": "scalar"`, while
        `MAYFLY_SIMD_TIER=scalar` now reports `"simd": "avx2"` — silently
        inert, which is the substitution `CIRCLEFIT_REQUIRE_SIMD_TIER` exists to
        catch. `simd_tier.go`'s comment no longer claims the old lever is "kept
        because CI steps and operator notes already use it"; it says the old
        spellings are inert.
      - `${RUNNER_TEMP}/mayfly-benchmark-base` and the Playwright temp roots.
- [x] Leave the pinned library names untouched: `cwbudde/mayfly`,
      `CWBudde/go-cma-es`, and `CWBudde/dragonfly` are separate projects, and
      their spellings are load-bearing in `go.mod` and in the resume guard's
      per-library allowlist. Untouched, along with everything else that is the
      library rather than the project: the `mayfly` optimizer wire value every
      checkpoint records, `optimizerVersion`, the variant names,
      `MayflyAdapter` and `mayfly_adapter.go`, the `"MayFly"` engine display
      string, and `dragonfly_adapter.go`'s `mayflyLogger`. Also untouched, as
      records rather than identity: `example/MayFly*.png` and the campaign
      documents that fit the photographed mayfly, the two Phase 9 flamegraphs
      whose embedded symbols name the run as it happened, and the historical
      `CHANGELOG.md` entries and four code comments that mention the
      already-removed `MAYFLY_REQUIRE_SSD_BACKEND`. Those are the complete
      residue of both old spellings in the tree.
- [x] Verify with `go build ./...`, `go test -short ./...`, the cross-build, and
      a fresh clone at the new path; confirm no artifact, checkpoint, or trace
      field carried the old name. **No persisted field carries it**, and this
      was measured rather than read: a job run on a binary built from
      `origin/main` (129885a) wrote `checkpoint.json`, `checkpoint-info.json`
      and `trace.jsonl`, and the only occurrence of the old spelling in any of
      them is the operator's own absolute `refPath`, which is the working
      directory's name and not a field the program composes. That checkpoint
      then resumed under the renamed binary, reporting `optimizer=mayfly`,
      `optimizerVersion=v0.7.1` and an improvement from 178.5268 to 157.7391 —
      the resume guard and the engine wire value survived intact.

Observed on this revision: `go tool templ generate` and `bash
scripts/bundle-web.sh` are both no-ops on a second run (the generated `*_templ.go`
and `internal/ui/static/dashboard.js` hash identically), `gofmt -s -l .` empty,
`go vet ./...`, `go build ./...`, `go test -short ./...` (exit 0, 11 packages),
`go test -race -short ./internal/... ./cmd/... .` (exit 0, 9 packages), the
`CGO_ENABLED=0 linux/arm64` cross-build, `golangci-lint run --config
./.golangci.toml --new-from-merge-base=origin/main` with the pinned v2.13.1 (0
issues; the rename tripped `lll` on `main.go:118` and `cmd/version_test.go:47`,
both wrapped), `npm run typecheck`, the 254-case `npm run test:unit`, and the
full Playwright matrix (`npm run test:e2e`, 182 passed). End to end:
`./bin/circlefit --version` prints `circlefit version dev`,
`scripts/build-release.sh 0.0.0-test` produces five
`circlefit_0.0.0-test_*` archives whose extracted `circlefit` binary reports
`circlefit version 0.0.0-test`, and a running `serve` returns
`<title>Dashboard - CircleFit</title>` with `circlefit.theme` in the pre-paint
script and all six `circlefit.*` keys in the served bundle.

**Rationale:** The engine seam is done, and the remaining asymmetry is
presentational rather than architectural — which is precisely why it is worth a
task list instead of being absorbed into whichever phase touches the UI next. A
knob reachable only by hand-written JSON is a knob the dashboard cannot make
observable, and Phase 21's open blocks are the campaigns that need it. The
rename is grouped here because it has the same cause: the project outgrew the
assumption that one library is the project.

---

## Phase 23: CMA-ES Step-Size Divergence and the Lambda Question

**Goal:** Repair the defect the complete Phase 21 campaign exposed, and close
the two questions its design could not answer, before any default changes.

[`docs/cmaes-report.md`](docs/cmaes-report.md) is the evidence for all four
tasks below. Read it first; the numbers here are not repeated there.

### Task 23.1: Make the CMA-ES restart arms measurable and end dead runs (P1)

Both IPOP arms in the campaign reached their best score around the middle of
their run and never improved on it: 46% and 41% of those budgets produced
nothing, against 21% for the non-restarting `cmaes-single`. The cause is
configuration, not a library defect — the campaign set none of the `stop*`
fields, so `Stop.enabled()` is false, `config.Convergence.StagnationIterations`
is never armed, and a restart schedule has no way to end a run that has stopped
progressing and hand its budget to the next restart.

The trajectories also record sigma reaching 1e43 in separable mode, which looks
like a diverged search and is not one. Sigma alone is gauge-dependent: CMA-ES
identifies only `sigma^2 * C`, go-cma-es does not renormalize `C`, and the
library's `TolXUp` guard correctly measures `sigma * max(D)` rather than sigma.
The campaign's own block-1 trace shows the incumbent still improving while
sigma rises 242-fold. **The identifiable quantity was never recorded**, so that
account is inference; recording it is what turns it into a measurement.

- [ ] Record `max(D)` — or `sigma * max(D)` directly — in `SearchDiagnostics`.
      `cmaes_adapter.go` takes Sigma and ConditionNumber from the distribution
      snapshot and drops its eigenvalues, so the extent of the sampling
      distribution cannot be recovered from any trace this project has written.
- [ ] Persist each restart's `TerminationReason`. The library records one per
      restart and the adapter discards it, then maps the schedule-level reason,
      which the restart driver overwrites with max-evaluations whenever the
      budget is spent. `completed` on all sixty campaign jobs is structurally
      guaranteed for a restart arm and carries no information.
- [ ] Decide whether a restart strategy should arm a default stagnation
      criterion when the caller sets none. It is the change that would have
      reclaimed 40% of two arms' budgets, and it is a behaviour change for
      every existing CMA-ES restart configuration, so it wants its own
      measurement rather than being folded into the observability work.

### Task 23.2: Separate covariance mode from restart strategy (P1)

`sep-cmaes-ipop` won the campaign, but it varies covariance mode *and* restart
strategy against `cmaes-single`. The registered design has no
separable-without-restarts arm, so nothing attributes the +90.24 to either.

- [ ] Add a `sep-cmaes-single` arm and run it on the same twelve seed prefixes,
      so the existing rows stay comparable and only the missing cell is bought.

### Task 23.3: Screen `lambda` (P2)

`internal/opt/cmaes_adapter.go` sets `Lambda = popSize` and `Mu = popSize/2`, so
every campaign arm ran `lambda = 1024`. Hansen's default for this 56-dimension
problem is `4 + floor(3 ln 56)` = 16, sixty-four times smaller, and a smaller
`lambda` converts the same evaluation budget into far more generations of metric
learning. CMA-ES won at 64x its own recommended population; whether it wins by
more at a sane one is the campaign's most promising untested knob.

Two limits currently make the screen inexpressible, and both are request-
validation guards rather than modelling statements:

- `app.MinPopulation` is 20, so `lambda = 16` cannot be requested at all.
- `app.MaxIterations` is 10000, but the shared 6,502,400-evaluation cap needs
  325,120 generations at `lambda = 20` and 406,400 at 16.

- [ ] Raise `app.MaxIterations` so a small `lambda` can reach the cap,
      documenting the reason in the constant's comment the way `MaxCircles` and
      `MaxPopulation` already do.
- [ ] Decide whether `app.MinPopulation` should reach 16. It has no rationale
      comment, unlike its neighbours, and lowering it touches the MayFly path
      with no evidence behind it.
- [ ] Screen `lambda` crossed with covariance mode, not under full covariance
      alone — the winning configuration is separable.

### Task 23.4: A second fixture (P3)

- [ ] Only after 23.1–23.3: repeat on a second reference image and a different
      circle count. Everything measured so far is eight circles on one 512x512
      reference.

**Rationale:** The campaign produced this project's strongest optimizer result
and a defect that bounds it, in the same data. Changing a default on the result
while the defect stands would ship a recommendation whose measured gain is
known to be a floor and whose winner confounds two variables. The order above is
the order in which each piece of work makes the next one interpretable.

---

## Summary and Next Steps

Completed implementation history is intentionally summarized above; detailed
design decisions, measurements, and observable contracts belong in `docs/`,
tests, and git history. A completed marker records implementation for its
historical revision, not a fresh release-gate result.

Current open work, in priority order:

1. **Release gate (P0):** Task 14.13.
2. **Correctness and throughput (P1):** Tasks 15.7 and 15.8, followed by the
   remaining Phase 15 measurement and optimization tasks.
3. **Search quality (P1):** Task 15.11 — restarts beat a longer single run by
   a measured, significant margin; see
   [`docs/restart-vs-budget-report.md`](docs/restart-vs-budget-report.md).
4. **CMA-ES measurement (P1):** compare evaluation-matched MayFly and CMA-ES
   arms, including IPOP and separable covariance.
5. ~~**CMA-ES surface parity and project identity (P2):** Tasks 22.1–22.4.~~
   Phase 22 is complete. The creation form configures every CMA-ES knob, carries
   the restart count Phase 21's arms need, and warns before the refusals it can
   anticipate (22.1). The README has an "Optimizer engines" section documenting
   all three engines, the CMA-ES flags, and the absence of a ranking (22.2).
   Polishing stays MayFly-only as a recorded decision, and every path that
   refuses it says why (22.3). The project is `CircleFit`, on
   `github.com/cwbudde/circlefit`, renamed before the first tag (22.4). The
   default-engine question stays with Phase 21, which the rename deliberately
   did not wait for.
6. **Dashboard sign-off (P1):** Task 17.11.
7. ~~**Frontend island transition (P1/P2):** Tasks 18.1–18.7.~~ Phase 18 is
   complete. The shadcn SPA rewrite remains a deferred alternative at the end
   of that phase, not scheduled work.
8. **Server memory (P2):** Task 17.12.
9. **UX and supporting documentation (P2/P3):** Tasks 12.9 and 13.15.
10. **Experimental backends/research:** Tasks 11.9–11.13 and 10.20.

Do not mark a check complete from its presence in code or CI configuration
alone. Record the exact command or observed CI result for the revision, and
include host/workload/allocation conditions with performance claims.
