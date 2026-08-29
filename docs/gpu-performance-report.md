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
| 64/session | 362.73 ms | ~~4.38 ms~~ — **withdrawn, see below** |
| 512/new | 478.00 ms | 277.38 ms |
| 512/session | 512.44 ms | ~~4.04 ms~~ — **withdrawn, see below** |

Creating a session used to be the same order of cost as constructing a whole
renderer, and it no longer is. The allocation profile is measured correctly and
stands: `session` drops from 48 to 12 allocs/op at both sizes, and its bytes per
op halve, 36032 to 18584 at 64² and 2100421 to 1050782 at 512². The `new` arm
goes from 49 to 55 allocs/op, and its before/after time difference is within
this host's run-to-run spread; it is not a claim.

#### The two `session` times above are wrong, and both were too high

The `session` arm held the base renderer with `defer release()`. A deferred call
runs before the benchmark function returns, so releasing the program, context,
queue and runtime — tens to hundreds of milliseconds, and a cost belonging to no
iteration — sat *inside* the measured region, where the framework divided it by
`b.N`. At `-benchtime=20x` that one-time term was essentially the entire
reported figure.

The signature is that the arm's answer depended on `b.N` alone. Measured on the
tranche 1 tip before the fix: 4.13 ms/op at `20x`, 699 µs at `100x`, 125 µs at
`1000x`, 33.5 µs at `10000x` — a 123x spread over a workload that did not
change. Total wall time stayed near 70-80 ms across the small counts, which is
what a fixed cost divided by a varying `b.N` looks like.

The arm now calls `b.StopTimer()` before releasing the base. It is stable across
`b.N` afterwards: 34.8 µs at `20x` against 35.6 µs at `1000x`.

Re-measured with the corrected benchmark, three passes per cell,
`-benchtime=20x -count=1`, both revisions on the host in the Task 11.13 tranche
1 conditions below. The "before" worktree is `0257b04` with the corrected
benchmark file copied in, because the benchmark did not exist before tranche 1
added it:

| Cell | Before median [min–max] | After median [min–max] | Ratio |
|------|-----------------------:|----------------------:|------:|
| 64/session | 182.8 ms [177.7–187.6] | **12.1 µs** [9.0–13.6] | ~15,000x |
| 512/session | 213.9 ms [200.8–238.6] | **220.6 µs** [183.7–233.1] | ~970x |

Every "before" sample beats every "after" sample by more than three orders of
magnitude, so both cells separate trivially. **Tranche 1's conclusion is
unchanged and its effect is far larger than was recorded** — the withdrawn
figures understated it by roughly two orders of magnitude, in the conservative
direction. Nothing else in this report rests on them: sections B and C measure
whole pipelines and are unaffected.

What remains in a 512² session is not device work. A phase breakdown — kernel
pair, the four buffers, and `clFinish` on an idle queue, each timed on its own —
accounts for about 14 µs of the 220 µs. The rest is the eager
`image.NewNRGBA` for `renderImage`, which is the 1,050,778 B/op the arm reports
and which a session only needs if something calls `Render`. Allocating it lazily
would remove most of what is left; it is not worth doing for the time alone, and
is recorded here as an observation rather than a proposal.

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

## Task 11.13 tranche 2 — the profile that decides the accumulated canvas

Tranche 1's write-up left the remaining tranches to be re-justified by a profile
of where the staged time goes, rather than by the 26x/84x gap it had removed.
This is that profile. It answers tranche 2, the accumulated canvas, and answers
it **yes** — for a reason none of the pipeline benchmarks could see.

### Hardware and conditions

Same host as tranche 1: NVIDIA T550 Laptop GPU, driver 580.178.04, 16 compute
units; `/etc/OpenCL/vendors/` holds only `nvidia.icd`. 12th Gen Intel Core
i7-1255U, Linux 6.8.0-138-generic, Go 1.24. Every pass ran under
`CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1`.

### The asymmetry being measured

`CPURenderer` implements `accumulatedSessionFactory`: a staged session it
creates rasterizes only the stage's new circles over the canvas the previous
stages retained, so its per-evaluation cost does not depend on how many circles
came before. The OpenCL renderer implements only `rendererSessionFactory`, so
`newStagedAccumulator` returns nil for it and every evaluation rebuilds the
whole draw order from white.

That makes staged CPU work grow with the circle count and staged OpenCL work
grow with its square. The two therefore cannot separate at small K — and every
pipeline benchmark in the package fixes K at 12.

