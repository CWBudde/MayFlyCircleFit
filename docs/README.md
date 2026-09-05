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
  requirements and per-platform setup, example commands, which workloads belong
  on the GPU and which do not, why macOS has no GPU backend, the device quirks
  that make a GPU measurement wrong, and the design record behind the choice of
  OpenCL.
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
- [`renderer-precision-measurements.md`](renderer-precision-measurements.md) —
  the boundary half of that contract, measured on amd64 and on ARM64 under
  emulation at every SIMD tier: fractional and tangent boundaries, radius
  extremes, clipping, batch boundaries, randomized circles, and row sharding.
  Also the one rasterization rule the matrix caught that nobody had written
  down. **No timing in it, and none may be inferred from it.**
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
- [`fixed-point-geometry-formats.md`](fixed-point-geometry-formats.md) — signed
  Q24.8 and normalized Q8.24 span geometry measured against production Q16.16
  on range, adversarial boundaries, full renders, and throughput. Q16.16 stays;
  read before proposing another fixed-point format.
- [`gpu-performance-report.md`](gpu-performance-report.md) — the OpenCL
  renderer measured on an NVIDIA GPU: the circle-count and image-size matrix,
  the transfer boundaries, and the staged pipelines before and after a renderer
  and its sessions began sharing one device engine. The evaluation path wins
  by 6-14x; the image readback is what costs. It also closes the batched
  objective question by measurement: a pipelined generation is 1.1-1.4x
  *slower*, and the launch floor bounds any batching scheme at 1.58x, so read it
  before proposing one. **Supersedes every PoCL timing in `gpu-backends.md`.**

**Polishing**

- [`polishing-throughput-report.md`](polishing-throughput-report.md) — session
  pool and active-set performance tradeoffs.
- [`polishing-budget-report.md`](polishing-budget-report.md) — how much budget
  polishing deserves.
- [`contiguous-window-polish-report.md`](contiguous-window-polish-report.md) —
  the windowed active-set polish and its crossover, plus the dirty-region
  evaluator's end-to-end check on a committed 2,111-circle fixture. Cost parity
  holds to the last bit, but the evaluator scores no candidate at all under the
  default `replacement` strategy, because merit selection ranks the
  canvas-spanning circles weakest. Read before claiming the 3.1x per-candidate
  figure for a real job, and for the measurement that the 5% fallback gate is
  too low at 2,111 circles.

**Search quality**

- [`cmaes-report.md`](cmaes-report.md) — the complete twelve-block CMA-ES
  campaign. Separable CMA-ES with IPOP restarts wins 12/12 blocks against
  MayFly's long run and 11/12 against its restart arm, while both IPOP arms
  spend about 40% of their budget after their last improvement. Read before
  proposing a CMA-ES default, and for why a large recorded sigma is not evidence
  of a diverged search. The `lambda` and covariance questions it leaves open are
  answered by `cmaes-lambda-report.md`.
- [`cmaes-lambda-report.md`](cmaes-lambda-report.md) — the twelve-block
  `lambda` x covariance screen, eight arms and ninety-six jobs. All thirteen
  paired contrasts retain their null under Holm: `lambda` has no measured effect
  on the mean at 20, 64 or 1024, and separable covariance without restarts is a
  null against full. Read before proposing a `lambda` default, before lowering
  `app.MinPopulation`, and for the measurement that retires the sigma question —
  `sigma * max(D)` never exceeds 1.52 while sigma spans fifty-two orders of
  magnitude. Also records that thirty-six cells reproduce Phase 21 bit for bit.
- [`cmaes-stagnation-report.md`](cmaes-stagnation-report.md) — the
  window-selection pilot and the twelve-block campaign that tested it. **Arming
  a stagnation criterion on a CMA-ES restart schedule does not improve the
  fit**: both registered contrasts retain their null under Holm, the primary at
  `t = -0.34` with 6/12 blocks won. The criterion fires as designed and buys
  almost nothing — at `lambda` 1024 it adds no restarts at all, and at
  `lambda` 20 it *raised* the wasted share. Read before proposing a default,
  and for a worked case of a three-block pilot whose mechanism finding did not
  replicate.
