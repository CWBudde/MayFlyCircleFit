# Contributing

Contributions are welcome while MayFlyCircleFit works through the active
production-readiness plan. Keep changes focused, preserve observable behavior
unless the change deliberately updates its tests and documentation, and link the
relevant `PLAN.md` task or issue.

## Before changing code

1. Read `AGENTS.md`, the active Phase 14 section of `PLAN.md`, the
   [behavior invariants](docs/behavior-invariants.md), the
   [support matrix](docs/support-matrix.md), and
   [known limitations](docs/known-limitations.md).
2. Use Go 1.24 or newer.
3. Create a focused branch and avoid including local profiles, coverage output,
   rendered PNGs, or checkpoint data.

## Generated UI

templ is pinned in `go.mod`, and generated `internal/ui/*_templ.go` files are
committed. When a `.templ` file changes, run:

```sh
go tool templ generate
git diff -- 'internal/ui/*_templ.go'
```

Include the source and its generated Go change in the same pull request.

## Tests and checks

Run focused package tests while developing. Before opening a pull request, run:

```sh
go tool templ generate
git diff --exit-code -- 'internal/ui/*_templ.go'
test -z "$(gofmt -s -l .)"
go vet ./...
go test -short ./...
go test -race -short ./...
go build ./...
```

`just check` is the local convenience target for the non-race core checks.
OpenCL changes also need a GPU-tag build and parity tests on documented hardware.
CI installs OpenCL headers and checks compilation, but it does not provide a
real OpenCL device for runtime validation.

If a check cannot be run, state that explicitly in the pull request. Never
describe an unexecuted check as passing. Include benchmark conditions and
allocation counts when claiming a performance change.

## Style and commits

- Follow idiomatic Go and run `gofmt -s`.
- Add table-driven regression tests near the package under change.
- Keep server/API errors safe for trusted-local clients; do not expose new raw
  filesystem or optimizer diagnostics over HTTP.
- Use Conventional Commits such as `fix(renderer): preserve custom canvas` or
  `docs: clarify restart semantics`.
- Keep generated changes, broad formatting, and unrelated refactors out of a
  focused commit unless they are integral to it.

By contributing, you agree that your contribution is licensed under the MIT
License in this repository.
