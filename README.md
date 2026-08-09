# MayFlyCircleFit

MayFlyCircleFit approximates a reference image with colored circles using the
[Mayfly](https://github.com/cwbudde/mayfly) optimizer. The default CPU renderer
supports joint, sequential, and batch optimization. An experimental OpenCL
renderer is available for joint optimization in GPU-tagged builds.

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
  --circles 1 \
  --iters 1 \
  --pop 20 \
  --seed 42
```

`out.png` is the fitted image. A seed of zero asks the application to generate
and report an effective seed; provide a nonzero seed for repeatable runs.

Equivalent convenience commands are `just build`, `just test`, `just lint`,
and `just check`.

## Commands

- `run` performs a local optimization and writes a PNG.
- `serve` starts the trusted-local HTTP UI and job API.
- `status [job-id]` lists server jobs or shows one job.
- `resume <job-id>` restarts from a saved best candidate, remotely or with
  `--local`.
- `checkpoints list` and `checkpoints clean` inspect or prune persisted jobs.
- `version` prints the application version string.

Use `./mayflycirclefit <command> --help` for the complete flag set.

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
  --data '{"refPath":"assets/test.png","mode":"joint","backend":"cpu","circles":1,"iters":1,"popSize":20,"seed":42}'
```

This same-origin check is a browser defense, not an authentication boundary.
Command-line clients normally omit `Origin` and are allowed.

## Backends and optimization modes

| Backend | Joint | Sequential | Batch | Notes |
| --- | --- | --- | --- | --- |
| CPU | Supported | Supported | Supported | Custom base canvases are supported |
| OpenCL | Experimental | Unsupported | Unsupported | Requires `-tags gpu`, CGO, OpenCL headers and a runtime |

Unsupported staged OpenCL requests return an explicit error; they are not
silently switched to a CPU staged pipeline. See
[GPU backend notes](docs/gpu-backends.md) for setup details.

```sh
CGO_ENABLED=1 go build -tags gpu -o mayflycirclefit .
./mayflycirclefit run --ref assets/test.png --backend opencl --mode joint
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
floor with an uploaded profile, portable cross-builds, an OpenCL-header GPU-tag
compile, and pinned `govulncheck` under Go 1.26.5. Their presence is not a claim
that the current branch or release candidate has passed them; consult the actual
workflow result.

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

See [CONTRIBUTING.md](CONTRIBUTING.md), [CHANGELOG.md](CHANGELOG.md), and
[PLAN.md](PLAN.md) for contribution checks, current changes, and remediation
status.
