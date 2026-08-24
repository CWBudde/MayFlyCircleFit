# Why the SIMD kernels are hand-written Plan 9 assembly

A condensed record of the design research that preceded the CPU vector kernels,
kept for the one question it still answers: *why this approach and not the
obvious alternatives.* The option analysis, five-phase roadmap, and risk
register that made up the bulk of the original document have all been settled by
the shipped implementation and are not reproduced here — they are in git
history.

**[`rendering-internals.md`](rendering-internals.md) is authoritative** wherever
it disagrees with anything below.

## The problem the research was answering

Baseline profiling put `compositePixel` at 54–61% of CPU time during circle
rendering, called on the order of 30 million times per evaluation at 256×256
with 30 circles. Scalar float64 Porter-Duff, one pixel at a time. A vector unit
processes four to eight pixels at once, so the ceiling was real.

## The three options

| Option | Verdict |
| --- | --- |
| cgo + C with AVX2/NEON intrinsics | **Rejected.** cgo call overhead dominates for kernels this small (measured elsewhere at 7.9 GB/s against 33.6 GB/s for assembly), it requires a C compiler in every build and cross-build, and it forfeits `go build`. |
| Go Plan 9 assembly | **Selected.** No call overhead, pure-Go toolchain, `CGO_ENABLED=0` cross-builds, production-proven in the Go standard library and in projects like Minio HighwayHash. |
| Pure Go with `unsafe` and `x/sys/cpu` | **Rejected as a SIMD strategy.** Go's compiler does not autovectorize the shapes this hot path needs. `x/sys/cpu` is still used — for feature *detection*, feeding the tier dispatch. |

The choice held. What the document got wrong was the *route*, not the
destination.

## What the design proposed and reality rejected

- **GoAT transpilation.** The plan was to write kernels as C intrinsics and
  transpile them to Plan 9 assembly, on the theory that hand-writing Plan 9 was
  the main risk. A prototype was built and validated at 3.05×, then discarded:
  the transpiler added a build-time dependency and a layer between the assembly
  that runs and the source anyone reads. Every shipped kernel is hand-written.
  See [`rejected-optimizations.md`](rejected-optimizations.md).
- **cgo anywhere near the renderer.** Beyond the performance argument, a package
  that uses cgo *cannot contain Go assembly* at all — `cmd/go` hands its `.s`
  files to the C compiler, which rejects Plan 9 directives. This is a hard
  constraint the design did not anticipate, and it is why the OpenCL renderer
  lives in its own package.
- **Memory-alignment engineering.** The design budgeted significant effort for
  guaranteeing 32-byte alignment. The shipped kernels use unaligned loads and
  handle the tail scalar; on the target microarchitectures the difference did
  not justify the machinery.
- **Compositing as the first target.** The design aimed SIMD at `compositePixel`
  and the implementation started with SSD instead — which turned out to be
  nearly worthless end to end (9–18× on the kernel, 1.03–1.29× overall). The
  *design's* instinct about where the time was, was right; the sequencing was
  not. See [`cpu-performance-history.md`](cpu-performance-history.md).

## What it predicted correctly

Four to six times on the vector kernels against scalar. Measured: about 6× for
AVX2 SSD, 5.2× for NEON, about 6× for SSE2. The end-to-end multiplier was
smaller than projected, because Amdahl applied to a hot path that kept moving as
each bottleneck was removed.
