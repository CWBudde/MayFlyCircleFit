# Task 10.5: ARM64 NEON SSD Kernel

**Implemented:** 2026-08-12  
**Implementation:** hand-written Go Plan 9 assembly; no C, cgo, or GoAT dependency  
**Hardware measurements:** Apple M5, completed in Task 10.10

The ARM64 kernel processes four interleaved NRGBA pixels per 128-bit iteration:

- compute unsigned absolute byte differences with NEON;
- mask alpha bytes and widen the remaining differences to 16-bit lanes;
- square all lanes in parallel;
- horizontally widen and reduce each batch into a 64-bit accumulator;
- process widths not divisible by four with an exact scalar tail.

Immediate widened reduction prevents the 32-bit lane overflow that can otherwise
appear on large images. The final integer total is converted to `float64` once,
matching the scalar oracle exactly for practical image sizes.

## Dispatch and validation

`ssd_dispatch_arm64.go` selects NEON only when
`golang.org/x/sys/cpu.ARM64.HasASIMD` is true and otherwise selects the scalar
kernel. Existing benchmarks compare the active backend against the scalar
baseline at 64×64, 128×128, 256×256, and 512×512.

Validation in this development environment includes:

- successful ARM64 assembly and test-binary cross-compilation;
- successful Linux, macOS, and Windows ARM64 builds with `CGO_ENABLED=0`;
- compile-time coverage for exact scalar equivalence, four-pixel batch
  boundaries and remainders, alpha exclusion, padded strides, concurrency, and
  a 512×512 maximum-difference total exceeding 32 bits;
- native amd64 formatting, lint, build, and regression tests.

Native Apple M5 validation now passes the exact NEON matrix and forced scalar
fallback. Five-sample medians show a stable 5.2× speedup and approximately
6.9 Gpixels/s from 64×64 through 1024×1024, exceeding the original 3–4× target.
See `task-10.9-simd-test-matrix.md` and
`task-10.10-simd-performance-report.md` for the complete results. Native Linux
ARM64 remains covered by the CI hardware gate rather than this local run.
