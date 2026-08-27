# Fixed-point span geometry: Q24.8 and Q8.24 against Q16.16

Task 10.20 asked whether the production span geometry should be some other
fixed-point format: signed **Q24.8**, which trades fraction bits for coordinate
range, or normalized **Q8.24**, which trades range for fraction bits. This is
the measurement that answers it.

**Q16.16 stays.** Neither alternate is better on any axis that matters, and both
are worse on at least one: Q24.8 is 58× less accurate against an exact oracle
for no throughput gain, and Q8.24 cannot represent a radius of 128 or more,
which is most of the legal parameter box on any canvas above 128 pixels. The
question is closed; the sections below record why, so it does not have to be
re-run.

The harness that produced every number is committed as
`internal/fit/renderer/circle_geometry_formats_test.go`. It implements both
alternates next to the production path — test-only, no build tags, nothing
exported — so the comparison is reproducible rather than merely reported.

Related: [`rendering-internals.md`](rendering-internals.md) for the span
geometry as implemented, [`renderer-correctness.md`](renderer-correctness.md)
for the parity contract this work does not touch,
[`rejected-optimizations.md`](rejected-optimizations.md) for the geometry ideas
already measured and dropped.

> All timings here are local measurements on the stated hardware. They are not
> portable guarantees and they do not certify a CI result for any revision.

## The three formats

Each format stores coordinates in an `int32` and widens products to `int64`, so
its magnitude limit is `2^31` divided by its scale. "Largest canvas" is the
largest square canvas whose *complete* legal parameter box the format can hold:
`fit.NewBounds` allows a center half a canvas beyond each edge and a radius up
to `max(W,H)`, so the extreme circles are `X = 1.5W - 1` and `R = W`. It is
binary-searched by `TestFixedPointGeometryFormatCanvasLimit` rather than
asserted.

| Format | Fraction bits | Resolution | Magnitude | Largest canvas | Circles it cannot hold |
| --- | ---: | ---: | ---: | ---: | --- |
| Q16.16 (production) | 16 | 1.53e-05 px | ±32768 | 21845² | none this program can produce |
| Q24.8 | 8 | 3.91e-03 px | ±8388608 | 5592405² | none |
| Q8.24, normalized | 24 | 5.96e-08 px | ±128 | 127² | every radius ≥ 128 |

Q16.16 already covers a 21845-pixel canvas: more than 40× the largest canvas
this repository benchmarks (512×512) and 400× the bundled fixture. So **Q24.8's
entire advantage is range nobody needs.** It buys that range by giving up eight
fraction bits, and the rest of this document is what those eight bits cost.

### What Q8.24 has to be normalized *into*

Q8.24 has about ±128 of integer range, so no canvas coordinate fits in it at
all: a raw Q8.24 renderer would be limited to a 127-pixel canvas and would fall
back to float64 for practically every circle. The only way to make the format
usable is to keep absolute coordinates out of the fixed-point domain entirely,
which is what the harness's `fixedCircleQ8` does:

- the center is stored as its **offset from an integer pixel anchor** —
  `int(X+0.5)` horizontally, which the span search already rounds to, and
  `floor(Y)` vertically — never as an absolute coordinate;
- every distance is formed against the same anchor, so `dx` and `dy` are
  offsets, not positions.

That works, and it is why the "largest canvas" column above is not the whole
story: a normalized Q8.24 circle can sit at *any* center whose integer part fits
an `int32` (`TestFixedPointGeometryFormatRange` covers `X = 1e6, Y = -1e6`).

What normalization cannot fix is the radius. `dx` and `dy` grow to the radius,
so a radius of 128 or more overflows the format no matter where the circle is.
That is a hard limit of 24 fraction bits in an `int32`, not an artifact of the
anchor choice.

The runtime cost of the normalization is one integer subtraction at each of the
four loop entry points and inside the two eight-pixel batch probes. The
finite-difference loops are unaffected, because an anchor cancels out of a
difference. It is small, and it is measured below; it is not the reason the
format loses.

