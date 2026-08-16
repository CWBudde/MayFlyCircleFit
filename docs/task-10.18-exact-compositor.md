# Task 10.18: Exact AVX2 span compositor, and what that means for `--fast-compositing`

**Host:** Linux, AMD Ryzen 5 4600H, 12 logical CPUs, AVX2.
Numbers are local measurements on that machine, not portable guarantees, and
they do not certify a CI result for any revision.

## Why

The opaque span compositor is the single largest symbol in every CPU profile
this repository has taken. On the no-AVX2 profile recorded in
`docs/task-10.17-sse2-report.md` it is 25.17% of flat samples; on the
post-Task-10.12 Apple M5 profile it is 65.01%.

ARM64 has had an exact float64 NEON kernel for it since Task 10.12, and Task
10.19 gave AMD64 one at the SSE2 tier. The widest AMD64 tier still composited
spans one pixel at a time in scalar float64, which is what this closes.

The opt-in float32 compositor added earlier on this branch measured well, but it
measured well *against that scalar loop*, which is not the
comparison that decides whether a reduced-precision path is worth its
reproducibility cost. The right comparison needs an exact vector kernel to exist
first.

## The exact kernel

`compositeSpanExactAVX2` blends two pixels per iteration. Eight bytes widen to
eight dwords and split into two YMM registers of four float64 lanes, so one
register holds exactly one pixel's R, G, B and A and no shuffling is required -
the same structural trick the float32 kernels use, one lane width down. It
shares `exactSpanConstants` with the SSE2 kernel from Task 10.19: the same
twenty-float64 block reads as five four-lane vectors here and ten two-lane ones
there, so there is one layout to keep right rather than two.

It is byte-identical to `compositeOpaqueSpanScalar`, not an approximation, so it
is on by default and there is no flag and no reproducibility caveat attached to
it.

Two details carry that guarantee:

**Op order.** `compositeOpaqueSpanScalar` compiles on amd64 to MULSD/ADDSD
pairs; Go's amd64 backend does not contract `a*b+c` into an FMA. The kernel
reproduces that sequence exactly with VMULPD/VADDPD. Folding either pair into
VFMADD231PS would change the rounding and break parity, and the symptom would
look like a harmless precision artifact rather than a defect.
`TestCompositeSpanExactFusionContract` pins the property by requiring
`fg + bg*blend` to differ from `math.FMA(bg, blend, fg)` on sampled inputs; if
the backend ever starts contracting, it fails with an explanation instead of the
parity tests failing mysteriously. ARM64 has the mirror-image dependency - its
NEON kernel needs the fusion that Go's arm64 backend does perform - and
`composite_span.go` documents that half.

**Alpha.** Lane 3 carries identity constants (inv255=1, bgBlend=1, fg=0,
scale=1, half=0.5), so the chain evaluates to `a + 0.5` and truncates back to
`a`, exactly, for every byte value. Preserving alpha arithmetically rather than
masking the store keeps the kernel branch-free and makes the property testable
against arbitrary alpha bytes, instead of resting on the canvas happening to be
opaque.

Parity is checked by direct kernel calls, not through the dispatcher, so the
crossover cutoff cannot hide short spans from the test: fixed colours across
fifteen span lengths, a 4000-round randomised sweep, an arbitrary-alpha case,
and separate dispatcher tests for the tail and the paired path.

## Measurements

`BenchmarkCompositeOpaqueSpanFast`, median of five 300 ms runs. `scalar f64` is
`compositeOpaqueSpanScalar`, `exact avx2` is the dispatched exact path, and
`fast f32` is the dispatched `--fast-compositing` path.

| Span | scalar f64 | exact avx2 | fast f32 | exact/scalar | fast/exact |
| --- | ---: | ---: | ---: | ---: | ---: |
| 2 px | 10.0 ns | 10.9 ns | 23.2 ns | 0.92× | 0.47× |
| 4 px | 13.1 ns | 14.7 ns | 25.2 ns | 0.89× | 0.58× |
| 8 px | 23.5 ns | 25.1 ns | 33.1 ns | 0.93× | 0.76× |
| 16 px | 46.0 ns | 46.2 ns | 32.9 ns | 1.00× | 1.40× |
| 64 px | 178.0 ns | 127.5 ns | 53.3 ns | 1.40× | 2.39× |
| 256 px | 761.4 ns | 412.6 ns | 111.5 ns | 1.85× | 3.70× |
| 1024 px | 2869.0 ns | 1562.0 ns | 373.8 ns | 1.84× | 4.18× |

