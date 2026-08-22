# Seed variance and the population question

> **The population half of this report is historical.** Everything below was
> measured under MayFly v0.4.0, whose `NC` — the number of crossover offspring
> — was fixed at an absolute 20 no matter how large `NPop` was. Raising
> `popSize` therefore bought more evaluations per iteration but no additional
> recombination, which is why the answer here is "a large population buys
> nothing". The pinned MayFly v0.5.0 scales `NC` with the population and that
> answer no longer holds; see *After the fix* at the end. **Do not size a new
> campaign's population from the figures below.**
>
> The seed-variance half — that base-stage quality does not predict the fit
> built on it — was not a function of `NC` and still stands.

Four seeds of the same pow2 campaign, run to a barrier at 256 circles, plus a
control that changes exactly one field. The question the run was meant to settle
was whether a very large population on the base stage buys anything. It did not
settle that, but it settled something more useful: **the quality of the base
arrangement does not predict the quality of the fit built on top of it.**

That is the second time this project has learned the same lesson. The first was
`example/campaigns/seed-audit-10.json`, where a hand-placed 10-circle
arrangement (cost 1187.13, PSNR 17.39) was beaten by an ordinary cold run with
*eight* circles. This report is the same finding with a controlled experiment
behind it.

## What was run

`example/campaigns/mayfly-3000-v2.json` at seeds 20260822, 20260823, 20260824
and 20260825, against `example/MayFly-512.png` (512x512, blank-canvas cost
38732.12). Base: 8 circles, batch, `batchSize` 8, `popSize` 4096, 2 x 1024
iterations. Extends append one circle at a time; polish sits at every pow2 rung.
A `pauseBefore` barrier stops each arm at 256 circles.

Conditions, because a cost is only comparable to a cost from the same renderer:
CPU backend, default exact compositor (`fastCompositing` off), one server with
`--max-jobs 4`, 16 evaluation workers per job, 64-core x86-64 host. All eight
campaigns in this report ran on that one host with one binary.

## Cost by rung, pop 4096

| circles | 20260822 | 20260823 | 20260824 | 20260825 | spread |
| --- | --- | --- | --- | --- | --- |
| 8 (base) | **880.62** | 925.88 | 927.36 | 909.40 | 5.3% |
| 8 (polished) | 880.62 | 925.87 | 927.26 | 909.40 | 5.3% |
| 16 | 714.73 | **671.62** | 792.83 | 673.03 | 18.0% |
| 32 | 531.12 | 510.32 | **489.24** | 497.70 | 8.6% |
| 64 | 446.28 | 430.17 | 414.66 | **414.39** | 7.7% |
| 128 | 354.86 | 343.86 | **340.12** | 342.09 | 4.3% |
| 256 | 273.46 | *pending* | 262.78 | **255.13** | 7.2% |

The 256-circle spread is over the three arms that had finished; across those same
three arms the 128 rung spreads 4.3%, so the divergence genuinely widens at the
end rather than being an artifact of the missing arm.

## Finding 1: the ranking inverts, then freezes

Seed 20260822 has the best base by a clear margin — 880.62 against a
second-best 909.40 — and is **last at every rung from 32 circles onward**. Seed
20260824 has the worst base and finishes second. The order settles by 32 circles
and never changes again.

This is not two arms trading places within noise. It is a stable reordering that
survives seven rungs and roughly 250 stages. Four seeds cannot establish an
anti-correlation and this report does not claim one; what it does establish is
that base quality carries no *positive* information about the outcome, which is
enough to undermine the reason for spending on the base at all.

The mechanism is the greedy schedule rather than the optimizer. An extend
freezes every existing circle and optimizes only what it appends, so the
arrangement reached at rung *k* is a commitment. Polishing is the only stage
that revisits earlier circles, and its active set is capped at
`MaxBatchSize` = 100 — at 256 circles a sweep can reach at most 64 of them.
Divergence that appears by 32 circles has no mechanism available to close it.

## Finding 2: polishing an 8-circle base is dead weight