## Method

`circle_geometry_formats_test.go` holds all of it:

- **`fixedCircleQ24` and `fixedCircleQ8`**, each a copy of `fixedCircleQ16.span`
  with its own constants — same center-out search, same monotonic eight-pixel
  batching, same first and second finite differences, same clamps. The
  duplication is deliberate: a shared implementation parameterized by fraction
  count would put a variable shift in the hot loop, and the benchmark would then
  measure the parameterization instead of the format.
- **`exactSpanOracle`**, which answers the same question with `big.Rat`. Every
  accept/reject is an exact rational comparison; float64 is used only to seed a
  four-pixel search window, so a rounded or contracted float can change how many
  comparisons run and nothing else. This matters more than it looks: a float64
  span search is not a valid oracle here, because it is an approximation itself
  *and* Go may contract `a*b+c` into one fused multiply-add, so a float
  comparison could be measuring contraction rather than format precision.
- **`renderCorpusWithFormat`**, which mirrors
  `renderCircleScanlineRowsTracked`'s row walk — the same early rejects, the
  same vertical clamp, the same hoisted `spanBlend`, the same compositor — with
  only the span search swapped. The mirror is pinned, not assumed:
  `TestFixedPointGeometryFormatFullRender` fails unless its Q16.16 arm is
  **byte-identical to `CPURenderer.Render`** on the same corpus, so a difference
  reported for an alternate is a difference in the format and not in the
  harness.

The production float64 path is carried through every comparison as a fourth
column, because it is what an unrepresentable circle already falls back to and
because it is not exact either.

The two heavy sweeps — the rational-oracle corpus and the large render corpus —
are gated behind `testing.Short()`, per this repository's `go test -short`
convention. Everything else runs in the short suite.

## Coordinate range

Verified by `TestFixedPointGeometryFormatRange` and
`TestFixedPointGeometryFormatCanvasLimit`:

| Format | Accepts | Rejects | Fully representable canvas |
| --- | --- | --- | ---: |
| Q16.16 | `X = 32768 − 2⁻¹⁶` | `X = 32768`, `R = 32768` | 21845² |
| Q24.8 | `X = 8388608 − 2⁻⁸`, `X = Y = 40000` | `X = 8388608` | 5592405² |
| Q8.24 | `R = 128 − 2⁻²⁴`, `X = 1e6` with `R = 100` | `R = 128`, `R = 512` | 127² |

The consequence for Q8.24 is quantitative, and
`TestFixedPointGeometryFormatQ8Fallback` measures it on 2000 bounds-legal
circles per canvas, radius uniform in `[1, max(W,H)]`:

| Canvas | Circles Q8.24 cannot represent |
| --- | ---: |
| 128×128 | 0.0% |
| 256×256 | 50.7% |
| 512×512 | 76.0% |
| 1024×1024 | 87.8% |

On a 512×512 canvas, three of four legal circles would run the float64 fallback.
A format that hands most of its work to the path it was supposed to replace is
not a candidate, whatever its precision.

## Adversarial boundaries

`TestFixedPointGeometryFormatAdversarialBoundaries` scans every row of a 129×97
canvas for each case and counts rows whose span differs from the exact oracle.
Two failure modes are counted separately: an **edge displacement** on a row both
agree intersects, and an **intersect flip**, where the quantized radius makes a
row appear or vanish and a whole span is gained or lost.

