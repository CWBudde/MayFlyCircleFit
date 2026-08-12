# MayFlyCircleFit

MayFlyCircleFit approximates a reference image with colored circles using the
[Mayfly](https://github.com/cwbudde/mayfly) optimizer. The default CPU renderer
supports joint, sequential, and batch optimization. An experimental OpenCL
renderer is available for all three modes in GPU-tagged builds.

The project is under active production-readiness remediation. Read the
[support matrix](docs/support-matrix.md) and
[known limitations](docs/known-limitations.md) before relying on it for long or
unattended runs.

## Requirements

- Go 1.24 or newer for source compatibility; use a currently supported,
  security-patched Go release for production binaries
- Git, if you want to run generated-file consistency checks
- `just` is optional; every essential command also has a direct Go equivalent

The repository pins templ `v0.3.960` as a Go tool and commits generated
`internal/ui/*_templ.go` files. A clean checkout therefore builds without a
separate templ installation. MayFly is pinned to `v0.3.0`.

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
./mayflycirclefit serve \
  --addr localhost \
  --port 8080 \
  --input-root ./assets \
  --data-root ./data
```

The default bind address is `localhost`. Browser requests with a foreign
`Origin` are rejected, and reference/canvas paths are canonicalized beneath one
of the repeatable `--input-root` directories. Job concurrency and queue length
are bounded by `--max-jobs` and `--queue-size`. Profiling routes are disabled by
default; `--enable-pprof` is accepted only with a loopback bind address.

A minimal API job can be submitted from the checkout directory with:

```sh
curl -fsS http://localhost:8080/api/v1/jobs \
  -H 'Content-Type: application/json' \
  --data '{"refPath":"assets/test.png","mode":"joint","backend":"cpu","threads":1,"circles":1,"iters":1,"popSize":20,"seed":42}'
```

This same-origin check is a browser defense, not an authentication boundary.
Command-line clients normally omit `Origin` and are allowed.

Progress can be observed either by polling the status resource or by keeping an
SSE connection open. SSE is an optional second transport; polling remains
available for clients that cannot maintain streaming HTTP connections.

```sh
# Polling
curl -fsS http://localhost:8080/api/v1/jobs/JOB_ID/status

# Live events (iteration, best cost, circles/second, and state)
curl -N http://localhost:8080/api/v1/jobs/JOB_ID/stream
```

The stream sends an immediate snapshot, at most one optimization progress event
per 500 ms, and a final `completed`, `failed`, or `cancelled` event before it
closes. A comment heartbeat is sent every 30 seconds while otherwise idle.

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
compile and focused runtime suite, pinned `govulncheck` under Go 1.26.5, and a
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
assets/                 Small example input
docs/                   Support, limitations, and design notes
```

See [CONTRIBUTING.md](CONTRIBUTING.md), [CHANGELOG.md](CHANGELOG.md), the
[release process](docs/releasing.md), and [PLAN.md](PLAN.md) for contribution
checks, current changes, publishing, and remediation status.
