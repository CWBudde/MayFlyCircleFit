# Task 10.7: SIMD SSD cost integration

**Validated:** 2026-08-12  
**Hardware:** AMD Ryzen 5 4600H, 12 logical CPUs  
**Active SSD backend:** AVX2

The production CPU renderer now evaluates rendered images with
`fit.FastMSECost`. Both the white-background and custom-canvas constructors
select it by default, and optimization sessions inherit the selected cost
function from their parent renderer. `FastMSECost` delegates to `FastSSD`, which
validates image dimensions, ignores alpha, dispatches to AVX2, NEON, or the
portable scalar kernel, and divides the resulting SSD by the RGB sample count.

`fit.MSECost` remains available as the readable reference implementation and as
an explicit opt-out through `CPURenderer.SetCostFunc`. `UseFastCost` restores the
production default after a custom cost function has been selected. OpenCL is
unchanged: its renderer computes the equivalent RGB MSE on the device and only
uses the CPU renderer on its documented fallback paths.

## Correctness

Renderer integration tests compare the default result exactly with an
explicitly selected `MSECost`. They cover a single pixel, widths below, at, and
above an eight-pixel AVX2 batch, odd rectangles, a large non-aligned rectangle,
and both renderer constructors. The lower-level cost suite additionally covers
alpha exclusion, empty and mismatched images, independent image origins and
strides, SIMD remainders, large accumulators, and concurrent calls.

The focused integration suite passed with:

```sh
go test ./internal/fit ./internal/fit/renderer
```

## Benchmarks

Five 500 ms samples were collected locally. The table reports the median; the
full-cost cases use one rendering thread to reduce scheduler noise and include
both circle rendering and image evaluation.

| Workload | Original MSE | Fast MSE | Speedup |
| --- | ---: | ---: | ---: |
| Direct 64×64 | 17.90 µs | 1.97 µs | 9.1× |
| Direct 256×256 | 496.00 µs | 27.11 µs | 18.3× |
| Direct 512×512 | 1.18 ms | 112.64 µs | 10.5× |
| Full cost 64×64/K10 | 65.33 µs | 50.50 µs | 1.29× |
| Full cost 256×256/K50 | 3.89 ms | 3.79 ms | 1.03× |
| Full cost 512×512/K100 | 29.78 ms | 28.29 ms | 1.05× |

The direct cost improvement is intentionally larger than the full-cost gain:
as circle count and image size grow, compositing dominates evaluation time.
All measured cases remained allocation-free.

Reproduce the direct and integrated measurements with:

```sh
go test -run '^$' -bench '^BenchmarkFit/Cost' -benchmem \
  -benchtime=500ms -count=5 ./internal/fit
go test -run '^$' -bench '^BenchmarkCPURenderer_CostComparison$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```

## Profile result

A production-default 512×512/K100 cost profile ran for a requested ten-second
benchmark interval:

```sh
go test -run '^$' \
  -bench '^BenchmarkCPURenderer_Cost$/512x512_100circles$' \
  -benchtime=10s -cpuprofile=/tmp/mayfly-task-10.7.prof \
  ./internal/fit/renderer
go tool pprof -top /tmp/mayfly-task-10.7.prof
```

The AVX2 SSD kernel accounted for **0.60%** of flat CPU samples. Pixel
compositing accounted for 84.46% and scanline rasterization for 13.60%, so SSD
is no longer a full-cost bottleneck and is well below the five-percent closure
threshold. Raw profiles remain untracked because their absolute samples and
embedded build identifiers are machine-specific.