| Case | Circle | Q16.16 | Q24.8 | Q8.24 | float64 |
| --- | --- | ---: | ---: | ---: | ---: |
| integer center, integer radius | (64, 48, r=20) | 0 | 0 | 0 | 0 |
| fractional center | (64.37, 48.61, r=20.5) | 0 | 0 | 0 | 0 |
| half-pixel center | (64.5, 48.5, r=20) | 0 | 0 | 0 | 0 |
| radius 1 | (64.25, 48.75, r=1) | 0 | 0 | 0 | 0 |
| maximum radius | (64, 48, r=129) | 0 | 0 | fallback | 0 |
| negative center | (−12.25, −8.5, r=30) | 0 | 0 | 0 | 0 |
| clipped off-canvas | (140.75, 100.5, r=45) | 0 | 0 | 0 | 0 |
| tangent row, fractional center | (64.25, 48, r=20) | 0 | 0 | 0 | 0 |
| tangent rows, half-pixel center | (64.5, 48.5, r=20.5) | 0 | 0 | 0 | 0 |
| radius one ulp below a Q16.16 boundary | (64, 48, r = 20 − 2⁻²⁰) | **7** | **7** | 0 | 0 |
| radius one ulp below a Q24.8 boundary | (64, 48, r = 20 − 2⁻¹²) | 0 | **7** | 0 | 0 |

The structural cases are all clean, for every format — including the exact
pixel-boundary case, where `dx² == remaining` exactly and the inclusive rule
decides it identically in all four representations. **Precision only shows up
where a radius sits between two representable values**, which is what the last
two rows construct.

Both epsilon radii are one ulp below `20`, where the span edge lands exactly on
pixels 44 and 84. `2⁻²⁰` is below Q16.16's resolution and below Q24.8's, so both
round the radius up to exactly 20 and grow the circle: 7 rows change, 2 of them
intersect flips. `2⁻¹²` is *representable* in Q16.16 and not in Q24.8, so Q16.16
is exact there and Q24.8 makes the same 7-row mistake. Q8.24 represents both
exactly and is right in every case it can hold.

That ladder is the whole comparison in miniature: each format is exact until the
input needs a bit it does not have, and Q24.8 runs out of bits 256× sooner than
Q16.16 does.

## Randomized rows

`TestFixedPointGeometryFormatRandomizedRows` (long mode) takes 400 circles on a
513×389 canvas, centers across the full bounds box, radii 1–120, seed
`20260828`, and compares every intersecting row against the rational oracle:

| Format | Rows changed | Rate | Intersect flips | Worst edge |
| --- | ---: | ---: | ---: | ---: |
| Q16.16 | 1 of 17748 | 0.0056% | 0 | 1 px |
| Q24.8 | 58 of 17751 | 0.3267% | 3 | 1 px |
| Q8.24 | 0 of 17748 | 0.0000% | 0 | 0 px |
| float64 | 0 of 17748 | 0.0000% | 0 | 0 px |

Q24.8 is **58× the disagreement count of Q16.16** on identical inputs. Read the
Q16.16 figure as an order of magnitude and not a rate: it is a single row, and
this corpus is not the one behind the 0.00074% recorded in
[`renderer-correctness.md`](renderer-correctness.md), which measures against the
float64 span rather than a rational oracle. What the two agree on is that
Q16.16's error is at the edge of measurability, and Q24.8's is not.

Q8.24 and the float64 path both matched the exact oracle on all 17748 rows.
Neither is an argument for a change: see the next section for why an exact
alternate is a migration rather than an improvement.

## Full render

`TestFixedPointGeometryFormatFullRender` renders a corpus three ways and
compares against `CPURenderer.Render` byte for byte. Seed `20260828`.

| Corpus | Format | Bytes differing | Pixels | Worst channel delta |
| --- | --- | ---: | ---: | ---: |
| 192×144, 96 circles, r ≤ 60 | Q16.16 | 0 of 110592 | 0 | 0 |
| | Q24.8 | 24 (0.0217%) | 8 | **82** |
| | Q8.24 | 0 | 0 | 0 |
| | float64 | 0 | 0 | 0 |
| 512×384, 200 circles, r ≤ 200 | Q16.16 | 0 of 786432 | 0 | 0 |
| | Q24.8 | 32 (0.0041%) | 16 | **75** |
| | Q8.24 | 1 (0.0001%) | 1 | 1 |
| | float64 | 1 (0.0001%) | 1 | 1 |

