# CPU benchmark suite

Task 9.8 defines a canonical, deterministic benchmark suite in
`internal/fit/bench_test.go`. Its stable `BenchmarkFit` name lets local and CI
runs compare the same cases across revisions.

## Workloads

The suite covers three layers:

- **Render:** one-thread CPU rendering at 64×64/K=4, 128×128/K=20,
  256×256/K=50, and 512×512/K=100. One thread isolates renderer changes
  from scheduler and machine-core-count differences; use
  `BenchmarkCPURendererThreadScaling` for parallel scaling.
- **Cost:** `MSECost` and the active `FastMSECost` implementation at 64×64,
  256×256, and 512×512. These cases report allocations and processed bytes.
- **Pipeline:** complete joint, sequential, and batch orchestration at 64×64,
  using the real Mayfly optimizer with bounded iteration/population counts and
  a fixed seed.

`BenchmarkFastSSD_Comparison` is the architecture-level SIMD suite. It compares
the portable scalar kernel with the runtime-selected kernel at 64×64, 128×128,
256×256, 512×512, and 1024×1024, reports Mpixels/s and allocations, and retains
the result to prevent dead-code elimination.

References, candidate images, circle parameters, optimizer seeds, and worker
counts are fixed. Benchmark setup is excluded from the timed region, and each
case reports allocations.

`BenchmarkCPURenderer_CostComparison` complements the canonical suite with
complete render-plus-cost measurements at 64×64/K10, 256×256/K50, and
512×512/K100. It explicitly selects `MSECost` for the baseline and compares it
with the production `FastMSECost` default using one rendering thread. This is
the full-cost integration benchmark used by Task 10.7; the canonical `Cost`
cases isolate the SSD improvement from circle rendering.

`BenchmarkCompositeOpaqueSpan` isolates scalar and automatically dispatched
opaque-span compositing. `BenchmarkCPURendererOpaqueSpan` compares the former
per-pixel loop with the production horizontal-span renderer at 512×512/K100;
it is the integration benchmark used by Task 10.12.

`BenchmarkCircleSpanGeometry` compares the `float64` oracle, scalar `float32`,
runtime-selected float32 SIMD, and Q16.16 span-edge searches across small,
large, clipped, and row-sharded circles. `BenchmarkCPURendererGeometry`
compares those modes in the complete one-thread 512×512/K100 renderer. On
AMD64, `BenchmarkCircleSpanFloat32AVX2Direct` isolates the AVX2 per-row kernel
crossover. These are the Task 10.13 geometry and integration benchmarks.

## Running benchmarks

Run six automatically calibrated samples of the canonical suite:

```sh
just benchmark
```

For a quick correctness and runtime check, run every case exactly once:

```sh
go test -run '^$' -bench '^BenchmarkFit$' -benchmem -benchtime=1x ./internal/fit
```

Run the full CPU renderer cost comparison with:

```sh
go test -run '^$' -bench '^BenchmarkCPURenderer_CostComparison$' \
  -benchmem ./internal/fit/renderer
```

Run the SIMD kernel matrix with repeated samples:

```sh
go test -run '^$' -bench '^BenchmarkFastSSD_Comparison$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit
```

Run the opaque-span microbenchmark and full renderer comparison with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkCompositeOpaqueSpan|BenchmarkCPURendererOpaqueSpan)$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```

Run the fixed-point geometry and full renderer comparison with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkCircleSpanGeometry|BenchmarkCPURendererGeometry)$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```

Save two runs made under the same machine, power, thermal, Go-version, and
`GOMAXPROCS` conditions, then compare them with the pinned `benchstat` tool:

```sh
just benchmark > /tmp/mayfly-before.txt
# build or check out the candidate revision
just benchmark > /tmp/mayfly-after.txt
just benchmark-compare /tmp/mayfly-before.txt /tmp/mayfly-after.txt
```

Do not compare absolute timings from different machines. Prefer at least six
samples, close background applications, and record CPU, OS, Go version, and
power settings with any published result. Allocation-count changes are usually
more portable than elapsed-time changes.

## CI regression reporting

The CI benchmark job runs the base and candidate revisions consecutively on
the same GitHub Actions runner with six short samples. It publishes the
`benchstat` table in the job summary and uploads both raw files plus the
comparison. If the base revision predates the canonical suite, CI records that
fact and starts tracking with the candidate result.

Performance comparisons are report-only: statistically significant timing
changes do not fail CI because shared-runner frequency and scheduling noise can
produce false gates. A benchmark that fails to compile or execute still fails
the job. Investigate regressions using longer local runs and profiling before
changing production code.
