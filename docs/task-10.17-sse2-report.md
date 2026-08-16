# Task 10.17: SSE2 SIMD tier for AMD64 hosts without AVX2

**Validated:** 2026-08-16  
**Profiling and microbenchmark hardware:** AMD Ryzen 5 4600H  
**End-to-end hardware:** 64-vCPU QEMU guest reporting sse2, sse3, ssse3,
sse4_1, and sse4_2 but no AVX2  
**Implementation:** hand-written Go Plan 9 assembly; no C, cgo, GoAT, or
external assembler

Before this work, AMD64 dispatch was AVX2 or scalar. A host without AVX2 ran
the fully portable path for every hot kernel, so it lost the entire Phase 10
speedup rather than part of it. Dispatch is now tiered AVX2, then SSE2, then
scalar, with each tier chosen once at initialization from `x/sys/cpu`.

## Where the time went without AVX2

`BenchmarkFit` profiled with `GODEBUG=cpu.all=off`, before the SSE2 kernels
existed, so that setting still produced scalar dispatch:

| Symbol | Flat samples |
| --- | ---: |
| `fit.ssdScalar` | 29.96% |
| `renderer.compositeOpaqueSpanScalar` | 25.17% |
| `fit.MSECost` | 24.63% |
| `renderer.fixedCircleQ16.span` | 2.80% |

Three symbols hold about 80% of the profile, and the Q16.16 span geometry that
dominates the AVX2-era profile holds almost none of it. The work was therefore
scoped to SSD and delta-SSD.

## Kernels

`ssd_sse2_amd64.s` processes four NRGBA pixels per 128-bit batch: unsigned byte
differences, alpha masked out, RGB widened to 16-bit lanes, squared and
pairwise-added with `PMADDWD`, with an exact scalar tail for widths not divisible
by four.

The accumulator is the one design choice worth restating. Partial sums stay in
int32 lanes and widen to int64 once at the end, which is what makes the kernel
fast; widening per iteration measured materially slower.

The bound this creates is per lane, not per row. `PMADDWD` pairwise-adds the
widened `R,G,B,0` words, so one lane accumulates `R²+G²` and its neighbour `B²`,
and the busiest lane carries at most `width*2*65025` - it first exceeds 2^31 at
width 16512, not at the row-total figure of 11009. `ssdSSE2MaxWidth` is 11000, a
deliberate margin under both, and `fastSSD_SSE2` hands wider rows to the scalar
kernel.

`delta_ssd_sse2_amd64.s` applies the same shape to the discontiguous dirty spans
of the incremental cost path. It initially did not: it was written as a
transliteration of the AVX2 delta kernel and widened per iteration, spending
sixteen extra instructions per four pixels doing exactly what the SSD kernel
next to it argues against. Switching it to an int32 accumulator with a single
vector-register epilogue is worth 1.11x to 1.45x over spans of eight pixels and
up, measured A/B in one binary on a Ryzen 5 4600H, and is neutral at four
pixels. Because a span is not bounded by a row, `deltaSSDSpanSSE2` splits inputs
longer than `deltaSSDSSE2MaxPixels` and sums them in int64, so the kernel has no
width cliff; the split is a separate function so the common single-chunk path
costs one compare.

Circle-span geometry got no SSE2 kernel. An earlier revision of this work added
`circle_geometry_sse2_amd64.s`, a float32 span-edge search; it was removed
before merge because `circleSpanFloat32Selected` is reachable only through
`CPURenderer.forceFloat32Geometry`, which no configuration path and no CLI flag
sets. The kernel could not execute outside its own tests. Its test table was
kept and retargeted at the AVX2 kernel, which was covered by a weaker one.

## Microbenchmarks

All figures below are from the no-AVX2 target itself (QEMU Virtual CPU, sse4_2
and no AVX, 64 vCPU), median of three 300 ms runs, 0 B/op and 0 allocs/op
throughout. An AVX2 host under `GODEBUG=cpu.avx2=off` has the right instruction
set and the wrong microarchitecture and is not a substitute.