The two polish steps that follow the base moved cost by 0.005, 0.012, 0.096 and
0.002 across the four arms, for 8 to 10 minutes of wall clock each.

Read the right way round, this is a *positive* result for the large population:
`popSize` 4096 converged the 56-parameter vector so completely that a
replacement sweep and a residual-region sweep could find nothing left. The
subproblem was solved exactly. It simply turned out that solving it exactly did
not matter, which is Finding 1.

Either way the two steps should come out of the document. They cost about 40
minutes of host time across four arms and returned a tenth of a cost unit.

## Finding 3: where the wall clock actually goes

| stage kind | count to the barrier | typical cost |
| --- | --- | --- |
| base | 1 | 30m03s - 44m34s |
| polish | 7 | 3m34s - 17m10s |
| extend | 248 | 2 - 3 s |

All 248 extends together take about ten minutes. The campaign is base plus
polish; the climb is free. An earlier estimate in this project's working notes
put the extends at roughly a minute each and projected 8-9 hours per arm — the
arms reached the barrier in 1h42m to 1h49m. Budget campaigns by polish count,
not by circle count.

## The control: pop 64 on an otherwise identical document

The experiment above cannot answer whether 4096 was worth it, because it has no
low-population arm to compare against. The control is the same document with
`base.popSize` changed to 64 and nothing else touched — verified field by field,
with the step list compared for equality.

| seed | pop 4096 | time | pop 64 | time |
| --- | --- | --- | --- | --- |
| 20260822 | **880.62** | 32m33s | 1004.06 | 1m13s |
| 20260823 | 925.88 | 44m34s | **903.30** | 52s |
| 20260824 | 927.36 | 38m08s | **897.44** | 54s |
| 20260825 | **909.40** | 30m03s | 1127.73 | 1m11s |
| mean | **910.82** | ~36m | 983.13 | ~1m |
| spread | **46.74** | | 230.29 | |

Head to head the two are level at two seeds each. The 7.4% mean advantage comes
entirely from variance: the large population never produced a bad seed, while
pop 64 produced a 1004.06 and a 1127.73. The *ceiling* did not move — the single
best base result in the table is pop 64's 897.44, found in 54 seconds, on the
same seed where pop 4096 spent 38 minutes to reach 927.36.

So a larger population raises the floor rather than the ceiling, at roughly 35x
the wall clock. That is consistent with the two mechanisms below: more initial
samples improve the worst case, while neither the exploration lifetime nor the
recombination count improves with population, so the best case is unchanged.

**The 256-circle half of the control is still running.** Whether that base
advantage survives the climb is the actual question, and Finding 1 predicts it
will not. Until those numbers land, no conclusion about population is stated
here.

## Why a bigger population may not behave the way GA intuition expects

Two mechanisms in the pinned `mayfly v0.4.0` are worth knowing before designing
the next experiment. Both are read from the library's defaults, not measured
here.

**Exploration is damped per iteration, not per individual.** `NewDefaultConfig`
sets `Dance: 5.0` with `DanceDamp: 0.8`, and `FL: 1.0` with `FLDamp: 0.99`. The
nuptial-dance term is the stochastic component that lets the best male leave its
current basin, and it is multiplied by 0.8 on every iteration: after 30
iterations it retains 0.8^30, about 0.1% of its initial magnitude. The female
random-walk term decays more slowly but is also negligible within a few hundred
iterations. Exploration therefore has a lifetime measured in iterations, and
`popSize` does not extend it. A larger population searches a wider net during
the same brief window; it cannot hold the window open.

**Recombination is indifferent to the population size.** `internal/opt`'s
adapter sets `config.NPop` and `config.NPopF` from `popSize` but never sets
`config.NC`, which stays at its default of 20. `NC` is not a rate — it is an
absolute count, and the mating loop spends it on a fixed elite slice
(`mayfly.go:891`):

```go
for k := range config.NC / 2 {
    p1 := males[k]      // both arrays are sorted by cost
    p2 := females[k]
```

