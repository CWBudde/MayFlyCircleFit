# Exact span compositors and `--fast-compositing`

The opaque span compositor is the largest symbol in every CPU profile this
repository has taken — 25.17% of flat samples on a no-AVX2 host, 65.01% on an
Apple M5 after span integration. amd64 and arm64 therefore each have a vector
kernel for it, and every one of those kernels is **byte-identical to the scalar
span**, which is why none of them is behind a flag. Every other target — the
supported linux/386 build among them — composites scalar: `compositeSpanKernel`
is a constant `fit.TierScalar` in `composite_span_generic.go`, so the timings
below do not describe it. amd64 and arm64 also fall back to the scalar span when
their tier is unavailable at runtime or the span is shorter than the tier's
cutoff.

The one exception is `--fast-compositing`, a float32 path that is *not* exact and
whose costs are consequently not comparable to anything else.

Related: [`rendering-internals.md`](rendering-internals.md) for the dispatch
model and cutoffs as implemented, [`renderer-correctness.md`](renderer-correctness.md)
for the parity contract these kernels hold, [`schedule-format.md`](schedule-format.md)
for when two campaigns' costs may be compared.

> All timings here are local measurements on the stated hardware. They are not
> portable guarantees and they do not certify a CI result for any revision.

## The kernels

| Architecture | Kernel | Pixels per iteration | Dispatch cutoff |
| --- | --- | ---: | ---: |
| arm64 | `compositeOpaqueSpanNEON` (`composite_span_arm64.s`) | 8, via `LD4` | 256 px |
| amd64 AVX2 | `compositeSpanExactAVX2` (`composite_span_amd64.s`) | 2 | 6 px |
| amd64 SSE2 | `compositeSpanExactSSE2` (`composite_span_amd64.s`) | 2 | 24 px |

The AVX2 kernel widens eight bytes to eight dwords and splits them into two YMM
registers of four float64 lanes, so one register holds exactly one pixel's
R, G, B, A and no shuffling is needed. The SSE2 kernel does the same one lane
width down, two float64 lanes per XMM register. They share one
`exactSpanConstants` block — twenty float64s read as five four-lane vectors by
AVX2 and ten two-lane ones by SSE2 — so there is a single layout to keep right.

The cutoffs are all measured, and they are deliberately **not** shared. Each
kernel has a different setup cost to amortize: the NEON kernel deinterleaves
with `LD4` and widens in three stages, and the AMD64 kernels each pay a
different price for widening bytes into float64 lanes and narrowing back.

