# GPU Backend Design and Status

## Baseline Constraints
- The renderer contract lives in `internal/fit/renderer/renderer.go` and expects `Render`, `Cost`, and `Reference`.
- Current CPU path composites circles in Go and computes SSD via SIMD kernels; GPU path must match float32/64 semantics within tolerance.
- Reference images are static per run, while candidate circle parameters stream in for each evaluation; minimizing host↔device transfers is critical.
- CLI aims to stay cross-platform (Windows, Linux, macOS) with optional headless execution on build agents.

## Candidates
### OpenGL Fragment/Compute
- **Bindings:** `github.com/go-gl/gl/v4.X-core/gl` with `glfw` for context; mature and CGO-based.
- **Approach:** Render circles in an off-screen framebuffer (fragment shader) or use compute shaders (>= GL 4.3) to draw directly into textures and compute SSD.
- **Pros:** Widely available drivers on desktop GPUs; rich tooling (RenderDoc). Easy batching via SSBOs.
- **Cons:** macOS caps at OpenGL 4.1 (no compute). Requires window/context management, even headless (use hidden pbuffer). Debugging CGO state in Go can be tricky.
- **Fit:** Good for rapid prototype on Windows/Linux; add a dynamic fallback for macOS (fragment shader + CPU reduction).

### OpenCL
- **Bindings:** `github.com/jgillich/go-opencl/cl`; maintained, maps closely to C API.
- **Approach:** `render_cost` composites circles and performs the first workgroup SSD reduction. A generic `reduce_sum` kernel then ping-pongs partial sums until one scalar remains. The reference and rendered image stay resident on the device between evaluations.
- **Pros:** Designed for compute; portable across NVIDIA/AMD/Intel GPU vendors and even CPU implementations. Headless-friendly; no windowing.
- **Cons:** Apple deprecated OpenCL (still available on Intel Macs, missing on Apple Silicon without third-party ICD). Kernel language feels lower-level; need to manage workgroup tuning for each vendor. Error messages less friendly.
- **Fit:** Strong choice for broad hardware coverage on Windows/Linux; document macOS limitations and provide CPU fallback.

### WebGPU
- **Bindings:** `github.com/webgpu-go/webgpu`, or via cgo layer around Dawn/WGPU.
- **Pros:** Modern API with explicit portability goals; maps to DX12/Metal/Vulkan/GL behind the scenes.
- **Cons:** Bindings still young; frequent API churn and limited documentation. Requires shipping Dawn/WGPU native libraries. Validation layers currently verbose but slow.
- **Fit:** Future-proof, but high setup cost; better as follow-up once a stable backend exists.

### Vulkan Compute
- **Bindings:** `github.com/vulkan-go/vulkan`; low-level, explicit control.
- **Pros:** Excellent performance potential; first-class compute shaders, descriptor sets allow efficient parameter streaming.
- **Cons:** Heavy boilerplate, large mental overhead, requires per-platform surface/instance setup. Validation layers necessary for sanity. Not ideal for quick delivery.
- **Fit:** Overkill for initial GPU enablement; only pursue if we need maximum control after OpenCL/OpenGL proves insufficient.

## Comparison Snapshot
| Option   | Portability (Win/Linux/macOS) | Binding maturity | Prototype complexity | Notes |
|----------|--------------------------------|------------------|----------------------|-------|
| OpenGL   | High / High / Medium           | High             | Medium               | Use fragment path on macOS, compute elsewhere |
| OpenCL   | High / High / Low              | Medium           | Medium               | macOS Apple Silicon unsupported |
| WebGPU   | Medium / Medium / High         | Low              | High                 | Requires bundling native runtimes |
| Vulkan   | High / High / Medium           | Medium           | Very High            | Significant boilerplate |

## Recommendation
Start with **OpenCL** as the primary GPU backend:
- Compute-centric API matches our need to combine rendering and SSD reduction without extra passes.
- Headless execution is straightforward and avoids OpenGL context quirks in CLI mode.
- Existing Go binding is ergonomic enough for kernel compilation, buffer management, and queue submission.
- Works on NVIDIA, AMD, and Intel GPUs out of the box; document macOS Apple Silicon fallback (rely on CPU renderer until Metal/WebGPU option is ready).

