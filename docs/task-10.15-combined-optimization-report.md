# Task 10.15: combined CPU optimization report

## Outcome

The production CPU renderer already composes the two proven rendering changes:

- opaque canvases use the horizontal span compositor from Task 10.12;
- representable circle geometry uses scalar Q16.16 with eight-pixel monotonic
  skips from Task 10.13, otherwise it falls back to the float64 oracle.

Task 10.15 adds an integration benchmark and correctness matrix, and evaluates
the proposed vertical-symmetry optimization under the current renderer. Task
10.14 subsequently corrected the prototype to use a true two-ended loop and a
paired scalar compositor. It now wins on fully eligible one-worker fixtures,
but remains disabled because continuous centers are almost never eligible and
four-worker rendering does not improve. The newer exact incremental SSD policy
from Task 10.16 is included in the combined cost result rather than being
mistaken for part of circle geometry.

## Why arbitrary fractional rows cannot be mirrored

The renderer samples circle coverage at integer pixel coordinates. Rows `y1`
and `y2` have equal horizontal spans only when they have equal absolute
distance from the center:

```text
abs(y1 - centerY) == abs(y2 - centerY)
y1 + y2 == 2 * centerY
```

Because both row coordinates are integers, `2*centerY` must also be an integer.
For production Q16.16 geometry this means the quantized Y center must lie on an
integer or half-integer row. Rounding every optimizer center to satisfy that
condition would change coverage and was rejected.

The prototype therefore exposes `fixedCircleQ16.symmetricRowSum` only for
eligible centers. It calculates a span once, composites it onto both rows, and
adds both rows to the dirty-span union. A pair is used only when both rows lie
inside the same worker shard; no goroutine writes another worker's rows. See
`task-10.14-circle-symmetry-report.md` for the final implementation and result.

## Correctness coverage

`combined_optimization_test.go` compares the prototype with the ordinary
row-by-row Q16.16 renderer across:

- integer, half-integer, Q16-rounded half-integer, and ineligible fractional
  centers;
- clipped and overlapping circles;
- opaque white and translucent custom canvases;
- one and four rendering workers;
- full-image cost versus forced exact incremental dirty-region cost; and
- ordinary and staged renderer-session propagation.

The paired and unpaired rendered bytes are identical, and incremental cost is
exactly equal to a full `FastMSECost` replay. Steady-state rendering and cost
evaluation allocate zero bytes.

## Native AMD64 measurements

Host: AMD Ryzen 5 4600H, Linux/amd64, Go 1.26.0, `GOMAXPROCS=1`.
Samples use `-benchtime=750ms -count=7`; medians are reported. The benchmark
fixture is deterministic 512×512/K100 rendering.

| Fractional-center renderer | Median | Relative to old loop | Allocations |
| --- | ---: | ---: | ---: |
| float64 scanline + per-pixel opaque loop | 13.986 ms | 1.00× | 0 |
| production span + Q16.16 | 7.376 ms | **1.90×** | 0 |

This comparison deliberately measures the components together. On this AMD64
host the opaque span implementation is optimized scalar Go; the exact Task
10.12 NEON kernel is ARM64-only. Q16.16 is also scalar because the tested AVX2
integer prototype was slower, as documented in the Task 10.13 report.

### Symmetry selection

The initial span-search-only experiment was inconclusive. The completed Task
10.14 prototype also shares scalar compositing setup and loop control across a
row pair. Eight-sample medians improved by 5.7% for mixed large radii and by
16-17% for fixed R5/R25 on deliberately 100%-eligible, one-worker fixtures.
The same mixed-radius workload with four workers was noise-level and slightly
slower because most partners cross row-shard boundaries.

The prototype is retained behind the internal `enableRowSymmetry` test and
benchmark switch, but constructors leave it disabled. With continuous Q16.16
centers, exact integer/half-integer eligibility occurs only about once per
32,768 circles, and the CLI defaults to multiple workers. The synthetic win
therefore does not translate into a meaningful production expectation.

## Interaction with Task 10.16

The combined cost path is not merely `Render` followed by a full-image SSD in
eligible staged sessions. A fresh five-sample 256×256 sequential K1 run gave:

| Cost policy | Median | Speedup | Allocations |
| --- | ---: | ---: | ---: |
| production render + full-image AVX2 SSD | 68.876 µs | 1.00× | 0 |
| production render + automatic dirty SSD | 59.201 µs | **1.16×** | 0 |

The exact crossover and larger workload results remain documented in
`task-10.16-incremental-cost-report.md`. Joint and uneconomical batch cases
still fall back to the full-image SIMD SSD kernel.

## Reproduction

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkCPURendererCombinedOptimizations$' \
  -benchmem -benchtime=750ms -count=7 ./internal/fit/renderer

GOMAXPROCS=1 go test -run '^$' \
  -bench 'BenchmarkIncrementalCostBaseline/sequential_K1/(Cost|IncrementalAuto)$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```