SSD, `BenchmarkFastSSD_Comparison`, SSE2 against the scalar oracle:

| Image | Scalar | SSE2 | Speedup |
| --- | ---: | ---: | ---: |
| 64×64 | 6622 ns | 1099 ns | 6.03× |
| 128×128 | 26304 ns | 4368 ns | 6.02× |
| 256×256 | 105222 ns | 16928 ns | 6.22× |
| 512×512 | 422968 ns | 73945 ns | 5.72× |
| 1024×1024 | 1698357 ns | 318398 ns | 5.33× |

Delta-SSD over dirty spans, `BenchmarkDeltaSSDSpanSSE2Direct` against
`BenchmarkDeltaSSDSpan/scalar`, with the int32 accumulator:

| Span | Scalar | SSE2 | Speedup |
| --- | ---: | ---: | ---: |
| 4 px | 10.2 ns | 4.5 ns | 2.25× |
| 8 px | 18.5 ns | 5.9 ns | 3.14× |
| 16 px | 35.1 ns | 9.4 ns | 3.75× |
| 32 px | 68.5 ns | 16.8 ns | 4.07× |
| 64 px | 135.3 ns | 31.6 ns | 4.28× |
| 128 px | 270.7 ns | 61.3 ns | 4.41× |
| 256 px | 546.2 ns | 122.8 ns | 4.45× |

The direct benchmark exists because `BenchmarkDeltaSSDSpan` times only the
installed kernel, so on an AVX2 development machine the SSE2 kernel could not be
measured at all - which is how it kept the slower accumulator strategy.

## Staged incremental cost at the SSE2 tier

`stagedIncremental` is gated on a vectorized delta-SSD kernel, and its crossover
constants are documented as modelling native AVX2 measurements. Extending them
to a half-width kernel was measured rather than assumed.
`BenchmarkIncrementalCostCrossover`, 256×256, single thread, median of three
200 ms runs, delta speedup over a full re-render:

| Radius | 4 | 8 | 16 | 32 | 48 | 64 | 80 | 96 | 112 | 128 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| AVX2 (Ryzen 5 4600H) | 3.65× | 3.79× | 2.71× | 1.83× | 1.41× | 1.24× | 0.93× | 0.86× | 0.90× | 1.00× |
| SSE2 (no-AVX2 target) | 3.33× | 3.09× | 2.50× | 1.75× | 1.34× | 1.15× | 1.05× | 0.99× | 0.92× | 0.92× |

Same shape, and the SSE2 crossover sits slightly later (radius 96 against
roughly 72), so the AVX2-tuned constants give up a little of the SSE2 win rather
than choosing a slower path. SSE2 therefore takes the staged path.

## End to end

`BenchmarkFit` on the no-AVX2 target, this branch against `origin/master`. Both
test binaries were cross-compiled locally with `CGO_ENABLED=0` and run on the
same machine back to back; median of three 200 ms runs.

| Case | master | branch | Speedup |
| --- | ---: | ---: | ---: |
| Cost/256×256/FastMSE | 107351 ns | 18353 ns | 5.85× |
| Cost/512×512/FastMSE | 423615 ns | 69197 ns | 6.12× |
| Pipeline/Sequential/K4 | 11857282 ns | 9573929 ns | 1.24× |
| Pipeline/Batch/K6/B2 | 14821269 ns | 12343750 ns | 1.20× |
| Pipeline/Joint/K8 | 7906113 ns | 6966160 ns | 1.13× |

The cost figures are the SSD kernel and track the microbenchmark. The pipeline
figures are far smaller because the optimizer, allocation, and the span
compositor - which has no AMD64 vector kernel at any tier - are unchanged, and
Amdahl applies.

