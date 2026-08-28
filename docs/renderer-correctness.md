# Renderer correctness contract

**Validated:** 2026-08-12 · **Historical baseline:** revision `3650d61`

The CPU renderer's correctness guarantee is **byte-for-byte NRGBA equality with
the project's pre-optimization rasterizer** on the exact float64-geometry path,
and exact equality between the production `FastMSECost` and the scalar `MSECost`
for every rendered case. Every optimization since — AABB rejection, the reusable
canvas, strength reduction, scanline sharding, span compositing, the SIMD tiers,
incremental cost — holds that line or does not ship. A short list of paths is
*deliberately* outside it, Q16.16 span geometry among them, and one row-span
rule turned out to be outside it without anyone having said so; all of them are
named at the end and none is covered by the sentence above.

Read this before changing renderer math, adding a kernel, or relaxing a
tolerance. The mechanism of the current code is in
[`rendering-internals.md`](rendering-internals.md); the exceptions that are
*deliberately* not byte-identical are named at the end. The boundary cases —
fractional and tangent rows, radius extremes, clipping, batch strides,
randomized circles, row sharding — are measured across architectures and SIMD
tiers in
[`renderer-precision-measurements.md`](renderer-precision-measurements.md).

## The oracle

`internal/fit/renderer/renderer_correctness_test.go` carries a test-only
implementation of the renderer as it stood at `3650d61`. It deliberately does
**not** share production helpers: it keeps the original bounding-box traversal,
Porter-Duff arithmetic, and `math.Round` conversion. An oracle that reuses the
code under test proves nothing.

Each case is rendered by the oracle and by the current renderer at one worker
and at the default maximum. Full pixel buffers are compared with no tolerance,
repeated renders verify the background reset, and costs are compared exactly
against scalar MSE.

```sh
go test -count=1 -run '^TestCPURendererMatchesPreOptimizationBaseline$' \
  ./internal/fit/renderer
```

## The matrix

Deterministic, and it covers:

- zero, one, several, 24, and 48 circles;
- 1×1, small, odd, rectangular, and 257×193 images;
- minimum-radius, oversized, edge-clipped, fully off-canvas, and fractional circles;
- transparent, sub-`0.001`, boundary-`0.001`, translucent, and opaque circles;
- heavily overlapping circles;
- white and nonuniform custom canvases.

Focused tests elsewhere add empty images, invalid parameter lengths, independent
image origins and strides, SIMD remainder widths, repeated parallel rendering,
thread counts above the image height, and pipeline replay consistency.

## The regression this matrix caught

The AABB opacity fast-reject originally skipped circles with opacity below
`0.001`. Such a circle *can* change an 8-bit channel on a non-white or custom
canvas, so the shortcut was not free — it was a silent output change on exactly
the inputs nobody benchmarks. The renderer now skips only circles whose opacity
is **exactly zero**, and the `0.001` boundary stays in the matrix so the mistake
cannot return.

The general lesson: an early-reject threshold chosen from a white-canvas
benchmark is not validated for the canvases production actually uses.

## Verification gates

```sh
just test
go test -race -short ./...
just lint
just build
```

All four passed on the validation host after the opacity correction, including
the complete race-enabled short suite.

## Standing tradeoffs

These are properties of the contract, not defects:

- **The output buffer is mutable and reused.** A `CPURenderer` returns one
  buffer and overwrites it on the next render. A caller that needs to retain an
  image must copy it.
- **One renderer is not concurrency-safe.** Simultaneous `Render` or `Cost`
  calls on a single instance are unsupported; independent instances and the
  renderer's own row workers are safe.
- **Threading is not free.** Scheduling and allocation overhead can make
  multi-worker rendering slower on tiny workloads; `--threads 1` is the explicit
  serial option. See [`cpu-rendering-threads.md`](cpu-rendering-threads.md).
- **The objective ignores alpha.** It compares RGB bytes and is not a perceptual
  color metric.
- **Edges are not antialiased.** The guarantee is parity with this project's
  historical rasterizer, not agreement with a vector renderer.

## Where byte-identity is deliberately not the contract

Two paths are approximations by design, and both are quantified rather than
hand-waved:

- **Q16.16 circle geometry** changes about 0.00074% of row spans against the
  float64 oracle, and geometry it cannot represent falls back to that oracle
  exactly.
- **`--fast-compositing`** is accurate to ±1 per channel and therefore breaks
  cost comparability outright. See
  [`exact-span-compositors.md`](exact-span-compositors.md) and
  [`schedule-format.md`](schedule-format.md).
- **The OpenCL renderer** computes in float32 throughout — geometry, blending,
  and the SSD reduction — against a float64 CPU path, so it is held to a
  measured budget instead: **±2 per channel and 1% relative cost**. Measured on
  an NVIDIA T550 across canvases from 32² to 256² and one to twenty-four
  circles, the worst case is 1 channel and 0.021% of cost, so both budgets hold
  with room. `TestOpenCLDeviationBudget` re-measures and reports it on every
  run, so these figures can be checked rather than trusted. Note which way the
  two bounds bind: the largest cost error comes from summing a million float32
  residuals on the device, not from geometry, and it grows with the canvas.

  This budget is a statement about arithmetic, and it only became one after the
  kernel stopped disagreeing about *rasterization*. See below.

