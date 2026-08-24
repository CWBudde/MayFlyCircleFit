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
| amd64 AVX2 | `compositeSpanExactAVX2` (`composite_span_amd64.s`) | 2 | 16 px |
| amd64 SSE2 | `compositeSpanExactSSE2` (`composite_span_amd64.s`) | 2 | 24 px |

The AVX2 kernel widens eight bytes to eight dwords and splits them into two YMM
registers of four float64 lanes, so one register holds exactly one pixel's
R, G, B, A and no shuffling is needed. The SSE2 kernel does the same one lane
width down, two float64 lanes per XMM register. They share one
`exactSpanConstants` block — twenty float64s read as five four-lane vectors by
AVX2 and ten two-lane ones by SSE2 — so there is a single layout to keep right.

The cutoffs are all measured, and they are deliberately **not** shared. Each
kernel has a different setup cost to amortize: the NEON kernel deinterleaves
with `LD4` and widens in three stages, the AMD64 kernels build twenty float64
constants per span.

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

ARM64 has the mirror-image dependency: its NEON kernel *needs* the fusion that
Go's arm64 backend does perform. `composite_span.go` documents that half.

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

The two vector columns have different cutoffs. `compositeSpanAVX2MinPixels = 16`
governs the exact column, so its first three rows measure the scalar fallback
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
including per-span constant setup because that is what the cutoff decides about.
Zero allocations per operation throughout.

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
- Zero allocations from both dispatch entry points.

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
- **Per-span constant setup is not hoisted.** The colour is constant for a whole
  circle, so the twenty-float64 block could be built once in
  `renderCircleScanlineRows` instead of once per span of every row. That setup
  is the entire difference between the 8-pixel crossover the direct benchmark
  shows and the 24-pixel one the dispatcher needs, so it is the highest-value
  remaining item. It is `PLAN.md` Task 10.20.
- **The SSE2 kernel is conversion-bound and was not optimised further.** About
  half its instructions widen and narrow, and the alpha lane consumes a quarter
  of both for an arithmetic identity. Skipping alpha needs a deinterleave and
  `PSHUFB` is SSSE3; freeing registers by making the constant vectors uniform
  would allow a four-pixel unroll, but loop overhead is three of forty-three
  instructions per pair, so that buys a few percent at most.
