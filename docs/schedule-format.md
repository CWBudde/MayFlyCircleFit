# The schedule document format

A **schedule** states a whole incremental campaign once — a base run and an
ordered list of `extend` and `polish` continuations — and hands it to the server
to execute as a single observable run. It replaces the throwaway Python
orchestrators that drove the first two incremental runs from outside the system.

This page is the format reference. The executor's behavior (crash safety,
adoption, the job limit) is in [`behavior-invariants.md`](behavior-invariants.md);
the surfaces that consume a schedule are `schedule` on the CLI and
`/api/v1/schedules` over HTTP.

The worked example is a real file:
[`examples/512-circle-campaign.json`](examples/512-circle-campaign.json). It is
parsed by `internal/app.TestDocumentedExamplePlansTheReferenceCampaign` and by
`cmd.TestScheduleDryRunListsTheReferenceCampaign`, so this document cannot
describe a format the parser would refuse.

## The document

```
{
  "schemaVersion": 1,
  "name": "512-circle campaign",
  "seed": 4242,
  "base": { ... a JobConfig ... },
  "steps": [ ... ]
}
```

| Field | Meaning |
| --- | --- |
| `schemaVersion` | `1`, or omitted for the current version. Any other value is refused rather than guessed at. |
| `name` | A human label carried onto the stage table. No effect on execution. |
| `seed` | The campaign seed, inherited by every stage. Zero resolves one and records it per stage. |
| `base` | The first run's configuration — the same `JobConfig` `POST /api/v1/jobs` takes. |
| `steps` | The ordered continuations. Optional; a document with none is a single run. |

A campaign has exactly one seed. `seed` and `base.seed` may both be written only
if they agree; disagreeing is an error, not a precedence rule to memorize.

A document with steps must run its base in `"mode": "batch"`, because both
continuation paths require a completed batch checkpoint.

Limits: at most 256 authored steps, 1024 repetitions per step, and 4096 realized
stages.

## Steps

Each step is `{"type": "extend"}` or `{"type": "polish"}`, and its override
fields mirror the `/extend` and `/polish` request bodies field for field. A step
you can read is a request you have already issued by hand.

`repeat` is the generator form: `{"type": "extend", "repeat": 63,
"additionalCircles": 8}` is a 63-stage climb in one stanza, not 63 stanzas.
Omitting it means one stage.

**Extend steps**

| Field | Meaning |
| --- | --- |
| `additionalCircles` | Required, positive. How many circles this stage appends. |
| `batchSize` | Overrides the append width, which otherwise equals `additionalCircles`. |
| `epochs`, `iters`, `popSize` | Budget overrides. |

The existing circles are a frozen prefix; an extend optimizes only what it
appends.

**Polish steps**

| Field | Meaning |
| --- | --- |
| `strategy`, `activeSetSize`, `maxSweeps`, `stagnationIters`, `minImprovement` | Polishing overrides. |
| `epochs`, `iters`, `popSize` | Budget overrides; on a polish these address the *polishing* budget. |
| `when` | The runtime condition. See below. |

An extend override on a polish step, or a polish override on an extend step, is
an error rather than a silently ignored field.

## `when`: policy, not mechanism

Both rules below were hardcoded constants in the Python orchestrator, and both
are policy — changing one used to mean killing and restarting the run.

```json
{"type": "polish", "when": {"circles": [32, 64, 96, 128, 192, 256], "minGain": 1.0, "abortAfterBarren": 2}}
```

- `circles` runs the step only when the canvas holds one of those counts.
- `minGain` with `abortAfterBarren` abandons the step for the rest of the
  campaign once that many consecutive stages of its kind each gained less than
  the threshold. The two are meaningless apart and must be given together.

`when` is valid on a **polish step only**. Skipping a polish leaves the canvas
exactly as the plan predicted; skipping an extend would move the circle count of
every later stage and make the realized run disagree with its own plan.

