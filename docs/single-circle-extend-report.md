# Single-circle extend fixed-cost report

Measured 2026-08-22 for PLAN Task 15.9. This report separates the work paid
once by a `+1` batch continuation from the work that scales with the optimizer
iteration budget, records the chosen optimization, and preserves the commands
needed to repeat the measurements.

## Outcome

The checkpoint JSON was not the dominant term. At 2,000 circles it took about
9--10 ms to validate, serialize, sync, and replace; full-vector validation was
below 1 ms and the fixed two-entry trace write was below 0.3 ms. The dominant
work was reconstructing and then repeatedly replaying the inherited image:

- batch setup rendered the complete prefix once for its cost and again for its
  retained canvas;
- result construction rendered the complete vector again even though the
  staged accumulator already held the exact accepted pixels;
- final artifact persistence rendered it once more before encoding `best.png`
  and `diff.png`;
- a parallel suffix pool copied the same immutable 512x512 background once per
  evaluation slot in addition to allocating each slot's required mutable
  canvas.

The extension path now loads the completed parent's `best.png`, verifies its
exact `FastMSECost` against the checkpoint, and starts the suffix sessions from
that retained canvas. A missing, unreadable, dimension-mismatched, or
cost-mismatched artifact falls back to replaying `BestParams`. The pipeline
returns the accumulator's final canvas directly, final checkpoint persistence
reuses that image, and the best/diff PNG files encode concurrently. CPU suffix
sessions share the retained background as immutable storage while keeping
their mutable render buffers isolated.

The JSON checkpoint remains the durable, lossless source of parameters. No
binary or delta checkpoint was introduced because serialization was roughly
two orders of magnitude smaller than the original fixed wall-clock term and
was not the term to optimize.

Two small adjacent costs were removed on the same hot path: decoded images now
use `draw.Draw` instead of an interface-based `Set` loop, and diff-image output
uses `SetNRGBA`, reducing the 512x512 diff construction from about 262,000
allocations to none per pixel.

## Component attribution

**Host:** AMD Ryzen 5 4600H, 12 logical CPUs, Linux 7.0.0-29-generic,
Go 1.26.0, `GOMAXPROCS=12`, AVX2.

**Portable workload:** deterministic 512x512 reference and parameter vector,
population 30 and one render thread. Component sub-benchmarks use one suffix
session to isolate each term; the Mayfly table below uses the production
evaluation width of 12. Circle radii use a production-shaped mixture with many
small circles and a long large-radius tail.

**Method:** `-benchtime=1x -count=3`; medians. Absolute times were noisy because
an independently started server process was running a 12-worker polishing
campaign on the same host. The component hierarchy and allocation counts are
the useful comparison; these samples are not idle-host release claims.

| Circles | Validation | Cached append pipeline | Load/verify parent PNG | Checkpoint JSON | Fixed trace | Final PNGs | Avoided prefix replay |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 500 | 0.059 ms | 67.3 ms | 27.2 ms | 1.12 ms | 0.107 ms | 74.0 ms | 49.2 ms |
| 2,000 | 0.255 ms | 45.4 ms | 6.91 ms | 9.99 ms | 0.189 ms | 120 ms | 161 ms |
| 3,000 | 0.647 ms | 80.1 ms | 11.9 ms | 17.0 ms | 0.148 ms | 58.3 ms | 277 ms |

`Avoided prefix replay` is diagnostic and is not added to the current fixed
total. It is the old two-render prefix session that the verified artifact
replaces, and its growth makes the old intercept's circle-count dependence
visible. The remaining checkpoint term grows from 1.1 to 17 ms, but is still
small beside even one avoided replay. PNG timing depends on image entropy and
filesystem scheduling rather than circle count.

The production-width Mayfly sub-benchmark runs one fixed append and one
50-iteration append at every vector size, then reports their difference per
iteration. A single post-change sample reported:

| Circles | Renderer fixed term | Net time / Mayfly iteration |
| ---: | ---: | ---: |
| 500 | 167 ms | 18.7 ms |
| 2,000 | 156 ms | 21.2 ms |
| 3,000 | 146 ms | 21.4 ms |

The fixed renderer term is now flat rather than proportional to the inherited
vector. One optimizer iteration is likewise independent of the prefix size:
candidate sessions render only the new circle over the retained canvas.

Before the change, a corrected one-stage sample at 2,000 circles measured
445 ms for the append pipeline and 198 ms for final artifacts, versus 9 ms for
checkpoint JSON, 0.7 ms for validation, and 0.1 ms for fixed trace I/O. The
post-change component sum at 2,000 circles is about 0.29 s even with a
12-session pool, a reduction of roughly 55% in the measured fixed work.

## End-to-end table and intercept

The opt-in production benchmark used checkpoint
`0425c7bb-4296-4664-88c4-0c7368d9df0c`: 2,000 circles against the committed
512x512 `example/MayFly-512.png`, population 30, one render thread, evaluation
width 12, one optimizer epoch, trace and final persistence enabled. Optimizer
early stopping was disabled so `iters` remained the independent variable.

| `iters` | wall clock |
| ---: | ---: |
| 50 | 2.499 s |
| 500 | 10.666 s |
| 1,500 | 26.670 s |

The least-squares fit is **1.963 s fixed + 0.01656 s/iteration**. This absolute
intercept is deliberately not presented as an idle-host before/after result:
the concurrently running polishing campaign kept system load around 19--22
while this table ran, and both the slope and intercept are correspondingly
higher than Task 15.9's 2026-08-20 observation. The isolated component
measurement above is the evidence for the fixed-cost reduction. Repeat this
table on an idle host before using its absolute numbers for capacity planning.

## Correctness

The optimization does not trust a PNG merely because it exists. Its decoded
pixels must have the reference dimensions and reproduce the checkpoint cost
exactly; otherwise the old parameter replay runs. Tests compare cached and
replayed append parameters, cost, and pixels, assert independent mutable pool
canvases, and assert final artifact persistence does not rerender a supplied
result image.

Checkpoint JSON round-trip is separately pinned: after save/load, a fresh
renderer evaluates the restored `BestParams` to the exact parent cost. This is
float equality, not a tolerance check.

## Reproducing

```sh
go test -run '^$' \
  -bench '^BenchmarkSingleCircleExtendTerms$' \
  -benchmem -benchtime=1x -count=3 ./internal/server

go test -run '^$' \
  -bench '^BenchmarkSingleCircleExtendWall$' \
  -benchmem -benchtime=1x -count=1 -timeout=10m ./internal/server

MAYFLY_EXTEND_CHECKPOINT=/path/to/jobs/UUID/checkpoint.json \
go test -run '^$' \
  -bench '^BenchmarkSingleCircleExtendProductionCheckpoint$' \
  -benchmem -benchtime=1x -count=1 -timeout=10m ./internal/server
```

Record load average, CPU power state, and competing jobs with every rerun. The
production benchmark reads its source checkpoint and parent artifact but
writes all continuation outputs to a temporary store.
