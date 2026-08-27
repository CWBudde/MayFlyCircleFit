# Renderer correctness contract

**Validated:** 2026-08-12 · **Historical baseline:** revision `3650d61`

The CPU renderer's correctness guarantee is **byte-for-byte NRGBA equality with
the project's pre-optimization rasterizer** on the exact float64-geometry path,
and exact equality between the production `FastMSECost` and the scalar `MSECost`
for every rendered case. Every optimization since — AABB rejection, the reusable
canvas, strength reduction, scanline sharding, span compositing, the SIMD tiers,
incremental cost — holds that line or does not ship. A short list of paths is
*deliberately* outside it, Q16.16 span geometry among them; they are named at the
end and are not covered by the sentence above.

Read this before changing renderer math, adding a kernel, or relaxing a
tolerance. The mechanism of the current code is in
[`rendering-internals.md`](rendering-internals.md); the exceptions that are
*deliberately* not byte-identical are named at the end.

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
