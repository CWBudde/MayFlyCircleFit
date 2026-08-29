# Polishing throughput: session pools and active-set selection

This report preserves the measurements and design decisions behind the first
two Phase 15 optimizations. The numbers were originally recorded only in
`PLAN.md`; they describe the revisions measured on 2026-08-16 and 2026-08-17,
not the current branch unless the reproduction commands are rerun.

Related contracts live in
[`behavior-invariants.md`](behavior-invariants.md), while budget and strategy
quality are covered by
[`polishing-budget-report.md`](polishing-budget-report.md) and
[`contiguous-window-polish-report.md`](contiguous-window-polish-report.md).

## Outcome

Long incremental runs had two independent serial costs:

1. Every optimizer candidate in a polishing sweep shared one renderer session
   and one merged parameter vector, so Mayfly could not evaluate a population
   concurrently.
2. `residual-region` active-set selection performed leave-one-out audits and
   region-influence renders serially before the optimizer started.

The implementation now pools isolated candidate sessions, parallelizes the
independent selection work, restricts influence rendering to the pixels it
actually reads, and caches the incumbent audit. On the recorded 12-thread host,
the session pool improved the production-shaped candidate term by about
4.1–4.3× at width 8, and the complete 512-circle selection fell from 4.24 s to
0.47 s.

## Transactional session pool

A sweep still has one incumbent and one commit point. Concurrency applies only
while the optimizer scores candidates:

- each `evaluationSlot` owns an independent renderer session and scratch
  parameter vector;
- the fixed draw-order prefix is rasterized once per sweep, then copied into
  each slot's `bakedSuffixSession`;
- a width-one pool uses the original session and is byte-identical to the
  serial path;
- after Mayfly returns, the merged candidate is evaluated serially on the full
  session and passes the usefulness gate before commit;
- a parallel optimizer is rejected unless the backend can create independent
  sessions *and* advertises concurrent evaluation. OpenCL can create staged
  sessions, but they share one in-order command queue and concurrent device
  evaluation has not been validated, so it remains serial.

This separation is important: making candidate evaluation concurrent does not
make acceptance concurrent and does not weaken the all-or-nothing sweep.

### Recorded scaling

**Host:** AMD Ryzen 5 4600H, 12 logical CPUs, `GOMAXPROCS=12`  
**Workload:** 512×512, `residual-region`, active set 8, renderer threads 1  
**Method:** `-benchtime 1x -count 5`; fit
`time = fixed + candidates × perCandidate` through 4,000 and 16,000 candidates,
then extrapolate the candidate term to the roughly 690,000 evaluations of the
production sweep.

| Circles | Width 8 | Width 12 | Width 48 |
| ---: | ---: | ---: | ---: |
| 256 | **4.09×** | 3.04× | 3.92× |
| 512 | **4.33×** | 3.60× | 3.65× |

Width 48 did not execute 48 evaluations at once on this host; it allocated 48
slots while the runtime could overlap at most 12. It is evidence about
oversized-pool cost, not 48-core scaling. Width 12 was consistently slower than
width 8, which motivated the still-open work to derive the default evaluation
width from measurements instead of equating it with `GOMAXPROCS`.

At 512×512 with 512 circles and 16,000 candidates, recorded allocation totals
were:

| Width | Bytes | Allocations |
| ---: | ---: | ---: |
| 1 | 37.9 MB | 16,686 |
| 8 | 80.5 MB | — |
| 12 | 101.8 MB | — |
| 48 | 293.8 MB | 17,656 |

One slot held about 5.3 MB. Pool setup cost 15–130 ms and saved roughly 1.5 µs
per candidate, giving recorded break-even points of about 13 candidates at
width 8 and 90 at width 48. Those figures explain why a real sweep amortizes
the pool while a tiny synthetic sweep may not.

### The shipped default is the core count, not this measurement

`EvaluationWorkers` resolves to `Threads` when it is zero
(`internal/app/config.go`) and `effectiveEvaluationWorkers`
(`internal/fit/renderer/renderer_cpu.go`) clamps that to `GOMAXPROCS`, so a
default configuration runs as many concurrent evaluations as the machine has
hardware threads. The table above disagrees with that on the one host where it
was measured, at both circle counts, on two revisions, and for medians and
minima alike: one evaluation goroutine per hardware thread leaves nothing for
the runtime and saturates memory bandwidth, and it costs 26% more memory.

