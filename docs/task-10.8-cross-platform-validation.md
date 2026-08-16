# Task 10.8: Cross-platform SIMD validation

**Implemented:** 2026-08-12  
**Local hardware:** Linux/AMD64, AMD Ryzen 5 4600H  
**Local SSD backend:** AVX2

The CPU build remains a pure-Go-toolchain build. The SIMD kernels use Go Plan 9
assembly, and every portable target is compiled with `CGO_ENABLED=0`; no C
compiler, architecture cross-compiler, or external linker is required.

## Cross-build and source-selection matrix

`just cross-build` validates all six targets below. For each target it first
asks `go list` which Go and assembly files were selected, asserts the expected
SSD implementation, and then builds the complete CLI into a temporary
directory. This makes a misplaced or weakened build constraint fail before the
binary build.

| Target | Selected SSD path | Validation |
| --- | --- | --- |
| Linux/AMD64 | `ssd_amd64.go`, `ssd_dispatch_amd64.go`, `ssd_amd64.s` | Cross-build + native CI |
| Linux/ARM64 | `ssd_arm64.go`, `ssd_dispatch_arm64.go`, `ssd_arm64.s` | Cross-build + native CI |
| macOS/AMD64 | `ssd_amd64.go`, `ssd_dispatch_amd64.go`, `ssd_amd64.s` | Cross-build |
| macOS/ARM64 | `ssd_arm64.go`, `ssd_dispatch_arm64.go`, `ssd_arm64.s` | Cross-build + native CI |
| Windows/AMD64 | `ssd_amd64.go`, `ssd_dispatch_amd64.go`, `ssd_amd64.s` | Cross-build + native CI |
| Linux/386 | `ssd_dispatch_generic.go`; no SSD assembly | Cross-build |

The CI cross-build job invokes the same script once per matrix entry, including
Linux/386. The release set remains the five 64-bit targets; the 386 build is a
portability and scalar-fallback gate, not a published release artifact.

## Native hardware execution

CI has native jobs for the four hardware/OS combinations required by the task:

| Runner | Required backend | What runs |
| --- | --- | --- |
| Linux AMD64 | AVX2 | Complete `internal/fit` tests + SSD comparison benchmark |
| macOS ARM64 | NEON | Complete `internal/fit` tests + SSD comparison benchmark |
| Windows AMD64 | AVX2 | Complete `internal/fit` tests + SSD comparison benchmark |
| Linux ARM64 | NEON | Complete `internal/fit` tests + SSD comparison benchmark |

`MAYFLY_REQUIRE_SIMD_TIER` turns tier detection into a hard assertion for these
jobs. A runner that silently selects scalar therefore fails rather than skipping
the architecture-specific correctness cases. Unlike the
`MAYFLY_REQUIRE_SSD_BACKEND` variable it replaces, it is honored by both
`internal/fit` and `internal/fit/renderer`, so a step that runs the renderer
package asserts the renderer's own dispatch too. The ARM64 dispatch test
also accounts for ASIMD feature detection explicitly and confirms that SAD,
which has no ARM64 assembly kernel, remains on its scalar path.

This development machine directly passed the AVX2-required tests. An Apple M5
MacBook Air also passed the required NEON and forced scalar suites in Task 10.9.
Windows AMD64 and Linux ARM64 remain intentionally validated by their native
GitHub-hosted runners; cross-compilation alone is not reported as runtime
evidence.

## Platform performance characteristics

- AMD64 uses 256-bit AVX2 batches of eight NRGBA pixels and a scalar remainder.
  CPUs without AVX2 select the scalar implementation at process startup.
- ARM64 uses 128-bit NEON batches of four NRGBA pixels and a scalar remainder.
  It selects NEON only when ASIMD is reported by the runtime feature detector.
- Linux/386 and other non-AMD64/non-ARM64 architectures compile only the
  portable scalar dispatcher.
- The Plan 9 assembly kernels are OS-neutral within an architecture, so Linux,
  macOS, and Windows use the same AMD64 kernel and Linux/macOS use the same
  ARM64 kernel. OS differences can still affect scheduling and benchmark noise.

Five local 300 ms samples produced these medians. Throughput is measured by the
low-level SSD kernel; all cases reported zero allocations.

| Workload | Scalar | AVX2 | Speedup | AVX2 throughput |
| --- | ---: | ---: | ---: | ---: |
| 64×64 | 10.55 µs | 1.64 µs | 6.4× | 2,497 Mpixels/s |
| 128×128 | 40.11 µs | 6.59 µs | 6.1× | 2,485 Mpixels/s |
| 256×256 | 166.85 µs | 26.89 µs | 6.2× | 2,438 Mpixels/s |
| 512×512 | 696.83 µs | 106.85 µs | 6.5× | 2,453 Mpixels/s |

Each native CI job records the same scalar-versus-active benchmark at 64×64,
128×128, 256×256, and 512×512. Those results characterize the particular
ephemeral runner and do not gate on timing. Task 10.10 remains responsible for
a stable, comprehensive cross-machine performance comparison.

## Reproduction

```sh
just cross-build

MAYFLY_REQUIRE_SIMD_TIER=avx2 \
  go test -count=1 ./internal/fit ./internal/fit/renderer

go test -run '^$' -bench '^BenchmarkFastSSD_Comparison$' \
  -benchmem -benchtime=300ms -count=5 ./internal/fit
```
