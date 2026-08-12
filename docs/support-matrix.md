# Support matrix

This matrix describes code paths that exist in the repository. It does not
certify a release or substitute for the CI result for a particular revision.

## Optimization modes

| Backend | Joint | Sequential | Batch | Custom canvas |
| --- | --- | --- | --- | --- |
| CPU | Supported | Supported | Supported | Supported |
| OpenCL (`gpu` tag) | Experimental | Experimental | Experimental | Unsupported |

Sequential and batch are staged pipelines. OpenCL creates independent
same-backend sessions and replays retained circles at each stage; it does not
silently replace the staged pipeline with CPU. A backend that cannot create
staged sessions returns `ErrStagedOptimizationUnsupported`. Batch mode accepts a
requested total and batch size, including a smaller final batch, so result
cardinality matches the total.

## Build targets

The portable CI matrix is defined with `CGO_ENABLED=0`, which intentionally
excludes OpenCL.

| Target | CPU build path | SIMD path | CI cross-build gate | Native SSD gate |
| --- | --- | --- | --- | --- |
| Linux/AMD64 | Available | AVX2 with runtime detection; scalar fallback | Configured | AVX2 required |
| Linux/ARM64 | Available | NEON with runtime detection; scalar fallback | Configured | NEON required |
| macOS/AMD64 | Available | AVX2 with runtime detection; scalar fallback | Configured | Not configured |
| macOS/ARM64 | Available | NEON with runtime detection; scalar fallback | Configured | NEON required |
| Windows/AMD64 | Available | AVX2 with runtime detection; scalar fallback | Configured | AVX2 required |
| Linux/386 | Portability only | Scalar | Configured | Not configured |

“Configured” means the workflow contains that gate. Cross-build jobs also
assert that Go selected the expected SSD Go and assembly files. Native gates
run the SSD correctness suite on the stated architecture and fail if runtime
dispatch does not select the required SIMD backend. Check the workflow run
before treating a commit as verified. Linux/386 is not a release artifact;
other Go targets may compile but are not claimed as supported until they are
added to the matrix and exercised.

On ARM64, opaque CPU-renderer spans additionally have an ASIMD-gated NEON
compositor for spans of at least 256 pixels, with an exact scalar span and
remainder path. Shorter spans use scalar because native Apple M5 measurements
show it is faster there. Translucent custom canvases always retain the general
scalar Porter-Duff path. This renderer kernel is natively validated on macOS
ARM64 but is not currently a required Linux/ARM64 timing gate.

## OpenCL

OpenCL is an experimental, opt-in renderer for all optimization modes:

- build with `-tags gpu` and `CGO_ENABLED=1`;
- install platform-specific OpenCL headers, loader, driver, and runtime;
- expect device/driver-specific behavior;
- validate CPU/OpenCL parity on the actual target device.

The ordinary and cross-build CI jobs intentionally exclude OpenCL. A separate
Ubuntu job installs the OpenCL headers and PoCL CPU runtime, verifies platform
discovery, compiles all GPU-tagged packages, and gates focused OpenCL runtime
tests. PoCL covers kernel correctness and lifecycle behavior deterministically;
there is still no required real-device GPU runner, vendor-driver compatibility
gate, or GPU performance threshold. Runtime OpenCL failures may cause the
experimental renderer to degrade individual rendering/cost work to its CPU
compatibility path and emit a warning; callers must not interpret the backend
label alone as proof that every evaluation ran on the GPU.

## Server deployment

| Deployment | Status | Boundary |
| --- | --- | --- |
| Loopback on a trusted workstation | Intended use | Same-origin browser policy, configured input roots, bounded queue |
| Trusted LAN | Not supported by default | No authentication or TLS |
| Public internet / multi-tenant | Unsupported | Requires an external security architecture not present here |

The default server address is `localhost`. pprof is disabled unless
`--enable-pprof` is supplied, and that flag is rejected for non-loopback bind
addresses. `--input-root` is repeatable; both reference and canvas paths are
canonicalized and checked against those roots.

## Toolchain and dependency baseline

| Component | Baseline |
| --- | --- |
| Go source compatibility | 1.24 or newer |
| Production/security toolchain | A currently supported patched Go release; vulnerability CI uses 1.26.5 |
| templ | `v0.3.960`, pinned Go tool; generated Go committed |
| MayFly | `v0.4.0` |
| govulncheck in CI | `v1.1.4`, installed at an explicit version |
| staticcheck in CI | `v0.6.1`, installed at an explicit version |

CI also enforces an aggregate statement-coverage floor of 50% and uploads the
coverage profile. The initial aggregate threshold is deliberately moderate
because generated UI and OS/device integration boundaries dilute package-level
coverage; it is a floor to raise as focused tests expand, not a target ceiling.

See [known limitations](known-limitations.md) for the constraints behind these
support statements.