One data point on one 12-thread box is not enough to pick a formula — whether
the rule is a fraction of `GOMAXPROCS`, a fixed headroom below it, or something
image-size dependent is unmeasured. Task 6 in [`../PLAN.md`](../PLAN.md)
carries it. An explicitly configured width stays authoritative either way.

## Active-set selection

After candidate evaluation widened, `residual-region` selection became the
largest fixed cost per sweep. The implementation changed it without changing
the selected circles:

- `AuditCircleBatch` partitions draw-order runs across independent,
  single-threaded sessions;
- the region-influence pass stripes circles across the same session shape;
- influence rendering is restricted to
  `selectedRegion ∩ circleRasterBounds`, and a non-intersecting circle scores
  zero without rendering;
- `incumbentAuditCache` survives a rejected sweep, because the incumbent did
  not change, and adopts the candidate audit after a successful commit.

Selection is a deterministic ranking, not an optimizer search. Worker width
must not change its active set, replacement set, or selected region.

### Recorded selection cost

**Host:** the same Ryzen 5 4600H, `GOMAXPROCS=12`  
**Workload:** 512×512, `residual-region`, active set 8  
**Method:** `-benchtime 2x -count 3`, medians.

| Circles | Before, 1 thread | Region-limited, 1 thread | Final, 12 threads |
| ---: | ---: | ---: | ---: |
| 32 | — | 0.11 s | 0.08 s |
| 128 | — | 0.43 s | 0.12 s |
| 256 | 1.62 s | 0.95 s | — |
| 512 | 4.24 s | 2.30 s | 0.47 s |

The region restriction alone improved the full selection by 1.71× at 256
circles and 1.84× at 512. Isolating only the influence loop showed the intended
effect more clearly: 0.151 s to 0.003 s at 128 circles and 1.86 s to 0.029 s at
512. Once that loop became cheap, parallel leave-one-out auditing supplied the
remaining gain; end-to-end selection improved 9.0× at 512 circles.

The tradeoff is transient memory. The recorded 512-circle selection rose from
21 MB to 119 MB because twelve audit sessions/steppers and twelve influence
scratch sessions coexist during selection. That memory is released when
selection returns; it bought back roughly 3.8 seconds per sweep on the measured
host.

## Correctness guards

The implementation is pinned by tests that cover distinct failure modes:

- `TestPolishCircleBatchPoolWidthParity` compares widths 1, 2, 4, and 8,
  including final parameters, cost, accepted sweeps, and rendered pixels.
- `TestPolishCircleBatchPoolServesConcurrentEvaluations` exercises multi-sweep
  pools under the race detector.
- `TestPolishCircleBatchBakesThePrefixOncePerSweep` prevents one expensive
  prefix rasterization per worker.
- `TestSelectPolishingActiveSetMatchesSerial` requires width-independent
  selection for all strategies.
- `TestRegionInfluenceEnergiesMatchFullRenders` proves the clipped row-band
  calculation matches full renders.
- `TestAuditCircleBatchChunkedMatchesSerial` proves the parallel audit matches
  the serial reference.

The usefulness gate was corrected after the initial pool work. Its current
non-regression contract is documented in `behavior-invariants.md`; do not infer
the current gate from older benchmark goldens.

## Remaining bottleneck

After the region pass was reduced, the audit dominated selection. It still
performs a full render, changed-pixel scans, and full-image SSD work for each
omitted circle. A dirty-region audit could correct the incumbent SSD only over
the removed circle's raster, but it must preserve the exact integer semantics
of `fit.FastMSECost`; a last-bit float change would reorder circles and break
selection determinism. It was tracked as the dirty-region work rather than
being folded into the completed selection change; the evaluator has since
shipped, and what remains of it is Task 4 in [`../PLAN.md`](../PLAN.md).

## Reproducing

Record CPU, OS, Go version, `GOMAXPROCS`, power state, and allocation counts.
Do not compare these absolute timings across machines.

```sh
go test -run '^$' \
  -bench '^(BenchmarkPolishSweepProductionShape|BenchmarkPolishSweepPoolSetup)$' \
  -benchmem -benchtime 1x -count 5 -timeout 120m ./internal/fit/renderer/

go test -run '^$' \
  -bench '^(BenchmarkPolishResidualRegionSelection|BenchmarkPolishSelectionByCircleCount|BenchmarkRegionInfluenceEnergies)$' \
  -benchmem -benchtime 2x -count 3 -timeout 120m ./internal/fit/renderer/
```