### A. Per-evaluation cost against circle count

`BenchmarkRendererBackendMatrix`, `cost` arm, eight separate passes,
`-benchtime=500ms -count=1`. Times are microseconds; ranges are over the eight
passes.

| Size | K | CPU median | OpenCL median [min–max] | Ratio |
|-----:|--:|-----------:|------------------------:|------:|
| 64² | 1 | 29.5 | 23.9 [23–24] | 1.24x |
| 64² | 10 | 67.9 | 30.7 [27–34] | 2.21x |
| 64² | 50 | 138.3 | 44.7 [44–46] | 3.09x |
| 64² | 100 | 233.0 | 63.8 [51–77] | 3.65x |
| 256² | 1 | 122.3 | 35.3 [31–40] | 3.47x |
| 256² | 10 | 343.7 | 44.8 [42–47] | 7.68x |
| 256² | 50 | 810.2 | 88.7 [86–91] | 9.14x |
| 256² | 100 | 997.4 | 134.9 [126–144] | 7.39x |
| 512² | 1 | 579.7 | 61.0 [58–64] | 9.50x |
| 512² | 10 | 1106.6 | 99.2 [96–103] | 11.16x |
| 512² | 50 | 2228.3 | 228.0 [227–229] | 9.77x |
| 512² | 100 | 3004.7 | 383.3 [369–397] | 7.84x |
| 1024² | 1 | 3045.7 | 161.5 [160–163] | 18.86x |
| 1024² | 10 | 3694.9 | 273.3 [266–280] | 13.52x |
| 1024² | 50 | 7827.5 | 776.4 [764–788] | 10.08x |
| 1024² | 100 | 9499.8 | 1407.9 [1391–1425] | 6.75x |

A least-squares fit over the four circle counts splits each OpenCL row into a
fixed per-evaluation floor and a marginal cost per circle:

| Size | Floor | Per circle | Circle work at K=100 |
|-----:|------:|-----------:|---------------------:|
| 64² | 25.1 µs | 0.389 µs | 38.9 µs (61% of the evaluation) |
| 256² | 35.2 µs | 1.011 µs | 101.1 µs (74%) |
| 512² | 63.1 µs | 3.224 µs | 322.4 µs (84%) |
| 1024² | 147.9 µs | 12.594 µs | 1259.4 µs (90%) |

The marginal term is what an accumulated canvas removes, and it is the majority
of an evaluation everywhere except the smallest canvas at the smallest circle
counts. This host has GPU-ahead medians in all sixteen cells, so the earlier
statement that the `cost` arm has no crossover in the measured range still
holds.

### B. One evaluation at campaign depth — the decisive instrument

`BenchmarkStagedEvaluationAtDepth`, five separate passes, `-benchtime=300ms
-count=1`. A stage appends one circle to D retained ones. Three arms, so backend
is separated from technique: `cpu_accumulated` is the CPU with its retained
canvas, `cpu_replay` is the CPU paying the same replay the GPU pays, and
`opencl_replay` is what the OpenCL renderer does today. Medians in
microseconds.

| Size | D | cpu_accumulated | cpu_replay | opencl_replay | opencl ÷ cpu_accum |
|-----:|--:|----------------:|-----------:|--------------:|-------------------:|
| 128² | 8 | 39.9 | 95.6 | 30.0 | 0.75x |
| 128² | 32 | 60.5 | 165.2 | 32.7 | 0.54x |
| 128² | 128 | 30.7 | 559.2 | 66.0 | **2.15x** |
| 128² | 512 | 32.0 | 1541.1 | 216.2 | **6.76x** |
| 512² | 8 | 252.7 | 941.4 | 93.0 | 0.37x |
| 512² | 32 | 444.4 | 1598.3 | 153.2 | 0.34x |
| 512² | 128 | 255.9 | 3496.4 | 437.7 | 1.71x |
| 512² | 512 | 218.6 | 10820.5 | 1764.6 | **8.07x** |

Three things are visible at once, and they are the whole argument:

- **`cpu_accumulated` is flat in D.** 30.7–60.5 µs across a 64-fold change in
  retained depth at 128², 218.6–444.4 µs at 512². That is the property an
  accumulated canvas has and the OpenCL renderer lacks.
- **Both replay arms grow with D**, roughly linearly, as replaying D circles
  must.
