# CPU Rendering Threads

The CPU renderer supports optional scanline sharding. Each worker owns
a disjoint horizontal band and composites all circles in parameter order. No
two workers write the same pixel, so parallel output is pixel-identical to the
single-threaded renderer and needs no per-pixel locking.

## Configuration

Local runs accept `--threads`:

```sh
./circlefit run --ref assets/test.png --threads 4
```

The default is `runtime.GOMAXPROCS(0)`. The effective worker count is capped at
both `GOMAXPROCS` and the image height, preventing oversubscription and empty
row shards. Use `--threads 1` to disable sharding. Server API jobs can set the
same value with the optional JSON field `"threads"`; omitted values use the
same `GOMAXPROCS` default. OpenCL rendering ignores this CPU-only setting.

The configured count is inherited by joint, sequential, and batch CPU renderer
sessions, including sessions based on a custom canvas and checkpoint resumes.
As before, one `CPURenderer` instance must not service simultaneous `Render` or
`Cost` calls because it deliberately reuses one canvas; independent instances
can run concurrently. The scanline workers inside one call are race-free.

## Measurements

Measurements were taken on 2026-08-12 with Go 1.26.0 on Linux/amd64 and an AMD
Ryzen 5 4600H (6 cores, 12 logical CPUs). The benchmark uses deterministic
circle parameters and reuses the render buffer. Run it with:

```sh
go test -run '^$' \
  -bench '^BenchmarkCPURendererThreadScaling$' \
  -benchmem -benchtime=2s ./internal/fit/renderer
```

Representative results:

| Workload | 1 thread | 2 threads | 4 threads | 12 threads | Best vs 1 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 32×32, 4 circles | 3.81 µs | 7.85 µs | 12.46 µs | 18.67 µs | 1 thread |
| 128×128, 20 circles | 372 µs | 302 µs | 281 µs | 219 µs | 1.70× |
| 512×512, 100 circles | 26.0 ms | 14.2 ms | 9.23 ms | 5.90 ms | 4.40× |

CPU profiles were also captured for the 512×512 workload with one and 12
threads using `-cpuprofile`. The benchmark rate improved from 27.9 ms/render to
6.24 ms/render in that profiling run; profiling overhead and scheduler variance
make the non-profiled table the comparison baseline.

## When threading helps

- Prefer the default for larger images and circle sets, where alpha compositing
  dominates the goroutine and barrier overhead.
- Benchmark representative inputs before fixing a deployment-specific value;
  memory bandwidth and physical-core count limit scaling before logical CPU
  count on many systems.
- Use `--threads 1` for tiny images or very small circle sets. The 32×32 case
  above was 4.9× slower at 12 threads because synchronization dominated useful
  work.
- When running several independent jobs concurrently, divide the available CPU
  budget between jobs explicitly. The per-render cap prevents one renderer from
  exceeding `GOMAXPROCS`, but it cannot account for other simultaneous jobs.

Correctness is covered by pixel-exact single-versus-multi-thread tests for odd
dimensions, short images, custom canvases, repeat rendering, and session
inheritance. Run these under the race detector with:

```sh
go test -race -run 'TestCPURenderer(Parallel|Sessions)' ./internal/fit/renderer
```