Three things to take from this.

**A displaced span edge is not a rounding error.** The worst channel deltas are
82 and 75, not 1 or 2. A moved edge does not perturb a pixel; it decides whether
that pixel receives a whole compositing step, so the delta is bounded by the
circle's own contrast against the background and can in principle reach 255. The
byte *rate* being small says nothing about how visible a single instance is.

**Q8.24's one differing byte is not its own.** In the 512×384 corpus, 36% of the
circles have a radius of 128 or more and take the float64 fallback. Q8.24's
single mismatch is at byte 502942 — the same byte, with the same delta of 1, as
the float64 column, which the test logs so the inheritance is visible rather
than argued. Everywhere Q8.24 actually ran, it reproduced production output.

**Byte-identical is the bar, and only Q16.16 clears it by construction.** That
is not a coincidence: production output *is* Q16.16's answer. Which makes the
next point the decisive one.

## An exact alternate is a migration, not an improvement

Q8.24 agrees with the exact oracle everywhere Q16.16 disagrees with it. Under
[`renderer-correctness.md`](renderer-correctness.md) that is a **breaking
change, not a fix**: rendered output is a contract,
`TestCPURendererMatchesPreOptimizationBaseline` pins it, and a more accurate
span still changes bytes. A
changed byte changes the SSD, which changes an accept/reject decision, which
sends the optimizer down a different trajectory — the same reproducibility
argument `--fast-compositing` carries in
[`exact-span-compositors.md`](exact-span-compositors.md), and it applies to
precision gained just as much as to precision lost.

So the case for Q8.24 would have to be that the new output is worth
invalidating every recorded measurement and every checkpoint comparison for. The
existing Q16.16 error is 0.0056% of rows, bounded at one pixel of edge
displacement, quantified in the correctness contract, and has an exact float64
fallback for anything it cannot represent. There is nothing there to buy.

## Throughput

`BenchmarkCircleSpanFormats` walks every row of one circle per iteration, so a
result is the cost of a complete circle's span search. Each arm calls its span
method **directly**; routing them through the harness's format registry would
measure the indirection instead, which is the mistake that once made the exact
span compositor look 5–9× slower than scalar
([`exact-span-compositors.md`](exact-span-compositors.md)).

i7-1255U (Alder Lake-P, hybrid), Go 1.26.5, `GOMAXPROCS=1`, pinned with
`taskset`, median of nine 500 ms runs on a P-core (`cpu0`, Golden Cove) and an
E-core (`cpu4`, Gracemont). **Zero allocations and zero bytes per operation on
every arm.** Ratios are against Q16.16 at the same radius; above 1.00 is faster
than production.

P-core (`cpu0`):

| Radius | Q16.16 | Q24.8 | Q8.24 | float64 |
| --- | ---: | ---: | ---: | ---: |
| 5.25 | 83.5 ns | 79.2 ns (1.06×) | 82.1 ns (1.02×) | 98.5 ns (0.85×) |
| 25.25 | 598.7 ns | 516.1 ns (1.16×) | 621.0 ns (0.96×) | 645.3 ns (0.93×) |
| 100.25 | 3584 ns | 3432 ns (1.04×) | 4233 ns (0.85×) | 3900 ns (0.92×) |
| 256.25 | 21092 ns | 19474 ns (1.08×) | unrepresentable | 19605 ns (1.08×) |

E-core (`cpu4`):

| Radius | Q16.16 | Q24.8 | Q8.24 | float64 |
| --- | ---: | ---: | ---: | ---: |
| 5.25 | 123.1 ns | 120.7 ns (1.02×) | 127.9 ns (0.96×) | 137.4 ns (0.90×) |
| 25.25 | 829.6 ns | 782.2 ns (1.06×) | 886.6 ns (0.94×) | 927.6 ns (0.89×) |
| 100.25 | 5746 ns | 5502 ns (1.04×) | 5963 ns (0.96×) | 6067 ns (0.95×) |
| 256.25 | 29681 ns | 29366 ns (1.01×) | unrepresentable | 33142 ns (0.90×) |