- **An OpenCL staged session over a retained canvas** is held to that same
  budget against the CPU, and to a *stricter* rule against itself: compositing
  one circle onto a canvas the device produced must be **byte-identical** to
  replaying the whole draw order from white. That is not a tolerance choice, it
  is a property of the kernel. The circle loop quantizes to eight bits after
  every layer to mirror the CPU's NRGBA round-trip, so the colour state after
  `D` circles is already an exact eight-bit value and reading it back as
  `byte / 255` recovers the identical `float32`. There is no stage-boundary
  error to absorb, so any difference at all is a defect.
  `TestOpenCLAccumulatedCanvasMatchesReplay` asserts it at tolerance zero.

  The base canvas must be **opaque**, and `NewSessionWithCanvas` refuses one
  that is not. The kernel composites premultiplied and writes an opaque image
  back, while the CPU renderer takes a different compositing path for a canvas
  that is not opaque, so the two are only known to agree on opaque canvases.
  Every canvas the pipelines can supply comes from `Render`, which writes alpha
  255 unconditionally, so a translucent one is a bug rather than a use case.

And one place where it was not the contract and nobody had said so. The span
search starts at `int(centerX+0.5)` and walks outward without ever testing that
pixel, so **every row the disc touches paints its nearest sample**, including a
tangent row where the pre-optimization rasterizer's `dx*dx + dy*dy <= r*r` test
paints nothing. A center of `(10.5, 10)` with radius `1` reproduces it on row 11.
This is a property of the center-out search, not of Q16.16: forcing the float64
geometry does the same thing, and so does every architecture and every tier. It
fires on 1,110 of 12,466,238 intersecting rows (0.0089%), it never *under*-covers,
and it is pinned by `TestSpanSearchAlwaysCoversNearestSample` and
`TestSpanSearchOverCoverageRate` rather than changed, because changing it would
move every recorded cost in `docs/`. The measurement and the argument are in
[`renderer-precision-measurements.md`](renderer-precision-measurements.md).

**The OpenCL kernel did not implement that rule, and this is what an undocumented
invariant costs.** It tested `dx*dx + dy*dy > radius*radius` per pixel — the
pre-optimization disc test — so it dropped exactly the samples the span search
paints unconditionally: both tangent rows of every circle. The rule had been
written down here only after the CPU renderer was measured, and nothing carried
it across to the kernel.

The failure was rare and total rather than small and widespread, which is why
tolerance-based parity tests had not caught it. On a patterned reference it
touched around 0.0005% of pixels, but each one was wrong by up to 226 of 255 —
a completely different colour, not a rounding difference. Its effect on cost
depended entirely on the scene: 0.01% on a busy reference, where a handful of
pixels is lost in the total, and **a factor of two** on a sparse one, where the
circle *is* the cost. A single black circle of radius 1 at `(10.5, 10)` on a
24×24 white canvas cost 451.5625 on the CPU and 225.78125 on the device.

The kernel now applies the CPU predicate exactly:

```
painted(x, y)  ⟺  dy² ≤ r²  ∧  ( x == int(centerX + 0.5)  ∨  dx² + dy² ≤ r² )
```

Both CPU geometry paths agree on it — the float64 oracle in `circleSpanFloat64`
and the production Q16.16 path both start the walk at `int(centerX + 0.5)` and
never test that pixel — so matching it is matching the renderer, not one of its
two implementations. On the same randomized catalog the worst channel deviation
went from 73 to 1. `TestOpenCLPaintsEveryIntersectingRowsNearestSample` pins the
tangent case byte-exactly, with tolerance zero, and
`TestOpenCLDeviationBudget` fails on the old kernel.

Two smaller divergences went with it. The kernel skipped any circle with
`opacity < 0.001`, where the CPU skips only exact zero; both now skip only exact
zero. And rows are now rejected before columns, as the CPU scanline loop does,
which is what makes a zero radius agree: it paints one pixel at an integer
centre and nothing at a fractional one, on both backends.

Everything else on the default path — SSD, delta-SSD, circle span, and the exact
span compositors on every tier — is byte-identical to its own architecture's
scalar oracle, and since the compositors stopped depending on multiply-add
contraction those scalar oracles agree with each other too: a 640x480 scene with
NEON-length spans renders to the same bytes and the same cost under
arm64/NEON and amd64/AVX2, and `internal/fit/renderer` is gated on both ARM64
rows of `ci-native-simd.yml`. That covers the exact renderer path only. The
opt-in fast compositor is still architecture-dependent by design, and nothing
here audits the non-default cost metrics or `internal/opt`, which has its own
unrelated ARM64 failures ([`known-limitations.md`](known-limitations.md)).
