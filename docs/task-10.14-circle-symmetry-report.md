# Task 10.14: circle symmetry report

## Outcome

The exact prototype is complete and measurably useful in its best case, but it
is not enabled by the CPU renderer constructors. On the validated AMD64 host,
fully eligible circles rendered 5.7-17% faster with one worker after pairing
both the geometry search and scalar opaque-span compositing. Four-worker
rendering did not improve, and arbitrary optimizer centers are almost never
eligible for exact mirroring.

The implementation therefore remains behind the internal
`enableRowSymmetry` benchmark/test switch. It is available for further CPU
experiments without adding a production policy that rarely fires.

## Exactness restriction

Pixels are sampled at integer row coordinates. Two rows have the same circle
span only when their vertical distances from the circle center are equal:

```text
abs(y1 - centerY) == abs(y2 - centerY)
y1 + y2 == 2 * centerY
```

Because `y1` and `y2` are integers, `2*centerY` must be an integer. In Q16.16,
only integer and half-integer Y centers satisfy that condition. A uniformly
distributed Q16.16 fractional part has two eligible values out of 65,536, or
about one in 32,768. Rounding other centers to a half pixel would alter circle
coverage and is not an optimization of the same renderer.

## Prototype design

The first prototype reused a span for eligible partners but still visited all
rows and skipped the already-rendered half. Task 10.14 replaces that with a
two-ended loop:

- matching top and bottom rows consume one span calculation and one iteration;
- an unmatched edge caused by image clipping or a worker shard is rendered
  normally and only that edge advances;
- a center row is rendered exactly once;
- paired dirty spans are both recorded for exact incremental SSD; and
- each worker writes only rows in its own shard.

On non-ARM64 scalar builds, the paired opaque-span compositor also processes
both rows in one loop. It reuses foreground/blend constants and exposes two
independent pixel streams while retaining the exact arithmetic of two ordinary
span calls. ARM64 keeps the existing runtime NEON crossover by dispatching its
two rows through the measured span backend.

## Correctness

Tests compare paired and ordinary Q16.16 rendering byte for byte across:

- integer, half-integer, arbitrary fractional, and negative/clipped centers;
- clipped and overlapping circles;
- opaque white and translucent custom canvases;
- one and four rendering workers;
- standalone and staged renderer sessions; and
- forced incremental dirty-region cost versus full-image cost.

The paired opaque-span primitive is also compared directly with two ordinary
span calls at sizes including the 256-pixel ARM64 NEON crossover boundary; the
ARM64 renderer test package is cross-compiled in validation.

## AMD64 measurements

Host: AMD Ryzen 5 4600H, Linux/amd64, Go 1.26.0. Each result is the median of
eight samples at `-benchtime=750ms`; fixtures are deterministic 512×512/K100
renders. Single-worker runs use `GOMAXPROCS=1` and allocate zero bytes.

| Fully eligible fixture | Ordinary Q16.16 | Paired rows | Speedup |
| --- | ---: | ---: | ---: |
| mixed radii (mean about R86), 1 worker | 8.044 ms | 7.607 ms | **1.06×** |
| R5, 1 worker | 246.9 µs | 212.9 µs | **1.16×** |
| R25, 1 worker | 1.169 ms | 996.5 µs | **1.17×** |
| mixed radii, 4 workers | 3.164 ms | 3.186 ms | 0.99× |

The paired scalar compositor is why short and medium spans improve more than
the initial span-search-only experiment. For long spans, pixel compositing
still dominates and the geometry saving is diluted.

## Production decision

The result proves that symmetry has a useful synthetic best case, but not that
it improves normal optimization:

1. Continuous Y parameters almost never land on an exact Q16.16 integer or
   half-integer center.
2. The CLI defaults to multiple rendering workers. Most mirrored partners then
   lie in another row shard and cannot be paired without violating ownership or
   adding per-circle synchronization.
3. The measured four-worker result is noise-level and slightly negative.

Changing renderer geometry or the parallel ownership model for this narrow
case is not justified. The exact prototype and benchmark stay in-tree so the
decision can be revisited for discrete-coordinate workloads or different CPUs.

## Reproduction

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkCPURendererCombinedOptimizations/(half_pixel_centers|half_pixel_centers_R5|half_pixel_centers_R25)$/(production_span_q16.16|experimental_with_symmetry)$' \
  -benchmem -benchtime=750ms -count=8 ./internal/fit/renderer

GOMAXPROCS=4 go test -run '^$' \
  -bench '^BenchmarkCPURendererCombinedOptimizations/half_pixel_centers_threads4/(production_span_q16.16|experimental_with_symmetry)$' \
  -benchmem -benchtime=750ms -count=8 ./internal/fit/renderer
```
