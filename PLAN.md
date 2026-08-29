# CircleFit Implementation Plan

**This document is the open work only.** Completed phases are not summarized
here; what they decided and measured lives in [`docs/`](docs/README.md), their
tests, and git history. A completed marker in git records implementation for its
revision, not a fresh release-gate result.

> **Production-readiness caveat (2026-08-09 audit):** the remediation code,
> release-gating CI, and corrected documentation are present, but no gate counts
> as passed because its workflow exists. Task 14.13 removes this caveat.

## Rules for this plan

- Do not mark a check complete from its presence in code or CI configuration
  alone. Record the exact command or observed CI result for the revision.
- Performance claims carry host, workload, budget, and allocation counts.
- A measurement taken under a superseded dependency pin is not a baseline. See
  the Toolchain section of [`AGENTS.md`](AGENTS.md).
- Findings belong in `docs/`, not here. A task keeps only what is needed to
  decide and do it, plus a link.

---

## Open work, in priority order

| # | Work | Tasks |
| --- | --- | --- |
| 1 | Release gate (P0) | 14.13 |
| 2 | CMA-ES stagnation default (P1) | 23.1 |
| 3 | Search quality (P1) | 15.11 |
| 4 | Polishing evidence and defaults (P1/P2) | 15.7, 15.5, 15.10 |
| 5 | Dashboard sign-off (P1) | 17.11 |
| 6 | Server memory (P2) | 17.12 |
| 7 | Supporting documentation (P3) | 13.15 |
| 8 | Second CMA-ES fixture (P3) | 23.4 |
| 9 | Experimental backends and research (P3) | 11.13, 10.20, 15.3 |

---

## Phase 14: Production readiness 🚨 RELEASE GATE

### Task 14.13: Final release verification (P0)

- [ ] Observe every required CI gate — all supported cross-builds, generation,
      race, vulnerability, GPU compile, and core end-to-end — passing from a
      clean clone on two consecutive runs of the release candidate.
- [ ] Verify repository and release controls prevent producing or publishing a
      release while any required gate fails; document any administrator-enforced
      setting that cannot be expressed in workflow files.
- [ ] On a clean user machine, follow the README verbatim and complete a small
      CLI job and a server/UI job without workspace preparation.
- [ ] After those checks pass, remove the production-readiness caveat above and
      mark Phase 14 complete.

---

## Phase 23: CMA-ES restart behaviour

[`docs/cmaes-report.md`](docs/cmaes-report.md),
[`docs/cmaes-lambda-report.md`](docs/cmaes-lambda-report.md), and
[`docs/cmaes-stagnation-pilot-report.md`](docs/cmaes-stagnation-pilot-report.md)
are the evidence. Read them first; their numbers are not repeated here. Tasks
23.2 and 23.3 are closed — both returned nulls, and neither `lambda` nor
separable covariance has a measured case for a default change.

### Task 23.1: Decide whether a restart strategy arms a stagnation criterion (P1)

The observability this needs is built: per-restart `TerminationReason` records,
`DistributionExtent`, and the trace `restart` index. The window is selected:
half Hansen's anchor at both `lambda` levels. The campaign that tests whether
reclaimed budget buys quality — `-design stagnation`, 4 arms x 12 blocks at
seeds 111013-111024, two named contrasts, `lambda` 20 primary — was submitted
2026-08-29.

- [ ] Analyze the `stagnation` campaign and report it in
      [`docs/cmaes-stagnation-pilot-report.md`](docs/cmaes-stagnation-pilot-report.md)
      or its own report, correcting the two registered contrasts together.
- [ ] Decide, on that result, whether a restart strategy arms a default
      stagnation criterion when the caller sets none. It is a behaviour change
      for every existing CMA-ES restart configuration. The pilot's prior is a
      null: it moved budget without moving cost.
- [ ] If adopted, document the default beside `stopStagnationIters` with the
      measurement behind it, and keep an explicit setting authoritative. A
      default must be window-only — `stopMinImprovement` is an absolute cost
      threshold and cannot transfer to another reference image.

### Task 23.4: A second fixture (P3)

- [ ] Only after 23.1: repeat on a second reference image and a different circle
      count. Everything measured so far is eight circles on one 512x512
      reference.

---

## Phase 15: Search quality and polishing throughput

### Task 15.11: Spend a stage's budget as restarts, not one long run (P1)

A budget-matched ladder over twelve paired blocks puts splitting a stage's
budget at about 160 cost points, winning every block at eight and sixteen
restarts; four restarts of 64 iterations beat one 2048-iteration run by 88
points on 15% of the compute. Mechanism and caveats are in
[`docs/restart-vs-budget-report.md`](docs/restart-vs-budget-report.md). The
ladder predates the v0.7.1 pin, so re-establish a baseline before comparing
anything new against its figures.

