# Cross-platform renderer precision measurements

**Measured:** 2026-08-28 · **Revision:** the branch adding
`internal/fit/renderer/precision_boundaries_test.go` · **Task:** PLAN.md 10.20

This is the boundary-case half of the byte-exact contract in
[`renderer-correctness.md`](renderer-correctness.md). That document's matrix
covers ordinary scenes; this one covers the places where a rendered pixel is
decided by a tie: a coordinate exactly between two Q16.16 steps, a scanline that
touches the disc at one sample, the first radius the fixed-point format cannot
hold, an eight-pixel batch stride that runs off the edge of the canvas.

Every case is measured on two architectures and at every SIMD tier each of them
offers, and all of them agree byte for byte. Nothing here is a timing, and
nothing here can be read as one.

## What the emulator does and does not establish

The ARM64 half of the matrix was produced by cross-compiling the package's test
binary and running it under `qemu-aarch64-static` with binfmt, on the same amd64
workstation as the native half. That is the recipe already recorded in
[`known-limitations.md`](known-limitations.md), where it reproduced the
multiply-add contraction defect exactly.

- **It establishes arithmetic and correctness.** The emulator implements the
  same instruction semantics, `cpu.ARM64.HasASIMD` is set, `fit.Tier()` reports
  `neon`, and the hand-written NEON kernels in `composite_span_arm64.s` and
  `delta_ssd_arm64.s` are the code that executes. A byte that differs between
  the two architectures differs under emulation too — that is how the
  contraction defect was found in the first place.
- **It establishes nothing about throughput.** No number in this document is a
  time, and none may be turned into one. Emulated wall clock is a property of
  the emulator. The ARM64 crossovers (`compositeSpanNEONMinPixels`, the ARM64
  half of the span-blend hoist) still need real hardware, and this document does
  not move them.

## Method

`internal/fit/renderer/precision_boundaries_test.go` holds the whole matrix. The
oracle has two halves, and the file asserts both.

**Within a process** — rendered bytes and the exact cost may not depend on the
SIMD tier or on how the row walk is sharded. Every case is rendered once at one
thread to produce a reference buffer, then again at every tier
`fit.SupportedTiers()` reports and at every shard count, and all of them are
compared byte for byte against that buffer. `fit.SetForcedTier` re-runs every
registered dispatch site, so one process walks the whole ladder without a
subprocess.

**Across processes** — they may not depend on the architecture either, and no
single binary can check that. Every case therefore carries a recorded SHA-256 of
its pixel buffer followed by the eight bytes of its exact cost: the named scenes
and the row-shard scene in `precisionDigests`, the randomized sweep in its own
two constants. The values were measured on amd64 and are asserted unchanged on
ARM64. Including the cost is deliberate: it routes through the tier-dispatched
SSD kernels, so the digest covers those as well as the compositors.

Both halves are needed together, and neither substitutes for the other. A
within-process sweep alone would pass a scene that renders one way on amd64 and
another on ARM64 as long as it stayed self-consistent on each; a digest alone
would say nothing about tiers or shard counts.

Two supporting decisions:

- **The scene data comes from a local SplitMix64**, not from `math/rand`. The
  digests are constants in a committed file, so they must not depend on anything
  outside it. `unitFloat` and `nextByte` are pure `uint64` arithmetic.
- **No test names a worker count.** `precisionShardCounts` derives its counts
  from `runtime.GOMAXPROCS(0)` and the image height and pushes each through
  `effectiveThreadCount`, and the row-shard sweep raises `GOMAXPROCS` for its own
  duration rather than assuming what the host has. The macOS ARM64 CI runner has
  three processors; a hard-coded four fails there and nowhere else.

`fit.SupportedTiers()` is new. It reports every tier this build and CPU can
execute, which is what `SetForcedTier` accepts. The alternative was an
architecture table inside the test, and such a table is wrong the moment it is
copied: `{scalar, sse2, avx2}` compiles on ARM64 and covers nothing there.

## The matrix

35 named scenes, each its own subtest so a failure names the boundary:

| Group | Scenes | What is on the boundary |
| --- | --- | --- |
| `fractional/*` | 6 | Half-, quarter- and eighth-pixel centers and radii; coordinates whose Q16.16 conversion lands exactly on a `.5` tie, in both signs, so `math.Round`'s away-from-zero rule decides them; the two values that straddle the `int(x+0.5)` pixel the center-out search starts from; one scene on a translucent canvas and one at the `0.001` opacity boundary. |
| `tangent/*` | 5 | An integer radius, whose extreme row touches the disc at exactly one sample, and one Q16.16 unit either side of it; a row that touches exactly one pixel; a center half-integer in both axes. |
| `radius/*` | 7 | `fit.MinCircleRadius` at an integer and at a fractional center; `fit.MinCircleOpacity`; the `max(width, height)` cap `fit.NewBounds` imposes; a radius near the signed Q16.16 maximum, with the disc edge still crossing the canvas; the first radius past that range and the first center past it, both of which fall back to the float64 oracle. |
| `clipping/*` | 9 | Partly and wholly beyond each of the four edges, and one circle larger than the canvas in both dimensions. |
| `batch/*` | 8 | The three canvas widths that put the `xEnd+7 < width` stride guard exactly on, one below and one above a multiple of eight, each with circles against the left guard, in the interior, and against the right guard; `circleBatchMinSquare` reached exactly, at a quarter-pixel center where crossing it may not change a pixel and at a half-pixel center where the inclusive `|dx| == R` edge decides two. |

Plus three sweeps that are not scene-shaped:

- **Randomized circles.** `TestRendererPrecisionRandomizedCircles` draws from the
  full legal parameter range `fit.NewBounds` permits — centers up to half a canvas
  dimension beyond each edge, radii from the minimum to `max(width, height)` —
  from the fixed seed `0x5150_1020_5EED`, three circles per draw on a 97x71
  non-white canvas. 512 draws in the ordinary run, 64 under `-short`; every
  rendered byte and every cost of every draw is folded into one digest, and both
  digests are recorded.
- **Row sharding.** `TestRendererPrecisionRowSharding` renders a twelve-circle
  scene at 13 shard counts from one to one worker past the image height, at every
  tier, all compared against the single-threaded walk. That single-threaded walk
  is itself pinned to a recorded digest in `precisionDigests`, under the key
  `sharding`, before it is used as the oracle — otherwise the sweep would prove
  only that the scene is internally consistent, and a scene rendering differently
  on ARM64 while staying shard-invariant there would pass on both architectures.
- **Batch pipeline.** `TestRendererPrecisionBatchPipelineBoundaries` runs
  `AuditCircleBatch` over `minAuditChunkCircles * auditChunksPerWorker * 2`
  circles at one thread, where `planAudit` stays serial, and again multi-threaded,
  where it hands runs of the draw order to several accumulating steppers, and
  requires every field of every `CircleAudit` to match. It then splits the vector
  at 0, 1, 4, 16 and 32 circles and requires `compositeParams` over a retained
  prefix plus its suffix to equal a full render.

## Results

Everything passes, on both architectures, at every tier, at every shard count.
Recorded per run:

| Architecture | Tier | How it was selected | Result |
| --- | --- | --- | --- |
| amd64 | avx2 | detected, and pinned with `CIRCLEFIT_SIMD_TIER=avx2` | pass |
| amd64 | sse2 | `CIRCLEFIT_SIMD_TIER=sse2`, and in-process via `SetForcedTier` | pass |
| amd64 | scalar | `CIRCLEFIT_SIMD_TIER=scalar`, and in-process | pass |
| arm64 (qemu) | neon | detected, and pinned with `CIRCLEFIT_SIMD_TIER=neon` | pass |
| arm64 (qemu) | scalar | `CIRCLEFIT_SIMD_TIER=scalar`, and in-process | pass |

Every recorded digest is identical on the two architectures — that is the whole
claim, and it is what the ARM64 rows of `ci-native-simd.yml` now assert on real
hardware as well.

The commands, exactly as run:

