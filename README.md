# MayFlyCircleFit

MayFlyCircleFit approximates a reference image with colored circles. The
[MayFly](https://github.com/cwbudde/mayfly) optimizer is the default; CMA-ES and
an experimental Dragonfly adapter are selectable with `--optimizer`, and
[Optimizer engines](#optimizer-engines) says which settings belong to which. The
default CPU renderer supports joint, sequential, and batch optimization. An
experimental OpenCL renderer is available for all three modes in GPU-tagged
builds.

The project is under active production-readiness remediation. Read the
[support matrix](docs/support-matrix.md) and
[known limitations](docs/known-limitations.md) before relying on it for long or
unattended runs, and [troubleshooting](docs/troubleshooting.md) when a run or a
request fails.

## Requirements

- Go 1.24 or newer for source compatibility; use a currently supported,
  security-patched Go release for production binaries
- Git, if you want to run generated-file consistency checks
- `just` is optional; every essential command also has a direct Go equivalent

The repository pins templ `v0.3.960` as a Go tool and commits generated
`internal/ui/*_templ.go` files. A clean checkout therefore builds without a
separate templ installation. MayFly is pinned to `v0.7.1`.

## Quick start

Build and run a small deterministic CPU job:

```sh
git clone https://github.com/CWBudde/MayFlyCircleFit.git
cd MayFlyCircleFit
go build -o mayflycirclefit .
./mayflycirclefit run \
  --ref assets/test.png \
  --out out.png \
  --mode joint \
  --backend cpu \
  --threads 1 \
  --circles 1 \
  --iters 1 \
  --pop 20 \
  --seed 42
```

`out.png` is the fitted image. A seed of zero asks the application to generate
and report an effective seed; provide a nonzero seed for repeatable runs.

Equivalent convenience commands are `just build`, `just test`, `just lint`,
and `just check`.

Run the opt-in release lifecycle test separately with `just test-e2e`. It
builds the CLI in a temporary directory, starts and restarts the real server,
and verifies create, SSE progress, checkpoint, cancellation, resume, and PNG
artifact retrieval. The test is intentionally excluded from the ordinary
short suite and has a three-minute Go test timeout.

When building an extracted `git archive` rather than a Git checkout, use
`go build -buildvcs=false ./...` (the `just build` recipe already does this),
because there is no repository metadata to stamp into the binary.

## Commands

- `run` performs a local optimization and writes a PNG.
- `serve` starts the trusted-local HTTP UI and job API.
- `status [job-id]` lists server jobs or shows one job.
- `resume <job-id>` restarts from a saved best candidate, remotely or with
  `--local`.
- `checkpoints list` and `checkpoints clean` inspect or prune persisted jobs.
- `version` and the root `--version` flag print version, commit, and build-date
  metadata. Source builds identify themselves as development builds.

Use `./mayflycirclefit <command> --help` for the complete flag set.

CPU rendering shards the image into horizontal scanline bands. `run --threads`
controls the worker count and defaults to `GOMAXPROCS`; the effective value is
capped at `GOMAXPROCS` and the image height. Use `--threads 1` for small inputs,
where coordination can cost more than it saves. See the [threading benchmarks
and guidance](docs/cpu-rendering-threads.md).

Run `just benchmark` for the canonical CPU rendering, cost, and optimization
pipeline suite. See the [benchmark guide](docs/benchmarks.md) for workload and
regression-comparison details.

## Trusted-local server

Server mode is designed for a trusted local machine. It has no authentication
or TLS and must not be exposed directly to an untrusted network.

```sh
GOMEMLIMIT=8GiB ./mayflycirclefit serve \
  --addr localhost \
  --port 8080 \
  --input-root ./assets \
  --data-root ./data
```

The default bind address is `localhost`. Browser requests with a foreign
`Origin` are rejected, and reference/canvas paths are canonicalized beneath one
of the repeatable `--input-root` directories. Job concurrency and queue length
are bounded by `--max-jobs` and `--queue-size`. Profiling routes are disabled by
default; `--enable-pprof` is accepted only with a loopback bind address. Size
the optional `GOMEMLIMIT` below the memory the host can spare; it is a soft Go
heap backstop, not a hard RSS cap.

A minimal API job can be submitted from the checkout directory with:

```sh
curl -fsS http://localhost:8080/api/v1/jobs \
  -H 'Content-Type: application/json' \
  --data '{"refPath":"assets/test.png","mode":"joint","backend":"cpu","threads":1,"circles":1,"iters":1,"popSize":20,"seed":42}'
```

This same-origin check is a browser defense, not an authentication boundary.
Command-line clients normally omit `Origin` and are allowed.

Send only the fields you have an opinion on. The defaults fill fields a request
omits, so a field written as `0` is a request for zero, not an omission, and is
refused with `invalid_config` naming it — which also means marshalling a
zero-valued configuration struct is not a valid partial request.

Progress can be observed either by polling the status resource or by keeping an
SSE connection open. SSE is an optional second transport; polling remains
available for clients that cannot maintain streaming HTTP connections.

The jobs page loads 100 compact summaries at a time as you scroll. API clients
can opt into the same bounded cursor response with
`GET /api/v1/jobs?limit=100`; follow its opaque `nextCursor` until it is absent.
Requests without `limit` retain the legacy JSON-array response for compatibility.

```sh
# Polling
curl -fsS http://localhost:8080/api/v1/jobs/JOB_ID/status

# Live events (iteration, best cost, circles/second, and state)
curl -N http://localhost:8080/api/v1/jobs/JOB_ID/stream
```

The stream sends an immediate snapshot, at most one optimization progress event
per 500 ms, and a final `completed`, `failed`, or `cancelled` event before it
closes. A comment heartbeat is sent every 30 seconds while otherwise idle.

Recent pages and API endpoints:

- `GET /` renders the dashboard with live aggregate metrics and running jobs.
- `GET /jobs` lists all jobs.
- `GET /api/v1/dashboard` returns the JSON model backing the dashboard.
- `GET /api/v1/system` returns runtime and host capability facts.
- `GET /api/v1/stream` streams global progress updates for all running jobs.
- `GET /static/dashboard.js` serves the committed React island bundle (versioned
  with a short content hash).
- `GET /static/dashboard.js.map` serves that bundle's source map, so a minified
  island stack trace maps back to `web/src`. The bundle links it, so only a
  browser with devtools open ever downloads it.

### Run schedules

A whole incremental campaign -- a base run plus an ordered list of `extend` and
`polish` continuations -- can be stated once and run unattended by the server.
The client may disconnect; the campaign keeps going, and it shares `--max-jobs`
with ordinary jobs.

```sh
curl -fsS http://localhost:8080/api/v1/schedules \
  -H 'Content-Type: application/json' \
  --data '{
    "name": "8 to 32",
    "seed": 42,
    "base": {"refPath":"assets/test.png","mode":"batch","circles":8,"batchSize":8,"iters":200,"popSize":30},
    "steps": [{"type":"extend","repeat":3,"additionalCircles":8}]
  }'

# The stage table is the campaign's progress; there is no second state file.
# The listing is a projection -- index, kind, state, circles, cost, elapsed and
# job -- so it stays readable for a campaign of any allowed length.
curl -fsS http://localhost:8080/api/v1/schedules/SCHEDULE_ID

# The configuration a single stage ran with, which is what replays that stage.
curl -fsS http://localhost:8080/api/v1/schedules/SCHEDULE_ID/stages/7

curl -fsS -X POST http://localhost:8080/api/v1/schedules/SCHEDULE_ID/pause
curl -fsS -X POST http://localhost:8080/api/v1/schedules/SCHEDULE_ID/resume
curl -fsS -X POST http://localhost:8080/api/v1/schedules/SCHEDULE_ID/cancel
```

Pausing stops at the next stage boundary and lets the in-flight stage finish;
cancelling also cancels the in-flight stage. Restarting the server resumes any
schedule that was running, continuing the stage it was on rather than starting a
second one.

## Backends and optimization modes

| Backend | Joint | Sequential | Batch | Notes |
| --- | --- | --- | --- | --- |
| CPU | Supported | Supported | Supported | Custom base canvases are supported |
| OpenCL | Experimental | Experimental | Experimental | Requires `-tags gpu`, CGO, OpenCL headers and a runtime; no custom base canvas |

Sequential and batch OpenCL runs create independent device sessions and replay
retained circles at each stage. They remain experimental and have not been
performance-characterized on vendor GPUs. See [GPU backend
notes](docs/gpu-backends.md) for setup details.

```sh
CGO_ENABLED=1 go build -tags gpu -o mayflycirclefit .
./mayflycirclefit run --ref assets/test.png --backend opencl --mode sequential
```

## Early stopping

Two independent mechanisms can end a run before its budget is spent. They count
different things, so they have separate flags.

| Mechanism | Flags | Counts | Improvement | Applies to | Default |
| --- | --- | --- | --- | --- | --- |
| Stage-level convergence | `--convergence`, `--patience`, `--threshold` | circles or batches | relative ratio | sequential, batch | on |
| Optimizer-level stopping | `--stop-*` | iterations | absolute cost | all modes | off |

Optimizer-level stopping is disabled unless you ask for it, so default runs stay
reproducible for a given seed:

```sh
# Stop once the optimizer stalls for 25 iterations, but never before iteration 50.
./mayflycirclefit run --ref assets/test.png --iters 500 \
  --stop-stagnation-iters 25 --stop-min-iters 50

# Stop as soon as the cost reaches a known-good value.
./mayflycirclefit run --ref assets/test.png --stop-target-cost 1200
```

`--stop-target-cost` and `--stop-min-improvement` use the same cost units shown
by `status` and written to the trace. In sequential and batch modes these apply
to each stage, so they can shorten stages without ending the run. `status` and
`checkpoints list` report why a run stopped: `completed`, `cancelled`,
`target_cost`, `stagnation`, `convergence`, or `stage_convergence`. Only
CMA-ES reports `convergence`, for its own distribution-aware criteria.

## Optimizer engines

`--optimizer` selects the search algorithm: `mayfly` (the default), `cmaes`, or
`dragonfly`. The same choice is available as the `optimizer` field of a JSON job
payload, of a schedule document's `base` configuration, and of the web creation
form. An absent field means `mayfly`, which is what every configuration and
checkpoint written before the field existed carries. A resume runs the engine
recorded in the checkpoint, whatever `--optimizer` asks for, so an optimizer
cannot change silently across a resume.

Most run flags are engine-agnostic. `--circles`, `--iters`, `--pop`, `--seed`,
`--mode`, `--backend`, `--batch-size`, the `--stop-*` family, `--restarts`,
`--optimizer-epochs`, and `--parallel-evaluation` mean the same thing for all
three. The rest belong to one engine:

| Flags | Engine |
| --- | --- |
| `--variant`, `--qmc-init`, `--crossover-count`, `--dance-damp`, `--aquila-weight`, `--opposition-probability`, `--polishing` | MayFly only |
| `--initial-sigma`, `--covariance-mode`, `--active-cma`, `--restart-strategy` | CMA-ES only |

Naming one of them alongside a different engine is refused at validation rather
than accepted and ignored: a usage error from the CLI, an `invalid_config`
envelope from the API, and a rejected schedule document. A setting that never
reached the optimizer would otherwise be persisted into the checkpoint and
reported back unchanged, which makes every cost it produced impossible to
compare.

**No engine ranking is established on this problem.** The only measurement that
puts CMA-ES against MayFly,
[`docs/cmaes-preliminary-report.md`](docs/cmaes-preliminary-report.md), is a
single paired seed block from a campaign that was stopped by operator request,
and it reports descriptive observations only -- no means, no variance, no test.
In that block full-covariance CMA-ES reached a lower cost than both MayFly arms
while spending 22% of the shared evaluation cap, and an IPOP run was lower still
when it was interrupted. One block cannot estimate seed variance, the IPOP
figure is right-censored, and no separable-covariance arm ran at all. Treat it
as a reason to measure, not as a default to change. Dragonfly is the one engine
that *has* been settled: twelve blocks against MayFly `standard`, all lost (see
below).

### MayFly

The default, [`github.com/cwbudde/mayfly`](https://github.com/cwbudde/mayfly)
pinned to v0.7.1, and the engine every measurement report in `docs/` was taken
under. Everything in this section is MayFly-only, as is polishing, which runs
its own MayFly population by construction.

Use `--variant` to select the MayFly algorithm variant: `standard`, `desma`,
`olce`, `eobbma`, `gsasma`, `mpma`, or `aoblmoa`.

#### Initial population

`--qmc-init` selects how a MayFly run draws its first generation: `uniform`
(the default), `sobol`, or `halton`. Uniform takes every coordinate as an
independent random draw. The other two draw from a low-discrepancy sequence,
which covers the search box more evenly for the same number of evaluations --
the standard cheap addition to a population-based optimizer. It is MayFly-only
and refused under `--optimizer dragonfly`.

The sequence does not place the whole population. Whenever residual seeding
succeeds -- always for a batch stage, and for a cold joint or sequential base
stage as well as for a continuation -- the seed candidate is expanded into half
the male and half the female population, and only the remaining slots come from
the sequence. So `sobol` and `halton` change how the unseeded half is placed,
which halves whatever the even-coverage argument is worth here.

**This is an expert knob with no measurement on circle fitting.** MayFly's own
benchmark study finds a chance-level effect, and the mechanism is weakest in
the regime this project usually runs: what an even sample buys is largest when
the population is small relative to the dimension, and a `--pop` of 1024 over a
56-dimension batch stage is the opposite of that. Where it is worth
trying is a small population on a short restart. Do not read a single campaign
as evidence for it, and see
[`docs/known-limitations.md`](docs/known-limitations.md).

Setting it keeps a run reproducible -- the sequence's scramble is drawn from
the run's own seeded generator, not from the clock -- but a quasi-random run is
not comparable to the uniform run of the same seed, because a different
starting population consumes the generator differently from the first
iteration onwards. Pair strategies across seeds rather than assuming a shared
trajectory.

#### Crossover count

`--crossover-count N` sets how many crossover offspring MayFly produces per
iteration. Zero, the default, leaves the library's own scaling alone, which is
one offspring per population member.

The offspring count is the dominant per-iteration cost, so lowering it lowers
the evaluation budget proportionally. On the eight-circle base stage at a
population of 1024, about 64 offspring was statistically indistinguishable from
the library default while spending 25% fewer evaluations, and cutting it to 2
was significantly worse. See
[`docs/restart-vs-budget-report.md`](docs/restart-vs-budget-report.md).

Two caveats. The library draws its mutant pool from the offspring, so a count
below the mutant count starves mutation rather than merely reducing
recombination. And an odd count yields one fewer offspring, because the library
mates pairs. The setting applies to the optimizer stages only, not to polishing
sweeps, which run their own smaller population.

#### Advanced MayFly parameters

Three further knobs reach parameters the MayFly library normally keeps to
itself. They are worth knowing about and are not worth changing casually: the
defaults are the ones the algorithm was published and tuned with, and a bad
value degrades a run quietly rather than failing it.

| Flag | Applies to | Library default |
| --- | --- | --- |
| `--dance-damp` | every variant | 0.8 |
| `--aquila-weight` | `aoblmoa` only | automatic fitness test (deprecated knob) |
| `--opposition-probability` | `aoblmoa` only | none -- inert since MayFly v0.6.0 |

All three take a value between 0 and 1, and all three are left entirely to the
library unless you name the flag. That distinction matters here because zero is
a real setting for each of them, not an absence -- so unlike the other numeric
flags, these are read back from whether the flag appeared on the command line,
and the JSON fields are omitted rather than zero when unset.

`--dance-damp` is the per-iteration decay on the nuptial dance, the random walk
the leading male takes on top of its velocity. It therefore sets how fast the
swarm stops exploring: at the default of 0.8 the term keeps about a tenth of
its initial size after ten iterations. Raising it slows that decay. Note that
the library does not range-check this one at all, so the range is enforced
here instead. Above 1 the dance coefficient grows every iteration, but the
library clamps velocity to `VelMin`/`VelMax` and positions to the bounds, so
the search degenerates into a saturated random walk inside the box rather than
diverging. The bound is a guard against that degenerate mode, not against a
crash: MayFly runs such a configuration and returns finite results.

`--aquila-weight` is deprecated as of MayFly v0.6.0. It used to be the
probability that an AOBLMOA individual takes an Aquila step instead of the
ordinary MayFly velocity and position update. The library now decides that
branch with a deterministic fitness test, as its source paper defines it, and
an unset flag selects exactly that. Naming the flag reinstates the old random
branch at the probability you give, which is useful only for reproducing a
pre-v0.6.0 run.

`--opposition-probability` is inert as of MayFly v0.6.0. It used to be the
share of solutions reflected through the search space each iteration; the
library now applies stochastic opposition to every offspring instead, so it
range-checks whatever you pass and then never reads it. The flag is still
accepted rather than removed because job submission and schedules reject
unknown fields, and resume reads the value back out of existing checkpoints --
dropping it would refuse configurations that load today. Setting it has no
effect on a run.

Setting either of the AOBLMOA knobs on another variant is rejected rather than
ignored, so a configuration that would have done nothing fails at validation
instead of running silently and being persisted into a checkpoint.

Like `--crossover-count`, all three apply to the optimizer stages only, not to
polishing sweeps, which run their own smaller standard-variant population.

### CMA-ES

`--optimizer cmaes` runs covariance matrix adaptation evolution strategy from
[`github.com/CWBudde/go-cma-es`](https://github.com/CWBudde/go-cma-es), pinned
to v0.1.0. It carries a sampling distribution rather than a population of
candidates, so `--pop` sets the generation size and `--iters` the generation
cap. It also brings its own distribution-aware convergence criteria and can stop
well below that cap; that is the only case in which `status` reports
`convergence`.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--initial-sigma` | 0.3 | Initial step size, in the adapter's normalized [0,1] search box, so it means the same thing for every circle parameter. Must be finite and positive. |
| `--covariance-mode` | `full` | `full`, `separable`, or `block`. |
| `--active-cma` | `true` | Negative rank-mu adaptation: the worst samples of a generation actively shrink the covariance along their own directions. |
| `--restart-strategy` | `none` | `none`, `ipop`, or `bipop`. |

`full` learns every pairwise coordinate correlation, at quadratic storage and a
cubic eigendecomposition, so it is refused above **512 optimizer dimensions**.
That bound counts the dimensions of one search, not of the whole picture: a
joint run searches every circle at once at seven coordinates each and so reaches
the limit at 73 circles, a sequential run searches seven dimensions whatever the
circle count, and a batch run searches one batch -- `--batch-size` circles, or
every circle when the batch is wider than the picture. The bound is checked
against the configuration the defaults produce, so an automatic `--batch-size 0`
is resolved to the default batch before it is applied. Above the limit, `block`
gives each circle its own seven-coordinate block and learns no correlation
between circles, and `separable` learns one variance per coordinate and no
correlation at all. Both are linear in the dimension.

`ipop` and `bipop` are the published restart schedules. On a convergence
criterion the search restarts: IPOP doubles the population each time, and BIPOP
alternates those doubling runs with shorter randomized small-population ones,
always advancing whichever regime has spent fewer evaluations. They run *inside*
one optimizer invocation and share a single budget of `--iters` x `--pop`
evaluations across all of their internal runs, which is what separates them from
`--restarts`: a restart is a whole fresh invocation at the full budget. That is
why `--restarts` has to stay at 1 when either is selected, and a configuration
setting both is refused rather than silently multiplied. For fixed independent
attempts instead, keep `--restart-strategy none` and use `--restarts`.

CMA-ES honours `--optimizer-epochs`, parallel evaluation, the `--stop-*` family,
and continuation profiles, whose seeded fraction, perturbation sigma, coordinate
rate, and initial sigma it reads. A continuation profile's `maxVelocity` has no
CMA-ES analogue and is not applied. Polishing is MayFly-only, and a CMA-ES job
that asks for it is rejected rather than quietly left unpolished.

### Dragonfly

`--optimizer dragonfly` runs a proof-of-concept adapter over
[`github.com/CWBudde/dragonfly`](https://github.com/CWBudde/dragonfly) v0.1.0,
continuous DA. It supports the shared lifecycle, continuation, progress, epoch
and parallel-evaluation contracts, and nothing else: every MayFly-only and
CMA-ES-only flag above is refused alongside it, polishing included.

Its published behaviour is to explore well and exploit poorly -- the convergence
factor reaches zero at the halfway point of a run -- so a worse fit than MayFly
is the expected outcome, and it is now the measured one. On the eight-circle
base stage it loses all twelve blocks in every arm, by 431.68 cost points
(`t = -16.81`), even when handed more evaluations than the MayFly baseline; see
[`docs/dragonfly-poc-report.md`](docs/dragonfly-poc-report.md). It stays in the
tree as an expert-only alternative for further experiments, not as a candidate
default.

## Restarts and epochs

`--restarts N` runs each optimizer invocation as N independent cold attempts
and keeps the best. It is not `--optimizer-epochs`: an epoch reseeds the next
run from the best candidate found so far and therefore inherits that
candidate's basin, while a restart draws a fresh population and explores
independently. Both can be combined -- restarts wrap epochs, so one attempt is
a whole epoch chain.

Both are engine-agnostic; the measurement below is MayFly's. CMA-ES has a
second, different notion of restarting -- `--restart-strategy ipop`/`bipop`
restarts inside one invocation and shares a single evaluation budget across the
attempts -- and requires `--restarts 1` when either is selected. See
[CMA-ES](#cma-es).

Restarts multiply the work, so `--restarts 4` costs four times a single
attempt at the same `--iters`. To hold a budget fixed, divide `--iters` by the
restart count. On the eight-circle base stage that trade is strongly
favourable: the same budget spent as several short attempts beat one long run
by about 160 cost points, and four attempts beat a single run of sixteen times
their length while using 15% of its compute. See
[`docs/restart-vs-budget-report.md`](docs/restart-vs-budget-report.md) for the
measurement, its limits, and the population-collapse behaviour behind it.

A restarted run stays reproducible for a fixed `--seed`; the attempts vary the
seed deterministically rather than drawing fresh entropy.

## Checkpoints and restart-from-best

Checkpoint files record the best candidate and measured progress. Resume does
not restore the MayFly algorithm's entire internal state. It starts a new,
deterministically seeded population containing the saved best and nearby
variations, and keeps the historical best if the new run is worse. This is a
restart-from-best operation, not bit-for-bit continuation. Server resume of
sequential and batch jobs is currently unsupported.

## Development checks

```sh
go tool templ generate
git diff --exit-code -- 'internal/ui/*_templ.go'
test -z "$(gofmt -s -l .)"
go vet ./...
go test -short ./...
go test -race -short ./...
go build ./...
```

The CI workflow runs these gates, pinned `staticcheck`, a 50% aggregate coverage
floor with an uploaded profile, portable cross-builds, an OpenCL/PoCL GPU-tag
compile and focused runtime suite, pinned `govulncheck` under Go 1.26.6, and a
dedicated release-lifecycle E2E job equivalent to `just test-e2e`. Their
presence is not a claim that the current branch or release candidate has passed
them; consult the actual workflow result.

SemVer tags trigger the same complete matrix and can publish portable CPU
archives only after every required job succeeds. See the
[release process](docs/releasing.md) for the local packaging command, artifact
contents, tag format, and repository-policy boundary.

## Repository layout

```text
cmd/                    CLI commands
internal/app/           Shared configuration and validation
internal/fit/           Rendering and cost functions
internal/fit/renderer/  Optimization pipelines and renderer backends
internal/opt/           MayFly adapter and lifecycle contract
internal/server/        Trusted-local HTTP server, jobs, and SSE
internal/store/         Checkpoints, traces, and artifacts
internal/ui/            templ source and committed generated Go
web/                    TypeScript/React islands and browser tests
assets/                 Small example input
docs/                   Support, limitations, and design notes
```

[`docs/README.md`](docs/README.md) indexes every document in `docs/`, grouped by
what you came for. See also the [architecture guide](docs/architecture.md),
[CONTRIBUTING.md](CONTRIBUTING.md), [CHANGELOG.md](CHANGELOG.md), the
[release process](docs/releasing.md), and [PLAN.md](PLAN.md) for system
boundaries, contribution checks, current work, and publishing status.
