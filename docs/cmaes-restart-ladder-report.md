# How many basins a budget can buy, and what actually found the record

**Both registered contrasts retain their null, the record was not beaten, and
the premise the campaign was built on turned out to be wrong.** Trading
population size for independent cold restarts at a fixed evaluation cap does
not beat the incumbent IPOP schedule (`t = -0.26`, five blocks won of twelve),
and BIPOP does not beat IPOP at a matched stagnation criterion (`t = +0.76`,
seven of twelve). The best cost recorded anywhere in the campaign is
**752.5220120747884** — exactly the standing record, matched but not improved.

The useful result is the mechanism, and it inverts the hypothesis. The record
is not the lucky tail of thirty draws. **It is what a population of 1024
reliably converges to on that seed.** All four `lambda` 1024 arms return it to
the last bit, whatever their restart shape, and no small-population arm found
it at all — not with 32 independent searches, and not with 64.

**Ran 2026-08-30** on the 64-core campaign host at `--max-jobs 8`, driver
`scripts/cmaes-measurement`, design `restart-ladder` (84 jobs, 7 arms, 12
paired blocks, 111,425 job-seconds / 31.0 job-hours, 04:01 of wall clock (00:43 to 04:44)).
Server binary stamped `83e36d2dbdaa352d9d3122d47e13c6ed95d75468`, which is the
commit the design was registered at and submitted from.

**Pins:** `github.com/CWBudde/go-cma-es v0.1.0` and `github.com/cwbudde/mayfly
v0.7.1`. Every arm is CMA-ES, so no MayFly code runs in this design. The
fixture is `example/MayFly-512.png` at eight circles, md5
`76c44ab079154956dfadd481b08204a9` — the shared fixture every CMA-ES campaign
except the budget-split screen has used.

## The replication check passed

`sep-ipop` and `sep-ipop-w60` repeat the stagnation campaign's arms of the same
names, on its own seeds. **All 24 cells reproduce bit for bit**, across a
different binary, a different concurrency setting and five days. That is what
licenses reading this campaign's rows against
[`cmaes-stagnation-report.md`](cmaes-stagnation-report.md)'s and against the
recorded record, and it is checked rather than assumed:

```sh
awk -F, 'NR>1 && ($1=="sep-ipop"||$1=="sep-ipop-w60"){print $1"_"$2"="$8}' \
  docs/cmaes-restart-ladder-measurement.csv | sort > /tmp/new
awk -F, 'NR>1 && ($1=="sep-ipop"||$1=="sep-ipop-w60"){print $1"_"$2"="$8}' \
  docs/cmaes-stagnation-measurement.csv | sort > /tmp/old
diff /tmp/new /tmp/old && echo "24 cells identical"
```

## The design

Four rungs hold `lambda * restarts` at 2048 and give every run
`6502400 / 2048 = 3175` generations, so each is capped at the same 6,502,400
evaluations while trading sampling breadth per generation against the number of
independent searches. Three restart-strategy arms run beside them at
`lambda` 1024.

| arm | shape | independent searches over 12 blocks |
| --- | --- | ---: |
| `sep-r2-l1024` | 2 cold restarts, lambda 1024 | 24 |
| `sep-r8-l256` | 8 cold restarts, lambda 256 | 96 |
| `sep-r32-l64` | 32 cold restarts, lambda 64 | 384 |
| `sep-r64-l32` | 64 cold restarts, lambda 32 | 768 |
| `sep-ipop` | IPOP, no criterion — the incumbent | ~30 |
| `sep-ipop-w60` | IPOP, `stopStagnationIters: 60` | ~30 |
| `sep-bipop-w60` | BIPOP, `stopStagnationIters: 60` | 156 |

The design was cut from six rungs to four before submission, for wall clock
against a fixed deadline; the dropped `lambda` 512 and 128 rungs are interior
points carrying no registered contrast. `lambda` and the restart count move
together by construction, so a rung difference belongs to the pair.

## Result

