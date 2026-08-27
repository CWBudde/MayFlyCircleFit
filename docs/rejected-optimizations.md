# Rejected optimizations

Approaches that were built or prototyped, measured, and then **not shipped**.
Each entry records what was tried and the measurement that killed it, so the
same ground is not covered twice. An entry here is a decision, not a backlog
item: reopen one only with a new measurement or a changed constraint, and say
which.

Search-quality interventions have their own registers — see
[`restart-vs-budget-report.md`](restart-vs-budget-report.md) for what did *not*
delay population collapse, and
[`aoblmoa-paper-fidelity-report.md`](aoblmoa-paper-fidelity-report.md) for the
variant screen. This document covers the CPU rendering and evaluation path.

## Build and toolchain

**C intrinsics plus GoAT transpilation.** The first AVX2 SSD kernel was written
in C with intrinsics and transpiled to Go assembly, and it worked — 3.05× over
scalar on the prototype. It was still dropped in favour of writing Plan 9
assembly by hand. The transpiler added a build-time dependency, an
untracked-in-repo C source, and a layer between the assembly that runs and the
source anyone reads. The shipped kernels use no cgo, no C sources, no external
assembler, and no GoAT.

The technical findings from the prototype did survive and are in the shipped
kernels: zero-extend to 16-bit before squaring, use the multiply-add instruction
as a fused square-and-horizontal-add, and expect interleaved RGBA to cost about
15–20% against packed RGB because of the alpha lane.

**cgo anywhere in the render path.** A package that uses cgo cannot contain Go
assembly — `cmd/go` hands its `.s` files to the C compiler, which rejects Plan 9
directives. This is why `internal/fit/renderer/opencl` is a separate package,
and why the CPU path stays pure Go plus assembly with `CGO_ENABLED=0`.

## Data layout

**Structure-of-arrays circle parameters.** Circle parameters are read all seven
at once per circle and then reused across thousands of pixels, so the access
pattern is exactly what array-of-structures is good at: 56 bytes, one cache
line, 87.5% line utilization. Splitting them into parallel arrays would multiply
cache-line fetches per circle for no gain. The decisive number is that parameter
loading is under 1% of runtime — no layout change there can pay. The
vectorization value is in the interleaved NRGBA pixel buffer, not in the
parameter encoding. `Circle` stays AoS (`internal/fit/types.go`).

## Scalar kernels

**Eight-way unrolled scalar SSD.** Unrolled-4 with int32 differences and a
single float64 conversion at the end is about 1.8× the naive per-pixel cost
function. Unrolling to eight buys a further 13% and risks register spills, so
unrolled-4 is the default (`internal/fit/ssd_scalar.go` keeps both).

## Fixed-point and geometry

**AVX2 Q16.16 span geometry.** 1.4–2.9× *slower* than the scalar Q16.16 span.
AVX2 has no packed eight-lane 32×32→64 multiply; `VPMULDQ` covers even lanes
only, so the vector form needs shuffles that cost more than the scalar
finite-difference walk it replaces.

**Signed Q24.8 and normalized Q8.24 span geometry.** Q24.8's only advantage is
coordinate range Q16.16 already covers 40× over — its limit is a 21845-pixel
canvas — and the eight fraction bits it gives up cost 58× the span
disagreement rate against an exact rational oracle
(58 of 17751 rows against 1 of 17748) at no throughput gain — 1.01×–1.08× on
both core types of an i7-1255U, inside that host's noise. Normalized Q8.24
cannot represent a radius of 128 or more at any center, which is 50.7% of
bounds-legal circles on a 256×256 canvas and 76.0% on a 512×512 one; where it
does apply it is exact, and exact means *different bytes*, so adopting it is a
migration cost rather than a gain. Both alternates and the rational oracle stay
in tree as a test-only harness,
`internal/fit/renderer/circle_geometry_formats_test.go`. See
[`fixed-point-geometry-formats.md`](fixed-point-geometry-formats.md).

**AVX2 float32 span geometry as the production path.** Competitive at small
radii and loses at large ones, where the span walk matters most. Production is
scalar Q16.16 with an exact float64 fallback for geometry Q16.16 cannot
represent; the float32 kernel stays reachable only through
`forceFloat32Geometry` for measurement.

The transferable win from that work was not the number format at all: it is
*monotonic eight-pixel batching*, where one farthest-candidate comparison
certifies eight pixels at a time and finite differences handle the 0–7 tail.
That applies to float32, float64, and fixed point alike, and it is what shipped.

**Circle vertical symmetry.** Mirroring a rendered row onto its partner across
the circle's horizontal axis is exact only when `2*centerY` is an integer — an
integer or half-integer Y center. A continuous optimizer produces one roughly
every 32,768 candidates. Worse, the CLI defaults to several row-sharded workers,
so even an eligible circle usually has its partner row in another shard.
Fully eligible single-worker fixtures gain 1.06–1.17×; four workers gain
nothing. The code stays in tree behind `enableRowSymmetry`, disabled by every
constructor, for discrete-coordinate workloads.

## Incremental cost

**Row-shard-local incremental reduction.** Accumulating the delta inside each
row shard tied with the simpler post-render pass and needed more coordination
between workers. The simpler one shipped.

**A pixel-percentage-only crossover rule.** Dirty *fraction* does not predict
whether incremental scoring wins, because the cost is per span as well as per
pixel: a 32-circle case measured 30.9% dirty but spread across 678 intervals,
where the full-image kernel is faster. The shipped rule prices both — see
[`incremental-cost.md`](incremental-cost.md).

## SIMD tiers