- **The GPU is not the problem; the technique is.** At 512², D=512 the GPU
  beats the CPU 6.1x on the *same* replay work (1764.6 against 10820.5 µs) and
  still loses to the CPU's accumulated canvas by 8.1x.

Three rows separate: 128² at D=128 and D=512, and 512² at D=512, each with
every `cpu_accumulated` sample beating every `opencl_replay` sample. The rest
overlap and are not decided by this host. The crossover lies between D=32 and
D=128 at both sizes, which is where the growing replay term passes the CPU's
flat one.

Extrapolating the fitted marginal cost, an accumulated OpenCL session would
evaluate one circle over a retained canvas for about the floor plus one circle —
roughly 65 µs at 512², plus one image-sized read for the base canvas — against
the 1764.6 µs it pays at D=512 today, and against the CPU's 218.6 µs. That
figure is a projection from Table A, not a measurement, and stays a projection
until the implementation exists.

An earlier revision of this table read 35–54% high in the `opencl_replay`
column, because `benchmarkDepthCost` did not stop the timer before the arm's
deferred OpenCL teardown — the same contamination this report documents for
`BenchmarkOpenCLSessionCreation`, reintroduced in the instrument built to study
it. Review caught it. The benchmark now registers teardown with `b.Cleanup` and
stops the timer before the post-loop device check, and the figures above are
from the corrected instrument. The direction of the conclusion did not change;
the size of the gap did, from 11.9x to 8.1x at 512²/D=512.

### C. Why no existing benchmark showed this

`BenchmarkOptimizeStagedGrowth` runs whole sequential and batch pipelines at
K = 8, 32, 128 on both backends. Its CPU-to-OpenCL ratio stays near 1.3–1.4x
and does not diverge, because a stage in these benchmarks runs eight
evaluations, and at eight evaluations per stage the per-stage setup — session
creation, residual seeding, canvas handling — dominates the evaluations
entirely. A stage in a real campaign runs hundreds.

That is why the benchmark is kept and why it is *not* the instrument for this
decision: it measures a pipeline whose evaluation term has been diluted by two
orders of magnitude relative to production. It does usefully bound the claim in
tranche 1's write-up, which was measured at K=12 — that "staged OpenCL is
indistinguishable from the CPU" is a statement about K=12 and does not survive
to campaign depths.

### What this decides

**Tranche 2 is justified and should be built.** Not by the old 26x/84x ratio,
which tranche 1 removed, but by a loss that grows without bound in the circle
count: `docs/schedule-format.md` describes campaigns run to 1000–3000 circles
with `additionalCircles: 1`, which is precisely the deepest-retained-prefix,
one-new-circle shape that Table B measures, and the shape where the OpenCL
staged path is furthest behind.

**What this does not establish.** One device, one vendor, one driver; AMD and
Intel are unmeasured. Table B's intermediate depths are undecided on this host.
The projected accumulated-canvas cost is arithmetic from Table A, and the cost
of the base-canvas upload and the extra per-pixel read the kernel would need is
estimated, not measured. Nothing here says the accumulated canvas is free.

### Reproducing the tranche 2 profile

```sh
# Correctness first: a degraded renderer would otherwise be benchmarked as a GPU.
CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
  go test -tags gpu -count=1 ./internal/fit/renderer/... -run '^TestOpenCL'

# A. Per-evaluation cost against circle count, eight separate passes.
for rep in $(seq 1 8); do
  CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
    go test -tags gpu -run '^$' \
    -bench '^BenchmarkRendererBackendMatrix$/.*/.*/^cost$/' \
    -benchmem -benchtime=500ms -count=1 ./internal/fit/renderer
done

# B. One evaluation at campaign depth, five separate passes.
for rep in $(seq 1 5); do
  CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
    go test -tags gpu -run '^$' -bench '^BenchmarkStagedEvaluationAtDepth$' \
    -benchmem -benchtime=300ms -count=1 ./internal/fit/renderer
done

# C. Whole staged pipelines, for the dilution argument.
CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
  go test -tags gpu -run '^$' -bench '^BenchmarkOptimizeStagedGrowth$' \
  -benchmem -benchtime=1x -count=1 ./internal/fit/renderer
```

## Task 11.13 tranche 2 — the accumulated canvas, built and measured

