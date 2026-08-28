# GPU Backend Design and Status

## Status, and where to start

OpenCL is **experimental and opt-in**, available only in a `-tags gpu` build,
and validated on exactly one vendor GPU. Read
[`gpu-performance-report.md`](gpu-performance-report.md) for what it is worth on
that device and [`renderer-correctness.md`](renderer-correctness.md) for the
parity contract it does *not* hold.

| You came here to | Read |
|---|---|
| Build and run it | [Requirements and setup](#requirements-and-setup), [Example commands](#example-commands) |
| Decide whether to use it | [When to use the GPU](#when-to-use-the-gpu) |
| Understand a bad number | [`gpu-performance-report.md`](gpu-performance-report.md), [Device and driver quirks](#device-and-driver-quirks-found-during-validation) |
| Fix a failing run | [`troubleshooting.md`](troubleshooting.md#gpu-unavailable) |
| Know why macOS has no GPU path | [macOS has no GPU backend, by decision](#macos-has-no-gpu-backend-by-decision) |
| Know why it is still experimental | [Why this is still experimental](#why-this-is-still-experimental) |

The design sections from [Baseline Constraints](#baseline-constraints) onward are
the selection record — why OpenCL and not OpenGL, Vulkan, or WebGPU — plus the
memory layout and the validation method. They are still accurate; they are not
what you need to run a job.

## Requirements and setup

Three separate things have to be present, and they fail differently:

| | Needed for | Missing means |
|---|---|---|
| The `gpu` build tag and `CGO_ENABLED=1` | Compiling the OpenCL renderer into the binary | The binary has no OpenCL backend at all. It does not advertise `opencl`, refuses `serve --backend opencl` at startup, and rejects a job naming it at submit. |
| OpenCL headers and an ICD loader | Building | `go build -tags gpu` fails in cgo. |
| A vendor ICD and a working device | Running | `renderer backend unavailable`, with the runtime's reason appended. |

Build:

```sh
CGO_ENABLED=1 go build -tags gpu -o circlefit .
```

The published portable builds are CGO-disabled and therefore have no GPU
backend; a GPU binary has to be built on, and for, the target platform.

**Linux.** Install the loader and headers, then whichever ICD matches the
device. On Debian and Ubuntu the loader/headers are `ocl-icd-opencl-dev`; the
ICD is the vendor's (`nvidia-driver-*` ships the NVIDIA ICD, `intel-opencl-icd`
is Intel's NEO, ROCm or `amdgpu-pro` is AMD's), or `pocl-opencl-icd` for the
PoCL CPU implementation. Verify with `clinfo` before building anything —
`clinfo` listing no platform is the same failure the renderer will report, and
it is faster to read.

**Windows.** The vendor driver installs the ICD; headers come from the vendor
SDK. cgo needs a working C toolchain (mingw-w64) that the ordinary CPU build
does not.

**macOS.** Not supported — see
[the decision below](#macos-has-no-gpu-backend-by-decision). Use the CPU
renderer.

Exactly two combinations have actually been run in this repository, and nothing
else should be described as working:

- **Ubuntu CI**, `ocl-icd-opencl-dev` plus `pocl-opencl-icd`, executing on a CPU
  device. This is correctness and lifecycle coverage; PoCL is not a GPU.
- **Linux with an NVIDIA T550**, driver 580.178.04, platform NVIDIA CUDA,
  OpenCL 3.0. This is the only device any performance or parity figure in these
  documents describes.

AMD and Intel devices are untried here for both parity and throughput.

### Confirming you are actually on a GPU

`InitOpenCL` prefers a GPU device but falls back to a CPU device when that is
all the platform offers. That is deliberate — it is what lets CI exercise the
path on PoCL — and it is the single easiest way to publish a CPU measurement
under a GPU label. Two environment switches separate the cases, and they are
independent:

| Switch | Means |
|---|---|
| `CIRCLEFIT_REQUIRE_OPENCL=1` | OpenCL must work here; fail rather than skip. What `ci-gpu-compile.yml` sets while running on PoCL on purpose. |
| `CIRCLEFIT_REQUIRE_GPU_DEVICE=1` | The selected device must additionally be of type GPU. |

Set both whenever a run is meant to be GPU validation:

```sh
CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
    go test -tags gpu -count=1 ./internal/fit/renderer/... -run '^TestOpenCL'
```

`TestOpenCLDeviceReportsAPreparedDevice` logs the platform, device, driver
version and compute-unit count either way, so its output is what to paste into a
measurement record.

## Example commands

A GPU-tagged binary, joint mode — the pipeline the GPU actually wins:

```sh
./circlefit run --ref assets/test.png --out out.png \
  --mode joint --backend opencl --circles 50 --iters 200 --seed 42
```

The staged modes work and hold the same parity budget, but they are much slower
than the CPU on real hardware; see [When to use the GPU](#when-to-use-the-gpu).

Run it, but do not fail if the device is missing:

```sh
./circlefit run --ref assets/test.png --backend opencl --backend-fallback cpu ...
```

`--backend-fallback` accepts only `cpu` and is unset by default, so an
unavailable backend fails the run unless you say otherwise. When it fires, a
warning names the reason and the run's own output names the backend that
actually ran:

```
Backend: cpu (requested opencl, unavailable) - this cost is not comparable with opencl runs
```

A run whose device failed part-way prints the other form,
`Backend: opencl (degraded to CPU mid-run)`. A server job records the same two
facts as `effectiveBackend` and `backendDegraded`; a one-shot CLI run has no job
resource, so this line is the record. A clean run prints nothing extra.

Serve with OpenCL as the default backend for jobs that name none:

```sh
./circlefit serve --backend opencl --addr localhost:8080
```

A build without the tag refuses this at startup rather than failing every job
later. Ask a running server what it can actually do:

```sh
curl -s localhost:8080/api/v1/system | jq .supportedBackends
```

A portable build answers `["cpu"]`; a GPU build answers `["cpu","opencl"]`.
Submit a job against it:

```sh
curl -s -X POST localhost:8080/api/v1/jobs -H 'Content-Type: application/json' \
  --data '{"refPath":"assets/test.png","mode":"joint","backend":"opencl",
           "backendFallback":"cpu","circles":50,"iters":200,"seed":42}'
```

Then read `effectiveBackend` and `backendDegraded` back off the job — not the
`backend` you asked for — before comparing its cost with anything.

Measure the device on your own hardware, as separate passes rather than
`-count=N` (see [the quirks below](#device-and-driver-quirks-found-during-validation)).
The benchmark reads both switches itself, so it fails rather than measuring a
CPU OpenCL device — a `-run '^$'` invocation executes no test, which would
otherwise leave `CIRCLEFIT_REQUIRE_GPU_DEVICE` inert exactly where a number is
being taken:

```sh
for i in $(seq 8); do
  CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
    go test -tags gpu -run '^$' -bench '^BenchmarkRendererBackendMatrix$' \
    -benchmem -benchtime=500ms -count=1 ./internal/fit/renderer
done
```

## When to use the GPU

Measured on an NVIDIA T550; see
[`gpu-performance-report.md`](gpu-performance-report.md) for the tables, the
separation criterion, and the conditions. The shapes below are what generalizes;
the ratios are one device on one contended host.

| Workload | Use | Why |
|---|---|---|
| Joint mode, any canvas from 256² up | **GPU** | The objective evaluation is 6-14x faster, and separated at every measured cell from 256² upward except K=1 at 256². |
| Joint mode at 64² | Either | The GPU leads, but by too little to separate from noise. Not worth a build-tag dependency on its own. |
| Sequential or batch mode, canvas 512² | **GPU** | Task 11.13 tranche 2 gave staged sessions an accumulated canvas, so a stage composites its new circles onto the retained one instead of replaying it. One such evaluation is flat in retained depth at 70-74 µs across a 64-fold depth change, 2.5-4.8x faster than the CPU's accumulated canvas and separated at every measured depth, and up to 22x faster than the replay it replaced. |
| Sequential or batch mode at 128² | Either | Both accumulated arms sit at 25-43 µs and nothing separates: the canvas is small enough that the GPU is bounded by launch latency rather than by work. |
| An image materialized on every evaluation, at K=1 on a large canvas | **CPU** | The readback is a fixed per-pixel cost the GPU pays regardless of circle count — 5.9 ms at 1024², more than three times a complete evaluation there — while the CPU composites only what one circle covers. This is the only regime where the CPU is separated ahead. |
| The same, from K=50 up | **GPU** | Circle count buys the readback back: 1024² runs 0.6x, 1.1x, 2.1x, 8.3x as K goes 1, 10, 50, 100. |
| A job-level custom base canvas (`canvasPath`) | **CPU** | OpenCL has no constructor for one; the job is refused. This is *not* the same thing as the accumulated staged canvas above, which is internal to a run and works on both backends. |
| Anything whose cost you will compare against a recorded CPU figure | **CPU** | The two backends do not produce the same number. See below. |

Two constraints that are not about speed:

- **A GPU cost is not a CPU cost.** The device computes in float32 end to end
  against a float64 CPU path, so parity is a budget — ±2 per channel and 1%
  relative cost — and the cost bound grows with canvas size because the SSD
  accumulates in float32. Within one run the numbers are consistent; across
  backends they are not a baseline for each other.
- **Read `effectiveBackend` and `backendDegraded`, not the label.** A run whose
  device failed mid-way continues on the CPU and its best-so-far spans two
  arithmetics. The backend you asked for says nothing about what ran.

In practice the honest summary today is: OpenCL is worth it on a canvas of 256²
or larger, for joint mode and — since tranche 2 — for the staged modes too,
where at 512² it is the faster backend at every retained depth measured. Below
that the canvas is too small for the device to separate from the CPU on
anything but joint.

## macOS has no GPU backend, by decision

Earlier revisions of this document framed macOS as a gap to be closed by a Metal
or WebGPU backend. It is now a decision: **CircleFit has no GPU backend on
macOS, and none is planned.** The CPU renderer is the supported path there, and
[`support-matrix.md`](support-matrix.md) lists macOS OpenCL as Unsupported
rather than Experimental.

What settles it:

- Apple deprecated OpenCL, and Apple Silicon ships no OpenCL implementation at
  all without a third-party ICD. There is nothing to target.
- A Metal backend would be a third renderer implementation — its own kernels,
  its own float32 parity budget to measure against the float64 CPU path, its own
  CI runner — for a platform where no measurement exists to say the GPU would
  win. WebGPU would additionally mean shipping Dawn or wgpu native libraries.
- Joint mode and, since tranche 2, the staged modes are measured ahead of the
  CPU — on one device, at one canvas size. Porting that to a second API buys a
  second copy of a question this project has answered for exactly one GPU.

Revisit only when both hold: Task 11.13 has made the staged path competitive on
hardware that already exists, and there is an Apple Silicon runner able to gate
parity. The first is now true rather than arguable — tranche 1 removed the
26-84x loss and tranche 2 put the staged modes ahead of the CPU at 512² — so the
decision rests on the second alone. That one has not moved at all: it means a CI runner this
project does not have, gating a float32 parity budget against the float64 CPU
path, for a third renderer implementation.

## Device and driver quirks found during validation

Things that cost time on the T550 and will cost it again on the next device:

- **A CPU device passes every parity test.** `InitOpenCL` falls back to one, so
  a machine with only PoCL installed validates nothing about a GPU while
  reporting complete success. `CIRCLEFIT_REQUIRE_GPU_DEVICE=1` is the guard, and
  both the parity suite and the benchmarks read it — the benchmarks separately,
  because a benchmark invocation runs no test. Set it, and record the device name
  the run logs.
- **A degraded renderer benchmarks as a GPU, and passes a parity test.** `Cost`
  and `Render` have no error return, so a device error leaves the renderer
  answering silently from its CPU fallback. A benchmark then publishes CPU
  timings under a GPU label, and a parity assertion compares the CPU oracle
  against the CPU fallback and passes while exercising no device at all. The
  matrix benchmark asserts `Degraded()` is false before *and* after its measured
  loop, and `newOpenCLTestRenderer` fails any test whose renderer ended up
  degraded. Any GPU measurement taken without that assertion is unreliable.
- **A laptop GPU idles down between dispatches.** The T550 sat at P8/300 MHz
  between benchmark cells. Absolute times on such a host are depressed and are
  not comparable with anything taken elsewhere.
- **`-count=N` is the wrong way to sample on a machine you are also using.** Go
  runs a cell's repetitions back to back, so one burst of background load
  corrupts every sample of one cell and none of its neighbour. A `-count=6`
  attempt produced 50-320% spreads and orderings that are impossible for this
  workload — K=50 slower than K=100, K=1 slower than K=10. Eight separate
  passes of the whole matrix removed the inversions. Use separate passes.
- **The renderer's single-slot parameter cache will answer a benchmark.**
  Repeated identical evaluations are served from the cached device result, which
  measures nothing. Perturb a coordinate per iteration.
- **A long kernel can trip the display driver watchdog** on a card that is also
  driving a desktop, usually surfacing as `CL_OUT_OF_RESOURCES` or
  `CL_DEVICE_NOT_AVAILABLE`. [`troubleshooting.md`](troubleshooting.md) has the
  rest of the mid-run failure catalog.

## Why this is still experimental

Parity and throughput are established on **one** vendor GPU. AMD and Intel are
unmeasured for both, and there is no required real-device CI runner — the GPU
gate runs PoCL on a CPU. Either would be enough on its own.

This verdict used to rest on a fourth reason, that the staged pipelines were
26-84x slower than the renderer they are supposed to accelerate. Task 11.13
tranche 1 removed it: they now measure within noise of the CPU. That changes
what "experimental" means here rather than whether it applies — the remaining
reasons are about coverage, not speed, and no amount of optimization answers
them. OpenCL stays experimental until a second vendor has been measured and a
real device gates CI.

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
- Works on NVIDIA, AMD, and Intel GPUs out of the box; macOS falls back to the CPU renderer. That fallback was written here as a temporary state pending Metal or WebGPU; it is now the decision, and [macOS has no GPU backend, by decision](#macos-has-no-gpu-backend-by-decision) records why.

The OpenGL fragment-shader fallback proposed here for macOS and integrated GPUs was never built and is not planned. The reasoning is the same as for Metal: a second GPU implementation would need its own kernels, its own parity budget and its own runner, and the one OpenCL pipeline that currently beats the CPU is joint mode.

## Implementation Status
- `internal/fit/renderer/backend.go` centralises backend selection and normalises CLI input.
- `internal/fit/gpu/opencl_runtime_*.go` enumerates platforms/devices and bootstraps an OpenCL context (GPU preferred, CPU fallback) when built with `-tags gpu`; non-GPU builds return a helpful error.
- `internal/fit/renderer/opencl/renderer_gpu.go` implements rendering and cost evaluation in OpenCL with CPU degradation on runtime errors. It lives in its own package because Go forbids Plan 9 assembly in a package that uses cgo, and `internal/fit/renderer` carries the SIMD kernels; `internal/fit/renderer/renderer_opencl_gpu.go` is the gpu-tagged adapter that injects the CPU fallback and supplies the unexported session hook. Cost reduction is entirely on-device: the host reads only the final float rather than a full pixel-error buffer.
- Cost and image caching are separate. `Cost` leaves the rendered output resident; `Render` reads the full image only when requested and can reuse output from a matching cost evaluation without dispatching the kernels again.
- The kernel quantizes composited channels to NRGBA semantics before scoring, so the reduced cost describes the image returned by `Render`. CPU/OpenCL parity tests allow a 1% cost tolerance and two channel values for float32 geometry and edge-coverage differences.
- CLI exposes `--backend` (default `cpu`) and reports the selected backend during runs. GPU mode renders and scores joint, sequential, and batch pipelines via OpenCL when compiled with `-tags gpu`.
- Sequential and batch optimization create same-backend OpenCL sessions as the active circle count grows. A session shares its parent renderer's device engine — the runtime, the context and command queue, the compiled program and the reference buffer — and allocates only its own kernel pair and the buffers its circle count needs; that is Task 11.13 tranche 1. These modes still replay retained parameters instead of accumulating a device-side base canvas, which preserves pipeline semantics and is what tranche 2 addresses. Before that sharing they measured 26x and 84x slower than the CPU renderer; they now run 83.8x and 85.9x faster than they did and neither separates from it. See [`gpu-performance-report.md`](gpu-performance-report.md).

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
the staged OpenCL path *as it stood then* was not suitable for PoCL: each stage
constructed an independent runtime and program and replayed retained circles,
whereas the CPU renderer accumulated a base canvas. Both of those are now fixed
-- Task 11.13 tranche 1 shares the engine and tranche 2 accumulates the canvas
-- so this row describes a code path that no longer exists as well as a device
that is not a GPU. It is kept as a dated record and must not be re-measured
against.

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
