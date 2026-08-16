# Task 10.9: SIMD test matrix

**Validated:** 2026-08-12  
**AMD64 host:** Linux, AMD Ryzen 5 4600H, 12 logical CPUs  
**ARM64 host:** macOS 26.6.1, Apple M5 MacBook Air, 10 logical CPUs

The SSD matrix now exercises native SIMD and forced scalar dispatch on both
architectures. Every native SIMD run requires the expected backend through
`MAYFLY_REQUIRE_SSD_BACKEND`, so a silent scalar fallback is a failure rather
than a skipped SIMD test.

| Architecture | Feature state | Required backend | Result |
| --- | --- | --- | --- |
| AMD64 | AVX2 enabled | AVX2 | Passed on Ryzen 5 4600H |
| AMD64 | `GODEBUG=cpu.avx2=off` | Scalar | Passed on Ryzen 5 4600H |
| ARM64 | ASIMD enabled | NEON | Passed on Apple M5 |
| ARM64 | `GODEBUG=cpu.all=off` | Scalar | Passed on Apple M5 |

The ARM64 fallback has a subprocess test because Go consumes CPU feature
overrides before package initialization. Native CI also runs the complete
`internal/fit` suite a second time with `cpu.all=off` on each hardware runner.

**Superseded on AMD64.** Since the SSE2 tier landed, `GODEBUG=cpu.avx2=off` and
`GODEBUG=cpu.all=off` select SSE2 rather than scalar on AMD64, because
`x/sys/cpu` marks sse2 as required on that architecture and never clears it. The
AMD64 forced-scalar row is now `MAYFLY_DISABLE_SIMD=1`. See
`docs/task-10.17-sse2-report.md`.

## Exactness

NEON matched the scalar integer total exactly for:

- widths below, at, and above its four-pixel batch boundary;
- odd and thin images, padded strides, and alpha-only differences;
- concurrent evaluation and randomized images through 512×512;
- 1024×1024 and 2048×2048 images; and
- a maximum-difference 512×512 total above the 32-bit range.

The two large-image checks reported a relative difference of zero on the M5.
AVX2 passes the corresponding eight-pixel boundaries, large accumulator, and
randomized scalar-equivalence suite on AMD64. Both forced scalar runs passed
their backend and result assertions.

The MacBook did not have Go installed. The test executable was cross-compiled
locally with `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go test -c`, copied to a
unique remote `/tmp` directory, checksum-verified, and executed natively. This
validates the same pure-Go-toolchain artifact produced by the cross-build gate.

## Native performance comparison

Five 500 ms samples were collected on each machine. These representative
medians show performance within each host; absolute AVX2-versus-NEON numbers
must not be interpreted as an ISA comparison because the CPUs and memory
systems differ.

| Host | Size | Scalar | SIMD | Speedup |
| --- | ---: | ---: | ---: | ---: |
| Ryzen 5 4600H | 256×256 | 416.6 Mpixels/s | 2,501 Mpixels/s AVX2 | 6.0× |
| Ryzen 5 4600H | 512×512 | 405.5 Mpixels/s | 2,419 Mpixels/s AVX2 | 6.0× |
| Apple M5 | 256×256 | 1,330 Mpixels/s | 6,906 Mpixels/s NEON | 5.2× |
| Apple M5 | 512×512 | 1,316 Mpixels/s | 6,891 Mpixels/s NEON | 5.2× |

The complete 64×64 through 1024×1024 matrix and memory-scaling analysis are in
the [Task 10.10 performance report](task-10.10-simd-performance-report.md).
