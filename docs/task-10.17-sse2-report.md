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
scoped to SSD, delta-SSD, and circle-span geometry.

## Kernels

`ssd_sse2_amd64.s` processes four NRGBA pixels per 128-bit batch: unsigned byte
differences, alpha masked out, RGB widened to 16-bit lanes, squared and
pairwise-added with `PMADDWD`, with an exact scalar tail for widths not divisible
by four.

The row accumulator is the one design choice worth restating. Partial sums stay
in int32 lanes for a whole row and widen to int64 once per row, which is what
makes the kernel fast; widening per iteration measured materially slower. A
row's maximum value is `width*3*255*255`, so int32 lanes stay exact through
11000 pixels (11000 * 3 * 65025 = 2145825000 < 2^31). `ssdSSE2MaxWidth` encodes
that bound and `fastSSD_SSE2` hands wider rows to the scalar kernel rather than
losing the per-row widening.

`delta_ssd_sse2_amd64.s` applies the same shape to the discontiguous dirty spans
of the incremental cost path. `circle_geometry_sse2_amd64.s` ports the float32
span-edge search: a scalar coarse scan with an eight-pixel stride, then one
four-lane vector compare (a second only when all four candidates are inside) to
locate the edge, replacing up to seven dependent scalar iterations. Both edges
stay bit-identical to `circleSpanFloat32`.

## Microbenchmarks

SSD, SSE2 against the scalar oracle, 0 B/op and 0 allocs/op throughout:

| Image | Speedup |
| --- | ---: |
| 64×64 | 6.35× |
| 128×128 | 6.48× |
| 256×256 | 6.28× |
| 512×512 | 6.08× |
| 1024×1024 | 4.66× |

Delta-SSD over dirty spans:

| Span | Scalar | SSE2 | Speedup |
| --- | ---: | ---: | ---: |
| 4 px | 14.53 ns | 8.69 ns | 1.67× |
| 16 px | 48.14 ns | 19.23 ns | 2.50× |
| 64 px | 193.1 ns | 60.10 ns | 3.21× |
| 128 px | 401.5 ns | 110.1 ns | 3.65× |
| 256 px | 727.1 ns | 212.8 ns | 3.42× |

## End to end

`BenchmarkFit` in the no-AVX2 configuration, before against after:

| Case | Before | After | Speedup |
| --- | ---: | ---: | ---: |
| Cost/512×512/FastMSE | 594848 ns | 106919 ns | 5.6× |
| Cost/256×256/FastMSE | 165608 ns | 24950 ns | 6.6× |
| Pipeline/Sequential/K4 | 24849266 ns | 16352832 ns | 1.52× |
| Pipeline/Batch/K6/B2 | 30256928 ns | 22792432 ns | 1.33× |
| Pipeline/Joint/K8 | 13748212 ns | 11373474 ns | 1.21× |

On the real no-AVX2 target, an identical 32-circle batch workload at seed 4242
with 8 threads ran in 300.81 s on the old binary and 150.52 s on the new one, a
2.0× improvement, with an identical final cost of 1032.75. Raising that run to
64 threads gave 150.33 s, essentially flat: the workload is not thread-bound at
this size, so the gain is the kernels, not the parallelism.

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
| SSE2 tier on an AVX2 host | `GODEBUG=cpu.avx2=off` |
| Complete scalar fallback | `MAYFLY_DISABLE_SIMD=1` |

`MAYFLY_DISABLE_SIMD` is read once during package initialization and applies to
every runtime-dispatched kernel on every architecture. The native CI gate uses
all three states, and `MAYFLY_REQUIRE_SSD_BACKEND` turns a silent fallback into
a failure in each of them.

Numbers here are local measurements on the stated hardware, not portable
guarantees, and they do not certify a CI result for any particular revision.

## Planned follow-up (not in this revision)

The measurements below were taken on a prototype and are recorded here so the
follow-up branch can reclaim them. Neither the kernel file nor the flags exist
in this revision.

`composite_span_fast_amd64.s` would add an opt-in float32 span compositor with
both an SSE2 four-lane and an AVX2 eight-lane kernel, against the exact float64
span:

| Span | float64 | SSE2 | AVX2 |
| --- | ---: | ---: | ---: |
| 64 px | 213 / 195 ns | 66 ns (3.3×) | 50 ns (3.9×) |
| 256 px | 733 / 700 ns | 184 ns (4.0×) | 114 ns (6.1×) |
| 1024 px | 3086 / 3200 ns | 717 ns (4.3×) | 470 ns (6.8×) |

The two float64 columns are the separate baseline runs of the SSE2 and AVX2
comparisons on the same machine.

Both follow-up additions are inexact or non-serial and would therefore be
opt-in and off by default:

- `--fast-compositing` regroups the opaque span blend into one float32
  multiply-add per pixel, which is accurate to +/-1 per channel rather than
  byte-identical to the default float64 span.
- `--parallel-evaluation` reproduces bit-identically for a fixed seed and does
  not depend on the worker count, but its trajectory differs from a serial run
  of the same seed, because MayFly holds the global best fixed across a parallel
  generation instead of updating it mid-generation.