```sh
# amd64, Go 1.26.5 linux/amd64, 12 logical CPUs
go test -count=1 -run 'TestRendererPrecision|TestSpanSearch' ./internal/fit/renderer
CGO_ENABLED=0 CIRCLEFIT_SIMD_TIER=scalar CIRCLEFIT_REQUIRE_SIMD_TIER=scalar \
  go test -count=1 -run 'TestRendererPrecision|TestSpanSearch|TestRequiredSIMDTier' ./internal/fit/renderer
CGO_ENABLED=0 CIRCLEFIT_SIMD_TIER=sse2  CIRCLEFIT_REQUIRE_SIMD_TIER=sse2  go test -count=1 -run … ./internal/fit/renderer
CGO_ENABLED=0 CIRCLEFIT_SIMD_TIER=avx2  CIRCLEFIT_REQUIRE_SIMD_TIER=avx2  go test -count=1 -run … ./internal/fit/renderer

# arm64, qemu-aarch64 8.2.2 plus binfmt, same host
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o /tmp/renderer-arm64.test ./internal/fit/renderer
cd internal/fit/renderer
qemu-aarch64-static /tmp/renderer-arm64.test -test.run 'TestRendererPrecision|TestSpanSearch' -test.v
CIRCLEFIT_SIMD_TIER=scalar CIRCLEFIT_REQUIRE_SIMD_TIER=scalar qemu-aarch64-static /tmp/renderer-arm64.test -test.run …
CIRCLEFIT_SIMD_TIER=neon   CIRCLEFIT_REQUIRE_SIMD_TIER=neon   qemu-aarch64-static /tmp/renderer-arm64.test -test.run …
qemu-aarch64-static /tmp/renderer-arm64.test          # the whole package, exit 0
```

The ARM64 binary logs `architecture arm64, detected tier neon, supported tiers
[scalar neon]`, which is the line that says the NEON kernels really ran.

## The finding: the span search always paints the nearest sample

One case in the matrix disagreed with the historical rasterizer, and the
disagreement is real. It is **not** a cross-platform defect — both architectures
and all five tiers produce the same bytes — so it is recorded here rather than
fixed under this task.

`circleSpanFloat64` and `fixedCircleQ16.span` both start at `int(centerX+0.5)`
and walk outward, and neither ever tests that starting pixel. So on any row the
disc touches at all, that pixel is painted — including a tangent row where its
distance to the center exceeds the radius, and where the pre-optimization
bounding-box rasterizer, which tests `dx*dx + dy*dy <= r*r` per pixel, paints
nothing.

The smallest reproduction is an ordinary optimizer circle: center `(10.5, 10)`,
radius `1`. On row 11 the disc reaches `dy == 1 == R`, so `remaining == 0`, and
the search returns `[11, 12)` while the exact interval is empty. Four bytes
differ from the oracle.

It is a property of the center-out search, not of the number format. Forcing the
float64 geometry (`CPURenderer.forceFloatGeometry`) reproduces it identically, so
it is unrelated to the Q16.16 approximation that
[`renderer-correctness.md`](renderer-correctness.md) already documents.

`TestSpanSearchOverCoverageRate` bounds how often it fires, against an exact
`math.Sqrt` interval that shares no code with the search: **1,110 of 12,466,238
intersecting rows, 0.0089%**, over 200,000 circles with radii in [1, 65] on a
513x389 canvas, and the same 1,110 on amd64 and arm64. The search never
*under*-covers: no row is ever missing a pixel the disc contains.

Why it is pinned rather than changed:

- The rule may well be the intended one. `fit.MinCircleRadius` exists so an
  on-canvas circle "covers at least one pixel", and `fit.RequiredCircleRadius`
  computes a radius from the distance to the *nearest integer pixel sample*. Both
  read as a nearest-sample rasterization convention rather than an exact-disc one.
- Changing it moves rendered output, and therefore every recorded cost in
  `docs/`, on about one row in eleven thousand. That is a decision about the
  objective, not about precision, and it belongs to whoever wants it — with a
  re-measurement, not with this task.

`TestSpanSearchAlwaysCoversNearestSample` pins the four span cases that define
the rule, through both the float64 and the Q16.16 paths, so it cannot drift
silently in either direction.

## What is still not measured

- **ARM64 throughput.** Untouched by this document, for the reason in the first
  section.
- **`--fast-compositing`.** Excluded on purpose. That kernel regroups the blend
  into one multiply-add, is accurate to ±1 per channel, and is deliberately
  architecture-dependent; a byte-exact digest is the wrong instrument for it. See
  [`exact-span-compositors.md`](exact-span-compositors.md).
- **The four deliberately fused sites.** The circle-area heuristics in
  `polish_dirty_cost.go` and `incremental_cost.go`, and the seed separation test
  and `correctiveChannel` in `batch_audit.go`, still choose search behaviour with
  contractable arithmetic, so a fit is not bit-reproducible across architectures
  even though a render is. Nothing here changes that, and nothing here tests it;
  the matrix's batch-pipeline case measures the audit's rendered images and exact
  costs, which do not pass through those sites.
- **The GPU renderer.** `internal/fit/renderer/opencl` is a separate package
  behind a build tag and is not in this matrix.