Below 16 pixels both vector paths lose, so both dispatch to their scalar
fallbacks and the first three rows are measuring those fallbacks rather than the
kernels. `compositeSpanAVX2MinPixels` is 16 for that reason. It is a measured
crossover, not the vector width: called directly, the exact kernel already beats
scalar at 4 pixels (1.21×), and the gap is the per-span setup of its twenty
float64 constants. Hoisting that setup to once per circle would lower the
crossover and is the obvious next improvement; it is not done here, so short
circle-edge spans still composite scalar.

The ARM64 kernel's cutoff is 256 and deliberately not shared: that one
deinterleaves with VLD4 and widens in three stages, so it has a far larger setup
cost to amortize.

End to end, against this branch's own base, `BenchmarkFit`, median of three
200 ms runs. Every one of these outputs is byte-identical to the base.

| Case | base | branch | Speedup |
| --- | ---: | ---: | ---: |
| Render/64×64/K4 | 3221 ns | 2958 ns | 1.09× |
| Render/128×128/K20 | 82196 ns | 63140 ns | 1.30× |
| Render/256×256/K50 | 708230 ns | 522114 ns | 1.36× |
| Render/512×512/K100 | 5464354 ns | 3818588 ns | 1.43× |
| Pipeline/Sequential/K4 | 18658082 ns | 17710040 ns | 1.05× |
| Pipeline/Batch/K6/B2 | 24192493 ns | 21550334 ns | 1.12× |
| Pipeline/Joint/K8 | 12132024 ns | 10616870 ns | 1.14× |

## What that means for `--fast-compositing`

The flag survives, and the reasoning changed.

The question the exact kernel was written to answer is whether a
reproducibility-breaking option is still worth having once an exact vector path
exists. The answer is yes, by a wide margin: at the span lengths that dominate a
real render the float32 kernel is still 2.4× to 4.2× faster than the exact
kernel, on top of the exact kernel's own 1.4× to 1.85× over scalar. That is a
large enough difference to be a legitimate user-facing choice rather than an
experiment, so it stays a documented CLI flag, off by default, with the exact
path as the default and the oracle.

Below 16 pixels the fast path is *slower* than the exact path as well as less
accurate, because its own scalar fallback is slower than the exact float64 loop
it replaces. That is a genuine wart of the current cutoffs and is documented in
`docs/known-limitations.md`.

### The accuracy claim, actually measured

The ±1-per-channel bound was previously asserted against five hand-picked
colours. It is now swept over every byte value against 2010 colours - ten
corner and near-degenerate cases plus 2000 randomised - which is 2,074,320
channel writes. The bound holds, and 16 of those writes (0.001%) actually
reached it.

That distribution matters for reading the number. The error is not gradual
precision loss; it is a tie-breaking flip at half-integer boundaries introduced
by regrouping `(fg + (p/255)*bg)*255` into `(fg*255 + 0.5) + p*bg`. Whether a
given colour trips it depends on where its products land relative to .5, so
sparse sampling can miss a whole colour class, and the observed rate says
nothing about the rate for any particular image.

`TestCompositeOpaqueSpanFastDoesNotAccumulate` covers the property the
single-span bound depends on when circles overlap: 200 stacked composites at low
alpha stay inside ±1, because each layer contracts the existing value by
`1-alpha` and so shrinks an inherited difference rather than compounding it.

### Reproducibility

A run with the flag on takes a different optimizer trajectory, not merely a
slightly different picture: a changed channel changes the SSD, which changes an
accept/reject decision. Two runs with the same seed and different compositing
settings converge to different circle sets and different final costs. There is
no experiment here on the *quality* impact of that, only on speed.

The setting is recorded where a later reader can find it: `fastCompositing` in
`app.JobConfig`, and therefore in the checkpoint, so a resume replays with the
same compositor; and in the server job detail view next to the seed rather than
among the tuning knobs. Startup logs now name both the tier and both installed
compositors, so a log is enough to tell whether two runs are comparable.

## Not done

- **No NEON float32 kernel.** ARM64 has the exact kernel only, so
  `--fast-compositing` there falls back to a float32 scalar loop that is slower
  and less accurate than the default - a pure loss, warned about at startup.
  Adding one is gated on ARM64 renderer CI existing at all; see
  `docs/known-limitations.md`.
- **Per-span constant setup is not hoisted**, which is why the crossover is 16
  rather than around 4.
