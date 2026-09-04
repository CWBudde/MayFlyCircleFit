# Renderer test fixtures

## `polish-fixture-2111.json`

A production-shaped batch fit, preserved so the dirty-region evaluator's
end-to-end check can be re-run from a clean clone. It is the fixture Task 4 of
[`../../../../PLAN.md`](../../../../PLAN.md) asks for.

| | |
| --- | --- |
| Source job | `228e3715-07c1-4e32-b634-950540b38891` |
| Circles | 2,111 |
| Cost | `85.12514114379883` |
| Reference | `example/MayFly-512.png` (committed, 512x512) |
| Effective seed | `20260821` |
| Iterations / evaluations | 801,169 / 73,979,717 |
| Termination | `completed` |
| Written | 2026-08-22 |

The file is the job's `checkpoint.json` verbatim, with exactly one edit: its
`config.refPath` was rewritten from the absolute path the run recorded to the
repo-relative `example/MayFly-512.png`. That keeps a machine-specific path out
of the repository and lets the harness resolve the reference from the fixture
itself rather than naming it independently.

**Treat it as immutable.** `TestPolishFixtureDirtyVsFull` and every figure the
dirty-region section of
[`../../../../docs/contiguous-window-polish-report.md`](../../../../docs/contiguous-window-polish-report.md)
attributes to it are tied to this exact vector. Replacing it invalidates those
numbers, so a replacement is a new measurement, not a fixture refresh.

Why this vector and not the one behind the original 599 s sweep: that run fitted
`example/Christian_after.jpeg`, which the repository deliberately does not carry
(`.gitignore` excludes `/example/*.jpeg`). Its wall clock is therefore not
reproducible here and the report does not claim it.
