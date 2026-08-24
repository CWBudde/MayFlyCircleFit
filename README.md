# MayFlyCircleFit

MayFlyCircleFit approximates a reference image with colored circles using the
[Mayfly](https://github.com/cwbudde/mayfly) optimizer. The default CPU renderer
supports joint, sequential, and batch optimization. An experimental OpenCL
renderer is available for all three modes in GPU-tagged builds.

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
separate templ installation. MayFly is pinned to `v0.7.0`.

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
`target_cost`, `stagnation`, or `stage_convergence`.

Use `--variant` to select the MayFly algorithm variant: `standard`, `desma`,
`olce`, `eobbma`, `gsasma`, `mpma`, or `aoblmoa`.

### Initial population

`--qmc-init` selects how a MayFly run draws its first generation: `uniform`
(the default), `sobol`, or `halton`. Uniform takes every coordinate as an
independent random draw. The other two take the whole population from a
low-discrepancy sequence, which covers the search box more evenly for the same
number of evaluations -- the standard cheap addition to a population-based
optimizer. It is MayFly-only and refused under `--optimizer dragonfly`.

**This is an expert knob with no measurement on circle fitting.** MayFly's own
benchmark study finds a chance-level effect, and the mechanism is weakest in
the regime this project usually runs: what an even sample buys is largest when
the population is small relative to the dimension, and a `--pop-size` of 1024
over a 56-dimension batch stage is the opposite of that. Where it is worth
trying is a small population on a short restart. Do not read a single campaign
as evidence for it, and see
[`docs/known-limitations.md`](docs/known-limitations.md).

Setting it keeps a run reproducible -- the sequence's scramble is drawn from
the run's own seeded generator, not from the clock -- but a quasi-random run is
not comparable to the uniform run of the same seed, because a different
starting population consumes the generator differently from the first
iteration onwards. Pair strategies across seeds rather than assuming a shared
trajectory.

### Restarts

`--restarts N` runs each optimizer invocation as N independent cold attempts
and keeps the best. It is not `--optimizer-epochs`: an epoch reseeds the next
run from the best candidate found so far and therefore inherits that
candidate's basin, while a restart draws a fresh population and explores
independently. Both can be combined -- restarts wrap epochs, so one attempt is
a whole epoch chain.

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
### Crossover count

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

### Advanced MayFly parameters

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


## Checkpoints and restart-from-best

Checkpoint files record the best candidate and measured progress. Resume does
not restore the Mayfly algorithm's entire internal state. It starts a new,
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
internal/opt/           Mayfly adapter and lifecycle contract
internal/server/        Trusted-local HTTP server, jobs, and SSE
internal/store/         Checkpoints, traces, and artifacts
internal/ui/            templ source and committed generated Go
web/                    TypeScript/React islands and browser tests
assets/                 Small example input
docs/                   Support, limitations, and design notes
```

See the [architecture guide](docs/architecture.md),
[CONTRIBUTING.md](CONTRIBUTING.md), [CHANGELOG.md](CHANGELOG.md), the
[release process](docs/releasing.md), and [PLAN.md](PLAN.md) for system
boundaries, contribution checks, current work, and publishing status.
