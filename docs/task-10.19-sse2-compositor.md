# Task 10.19: Exact SSE2 span compositor

**Host.** A 64-vCPU KVM guest at `192.168.30.159` whose CPU is masked to
`QEMU Virtual CPU version 2.5+` and exposes nothing above SSE4.2 — a host that
genuinely lacks AVX2 rather than one masked with `GODEBUG=cpu.avx2=off`. The
masked model hides the underlying microarchitecture, but the timings are real
hardware timings under KVM, not TCG emulation.

These are local measurements, not portable guarantees, and they do not certify
a CI result for any revision.

## Why

The opaque span compositor is the largest symbol in every CPU profile this
repository has taken: 25.17% of flat samples on the no-AVX2 profile recorded in
`docs/task-10.17-sse2-report.md`, and 65.01% on the post-Task-10.12 Apple M5
profile.

ARM64 has had an exact float64 NEON kernel for it since Task 10.12. AMD64 had
none at any tier — the widest AMD64 tier composited spans one pixel at a time in
scalar float64.

## The kernel

`composite_span_amd64.s` blends two pixels per iteration. An XMM register holds
two float64 lanes, so one pixel needs two registers and a pair needs four.

It is byte-identical to `compositeOpaqueSpanScalar`, not an approximation, so it
is on by default and there is no flag and no reproducibility caveat attached to
it.

Two details carry that guarantee:

**Op order.** `compositeOpaqueSpanScalar` compiles on amd64 to MULSD/ADDSD
pairs; Go's amd64 backend does not contract `a*b+c` into an FMA. The kernel
reproduces that sequence exactly with MULPD/ADDPD. Folding either pair into an
FMA would change the rounding and break parity, and the symptom would look like
a harmless precision artifact rather than a defect.
`TestCompositeSpanExactFusionContract` pins the property by requiring
`fg + bg*blend` to differ from `math.FMA` on sampled inputs; if the backend ever
starts contracting, it fails with an explanation instead of the parity tests
failing mysteriously. ARM64 has the mirror-image dependency — its NEON kernel
needs the fusion that Go's arm64 backend does perform — and `composite_span.go`
documents that half.

**Alpha.** Lane 3 carries identity constants (inv255=1, bgBlend=1, fg=0,
scale=1, half=0.5), so the chain evaluates to `a + 0.5` and truncates back to
`a`, exactly, for every byte value. Preserving alpha arithmetically rather than
masking the store keeps the kernel branch-free and makes the property testable
against arbitrary alpha bytes, instead of resting on the canvas happening to be
opaque.

### Three SSE2 constraints, each documented where it would be undone

- **No `PMOVZXBD`.** Bytes widen in two `PUNPCK` steps against a zero register,
  then the high two dwords come down with `PSHUFD` before the second
  `CVTPL2PD`. Roughly half the instructions in the loop are format conversion.
- **Two-operand encodings.** Every constant vector needs both halves resident,
  so ten of the sixteen XMM registers hold constants and only five are working.
  That is why the loop is not unrolled further.
- **Unaligned constants.** Go aligns a `[20]float64` to eight bytes, so all ten
  constant loads are `MOVOU` and no SSE2 memory operand may be used.

The constant block is laid out as five four-lane vectors and read as ten
two-lane ones, so an exact AVX2 kernel added later needs no second layout.

## Measurements

`BenchmarkCompositeSpanExactCutoff`, median of nine 500 ms runs at `-cpu=1`.
This benchmark includes the per-span constant setup, because that is the cost
the dispatch cutoff is actually deciding about.

| Span | scalar f64 | exact sse2 | sse2/scalar |
| --- | ---: | ---: | ---: |
| 2 px | 10.89 ns | 17.04 ns | 0.64× |
| 4 px | 12.46 ns | 19.38 ns | 0.64× |
| 8 px | 22.18 ns | 25.34 ns | 0.88× |
| 16 px | 42.95 ns | 45.20 ns | 0.95× |
| 20 px | 65.12 ns | 72.33 ns | 0.90× |
| 24 px | 87.33 ns | 85.27 ns | 1.02× |
| 32 px | 90.22 ns | 89.50 ns | 1.01× |
| 64 px | 167.65 ns | 155.55 ns | 1.08× |