Tranche 2 is implemented: the render kernel takes a packed `uchar4` base canvas
and a uniform `hasBase` flag, `NewSessionWithCanvas` uploads the retained canvas
once per session, and the OpenCL adapter now satisfies `accumulatedSessionFactory`.
A sequential or batch stage composites its new circles onto the canvas the
retained ones produced instead of replaying them on every evaluation.

The profile above projected "roughly 65 µs at 512²" for such a session. The
measurement is 70–74 µs, and it is flat in depth.

### Hardware and conditions

NVIDIA T550 Laptop GPU, driver 580.178.04, 16 compute units, reduction
workgroup size 256. 12th Gen Intel Core i7-1255U. Linux, `powersave`.
`/etc/OpenCL/vendors/` holds only `nvidia.icd`, so no run here can land on a CPU
device, and both benchmark arms fail rather than skip under
`CIRCLEFIT_REQUIRE_OPENCL=1`.

**The host was busy and the run waited for it.** An unrelated workload held the
load average above 16 for the first attempt, which reported `cpu_accumulated` at
128²/D=32 as 1266 µs against the 60.5 µs the profile above recorded — it was
measuring the other job. That attempt was discarded. The published run blocked
until the one-minute load average fell below 3 and then took five separate
passes at `-count=1`; the passes are timestamped in order with the load average
each started at (2.4–2.9, which is the benchmark itself). Residual load from the
first minutes still shows in the upper ends of the observed ranges, which makes
the disjointness test *harder* to pass, not easier — so every separation below
is conservative.

### A. One evaluation at campaign depth, four arms

`BenchmarkStagedEvaluationAtDepth`, five separate passes, `-benchtime=300ms
-count=1`. A stage appends one circle to D retained ones. The arms separate
backend from technique in both directions: the two accumulated arms are a
backend comparison, and the two OpenCL arms are a technique comparison. Medians
in microseconds, observed range in brackets.

| Size | D | cpu_accumulated | cpu_replay | opencl_replay | **opencl_accumulated** |
|-----:|--:|----------------:|-----------:|--------------:|-----------------------:|
| 128² | 8 | 30.4 [25–34] | 79.5 [79–89] | 28.1 [26–36] | **26.1 [25–43]** |
| 128² | 32 | 43.2 [40–183] | 151.6 [146–742] | 32.6 [28–128] | **25.9 [25–102]** |
| 128² | 128 | 33.4 [31–166] | 537.9 [520–1886] | 66.1 [61–298] | **25.4 [25–138]** |
| 128² | 512 | 27.7 [25–157] | 1521.8 [1422–5943] | 205.4 [193–689] | **26.8 [27–108]** |
| 512² | 8 | 175.8 [162–1068] | 817.1 [771–3067] | 91.2 [85–150] | **70.2 [66–130]** |
| 512² | 32 | 345.2 [323–1605] | 1357.7 [1315–4331] | 151.6 [151–233] | **71.9 [69–128]** |
| 512² | 128 | 194.0 [172–902] | 3079.6 [2482–10143] | 438.3 [432–724] | **73.6 [70–128]** |
| 512² | 512 | 180.5 [156–818] | 9686.8 [9450–36417] | 1596.5 [1549–1926] | **72.1 [70–130]** |

Allocations are unchanged from the replay arm: 47 B/op and 6 allocs/op for both
OpenCL arms at every cell, against 817–825 B/op and 11 allocs/op for both CPU
arms. The base canvas is uploaded once at session construction and never
touched again, so it costs nothing per evaluation.

**The three control arms reproduce the profile**, which is what makes the fourth
believable: `cpu_accumulated` at 512²/D=512 measured 218.6 µs there and 180.5 µs
here, `opencl_replay` 1764.6 µs and 1596.5 µs, and both replay arms still grow
roughly linearly in D while `cpu_accumulated` stays flat.

**`opencl_accumulated` is flat in D too, which is the whole point.** 25.4–26.8 µs
at 128² and 70.2–73.6 µs at 512², across a 64-fold change in retained depth. The
quadratic term is gone.

Separation, by disjoint observed ranges:

| Size | D | vs `opencl_replay` | vs `cpu_accumulated` |
|-----:|--:|--------------------|----------------------|
| 128² | 8 | 1.08x — | 1.16x — |
| 128² | 32 | 1.26x — | 1.67x — |
| 128² | 128 | 2.61x — | 1.31x — |
| 128² | 512 | **7.67x separated** | 1.03x — |
| 512² | 8 | 1.30x — | **2.50x separated** |
| 512² | 32 | **2.11x separated** | **4.80x separated** |
| 512² | 128 | **5.96x separated** | **2.64x separated** |
| 512² | 512 | **22.14x separated** | **2.50x separated** |

