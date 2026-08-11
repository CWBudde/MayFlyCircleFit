# Support matrix

This matrix describes code paths that exist in the repository. It does not
certify a release or substitute for the CI result for a particular revision.

## Optimization modes

| Backend | Joint | Sequential | Batch | Custom canvas |
| --- | --- | --- | --- | --- |
| CPU | Supported | Supported | Supported | Supported |
| OpenCL (`gpu` tag) | Experimental | Unsupported | Unsupported | Unsupported |

Sequential and batch are staged pipelines. A backend that cannot create staged
sessions returns `ErrStagedOptimizationUnsupported`; the pipeline does not
silently replace it with CPU. Batch mode accepts a requested total and batch
size, including a smaller final batch, so result cardinality matches the total.

## Build targets

The portable CI matrix is defined with `CGO_ENABLED=0`, which intentionally
excludes OpenCL.

| Target | CPU build path | SIMD path | CI cross-build gate |
| --- | --- | --- | --- |
| Linux/AMD64 | Available | AVX2 with runtime detection; scalar fallback | Configured |
| Linux/ARM64 | Available | Scalar fallback; no NEON kernel | Configured |
| macOS/AMD64 | Available | AVX2 with runtime detection; scalar fallback | Configured |
| macOS/ARM64 | Available | Scalar fallback; no NEON kernel | Configured |
| Windows/AMD64 | Available | AVX2 with runtime detection; scalar fallback | Configured |

“Configured” means the workflow contains that gate. Check the workflow run
before treating a commit as verified. Other Go targets may compile but are not
claimed as supported until they are added to the matrix and exercised.

## OpenCL

OpenCL is an experimental, opt-in joint renderer:

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
| MayFly | `v0.3.0` |
| govulncheck in CI | `v1.1.4`, installed at an explicit version |
| staticcheck in CI | `v0.6.1`, installed at an explicit version |

CI also enforces an aggregate statement-coverage floor of 50% and uploads the
coverage profile. The initial aggregate threshold is deliberately moderate
because generated UI and OS/device integration boundaries dilute package-level
coverage; it is a floor to raise as focused tests expand, not a target ceiling.

See [known limitations](known-limitations.md) for the constraints behind these
support statements.
