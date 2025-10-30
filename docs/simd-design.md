# SIMD Design Document - CPU Rendering Optimization

**Date:** 2025-10-28
**Author:** Research for Phase 10 (SIMD/C Intrinsics)
**Status:** Design Research Complete (Task 10.1)

---

## Executive Summary

### Problem Statement
Baseline profiling (Phase 9.2) identified `compositePixel` as the critical bottleneck, consuming **54-61% of total CPU time** during circle rendering. This function performs Porter-Duff alpha compositing and is called millions of times per optimization evaluation (e.g., ~30 million calls for a 256×256 image with 30 circles).

Current scalar implementation processes one pixel at a time using floating-point arithmetic. SIMD (Single Instruction, Multiple Data) can process 4-8 pixels simultaneously, offering potential 4-8× speedup.

### Recommended Approach
**Plan9 Assembly with GoAT tooling** for the following reasons:
- **Best performance:** 4-6× speedup with no cgo overhead (benchmarks show 33.6 GB/s vs 7.9 GB/s for cgo)
- **Pure Go build:** No C compiler required, standard `go build` workflow
- **Production-proven:** Used by Minio HighwayHash (>10 GB/sec) and Go standard library
- **Tooling support:** GoAT transpiles C intrinsics → Plan9 assembly, mitigating learning curve

### Expected Performance Gain
- **Conservative estimate:** 4-6× speedup for compositing hot path
- **Overall impact:** 2.5-3.5× total rendering speedup (compositing is 54-61% of time)
- **Target throughput:** 2,500-3,500 circles/sec (from current ~1,350 on 256×256 medium workload)

---

## Background & Motivation

### Current Bottleneck Analysis

From `docs/baseline-performance-report.md` (Phase 9.2):

**Medium Workload (256×256, K=30):**
- **Total time:** 66.5 seconds (after Phase 9 optimizations)
- **Compositing time:** ~40 seconds (61.4% of CPU time in `compositePixel`)
- **Throughput:** 1,353 circles/sec
- **Per-pixel operations:** 30 million compositing calls per evaluation

**Hotspot breakdown (256×256 profile):**
```
Function           Flat Time   Flat %   Analysis
compositePixel     82.81s      61.4%    Critical: Alpha compositing inner loop
math.Round         21.47s      15.9%    High: Replaced in Phase 9.6
renderCircle        9.02s       6.7%    Low: Orchestration overhead
```

### Why SIMD is Necessary

**Current scalar operation (per pixel):**
```go
// Load 4 background bytes → convert to float64
bgR := float64(img.Pix[i+0]) * inv255  // 1 load, 1 int→float, 1 multiply
bgG := float64(img.Pix[i+1]) * inv255
bgB := float64(img.Pix[i+2]) * inv255
bgA := float64(img.Pix[i+3]) * inv255

// Porter-Duff blending (12 floating-point ops)
outA := fgA + bgA*(1-fgA)
invOutA := 1.0 / outA
bgBlend := bgA * (1 - fgA)
outR := (fgR + bgR*bgBlend) * invOutA
// ... similar for G, B

// Convert back to uint8
img.Pix[i+0] = uint8(outR*255 + 0.5)  // 1 multiply, 1 float→int, 1 store
```

**Total per pixel:** 4 loads + 8 conversions + 12 FP ops + 4 stores = **28 operations**

**SIMD benefits:**
- **AVX2 (256-bit):** Process 8 pixels (32 bytes) per instruction → 8× throughput
- **NEON (128-bit):** Process 4 pixels (16 bytes) per instruction → 4× throughput
- **Reduced instruction count:** Fewer loop overhead instructions
- **Better CPU pipelining:** Fewer branches, more predictable execution

### Goals for Phase 10

1. **Solid CPU alternative** while GPU work (Phase 11) is in progress
2. **Cross-platform performance:** AVX2 (x86-64) and NEON (ARM64) support
3. **Pure Go build system:** No external dependencies, easy CI/CD
4. **Maintainable:** Use tooling (GoAT) to generate assembly from C prototypes
5. **Validated correctness:** Pixel-exact equivalence with scalar reference

---

## Option 1: cgo + C with Intrinsics (AVX2/NEON)

### How It Works

Write the hot path in C using SIMD intrinsics, compile with GCC/Clang, link via cgo:

```c
// compositePixel_avx2.c
#include <immintrin.h>

void compositePixelBatch_AVX2(uint8_t *img, int stride,
                               const float *fgRGB, float fgA,
                               int x, int y, int count) {
    __m256 vFgR = _mm256_set1_ps(fgRGB[0] * fgA);
    __m256 vFgG = _mm256_set1_ps(fgRGB[1] * fgA);
    __m256 vFgB = _mm256_set1_ps(fgRGB[2] * fgA);
    __m256 vFgA = _mm256_set1_ps(fgA);

    for (int i = 0; i < count; i += 8) {
        // Load 8 pixels (32 bytes RGBA)
        __m256i vBg = _mm256_loadu_si256((__m256i*)(img + i*4));

        // Convert uint8 → float, blend, convert back
        // ... AVX2 intrinsics ...

        // Store 8 pixels
        _mm256_storeu_si256((__m256i*)(img + i*4), vOut);
    }
}
```

```go
// compositePixel_avx2.go
/*
#cgo CFLAGS: -mavx2 -O3
#include "compositePixel_avx2.c"
*/
import "C"

func compositePixelBatchAVX2(img *image.NRGBA, ...) {
    C.compositePixelBatch_AVX2((*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), ...)
}
```

### Portability Analysis

**Cross-compiler support:**
- ✅ **GCC/Clang:** Excellent intrinsics support (`immintrin.h`, `arm_neon.h`)
- ✅ **MSVC:** Windows support with same intrinsics headers
- ✅ **Standard headers:** Intrinsics are part of C/C++ standards

**Build complexity:**
- ❌ **Requires C toolchain at build time:** Users need GCC/Clang installed
- ❌ **Cross-compilation:** Need target C compiler (e.g., `aarch64-linux-gnu-gcc` for ARM64)
- ❌ **Platform-specific flags:** `-mavx2` (x86), `-march=armv8-a+simd` (ARM)
- ⚠️ **Static vs dynamic linking:** Ensure no runtime C library dependencies

### Performance Characteristics

**Theoretical speedup:**
- **Alpha blending benchmarks (2024):** AVX2 achieves **5.7× speedup** over scalar (source: breeswish.org)
- **SSE4.1:** ~3.14× speedup (baseline for comparison)
- **AVX2 vs SSE:** ~2× improvement (256-bit vs 128-bit registers)

**Cgo overhead (critical limitation):**
- **Per-call overhead:** 20-50 nanoseconds (function call, stack switch, GC coordination)
- **Real-world impact:** 2024 benchmarks show cgo SIMD at **7.9 GB/s** vs native SIMD **33.6 GB/s** (4.25× slower!)
- **Mitigation:** Batch pixels (process 64-256 pixels per cgo call), but limits granularity