- [ ] Measure `--optimizer-epochs` against cold restarts at a matched budget
      before settling on any API. An epoch already re-initializes substantially:
      it advances to a fresh deterministic seed and, with no continuation
      profile, seeds only half the population around the incumbent. Every ladder
      arm ran `optimizerEpochs: 1`, so the comparison is unmeasured.
- [ ] Decide the surface once that comparison exists. A full restart differs
      from an epoch in independent re-initialization of the whole population
      plus best-of selection; if epochs already capture most of the gain, tune
      them rather than adding a second mode.
- [ ] Implement restarts for the base stage on the CLI, the job config, and the
      schedule format, keeping determinism per seed.
- [ ] Re-measure on a second reference image before changing any default. The
      ladder covered one image, `variant` standard, and the eight-circle base
      stage only.
- [ ] Measure whether extend and polish stages benefit. They start from a fitted
      vector rather than a cold population, so the collapse dynamics there are
      unmeasured.

### Task 15.7: Close the dirty-region evaluator's end-to-end check (P1)

The evaluator is built, measured, and pinned for exact cost parity; the
production-shaped 2,111-circle case is 3.1x faster per candidate. See the
dirty-region section of
[`docs/contiguous-window-polish-report.md`](docs/contiguous-window-polish-report.md).
What is missing is the end-to-end confirmation, and it is blocked on a fixture:
the 2,111-circle checkpoint behind the original 599 s sweep is no longer under
`data/jobs` and was never committed.

- [ ] Preserve an immutable production-shaped checkpoint as a fixture.
- [ ] Re-run that sweep at equal budget, record its wall clock, and confirm the
      cost it reaches is unchanged.
- [ ] Record per-candidate cost against affected fraction in the report.

### Task 15.5: Derive the evaluation width from a measurement (P2)

`EvaluationWorkers` defaults to `Threads`, clamped to `GOMAXPROCS`, so an
ordinary run uses one concurrent evaluation per hardware thread. That is the
core count talking, and the one host measured disagrees — see "The shipped
default is the core count, not this measurement" in
[`docs/polishing-throughput-report.md`](docs/polishing-throughput-report.md).

- [ ] Benchmark widths on more than one machine and more than one canvas size.
      One data point on one 12-thread box cannot pick a formula.
- [ ] Establish whether the rule is a fraction of `GOMAXPROCS`, a fixed headroom
      below it, or image-size dependent, and replace the default with it.
- [ ] Document the chosen rule next to `EvaluationWorkers` with the measurement
      behind it, keeping an explicit setting authoritative.

**Acceptance checks:**

- [ ] A benchmark table shows sweep cost against evaluation width on the stated
      machines, and the default the code picks is the width that table
      recommends.
- [ ] An explicitly configured `EvaluationWorkers` is still honored up to the
      `GOMAXPROCS` clamp, with a test covering it.

### Task 15.10: Refresh post-fix polishing evidence (P2)

- [ ] Re-measure `BenchmarkPolishStrategyQualityAfterBatchFit` after the Task
      15.6 acceptance-gate correction and refresh
      [`docs/contiguous-window-polish-report.md`](docs/contiguous-window-polish-report.md).
      The old ranking was partly determined by which active set happened to
      cover inherited blocker circles.

### Task 15.3: Prefix-aware active-set selection (P3, effectively closed)

Dirty-region scoring removed the premise. Ordinary evaluations no longer
rasterize the whole suffix or score the whole canvas, and the report's verdict
is to keep selection quality-driven. Reopen only if a new end-to-end profile
shows the prefix mattering again.

- [ ] If reopened: bias selection toward later draw slots when region energy is
      close, and ship it only with a measured quality comparison at equal
      optimizer budget on the same seed — not on the cost argument alone.

---

## Phase 17: Dashboard

### Task 17.11: Browser, bundle, and documentation sign-off (P1)

- [ ] Capture and add the README dashboard screenshot on a working browser
      runner.
- [ ] Observe `just check` with npm available, including the bundle drift gate,
      and prove it rejects a stale committed bundle.
- [ ] Verify the dashboard shows correct stat tiles, ordered campaign cards,
      running jobs, and an architecture badge matching a forced
      `CIRCLEFIT_SIMD_TIER`.
- [ ] Start a campaign and observe its card move to running with a ticking
      chart.