**The three formats are the same speed**, which is what the code predicts: they
execute the identical sequence of `int64` multiplies, adds and compares, and
differ only in the constants. Q24.8 measures 1.01×–1.08× on both core types with
one 1.16× outlier the other core does not reproduce at 1.06×; that is the
host's noise floor, not a win. Q8.24 is 0.85×–1.02×, its worst cells at the
larger radii where the anchor subtraction in the batch probes runs most often —
a real cost, and still too small to matter next to the fact that it cannot
represent those radii at all.

The E-core samples were tight (minimum within 10% of median on every arm) and
the P-core samples carry SMT-sibling outliers; read the E-core table as the
lower-noise one. Independent earlier batches on the same host, taken under
heavier load and summarized by per-arm minima, reproduce the same ordering:
Q24.8 within 4% of Q16.16 at every radius, Q8.24 7–17% slower at radius 25 and
above on both core types.

The float64 column is the only interesting one, and it is not a proposal: the
fallback path costs 5–15% more than Q16.16 at most radii, and lands inside the
noise at the largest one on the P-core. That is a small enough gap to be worth
remembering the next time someone assumes the fixed-point search is carrying the
renderer. It is not; the span
compositor is ([`exact-span-compositors.md`](exact-span-compositors.md)).

## The decision

Q16.16 stays, for one reason per alternate:

- **Q24.8 loses 58× accuracy and buys nothing.** Its only advantage is
  coordinate range — a 5.6-million-pixel canvas against Q16.16's 21845 — and
  nothing this program runs is within a factor of 40 of needing even the
  smaller one. It
  is not faster (1.01×–1.08×, within noise, on both core types), and it changes
  rendered output on 24–32 bytes per corpus with channel deltas up to 82.
- **Q8.24 cannot represent the problem.** A radius of 128 or more is
  unrepresentable at any center, which is 50.7% of bounds-legal circles on a
  256×256 canvas and 76.0% on a 512×512 one. Where it does apply it is exact,
  and exact is a *migration cost* under the byte-parity contract, not a gain. It
  is also the slowest of the three at radius 25 and above.

Neither result depends on the harness being clever: the Q16.16 arm of the render
comparison is byte-identical to `CPURenderer.Render`, and every precision
comparison is decided by rational arithmetic rather than by another float.

## What would reopen it

Only a changed constraint, and there are exactly two that would do it:

- **A canvas beyond 21845 pixels on a side**, which would put Q16.16 into its
  float64 fallback for ordinary circles. Q24.8 becomes the right answer at that
  point, and the cost of the switch — 58× the span disagreement rate and a new
  output baseline — is priced above.
- **The byte-parity contract being deliberately rebased**, for some unrelated
  reason. If a new baseline is being taken anyway, normalized Q8.24 would be
  worth re-examining for the sub-128-radius majority of circles, with the
  float64 fallback for the rest — but only if the profile still shows the span
  search mattering, and today it does not: a measured no-AVX2 profile attributes
  2.80% of flat samples to `fixedCircleQ16.span`.

## Not done

- **No architecture-specific kernel for either alternate.** Both were measured
  as scalar Go only. There is no reason to hand-write assembly for a format that
  is not going to ship, and the existing AVX2 Q16.16 kernel is already 1.6×–3.0×
  *slower* than the scalar span
  ([`rejected-optimizations.md`](rejected-optimizations.md)).
- **No end-to-end `BenchmarkFit` comparison.** A whole-render timing would have
  been measuring a format that changes output, against a host noise floor of
  roughly 7% on this laptop, for a per-span difference the microbenchmark puts
  inside that floor. The span search is not the hot symbol.
- **Measured on amd64 only.** The comparison is integer arithmetic with no
  float-contraction exposure, so it should be architecture-independent, but that
  is a derivation and not an ARM64 measurement.