| arm | mean | sd | median | best | gain vs `sep-ipop` | `t` (df=11) | p | blocks won |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `sep-r2-l1024` | 896.59 | 63.79 | 904.08 | **752.52** | -17.79 | -1.60 | 0.138 | 3/12 |
| `sep-r8-l256` | 875.42 | 18.74 | 875.68 | 853.05 | +3.38 | +0.23 | 0.822 | 8/12 |
| `sep-r32-l64` | 882.46 | 22.59 | 893.07 | 840.82 | **-3.67** | **-0.26** | **0.803** | **5/12** |
| `sep-r64-l32` | **864.35** | 25.58 | 860.10 | 824.37 | +14.44 | +0.99 | 0.343 | 10/12 |
| `sep-ipop` | 878.80 | 50.93 | 886.80 | **752.52** | control | control | control | control |
| `sep-ipop-w60` | 868.95 | 48.28 | 881.89 | **752.52** | +9.84 | +0.82 | 0.430 | 4/12 |
| `sep-bipop-w60` | **862.13** | 43.42 | 864.73 | **752.52** | +16.66 | +1.54 | 0.152 | 6/12 |

| registered contrast | gain | `t` (df=11) | p | Holm | blocks won |
| --- | ---: | ---: | ---: | --- | ---: |
| `sep-r32-l64` vs `sep-ipop` (**primary**) | -3.67 | -0.26 | 0.80272 | retain | 5/12 |
| `sep-bipop-w60` vs `sep-ipop-w60` (secondary) | +6.82 | +0.76 | 0.46211 | retain | 7/12 |

Holm step-down over the two registered contrasts at a family-wise alpha of
0.05. Neither comes close; the smaller p would have had to clear 0.025. Lower
cost is better, and a positive gain means the candidate beat the control. Every
column outside the registered pair is exploratory and uncorrected.

### Per block

| block | `sep-r2-l1024` | `sep-r8-l256` | `sep-r32-l64` | `sep-r64-l32` | `sep-ipop` | `sep-ipop-w60` | `sep-bipop-w60` |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 982.00 | 893.11 | 898.59 | 916.02 | 979.71 | 872.37 | 873.07 |
| 2 | 971.04 | 862.51 | 840.82 | 824.37 | 886.78 | 893.49 | 852.38 |
| 3 | 877.00 | 903.29 | 905.71 | 856.89 | 852.73 | 877.00 | 865.11 |
| 4 | 886.78 | 886.44 | 899.96 | 863.31 | 886.78 | 886.78 | 859.86 |
| 5 | 931.93 | 858.87 | 902.04 | 848.34 | 872.22 | 904.40 | 892.52 |
| **6** | **752.52** | 870.38 | 851.93 | 882.91 | **752.52** | **752.52** | **752.52** |
| 7 | 945.64 | 853.05 | 874.04 | 840.39 | 891.30 | 907.59 | 891.90 |
| 8 | 849.29 | 882.22 | 902.35 | 839.81 | 854.40 | 817.40 | 835.06 |
| 9 | 921.79 | 856.62 | 894.04 | 880.25 | 890.99 | 869.03 | 921.01 |
| 10 | 880.38 | 903.57 | 870.42 | 853.25 | 892.58 | 827.68 | 864.35 |
| 11 | 839.31 | 880.99 | 857.56 | 883.91 | 898.70 | 927.98 | 903.45 |
| 12 | 921.37 | 853.94 | 892.11 | 882.77 | 886.81 | 891.20 | 834.39 |

## The record, and why the premise was wrong

The campaign was designed on a reading of the record that the campaign itself
refutes. That reading was: 752.52 was reached at 19% of its run's cap, and IPOP
affords only two or three runs per block, so the record is the minimum of
roughly thirty draws from a basin distribution — and more draws should beat it.

**Block 6 says otherwise.** Four arms return 752.5220120747884 there, to the
last bit: `sep-r2-l1024`, `sep-ipop`, `sep-ipop-w60` and `sep-bipop-w60`. Those
are exactly the four arms that search at `lambda` 1024. The three
small-population arms miss it entirely, and not narrowly — 870.38 at
`lambda` 256, 851.93 at `lambda` 64, 882.91 at `lambda` 32 — despite taking 8,
32 and 64 independent draws each on that seed.