- [ ] Check chart legibility in auto, forced-light, and forced-dark themes.
- [ ] Exercise all five campaign image modes, overlay opacity, and shortcuts
      `1`–`5`.
- [ ] With JavaScript disabled, verify the dashboard and campaign cost plot
      remain complete and readable.
- [ ] Kill and restart the server while viewing the dashboard; verify the client
      reconnects, refetches, and converges without a navigation.

Safari proper is not covered by CI — Playwright ships WebKit built for Linux.
Use the manual checklist in
[`docs/browser-support.md`](docs/browser-support.md).

### Task 17.12: Bound the restore-path resident set (P2)

The measurement and the mechanism are in the "Security and deployment" section
of [`docs/known-limitations.md`](docs/known-limitations.md): 1.34 GB resident
and a 135 s startup on a 4,358-job data root, proportional to all history rather
than to what is being served.

- [ ] Restore from the `CheckpointInfo` sidecar and load `BestParams` lazily on
      the paths that need a parameter vector (resume, extend, render, artifact
      download).
- [ ] Stop holding full `MetricHistory` for terminal jobs. The chart endpoints
      can read the trace on demand, or a downsampled summary can be persisted
      beside the checkpoint; only running jobs need the live series in memory.
- [ ] Serve `GET /` and `/api/v1/dashboard` from a cached sidecar index
      invalidated by checkpoint writes, rather than re-walking the job tree.

**Acceptance checks:**

- [ ] Resident set after restore is reported for 500, 2,000, and 4,000 persisted
      jobs and is sub-linear in job count; record the host and the data root's
      checkpoint and trace byte totals with the numbers.
- [ ] `GET /` and `/api/v1/dashboard` latency is reported at the same three
      sizes and does not grow with total job count.
- [ ] A restored job still resumes, extends, and renders identically — a resumed
      job reproduces the parent cost exactly, as in Task 15.9.
- [ ] Job detail, campaign charts, and the trace download return the same series
      as before for a terminal job whose history is no longer resident.

---

## Phase 13: Documentation and observability

### Task 13.15: Remaining documentation, examples, and observability (P3)

- [ ] Audit structured logging fields and levels, document logging
      configuration, and add measured slow-operation or progress logging where
      useful.
- [ ] Decide whether a disabled-by-default Prometheus endpoint is warranted
      before adding a new public surface.
- [ ] Add a focused `docs/getting-started.md` covering CLI, server, UI, API,
      artifacts, and common configuration from a clean installation.
- [ ] Curate small redistributable examples with documented settings and
      expected qualitative results; add a deterministic example script and an
      appropriate CI smoke test.
- [ ] Decide whether a separate public roadmap or issue-tracker page adds value
      beyond this plan and [`docs/known-limitations.md`](docs/known-limitations.md).

Badges, promotional screenshots, a walkthrough video, source-file copyright
headers, and a code of conduct remain optional publication work rather than
engineering tasks.

---

## Phase 11: GPU backend (experimental)

Tasks 11.1–11.12 are complete: OpenCL is integrated, benchmarked, parity-tested,
and documented on one vendor GPU, with deliberate opt-in fallback.
[`docs/gpu-backends.md`](docs/gpu-backends.md) and
[`docs/gpu-performance-report.md`](docs/gpu-performance-report.md) are
authoritative.

**OpenCL stays experimental**, and the remaining reasons are coverage, not
speed: parity and throughput are established on one NVIDIA T550, AMD and Intel
are unmeasured for both, and there is no required real-device CI runner — the
GPU gate runs PoCL on a CPU. No optimization answers any of those.

### Task 11.13: Remaining OpenCL optimization tranches (P3)

Tranches 1 and 2 shipped: sessions share one device engine, and staged sessions
composite onto a retained canvas. The staged path went from a 26x/84x separated
loss to 2.5–4.8x faster than the CPU at 512², flat in retained depth.

**Everything below was justified by that gap, so each item now needs its own
measurement rather than an inherited one.** Three sub-items were answered and
closed without building: pinned parameter staging (upload is flat at ~10 µs from
K=1 to K=100 and latency-bound), `engine.poison()` (the shared degradation
record already discovers a lost device once per run), and a device-resident
retained-canvas handoff (the host needs the canvas on every stage boundary
regardless, so only the upload could be avoided — one image copy per stage
against the term tranche 2 made flat).

Note before benchmarking any of this: whole-pipeline benchmarks fix K at 12 and
run eight evaluations per stage where a real stage runs hundreds, so they cannot
see the effects that matter. Use `BenchmarkStagedEvaluationAtDepth`.