- [`cmaes-budget-split-report.md`](cmaes-budget-split-report.md) — six arms and
  seventy-two jobs on a **second fixture**, a photograph at twelve circles.
  Phase 21's headline reproduces (`+36.36`, `t = +5.23`, 11/12, rejects under
  Holm), so it is not specific to one image — but **separable CMA-ES run as one
  long search is indistinguishable from `mayfly-r16`** (`p = 0.20`). Splitting
  the budget is what wins, not the engine. Read before proposing a default
  change, for why the IPOP ladder is budget-capped at two or three runs and
  returns the unsplit arm's exact cost in half the blocks, and for the
  registration discrepancy that leaves the epoch-versus-restart question open.
- [`cmaes-restart-ladder-report.md`](cmaes-restart-ladder-report.md) — seven
  arms and eighty-four jobs asking how many independent basins a fixed budget
  can buy. **Both registered contrasts retain**: trading population for cold
  restarts does not beat the incumbent IPOP schedule (`t = -0.26`), and BIPOP
  does not beat IPOP at a matched criterion (`t = +0.76`). The record was
  matched, not beaten. Read it for why the four bit-identical `lambda` 1024
  cells prove less than they appear to: **they are one deterministic trajectory
  shared by four arms**, which reaches the record before any restart shape
  separates them. The surviving claim is one-sided — that one `lambda` 1024
  trajectory found the basin where 8, 32 and 64 small-population draws did not,
  on a seed that is one of twelve. Read it also before designing another
  restart ladder, because this one's arms spent only 29-44% of their cap and a
  fixed `optimizerRestarts` count cannot express "restart until the budget is
  gone". Carries the first `bipop` data in the repository, and the twenty-four
  cells that reproduce `cmaes-stagnation-report.md` bit for bit.
- [`cmaes-deep-hunt-report.md`](cmaes-deep-hunt-report.md) — nine arms and 89
  jobs whose only purpose was to beat the recorded eight-circle cost, and which
  did: **752.5220 is superseded by 726.1984**, from `blk-ipop` — block
  covariance, IPOP, at a budget 1.94x the one every comparative campaign uses.
  The design is descriptive and registers no contrasts, so **nothing in it is a
  test and none of its costs is comparable to the other CMA-ES reports**. Read
  it for three things. Block covariance beats the separable control in 11/11
  blocks by a mean of 77 — the campaign's strongest lead, and the obvious next
  registered measurement. The lambda-4096 convergence question the restart
  ladder left open is discharged: at this budget 4096 converges in 32/45 runs
  while lambda 8192, the rung no earlier campaign reached, is truncated in
  33/33 — and it is the rung that set the record, still cut off by the cap. And
  a warm start from the old record beats it every time: eleven of them ended in
  a ~743 band, none of them near the new record. That says the warm start finds
  something better, not that the old record was a point on a slope — the start
  quantizes colours to eight bits and sigma 0.05 can leave a genuine local
  minimum. `activeCMA` is **not** answered — its arm is n = 1 because ten of its
  jobs were cancelled while queued.
- [`cmaes-covariance-report.md`](cmaes-covariance-report.md) — the registered
  test of the deep hunt's strongest lead, three arms and 36 jobs, all completed.
  **Block covariance beats separable and rejects under Holm** (`+39.12`,
  `t = +2.72`, 11/12) — about half the size the unregistered hunt observed, with
  a standard deviation larger than the mean, and one block reversing it by
  84.38. The mechanism is the ladder rather than the update: block covariance
  converges every rung up to lambda 4096 in 12/12 jobs and takes its block best
  from lambda 8192 in 7 blocks, where the separable control never does. Read it
  above all for the second contrast, which is **void rather than null**:
  `activeCMA` is arithmetically inert, and the covariance update memoryless,
  wherever `go-cma-es v0.1.0`'s rank-mu clamp binds — separable above lambda 256
  at 56 dimensions, block above 1024, full never. That covers every separable
  arm at the default popSize of 1024 and this campaign's own top rungs, though
  not the restart ladder's fixed-lambda arms at 32, 64 and 256. Fixed upstream
  in 0.2.0, which this repository has **not** taken.