A condition does not affect expansion. The stage is planned either way, printed
by a dry run marked `conditional:` with its condition spelled out, and decided
against the recorded outcomes when it comes up. A skipped stage is recorded with
the reason it was declined, so the records account for every planned stage.

## Two validation traps worth knowing

**A field the defaults would replace is an error.** The parser refuses any
written field that `ApplyDefaults` would overwrite, and names the field that
actually works. The canonical case:

```
base.convergenceEnabled is silently replaced by the configuration defaults;
use disableConvergence instead
```

`convergenceEnabled: false` is `omitempty` and re-enabled by `ApplyDefaults`
(`internal/app/config.go`). The effective lever is `disableConvergence: true`.
Hours were lost to that silent drop; a schedule refuses rather than repeats it.

**Unknown fields are refused** at every level, exactly as the HTTP request types
refuse them. A typo is an error, not a dropped setting.

## Worked example: the 512-circle campaign

[`examples/512-circle-campaign.json`](examples/512-circle-campaign.json) is the
second incremental run's policy stated once: base 8 circles, `+8` to 512, polish
at 32/64/96/128/192/256 with `minGain: 1.0` and `abortAfterBarren: 2`.

The extends are written as several stanzas rather than one `repeat: 63` because
the polishes sit between them; each stanza climbs to the next polish point.

`schedule create --dry-run` expands it without opening a socket:

```
$ mayflycirclefit schedule create --dry-run docs/examples/512-circle-campaign.json
Dry run of docs/examples/512-circle-campaign.json — nothing was submitted and no schedule was created.
Name: 512-circle campaign
Seed: 4242
Stages: 70 (1 base, 63 extend, 6 polish; 6 conditional)

#   KIND    CIRCLES  ITERATIONS  PARAMETERS                                                    WHEN
0   base    8        200         batch, batch 8, 1 × 200 iters, pop 30                         always
1   extend  16       200         +8 circles, batch 8, 1 × 200 iters, pop 30                    always
2   extend  24       200         +8 circles, batch 8, 1 × 200 iters, pop 30                    always
3   extend  32       200         +8 circles, batch 8, 1 × 200 iters, pop 30                    always
4   polish  32       6000        replacement, active set 5, 3 sweeps × 2 × 1000 iters, pop 30  conditional: only at 32/64/96/128/192/256 circles; abandoned after 2 consecutive stages gaining less than 1
...
69  extend  512      200         +8 circles, batch 8, 1 × 200 iters, pop 30                    always

Planned optimizer iterations: 48800
  unconditional: 12800
  conditional:   36000 across 6 stages, decided at run time
```

70 stages and 48,800 iterations, which is:

```
stages   1 base + 63 extends + 6 polishes                     =    70
base     1 batch run × 1 epoch × 200 iters                    =   200
extend   1 batch run × 1 epoch × 200 iters, × 63              = 12600
polish   3 sweeps × 2 epochs × 1000 iters, × 6                = 36000
total                                                          = 48800
```

63 extends because the canvas climbs 8 → 512 in steps of 8, and
(512 − 8) / 8 = 63. The arithmetic is written out in
`internal/app.TestReferenceCampaignPlanMatchesTheHandComputation`.

The figure is the budget the configuration *authorizes*, not a prediction. Early
stopping and convergence detection can only spend less, and the six polish
stages are conditional — a campaign whose polishing stops paying spends less
than half of it.

## Comparing two campaigns

A cost is only comparable to another cost produced by the same renderer, and
none of what follows is visible in the schedule document. Record it alongside
the run: the startup log names the SIMD tier, both installed compositors, and
`fastCompositing`, so one log line settles whether two campaigns can be
compared.