Zero allocations per operation throughout.

End to end against `master`, `BenchmarkFit/Render`, median of seven 300 ms runs
at `-cpu=1`:

| Case | master | branch | Speedup |
| --- | ---: | ---: | ---: |
| Render/64×64/K4 | 2308 ns | 2331 ns | 0.99× |
| Render/128×128/K20 | 62475 ns | 62249 ns | 1.00× |
| Render/256×256/K50 | 611600 ns | 578124 ns | 1.06× |
| Render/512×512/K100 | 4540816 ns | 4272474 ns | 1.06× |

The two small canvases are unchanged because their spans sit below the 24-pixel
cutoff, which is the cutoff behaving as intended rather than a disappointment.

**1.06× is a modest return for 130 lines of assembly, and that is stated here
rather than dressed up.** The span compositor is 25.17% of this host's flat
profile, so a 1.07× kernel can only ever buy about that much. It ships because
it is byte-identical, needs no flag, and removes the last scalar-only hot loop
on the no-AVX2 tier — not because it is a large win.

### Why the cutoff is 24 and not 8

The same kernel measures 1.35×–1.45× on a Zen 2 laptop with AVX2 masked off, and
crosses over there at about 8 pixels. That curve is not used.

Dispatch selects SSE2 only when AVX2 is *absent*, so the masked-Zen-2
measurement describes a configuration production never enters. Tuning to it
would put the cutoff two thirds below where the real target machine breaks even,
and would produce a kernel that loses on every span between 8 and 24 pixels on
the only hardware that runs it. Where two machines disagree and only one of them
can actually reach the code, the constant belongs to that one.

This is the same mistake, in a new costume, as measuring SSE2 under
`GODEBUG=cpu.avx2=off`: right instruction set, wrong machine.

### The benchmark that lied

Measured through a function pointer instead of a direct call, this kernel
appears **5×–9× slower than scalar**. The indirection defeats `//go:noescape`,
moves the 160-byte constant block to the heap, and adds a malloc per span that
costs more than the kernel saves.

`compositeSpanExactSSE2` is therefore called directly, never through a function
pointer; `TestCompositeOpaqueSpanDoesNotAllocate` pins that, and
`BenchmarkCompositeSpanExactCutoff` reports allocations so the mistake stays
visible to whoever measures next.

## Testing

Parity is checked by direct kernel calls, not through the dispatcher, so the
crossover cutoff cannot hide short spans from the test. SSE2 is baseline on
amd64, so every one of these runs on an AVX2 developer machine and on CI, where
dispatch would never select the kernel — the SSE2 SSD kernel's original tests
made the opposite choice and were skipped everywhere CI runs.

- Byte parity over 18 span lengths × 7 colours, plus a 4000-round randomised
  sweep.
- Arbitrary-alpha preservation over every byte value.
- Zero-pairs early exit, with guard pixels catching any write outside the span.
- The FMA-fusion contract.
- Dispatcher and pair-dispatcher parity, including sub-cutoff spans and the
  odd-pixel tail.
- A forced-tier ladder walk in one process. Forcing AVX2 must land on *scalar*,
  because there is no exact AVX2 assembly here; a switch arm claiming a tier it
  has no code for is what produced the broken revision this replaces.
- Zero allocations from both dispatch entry points.

## Not done

- **No exact AVX2 kernel.** An AVX2 host — the common case — still composites
  scalar. That is a separate change and shares this one's constant layout.
- **The kernel is conversion-bound and was not optimised further.** About half
  its instructions are widening and narrowing, and the alpha lane consumes a
  quarter of both for an arithmetic identity. Skipping alpha would need a
  deinterleave, and `PSHUFB` is SSSE3; freeing registers by making the constant
  vectors uniform and restoring alpha with a mask would allow a four-pixel
  unroll, but loop overhead is only three of forty-three instructions per pair,
  so that buys a few percent at most. Neither was attempted.
- **Per-span constant setup is not hoisted.** The colour is constant for a whole
  circle, so the twenty-float64 block could be built once in
  `renderCircleScanlineRows` instead of once per span of every row. That setup
  is the entire difference between the 8-pixel crossover the direct benchmark
  shows and the 24-pixel one the dispatcher needs, so it is the highest-value
  remaining item here.