**Memory allocation issues:**
- **Slice passing:** Go slices must be pinned for cgo (GC coordination overhead)
- **Data copying:** May need to copy pixel data to C-aligned buffers (defeats SIMD gains)

### Maintenance Burden

**Code maintainability:**
- ⚠️ **Moderate:** C code is familiar to most developers
- ✅ **Well-documented:** Intel Intrinsics Guide, ARM NEON intrinsics reference
- ⚠️ **Debugging:** Crossing Go↔C boundary complicates stack traces
- ✅ **Isolated testing:** Can test C code independently with C unit tests

**Complexity:**
- **Two languages:** Go wrapper + C implementation
- **Memory safety:** Unsafe pointer conversions, manual bounds checking
- **ABI compatibility:** Go and C calling conventions must match

### Build Complexity

**Development workflow:**
- ❌ **Requires C compiler:** `gcc`, `clang`, or `msvc` must be in PATH
- ❌ **cgo environment variables:** `CGO_ENABLED=1`, `CC=...`, `CXX=...`
- ❌ **Cross-compilation nightmare:** Need cross-compilers for each target (Linux→Windows, x86→ARM)

**CI/CD impact:**
- ❌ **CI needs C toolchains:** Ubuntu needs `gcc`, `gcc-aarch64-linux-gnu`, `gcc-mingw-w64`
- ❌ **Build time:** C compilation adds 10-30 seconds to build
- ❌ **Distribution:** Static linking increases binary size

**Breaks Go's simplicity:**
- Go philosophy: `go build` should work without external dependencies
- Cgo breaks: `go get`, reproducible builds, fast compilation

### Verdict: ❌ Not Recommended

**Why avoid cgo for this use case:**
1. **Cgo overhead negates SIMD gains** for per-pixel operations (4.25× slower than native)
2. **Build complexity** hurts developer experience (especially cross-compilation)
3. **Distribution issues:** Binary portability, static linking bloat
4. **Better suited for:** Batch operations (full-image cost computation), not inner loops

**When cgo + SIMD makes sense:**
- Very large batches (>10,000 pixels per call)
- Existing C library integration (e.g., wrapping libjpeg-turbo)
- Prototype/validation (easier to write C first, then port)

---

## Option 2: Go Assembly (Plan9 ASM)

### How It Works

Hand-write SIMD instructions in Go's Plan9 assembly syntax, one file per architecture:

```
// compositePixel_amd64.s
//go:build amd64

#include "textflag.h"

// func compositePixelBatchAVX2(img *image.NRGBA, ...)
TEXT ·compositePixelBatchAVX2(SB), NOSPLIT, $0-32
    MOVQ    img+0(FP), DI      // Load img pointer
    MOVL    count+24(FP), CX   // Load pixel count

    // Broadcast foreground color to YMM registers
    VBROADCASTSS fgR+8(FP), Y0
    VBROADCASTSS fgG+12(FP), Y1
    VBROADCASTSS fgB+16(FP), Y2
    VBROADCASTSS fgA+20(FP), Y3

loop:
    // Load 8 background pixels (32 bytes)
    VMOVDQU (DI), Y4

    // Convert uint8 → float32 (use VPMOVZXBD, VCVTDQ2PS)
    VPMOVZXBD Y4, Y5
    VCVTDQ2PS Y5, Y6

    // Porter-Duff blending (AVX2 FP operations)
    // ... register-based computation ...

    // Convert float32 → uint8, store
    VCVTPS2DQ Y7, Y8
    VPACKUSDW Y8, Y8, Y9
    VPACKUSWB Y9, Y9, Y10
    VMOVDQU Y10, (DI)

    ADDQ $32, DI
    SUBL $8, CX
    JG loop

    RET
```

### Performance Characteristics

**Real-world production benchmarks (2024):**
- **Minio HighwayHash:** >10 GB/sec with hand-written Plan9 assembly (AVX2/NEON)
- **Sourcegraph SIMD story:** "Go assembly with direct CPU access was faster than cgo" (no overhead)
- **2024 Go SIMD benchmark:** Native SIMD **33.6 GB/s** vs cgo SIMD **7.9 GB/s** (4.25× faster)

**No cgo overhead:**
- Direct function call (Go calling convention, no stack switch)
- No GC coordination (assembly is part of Go runtime)
- Inlining possible for small functions

**Theoretical speedup:**
- **AVX2 (8 pixels/instruction):** 6-8× over scalar (accounting for conversion overhead)
- **NEON (4 pixels/instruction):** 4-5× over scalar
- **Actual in compositing:** 4-6× expected (conservative, includes memory bandwidth limits)

### Maintenance Complexity