Parallel to the OpenCL prototype, scope an **OpenGL fragment-shader fallback** for macOS or integrated GPUs where OpenCL is unavailable, reusing the same parameter packing logic.

## Implementation Status
- `internal/fit/renderer/backend.go` centralises backend selection and normalises CLI input.
- `internal/fit/gpu/opencl_runtime_*.go` enumerates platforms/devices and bootstraps an OpenCL context (GPU preferred, CPU fallback) when built with `-tags gpu`; non-GPU builds return a helpful error.
- `internal/fit/renderer/opencl/renderer_gpu.go` implements rendering and cost evaluation in OpenCL with CPU degradation on runtime errors. It lives in its own package because Go forbids Plan 9 assembly in a package that uses cgo, and `internal/fit/renderer` carries the SIMD kernels; `internal/fit/renderer/renderer_opencl_gpu.go` is the gpu-tagged adapter that injects the CPU fallback and supplies the unexported session hook. Cost reduction is entirely on-device: the host reads only the final float rather than a full pixel-error buffer.
- Cost and image caching are separate. `Cost` leaves the rendered output resident; `Render` reads the full image only when requested and can reuse output from a matching cost evaluation without dispatching the kernels again.
- The kernel quantizes composited channels to NRGBA semantics before scoring, so the reduced cost describes the image returned by `Render`. CPU/OpenCL parity tests allow a 1% cost tolerance and two channel values for float32 geometry and edge-coverage differences.
- CLI exposes `--backend` (default `cpu`) and reports the selected backend during runs. GPU mode renders and scores joint, sequential, and batch pipelines via OpenCL when compiled with `-tags gpu`.
- Sequential and batch optimization create independent OpenCL sessions as the active circle count grows. These modes replay retained parameters instead of accumulating a device-side base canvas; this preserves pipeline semantics but needs vendor-GPU benchmarking before performance claims.

## Memory Layout and Transfers

Let `P = width * height`, `K` be the circle count, `L` the selected reduction
workgroup size, and `G = ceil(P / L)`. Buffers are allocated once per renderer
and released with its OpenCL runtime.

| Storage | Device layout and size | Lifetime | Host/device traffic |
|---------|------------------------|----------|---------------------|
| Circle parameters | `float32[7*K]`, `28*K` bytes | Persistent | Host to device only when the parameter hash changes. The default 10-circle job writes 280 bytes; the accepted 3000-circle maximum writes 84,000 bytes. |
| Reference image | packed `uchar4[P]` NRGBA, `4*P` bytes | Persistent, read-only | Uploaded once during initialization. Non-zero image origins and padded host strides are normalized row by row before upload. |
| Rendered image | packed `uchar4[P]` NRGBA, `4*P` bytes | Persistent | Remains device-resident after `Cost`; copied to the reusable host `image.NRGBA` only when `Render` requests a new parameter hash. |
| Partial sums A/B | `float32[G]` each, `4*G` bytes each | Persistent | Device-only ping-pong storage for multi-pass reduction. |
| Reduction scratch | `float32[L]`, `4*L` bytes per workgroup | One kernel dispatch | OpenCL local memory; never transferred to the host. |
| Final cost | one `float32`, 4 bytes | Per changed evaluation | Device to host after reduction; this read also synchronizes the evaluation required by the optimizer. |

Repeated `Cost` calls with identical parameters reuse the cached device result.
Calling `Render` immediately after `Cost` reuses that same rendered output rather
than dispatching kernels again. Packing the reference and output as `uchar4`
replaced the prototype's `float4` storage, reducing both persistent pixel-buffer
memory and the only large, lazy image readback by 75% while matching NRGBA's
native representation.

### Pinned-memory decision

Pinned host memory was evaluated but is deliberately not used yet. The only
recurring host-to-device payload is `28*K` bytes, at most 28 KB under the current
input limit, while every evaluation already has to wait for a four-byte reduced
cost. An OpenCL pinned buffer would add map/unmap synchronization, lifetime and
cgo ownership complexity, and vendor-dependent behavior. Packing the large
image buffers removes 75% of the material transfer without those costs.

