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
| `name` | A human label carried onto the stage table, at most 200 characters. No effect on execution. |
| `seed` | The campaign seed, inherited by every stage. Zero resolves one and records it per stage. |
| `base` | The first run's configuration — the same `JobConfig` `POST /api/v1/jobs` takes. |
| `steps` | The ordered continuations. Optional; a document with none is a single run. |

A campaign has exactly one seed. `seed` and `base.seed` may both be written only
if they agree; disagreeing is an error, not a precedence rule to memorize.

A document with steps must run its base in `"mode": "batch"`, because both
continuation paths require a completed batch checkpoint.

Limits: at most 256 authored steps, 1024 repetitions per step, and 4096 realized
stages. The document itself is at most 128 KiB, because `GET
/api/v1/schedules/:id` carries it in full — the finish projection is computed
from the plan it expands to — and it shares that response with a stage listing
that reaches roughly 706 kB at 4096 stages. Both bounds together are what keeps
`schedule status` able to print any campaign this format allows. A document that
needs more than 128 KiB is one written stanza by stanza where `repeat` says the
same thing in a line.

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
| `epochs`, `iters`, `popSize` | Budget overrides; on a polish these address the *polishing* budget, so `popSize` sets `polishingPopSize` rather than the job-wide population a polish-only stage never spends. |
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

## Starting from a hand-placed arrangement

`base.initialCircles` seeds the campaign from circles someone placed by hand
instead of from a random population. It is the only way to supply explicit
circle parameters: every other warm start in the system comes from a checkpoint,
and a checkpoint is written by a run, not by an operator.

```json
"base": {
  "mode": "batch",
  "circles": 8,
  "initialCircles": [
    {"x": 256, "y": 256, "r": 400, "color": "#c8cbd0"},
    {"x": 256, "y": 660, "r": 300, "color": "#232650", "opacity": 1}
  ]
}
```

Entries are painted back to front, exactly like the parameter vector they
become: the first is the backdrop and the last is on top. `opacity` is optional
and defaults to 1. A centre may sit off-canvas — half a canvas dimension past
each edge, the same bound the optimizer explores under — which is how a large
circle contributes only its cap.

Four rules keep the field honest:

- **Batch mode only, and exactly `circles` entries.** Batch is the mode whose
  optimizer receives the whole vector at once. A sequential or joint run builds
  its vector as it goes, so a full arrangement handed to one would be partly
  ignored — worse than refused, because the run would look seeded and not be.
- **`batchSize` must cover every circle.** The same argument one level down: a
  batch smaller than the circle count optimizes the vector in chunks and would
  seed the first chunk only. Omit `batchSize` and it follows the seed instead of
  the default of five; set it smaller and the configuration is refused.
- **Out of bounds is refused, not clamped.** Clamping is right for a candidate
  the optimizer proposed; a hand-placed circle that silently moves is a run that
  no longer matches the document describing it.
- **The base stage only.** Expansion clears the field on every later stage, and
  the worker prefers a parent's parameters over any spec that reached it anyway.
  A continuation is seeded from its parent checkpoint, always.

`initialCircles` changes where a run starts, not what it costs to run. The base
stage still executes its configured budget; giving it `"iters": 1` makes it a
recording step, so the campaign's first stage is the authored arrangement and
its cost, and the polish steps that follow do the work.

Score an arrangement before committing it to a campaign:

```sh
mayflycirclefit score --ref example/Christian_after.jpeg \
    --circles example/christian-16-handcrafted-v6.json --out preview.png
```

`score` takes either a bare JSON array of specifications or a schedule document,
in which case it reads `base.initialCircles`. It renders with the same CPU
renderer a run uses — including the base stage's `canvasPath`, so the cost is
measured against the canvas the campaign will actually start from — and prints
the cost, the PSNR, and the blank-canvas cost the arrangement improved on.
Scoring the campaign file directly is the point: the number then describes the
document that will actually run.

## `pauseBefore`: a barrier

`pauseBefore` on a step makes the stage it produces a barrier. The campaign runs
everything before it and then pauses; the barred stage and everything after it
stay planned but unstarted, and `schedule resume` releases it.

```json
{"type": "polish", "strategy": "replacement"},
{"type": "extend", "additionalCircles": 4, "pauseBefore": true}
```

It is how a document says "go this far for now" without the plan having to be
edited down and edited back — which matters because the edited-back version is a
different document, and a campaign is only comparable to another campaign that
planned the same stages.

A dry run states it before the table, so how far a run will get is not something
a reader has to infer from fourteen rows:

```
Barrier: runs stages 0-4, then pauses before stage 5 (extend, 12 circles).
         Everything after it stays planned; `schedule resume` releases it.
```

Three properties are worth knowing:

- **It is not a `when` condition.** A condition decides whether a stage runs at
  all, and is refused on extend because skipping one would move every later
  stage's circle count. A barrier skips nothing — it only stops — so it is legal
  on either kind.
- **Nothing is recorded for the barred stage.** The check runs before policy and
  before the stage record is written, so a paused campaign leaves no
  half-decided stage behind and the next resume starts exactly there.
- **On a repeated step it marks the first repetition only.** `repeat: 4` with a
  barrier means "stop before the first of the four", which is what makes it
  readable as a single point in the plan.

The pause is durable and pollable, which is what makes it a signal rather than
just a stop. `schedule status` reports it, and the reason names the stage:

```
State: paused
Paused: paused at the barrier before stage 5 (extend, 12 circles); resume to continue
```