Two readings, and they are different claims:

- **Against its own replay**, the accumulated session wins by 22x at 512²/D=512
  and the margin grows with depth exactly as a removed linear term should. Four
  cells separate; the shallow ones do not, because at D=8 there is barely any
  replay to remove.
- **Against the CPU's accumulated canvas** — the arm that was 8.1x ahead in the
  profile — the GPU is now 2.5–4.8x faster at 512², separated at every depth
  including D=8. At 128² nothing separates: that canvas is small enough that
  both accumulated arms sit at 25–43 µs and the GPU is bounded by launch
  latency rather than by work.

So this is the first staged result on this host where the GPU is not merely
usable but measurably the faster place to run, and the qualification is a canvas
size rather than a circle count.

### B. The white path did not pay for it

The kernel gained two arguments for every renderer, including the ones that
start from white. The design intends that to cost nothing: `hasBase` is uniform
across the whole dispatch, so a renderer without a base canvas never executes
the load. Two benchmarks were run against `e401c68` to check it, alternating
revisions pass by pass between two worktrees.

`BenchmarkRendererBackendMatrix`, OpenCL `cost` arm, eight interleaved passes
each, `-benchtime=500ms -count=1`. This is the sharper of the two instruments,
because it measures one evaluation rather than a whole pipeline. Medians in
microseconds:

| Size | K | before (`e401c68`) | after | ratio |
|-----:|--:|-------------------:|------:|------:|
| 256² | 10 | 41.6 [35.4–142.2] | 40.8 [35.2–136.3] | 0.98x |
| 256² | 100 | 142.0 [123.1–295.9] | 126.8 [124.6–471.0] | 0.89x |
| 512² | 10 | 119.6 [91.9–192.1] | 96.8 [93.5–102.1] | 0.81x |
| 512² | 100 | 429.5 [364.6–513.8] | 385.5 [371.6–403.0] | 0.90x |
| 1024² | 10 | 317.6 [270.0–364.7] | 282.4 [272.3–302.0] | 0.89x |
| 1024² | 100 | 1513.4 [1371.8–1553.5] | 1379.4 [1340.5–1518.2] | 0.91x |

**Not one of the sixteen cells separates**, so this does not *prove* the white
path is unchanged. What it does say is the useful half: at the canvas sizes
where an extra per-pixel read would show up most — 512² and 1024², which are
also the least noisy cells — the after distribution lies at or below the before
one at every K, 0.81–0.92x. The two cells that look like a regression, 64² at
K=10 and K=50, are the noisiest in the table (ranges reaching 148 and 224 µs
against medians near 28 and 38) and are first-pass load, not signal.

Read together with the design — the flag is uniform, so the load is not
executed — the fair statement is that the measurement is consistent with the
white path costing nothing and cannot separate a change from zero. Nothing here
supports a claim in either direction beyond that.

`BenchmarkOptimizePipelineBackends`, six interleaved passes each, `-benchtime=5x`,
decided nothing at all: every cell overlaps, joint included (2.53 ms before,
2.86 ms after, ranges [1.65–3.28] and [1.47–4.65]). That is the dilution this
report already documents — K fixed at 12 and eight evaluations per stage, where
per-stage setup dominates — and it is why the depth benchmark exists.

### What tranche 2 buys, and what it does not

It buys the removal of a term that grew without bound. `docs/schedule-format.md`
describes campaigns run to 1000–3000 circles with `additionalCircles: 1`, which
is exactly the deep-prefix, one-new-circle shape Table A measures, and the shape
where the OpenCL staged path was furthest behind. At 512² that shape is now
2.5x faster than the CPU rather than 8.1x slower, and the margin against the old
replay path grows with depth.

It does not buy a faster pipeline on any benchmark in this repository, because
none of them runs a stage long enough to show it. It does not buy anything at
128². And it changes one observable: `finishStagedResult` now returns the
retained canvas instead of opening a final replay session, so sequential creates
four OpenCL sessions instead of five and batch eight instead of nine.

### What this does not establish

One device, one vendor, one driver. AMD and Intel remain unmeasured for both
parity and throughput. The 128² cells are undecided rather than equal. The
white-path check separates nothing, so it bounds a regression loosely rather
than excluding one. And the whole comparison is per-evaluation: no measurement
here says a complete campaign finishes faster, only that the evaluation it
repeats hundreds of times per stage does.