Vendor-GPU measurement has since settled half of this and reopened the other
half. Parameter upload is flat at about 10 µs from K=1 to K=100, so it is
latency-bound and pinning it would buy nothing: **that decision stands.** The
condition above named the wrong transfer, though. The image readback runs at
0.5-0.7 GB/s and costs 5.9 ms at 1024², more than three times a complete
evaluation at that size, and it is the only regime in which the CPU renderer
beats the GPU. Pinned staging is worth evaluating **for the readback**; see
[`gpu-performance-report.md`](gpu-performance-report.md).

## Validation

The Ubuntu GPU-tag CI job installs the PoCL CPU implementation, verifies OpenCL
platform discovery, compiles all GPU-tagged packages, and runs the focused
OpenCL parity, reduction, caching, and all-mode pipeline tests. This is deterministic runtime
coverage of the OpenCL path, but it is not evidence of GPU throughput or vendor
driver compatibility.

Run the same focused tests on each target GPU before relying on the backend:

```sh
go test -tags gpu -count=1 ./internal/fit/renderer/... -run '^TestOpenCL'
go test -tags gpu ./internal/fit/renderer -bench '^BenchmarkRenderer'
```

Performance claims require benchmarks on actual GPU hardware. PoCL executes on
the CI runner CPU and is used for correctness and lifecycle coverage only.

### Correctness against the CPU renderer

`renderer_opencl_correctness_gpu_test.go` is the parity suite: a circle-count ×
canvas-size matrix, degenerate canvases (1×1, single row, single column), a
named catalog of edge cases — circles outside each bound, straddling each edge
and the corner, zero and negative radius, zero opacity, a canvas-covering
radius, concentric and coincident stacks, a subpixel centre walk — a
compositing-order check, and a randomized deviation sweep. The CPU render is the
golden image throughout; there is no committed PNG, because a checked-in
baseline would only duplicate the oracle and then go stale against it. When a
scene fails, the CPU render, the GPU render, and an amplified difference map are
written to `$CIRCLEFIT_GPU_ARTIFACTS` (default: a `circlefit-gpu-mismatch`
directory under the system temp directory), because a coordinate and two channel
values do not show what a rasterizer got wrong.

Two things that suite is built to prevent:

- **A CPU device passing as validation.** `InitOpenCL` prefers a GPU but falls
  back to a CPU device, so on a machine with only PoCL installed every parity
  test passes while measuring nothing about a GPU.
  `TestOpenCLDeviceReportsAPreparedDevice` fails under
  `CIRCLEFIT_REQUIRE_GPU_DEVICE=1` unless the selected device is of type GPU,
  and logs the platform, device, driver version, and compute units either way.

  The two switches are deliberately separate. `CIRCLEFIT_REQUIRE_OPENCL=1`
  means "OpenCL has to work here, do not skip" and is what `ci-gpu-compile.yml`
  sets while running on PoCL's CPU device on purpose;
  `CIRCLEFIT_REQUIRE_GPU_DEVICE=1` additionally demands a vendor GPU. Set both
  when a run is meant to be GPU validation:

  ```sh
  CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
      go test -tags gpu -count=1 ./internal/fit/renderer/... -run '^TestOpenCL'
  ```
- **A tolerance hiding a structural mismatch.** The suite found one: the kernel
  implemented a different rasterization rule from the CPU renderer, wrong by up
  to 226 of 255 on a small number of pixels and by a factor of two in cost on a
  sparse scene. It is fixed, and the whole account is in
  [`renderer-correctness.md`](renderer-correctness.md).

What remains is arithmetic. The device is float32 end to end against a float64
CPU path, so parity is a budget — ±2 per channel and 1% relative cost — and not
byte-identity. Measured on an NVIDIA T550 the worst case is 1 channel and 0.021%
of cost. The cost bound is the one that binds: it comes from accumulating the
SSD in float32 across the canvas, so it grows with pixel count while the channel
deviation does not. **Do not compare a GPU cost against a CPU cost as if they
were the same number** — within a run they are consistent, but a recorded
figure from one backend is not a baseline for the other.

Validated on one vendor GPU (NVIDIA T550, driver 580.178.04, OpenCL 3.0 CUDA).
AMD and Intel remain unmeasured for both parity and throughput.

### Local PoCL transfer baseline

**Superseded for performance by
[`gpu-performance-report.md`](gpu-performance-report.md).** PoCL runs on the
host CPU. The section is kept because its method and its shape conclusions
still hold; its numbers describe a CPU pretending to be a device.

