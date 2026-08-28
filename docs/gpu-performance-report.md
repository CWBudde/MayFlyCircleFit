# GPU Performance Report

Measurements of the OpenCL renderer on a vendor GPU. Every earlier figure in
[`gpu-backends.md`](gpu-backends.md) came from PoCL executing on the host CPU,
which is a correctness and lifecycle vehicle and says nothing about device
throughput. This report supersedes those figures.

Two runs live here. Task 11.9's is the body of the document and is a dated
record of revision `3d50800`. Task 11.13 tranche 1's is the section at the end,
and it retired the third finding below. Their absolute times are **not**
comparable with each other: the host was loaded differently, and only
same-sitting comparisons are valid.

Task 11.9's three findings, in descending order of confidence:

1. **On the per-evaluation path the GPU wins, and not narrowly.** From 256²
   upward it is 6-14x faster than the multi-threaded SIMD CPU renderer, and
   twenty of thirty-two matrix cells separate in the GPU's favour, with no
   overlap between the two backends' sample ranges.
2. **The image readback, not the parameter upload, is the transfer that costs.**
   Parameters move in about 10 µs regardless of circle count; reading the
   rendered image back runs at 0.5-0.7 GB/s and reaches 5.9 ms at 1024², which
   is three times a complete 1024² evaluation.
