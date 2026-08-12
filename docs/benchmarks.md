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

References, candidate images, circle parameters, optimizer seeds, and worker
counts are fixed. Benchmark setup is excluded from the timed region, and each
case reports allocations.

## Running benchmarks

Run six automatically calibrated samples of the canonical suite:

```sh
just benchmark
```

For a quick correctness and runtime check, run every case exactly once:

```sh
go test -run '^$' -bench '^BenchmarkFit$' -benchmem -benchtime=1x ./internal/fit
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