The local development baseline uses PoCL on `cpu-haswell-AMD Ryzen 5 4600H with
Radeon Graphics` (12 compute units). The prototype materialization path read 16
bytes per pixel and then converted float channels on the host; inspecting that
transfer boundary motivated eliminating the conversion and reducing the
readback to 4 bytes per pixel. PoCL uses host CPU memory, so this is not evidence
about PCIe transfers or real-GPU performance.

With packed `uchar4` buffers, the focused local microbenchmarks reported these
medians (`-benchtime=300ms -count=3`, with zero Go allocations per operation):

| Boundary | Cases | PoCL median |
|----------|-------|-------------|
| Parameter pack and blocking upload | K=1, 10, 50, 100 | 21.96, 22.25, 22.45, 22.18 µs/op |
| Resident image readback | 64², 256², 512², 1024² | 22.14, 34.61, 92.20, 473.78 µs/op |

The nearly constant parameter times through 2.8 KB indicate that PoCL queue and
driver latency dominates this range, so pinned parameter staging has no local
justification. The packed readback scales with image size and avoids both the
old four-times-larger transfer and its host conversion loop. These conclusions
apply to this PoCL CPU baseline only. A short post-change end-to-end comparison
was noisy, including an inverted 256x256 `Cost`/`CostThenRender` result, so it is
not used for a before/after speedup claim.

### Local CPU/PoCL pipeline comparison

**Superseded for performance by
[`gpu-performance-report.md`](gpu-performance-report.md).** PoCL runs on the
host CPU. The section is kept because its method and its shape conclusions
still hold; its numbers describe a CPU pretending to be a device.

The backend pipeline benchmark uses a 64x64 reference, 12 circles, and eight
distinct (uncached) optimizer evaluations per stage. It includes stage-session
creation, retained-state handling, and final image materialization, while
excluding construction of the initial renderer. The table reports medians from
five samples of five complete pipelines (`-benchtime=5x -count=5`):

| Mode | Evaluations/pipeline | CPU median | PoCL median | PoCL vs CPU | CPU B/op, allocs/op | PoCL B/op, allocs/op |
|------|---------------------:|-----------:|------------:|------------:|--------------------:|---------------------:|
| Joint | 10 | 0.740 ms | 2.187 ms | 3.0x slower | 20,006, 12 | 19,833, 65 |
| Sequential | 109 | 3.315 ms | 629.393 ms | 190x slower | 602,182, 179 | 1,001,633, 1,435 |
| Batch (four circles/stage) | 28 | 1.887 ms | 226.270 ms | 120x slower | 247,891, 64 | 370,521, 452 |

PoCL ran on the host CPU, so these numbers compare the complete backend paths
on one machine rather than CPU rendering with a physical GPU. They show that
the current staged OpenCL path is not suitable for PoCL: each stage constructs
an independent runtime and program and replays retained circles, whereas the
CPU renderer accumulates a base canvas. Sharing compiled OpenCL resources and
adding a device-side accumulated canvas are the likely next staged-path
optimizations, but vendor-GPU measurements are still required before making
hardware performance claims.

Reproduce the focused correctness and transfer-sensitive benchmarks with:

```sh
CIRCLEFIT_REQUIRE_OPENCL=1 go test -tags gpu -count=1 \
  ./internal/fit/renderer/... -run '^TestOpenCL'
CIRCLEFIT_REQUIRE_OPENCL=1 go test -tags gpu -run '^$' \
  -bench '^BenchmarkOpenCL(ParameterPackAndUpload|ResidentImageReadback)$' \
  -benchmem -benchtime=2s -count=5 ./internal/fit/renderer/opencl
go test -tags gpu -run '^$' -bench '^BenchmarkRenderer(Cost|CostThenRender)$' \
  -benchmem -benchtime=2s -count=5 ./internal/fit/renderer
CIRCLEFIT_REQUIRE_OPENCL=1 go test -tags gpu -run '^$' \
  -bench '^BenchmarkOptimizePipelineBackends$' \
  -benchmem -benchtime=5x -count=5 ./internal/fit/renderer
```

Record the OpenCL device name/vendor with every result. The matrix has now been
measured on an NVIDIA T550: see
[`gpu-performance-report.md`](gpu-performance-report.md), which supersedes every
PoCL timing on this page. AMD and Intel devices remain unmeasured.