Releasing happens as part of the resume: the schedule record carries
`releasedThroughStage`, so a resumed campaign runs past the barrier instead of
meeting it again and pausing forever.

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

**Its `refPath` is a placeholder — replace it before submitting.**
`assets/ref.png` is not in the repository: the reference the second incremental
run actually used lived only on the compute box whose working directory was
destroyed on 2026-08-17, and the one committed image, `assets/test.png`, is a
50×50 unit-test fixture that no 512-circle campaign should be pointed at. The
example is checked in so the parser and the documented format cannot drift, and
neither `ParseSchedule` nor `schedule create --dry-run` opens the reference, so
both work on the file as it stands. `POST /api/v1/schedules` does resolve it,
eagerly and on purpose, and refuses an unreachable path up front rather than
failing a stage hours in — so point `base.refPath` at your own reference first.

`schedule create --dry-run` expands it without opening a socket:

```
$ mayflycirclefit schedule create --dry-run docs/examples/512-circle-campaign.json
Dry run of docs/examples/512-circle-campaign.json — nothing was submitted and no schedule was created.
Name: 512-circle campaign
Seed: 4242
Stages: 70 (1 base, 63 extend, 6 polish; 6 conditional)

#   KIND    CIRCLES  ITERATIONS  PARAMETERS                                                   WHEN
0   base    8        200         batch, batch 8, 1 × 200 iters, pop 30                        always
1   extend  16       200         +8 circles, batch 8, 1 × 200 iters, pop 30                   always
2   extend  24       200         +8 circles, batch 8, 1 × 200 iters, pop 30                   always
3   extend  32       200         +8 circles, batch 8, 1 × 200 iters, pop 30                   always
4   polish  32       3200        replacement, active set 5, 8 sweeps × 2 × 200 iters, pop 30  conditional: only at 32/64/96/128/192/256 circles; abandoned after 2 consecutive stages gaining less than 1
...
69  extend  512      200         +8 circles, batch 8, 1 × 200 iters, pop 30                   always

Planned optimizer iterations (nominal): 32000
  unconditional: 12800
  conditional:   19200 across 6 stages, decided at run time
```

70 stages and 32,000 iterations, which is:

```
stages   1 base + 63 extends + 6 polishes                     =    70
base     1 batch run × 1 epoch × 200 iters                    =   200
extend   1 batch run × 1 epoch × 200 iters, × 63              = 12600
polish   8 sweeps × 2 epochs × 200 iters, × 6                 = 19200
total                                                          = 32000
```

63 extends because the canvas climbs 8 → 512 in steps of 8, and
(512 − 8) / 8 = 63. The arithmetic is written out in
`internal/app.TestReferenceCampaignPlanMatchesTheHandComputation`.

The figure is the budget the configuration *authorizes*, not a prediction. Early
stopping and convergence detection can only spend less, and the six polish
stages are conditional — a campaign whose polishing stops paying spends less
than half of it.

## Comparing two campaigns

A cost is only comparable to another cost produced by the same renderer and
the same optimizer, and none of what follows is visible in the schedule
document or in a checkpoint. Record it alongside the run.

The startup log is the only place these travel together. `serve` logs the
build version and the linked MayFly version; `run` logs the MayFly version on
its `Starting optimization` line, next to the seed. The SIMD tier and both
installed compositors are logged at debug level, and `fastCompositing` comes
from the job configuration, so capture the log at debug level — or query
`GET /api/v1/system`, which reports the tier, both compositors, and the build
in one response — if you intend to compare across machines.

Two records with identical seeds and identical renderer settings can still lie
on opposite sides of the optimizer boundary below, and nothing persisted will
say so. A campaign whose optimizer version was never captured cannot be made
comparable after the fact; treat it as its own baseline.

- **The optimizer version — the one that breaks comparability across a
  dependency bump.** MayFly v0.5.0 scales the crossover offspring count with
  the population, where v0.4.0 held it at an absolute 20. A run at the default
  population of 20 is unaffected, but every run at any other population
  performs a different number of crossovers and converges differently, so a
  cost recorded under v0.4.0 is **not** comparable to one recorded under
  v0.5.0. Every campaign in this repository ran at a raised population, so the
  boundary is real rather than theoretical. Pin `config.NC = 20` inside the
  adapter to reproduce a v0.4.0 run exactly.
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
- **`parallelEvaluation` — the mode must match, the worker count need not.**
  Parallel evaluation reproduces bit-identically for a fixed seed at *any*
  evaluation-worker count, but its trajectory differs from a serial run of the
  same seed, because MayFly holds the global best fixed for a whole parallel
  generation. So a parallel campaign is not comparable to a serial one of the
  same seed, while two parallel campaigns of that seed are — including at
  different `evaluationWorkers`.
- **`threads` — comparable.** Threads shard the rows of one render into disjoint
  bands, and every worker composites every circle in index order within its own
  rows, so the image is pixel-exact at any thread count. The cost is then
  reduced once, single-threaded, in row-major order over integer-valued data
  (`CPURenderer.Cost`), so the thread count is not a summation order and cannot
  move a cost. `renderer_correctness_test.go` compares one worker against
  `GOMAXPROCS` with exact equality. Do not reject a comparison because two
  campaigns used a different `threads`.

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
- [`architecture.md`](architecture.md) — how schedules, ordinary jobs,
  persistence, and campaign read models fit together.
- `PLAN.md` Phase 16 — current schedule follow-up work.