So the basin is not rare. It is found reliably, by every schedule that samples
1024 points per generation, including the one that only restarts twice. What
does *not* find it is a small population, however many times it is restarted.
**Population size reaches that basin; draw count does not.**

That is consistent with, and sharper than, the `lambda` screen's null: `lambda`
has no measured effect on the *mean*, and this campaign reproduces that — but
on the one seed where an exceptional basin exists, `lambda` is what decides
whether the search enters it. A mean-level null does not imply a tail-level
one, and the two campaigns together are a worked example of the difference.

**One block is one block.** This is n = 1 for the exceptional basin, and it
cannot support a general claim that large populations find rare optima. It is
the strongest available reading of the only such basin the project has seen,
and the right response is a design that goes looking for more of them, not a
default change.

The record therefore stands at 752.52, and this campaign did not beat it. What
it removed is the reason to expect that more restarts would.

## The ladder never spent its budget

The rungs are evaluation-matched by their *cap* and turned out not to be
matched by their *spend*, and the gap is much larger than the design
anticipated:

| arm | mean evaluations used | share of the 6,502,400 cap |
| --- | ---: | ---: |
| `sep-r2-l1024` | 2,233,347 | 34% |
| `sep-r8-l256` | 1,906,584 | 29% |
| `sep-r32-l64` | 2,387,822 | 37% |
| `sep-r64-l32` | 2,858,448 | 44% |
| `sep-ipop` | 6,502,403 | 100% |
| `sep-ipop-w60` | 6,502,403 | 100% |
| `sep-bipop-w60` | 6,502,403 | 100% |

Each cold restart converges and trips `TolFun` long before its 3175 generations
are used, and because the restart count is *fixed*, the remainder is simply not
spent. The design predicted the direction — it is why the rungs deliberately
carry no stagnation criterion — but assumed runs would use most of their
generations. They use about a third.

This cuts both ways and both directions matter.

It **weakens the null**: the ladder arms were never given the comparison's
budget, so the primary contrast is not the clean test of restart count against
IPOP that it was registered as. A rung that spends 37% of the cap losing by 3.67
points to one that spends 100% is not evidence that restarts do not help.

It **strengthens the efficiency reading**: `sep-r64-l32` reaches 864.35 on 44%
of the cap where `sep-ipop` reaches 878.80 on all of it, and wins 10 of 12
blocks doing so. On evaluations actually spent, the ladder is far ahead. Under a
fixed cap — which is what an operator pays for — it is a null.

**The fix for a follow-up is structural, not a bigger budget:** cold restarts
should run until the budget is exhausted rather than a fixed number of times.
`optimizerRestarts` is a count, so this shape cannot express "keep restarting
until the cap", and that is the gap a next campaign has to close before the
restart-count question can be answered properly.

## BIPOP works, and is the best arm on the mean

This is the first `bipop` data in the repository, and the strategy did what it
is supposed to do. Across twelve blocks it ran **156 runs — 123 small and 33
large** — between 9 and 19 per block, against the two or three an IPOP ladder
manages on the same budget. The small regime is reached in every block, its
populations are randomized between 1024 and about 2050 as designed, and **the
best result came from a small run in 4 of 12 blocks**.

The structural precaution was necessary and sufficient: with the stagnation
criterion armed, the first large run ends and the schedule proceeds. Without it
the arm would have been IPOP under another name.

`sep-bipop-w60` has the best mean in the campaign, 862.13 against the
incumbent's 878.80. It is **not** significant — `t = +0.76` against its matched
control, six blocks won of twelve against the unarmed incumbent — and a mean
that leads while the win count sits at half is the pattern that has now
dissolved three times in this program. Report it as the most promising
unconfirmed arm and nothing more.

## Sigma, a third-campaign reading