- [`cmaes-active-cma-report.md`](cmaes-active-cma-report.md) — two arms and 24
  jobs that finally measure `activeCMA`, after the deep hunt lost its arm to
  cancelled jobs and the covariance campaign lost its contrast to the clamp.
  **The registered contrast retains** (`-23.79`, `t = -1.70`, `p = 0.117`,
  8/12 blocks favouring the knob) — but unlike the covariance campaign's void,
  every block separates, by up to 90.38 in both directions, so this is a
  measurement of the knob rather than of the clamp. It is absence of evidence
  and not a zero — the 95% paired interval runs from -6.98 to +54.56, so it
  admits a benefit twice the point estimate. Read it for two things beyond
  that. Its registered spend reading puts the arms inside the ladder's own
  5.5-point spread — 38.96% of the cap against 41.59% — but that yardstick is
  a range from another arm rather than a paired test, and the unregistered
  paired test on `finalEvaluations` is `t = 2.66` in 9/12 blocks, so read the
  spend question as open. Its by-product reads **block against
  separable at a rung where both modes are clean**, which no other campaign
  can: block leads by only `+7.27` (`t` = 0.54, 7/12) against the `+39.12` the
  covariance campaign registered against a clamped control. Cross-campaign and
  unregistered — a lead, not a finding — but answer it before proposing a
  covariance default.
- [`cmaes-covariance-clean-report.md`](cmaes-covariance-clean-report.md) — four
  arms and 96 jobs, the 2x2 that answers the lead the active-CMA campaign left
  open. **Block covariance does not beat separable where both modes are clean**:
  the registered primary is `-7.53` (`t = -0.82`, 9/24) and its 95% interval,
  `-26.55` to `+11.49`, **excludes the `+39.12`** the covariance campaign
  registered against a clamped control. All three registered contrasts retain.
  Read it before proposing a covariance default — this is the measurement that
  discharges that question, in the negative. Its interaction was registered so
  the clamp explanation would get a test rather than an eyeball, and came back
  inconclusive (`+15.11`, `-33.63` to `+63.84`), underpowered by about tenfold
  because the lambda 1024 arms are erratic; so the clamp remains the leading
  explanation on arithmetic grounds and gains no measured support here. Carries
  a `distributionExtent` reading that reproduces the lambda screen's sigma
  finding on fresh seeds, an unregistered lead that 32 small restarts beat 2
  large ones in separable mode (`+30.15`, `t = 3.38`), and the two driver
  defects it exposed — a registered contrast that was never printed, and a fixed
  restart count that recorded nothing about its attempts.
- [`cmaes-preliminary-report.md`](cmaes-preliminary-report.md) — the stopped
  one-block CMA-ES campaign: descriptive costs and metric/adaptation traces,
  explicitly without the planned twelve-block inference. Superseded by
  `cmaes-report.md`, whose twelve blocks contradict its collapse-and-freeze
  mechanism reading.
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
- [`frontend-spa-rewrite-decision.md`](frontend-spa-rewrite-decision.md) — why
  the frontend is React islands over templ rather than a Vite/shadcn single-page
  application, and the five constraints such a rewrite would have to pay for.
  **Read before proposing an SPA rewrite**; Tailwind and shadcn are already
  adoptable inside an island.
- [`typescript-read-model-generation.md`](typescript-read-model-generation.md) —
  why the `web/src` read models are not generated from the Go structs, measured
  against `tygo`, and which parity tests are the contract instead. **Read before
  proposing Go→TypeScript codegen.**

## Other directories

- `examples/` — a runnable campaign document, referenced from
  [`schedule-format.md`](schedule-format.md) and exercised by tests.
- `images/`, `profiles/` — figures and self-contained flamegraph captures for
  [`cpu-performance-history.md`](cpu-performance-history.md).
