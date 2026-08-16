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

## Early stopping

Two independent mechanisms exist. They use different units and must not be
confused.

| Mechanism | Flags | Unit | Improvement | Modes | Default |
| --- | --- | --- | --- | --- | --- |
| Stage-level convergence | `--convergence`, `--patience`, `--threshold` | circles/batches | relative ratio | Sequential, batch | Enabled |
| Optimizer-level stopping | `--stop-target-cost`, `--stop-stagnation-iters`, `--stop-min-improvement`, `--stop-min-iters` | iterations | absolute cost | Joint, sequential, batch | Disabled |

Joint mode ignores stage-level convergence. In staged modes, optimizer-level
stopping applies to each stage independently, so it can shorten stages without
ending the run; the run then reports `completed`. Reported termination reasons
are `completed`, `cancelled`, `target_cost`, `stagnation`, and
`stage_convergence`. MayFly evaluation parallelism (`EnableParallel`) is opt-in
through `--parallel-evaluation`, sized by `--evaluation-workers`, and off by
default. It requires a backend that can hand out independent renderer sessions,
so it applies to the CPU backend only; OpenCL declines it with a warning and
evaluates serially. Transactional polishing always evaluates serially and
rejects a parallel optimizer.

## Build targets

The portable CI matrix is defined with `CGO_ENABLED=0`, which intentionally
excludes OpenCL.

| Target | CPU build path | SIMD path | CI cross-build gate | Native SSD gate |
| --- | --- | --- | --- | --- |
| Linux/AMD64 | Available | AVX2, then SSE2, then scalar, with runtime detection | Configured | AVX2, SSE2, and scalar required |
| Linux/ARM64 | Available | NEON with runtime detection; scalar fallback | Configured | NEON and scalar required |
| macOS/AMD64 | Available | AVX2, then SSE2, then scalar, with runtime detection | Configured | Not configured |
| macOS/ARM64 | Available | NEON with runtime detection; scalar fallback | Configured | NEON and scalar required |
| Windows/AMD64 | Available | AVX2, then SSE2, then scalar, with runtime detection | Configured | AVX2, SSE2, and scalar required |
| Linux/386 | Portability only | Scalar | Configured | Not configured |

“Configured” means the CI workflows contain that gate. Each gate lives in its
own reusable workflow, `.github/workflows/ci-<concern>.yml`, called from the
`ci.yml` orchestrator; the cross-build gate is `ci-cross-build.yml` and the
native SSD gate is `ci-native-simd.yml`. Cross-build jobs also
assert that Go selected the expected SSD Go and assembly files. Native gates
run the SSD correctness suite on the stated architecture and fail if runtime
dispatch does not select the required SIMD backend. Check the CI run
before treating a commit as verified. Linux/386 is not a release artifact;
other Go targets may compile but are not claimed as supported until they are
added to the matrix and exercised.

The native gate distinguishes two things that were previously conflated.
`MAYFLY_REQUIRE_SIMD_TIER` asserts which tier detection selected and never sets
one; `MAYFLY_SIMD_TIER` pins a tier and is therefore useless as an assertion
target. Each step pairs exactly one of them with the assertion.

The AMD64 native gate runs five steps: natively for AVX2, under
`GODEBUG=cpu.all=off` asserting that feature masking demotes to SSE2, with the
SSE2 tier pinned, with the scalar tier pinned, and once through the legacy
`MAYFLY_DISABLE_SIMD=1` alias. `GODEBUG` cannot mask SSE2 on AMD64 because
`golang.org/x/sys/cpu` marks it as required there, so pinning is the only way to
reach the scalar tier on that architecture. The ARM64 gates run natively for
NEON, under `GODEBUG=cpu.all=off` for scalar, with the scalar tier pinned, and
through the legacy alias; they have no middle tier to pin. SAD has no SSE2
kernel, so it stays scalar on AMD64 hosts without AVX2, as it already does on
ARM64.

The AMD64 steps additionally run `./internal/fit/renderer`, which asserts the
same tier for the renderer's own kernels. The ARM64 steps do not, for the
pre-existing reason recorded in `docs/known-limitations.md`.

On ARM64, opaque CPU-renderer spans additionally have an ASIMD-gated NEON
compositor for spans of at least 256 pixels, with an exact scalar span and
remainder path. Shorter spans use scalar because native Apple M5 measurements
show it is faster there. Translucent custom canvases always retain the general
scalar Porter-Duff path. This renderer kernel is natively validated on macOS
ARM64 but is not currently a required Linux/ARM64 timing gate.

## OpenCL

OpenCL is an experimental, opt-in renderer for all optimization modes:

- build with `-tags gpu` and `CGO_ENABLED=1`;
- the cgo renderer lives in `internal/fit/renderer/opencl`, kept apart from the
  assembly kernels in `internal/fit/renderer` because Go forbids Plan 9 assembly
  in a package that uses cgo;
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
| Production/security toolchain | A currently supported patched Go release; vulnerability CI uses 1.26.6 |
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
