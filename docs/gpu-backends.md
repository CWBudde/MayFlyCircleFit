# GPU Backend Research (Task 11.1 Draft)

## Baseline Constraints
- The renderer contract lives in `internal/fit/renderer.go` and expects `Render`, `Cost`, and `Reference`.
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
- **Approach:** One kernel composites circles into an image buffer, second kernel (or same with atomics) performs per-pixel SSD reduction. Keeps reference image resident on device memory.
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

## Immediate Next Steps
1. Draft `internal/fit/gpu` package layout (`context.go`, `opencl_renderer.go`, `kernels/`).
2. Author initial OpenCL kernels: circle compositing (float32 RGBA) and SSD reduction (local reductions + final sum on host).
3. Wire renderer constructor (`internal/fit/renderer_opencl_gpu.go`) to the runtime: keep context alive, hydrate parameter buffers, return a functional `Renderer`.
4. Expand CLI/server plumbing so `--backend opencl` flows through pipelines; add graceful fallback messaging.
5. Seed benchmarks (`benchmarks/gpu_render_test.go`) to compare CPU SIMD vs OpenCL for 256×256 and 512×512 cases.
6. Capture investigation notes in this document as kernels mature (driver quirks, tuning parameters, fallbacks).

## Scaffolding Status
- `internal/fit/backend.go` centralises backend selection and normalises CLI input.
- `internal/fit/gpu/opencl_runtime_*.go` enumerates platforms/devices and bootstraps an OpenCL context (GPU preferred, CPU fallback) when built with `-tags gpu`; non-GPU builds return a helpful error.
- CLI exposes `--backend` (default `cpu`) and reports the selected backend during runs. GPU mode currently stops after context init until kernels land.