**An SSE2 kernel for SAD.** `FastSAD` has no non-test callers, and its AVX2 form
depends on SSSE3/SSE4.1 instructions that an SSE2-only host does not have. No
kernel was written.

**An SSE2 kernel for Q16.16 span geometry.** SSE2 has no 64-bit signed compare,
and the symbol is 2.80% of the no-AVX2 profile while even the *AVX2* version is
1.6–3.0× slower than the scalar span. `circle_geometry_sse2_amd64.s` was written
and then removed before merge: do not add a kernel to a path production cannot
enter.

**A NEON kernel for Q16.16 span geometry.** Not written, and the ceiling is why.
The eight-pixel batched probe and the finite-difference tail that make up
`fixedCircleQ16.span` are 12.25–15.67% of flat samples on an i7-1255U
(`BenchmarkFit/Render`, `GOMAXPROCS=1`, pinned to a P-core, 10 s profiles: 13.41%
at 64×64/K4, 15.67% at 256×256/K50, 12.25% at 512×512/K100). Forcing the scalar
compositor — the shape ARM64 has below its 256-pixel cutoff — moves 256×256/K50
to 10.01%, because the share shrinks as compositing grows. So an *infinitely
fast* span-edge kernel buys at most 12–16% of render time on this host and less
on ARM64, where the exact compositor is already recorded at 65.01% of an Apple M5
profile.

Against that ceiling, the amd64 sibling is a measured loss, and it was
re-measured here rather than cited: `BenchmarkCircleSpanQ16AVX2Direct`, median of
nine pinned 500 ms runs at `GOMAXPROCS=1`, puts the AVX2 kernel at
7.65/19.09/52.75/116.60 ns against 7.24/8.23/19.74/39.82 ns for the scalar span
at radii 5.25/25.25/100.25/256.25 — 1.06× to 2.93× **slower**, worsening with
radius, and 1.06× to 3.73× slower on an E-core. `spanAVX2` has no production
call site at all; every renderer and polish path calls `span`, which is why the
profiles above name `fixedCircleQ16.span` and never the kernel.

**NEON is genuinely better suited to the arithmetic than either amd64 tier, and
it does not matter.** `SMULL`/`SMULL2` give the full signed 32×32→64 widening
multiply whose absence (`VPMULDQ` covers even lanes only) forced the shuffles
that made the AVX2 kernel slow, and `CMGT` on `.2D` gives the 64-bit signed
compare SSE2 lacks. But a 128-bit register holds two Q32.32 products where AVX2
holds four, and the shape of the loss is not the multiply: the scalar walk
already spends *one* multiply and one compare per eight pixels, because monotonic
eight-pixel batching already extracted that win, and the tail runs on int64
finite differences with no multiply at all. A vector kernel computes eight
independent squared distances where the scalar code computes one, then pays a
vector-to-general-register reduction to make a branch decision the scalar code
makes with a single `CMP`. Halving the lane count against a kernel already
1.06× to 2.93× behind does not recover that.

**The portable geometry layout is not the obstacle, and would not have to
change.** `fixedCircleQ16` already stores `xQ` as `int32`, which is the operand
form `SMULL` wants, `radiusSquared` as `int64` for the row prologue, and
`centerX` as the search origin; `remaining` broadcasts with one `DUP` per row.
`spanAVX2` is written against that struct and adds no field, which is the
existence proof. What *would* compromise the layout is amortizing vector setup
across rows by storing a broadcast or a candidate ramp in the struct — bytes the
portable and amd64 paths pay for and never read — or re-scaling to Q24.8 to keep
candidates in 32 bits without the range guard, which changes quantization for
every path. Neither is needed, so the portable-layout constraint is satisfied
trivially and settles nothing about whether to build it.

Finally, geometry parity is weaker than compositor parity and would be harder to
police: Q16.16 is a quantified approximation that already changes about 0.00074%
of row spans against the float64 oracle, so a divergent NEON kernel would not be
caught by a byte-identity test the way a divergent compositor is. Adding one
would mean a permanent correctness surface on a path production does not enter,
for at most a fraction of a 12–16% ceiling. Do not build it without a *new*
measurement showing span-edge derivation dominating an ARM64 profile.

**Tuning a fallback tier's constants on masked hardware.** The exact SSE2 span
compositor crosses over against scalar at 8 pixels on a Zen 2 with AVX2 masked
off, and at 24 pixels on a host that genuinely lacks AVX2. Only the second kind
of host ever runs the kernel, so only its number is a valid constant. Measure a
fallback tier on hardware that actually reaches it.

**Function-pointer dispatch for the span compositors.** Routing the kernel
through a function pointer defeats `//go:noescape`, moves the 160-byte constant
block to the heap, and makes the kernel measure 5–9× *slower* than scalar. The
compositors are dispatched by direct call behind a build-tagged wrapper;
`TestCompositeOpaqueSpanDoesNotAllocate` pins the property. Hoisting the block to
once per circle did not relax this: it moved the storage one frame up, into the
row walkers, where `TestRenderCircleRowsDoesNotAllocate` pins it — the older test
cannot see that frame. The same rule reaches the benchmarks, which is why
`benchmarkCompositeOpaqueSpan` takes a flag instead of the compositor as a func
value.

**FMA contraction on amd64.** Not a rejected optimization so much as a
prohibition: the exact AVX2 compositor is byte-identical to the scalar path only
because Go's amd64 backend does not contract `a*b+c`. Introducing an FMA there
would silently break parity. `TestCompositeSpanExactFusionContract` pins it. See
[`exact-span-compositors.md`](exact-span-compositors.md).
