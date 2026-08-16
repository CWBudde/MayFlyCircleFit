# Parallel evaluation performance report

**Baseline date:** 2026-08-16
**Hardware:** AMD Ryzen 5 4600H, 12 logical cores (`GOMAXPROCS=12`)
**Backend:** CPU renderer, AVX2 SSD
**Benchmark:** `BenchmarkParallelEvaluationScaling` (`internal/fit/renderer`)

`--parallel-evaluation` shipped without a measurement. This note supplies one,
and the answer is narrower than the feature's framing suggests: the win is real
on small images and close to absent on large ones.

## Benchmark design

Each sample runs one complete joint optimization -- the real MayFly optimizer,
10 iterations, population 20, fixed seed 4242 -- so the measurement includes the
evaluation pool in the arrangement a real run uses, not a synthetic loop over
`Cost`.

The baseline is the **default configuration**, not a single-threaded renderer.
That is the honest comparison, because the default is what a user gives up by
setting the flag: row-band sharding already spends every core on one render.
The question is therefore not "is concurrency faster than none" but "is one
render per core faster than one render split across cores". The two strategies
compete for the same cores and cannot be combined, which is why each pooled
session renders single-threaded.

`eval=N` rows configure `--parallel-evaluation --evaluation-workers N`.
Five iterations per sample, three samples per configuration; tables report
medians.

## Results

### 128x128, 20 circles

| Configuration | ns/op | Speedup vs default | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| default (serial, 12 render threads) | 220,894,581 | 1.00x | 4,535,275 | 11,038 |
| `eval=2` | 250,855,294 | **0.88x** | 4,275,324 | 3,333 |
| `eval=4` | 149,020,990 | 1.48x | 4,543,259 | 3,351 |
| `eval=8` | 94,394,014 | **2.34x** | 5,078,889 | 3,392 |
| `eval=12` | 98,966,762 | 2.23x | 5,614,588 | 3,432 |

### 512x512, 60 circles

| Configuration | ns/op | Speedup vs default | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| default (serial, 12 render threads) | 2,746,017,136 | 1.00x | 20,862,987 | 11,079 |
| `eval=2` | 4,466,477,752 | **0.61x** | 24,653,473 | 3,372 |
| `eval=4` | 3,042,384,335 | **0.90x** | 28,863,156 | 3,393 |
| `eval=8` | 2,470,318,142 | 1.11x | 37,280,900 | 3,432 |
| `eval=12` | 2,320,550,187 | **1.18x** | 45,699,302 | 3,471 |

## Reading the numbers

**The gain is inversely related to image size.** At 128x128 the flag is worth
2.34x at eight workers. At 512x512 the best configuration reaches 1.18x. This
is the expected consequence of what the two parallelism strategies do: row-band
sharding scales well once a render is large enough to amortize the fan-out, so
at 512x512 the default already uses the cores efficiently and there is little
left to win. At 128x128 a single render is too small to shard profitably --
`effectiveThreadCount` clamps to the image height and the per-band overhead
dominates -- so replacing the fan-out with whole independent renders pays.

**Narrow pools are slower than the default, sometimes much slower.** `eval=2`
costs 12% at 128x128 and 39% at 512x512. Below roughly four workers the flag
gives up all twelve cores' worth of row sharding in exchange for two concurrent
renders, which is a straight loss. `--evaluation-workers` should not be set
below about half of `GOMAXPROCS`.

**Peak is not always full width.** At 128x128, `eval=8` beats `eval=12`
(2.34x vs 2.23x): the last four workers contend for memory bandwidth and the
optimizer's serial phases cannot keep twelve slots busy at population 20. A
pool wider than the population's largest batch cannot help by construction.

**Memory scales with the pool, as documented.** At 512x512 the twelve-worker
pool holds 45.7 MB against the default's 20.9 MB, a 2.19x increase, because
every worker above one carries its own canvas and background copy. At HD
resolution this term grows accordingly; the `GOMAXPROCS` clamp is what keeps it
bounded.

**Allocation count falls, which is not a win.** Every pooled configuration
reports ~3,400 allocs/op against the default's ~11,000. The pool allocates its
sessions once per run instead of per stage, so the count drops while the
bytes rise. Judge this feature by wall-clock and resident bytes, not by
allocation count.

## Recommendation

Enable `--parallel-evaluation` for small references, and set
`--evaluation-workers` to roughly `GOMAXPROCS` or to the population's batch
width, whichever is smaller. For references at 512x512 and above, the default
serial path with full row sharding is within 20% of the best pooled
configuration and uses half the memory; the flag is not worth the reproducibility
caveat there.

These are wall-clock comparisons on one machine. Per the repository convention,
do not compare these absolute timings against those in
`docs/task-9.9-performance-report.md` or any other machine's report; re-run the
benchmark on the target hardware instead.

## Reproducing

```sh
go test -run XXX -bench BenchmarkParallelEvaluationScaling -benchtime 5x -count 3 ./internal/fit/renderer/
```

The benchmark derives its worker sweep from `GOMAXPROCS`, so a machine with a
different core count produces a different set of `eval=N` rows.