**Learning curve:**
- ❌ **Steep:** Plan9 syntax is non-standard (different from AT&T, Intel, ARM assembly)
- ⚠️ **Quirks:**
  - Operand order: `MOVQ src, dst` (opposite of AT&T's `movq %src, %dst`)
  - Register names: `AX` not `RAX`, `R8` not `%r8`
  - Pseudo-registers: `SP`, `FP`, `SB` for stack, frame, static base
  - Limited immediate support: Some instructions require register-only operands

**Example differences:**
```asm
# AT&T syntax (gas)
movq %rax, %rbx
addq $10, %rcx

# Intel syntax (nasm)
mov rbx, rax
add rcx, 10

# Plan9 syntax (Go)
MOVQ AX, BX
ADDQ $10, CX
```

**Documentation gaps:**
- ✅ **Official guide:** `go.dev/doc/asm` (basic coverage)
- ⚠️ **Limited examples:** Must study Go stdlib (`crypto/aes`, `math/big`) for patterns
- ❌ **No canonical instruction database:** Trial and error for obscure instructions
- ✅ **Community resources:** Alex Sharipov's blog, Go assembly reference

**Error-prone:**
- ❌ **No type safety:** Wrong register size → silent corruption
- ❌ **Stack management:** Manual frame pointer arithmetic
- ❌ **Register clobbers:** Must preserve caller-save registers (BX, BP, R12-R15)
- ⚠️ **ABI compliance:** Must follow Go's calling convention (parameters on stack, not registers)

**Long-term maintenance:**
- ❌ **Architecture-specific:** Separate `.s` file for amd64, arm64, 386, etc.
- ❌ **Hard to review:** Few developers comfortable with assembly
- ✅ **Stable:** Plan9 syntax hasn't changed since Go 1.0 (backward compatibility guarantee)
- ✅ **Debugger support:** Delve can step through assembly, inspect registers

### Separate Files Per Architecture

**File structure:**
```
internal/fit/
├── compositePixel.go           // Go dispatcher (all platforms)
├── compositePixel_amd64.s      // AVX2 implementation (x86-64)
├── compositePixel_arm64.s      // NEON implementation (ARM64)
├── compositePixel_generic.go   // Scalar fallback (386, wasm, etc.)
└── compositePixel_test.go      // Tests (all platforms)
```

**Build tags:**
```go
// compositePixel_amd64.s
//go:build amd64

// compositePixel_arm64.s
//go:build arm64

// compositePixel_generic.go
//go:build !amd64 && !arm64
```

**Shared interface:**
```go
// compositePixel.go (runtime dispatch)
var compositePixelBatch func(img *image.NRGBA, x, y int, r, g, b, a float64, count int)

func init() {
    if cpu.X86.HasAVX2 {
        compositePixelBatch = compositePixelBatchAVX2  // from _amd64.s
    } else if cpu.ARM64.HasASIMD {
        compositePixelBatch = compositePixelBatchNEON  // from _arm64.s
    } else {
        compositePixelBatch = compositePixelBatchScalar  // from _generic.go
    }
}
```

### Tooling Help

**GoAT (Go Assembly Transpiler) - RECOMMENDED**
- **Project:** `github.com/gorse-io/goat`
- **Status:** Actively maintained (2024), successor to c2goasm
- **Features:**
  - Compiles C code with intrinsics → Plan9 assembly
  - Uses Clang/LLVM for optimization before transpilation
  - Supports AVX512, AVX2, SSE, NEON
- **Workflow:**
  1. Write function in C with intrinsics (easy to test/debug)
  2. Run `goat -O3 compositePixel.c` → generates `.s` file
  3. Review and hand-tune generated assembly
  4. Add to Go project with build tags

**avo (Assembly in Go)**
- **Project:** `github.com/mmcloughlin/avo`
- **Concept:** Write Go code that *generates* Plan9 assembly
- **Benefits:** Type-safe register allocation, Go control flow
- **Drawbacks:** Another layer of abstraction, learning curve
- **Best for:** Complex functions with many register allocations

**c2goasm (DEPRECATED)**
- **Status:** Archived December 2021, read-only
- **Reason:** Hasn't been updated in 4+ years, no AVX512 support
- **Replacement:** GoAT inherits c2goasm's ideas with enhancements

### Production Examples

**Minio HighwayHash** (350+ imports, actively maintained 2024)
- **Repository:** `github.com/minio/highwayhash`
- **Files:** `highwayhash_amd64.s`, `highwayhash_arm64.s`, `highwayhash_generic.go`
- **Performance:** >10 GB/sec hashing with hand-written assembly
- **Pattern:** Runtime dispatch based on CPU features, scalar fallback

**Go Standard Library**
- **crypto/aes:** AES-NI instructions for x86-64 (`aes_amd64.s`)
- **crypto/sha256:** SHA extensions (`sha256block_amd64.s`)
- **math/big:** Multi-precision arithmetic with SIMD
- **All use Plan9 assembly for performance-critical operations**

### Verdict: ✅ **Recommended**

**Why Plan9 Assembly is the best choice:**
1. **Best performance:** No cgo overhead, direct SIMD (4-6× expected speedup)
2. **Pure Go build:** Standard `go build`, no C compiler, easy cross-compilation
3. **Production-proven:** Go stdlib and high-performance projects use this approach
4. **Tooling mitigation:** GoAT generates assembly from C prototypes (avoids writing Plan9 by hand)
5. **Complementary to GPU:** Fast CPU path benefits users without GPU, provides baseline

**Upfront cost:**
- Learning Plan9 syntax (mitigated by GoAT transpilation)
- Writing architecture-specific code (required for SIMD regardless of approach)

**Long-term benefits:**
- Pure Go ecosystem (CI/CD, distribution, developer experience)
- Maintainable with good documentation and tooling
- Reusable pattern for future SIMD optimizations

---

## Option 3: Pure Go with `unsafe` + `golang.org/x/sys/cpu`

### Autovectorization Capabilities

**Go compiler status (2024):**
- ❌ **No autovectorization:** Go compiler does NOT generate SIMD instructions from Go code
- ❌ **Confirmed by multiple sources:**
  - Daniel Lemire's blog (2020, still true in 2024): "The Go compiler needs to be smarter"
  - Go SIMD packages document: "The compiler cannot be trusted to autovectorize within the next several years"
  - Proposal golang/go#67520 (May 2024): SIMD intrinsics still just a proposal, not implemented

**Compiler optimization improvements (Go 1.20-1.23):**
- ✅ **Profile-Guided Optimization (PGO):** 2-14% speedup via devirtualization, inlining
- ✅ **Memory optimizations:** 1-3% CPU improvement, ~1% memory reduction
- ✅ **Better code generation:** Register allocation, bounds check elimination
- ❌ **Still no SIMD:** None of these generate vector instructions

**Why Go doesn't autovectorize:**
- **Compiler philosophy:** Prioritizes fast compilation over aggressive optimization
- **No whole-program optimization (LTO):** Each package compiled independently
- **Safety guarantees:** Bounds checks, nil checks prevent many SIMD opportunities
- **Escape analysis:** Conservative, doesn't always stack-allocate arrays

### Runtime Feature Detection

**`golang.org/x/sys/cpu` package:**
```go
import "golang.org/x/sys/cpu"

func init() {
    // x86-64 feature detection
    if cpu.X86.HasAVX2 {
        fmt.Println("AVX2 supported")
    }
    if cpu.X86.HasFMA {
        fmt.Println("FMA supported")
    }

    // ARM64 feature detection
    if cpu.ARM64.HasASIMD {  // NEON
        fmt.Println("NEON supported")
    }
    if cpu.ARM64.HasSVE {  // Scalable Vector Extension
        fmt.Println("SVE supported")
    }
}
```

**GODEBUG override:**
```bash
# Force disable AVX2 for testing fallback
GODEBUG=cpu.avx2=off go run ./cmd/mayflycirclefit
```

**Use for runtime dispatch:**
- Select SIMD vs scalar implementation at startup
- Benchmark different implementations on user's hardware
- Graceful fallback if CPU features unavailable

### Safety Considerations

**Unsafe pointer tricks (don't enable SIMD):**
```go
// This does NOT generate SIMD instructions
func compositePixelUnsafe(img *image.NRGBA, ...) {
    pix := (*[1 << 30]byte)(unsafe.Pointer(&img.Pix[0]))[:len(img.Pix)]

    // Loop unrolling, manual vectorization hints
    for i := 0; i < len(pix); i += 32 {
        // Compiler still generates scalar instructions
        pix[i] = ...
    }
}
```

**What `unsafe` CAN help with:**
- ✅ **Cache locality:** Align data structures to cache line boundaries
- ✅ **Eliminate bounds checks:** Direct pointer arithmetic (dangerous!)
- ✅ **Reduce slice header overhead:** Access backing array directly
- ❌ **Does NOT generate SIMD:** Compiler won't vectorize, even with hints

**Trade-offs:**
- ⚠️ **Memory safety:** Unsafe code can corrupt memory (violates Go's guarantees)
- ⚠️ **GC issues:** Raw pointers confuse garbage collector (must pin carefully)
- ❌ **No performance gain for SIMD:** Without assembly, no vector instructions

### Verdict: ❌ Not Viable for SIMD Acceleration

**Use `golang.org/x/sys/cpu` for:**
- Runtime dispatch to SIMD implementations (from Option 2)
- Benchmarking and testing (force disable features with GODEBUG)
- Documentation (document CPU requirements)

**Do NOT expect:**
- Go compiler to generate SIMD from pure Go code (won't happen in 2024-2025)
- `unsafe` tricks to enable vectorization (requires assembly or cgo)

**Pure Go optimization (already done in Phase 9):**
- ✅ Reciprocal multiplication instead of division
- ✅ Strength reduction (hoisting common subexpressions)
- ✅ Bounds check elimination
- ✅ Integer arithmetic for rounding
- ❌ **No SIMD from these techniques**

---

## Recommended Approach: Hybrid Strategy

### Three-Tier Implementation

**Tier 1: SIMD Assembly (Primary Performance Path)**
- **AVX2 implementation** (`compositePixel_amd64.s`):
  - Process 8 pixels per iteration (256-bit registers)
  - Target: 4-6× speedup over scalar
- **NEON implementation** (`compositePixel_arm64.s`):
  - Process 4 pixels per iteration (128-bit registers)
  - Target: 4-5× speedup over scalar
- **Batch interface:** Process 8-32 pixels per function call (amortize call overhead)

**Tier 2: Scalar Fallback**
- **Generic implementation** (`compositePixel_generic.go`):
  - Current optimized `compositePixel` from Phase 9
  - Used for:
    - Unsupported architectures (386, wasm, riscv64)
    - Remainder pixels after SIMD batch (e.g., 7 pixels when batch size is 8)
    - Fallback if runtime CPU detection fails

**Tier 3: Runtime Dispatch**
- **Feature detection** (`golang.org/x/sys/cpu`):
  - Check CPU capabilities at startup
  - Select best available implementation
  - Function pointer indirection (minimal overhead: 1-2ns)

### Batch Processing Strategy

**Why batching:**
- **Amortize function call overhead:** ~10ns per call (negligible for 8-32 pixels)
- **Register efficiency:** Load/store 8 pixels at once (single AVX2 instruction)
- **Cache efficiency:** Process contiguous memory, better prefetching

**Batch size selection:**
```go
const (
    BatchSizeAVX2 = 8   // 256-bit / 32 bytes per pixel = 8 pixels
    BatchSizeNEON = 4   // 128-bit / 32 bytes per pixel = 4 pixels
)

func (r *CPURenderer) renderCircle(img *image.NRGBA, c Circle) {
    // ... AABB calculation ...

    for y := minY; y < maxY; y++ {
        rowStart := y*img.Stride + minX*4
        rowEnd := y*img.Stride + maxX*4

        // Process full batches with SIMD
        for x := rowStart; x <= rowEnd-BatchSizeAVX2*4; x += BatchSizeAVX2*4 {
            compositePixelBatch(img.Pix, x, c.CR, c.CG, c.CB, c.Opacity, BatchSizeAVX2)
        }

        // Process remainder with scalar
        for x := rowStart + (rowEnd-rowStart)/BatchSizeAVX2*4*BatchSizeAVX2; x < rowEnd; x += 4 {
            compositePixel(img, x/4%width, x/img.Stride, c.CR, c.CG, c.CB, c.Opacity)
        }
    }
}
```

### Function Pointer Dispatch Pattern

**Implementation (inspired by Minio HighwayHash):**
```go
// compositePixel.go (all platforms)
package fit

import "golang.org/x/sys/cpu"

// Function pointer for runtime dispatch
var compositePixelBatch func(pix []byte, offset int, r, g, b, a float64, count int)

func init() {
    // Detect CPU features and select best implementation
    if cpu.X86.HasAVX2 {
        compositePixelBatch = compositePixelBatchAVX2  // from compositePixel_amd64.s
        log.Println("Using AVX2 SIMD for rendering")
    } else if cpu.ARM64.HasASIMD {
        compositePixelBatch = compositePixelBatchNEON  // from compositePixel_arm64.s
        log.Println("Using NEON SIMD for rendering")
    } else {
        compositePixelBatch = compositePixelBatchScalar  // from compositePixel_generic.go
        log.Println("Using scalar fallback for rendering")
    }
}

// Scalar fallback (used for remainder pixels and unsupported platforms)
func compositePixelBatchScalar(pix []byte, offset int, r, g, b, a float64, count int) {
    for i := 0; i < count; i++ {
        idx := offset + i*4
        // Current optimized compositePixel implementation from Phase 9
        // ... (inline the logic here to avoid function call overhead) ...
    }
}
```

**Assembly declarations:**
```go
// compositePixel_amd64.go
//go:build amd64

package fit

// Implemented in compositePixel_amd64.s
func compositePixelBatchAVX2(pix []byte, offset int, r, g, b, a float64, count int)
```

```go
// compositePixel_arm64.go
//go:build arm64

package fit

// Implemented in compositePixel_arm64.s
func compositePixelBatchNEON(pix []byte, offset int, r, g, b, a float64, count int)
```

---

## Runtime Dispatch Strategy

### Feature Detection with `golang.org/x/sys/cpu`

**Installation:**
```bash
go get golang.org/x/sys/cpu
```

**Detection code:**
```go
package fit

import (
    "log/slog"
    "golang.org/x/sys/cpu"
)

type SIMDBackend int

const (
    BackendScalar SIMDBackend = iota
    BackendAVX2
    BackendNEON
)

var activeBackend SIMDBackend

func init() {
    // x86-64: Check AVX2 support
    if cpu.X86.HasAVX2 {
        activeBackend = BackendAVX2
        compositePixelBatch = compositePixelBatchAVX2
        slog.Info("SIMD backend selected", "backend", "AVX2", "width", "256-bit")
        return
    }

    // ARM64: Check ASIMD (NEON) support
    if cpu.ARM64.HasASIMD {
        activeBackend = BackendNEON
        compositePixelBatch = compositePixelBatchNEON
        slog.Info("SIMD backend selected", "backend", "NEON", "width", "128-bit")
        return
    }

    // Fallback to scalar
    activeBackend = BackendScalar
    compositePixelBatch = compositePixelBatchScalar
    slog.Info("SIMD backend selected", "backend", "scalar", "reason", "no SIMD support")
}
```

### AVX2 Detection on amd64

**CPU feature check:**
```go
if cpu.X86.HasAVX2 {
    // AVX2 is available (Intel Haswell 2013+, AMD Excavator 2015+)
}

if cpu.X86.HasFMA {
    // Fused Multiply-Add available (often paired with AVX2)
    // Can optimize: outR = FMA(bgBlend, bgR, fgR)
}
```

**Platform support:**
- ✅ **Intel:** Haswell (2013) and newer (Core i5-4xxx, Xeon E3-12xx v3)
- ✅ **AMD:** Excavator (2015) and newer (Ryzen all generations)
- ⚠️ **Older CPUs:** Fall back to SSE4.1 (2008+) or scalar

**Testing fallback:**
```bash
# Force scalar fallback for testing
GODEBUG=cpu.avx2=off ./bin/mayflycirclefit run --ref test.png
```

### NEON/ASIMD Detection on arm64

**CPU feature check:**
```go
if cpu.ARM64.HasASIMD {
    // Advanced SIMD (NEON) available (mandatory in ARMv8-A)
}

if cpu.ARM64.HasSVE {
    // Scalable Vector Extension (future: variable-width SIMD)
    // Not targeting in Phase 10 (very new, complex)
}
```

**Platform support:**
- ✅ **All ARMv8-A CPUs:** NEON/ASIMD is mandatory (no detection needed, but good practice)
- ✅ **Apple Silicon:** M1/M2/M3 (excellent NEON performance)
- ✅ **AWS Graviton:** Graviton2/3 (server-grade ARM)
- ✅ **Raspberry Pi 4:** Cortex-A72 (consumer ARM)

**Note:** NEON is so ubiquitous on ARM64 that the check is mostly for safety. All ARM64 builds can assume NEON support.

### Scalar Fallback for Unsupported Platforms

**When scalar is used:**
- **Unsupported architectures:** 386 (32-bit x86), wasm, riscv64, mips64
- **Remainder pixels:** After SIMD batch processing (e.g., 7 pixels when batch size is 8)
- **Developer override:** `GODEBUG=cpu.avx2=off` for testing
- **Feature detection failure:** Conservative fallback if CPU detection errors

**Fallback implementation:**
```go
// compositePixel_generic.go
//go:build !amd64 && !arm64

package fit

// Scalar fallback (current Phase 9 optimized version)
func compositePixelBatchScalar(pix []byte, offset int, r, g, b, a float64, count int) {
    inv255 := 1.0 / 255.0

    for i := 0; i < count; i++ {
        idx := offset + i*4

        // Current optimized compositePixel from Phase 9
        bgR := float64(pix[idx+0]) * inv255
        bgG := float64(pix[idx+1]) * inv255
        bgB := float64(pix[idx+2]) * inv255
        bgA := float64(pix[idx+3]) * inv255

        fgR := r * a
        fgG := g * a
        fgB := b * a
        fgA := a

        outA := fgA + bgA*(1-fgA)
        if outA == 0 {
            continue
        }

        invOutA := 1.0 / outA
        bgBlend := bgA * (1 - fgA)

        outR := (fgR + bgR*bgBlend) * invOutA
        outG := (fgG + bgG*bgBlend) * invOutA
        outB := (fgB + bgB*bgBlend) * invOutA

        pix[idx+0] = uint8(outR*255 + 0.5)
        pix[idx+1] = uint8(outG*255 + 0.5)
        pix[idx+2] = uint8(outB*255 + 0.5)
        pix[idx+3] = uint8(outA*255 + 0.5)
    }
}
```

---

## Build Tags and File Layout Strategy

### Recommended File Structure

```
internal/fit/
├── compositePixel.go              # Runtime dispatcher (all platforms)
│   └── init() selects implementation based on CPU features
│
├── compositePixel_amd64.s         # AVX2 assembly (x86-64 only)
│   └── //go:build amd64
│
├── compositePixel_amd64.go        # AVX2 Go declaration
│   └── //go:build amd64
│   └── func compositePixelBatchAVX2(...) // implemented in .s
│
├── compositePixel_arm64.s         # NEON assembly (ARM64 only)
│   └── //go:build arm64
│
├── compositePixel_arm64.go        # NEON Go declaration
│   └── //go:build arm64
│   └── func compositePixelBatchNEON(...) // implemented in .s
│
├── compositePixel_generic.go      # Scalar fallback (all other platforms)
│   └── //go:build !amd64 && !arm64
│   └── func compositePixelBatchScalar(...)
│
└── compositePixel_test.go         # Tests (all platforms)
    └── Test equivalence (SIMD vs scalar)
```

### Build Tag Syntax

**Assembly files (`.s`):**
```asm
// compositePixel_amd64.s
//go:build amd64

#include "textflag.h"

TEXT ·compositePixelBatchAVX2(SB), NOSPLIT, $0-56
    // ... AVX2 assembly ...
    RET
```

**Go files (`.go`):**
```go
// compositePixel_amd64.go
//go:build amd64

package fit

// Implemented in compositePixel_amd64.s
func compositePixelBatchAVX2(pix []byte, offset int, r, g, b, a float64, count int)
```

```go
// compositePixel_generic.go
//go:build !amd64 && !arm64

package fit

func compositePixelBatchScalar(pix []byte, offset int, r, g, b, a float64, count int) {
    // ... scalar implementation ...
}
```

**Note:** Use `//go:build` (modern) not `// +build` (deprecated in Go 1.17+)

### Cross-Compilation Workflow

**Building for different platforms:**
```bash
# Native build (auto-detects current GOARCH)
go build -o bin/mayflycirclefit ./cmd/mayflycirclefit

# Cross-compile to Linux ARM64 (uses NEON assembly)
GOOS=linux GOARCH=arm64 go build -o bin/mayflycirclefit-arm64 ./cmd/mayflycirclefit

# Cross-compile to Windows x86-64 (uses AVX2 assembly)
GOOS=windows GOARCH=amd64 go build -o bin/mayflycirclefit.exe ./cmd/mayflycirclefit

# Cross-compile to 32-bit x86 (uses scalar fallback, no AVX2)
GOOS=linux GOARCH=386 go build -o bin/mayflycirclefit-i386 ./cmd/mayflycirclefit
```

**Build matrix (example for releases):**
```bash
#!/bin/bash
# scripts/build-all.sh

platforms=(
    "linux/amd64"      # AVX2 assembly
    "linux/arm64"      # NEON assembly
    "darwin/amd64"     # AVX2 assembly (Intel Mac)
    "darwin/arm64"     # NEON assembly (Apple Silicon)
    "windows/amd64"    # AVX2 assembly
    "linux/386"        # Scalar fallback
)

for platform in "${platforms[@]}"; do
    GOOS="${platform%/*}"
    GOARCH="${platform#*/}"
    output="bin/mayflycirclefit-${GOOS}-${GOARCH}"

    echo "Building for $GOOS/$GOARCH..."
    GOOS=$GOOS GOARCH=$GOARCH go build -o "$output" ./cmd/mayflycirclefit
done
```

**CI/CD integration (GitHub Actions):**
```yaml
# .github/workflows/build.yml
jobs:
  build:
    strategy:
      matrix:
        include:
          - os: ubuntu-latest
            goarch: amd64
            assembly: AVX2
          - os: ubuntu-latest
            goarch: arm64
            assembly: NEON
          - os: macos-latest
            goarch: arm64
            assembly: NEON
          - os: windows-latest
            goarch: amd64
            assembly: AVX2

    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'
      - run: go build -v ./...
      - run: go test -v ./...
```

---

## Memory Alignment Requirements

### Why Alignment Matters

**SIMD load/store instructions:**
- **Aligned loads:** Faster, single cache line access (e.g., `VMOVDQA` for AVX2)
- **Unaligned loads:** Slower, may span cache lines (e.g., `VMOVDQU` for AVX2)
- **Performance penalty:** 2-5% slowdown for unaligned access on modern CPUs (not critical, but nice-to-have)

**Alignment requirements:**
- **AVX2 (256-bit):** 32-byte alignment optimal (not required)
- **NEON (128-bit):** 16-byte alignment optimal (not required)
- **Scalar:** No alignment requirement (1-byte boundary)

### Ensuring Alignment in Go

**Image buffer alignment:**
```go
// image.NRGBA Pix is guaranteed to be 8-byte aligned (Go slice header)
// For 32-byte alignment (AVX2), we'd need to allocate custom buffer:

func newAlignedImage(width, height int) *image.NRGBA {
    pixelCount := width * height * 4

    // Allocate extra bytes for alignment padding
    const alignment = 32  // AVX2
    rawBuf := make([]byte, pixelCount+alignment-1)

    // Calculate aligned offset
    addr := uintptr(unsafe.Pointer(&rawBuf[0]))
    offset := (alignment - (addr % alignment)) % alignment

    // Create slice starting at aligned address
    alignedBuf := rawBuf[offset : offset+pixelCount]

    return &image.NRGBA{
        Pix:    alignedBuf,
        Stride: width * 4,
        Rect:   image.Rect(0, 0, width, height),
    }
}
```

**Complexity vs benefit:**
- ⚠️ **Manual alignment adds complexity** (unsafe pointer arithmetic, slice lifetime)
- ⚠️ **Modern CPUs tolerate unaligned access well** (2-5% penalty)
- ✅ **Use unaligned loads initially** (`VMOVDQU` for AVX2, `VLD1` for NEON)
- 🔮 **Future optimization:** If profiling shows alignment is bottleneck (unlikely)

### Unaligned Fallback Strategy

**Assembly code using unaligned loads:**
```asm
// compositePixel_amd64.s

// Use unaligned load (VMOVDQU, not VMOVDQA)
VMOVDQU (DI), Y4          // Load 32 bytes from potentially unaligned address

// ... processing ...

// Use unaligned store
VMOVDQU Y10, (DI)         // Store 32 bytes to potentially unaligned address
```

**Performance impact:**
- **Modern CPUs (2015+):** Negligible penalty (<5%) for unaligned SIMD
- **Older CPUs (pre-2013):** May see 10-20% penalty, but those lack AVX2 anyway
- **ARM NEON:** Unaligned loads are standard (`VLD1` handles any alignment)

**Recommendation:**
- ✅ **Start with unaligned loads** (simpler, works everywhere)
- 📊 **Profile first** before adding alignment complexity
- 🔮 **Optimize later** only if profiling shows it's a bottleneck (unlikely)

---

## Implementation Roadmap

### Phase 1: Prototype AVX2 in C (Validation & Learning)

**Goal:** Validate SIMD approach, learn intrinsics, establish baseline

**Tasks:**
1. Write `compositePixelBatch_avx2.c` with Intel intrinsics:
   - Process 8 pixels per iteration (256-bit)
   - Porter-Duff alpha compositing
   - Handle remainder pixels with scalar loop
2. Write C test harness:
   - Compare against scalar reference pixel-by-pixel
   - Benchmark with varying pixel counts (8, 64, 256, 1024)
3. Measure speedup:
   - Target: 4-6× over scalar C implementation
   - If <2×, reevaluate SIMD approach

**Deliverable:** `prototypes/compositePixel_avx2.c` with benchmarks

**Timeline:** 1-2 days (learning intrinsics, testing)

### Phase 2: Transpile with GoAT

**Goal:** Convert C intrinsics → Plan9 assembly

**Tasks:**
1. Install GoAT:
   ```bash
   go install github.com/gorse-io/goat@latest
   ```
2. Transpile C code:
   ```bash
   goat -O3 prototypes/compositePixel_avx2.c > internal/fit/compositePixel_amd64.s
   ```
3. Review generated assembly:
   - Add comments explaining register usage
   - Check for inefficiencies (unnecessary moves, missed optimizations)
   - Hand-tune hot loops if needed
4. Fix calling convention:
   - Ensure parameters match Go function signature
   - Verify stack frame layout (`FP`, `SP` offsets)

**Deliverable:** `internal/fit/compositePixel_amd64.s` (working Plan9 assembly)

**Timeline:** 1 day (transpilation, review, fixes)

### Phase 3: Integrate into Go

**Goal:** Wire up assembly to Go renderer, add runtime dispatch

**Tasks:**
1. Create file structure:
   - `compositePixel.go` (runtime dispatcher)
   - `compositePixel_amd64.go` (AVX2 declaration)
   - `compositePixel_generic.go` (scalar fallback)
2. Implement runtime dispatch:
   ```go
   var compositePixelBatch func(...)

   func init() {
       if cpu.X86.HasAVX2 {
           compositePixelBatch = compositePixelBatchAVX2
       } else {
           compositePixelBatch = compositePixelBatchScalar
       }
   }
   ```
3. Update `renderCircle` to use batch interface:
   - Process scanlines in batches of 8 pixels
   - Handle remainder pixels with scalar
4. Add logging:
   ```go
   slog.Info("SIMD backend", "backend", "AVX2", "width", "256-bit")
   ```

**Deliverable:** Working SIMD integration with runtime dispatch

**Timeline:** 1 day (integration, testing)

### Phase 4: Validate & Test

**Goal:** Ensure correctness, measure performance

**Tasks:**
1. **Correctness tests:**
   ```go
   func TestCompositePixelBatchEquivalence(t *testing.T) {
       // Generate random pixel data
       img1 := randomImage(256, 256)
       img2 := cloneImage(img1)

       // Apply SIMD batch
       compositePixelBatchAVX2(img1.Pix, 0, 0.5, 0.3, 0.7, 0.8, 8)

       // Apply scalar reference
       for i := 0; i < 8; i++ {
           compositePixelScalar(img2.Pix, i*4, 0.5, 0.3, 0.7, 0.8)
       }

       // Compare pixel-by-pixel (allow 1 unit difference due to rounding)
       for i := 0; i < 32; i++ {
           if abs(img1.Pix[i] - img2.Pix[i]) > 1 {
               t.Errorf("Pixel %d: SIMD=%d, scalar=%d", i, img1.Pix[i], img2.Pix[i])
           }
       }
   }
   ```

2. **Benchmark tests:**
   ```go
   func BenchmarkCompositePixelScalar(b *testing.B) {
       img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
       b.ResetTimer()
       for i := 0; i < b.N; i++ {
           compositePixelBatchScalar(img.Pix, 0, 0.5, 0.3, 0.7, 0.8, 256)
       }
   }

   func BenchmarkCompositePixelAVX2(b *testing.B) {
       if !cpu.X86.HasAVX2 {
           b.Skip("AVX2 not available")
       }
       img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
       b.ResetTimer()
       for i := 0; i < b.N; i++ {
           compositePixelBatchAVX2(img.Pix, 0, 0.5, 0.3, 0.7, 0.8, 256)
       }
   }
   ```

3. **Real-world profiling:**
   ```bash
   # Profile medium workload with SIMD
   ./scripts/profiling/profile-run.sh test-256x256.png 30 100 joint

   # Force scalar fallback for comparison
   GODEBUG=cpu.avx2=off ./scripts/profiling/profile-run.sh test-256x256.png 30 100 joint
   ```

4. **Multi-platform testing:**
   - Linux x86-64 (AVX2)
   - macOS ARM64 (NEON, deferred to Phase 5)
   - Windows x86-64 (AVX2)
   - Linux 386 (scalar fallback)

**Deliverable:** Test coverage >90%, benchmarks showing 4-6× speedup

**Timeline:** 2 days (tests, benchmarks, profiling)

### Phase 5: ARM NEON Support (Optional)

**Goal:** Add ARM64 SIMD for Apple Silicon, AWS Graviton

**Tasks:**
1. Repeat Phase 1-4 for NEON:
   - Prototype in C with `arm_neon.h` intrinsics
   - Transpile with GoAT
   - Create `compositePixel_arm64.s`
   - Test on ARM64 hardware
2. Update runtime dispatch:
   ```go
   if cpu.ARM64.HasASIMD {
       compositePixelBatch = compositePixelBatchNEON
   }
   ```
3. Benchmark on Apple M-series:
   - Target: 4-5× speedup (128-bit NEON vs 256-bit AVX2)

**Deliverable:** ARM64 SIMD support

**Timeline:** 3 days (if AVX2 successful, this is faster due to learned pattern)

---

## Comparison Matrix

| Criterion | cgo + C Intrinsics | Plan9 Assembly | Pure Go + unsafe |
|-----------|-------------------|----------------|------------------|
| **Performance** | 3-5× (minus cgo overhead) | **4-6× (no overhead)** | ~1× (no SIMD) |
| **Portability** | Requires C compiler | ✅ Pure Go toolchain | ✅ Pure Go |
| **Build complexity** | ❌ High (cgo, cross-compilers) | ✅ Low (`go build`) | ✅ Lowest |
| **Maintenance burden** | ⚠️ Moderate (C code, cgo boundary) | ❌ High (assembly, arch-specific) | ✅ Low (readable Go) |
| **Learning curve** | ✅ C intrinsics familiar | ❌ Steep (Plan9 syntax) | ✅ Go only |
| **Debugging** | ⚠️ Cross-boundary issues | ✅ Delve supports assembly | ✅ Standard Go tools |
| **Cross-compilation** | ❌ Hard (need target C compiler) | ✅ Easy (`GOARCH=arm64`) | ✅ Easy |
| **Production usage** | Rare in Go projects | ✅ Go stdlib, Minio, many | Standard |
| **Tooling support** | ✅ Intrinsics well-documented | ✅ GoAT, avo (transpilers) | None needed |
| **Distribution** | ❌ Static linking bloat, C deps | ✅ Single binary, no deps | ✅ Single binary |
| **CI/CD complexity** | ❌ Need C toolchains per platform | ✅ Standard Go CI | ✅ Standard Go CI |
| **Real-world benchmarks** | 7.9 GB/s (cgo overhead) | **33.6 GB/s** (native) | N/A (no SIMD) |
| **Autovectorization** | ✅ C compiler may help | ❌ Hand-written only | ❌ Go compiler won't vectorize |
| **Memory safety** | ⚠️ Manual in C, cgo boundary risks | ⚠️ Unsafe, manual register management | ✅ Safe (but no SIMD) |
| **Platform support** | Windows, Linux, macOS (if C compiler) | Windows, Linux, macOS (pure Go) | All platforms (pure Go) |
| **Recommended for** | Large batches, external lib integration | **Performance-critical inner loops** | Non-SIMD optimizations |

**Legend:**
- ✅ **Excellent** (major advantage)
- ⚠️ **Moderate** (acceptable with tradeoffs)
- ❌ **Poor** (significant drawback)

---

## Risks and Mitigations

### Risk 1: Plan9 Assembly is Hard to Write and Maintain

**Severity:** High
**Probability:** High (if writing assembly from scratch)

**Impact:**
- Developers unfamiliar with Plan9 syntax struggle to contribute
- Bugs in assembly are harder to debug (no type safety)
- Architecture-specific code fragments codebase

**Mitigations:**
1. **Use GoAT transpiler (primary mitigation):**
   - Write C prototype with intrinsics (familiar, easy to test)
   - Transpile to Plan9 assembly automatically
   - Hand-tune only if profiling shows inefficiencies

2. **Comprehensive testing:**
   - Pixel-exact equivalence tests (SIMD vs scalar)
   - Fuzz testing with random pixel data
   - Benchmark regression tracking

3. **Documentation:**
   - Add detailed comments to assembly (register usage, algorithm)
   - Document calling convention (stack frame layout)
   - Provide C prototype alongside assembly for reference

4. **Code review:**
   - Assembly changes require extra scrutiny
   - Test on multiple platforms (Linux, macOS, Windows)
   - Run full profiling suite before merging

**Success metrics:**
- Assembly code has >20 comments explaining logic
- Tests cover 100% of assembly code paths
- Documentation includes C prototype for maintenance

### Risk 2: Performance Gains Might Not Justify Complexity

**Severity:** Medium
**Probability:** Low (based on alpha blending benchmarks)

**Impact:**
- If <2× speedup, complexity not worth it
- Maintenance burden outweighs performance benefit

**Mitigations:**
1. **Prototype first (Phase 1):**
   - Measure speedup in C before committing to Go integration
   - If C prototype shows <2× speedup, reevaluate approach
   - Decision gate: Proceed only if ≥4× speedup in C

2. **Benchmark-driven development:**
   - Establish baseline with current scalar implementation
   - Measure after each optimization
   - Document speedup in `docs/task-10.x-optimization-report.md`

3. **Fallback plan:**
   - If SIMD doesn't deliver, document findings
   - Defer CPU optimization to Phase 11 (focus on GPU)
   - GPU will provide 10-100× speedup (more impact than CPU SIMD)

**Success metrics:**
- AVX2 implementation: ≥4× speedup over scalar
- NEON implementation: ≥4× speedup over scalar
- Overall rendering: ≥2.5× speedup (compositing is 54-61% of time)

### Risk 3: GoAT Generates Suboptimal Assembly

**Severity:** Low
**Probability:** Medium (transpilers rarely perfect)

**Impact:**
- Generated assembly has unnecessary instructions (extra moves, spills)
- Not reaching peak theoretical performance
- Hand-tuning required (defeats purpose of tooling)

**Mitigations:**
1. **Hand-tune hot loops:**
   - Identify innermost loop via profiling
   - Manually optimize register allocation
   - Remove unnecessary moves between registers
   - Compare against known-good examples (Minio HighwayHash)

2. **Benchmarking against C:**
   - Keep C prototype for performance comparison
   - If Plan9 assembly is <90% of C performance, investigate
   - Use `godbolt.org` to compare C compiler output vs GoAT output

3. **Alternative tooling:**
   - If GoAT output is poor, try avo (Go-based assembly generation)
   - As last resort, hand-write Plan9 assembly (learning curve)

**Success metrics:**
- Plan9 assembly performance ≥90% of C intrinsics performance
- Assembly code is readable with comments
- No obvious inefficiencies (unnecessary spills, moves)

### Risk 4: Cross-Platform Testing Burden

**Severity:** Medium
**Probability:** Medium

**Impact:**
- Bugs only surface on specific platforms (e.g., macOS ARM64)
- CI/CD complexity increases with multiple architectures
- Windows assembly may have subtle differences

**Mitigations:**
1. **CI matrix testing:**
   - GitHub Actions: test on ubuntu-latest, macos-latest, windows-latest
   - Test GOARCH: amd64, arm64, 386
   - Run full test suite + benchmarks on each platform

2. **Docker for cross-arch testing:**
   ```bash
   docker run --platform linux/arm64 golang:1.23 go test ./...
   ```

3. **Fallback validation:**
   - Force scalar fallback: `GODEBUG=cpu.avx2=off go test`
   - Ensure all platforms pass tests (even without SIMD)

**Success metrics:**
- All tests pass on Linux x86-64, macOS ARM64, Windows x86-64
- Scalar fallback works on 386, wasm

### Risk 5: Memory Alignment Issues Cause Crashes

**Severity:** Low (modern CPUs tolerate unaligned access)
**Probability:** Very Low

**Impact:**
- Segfault on older CPUs with strict alignment
- Performance degradation on some platforms

**Mitigations:**
1. **Use unaligned loads initially:**
   - AVX2: `VMOVDQU` (unaligned) not `VMOVDQA` (aligned)
   - NEON: `VLD1` handles any alignment automatically
   - Worst case: 5% penalty on modern CPUs (acceptable)

2. **Test on diverse hardware:**
   - Older Intel CPUs (pre-Haswell) if available
   - AWS EC2 instances (various CPU generations)
   - Raspberry Pi 4 (ARM Cortex-A72)

3. **Add alignment check logging (debug mode):**
   ```go
   if addr := uintptr(unsafe.Pointer(&img.Pix[0])); addr%32 != 0 {
       slog.Debug("Unaligned image buffer", "addr", addr, "alignment", addr%32)
   }
   ```

**Success metrics:**
- No crashes on any tested platform
- Alignment penalty <5% (acceptable for initial version)

---

## References

### Performance Benchmarks

**Alpha Blending with SIMD:**
- breeswish.org: "AVX2 Optimized Alpha Blend" (2015) — 5.7× speedup, detailed implementation
- Stack Overflow: "SIMD for alpha blending" — SSE4.1 3.14× speedup, AVX2 discussion
- GitHub sid5291/Neon-AlphaBlend — NEON implementation benchmarks

**Go SIMD Benchmarks (2024):**
- Sourcegraph Blog: "From slow to SIMD: A Go optimization story" — Assembly faster than cgo
- 1337-42 Blog: "Bit Hamming in Golang: SIMD Supported Code" — Native 33.6 GB/s vs cgo 7.9 GB/s
- Gorse.io: "How to Use AVX512 in Golang via C Compiler" — GoAT performance analysis

### Production Examples

**Minio HighwayHash:**
- Repository: github.com/minio/highwayhash
- Performance: >10 GB/sec with hand-written Plan9 assembly
- Usage: 350+ imports, actively maintained (2024)
- Pattern: Runtime dispatch, AVX2/NEON/scalar fallback

**Go Standard Library:**
- crypto/aes: AES-NI instructions (`crypto/aes/asm_amd64.s`)
- crypto/sha256: SHA extensions (`crypto/sha256/sha256block_amd64.s`)
- math/big: Multi-precision arithmetic with SIMD

### Research Articles

**SIMD in Go (2024):**
- Tfrain: "SIMD in Go: An In-Depth Exploration" — Comprehensive SIMD overview
- Ben Hoyt: "Go performance from version 1.0 to 1.22" — Compiler optimization history
- Daniel Lemire: "The Go compiler needs to be smarter" — Autovectorization limitations

**Compiler Analysis:**
- InfoQ: "Go 1.20-1.23 release notes" — PGO improvements, no SIMD
- Medium (c-bata): "Optimizing Go by AVX2 using Auto-Vectorization in LLVM" — Cgo LLVM approach

### Tools and Documentation

**GoAT (Recommended):**
- Repository: github.com/gorse-io/goat
- Status: Actively maintained (2024)
- Features: C intrinsics → Plan9 assembly, AVX512/AVX2/NEON support

**avo:**
- Repository: github.com/mmcloughlin/avo
- Concept: Generate Plan9 assembly from Go code
- Use case: Complex register allocation, type-safe assembly generation

**c2goasm (Deprecated):**
- Repository: github.com/minio/c2goasm (archived Dec 2021)
- Status: Read-only, no AVX512 support
- Successor: GoAT

**Official Documentation:**
- go.dev/doc/asm — Go assembler reference
- golang.org/x/sys/cpu — CPU feature detection
- Intel Intrinsics Guide — x86 intrinsics reference
- ARM NEON Intrinsics — ARM intrinsics reference

---

## Conclusion

**Recommended approach for MayFlyCircleFit:** **Plan9 Assembly with GoAT tooling**

### Rationale Summary

1. **Performance critical:** Baseline profiling shows 54-61% time in compositing → SIMD will have measurable impact (4-6× speedup)
2. **Pure Go ecosystem:** No build complexity, easy CI/CD, single binary distribution (aligns with Go philosophy)
3. **Production-proven:** Minio HighwayHash and Go stdlib demonstrate this approach scales to high-performance projects
4. **Tooling mitigation:** GoAT transpiles C intrinsics → Plan9 assembly, avoiding hand-writing assembly from scratch
5. **Complementary to GPU:** Fast CPU path benefits users without GPU, provides baseline for GPU comparison (Phase 11)

### Expected Outcomes

**Performance:**
- **AVX2 (x86-64):** 4-6× speedup in compositing → 2.5-3× overall rendering speedup
- **NEON (ARM64):** 4-5× speedup in compositing → 2.5-3× overall rendering speedup
- **Throughput:** 1,353 → ~3,500 circles/sec on 256×256 medium workload

**Deliverables (Phase 10.1-10.10):**
- ✅ This design document (Task 10.1)
- Pending: SSD kernel interface design (Task 10.2)
- Pending: Scalar baseline, AVX2, NEON implementations (Tasks 10.3-10.5)
- Pending: Runtime dispatch, integration, testing (Tasks 10.6-10.9)
- Pending: Performance validation and documentation (Task 10.10)

### Next Steps

1. **Immediate (Task 10.2):** Design SSD kernel interface in `internal/fit/ssd.go`
2. **Phase 1 (Task 10.3):** Implement scalar baseline, establish correctness tests
3. **Phase 2 (Task 10.4):** Prototype AVX2 in C, transpile with GoAT, integrate into Go
4. **Phase 3 (Task 10.5):** Add NEON support for ARM64 (if AVX2 successful)
5. **Phase 4 (Tasks 10.6-10.10):** Runtime dispatch, testing, performance validation

**Document status:** ✅ Complete (Task 10.1 research finished)
**Approval needed:** Review this design before proceeding to Task 10.2