- **`fastCompositing` — the one that genuinely breaks comparability.** The
  float32 compositor is accurate to ±1 per channel, measured over 2,074,320
  channel writes. A changed channel changes the SSD, which changes an
  accept/reject decision, so two runs with the same seed and different
  compositing settings converge to different circle sets and different final
  costs. Fast-compositing costs are **not** comparable to exact-compositor
  costs — not within a tolerance, not approximately. See
  [`task-10.18-exact-compositor.md`](task-10.18-exact-compositor.md). On ARM64
  the flag is a pure loss as well: there is no float32 NEON kernel, so it falls
  back to a float32 scalar loop that is both slower and less accurate than the
  default.
- **SIMD tier — comparable *within* an architecture, and only by construction.**
  Every shipped kernel on the default path is byte-identical to its scalar
  oracle: SSD, delta-SSD and circle-span since
  [`task-10.17-sse2-report.md`](task-10.17-sse2-report.md), and the exact span
  compositors since [`task-10.19`](task-10.19-sse2-compositor.md) and
  [`task-10.18`](task-10.18-exact-compositor.md). So swapping AVX2 for SSE2 for
  scalar on one machine does not move a cost. That is a property the current
  kernels hold and parity tests pin — `TestCompositeSpanExactFusionContract`
  among them — not a guarantee the schedule format makes, and a future inexact
  kernel would end it silently. `MAYFLY_SIMD_TIER` forces a tier and
  `MAYFLY_REQUIRE_SIMD_TIER` asserts the detected one.
- **Architecture — not comparable.** The parity above is each kernel against
  *its own architecture's* scalar loop, not against the other architecture's.
  Go's amd64 backend does not contract `a*b+c`, so the blend is MUL+ADD there;
  the arm64 backend does, so it is an FMA. The two round differently. An amd64
  cost and an arm64 cost are different numbers for the same fit.
- **Renderer version, where parity was not the contract.** Compositor work is
  safe to compare across, because byte-parity was the acceptance condition for
  each kernel. A change to how the cost itself is accumulated is not covered by
  that — the delta-SSD accumulator change noted in
  [`task-10.17-sse2-report.md`](task-10.17-sse2-report.md) is the example, and
  the report marks an earlier measurement as predating it for exactly this
  reason. Cite the revision when a cost is meant to be compared later.
- **`parallelEvaluation` and `threads`.** Parallel evaluation reproduces
  bit-identically for a fixed seed *and* a fixed worker count, but its
  trajectory differs from a serial run of the same seed, because MayFly holds
  the global best fixed for a whole parallel generation. `threads` shards the
  rows of one render, so a different thread count is a different summation
  order. Pin both when two runs must be compared exactly.

`internal/server.TestScheduleReproducesTheHandDrivenCampaign` is the standing
demonstration: the same campaign driven through the schedule executor and driven
by hand through `POST /api/v1/jobs`, `/extend`, `/extend`, `/extend`, `/polish`
produces cost sequences that match **exactly**, with no tolerance, because it
pins the seed, `threads: 1`, serial evaluation, and the exact compositor.

### Historical baseline numbers

Figures quoted from the first two incremental runs — notably the 512-circle
run's final cost of 161.99 — are **documented figures, not checkable
artifacts**. The parameter vectors and checkpoints they came from were destroyed
with the compute box's working directory on 2026-08-17. They cannot be
re-derived, re-rendered, or diffed against a new run, and the caveats above mean
a fresh run of the same document would not be expected to reproduce them even if
they could be. Treat them as history, and cite them as such.

## See also

- [`behavior-invariants.md`](behavior-invariants.md) — determinism, parallel
  evaluation, SIMD dispatch, and the server trust boundary.
- [`rendering-internals.md`](rendering-internals.md) — the SSD kernels and span
  compositors whose choice the caveats above turn on.
- [`task-10.18-exact-compositor.md`](task-10.18-exact-compositor.md) — the
  measured ±1-per-channel bound and why the flag breaks reproducibility.
- `PLAN.md` Phase 16 — why schedules exist and what each task delivered.