Across 14,185 CMA-ES trajectory samples, `sigma` spans `4.677e-05` to
`8.447e+55` while the identifiable `sigma * max(D)` stays between `0.0000` and
**2.0596**. The extent bound is looser than the 1.52 and 1.386 recorded by the
`lambda` screen and the budget-split campaign, so **quote 2.06 here rather than
reusing either of those figures**. The qualitative finding is unchanged and now
holds on a fourth campaign: sigma alone is gauge-dependent and spans sixty
orders of magnitude while the identifiable extent stays order one. Cite
`distributionExtent`, never `sigma`.

## What this licenses, and what it does not

**Supported.** Trading population for cold restarts at a fixed cap does not
improve the fit on this fixture. BIPOP does not improve it either, at a matched
criterion. The record stands at 752.52. And the stagnation campaign's rows
reproduce exactly, five days and one binary later.

**Not supported.** That restart count does not matter — the ladder arms never
spent their budget, so that question is still open and needs a
restart-until-exhausted shape to ask properly. That `lambda` 1024 finds rare
basins in general — that rests on one block. That BIPOP is worthless: it is the
best arm on the mean, it ran its schedule correctly, and it is one campaign
old.

**Recommended.** Do not change a default on this. The next campaign worth
running is the one this one could not: cold restarts that consume the cap,
against BIPOP, with enough blocks to see a tail rather than a mean. If the
`lambda` 1024 reading is to be tested, it needs a design that seeks exceptional
basins deliberately — many seeds, best-of, at two population sizes — rather
than a paired test of means, which is the instrument that just found nothing.

## Limitations

- One fixture, one circle count, one canvas size, twelve blocks.
- **The ladder arms spend 29-44% of the cap.** Every contrast involving them is
  cap-matched, not spend-matched, and the primary contrast is weakened by it.
- The `lambda` 1024 basin finding rests on a single block.
- Four rungs, not six; `lambda` 512 and 128 were dropped for wall clock before
  submission. `lambda` and the restart count are confounded by construction.
- Six of twelve blocks contain at least one exact tie between arms, which
  contributes a structural rather than a sampled zero to those paired
  differences.
- `sep-bipop-w60` is the only `bipop` arm ever measured here; nothing separates
  the strategy from its window beyond the single control it was given.
- Timings are specific to the 64-core host at `--max-jobs 8` and eight
  evaluation workers.

## Raw data

- [`cmaes-restart-ladder-measurement.csv`](cmaes-restart-ladder-measurement.csv)
  — one row per job.
- [`cmaes-restart-ladder-trajectories.csv`](cmaes-restart-ladder-trajectories.csv)
  — per-iteration cost and adaptation traces including `distributionExtent`.
- [`cmaes-restart-ladder-restarts.csv`](cmaes-restart-ladder-restarts.csv) —
  228 per-restart records with each run's regime, population and its own
  termination reason. The BIPOP rows are the first of their kind here.

`-action analyze` reproduces the result table from the measurement CSV alone,
with no server and no job directories:

```sh
go run ./scripts/cmaes-measurement -action analyze \
  -design restart-ladder -results docs/cmaes-restart-ladder-measurement.csv
```

## Reproducing it

```sh
go build -o ./data/restart-ladder/circlefit .
./data/restart-ladder/circlefit serve \
  --port 8092 --data-root ./data/restart-ladder --max-jobs 1 \
  --queue-size 100 --input-root .
```

In another shell — read the design before queueing it, because a manifest may
only be written once:

```sh
go run ./scripts/cmaes-measurement -action plan    -design restart-ladder
go run ./scripts/cmaes-measurement -action submit  -design restart-ladder
go run ./scripts/cmaes-measurement -action collect -design restart-ladder
go run ./scripts/cmaes-measurement -action analyze -design restart-ladder
```

**Keep `evaluationWorkers` at 8.** It is the value the stagnation campaign ran,
and changing it alters parallel-evaluation ordering and destroys the bit-for-bit
replication the report above rests on. `--max-jobs` may be changed freely; this
campaign ran at 8 where the stagnation campaign ran at 7, and the cells still
matched.

The arm table, the ladder product and the seed base are in
`scripts/cmaes-measurement/main.go`; `main_test.go` pins the registered shape,
including that the two IPOP arms are configuration-identical to the stagnation
campaign's and that the BIPOP arm carries a window.