Ten pairs, always the ten best. At the library's default population of 20 that
recombines the whole top half; at `popSize` 4096 it recombines the same ten
pairs and leaves 4086 individuals doing nothing but velocity-following toward
`gbest`. Raising `popSize` therefore buys a better initial sample and more
mutants (`NM` does scale, at 5% of `NPop`) — but not one additional crossover.

That is the substantive break with classical GA intuition, where offspring count
scales with the population so a larger population sustains more recombination
and more diversity indefinitely. Here the genetic operator is pinned to the
elite regardless of population, so at large `popSize` the algorithm degenerates
toward pure PSO — whose signature failure on multimodal landscapes is premature
convergence.

If the control confirms that the base advantage does not survive, the levers
worth trying next are, in rough order of expected value:

- **`optimizerEpochs`.** An epoch restarts the optimizer from the best result
  with fresh diversity, which resets the damping schedule. It is the only
  diversity mechanism this codebase currently exposes, and the campaign uses 2.
  `popSize` 512 with 16 epochs costs about what `popSize` 4096 with 2 epochs
  costs and reopens the exploration window 16 times instead of twice.
- **`variant`.** Every non-default variant in the pinned library is a published
  attempt to fix premature convergence: DESMA (dynamic elite strategy,
  multimodal), OLCE-MA (orthogonal learning and chaotic exploitation, aimed at
  highly multimodal problems), EOBBMA (elite opposition-based bare bones, aimed
  at deceptive landscapes). Fitting circles to a photograph under a frozen
  prefix is multimodal and arguably deceptive. Every run in this project so far
  has used `standard`, which was never a decision.
- **Less greedy extends.** `additionalCircles` above 1 co-optimizes several
  circles instead of committing them one at a time, which attacks Finding 1 at
  its source.

Two gaps are worth recording. `internal/app` accepts only `standard`, `desma`
and `olce`, while `internal/opt` supports four more (`eobbma`, `gsasma`, `mpma`,
`aoblmoa`) that no configuration can currently reach. And `NC` and the damping
coefficients are not exposed at all, so the mechanisms most likely to govern
this problem cannot be configured from a campaign document.

## Reproducing

```sh
mayflycirclefit schedule create --dry-run example/campaigns/mayfly-3000-v2.json
```

expands to 3007 stages and 6,359,040 nominal iterations, with the barrier
reported before the stage table. The per-seed documents differ from the
committed one only in `seed`, `name` and `refPath`; the control additionally
sets `base.popSize` to 64.

## After the fix — MayFly v0.5.0

The negative result above was a property of the library, not of the mayfly
algorithm. v0.5.0 derives the crossover offspring count from the population
instead of pinning it at 20, which is the behavior the algorithm's description
always implied.

Re-measured with a 24-run A/B: 8 circles, 256 iters, 1 epoch, batch mode, three
seeds (1, 2, 3) per cell, mean final cost. Both binaries were built from
separate checkouts and each was verified with `go version -m` to link the
version it claims, because an earlier attempt at this comparison built both
arms from the same worktree and produced identical numbers.

| `popSize` | v0.4.0 | v0.5.0 |
| --- | --- | --- |
| 64 | 1174.97 | 1151.85 |
| 256 | 1118.29 | 1044.16 |
| 1024 | 1049.15 | **898.15** |
| 4096 | 1109.33 | 1031.58 |

Two things changed. Population now helps **monotonically up to 1024**, which it
never did under v0.4.0, and the v0.4.0 column's own non-monotonicity — 4096
scoring worse than 1024 — turns out to be present under both versions. 1024 is
the working sweet spot; 4096 is past it and pays for evaluations it cannot
convert into search.

This also retires the earlier suspicion that the algorithm variants (DESMA,
OLCE, EOBBMA, GSASMA, MPMA, AOBLMOA) were a dead end. Every variant is derived
from `NewDefaultConfig` and so inherited the same fixed `NC`, which means the
variant comparison that showed no differences was not a fair test of the
variants. It has not yet been re-run.