3. **The staged pipelines lost badly, on real hardware and not just under
   PoCL.** Sequential measured 26x slower than the CPU and batch 84x, because
   each stage built its own OpenCL context and program. This was the vendor-GPU
   evidence Task 11.13 was waiting for, and it has since been acted on: a
   renderer and its sessions now share one device engine, and neither staged
   mode separates from the CPU any more. **This finding no longer describes the
   code** — see [Task 11.13 tranche 1](#task-1113-tranche-1--sessions-share-a-device-engine)
   at the end of this document.

## Hardware and conditions

| | |
|---|---|
| GPU | NVIDIA T550 Laptop GPU, 16 compute units, 4 GiB, driver 580.178.04 |
| OpenCL platform | NVIDIA CUDA |
| CPU | 12th Gen Intel Core i7-1255U (2 P-cores + 8 E-cores, 12 threads) |
| GOMAXPROCS | 11 |
| Go | 1.26.5, linux/amd64 |
| Revision | `3d50800` plus this branch's benchmark additions |

**These measurements were taken on a contended interactive desktop.** The host
was running a full GNOME session, a browser, an editor and a chat client at a
15-minute load average of 10.75, under the `powersave` governor with the CPU
clocked at 1.0-1.3 GHz and the GPU idling at P8/300 MHz between dispatches. A
quiet machine would make both backends faster, and would not necessarily scale
them equally. Read the separations below, not the absolute times, and do not
compare any figure here against one taken on another host.

## Method

`BenchmarkRendererBackendMatrix` crosses every image size with every circle
count in two arms. The `cost` arm is a bare objective evaluation, which is what
an optimizer actually runs. The `cost_then_render` arm adds the image
materialization, which an optimizer pays only when it keeps a result; on the
OpenCL path that is the lazy readback of the device-resident output, so the
difference between the arms isolates the transfer.

Both arms perturb one coordinate per iteration so that consecutive calls always
differ, which keeps the OpenCL renderer's single-slot parameter cache from
answering a measured call from its previous result. The benchmark fails rather
than skips when `CIRCLEFIT_REQUIRE_OPENCL=1`, and it asserts before and after
the measured loop that the renderer has not degraded to its CPU fallback — a
degraded OpenCL renderer answers silently from the CPU, which would otherwise
publish CPU timings under a GPU label.

The CPU arm runs the renderer's default configuration, in which incremental
cost is **not** active: `NewCPURenderer` leaves `incrementalCostMode` disabled
and only staged sessions enable it. Each CPU evaluation is therefore a full
render plus a full SSD. That is the correct comparison for the joint pipeline,
and it is one reason the staged-pipeline result below runs the other way.

### Interleaving, and why it was necessary

The matrix was run as **eight separate passes of the whole matrix at
`-count=1`**, not as one pass at `-count=8`. Go runs a cell's repetitions
back-to-back, so on a contended host a single burst of background load corrupts
every sample of one cell and none of its neighbour. A first attempt at
`-count=6` produced exactly that: 50-320% spreads, K=50 measuring slower than
K=100, and K=1 slower than K=10 — orderings that are impossible for this
workload. Spreading each cell's samples across eight passes over twelve minutes
removed the inversions and left the results monotonic in both K and image size.
**Use separate passes, not `-count`, for any comparison taken on a machine you
are also using.**

A cell is called separated only when the two backends' observed ranges are
disjoint — every sample of one backend beat every sample of the other. That
verdict is robust to the remaining noise. Cells marked "—" overlap, and this
host cannot decide them; the median ratio is printed for those rows as an
indication only.

## Per-evaluation results

Eight samples per cell, `-benchtime=500ms`. Times are microseconds.

### `cost` — the objective an optimizer evaluates

| Size | K | CPU median [min–max] | OpenCL median [min–max] | Median ratio | Separated |
|-----:|--:|---------------------:|------------------------:|-------------:|:----------|
| 64² | 1 | 133 [25–150] | 101 [23–139] | 1.3x | — |
| 64² | 10 | 265 [58–291] | 109 [32–139] | 2.4x | — |
| 64² | 50 | 649 [129–692] | 141 [31–173] | 4.6x | — |
| 64² | 100 | 965 [199–1192] | 175 [42–193] | 5.5x | GPU |
| 256² | 1 | 619 [122–670] | 107 [29–151] | 5.8x | — |
| 256² | 10 | 1206 [296–1265] | 107 [37–119] | 11.3x | GPU |
| 256² | 50 | 800 [678–2636] | 90 [87–185] | 8.9x | GPU |
| 256² | 100 | 1136 [1065–3598] | 144 [140–230] | 7.9x | GPU |
| 512² | 1 | 479 [439–507] | 63 [62–65] | 7.6x | GPU |
| 512² | 10 | 952 [850–993] | 98 [94–99] | 9.8x | GPU |
| 512² | 50 | 2155 [1854–2223] | 260 [251–273] | 8.3x | GPU |
| 512² | 100 | 2989 [2578–3099] | 465 [453–468] | 6.4x | GPU |
| 1024² | 1 | 2303 [2265–2507] | 166 [163–167] | 13.9x | GPU |
| 1024² | 10 | 3636 [3536–3811] | 308 [298–313] | 11.8x | GPU |
| 1024² | 50 | 7240 [5825–21697] | 959 [884–1056] | 7.6x | GPU |
| 1024² | 100 | 10754 [10157–35339] | 1816 [1755–1868] | 5.9x | GPU |

### `cost_then_render` — the same evaluation plus image materialization

| Size | K | CPU median [min–max] | OpenCL median [min–max] | Median ratio | Separated |
|-----:|--:|---------------------:|------------------------:|-------------:|:----------|
| 64² | 1 | 235 [45–259] | 168 [56–215] | 1.4x | — |
| 64² | 10 | 522 [124–540] | 181 [54–231] | 2.9x | — |
| 64² | 50 | 1298 [261–1386] | 236 [77–256] | 5.5x | GPU |
| 64² | 100 | 1911 [449–2017] | 305 [79–406] | 6.3x | GPU |
| 256² | 1 | 964 [168–1058] | 569 [406–600] | 1.7x | — |
| 256² | 10 | 1965 [461–2154] | 460 [382–672] | 4.3x | — |
| 256² | 50 | 1501 [1075–5075] | 449 [424–695] | 3.3x | GPU |
| 256² | 100 | 2154 [1620–6572] | 498 [471–787] | 4.3x | GPU |
| 512² | 1 | 686 [626–719] | 1420 [1409–1446] | 0.5x | **CPU** |
| 512² | 10 | 1599 [1467–1679] | 1451 [1435–1564] | 1.1x | — |
| 512² | 50 | 3902 [3202–4015] | 1584 [1577–1602] | 2.5x | GPU |
| 512² | 100 | 5318 [5077–5590] | 1763 [1755–1922] | 3.0x | GPU |
| 1024² | 1 | 3417 [3209–3750] | 5401 [5200–5760] | 0.6x | **CPU** |
| 1024² | 10 | 5629 [5069–5856] | 5348 [5314–5806] | 1.1x | — |
| 1024² | 50 | 12888 [11302–39300] | 6058 [5855–6417] | 2.1x | GPU |
| 1024² | 100 | 60789 [17781–66180] | 7336 [6528–8830] | 8.3x | GPU |

Twenty-two of thirty-two cells separate: twenty for the GPU, two for the CPU.

## Where the crossover is

The `cost` arm has no crossover in the measured range that this host can
resolve. The GPU leads at every cell; the lead is merely too small to separate
from noise at 64², and at 256² with K=1. A crossover, if one exists, lies below
64² or below K=1 — that is, outside any workload this renderer serves.

The `cost_then_render` arm does have one, and it is not about compute. The CPU
wins at 512² and 1024² **when K=1**, by a separated margin. The readback is a
fixed per-pixel cost that the GPU pays whatever the circle count, while the CPU
composites only what one circle covers. Adding circles buys the GPU back: the
same 1024² column runs 0.6x, 1.1x, 2.1x, 8.3x as K goes 1, 10, 50, 100. The
practical rule is that **materializing an image per evaluation is the only
regime where this GPU loses**. Both sizes behave alike: the CPU is separated
ahead at K=1, the two are indistinguishable at K=10, and the GPU is separated
ahead from K=50.

This matters less than it looks, because an optimizer calls `Cost` on every
candidate and `Render` only on the ones it keeps. The renderer already
separates the two: `Cost` leaves the output device-resident, and `Render`
reads it back only when asked.

## Transfer boundaries

Five samples each, `-benchtime=2s`.

| Boundary | Case | Median | Range | Payload | Throughput |
|----------|------|-------:|-------|--------:|-----------:|
| Parameter pack and blocking upload | K=1 | 57.02 µs | 37.08–100.81 | 28 B | — |
| | K=10 | 10.85 µs | 7.34–37.73 | 280 B | — |
| | K=50 | 9.25 µs | 9.02–41.30 | 1.4 KB | — |
| | K=100 | 10.48 µs | 10.34–36.01 | 2.8 KB | — |
| Resident image readback | 64² | 30.62 µs | 30.07–32.38 | 16 KiB | 0.54 GB/s |
| | 256² | 413.59 µs | 365.06–503.98 | 256 KiB | 0.63 GB/s |
| | 512² | 2025.54 µs | 1788.22–2116.88 | 1 MiB | 0.52 GB/s |
| | 1024² | 5912.13 µs | 5877.84–5958.44 | 4 MiB | 0.71 GB/s |

Parameter upload is flat in K across a hundredfold change in payload, so it is
latency-bound and nowhere near a bottleneck. **The pinned-memory decision
recorded in `gpu-backends.md` should stand for parameters and be reconsidered
for the readback.** That document made revisiting conditional on parameter
upload becoming a meaningful share of evaluation time; it has not, but the
condition named the wrong transfer. A blocking, unpinned 4 MiB readback at 0.71
GB/s costs 5.9 ms, against 1.8 ms for a complete 1024² K=100 evaluation — the
readback is more than three times the work it delivers, and it is the entire
reason the CPU wins the two cells it wins.

The readback figures also match the matrix independently: at 1024² K=1 the two
arms differ by 5235 µs, against 5912 µs measured standalone.

## Staged pipelines

`BenchmarkOptimizePipelineBackends`, 64² reference, 12 circles, five samples of
five complete pipelines. Renderer construction for the base is excluded;
per-stage session creation, evaluation, retained state and final image
materialization are included.

| Mode | Evaluations/pipeline | CPU median [min–max] | OpenCL median [min–max] | OpenCL vs CPU | Separated |
|------|---------------------:|---------------------:|------------------------:|--------------:|:----------|
| Joint | 11 | 19.7 ms [19.2–20.8] | 15.1 ms [14.7–17.7] | **0.8x — faster** | GPU |
| Sequential | 121 | 216.2 ms [207.9–257.3] | 5623.8 ms [2559.3–9098.2] | 26x slower | CPU |
| Batch (four circles/stage) | 31 | 18.5 ms [16.2–19.3] | 1553.0 ms [1530.6–5337.0] | 84x slower | CPU |

Joint was the one pipeline that created a single session, and it was the one the
GPU won. The staged modes created one OpenCL context, queue and compiled program
per stage, and that setup dominated everything else they did. PoCL reported 190x
and 120x for these two modes; a real GPU reported 26x and 84x. The ratios moved,
the conclusion did not, and vendor hardware now backed it: **the staged OpenCL
path was not usable until sessions shared compiled resources.** That was the
first tranche of Task 11.13.

That tranche has since been implemented and measured. The table above is a
dated record of the code as it stood at `3d50800` and does not describe the
renderer as it is now; the "Task 11.13 tranche 1" section at the end of this
report records what replaced it.

## What this does not establish

- Nothing about any other vendor. AMD and Intel OpenCL devices remain
  unmeasured, and the readback throughput in particular is a property of this
  laptop's link and driver.
- No precise crossover in the `cost` arm, and no timing claim at all for the
  cells marked "—".
- Nothing about a quiet machine. Absolute times here are depressed by
  contention and a `powersave` CPU governor.
- Nothing about correctness, which the parity tests own. All `TestOpenCL*`
  tests pass on this device.

## Reproducing

```sh
# Correctness first: a degraded renderer would otherwise be benchmarked as a GPU.
CIRCLEFIT_REQUIRE_OPENCL=1 go test -tags gpu -count=1 \
  ./internal/fit/renderer/... -run '^TestOpenCL'

# The matrix, as eight separate passes rather than -count=8.
for rep in $(seq 1 8); do
  CIRCLEFIT_REQUIRE_OPENCL=1 go test -tags gpu -run '^$' \
    -bench '^BenchmarkRendererBackendMatrix$' \
    -benchmem -benchtime=500ms -count=1 ./internal/fit/renderer
done

CIRCLEFIT_REQUIRE_OPENCL=1 go test -tags gpu -run '^$' \
  -bench '^BenchmarkOpenCL(ParameterPackAndUpload|ResidentImageReadback)$' \
  -benchmem -benchtime=2s -count=5 ./internal/fit/renderer/opencl

CIRCLEFIT_REQUIRE_OPENCL=1 go test -tags gpu -run '^$' \
  -bench '^BenchmarkOptimizePipelineBackends$' \
  -benchmem -benchtime=5x -count=5 ./internal/fit/renderer
```

Record the device name and vendor with every result; the matrix logs them once
per run. Report a cell as decided only when the ranges are disjoint.

## Task 11.13 tranche 1 — sessions share a device engine

Tranche 1 of Task 11.13 gives a renderer and every session it creates one shared
device engine — a single OpenCL context, queue and compiled program — in place
of the per-session rebuild the staged-pipeline table above measured. This
section records the before/after measurement of that change.

**No absolute time in this section is comparable with the Task 11.9 tables
above.** It is the same laptop under different contention: 11.9 measured
sequential/cpu at 216.2 ms, this run measures it at 51.94 ms. What this section
claims is the before/after ratio taken from a single interleaved sitting, and
nothing else.

### Hardware and conditions

| | |
|---|---|
| GPU | NVIDIA T550 Laptop GPU, 16 compute units, driver 580.178.04 |
| OpenCL platform | NVIDIA CUDA |
| CPU | 12th Gen Intel Core i7-1255U, 12 threads, `powersave` governor |
| Go | 1.26.5, linux/amd64 |
| Revisions | before `0257b04` (main, Task 11.12), after `114b1ad` (this branch) |

The host was again a contended interactive desktop; the load average had reached
17.8 by the end of the run. Only `nvidia.icd` is installed, and both switches
were set (`CIRCLEFIT_REQUIRE_OPENCL=1`, `CIRCLEFIT_REQUIRE_GPU_DEVICE=1`), so no
run could have landed on a CPU OpenCL device and published CPU timings under a
GPU label.

### Method, and why it differs from the 11.9 method

The 11.9 matrix was run as eight separate passes at `-count=1` because Go runs a
cell's repetitions back to back, so on a contended host a single burst of
background load corrupts every sample of one cell and none of its neighbour's.
That problem is still here, and this run has a second one on top of it: it
compares two *revisions*, which live in two working trees. Run as two sittings —
all of "before", then all of "after" — every baseline sample would carry one set
of host conditions and every branch sample another, and host drift over the
intervening minutes would be indistinguishable from the effect of the change.

The pipeline comparison was therefore run as **alternating single passes between
two git worktrees**: one baseline pass and one branch pass per round, five
rounds, `-benchtime=5x -count=1`, with the build cache warmed in both trees
first so that round one does not time a compile. Drift then falls on both arms
equally.

```sh
before=…/worktrees/task-11.12   # 0257b04
after=…/worktrees/task-11.13    # 114b1ad
export CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1

# Warm the build cache in both trees before the first timed round.
for dir in "$before" "$after"; do
  (cd "$dir" && go test -tags gpu -run '^$' -bench '^$' ./internal/fit/renderer)
done

for round in $(seq 1 5); do
  for dir in "$before" "$after"; do
    (cd "$dir" && go test -tags gpu -run '^$' \
      -bench '^BenchmarkOptimizePipelineBackends$' \
      -benchmem -benchtime=5x -count=1 ./internal/fit/renderer)
  done
done
```

"Separated" means below what it means above: the two ranges are disjoint, every
sample of one arm beating every sample of the other. Cells whose ranges overlap
are marked "no" and are not decided by this host; their median ratio is printed
as an indication only.

### A. Session creation

`BenchmarkOpenCLSessionCreation`, `-benchtime=20x -count=1`, one pass per
revision. This is the direct instrument. The `session` arm times `NewSession`
over a base renderer built outside the timed loop, which is exactly what tranche
1 changes; the `new` arm times a full construction, which it does not.

| Cell | Before | After |
|------|-------:|------:|
| 64/new | 654.19 ms | 309.31 ms |
| 64/session | 362.73 ms | **4.38 ms** |
| 512/new | 478.00 ms | 277.38 ms |
| 512/session | 512.44 ms | **4.04 ms** |

Creating a session used to be the same order of cost as constructing a whole
renderer. It is now under 5 ms at both canvas sizes, and the 512² session —
whose reference image is sixty-four times the 64² one — is no more expensive
than the small one, which is what a shared engine predicts and a per-session
rebuild does not. The allocation profile moves with it: `session` drops from 48
to 12 allocs/op at both sizes, and its bytes per op halve, 36032 to 18584 at
64² and 2100421 to 1050782 at 512². The `new` arm goes from 49 to 55 allocs/op,
and its before/after time difference is within this host's run-to-run spread; it
is not a claim.

### B. Pipelines, before and after

`BenchmarkOptimizePipelineBackends`, the interleaved run described above: five
rounds, alternating worktrees, `-benchtime=5x -count=1`. Both backends were
measured in every pass, so the CPU rows are a control on the OpenCL rows.
Nothing in tranche 1 touches the CPU path, and if the CPU arms had moved between
the worktrees the sitting would have been measuring host drift rather than the
change.

| Cell | Before median [min–max] | After median [min–max] | Ratio | Separated |
|------|------------------------:|-----------------------:|------:|:----------|
| sequential/opencl | 3065.09 ms [2994.4–4266.1] | 36.58 ms [29.0–52.4] | 83.8x | **yes** |
| batch/opencl | 1904.22 ms [1728.9–2280.1] | 22.17 ms [16.9–28.6] | 85.9x | **yes** |
| joint/opencl | 6.00 ms [1.6–6.8] | 1.70 ms [1.7–2.5] | 3.5x | no |
| sequential/cpu | 51.94 ms [39.1–61.9] | 53.13 ms [33.0–67.3] | 0.98x | no (control) |
| batch/cpu | 16.44 ms [9.5–29.0] | 23.72 ms [10.2–26.6] | 0.69x | no (control) |
| joint/cpu | 6.00 ms [4.8–6.8] | 6.09 ms [2.0–6.2] | 0.98x | no (control) |

The three CPU arms did not move in any way this sitting can detect. None of
them separates, and the widest excursion — batch/cpu at 0.69x — is a factor of
one and a half on a cell whose own before-range already spans threefold, 9.5 to
29.0 ms. That is the size of this host's noise, and it bounds what drift could
have contributed to the OpenCL rows. Against that background the two staged
OpenCL rows are the change and not the host: 83.8x and 85.9x, both separated,
with every "after" sample faster than every "before" sample. joint/opencl
improved too, but its two ranges touch, so this sitting does not decide it.

The allocation counts agree: sequential/opencl 1815 → 1311, batch/opencl 1612 →
1329, both CPU arms flat to within 7 allocs. Evaluation counts are identical
across the revisions (joint 11, sequential 121, batch 31), and so are the
pipelines' session counts (0 / 5 / 9). The pipelines do the same work and create
the same number of sessions; what changed is what a session costs.

### C. CPU against OpenCL after the change

Eight separate passes on the branch, `-benchtime=5x -count=1`. Interleaving is
not needed here because there is only one revision in the comparison, so this
follows the 11.9 method exactly.

| Mode | CPU median [min–max] | OpenCL median [min–max] | OpenCL/CPU | Separated |
|------|---------------------:|------------------------:|-----------:|:----------|
| joint | 3.44 ms [2.14–6.60] | 3.42 ms [1.89–7.17] | 1.00x | **no** |
| sequential | 37.88 ms [25.16–65.35] | 42.39 ms [31.21–58.91] | 1.12x | **no** |
| batch | 14.77 ms [13.57–26.63] | 15.95 ms [14.12–23.33] | 1.08x | **no** |

### What the tranche buys, and what it does not

The staged modes went from a separated 26x and 84x loss against the CPU to
statistically indistinguishable from it. That is the finding, and its shape
matters: **no cell in table C separates.** This is a disqualification removed,
not a win. The staged OpenCL pipelines are no longer ruled out on throughput;
that is not the same as the GPU beating the CPU at sequential or batch, and this
host does not show that it does.

The same caution runs the other way for joint. This run cannot separate joint
either — 1.00x, with the ranges overlapping — so it neither confirms nor
contradicts the 0.8x recorded under Task 11.9. Do not restate that 0.8x as a
current figure on the strength of this run.

### What this does not establish

- Nothing about any other vendor. This is still one NVIDIA laptop GPU; AMD and
  Intel OpenCL devices remain unmeasured, for throughput as for parity.
- Nothing about a quiet machine. The host was contended throughout, under the
  `powersave` governor, at a load average of 17.8 by the end.
- Nothing beyond one workload point. `BenchmarkOptimizePipelineBackends` runs a
  64² reference at K=12; whether these ratios hold at larger canvases or circle
  counts is unmeasured.
- No comparison of absolute times against the Task 11.9 tables above, in either
  direction.
- Nothing about correctness, which the parity tests own and no benchmark here
  asserts.

### Reproducing the tranche 1 measurement

```sh
# Correctness first: a degraded renderer would otherwise be benchmarked as a GPU.
CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
  go test -tags gpu -count=1 ./internal/fit/renderer/... -run '^TestOpenCL'

# A. Session creation, one pass per revision.
CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
  go test -tags gpu -run '^$' -bench '^BenchmarkOpenCLSessionCreation$' \
  -benchmem -benchtime=20x -count=1 ./internal/fit/renderer/opencl

# B. The interleaved before/after: the two-worktree loop under Method above.

# C. CPU against OpenCL on one revision, as eight separate passes.
for rep in $(seq 1 8); do
  CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
    go test -tags gpu -run '^$' -bench '^BenchmarkOptimizePipelineBackends$' \
    -benchmem -benchtime=5x -count=1 ./internal/fit/renderer
done
```

Set `CIRCLEFIT_REQUIRE_GPU_DEVICE=1` alongside `CIRCLEFIT_REQUIRE_OPENCL=1` on
any host that also has a CPU OpenCL runtime installed. Without it a pass can
land on PoCL, which satisfies the first switch while measuring the CPU.
