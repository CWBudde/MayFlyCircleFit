# Task 10.10: SIMD performance validation

**Validated:** 2026-08-12  
**Samples:** five per case, 500 ms benchmark time  
**Allocation result:** 0 B/op, 0 allocs/op for every kernel case

## Kernel throughput

The canonical SSD comparison covers all planned sizes and assigns every result
to a package-level sink. Times and throughput below are medians.

### Linux/AMD64 — Ryzen 5 4600H

| Size | Scalar time | Scalar throughput | AVX2 time | AVX2 throughput | Speedup |
| --- | ---: | ---: | ---: | ---: | ---: |
| 64×64 | 10.41 µs | 393.7 Mpixels/s | 1.66 µs | 2,475 Mpixels/s | 6.3× |
| 128×128 | 39.88 µs | 410.8 Mpixels/s | 6.39 µs | 2,562 Mpixels/s | 6.2× |
| 256×256 | 157.33 µs | 416.6 Mpixels/s | 26.20 µs | 2,501 Mpixels/s | 6.0× |
| 512×512 | 646.44 µs | 405.5 Mpixels/s | 108.39 µs | 2,419 Mpixels/s | 6.0× |
| 1024×1024 | 2.73 ms | 384.2 Mpixels/s | 791.46 µs | 1,325 Mpixels/s | 3.4× |

The historical 256×256 scalar baseline was 316 Mpixels/s. The current scalar
kernel is 1.32× faster, and AVX2 exceeds the original 1.2–2.0 Gpixels/s target
through 512×512. At 1024×1024, AVX2 throughput becomes noisy and falls by about
45%, indicating a cache/memory boundary rather than fixed dispatch overhead.

### macOS/ARM64 — Apple M5

| Size | Scalar time | Scalar throughput | NEON time | NEON throughput | Speedup |
| --- | ---: | ---: | ---: | ---: | ---: |
| 64×64 | 3.07 µs | 1,336 Mpixels/s | 589 ns | 6,950 Mpixels/s | 5.2× |
| 128×128 | 12.31 µs | 1,331 Mpixels/s | 2.37 µs | 6,907 Mpixels/s | 5.2× |
| 256×256 | 49.27 µs | 1,330 Mpixels/s | 9.49 µs | 6,906 Mpixels/s | 5.2× |
| 512×512 | 199.20 µs | 1,316 Mpixels/s | 38.04 µs | 6,891 Mpixels/s | 5.2× |
| 1024×1024 | 798.41 µs | 1,313 Mpixels/s | 153.06 µs | 6,851 Mpixels/s | 5.2× |

NEON exceeds both the 3–4× speedup and 0.9–1.2 Gpixels/s research targets. Its
throughput changes by less than 1.5% across the full matrix, so no large-image
cache cliff is visible on this M5 workload.

## Memory behavior

Both kernels scan two interleaved NRGBA buffers sequentially, reading eight
bytes per pixel with no output stores. The combined input working set grows
from 32 KiB at 64×64 to 8 MiB at 1024×1024. The Ryzen throughput drop at the
8 MiB case and the M5's flat curve characterize the relevant cache behavior.

Direct hardware cache-miss counters were not available: Linux has
`perf_event_paranoid=4`, and the MacBook has no installed Instruments developer
template. No system security setting was weakened for the measurement. The
report therefore treats the throughput curve as cache evidence and does not
claim measured miss counts.

All kernel cases report zero timed allocations, confirming that the Plan 9
assembly dispatch has no cgo or GC pressure.

## Production renderer profile and follow-up improvement

A ten-second, one-thread 512×512/K100 production-cost profile on the M5 found:

| Function | Pre-change flat CPU | Post-change flat CPU |
| --- | ---: | ---: |
| `compositePixel` | 76.60% | 72.56% |
| `renderCircleScanlineRows` | 20.79% | 22.76% |
| `ssdNEON` | Below sample threshold | 0.90% |

The profile showed that further SSD work cannot materially improve the full
renderer. It also exposed redundant general alpha arithmetic on the default
opaque canvas. An exact opaque-destination fast path now skips alpha
normalization, output-alpha division, and the unchanged alpha store while
retaining the original general path for translucent custom canvases.

Five matching M5 samples improved the full production-default workload from a
7.862 ms median to 6.898 ms, a **12.3% time reduction (1.14×)**. Both versions
reported zero timed allocations. The focused opaque/general equivalence test
protects the shortcut, and the existing AMD64 renderer matrix remains
pixel-exact.

Task 10.12 subsequently integrated opaque horizontal spans and a selectively
dispatched ARM64 NEON kernel. The final controlled M5 renderer benchmark
improved by 1.93×, although exact float64 NEON itself only beats the scalar span
on long inputs. The production cutoff and post-change profile are documented in the
[Task 10.12 report](task-10.12-neon-span-report.md).

During hardware validation, the pre-existing historical-renderer oracle test
was found to differ by one alpha unit on one translucent custom-canvas pixel on
ARM64; the unmodified and optimized binaries produce the same current-renderer
output. This does not affect SSD exactness or the opaque-path result, but it is
tracked as a cross-architecture renderer-rounding limitation rather than being
silently relaxed.

## Implementation and reproduction

The AVX2 and NEON kernels are hand-written Go Plan 9 assembly. The implemented
workflow does not use GoAT or retain a C prototype; runtime dispatch uses
`golang.org/x/sys/cpu`, and all portable builds set `CGO_ENABLED=0`.

```sh
go test -run '^$' -bench '^BenchmarkFastSSD_Comparison$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit

go test -run '^$' \
  -bench '^BenchmarkCPURenderer_CostComparison$/^512x512_100circles$/^FastMSECost$' \
  -benchmem -benchtime=500ms -count=5 ./internal/fit/renderer
```

Raw profiles remain untracked because samples and embedded build identifiers
are machine-specific.
