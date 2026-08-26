# CMA-ES preliminary measurement

> **Preliminary, stopped campaign.** This report contains one paired seed block,
> not the twelve blocks in the registered design. The campaign was stopped by
> operator request on 2026-08-25 when its calibrated wall time proved to be
> several days. It supports descriptive observations only: no means, standard
> deviations, paired t-tests, or population claims are reported.

In the one completed paired block, full-covariance CMA-ES reached cost 911.22
and stopped after 1,441,792 optimizer evaluations, 22.2% of the shared
6,502,400-evaluation cap. Mayfly's single long run reached 1163.00 at the cap,
and its sixteen-restart arm reached a cap-valid 959.26. The interrupted IPOP
run had improved the same CMA-ES start to 875.63 at 2,713,600 evaluations when
the campaign was stopped. Lower cost is better.

These are promising observations, not an optimizer ranking. One block cannot
estimate seed variance, and the IPOP observation is right-censored by the
operator stop.

## What was run

All available arms used seed prefix 111001 on
`example/MayFly-512.png`: 512x512 CPU rendering, blank-canvas cost 38732.12,
eight circles in one batch, population 1024, exact AVX2 compositor, one render
thread, eight parallel evaluation workers, and ordinary stage convergence
disabled. The scoring cap was 6,502,400 optimizer evaluations. The Mayfly
control used 2,048 iterations; r16 used sixteen cold attempts of 128 iterations.
CMA-ES was configured in full-covariance active mode with normalized initial
sigma 0.3; its single arm used no restart and its IPOP arm shared one cap across
restarts.

The identified binary was built from MayFlyCircleFit commit
`dd1fce31ee4fd00fbc9c96c1689027f7de908bc2` with Go 1.26.0. It pinned Mayfly
v0.7.1 and go-cma-es
`v0.0.0-20260825143954-e528faf326bf`. That pseudo-version is code-identical to
the library's later `v0.1.0` tag on the search path — the intervening commits
added a benchmark function suite, the WebAssembly demo, and the version constant
— so this measurement remains comparable to a run on the current pin. The host
was a six-core/twelve-thread AMD Ryzen 5 4600H. The server admitted one job at a
time.

The planned design had five arms in each of twelve paired blocks. Three jobs
completed, the fourth was checkpointed on interruption, and the remaining 56
never produced job artifacts. In particular, there is no separable-CMA result.

## Preliminary result

The score is the minimum trace cost whose optimizer evaluation count is at or
below the cap. `Scored at` is when that minimum first appeared, not the total
work allocated to the arm.

| Arm | Status | Cost | Scored at | Work observed | Share of cap | Descriptive change |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Mayfly single | complete | 1163.00 | 5,426,414 | 6,502,400 | 100.0% | control |
| Mayfly r16 | complete | 959.26 | 5,304,986 | 6,533,120 | 100.5% run; score cap-valid | 203.74 below Mayfly single |
| CMA-ES single | complete (`TolFun`) | 911.22 | 1,219,584 | 1,441,792 | 22.2% | 251.78 below Mayfly single; 48.04 below r16 |
| CMA-ES IPOP | **interrupted** | 875.63 | 2,713,600 | 2,713,600 | 41.7% | 287.37 below Mayfly single; 83.63 below r16 |
| sep-CMA-ES IPOP | not started | — | — | — | — | no data |

The Mayfly r16 job completed all attempts and therefore ran 30,720 optimizer
evaluations beyond the cap. Its best cost first appeared before the cap, so
capping changes neither the displayed score nor the descriptive comparison.
The three extra evaluations shown in completed job checkpoints are initial and
pipeline accounting; the shares above use optimizer evaluations.

Full CMA-ES single was 21.6% below Mayfly single and 5.0% below r16 in this
block. The partial IPOP value was respectively 24.7% and 8.7% below them, and
3.9% below CMA-ES single. Those percentages are effect descriptions for seed
111001 only. They are not estimates of expected improvement.

## Mechanism observations

The new diagnostic records the RMS pairwise Mayfly population spread in the
adapter's normalized parameter space. The first post-update observation was
2.187 at iteration 1. Spread briefly rose to 2.797 at iteration 5, fell below
10% of its iteration-1 value at iteration 33, and was 0.175 at iteration 40.
It reached 0.000515 at iteration 512, a 99.98% reduction from iteration 1.

