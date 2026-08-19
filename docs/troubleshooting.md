# Troubleshooting

How MayFlyCircleFit reports failures, and what the common ones mean.

## CLI exit status

The binary distinguishes a wrong invocation from work that failed:

| Status | Meaning | Example |
| ------ | ------- | ------- |
| `0` | Success | the run finished and wrote its artifacts |
| `1` | The command ran but its work failed | the reference image could not be read |
| `2` | The command was invoked incorrectly | an unknown flag, a missing argument, a bad `--log-level` |

Status `2` covers every flag-parsing failure, Cobra's own argument validation,
and any command that reports an invocation problem with `cmd.NewUsageError`.
Scripts can therefore retry a `1` and never a `2`.

Errors are printed to stderr as `Error: <message>` (or `Usage error: <message>`
plus a pointer to `--help`). When the failure names a file the process could not
open, a `Suggestion:` line follows with the concrete next step.

## Common CLI errors

| Message | Cause | Fix |
| ------- | ----- | --- |
| `open assets/…: no such file or directory` | The reference image path is wrong. Paths are relative to the working directory, not to the binary. | Run from the repository root, or pass an absolute path. |
| `open …: permission denied` | The reference image or the output directory is not readable/writable by the current user. | Check the permissions on the path named in the `Suggestion:` line. |
| `invalid log level "…": use debug, info, warn, or error` | `--log-level` got a value outside the accepted set. | Use one of the four listed levels. |
| `unknown command "…"` / `unknown flag: --…` | Typo in the invocation. | `mayflycirclefit --help`, or `mayflycirclefit <command> --help`. |
| OpenCL device errors, or a GPU request falling back to the CPU | The `gpu` build tag, the OpenCL runtime, or a usable device is missing. | See [`gpu-backends.md`](gpu-backends.md) and [`support-matrix.md`](support-matrix.md). A CGO-disabled build has no GPU backend at all. |

## HTTP API errors

Every `/api/v1/…` failure answers with `Content-Type: application/json` and this
envelope:

```json
{"error": {"code": "not_found", "message": "job not found"}}
```

The `code` is the stable, machine-readable field; the `message` is for humans
and may be reworded. Messages are deliberately generic for anything caused by
server-side state — the detail, including filesystem paths, goes to the server
log instead of the response, because the browser is not a trusted audience even
on a trusted-local boundary (see
[`behavior-invariants.md`](behavior-invariants.md)).

Frequently seen codes:

| Status | Code | Meaning |
| ------ | ---- | ------- |
| 400 | `invalid_request`, `invalid_config`, `invalid_job_id`, `invalid_schedule_id`, `invalid_colormap`, `invalid_ref_path`, `invalid_canvas_path`, `invalid_checkpoint` | The request or one of its fields did not validate. The message names the field. |
| 403 | `origin_forbidden` | The request's `Origin` is not a trusted local origin. |
| 404 | `not_found` | No job, schedule, checkpoint, or project with that identifier. |
| 404 | `no_results` | The job exists but has not produced a best result yet. Poll the status endpoint or the SSE stream first. |
| 409 | `invalid_state` | The action does not apply in the job's current state, such as pausing a job that already finished. |
| 413 | `request_too_large` | The request body exceeded the server's limit. |
| 429 | `queue_full` | The server's job queue is full. Wait for a job to finish, cancel one, or raise `--queue-size`. |
| 500 | `reference_load_failed` | The job's reference image could not be read from disk. Check the server log for the path and the underlying cause. |
| 500 | `render_failed`, `report_failed`, `resume_failed`, `schedule_error`, `pause_failed` | The server-side operation failed. The server log has the cause. |
| 501 | `sse_not_supported` | The response writer cannot stream. This means a proxy or middleware in front of the server buffers responses; connect to the server directly. |
| 503 | `checkpoints_unavailable`, `schedules_unavailable`, `project_unavailable` | The feature needs the persistence root the server was started without. Start `serve` with a writable `--data-root`. |

## When you need more detail

Run with `--log-level debug`. The server logs the full error, the job
identifier, and the path involved for every response whose body is generic.
