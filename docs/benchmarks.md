# CPU benchmark suite

The canonical, deterministic benchmark suite lives in
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
- **Evaluation parallelism:** `BenchmarkParallelEvaluationScaling` compares the
  default row-sharded serial path against `--parallel-evaluation` at several
  worker counts. The two strategies compete for the same cores, so the
  comparison is against the default rather than against one thread. See
  [parallel-evaluation-report.md](parallel-evaluation-report.md) for measured
  results and the configurations that lose to the default.
- **Polishing strategies:** `BenchmarkPolishCircleBatchStrategy` isolates the
  render cost of one sweep with the optimizer stubbed out, while
  `BenchmarkPolishStrategyQuality` and
  `BenchmarkPolishStrategyQualityAfterBatchFit` run the real optimizer and
  report `final_cost`, `reduction_pct`, and `accepted_sweeps` per run so the
  strategies can be compared at equal wall clock rather than at equal sweeps.
  See [contiguous-window-polish-report.md](contiguous-window-polish-report.md);
  a cheaper sweep is not the same as a better run.
- **Polishing throughput:** `BenchmarkPolishSweepProductionShape` separates
  fixed sweep setup from per-candidate evaluation across pool widths;
  `BenchmarkPolishSweepPoolSetup` measures the memory/setup term; and
  `BenchmarkPolishSelectionByCircleCount`,
  `BenchmarkPolishResidualRegionSelection`, and
  `BenchmarkRegionInfluenceEnergies` isolate active-set selection. See
  [polishing-throughput-report.md](polishing-throughput-report.md) for the
  recorded scaling, break-even, and transient-memory tradeoffs.
- **Polishing budget:** `BenchmarkPolishBudgetShape`,
  `BenchmarkPolishBudgetSweepFalloff`,
  `BenchmarkPolishBudgetShippedConfiguration`, and
  `BenchmarkPolishBudgetProductionShape` measure what a polishing population,
  iteration count, epoch count, and sweep budget buy per second at the
  dimensionality of an active set rather than of the whole vector. They report
  `removed_per_s` beside the quality metrics, and are the measurement the
  polishing defaults are set from. See
  [polishing-budget-report.md](polishing-budget-report.md).

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
the full-cost integration benchmark behind the SIMD cost-integration result;
the canonical `Cost` cases isolate the SSD improvement from circle rendering.

`BenchmarkCompositeOpaqueSpan` isolates scalar and automatically dispatched
opaque-span compositing. `BenchmarkCPURendererOpaqueSpan` compares the former
per-pixel loop with the production horizontal-span renderer at 512×512/K100;
it is the integration benchmark behind the span-compositing result.

`BenchmarkCircleSpanGeometry` compares the `float64` oracle, scalar `float32`,
runtime-selected float32 SIMD, and Q16.16 span-edge searches across small,
large, clipped, and row-sharded circles. `BenchmarkCPURendererGeometry`
compares those modes in the complete one-thread 512×512/K100 renderer. On
AMD64, `BenchmarkCircleSpanFloat32AVX2Direct` isolates the AVX2 per-row kernel
crossover, while `BenchmarkCircleSpanQ16AVX2Direct` compares scalar monotonic
Q16.16 with its exact eight-lane AVX2 prototype. The latter remains a benchmark
backend because widened integer multiplies make it slower on the validated
AMD64 host. These are the geometry and integration benchmarks behind the fixed-point
result.

`BenchmarkCPURendererCombinedOptimizations` stacks the renderer components and
compares the old float64 per-pixel scanline path, span compositing, production
Q16.16 geometry, and the exact-but-experimental paired-row prototype. Separate
fractional, half-pixel, R5, R25, and four-worker fixtures make symmetry
eligibility, span-length crossover, and row-shard effects visible.
[`rejected-optimizations.md`](rejected-optimizations.md) records why symmetry is
disabled; [`cpu-performance-history.md`](cpu-performance-history.md) records the
combined span-plus-Q16.16 result.

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

On AMD64 the runtime-selected kernel is AVX2, SSE2, or scalar. Prefix the same
command with `CIRCLEFIT_SIMD_TIER=sse2` or `CIRCLEFIT_SIMD_TIER=scalar` to measure a
narrower tier; the variable pins every kernel in the process, and an unreachable
tier fails loudly rather than falling back. `GODEBUG=cpu.all=off` does not reach
the scalar kernel on AMD64, because `golang.org/x/sys/cpu` marks sse2 as
required there.

Measuring a fallback tier on a machine that has the wider one tells you what
that instruction set does on *this* microarchitecture, which is not the same
question as what a machine shipping without AVX2 does. Where the difference
matters, say which host produced the number.

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

Run the combined renderer and symmetry-selection benchmark with:

```sh
go test -run '^$' -bench '^BenchmarkCPURendererCombinedOptimizations$' \
  -benchmem -benchtime=750ms -count=7 ./internal/fit/renderer
```

Run the polishing pool and selection benchmarks with:

```sh
go test -run '^$' \
  -bench '^(BenchmarkPolishSweepProductionShape|BenchmarkPolishSweepPoolSetup|BenchmarkPolishSelectionByCircleCount|BenchmarkPolishResidualRegionSelection|BenchmarkRegionInfluenceEnergies)$' \
  -benchmem -benchtime=1x -count=3 -timeout=120m ./internal/fit/renderer
```

These cases are intentionally expensive. Use the per-candidate/per-circle
metrics to compare revisions, and keep image size, circle count, pool width,
`GOMAXPROCS`, and allocation totals with any published result.

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

## Benchmarking across history

Comparing a revision against one from before a package move needs more than a
checkout, because a test file in the current package cannot be replayed into an
older tree. The harness for that lives in `scripts/profiling/benchmarks/` as
`.txt` **templates** rather than as committed test files: copy a template into a
detached worktree at the target commit, run it there, and compare with
`benchstat`. This is how the pre-optimization renderer baseline is still
measurable even though the renderer moved from `internal/fit` to
`internal/fit/renderer`.

Whatever the vintage, a timed benchmark must build its renderer and parameter
vector *before* the timer starts, reuse the renderer across iterations, and
report allocations. Raw benchmark and profile output stays untracked because
absolute profiles are machine- and build-specific; the templates, commit hashes,
and exact commands are what make an experiment reproducible. Worked examples
with commits and results are in
[`cpu-performance-history.md`](cpu-performance-history.md).

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