### Reproducing the tranche 2 measurement

```sh
# Correctness first, including the tolerance-zero accumulated parity test.
CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
  go test -tags gpu -count=1 ./internal/fit/renderer/... -run '^TestOpenCL'

# A. One evaluation at campaign depth, four arms, five separate passes.
# Wait for a quiet host first; a run under load measures the other workload.
for rep in $(seq 1 5); do
  CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
    go test -tags gpu -run '^$' -bench '^BenchmarkStagedEvaluationAtDepth$' \
    -benchmem -benchtime=300ms -count=1 ./internal/fit/renderer
done

# B. White-path regression, alternating revisions between two worktrees.
git worktree add /tmp/before e401c68
for rep in $(seq 1 8); do
  for dir in /tmp/before .; do
    ( cd "$dir" && CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 \
      go test -tags gpu -run '^$' \
      -bench '^BenchmarkRendererBackendMatrix$/.*/.*/^cost$/^opencl$' \
      -benchmem -benchtime=500ms -count=1 ./internal/fit/renderer )
  done
done
```

## Task 11 — the batched objective, measured and declined

`PLAN.md` Task 11 carried "Design a batched objective interface so optimizer
populations can share kernel launches and scalar synchronization", and its own
framing required a measurement before building: *everything below was justified
by that gap, so each item now needs its own measurement rather than an inherited
one.* This is that measurement, and it says **do not build it.**

### Hardware and conditions

| | |
|---|---|
| GPU | NVIDIA T550 Laptop GPU, 16 compute units, 4 GiB, driver 580.178.04 |
| OpenCL platform | NVIDIA CUDA (the only ICD installed on this host) |
| Reduction local size | 256, probed |
| CPU | 12th Gen Intel Core i7-1255U, 12 threads |
| Go | 1.26.5, linux/amd64 |
| Revision | `68a9509` plus this branch's batch evaluator and benchmarks |