An earlier revision of this branch also recorded a full 32-circle batch fit on
this target at seed 4242 with 8 threads: 300.81 s before, 150.52 s after, with
an identical final cost of 1032.75, and 150.33 s at 64 threads, essentially
flat. That run predates the delta-SSD accumulator change and the staged-path
decision above and has not been repeated since.

## Deliberate non-ports

SAD has no SSE2 kernel. `FastSAD` has no non-test callers, and its AVX2 kernel
depends on `VPMADDUBSW` (SSSE3) and `VPMULLD` (SSE4.1), which baseline SSE2 does
not provide. Porting it would mean introducing SSSE3 and SSE4.1 dispatch tiers
for a cost function nothing calls.

The Q16.16 circle-span kernel also stays scalar without AVX2. It compares Q32.32
products with `VPCMPGTQ`, and SSE2 has no 64-bit signed compare. Emulation costs
several extra instructions per vector, and the ceiling is low twice over: the
symbol is 2.80% of the no-AVX2 profile, and `BenchmarkCircleSpanQ16AVX2Direct`
measures even the hardware-compare AVX2 kernel at 14.4/28.2/62.8/133 ns against
9.2/9.8/23.3/44.6 ns for the scalar finite-difference span at radii
5.25/25.25/100.25/256.25 — already 1.6× to 3.0× slower. `spanAVX2` therefore
falls through to the scalar span on non-AVX2 CPUs.

## Exactness and opt-in behavior

The SSD, delta-SSD, and circle-span SSE2 kernels are exact: they match their
scalar and float32 oracles bit for bit, including at batch boundaries, padded
strides, and alpha-only differences, and they are enabled unconditionally on
capable hardware.

Every kernel in this revision is exact, so there is no opt-in flag to reason
about; see the planned follow-up below for the two inexact additions.

## Forcing a backend

`GODEBUG=cpu.all=off` no longer selects the scalar kernel on AMD64.
`golang.org/x/sys/cpu` registers sse2 with `Required: runtime.GOARCH == "amd64"`,
and `processOptions` computes `Enable = enable || options[i].Required`, so
`cpu.X86.HasSSE2` stays true and dispatch picks SSE2.

| Goal | Setting |
| --- | --- |
| Native tier | none |
| Any reachable tier | `MAYFLY_SIMD_TIER=avx2\|sse2\|neon\|scalar` |
| Complete scalar fallback | `MAYFLY_SIMD_TIER=scalar`, or the older alias `MAYFLY_DISABLE_SIMD=1` |
| Assert the detected tier | `MAYFLY_REQUIRE_SIMD_TIER=<tier>` |

`MAYFLY_SIMD_TIER` is read once during package initialization and applies to
every runtime-dispatched kernel in both packages on every architecture. An
unparseable or unreachable value panics rather than falling back, because
quietly substituting the detected tier would let a gate asking for SSE2 pass
while measuring AVX2.

`MAYFLY_REQUIRE_SIMD_TIER` is the assertion half and never sets a tier. That
separation is the point: pairing `MAYFLY_SIMD_TIER=x` with
`MAYFLY_REQUIRE_SIMD_TIER=x` only proves dispatch honored the pin, whereas
`GODEBUG=cpu.avx2=off` with `MAYFLY_REQUIRE_SIMD_TIER=sse2` proves that feature
masking still demotes detection the way this document claims. It replaces
`MAYFLY_REQUIRE_SSD_BACKEND`, which described one kernel in one package: setting
it while running `./internal/fit/renderer` asserted nothing about the renderer.

Within a process, `fit.SetForcedTier` re-runs every registered dispatch site, so
the whole ladder is exercised in one test binary. The CI gate needs a separate
step only for what forcing cannot cover.

Numbers here are local measurements on the stated hardware, not portable
guarantees, and they do not certify a CI result for any particular revision.

## Planned follow-up (not in this revision)

An opt-in float32 span compositor and the `--fast-compositing` flag are the
subject of a separate branch. Its prototype measurements previously lived here
and have moved with it, so that this document describes only code that ships in
this revision.
