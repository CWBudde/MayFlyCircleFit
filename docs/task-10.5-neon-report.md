# Task 10.5: ARM64 NEON SSD Kernel

**Implemented:** 2026-08-12  
**Implementation:** hand-written Go Plan 9 assembly; no C, cgo, or GoAT dependency  
**Hardware measurements:** pending ARM64 hardware access

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

An ARM64 emulator or physical ARM64 machine was not available, so this report
does not claim the original 3–4× target. Execution and benchmark measurements
on Apple Silicon and Linux ARM64 remain part of Tasks 10.8–10.10.