Taken on a contended interactive desktop, and this laptop throttles: an
end-to-end run of identical work varied 25.8–47.0 s in the same sitting. Every
figure below is the ordinary `-benchtime=1s` default over **two separate
passes**, never `-count`, for the reason recorded under
[Reproducing](#reproducing).

### What prompted it

An end-to-end campaign-shaped run (512², 8 circles in one batch stage, separable
CMA-ES with IPOP, 204,803 evaluations, three seeds) measured the same
per-evaluation cost at both population sizes the campaign uses:

| λ | iterations | CPU, 8 workers | OpenCL | ms/eval CPU | ms/eval GPU |
|---:|---:|---:|---:|---:|---:|
| 20 | 10240 | 109.1 s | 39.1 s | 0.533 | 0.191 |
| 1024 | 200 | 106.5 s | 39.8 s | 0.520 | 0.194 |

Flat across a 51-fold change in generation size is what "nothing amortizes"
looks like from outside the renderer. `--evaluation-workers 1` and `8` produced
bit-identical costs and no speedup in the same run, which is the documented
decline path in [`known-limitations.md`](known-limitations.md) behaving as
specified: OpenCL withholds the concurrent-evaluation marker.

### A. Removing the per-candidate host synchronization

`BenchmarkOpenCLGenerationEvaluation`, 512², 8 circles. The `serial` arm is one
`Renderer.Cost` per candidate — today's path, each with its own blocking round
trip. The `pipelined` arm gives every candidate its own parameter and partial
buffers, issues all λ chains with non-blocking transfers, and synchronizes
**once** per generation instead of λ times. The kernels are unchanged. Both arms
are pinned bit-exact against each other by `TestOpenCLCostBatchMatchesSerial`.

Microseconds per evaluation, two passes:

| λ | serial | pipelined | pipelined ÷ serial |
|---:|---:|---:|---:|
| 20 | 90.34 / 88.47 | 109.2 / 113.3 | 1.24x / 1.28x |
| 64 | 99.36 / 91.30 | 110.2 / 111.3 | 1.11x / 1.22x |
| 256 | 88.80 / 91.00 | 114.3 / 118.2 | 1.29x / 1.30x |
| 1024 | 93.38 / 95.36 | 120.0 / 131.6 | 1.29x / 1.38x |

**Pipelining is slower, in all eight cells, on both passes.** The serial column
is also flat from λ=20 to λ=1024, reproducing the end-to-end observation inside
the renderer where nothing else can explain it.

The direction is not mysterious once stated: the engine's command queue is
in-order, so queueing λ chains ahead does not let any two of them overlap. It
buys only the host round trip at the end of each chain — and it pays for that
with an extra `clSetKernelArg` per candidate and with λ×3 distinct buffers the
driver must make resident instead of three hot ones. On this device the second
term is the larger one.

### B. How much of an evaluation is removable at all

If removing the host synchronization makes a generation *slower*, the question
becomes whether a true batched kernel — one dispatch over a candidate dimension —
has anything left to win. `BenchmarkOpenCLEvaluationFloorBySize` sweeps the
canvas at 8 circles and λ=64 on the serial path. A tiny canvas does almost no
per-pixel work, so its per-evaluation figure is very nearly the launch-and-
synchronize floor on its own.

| Canvas | µs/eval, pass 1 | pass 2 |
|---:|---:|---:|
| 8² | 30.74 | 35.23 |
| 64² | 34.55 | 32.76 |
| 128² | 30.65 | 31.41 |
| 256² | 44.12 | 42.09 |
| 512² | 89.37 | 88.18 |

The floor is **~32.6 µs** and is flat from 8² to 128², which is the same fact
tranche 2 recorded from the other side ("at 128² nothing separates: the canvas
is small enough that the device is bounded by launch latency"), now measured
directly rather than inferred.

At 512² an evaluation costs 88.8 µs, so the floor is **37%** of it and per-pixel
work is the other 63%. A batched kernel can only attack the floor: every
candidate still has to touch every pixel. So even a batch that removed **100%**
of the per-candidate floor — which is impossible, since it still needs its own
dispatch, its own reduction and λ times the partial-sum traffic — would win

    88.8 / (88.8 − 32.6) = 1.58x

and the achievable figure is below that. Note this is a *renderer-level* bound;
the end-to-end campaign figure above is 191 µs per evaluation, so more than half
of a real evaluation is already outside the device and would not move at all.

### What this decides

**The batched objective interface is not worth building on this device, and the
`PLAN.md` item is closed by measurement rather than by code.** One scheme was
built and measured 1.1-1.4x *slower*; the best conceivable scheme is bounded at
1.58x at the renderer level and less end to end. That is far short of what
justifies a `BatchObjectiveFunc` upstream in `go-cma-es`, a new pin, a new entry
in the measured-pairs allowlist in `internal/opt/resume_guard.go`, and the same
again for MayFly.

It also retires the reading that the 63.1 µs fixed floor fitted under tranche 2
was overhead waiting to be amortized. It is not: about half of it is per-pixel
work that does not depend on circle count, and only ~32.6 µs is launch and
synchronization.

The `batchEvaluator` stays in the tree, unexported and reached only from these
benchmarks and the parity test, because it is the instrument that produced this
answer and the answer is device-specific — an AMD or Intel device, or an
out-of-order queue, could reach a different one.

### What this does not establish

**Nothing about another vendor.** One NVIDIA T550, one driver, one in-order
queue. The pipelined arm's loss in particular is a property of how this driver
handles buffer residency, and the floor is a property of this device's launch
latency; both are exactly the kind of figure that moves between vendors.

**Nothing about an out-of-order queue.** The engine creates an in-order queue,
so the pipelined arm could never overlap two candidates' dispatches. Whether an
out-of-order queue plus events would change the answer is unmeasured — but it
would not change the floor analysis in section B, which is what bounds the win.

**Nothing about a larger canvas.** At 1024² the per-pixel term grows faster than
the floor, so the ceiling for batching is *lower* there, not higher. The
measurement was taken at the campaign's own canvas.

**Nothing about the image readback.** `Cost` defers it, so it never appears in
these numbers. Pinned staging for the readback remains open and unmeasured, and
it is a `Render` concern — once per job, not once per evaluation.

### Reproducing

```sh
# Parity first. Both arms are the same float32 device arithmetic, so this is an
# exact-equality test, not a deviation budget.
just test-gpu

# A and B, two separate passes each. No -count: a -count=6 attempt on this
# device produced 50-320% spreads and impossible orderings.
for rep in 1 2; do just bench-gpu '^BenchmarkOpenCLGenerationEvaluation'; done
for rep in 1 2; do just bench-gpu '^BenchmarkOpenCLEvaluationFloorBySize'; done
```