- [ ] Reduce per-evaluation synchronization and memory traffic
  - [ ] Add a cost-only execution path that omits full output-buffer writes
        during optimizer evaluations and materializes the final or best image on
        demand. A 512² session still holds a 1,050,778-byte eager
        `image.NewNRGBA` for `renderImage` — about 14 µs of 220.6 µs — that only
        a `Render` caller needs.
  - [ ] Design a batched objective interface so optimizer populations can share
        kernel launches and scalar synchronization.
- [ ] Optimize the render kernel based on profiling
  - [ ] Precompute radius squared, premultiplied color, opacity, and inverse
        opacity into aligned circle records.
  - [ ] Evaluate constant-memory circle parameters and device-specific preferred
        workgroup sizes.
  - [ ] Investigate order-preserving tile or bin circle lists so pixels skip
        non-overlapping circles.
- [ ] Preserve semantics and fallback behavior
  - [ ] Extend CPU/OpenCL parity across joint, sequential, and batch modes after
        each optimization.
  - [ ] Verify cache invalidation, lazy image materialization, cleanup, and the
        permanent CPU degradation path.
- [ ] Re-run the complete backend pipeline benchmark after each tranche
  - [ ] Record vendor-GPU before/after medians, allocations, session counts, and
        evaluation counts, as interleaved single passes rather than `-count=N`.
        PoCL is for lifecycle and allocation deltas only.
  - [ ] Run the same benchmark on supported AMD, Intel, and NVIDIA devices where
        available.
  - [ ] Document crossover points and retain an optimization only where
        profiling demonstrates a benefit.

---

## Phase 10: CPU kernel research

### Task 10.20: Deferred CPU-kernel research (P3)

Bounded research follow-ups, not blockers for the selected production CPU path.
Everything already measured is recorded in
[`docs/rejected-optimizations.md`](docs/rejected-optimizations.md),
[`docs/exact-span-compositors.md`](docs/exact-span-compositors.md),
[`docs/fixed-point-geometry-formats.md`](docs/fixed-point-geometry-formats.md),
and [`docs/renderer-precision-measurements.md`](docs/renderer-precision-measurements.md).

- [ ] Re-derive `compositeSpanSSE2MinPixels` on a host that genuinely lacks
      AVX2. 24 is the pre-hoist crossover and is now a correct upper bound —
      hoisting can only move a crossover left — so it merely leaves some spans
      on scalar. It cannot be measured here: dispatch selects SSE2 only when
      AVX2 is absent, and neither `CIRCLEFIT_SIMD_TIER=sse2` nor
      `GODEBUG=cpu.avx2=off` changes the microarchitecture.
- [ ] Re-derive `compositeSpanNEONMinPixels` on ARM64 benchmarking hardware. 256
      is the pre-hoist crossover, measured on an Apple M5, and is an upper bound
      for the same reason. `BenchmarkCompositeOpaqueSpanNEONCutoff` is the
      command — `scalar`, `neon_hoisted` and `neon_rebuilt` arms at nine
      lengths, so one run yields both the new crossover and the setup the hoist
      removed. The ARM64 rows of `ci-native-simd.yml` cover correctness only,
      and emulated timings do not count.
- [ ] If the original Pascal/Delphi source becomes available, document its exact
      cost arithmetic and numeric/SIMD representations. Until then,
      [`docs/incremental-cost.md`](docs/incremental-cost.md) is the contract.

---

## Not scheduled

- **A shadcn/Vite SPA rewrite of the frontend.** Decided against and recorded in
  [`docs/frontend-spa-rewrite-decision.md`](docs/frontend-spa-rewrite-decision.md).
  Tailwind and shadcn are adoptable inside an island today.
- **Go→TypeScript read-model generation.** Evaluated against a real `tygo` run
  and rejected; the parity fixtures are the contract. See
  [`docs/typescript-read-model-generation.md`](docs/typescript-read-model-generation.md).
- **A macOS GPU backend.** No OpenCL on Apple Silicon and no Metal backend
  planned. The condition to revisit is an Apple Silicon runner that can gate
  parity; see [`docs/gpu-backends.md`](docs/gpu-backends.md).
- **CMA-ES polishing.** Polishing stays MayFly-only by decision, with the reason
  in [`docs/behavior-invariants.md`](docs/behavior-invariants.md). Reopen only
  if a CMA-ES base stage is measured to beat MayFly at an equal evaluation
  budget.
- **Per-client rate limiting.** Not carried forward for the trusted-local
  server; bounded admission and resource limits are the contract.
- **Dragonfly as anything but an expert-only alternative.** It loses all twelve
  blocks; see [`docs/dragonfly-poc-report.md`](docs/dragonfly-poc-report.md).