Every kernel used to build its blend state once per span. None does now — see
[Hoisting the constant block](#hoisting-the-constant-block), which is why the
AVX2 cutoff is 6 and not the 16 the tables further down were measured against.
The SSE2 cutoff is unchanged at 24 and the NEON cutoff at 256; both are now
upper bounds rather than crossovers.

## The two details that carry byte-parity

**Operation order.** `compositeOpaqueSpanScalar` compiles on amd64 to
MULSD/ADDSD pairs, because Go's amd64 backend does not contract `a*b+c` into an
FMA. Both AMD64 kernels reproduce that sequence exactly. Folding either pair
into an FMA would change the rounding and break parity — and the symptom would
read as a harmless precision artifact rather than as a defect.
`TestCompositeSpanExactFusionContract` pins the property by requiring
`fg + bg*blend` to differ from `math.FMA(bg, blend, fg)` on sampled inputs, so
if the backend ever starts contracting, it fails with an explanation instead of
the parity tests failing mysteriously.

ARM64 used to have the mirror-image arrangement, and it was the wrong way round:
the scalar span was written to fuse so it would match the NEON kernel's `FMLA`,
which made the two agree with each other and with neither amd64 nor the
float64 oracle. The kernel is now *unfused* to match the scalar function — a 1:1
swap of `VMOV`+`VFMLA` for `FMUL`+`FADD`, hand-encoded because Go's arm64
assembler has no vector mnemonic for them. **Do not reintroduce a fused
multiply-add there.** `composite_span.go` documents the scalar half and
`TestCompositeSpanNEONMatchesScalar` is what would catch it; see
[`known-limitations.md`](known-limitations.md).

**Alpha.** Lane 3 carries identity constants (`inv255=1, bgBlend=1, fg=0,
scale=1, half=0.5`), so the chain evaluates to `a + 0.5` and truncates back to
`a` exactly, for every byte value. Preserving alpha arithmetically instead of
masking the store keeps the kernel branch-free and makes the property testable
against arbitrary alpha bytes, rather than resting on the canvas happening to be
opaque.

## Three SSE2 constraints

Each is recorded where someone would otherwise undo it:

- **No `PMOVZXBD`.** Bytes widen in two `PUNPCK` steps against a zero register,
  then the high two dwords come down with `PSHUFD` before the second
  `CVTPL2PD`. Roughly half the loop is format conversion.
- **Two-operand encodings.** Every constant vector needs both halves resident,
  so ten of sixteen XMM registers hold constants and only five are working
  registers. That is why the loop is not unrolled further.
- **Unaligned constants.** Go aligns a `[20]float64` to eight bytes, so all ten
  constant loads are `MOVOU`; no SSE2 memory operand may be used.

## Measurements

### AVX2 — Ryzen 5 4600H

`BenchmarkCompositeOpaqueSpanFast`, median of five 300 ms runs. `fast f32` is
the dispatched `--fast-compositing` path.

| Span | scalar f64 | exact avx2 | fast f32 | exact/scalar | fast/exact |
| --- | ---: | ---: | ---: | ---: | ---: |
| 2 px | 10.0 ns | 10.9 ns | 23.2 ns | 0.92× | 0.47× |
| 4 px | 13.1 ns | 14.7 ns | 25.2 ns | 0.89× | 0.58× |
| 8 px | 23.5 ns | 25.1 ns | 33.1 ns | 0.93× | 0.76× |
| 16 px | 46.0 ns | 46.2 ns | 32.9 ns | 1.00× | 1.40× |
| 64 px | 178.0 ns | 127.5 ns | 53.3 ns | 1.40× | 2.39× |
| 256 px | 761.4 ns | 412.6 ns | 111.5 ns | 1.85× | 3.70× |
| 1024 px | 2869.0 ns | 1562.0 ns | 373.8 ns | 1.84× | 4.18× |

> The two AVX2 tables in this section were measured before the constant block was
> hoisted, when `compositeSpanAVX2MinPixels` was 16. They are kept as the record
> of what the hoist improved on; the current numbers are in [Hoisting the
> constant block](#hoisting-the-constant-block).

The two vector columns have different cutoffs. `compositeSpanAVX2MinPixels = 16`
governed the exact column, so its first three rows measure the scalar fallback
rather than the kernel. The fast float32 path enters far earlier —
`fastCompositeAVX2MinPixels = 8` (`fastCompositeSSE2MinPixels = 4`) — so the
8-pixel `fast f32` figure is already vectorized, which is why that column turns
the corner one row before the exact one does.
`compositeSpanAVX2MinPixels = 16` is a measured crossover, not the vector width:
called directly the exact kernel already beats scalar at 4 pixels (1.21×), and
the gap is per-span setup of the twenty float64 constants.

End to end, `BenchmarkFit`, median of three 200 ms runs. Every output is
byte-identical to the base revision.

| Case | base | branch | Speedup |
| --- | ---: | ---: | ---: |
| Render/64×64/K4 | 3221 ns | 2958 ns | 1.09× |
| Render/128×128/K20 | 82196 ns | 63140 ns | 1.30× |
| Render/256×256/K50 | 708230 ns | 522114 ns | 1.36× |
| Render/512×512/K100 | 5464354 ns | 3818588 ns | 1.43× |
| Pipeline/Sequential/K4 | 18658082 ns | 17710040 ns | 1.05× |
| Pipeline/Batch/K6/B2 | 24192493 ns | 21550334 ns | 1.12× |
| Pipeline/Joint/K8 | 12132024 ns | 10616870 ns | 1.14× |

### SSE2 — a host that genuinely lacks AVX2

A 64-vCPU KVM guest masked to `QEMU Virtual CPU version 2.5+`, exposing nothing
above SSE4.2. Real hardware timings under KVM, not TCG emulation.
`BenchmarkCompositeSpanExactCutoff`, median of nine 500 ms runs at `-cpu=1`,
including per-span constant setup because that is what the cutoff decided about
at the time. Zero allocations per operation throughout.

> This table predates the hoist, and `compositeSpanSSE2MinPixels` is still 24
> because of it. Hoisting can only remove cost from the vector path, so a
> post-hoist crossover can only move left: 24 stays correct and merely
> conservative, leaving some spans on scalar that the kernel could now win. It is
> not re-derived here because dispatch selects SSE2 only when AVX2 is absent, and
> the host that produced the numbers above is the class of machine that
> re-derivation needs. The setup measured on the Alder Lake-P host above is 3–8 ns
> per SSE2 span, which is the estimate of how far left it should move — a
> hypothesis for the right hardware, not a constant to ship.

| Span | scalar f64 | exact sse2 | sse2/scalar |
| --- | ---: | ---: | ---: |
| 8 px | 22.18 ns | 25.34 ns | 0.88× |
| 16 px | 42.95 ns | 45.20 ns | 0.95× |
| 24 px | 87.33 ns | 85.27 ns | 1.02× |
| 32 px | 90.22 ns | 89.50 ns | 1.01× |
| 64 px | 167.65 ns | 155.55 ns | 1.08× |

End to end, `BenchmarkFit/Render`, median of seven 300 ms runs at `-cpu=1`:

| Case | before | after | Speedup |
| --- | ---: | ---: | ---: |
| Render/64×64/K4 | 2308 ns | 2331 ns | 0.99× |
| Render/128×128/K20 | 62475 ns | 62249 ns | 1.00× |
| Render/256×256/K50 | 611600 ns | 578124 ns | 1.06× |
| Render/512×512/K100 | 4540816 ns | 4272474 ns | 1.06× |

The two small canvases are unchanged because their spans sit below the 24-pixel
cutoff — the cutoff behaving as intended, not a disappointment.

**1.06× is a modest return for 130 lines of assembly, and that is stated rather
than dressed up.** The span compositor is 25.17% of that host's flat profile, so
a 1.07× kernel can only ever buy about that much. It ships because it is
byte-identical, needs no flag, and removes the last scalar-only hot loop on the
no-AVX2 tier.

## Hoisting the constant block

The twenty float64s are a pure function of the circle's colour and opacity, so
they are now built **once per circle** rather than once per span of every row.
`renderCircleScanlineRowsTracked` and `polishDirtySession.compositeCircleDirtyRows`
each construct a `spanBlend` after their early rejects and thread a pointer to it
down to `compositeOpaqueSpan`. The second site mattered more than it looks:
`compositeDirtySpan` reaches the compositor once per dirty sub-span, so one row
could rebuild the block several times.

`spanBlend` deliberately carries neither the colour nor the tier. The colour
keeps its own route to `compositeOpaqueSpanScalar`, so the fallback's arithmetic
is visibly untouched by the hoist rather than argued about; the tier and its
cutoff stay read at call time, because a snapshot taken when a circle started
would survive a tier change every dispatch site is required to follow.
`TestSpanBlendSurvivesTierChange` pins that by requiring two blends for the same
colour, built under different forced tiers, to be identical — byte parity alone
cannot catch a cached tier, because every exact kernel is byte-identical to
scalar by construction. On every architecture without an exact vector span
compositor `spanBlend` is an empty struct, which is what lets the row walkers
stay free of build tags.

### ARM64

ARM64 is hoisted the same way, and by the same frames. Its `spanBlend` carries
four float64s rather than twenty, because `compositeOpaqueSpanNEON` takes
`fgR`, `fgG`, `fgB` and `bgBlend` as arguments instead of reading a constant
block, and `compositeOpaqueSpan` recomputed all four on every span. The
arithmetic is unchanged: `newSpanBlend` evaluates the same three products and
the same subtract the scalar span does, in the same expression shape, so the
kernel and its reference still start from bit-identical foregrounds. Nothing in
`composite_span_arm64.s` changed — in particular the deliberately *unfused*
`FMUL`+`FADD` pairs that keep the kernel byte-identical to the scalar span are
untouched, and reintroducing a fused multiply-add there would break parity; see
[`known-limitations.md`](known-limitations.md).

`TestRenderCircleRowsDoesNotAllocate` and `TestSpanBlendSurvivesTierChange` now
exist on ARM64 as well, walking the scalar and NEON tiers instead of scalar,
SSE2 and AVX2. Both, and the whole `internal/fit/renderer` short suite, were
observed passing on a cross-compiled ARM64 test binary under
`qemu-aarch64-static`, which reports NEON and runs the kernel. That is a
correctness result and nothing else: **no timing in this document was measured
on ARM64 by this change, and an emulated timing would not count.**

`compositeSpanNEONMinPixels` stays at 256, which is now an upper bound rather
than the crossover, by the same argument that keeps `compositeSpanSSE2MinPixels`
at 24. 256 was measured on an Apple M5 with the four scalars rebuilt per span.
Hoisting only removes cost from the vector path, so a post-hoist crossover can
only move left: 256 stays correct and merely conservative, leaving some spans on
scalar that the kernel could now win, and regressing nothing. Re-deriving it
needs ARM64 benchmarking hardware — the Apple M5 that produced 256, or another
ARM implementation with its own recorded provenance. The ARM64 rows of
`ci-native-simd.yml` do run this package now, but they establish correctness,
not throughput. `BenchmarkCompositeOpaqueSpanNEONCutoff` is the command that
re-derives it: it reports a `scalar`, a `neon_hoisted` and a `neon_rebuilt` arm
at nine span lengths, so the same run yields both the new crossover and the
per-span setup the hoist removed.

What the hoist removes per span on ARM64 is three multiplies and a subtract,
against the twenty stores it removes on AMD64, so the shift should be smaller in
absolute terms than the 6-9 ns and 3-8 ns recorded above. It is not necessarily
smaller in *pixels*: the NEON kernel's own floor is set by `VLD4` and three
widening stages, and where that floor sits relative to the scalar span decides
how many pixels the removed setup is worth. That is a hypothesis for the right
hardware, not a number.

### What it cost per span

`BenchmarkCompositeSpanExactCutoff` (block rebuilt per span, the old behaviour)
against `BenchmarkCompositeSpanExactHoistedCutoff` (block built once, the current
behaviour). i7-1255U, median of nine 500 ms runs at `GOMAXPROCS=1`, pinned with
`taskset`, zero allocations throughout. The part is hybrid, so both core types
are reported: Gracemont splits a 256-bit operation into two 128-bit µops.

P-core (`cpu0`, Golden Cove, 4.7 GHz):

| Span | scalar | avx2 rebuilt | avx2 hoisted | setup | sse2 rebuilt | sse2 hoisted | setup |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 px | 8.73 ns | 17.19 ns | 11.26 ns | 5.93 ns | 13.14 ns | 10.48 ns | 2.66 ns |
| 4 px | 11.08 ns | 17.23 ns | 11.46 ns | 5.77 ns | 15.41 ns | 12.74 ns | 2.67 ns |
| 6 px | 14.76 ns | 18.60 ns | 11.72 ns | 6.88 ns | 18.55 ns | 14.23 ns | 4.32 ns |
| 8 px | 19.28 ns | 19.48 ns | 12.03 ns | 7.45 ns | 22.10 ns | 16.96 ns | 5.14 ns |
| 16 px | 38.09 ns | 25.47 ns | 16.62 ns | 8.85 ns | 38.87 ns | 32.74 ns | 6.13 ns |
| 24 px | 57.95 ns | 36.54 ns | 24.48 ns | 12.06 ns | 55.65 ns | 49.83 ns | 5.82 ns |
| 64 px | 153.90 ns | 76.16 ns | 67.12 ns | 9.04 ns | 138.50 ns | — | — |

E-core (`cpu4`, Gracemont, 3.5 GHz):

| Span | scalar | avx2 rebuilt | avx2 hoisted | setup | sse2 rebuilt | sse2 hoisted | setup |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 px | 10.51 ns | 15.03 ns | 11.56 ns | 3.47 ns | 16.79 ns | 12.73 ns | 4.06 ns |
| 4 px | 14.76 ns | 17.64 ns | 13.30 ns | 4.34 ns | 19.25 ns | 14.10 ns | 5.15 ns |
| 6 px | 22.17 ns | 25.02 ns | 15.82 ns | 9.20 ns | 27.05 ns | 19.78 ns | 7.27 ns |
| 8 px | 29.11 ns | 29.27 ns | 20.80 ns | 8.47 ns | 31.94 ns | 24.62 ns | 7.32 ns |
| 16 px | 56.32 ns | 50.24 ns | 41.63 ns | 8.61 ns | 57.99 ns | 47.41 ns | 10.58 ns |
| 24 px | 83.40 ns | 71.05 ns | 62.22 ns | 8.83 ns | 80.95 ns | 74.40 ns | 6.55 ns |
| 64 px | 219.50 ns | 174.80 ns | 166.40 ns | 8.40 ns | 200.50 ns | 192.30 ns | 8.20 ns |

The `sse2` 64-pixel P-core cell is omitted rather than reported: its nine samples
ranged 144.6–227.8 ns against a 136.0–143.4 ns spread for the same length in the
other benchmark, which is a thermal artifact of that sub-benchmark and not a
measurement of anything.

Setup is 6–9 ns for AVX2 and 3–8 ns for SSE2, roughly flat in span length, which
is what a fixed block of twenty stores should look like.

### The new AVX2 cutoff: 6

The AVX2 kernel has a floor of about 11.5 ns on the P-core that barely moves from
4 to 6 pixels, while the scalar span grows past it. So 4 pixels still loses on
the P-core (0.97×) while winning on the E-core (1.11×), and 6 wins on both
(1.26× P, 1.40× E). Where the two core types disagree the **larger** length is
taken: dispatch cannot know which one it landed on, and a cutoff set too high
only leaves some spans on scalar, while one set too low loses on every span in
the gap.

`BenchmarkCompositeOpaqueSpanBlend` confirms it through the real dispatcher —
same host and protocol, and every length from 6 upward wins on both core types:

| Span | P scalar | P dispatched | P ratio | E scalar | E dispatched | E ratio |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 px | 10.93 ns | 11.37 ns | 0.96× | 14.68 ns | 15.85 ns | 0.93× |
| 6 px | 14.83 ns | 12.46 ns | 1.19× | 22.16 ns | 18.46 ns | 1.20× |
| 8 px | 19.41 ns | 12.85 ns | 1.51× | 28.88 ns | 22.47 ns | 1.29× |
| 16 px | 37.84 ns | 19.09 ns | 1.98× | 56.75 ns | 43.45 ns | 1.31× |
| 24 px | 56.88 ns | 26.49 ns | 2.15× | 84.03 ns | 64.21 ns | 1.31× |
| 64 px | 150.80 ns | 67.22 ns | 2.24× | 221.40 ns | 169.30 ns | 1.31× |

Below 6 the dispatcher takes the identical scalar path it always did; those rows
measure the branch, not a regression.

**The provenance changed.** The previous 16 was measured on a Ryzen 5 4600H
(Zen 2). 6 is measured on an Alder Lake-P laptop. That is a differently sourced
single-machine constant, not a better-sourced one.

### End to end

`BenchmarkFit`, base revision `e32b907` against this branch, both compiled to
test binaries and run **interleaved** in six alternating rounds of `-count=2` at
`-benchtime=300ms`, `GOMAXPROCS=1`, pinned to `cpu0`. Interleaving is not
decoration: a straight before-then-after run on this laptop drifted the
`Fit/Cost` arms — which this change cannot touch — by +6% to +11%, and an
unpinned run put them at ±134%.

| Case | base | branch | Change | p |
| --- | ---: | ---: | ---: | ---: |
| Render/64×64/K4 | 2.109 µs | 1.562 µs | **−25.9%** | 0.000 |
| Render/128×128/K20 | 42.44 µs | 33.05 µs | **−22.1%** | 0.000 |
| Render/256×256/K50 | 338.4 µs | 319.7 µs | −5.5% | 0.003 |
| Render/512×512/K100 | 2.429 ms | 2.281 ms | −6.1% | 0.017 |
| Cost/64×64/MSE | 12.18 µs | 11.83 µs | ~ | 0.139 |
| Cost/64×64/FastMSE | 1.163 µs | 1.123 µs | ~ | 0.259 |
| Cost/256×256/MSE | 184.2 µs | 183.4 µs | ~ | 0.713 |
| Cost/256×256/FastMSE | 20.61 µs | 19.20 µs | −6.8% | 0.020 |
| Cost/512×512/MSE | 765.0 µs | 736.1 µs | ~ | 0.101 |
| Cost/512×512/FastMSE | 74.52 µs | 73.58 µs | ~ | 0.319 |

The `Cost` rows are the control: they run identical code on both revisions. Four
of the five are null, and the fifth moved 6.8%, which is the honest noise floor
of this host. Read the two small-canvas `Render` results — 26% and 22% — as real
and the two large-canvas ones as at or barely above that floor.

The split is what the mechanism predicts. A 512×512 canvas is mostly long spans
that vectorized under the old cutoff too, so it gains only the removed setup. A
64×64 canvas is mostly spans between 6 and 15 pixels, which used to take the
scalar path entirely and now do not.

`BenchmarkCPURendererOpaqueSpan` at 512×512/K100, six pinned runs each, agrees
and isolates it: `horizontal_span_avx2` −3.7% (p=0.002), `pixel_loop` unchanged
(p=0.394). The unconditional `newSpanBlend` call therefore costs the non-opaque
canvas path nothing measurable, and the `--fast-compositing` arms of
`BenchmarkCompositeOpaqueSpanFast` are likewise unchanged.

Zero allocations per operation throughout, on every arm above.

## Two methodology lessons worth more than the kernels

**Tune a fallback tier on hardware that reaches it.** The same SSE2 kernel
measures 1.35×–1.45× on a Zen 2 laptop with AVX2 masked off and crosses over
there at about 8 pixels. That curve is *not used*: dispatch selects SSE2 only
when AVX2 is absent, so the masked-Zen-2 measurement describes a configuration
production never enters. Tuning to it would put the cutoff two thirds below
where the real target breaks even, producing a kernel that loses on every span
between 8 and 24 pixels on the only hardware that runs it. Right instruction
set, wrong machine.

**A benchmark that lied.** Measured through a function pointer instead of a
direct call, this kernel appears **5×–9× slower than scalar**. The indirection
defeats `//go:noescape`, moves the 160-byte constant block to the heap, and adds
a malloc per span that costs more than the kernel saves.
`compositeSpanExactSSE2` is therefore called directly, never through a function
pointer; `TestCompositeOpaqueSpanDoesNotAllocate` pins that, and
`BenchmarkCompositeSpanExactCutoff` reports allocations so the mistake stays
visible to whoever measures next.

## Testing

Parity is checked by **direct kernel calls, not through the dispatcher**, so the
crossover cutoff cannot hide short spans from the test. SSE2 is baseline on
amd64, so all of these run on an AVX2 developer machine and on CI, where
dispatch would never select the kernel — the SSE2 SSD kernel's original tests
made the opposite choice and were skipped everywhere CI runs.

- Byte parity over 18 span lengths × 7 colours, plus a 4000-round randomised sweep.
- Arbitrary-alpha preservation over every byte value.
- Zero-pairs early exit, with guard pixels catching any write outside the span.
- The FMA-fusion contract.
- Dispatcher and pair-dispatcher parity, including sub-cutoff spans and the odd tail.
- A forced-tier ladder walk in one process.
- Zero allocations from both dispatch entry points, and separately from the
  circle row walk that now owns the constant block —
  `TestCompositeOpaqueSpanDoesNotAllocate` only sees the compositor's own frames,
  so `TestRenderCircleRowsDoesNotAllocate` covers the frame the block actually
  lives in.
- Tier-independence of `spanBlend`, so nobody caches the tier or the cutoff into
  it.

The last three exist on ARM64 as well, over the scalar and NEON tiers, and
`TestCompositeSpanNEONMatchesScalar` is the kernel's own parity contract. Its
k/255 colour sampling is load-bearing and must not be replaced with uniform
random floats: the kernel and the scalar reference diverge only where a product
lands on a half-integer boundary, and uniform floats essentially never do — an
earlier sweep found zero mismatches in 51.2 million evaluations against a
reference that was genuinely wrong.

## `--fast-compositing`

The flag survives, and the reason it survives is the comparison the exact
kernels made possible. A reduced-precision path measured against a *scalar* loop
proves nothing; measured against an exact vector kernel, the float32 path is
still **2.4× to 4.2× faster** at the span lengths that dominate a real render,
on top of the exact kernel's own 1.4×–1.85× over scalar. That is a large enough
difference to be a legitimate user-facing choice rather than an experiment. It
stays off by default, with the exact path as both the default and the oracle.

Below 16 pixels the fast path is *slower* than the exact path as well as less
accurate, because its own scalar fallback is slower than the exact float64 loop
it replaces. That wart is recorded in
[`known-limitations.md`](known-limitations.md).

### The accuracy claim, actually measured

The ±1-per-channel bound was originally asserted against five hand-picked
colours. It is now swept over every byte value against 2010 colours — ten corner
and near-degenerate cases plus 2000 randomised — which is 2,074,320 channel
writes. The bound holds; 16 of those writes (0.001%) actually reached it.

The distribution matters more than the rate. The error is **not gradual
precision loss**: it is a tie-breaking flip at half-integer boundaries
introduced by regrouping `(fg + (p/255)*bg)*255` into `(fg*255 + 0.5) + p*bg`.
Whether a colour trips it depends on where its products land relative to `.5`,
so sparse sampling can miss a whole colour class and the observed rate says
nothing about the rate for any particular image.

`TestCompositeOpaqueSpanFastDoesNotAccumulate` covers what the single-span bound
depends on when circles overlap: 200 stacked composites at low alpha stay inside
±1, because each layer contracts the existing value by `1-alpha` and so shrinks
an inherited difference rather than compounding it.

### Reproducibility

A run with the flag on takes a different optimizer *trajectory*, not merely a
slightly different picture: a changed channel changes the SSD, which changes an
accept/reject decision. Two runs with the same seed and different compositing
settings converge to different circle sets and different final costs. There is
no experiment here on the quality impact of that, only on speed.

The setting is recorded where a later reader finds it: `fastCompositing` in
`app.JobConfig`, and therefore in the checkpoint, so a resume replays with the
same compositor; and in the server job detail view next to the seed rather than
among the tuning knobs. Startup logs name both the tier and both installed
compositors, so a log is enough to tell whether two runs are comparable.

## Not done

- **No NEON float32 kernel.** ARM64 has the exact kernel only, so
  `--fast-compositing` there falls back to a float32 *scalar* loop that is both
  slower and less accurate than the default — a pure loss, warned about at
  startup. Adding one is gated on ARM64 renderer CI existing at all; see
  [`known-limitations.md`](known-limitations.md).
- **The SSE2 cutoff has not been re-derived after the hoist.** 24 is still
  correct but no longer tight; see [Hoisting the constant
  block](#hoisting-the-constant-block). It needs a host that genuinely lacks
  AVX2.
- **The NEON cutoff has not been re-derived after the hoist.** 256 is still
  correct but no longer tight; see [ARM64](#arm64). It needs ARM64 benchmarking
  hardware, and `BenchmarkCompositeOpaqueSpanNEONCutoff` is the command.
- **The SSE2 kernel is conversion-bound and was not optimised further.** About
  half its instructions widen and narrow, and the alpha lane consumes a quarter
  of both for an arithmetic identity. Skipping alpha needs a deinterleave and
  `PSHUFB` is SSSE3; freeing registers by making the constant vectors uniform
  would allow a four-pixel unroll, but loop overhead is three of forty-three
  instructions per pair, so that buys a few percent at most.
