# Documentation index

Everything in `docs/`, grouped by what you came here to do. Start from
[`README.md`](../README.md) for installation and first runs, and from
[`AGENTS.md`](../AGENTS.md) for working conventions.

## Using it

- [`browser-support.md`](browser-support.md) — supported browser engines and
  viewport sizes, what `ci-web` enforces, and the manual Safari/VoiceOver
  checklist for the parts it cannot.
- [`support-matrix.md`](support-matrix.md) — supported platforms, backends,
  build targets, CI gates, and the toolchain baseline.
- [`known-limitations.md`](known-limitations.md) — current operational
  constraints. Read before an unattended or long run.
- [`troubleshooting.md`](troubleshooting.md) — CLI exit statuses, the JSON API
  error envelope, and what to do when a run or a request fails.
- [`releasing.md`](releasing.md) — local packaging, artifacts, and the tag-gated
  release job.
- [`gpu-backends.md`](gpu-backends.md) — the experimental OpenCL renderer:
  setup, fallbacks, and hardware assumptions.
- [`cpu-rendering-threads.md`](cpu-rendering-threads.md) — how many render
  threads to give a workload, and when one is right.
- [`schedule-format.md`](schedule-format.md) — the declarative campaign
  document, a worked example, the measured growth recipe, and when two
  campaigns' costs are comparable.
- [`benchmarks.md`](benchmarks.md) — the CPU benchmark suite: workloads, how to
  run them, how to compare revisions, and CI regression reporting.

## Output and reporting

- [`html-reports.md`](html-reports.md) — the self-contained run report.
- [`difference-heatmaps.md`](difference-heatmaps.md) — visualizing residual
  error against the reference.
- [`advanced-quality-metrics.md`](advanced-quality-metrics.md) — quality
  measures beyond the optimized MSE objective.

## How it works

- [`architecture.md`](architecture.md) — package ownership and the CLI, server,
  renderer, persistence, schedule, and web-UI data flows.
- [`behavior-invariants.md`](behavior-invariants.md) — observable behavior that
  must stay explicit: backends, SIMD dispatch, parallel evaluation, polishing,
  determinism, early stopping, and the server trust boundary.
- [`rendering-internals.md`](rendering-internals.md) — the rendering hot path as
  implemented: canvas invariants, span geometry, SSD kernels, tier dispatch, and
  span compositing. **The reference for anyone touching renderer math.**
- [`renderer-correctness.md`](renderer-correctness.md) — the byte-exact parity
  contract, its oracle and matrix, and the standing tradeoffs.
- [`exact-span-compositors.md`](exact-span-compositors.md) — the exact vector
  span kernels, their parity requirements, and why `--fast-compositing` exists.
- [`incremental-cost.md`](incremental-cost.md) — the dirty-span cost contract,
  its selection rule, and measured dirty coverage.
- [`checkpoint-resume-test-results.md`](checkpoint-resume-test-results.md) —
  what "resume" actually restores, and what it does not.

## Measured reports

Timings in these are machine-specific. Do not compare a figure across hosts;
re-measure instead.

**Rendering and evaluation**

- [`cpu-performance-history.md`](cpu-performance-history.md) — the measured
  milestones of the CPU path, from the pre-optimization renderer through the
  SIMD tiers and span compositing.
- [`parallel-evaluation-report.md`](parallel-evaluation-report.md) — population
  evaluation parallelism.
- [`single-circle-extend-report.md`](single-circle-extend-report.md) — starting
  a batch from a verified rendered prefix.

**Polishing**

- [`polishing-throughput-report.md`](polishing-throughput-report.md) — session
  pool and active-set performance tradeoffs.
- [`polishing-budget-report.md`](polishing-budget-report.md) — how much budget
  polishing deserves.
- [`contiguous-window-polish-report.md`](contiguous-window-polish-report.md) —
  the windowed active-set polish and its crossover.

**Search quality**

- [`cmaes-preliminary-report.md`](cmaes-preliminary-report.md) — the stopped
  one-block CMA-ES campaign: descriptive costs and metric/adaptation traces,
  explicitly without the planned twelve-block inference.
- [`restart-vs-budget-report.md`](restart-vs-budget-report.md) — why a stage's
  budget is better spent as several cold runs than one long one, and which
  interventions did *not* delay population collapse. Read before proposing a
  search-quality change.
- [`seed-variance-and-population-report.md`](seed-variance-and-population-report.md)
  — why base-stage quality does not predict the fit built on it, and what the
  population knob does and does not buy.
- [`aoblmoa-paper-fidelity-report.md`](aoblmoa-paper-fidelity-report.md) — the
  paper-faithful `aoblmoa` measured against `standard`.
- [`dragonfly-poc-report.md`](dragonfly-poc-report.md) — the proof-of-concept
  Dragonfly adapter measured against MayFly `standard`.
- [`qmc-initial-population-report.md`](qmc-initial-population-report.md) —
  `qmcInit` measured at three population sizes; a null result, with the raw
  [`qmc-initial-population-screen.csv`](qmc-initial-population-screen.csv)
  beside it.

## Decisions and history

- [`rejected-optimizations.md`](rejected-optimizations.md) — what was built,
  measured, and deliberately not shipped. **Check it before proposing a
  rendering optimization.**
- [`simd-design.md`](simd-design.md) — why the kernels are hand-written Plan 9
  assembly, and what the original design got wrong.
- [`typescript-read-model-generation.md`](typescript-read-model-generation.md) —
  why the `web/src` read models are not generated from the Go structs, measured
  against `tygo`, and which parity tests are the contract instead. **Read before
  proposing Go→TypeScript codegen.**

## Other directories

- `examples/` — a runnable campaign document, referenced from
  [`schedule-format.md`](schedule-format.md) and exercised by tests.
- `images/`, `profiles/` — figures and self-contained flamegraph captures for
  [`cpu-performance-history.md`](cpu-performance-history.md).
