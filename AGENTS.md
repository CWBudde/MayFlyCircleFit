# Repository Guidelines

## Project Structure & Module Organization
MayFlyCircleFit is a Go 1.23 CLI; the entry point `main.go` compiles to `bin/`. Core rendering and evaluation reside in `internal/fit`, optimizers in `internal/opt`, and orchestration code in `internal/server`. UI templ files live in `internal/ui`, persistence helpers in `internal/store`, while assets, data fixtures, and research notes are in `assets/`, `data/`, and `docs/` or `profiles/`.

## Build, Test, and Development Commands
Prefer the `just` recipes defined in `justfile`. Run `just build` to compile `bin/mayflycirclefit`, and `just run` to exercise the binary. Enforce formatting with `just fmt`, then confirm static checks via `just lint`. Use `just test` for unit and integration suites, `just test-coverage` to emit `coverage.out` and `coverage.html`, and `just clean` to drop build or coverage artifacts.

## Coding Style & Naming Conventions
Stick to idiomatic Go: gofmt tabs, exported identifiers in PascalCase, unexported helpers camelCase. Reserve SCREAMING_SNAKE_CASE for grouped constants. Mirror the existing `internal/*` layering before adding packages. Cobra commands should use short imperative verbs (`resume`, `render`). After editing templ files, regenerate outputs with `just templ` or `just templ-watch`.

## Testing Guidelines
Locate tests alongside their packages using filenames such as `optimizer_test.go`. Favor table-driven cases for optimizer inputs and assert behavior on CPU and GPU paths when applicable. Maintain or improve coverage; if it dips, explain the gap and attach fresh `just test-coverage` results. Place long-running optimizer fixtures in `profiles/` with documented seeds for reproducibility.

## Commit & Pull Request Guidelines
Follow Conventional Commits (`feat:`, `fix:`, `refactor:`); scoped prefixes (`feat(server): resume endpoint`) help reviewers. Keep commits focused and avoid mixing formatting with logic. Pull requests should link the relevant PLAN.md task or issue, summarize impact, and include renders or metrics when optimization quality shifts. Confirm lint, fmt, and tests in the PR notes, and flag any reviewer setup needs.

## Runtime & Configuration Tips
The CLI reads reference imagery from `assets/`; pass relative paths in scripts and docs. GPU acceleration is optional—note hardware assumptions and fallbacks when you update those paths. Generated artifacts (`coverage.html`, resume snapshots, `out*.png`) stay untracked; clean them before publishing branches.