| Mayfly single iteration | Evaluations | Best cost | Population spread |
| ---: | ---: | ---: | ---: |
| 1 | 5,222 | 2073.00 | 2.1873 |
| 40 | 129,008 | 1325.48 | 0.1754 |
| 80 | 255,968 | 1185.86 | 0.1093 |
| 160 | 509,888 | 1172.28 | 0.03545 |
| 255 | 811,418 | 1168.52 | 0.01087 |
| 512 | 1,627,136 | 1163.31 | 0.000515 |
| 2,048 | 6,502,400 | 1163.00 | 0.002468 |

After iteration 512, Mayfly spent 75% of its iteration budget to improve by
only 0.30 cost points (0.026%). Under Mayfly v0.7.1 this seed therefore still
exhibits the collapse-and-freeze mechanism from the older restart report,
although its first below-10% observation at iteration 33 is later than the
11–16 iteration range measured there under v0.5.1.

CMA-ES exposes a different mechanism. Sigma remained adaptive while the
covariance condition number grew from 1.12 to 1.31e11, direct evidence that the
search learned a highly anisotropic metric instead of retaining an isotropic
population. Its `TolFun` criterion then ended the run at 1,408 generations
rather than spending the remaining 77.8% of the cap.

| CMA-ES single generation | Evaluations | Best cost | Sigma | Condition number |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1,024 | 17388.42 | 0.287 | 1.12 |
| 80 | 81,920 | 1581.45 | 0.413 | 7.81 |
| 160 | 163,840 | 1362.32 | 0.403 | 440 |
| 255 | 261,120 | 1135.20 | 0.634 | 3,772 |
| 512 | 524,288 | 914.66 | 1.385 | 23,466 |
| 1,024 | 1,048,576 | 911.23 | 0.915 | 6.28e9 |
| 1,408 | 1,441,792 | 911.22 | 0.619 | 1.31e11 |

IPOP's first restart reproduced the single-run result. The second restart
doubled the population, reset the condition number to 1.42, and eventually
improved the incumbent from 911.22 to the interrupted 875.63. At the stop its
second-run sigma was 83.80 and its condition number 7.22e6. This is evidence
that the restart escaped this first run's basin for this seed; it does not tell
us the completed IPOP distribution or whether IPOP is generally preferable.

## What the result does not establish

- Only one paired block exists. The registered `df=11` paired tests require all
  twelve; calculating a t statistic or reporting “blocks won” here would be
  misleading.
- The IPOP arm is incomplete and may have improved further. It must not be
  compared as though it had a common terminal rule with completed arms.
- No separable-CMA job ran, so the covariance-mode comparison is unanswered.
- One seed cannot establish robustness, expected gain, or a new default.
- Trace diagnostics add observation overhead. The recorded trace intervals are
  useful operational records, not optimizer-throughput benchmarks.
- The persisted r16 trace from this revision only emitted progress when the
  global incumbent improved, so it censors non-improving restart populations.
  The mechanism discussion therefore uses the complete Mayfly single trace;
  r16 contributes a cost score only.
- This is one eight-circle base-stage workload. It says nothing yet about later
  stages, other dimensions, other images, or polishing.

The defensible preliminary conclusion is narrow: on seed 111001, CMA-ES both
beat the two Mayfly allocations and used its own convergence criterion to stop
far below the shared cap; its IPOP restart improved further before interruption.
The Mayfly trace independently confirms that a long v0.7.1 run again spent most
of its budget after population diversity had collapsed. The planned campaign
remains incomplete and should not be resumed without explicitly accepting its
multi-day runtime or redesigning it as a smaller registered experiment.

## Reproduction and raw data

- [`cmaes-preliminary-results.csv`](cmaes-preliminary-results.csv) contains all
  four persisted scores and job identifiers. `elapsedSeconds` is the interval
  from the first to last persisted trace entry because the stopped server's
  in-memory status was deliberately not restarted.
- [`cmaes-preliminary-trajectories.csv`](cmaes-preliminary-trajectories.csv)
  contains the downsampled mechanism traces up to the common cap.
- The local, ignored full-resolution traces and checkpoints remain below
  `data/cmaes-phase11/projects/cmaes-phase11/jobs/`.

The committed CSVs can be regenerated offline, without starting the server or
resuming work:

```sh
go run ./scripts/cmaes-measurement \
  -action preliminary \
  -results docs/cmaes-preliminary-results.csv \
  -trajectories docs/cmaes-preliminary-trajectories.csv
```
