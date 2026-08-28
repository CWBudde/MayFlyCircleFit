# Troubleshooting

How CircleFit reports failures, and what the common ones mean.

## CLI exit status

The binary distinguishes a wrong invocation from work that failed:

| Status | Meaning | Example |
| ------ | ------- | ------- |
| `0` | Success | the run finished and wrote its artifacts |
| `1` | The command ran but its work failed | the reference image could not be read |
| `2` | The command was invoked incorrectly | an unknown flag, a missing argument, a bad `--log-level` |

Status `2` covers every flag-parsing failure, Cobra's own argument validation,
and the flag-value checks a command makes itself — `serve --max-jobs 0`,
`run --backend nope`, `checkpoints clean` without a retention flag. What the
command's *work* runs into (an unreadable image, a failed render, a server that
does not answer) is status `1`. Scripts can therefore retry a `1` and never
a `2`.

Errors are printed to stderr as `Error: <message>` (or `Usage error: <message>`
plus a pointer to `--help`). When the failure names a file the process could not
open, a `Suggestion:` line follows with the concrete next step.

## Common CLI errors

| Message | Cause | Fix |
| ------- | ----- | --- |
| `open assets/…: no such file or directory` | The reference image path is wrong. Paths are relative to the working directory, not to the binary. | Run from the repository root, or pass an absolute path. |
| `open …: permission denied` | The reference image or the output directory is not readable/writable by the current user. | Check the permissions on the path named in the `Suggestion:` line. |
| `invalid log level "…": use debug, info, warn, or error` | `--log-level` got a value outside the accepted set. | Use one of the four listed levels. |
| `unknown command "…"` / `unknown flag: --…` | Typo in the invocation. | `circlefit --help`, or `circlefit <command> --help`. |
| `max-jobs must be between 1 and 16`, `queue-size must be between 1 and 100`, `invalid backend: …`, `invalid configuration: …` | A flag value is outside its accepted range or set. | Correct the flag; the message names the bound or the accepted values. |
| `server response exceeds 1048576 bytes` | The CLI refuses to decode an unbounded response. The stage listing and the chain view are projections sized to stay under it for any campaign a document may expand to, so this now means a different endpoint, or a server older than the projection. | Compare versions with `circlefit version`; a campaign's per-stage configuration is read one stage at a time from `/api/v1/schedules/:id/stages/:index`. |
| `invalid configuration: circles must be between 1 and 1000` from a flag you set to `0` | A flag always carries a value, so a zero is a value you asked for, not an omission. | Drop the flag to get the default; the defaults only fill flags and fields you leave out. |
| OpenCL device errors, or a GPU request falling back to the CPU | The `gpu` build tag, the OpenCL runtime, or a usable device is missing. | See [`gpu-backends.md`](gpu-backends.md) and [`support-matrix.md`](support-matrix.md). A CGO-disabled build has no GPU backend at all. |

## HTTP API errors

Every `/api/v1/…` failure answers with `Content-Type: application/json` and this
envelope, including a path that matches no route at all:

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

`invalid_config` with a message like `circles is silently replaced by the
configuration defaults` means the body wrote a value the defaults would swallow
— most often `"circles": 0` from marshalling a zero-valued configuration
struct. Omit a field you have no opinion on rather than sending its zero; only
an omitted field is defaulted.
| 403 | `origin_forbidden` | The request's `Origin` is not a trusted local origin. |
| 404 | `not_found` | No job, schedule, checkpoint, or project with that identifier — or no API endpoint at that path. |
| 404 | `no_results` | The job exists but has not produced a best result yet. Poll the status endpoint or the SSE stream first. |
| 409 | `invalid_state` | The action does not apply in the job's current state, such as pausing a job that already finished. |
| 409 | `optimizer_version_mismatch` | The checkpoint was written by a different MayFly version than the server links, so the continuation would not be comparable. Re-baseline, or repeat the request with `?allowOptimizerMismatch=true`. |
| 413 | `request_too_large` | The request body exceeded the server's limit. |
| 429 | `queue_full` | The server's job queue is full. Wait for a job to finish, cancel one, or raise `--queue-size`. |
| 500 | `reference_load_failed` | The job's reference image could not be read from disk. Check the server log for the path and the underlying cause. |
| 500 | `render_failed`, `report_failed`, `resume_failed`, `schedule_error`, `pause_failed` | The server-side operation failed. The server log has the cause. |
| 501 | `sse_not_supported` | The response writer cannot stream. This means a proxy or middleware in front of the server buffers responses; connect to the server directly. |
| 503 | `checkpoints_unavailable`, `schedules_unavailable`, `project_unavailable` | The feature needs the persistence root the server was started without. Start `serve` with a writable `--data-root`. |

### A batch ends at `refill_limit`

A batch whose circles change no pixel at all, or that leaves the image no
better than it found it, places nothing. The stage is then retried against the
residual --- but only while the run's own iteration budget still covers the
retry, because a refill is a whole further optimizer run and an unbudgeted one
doubles what the job spends. A job that runs out of budget, or out of the three
bounded attempts, finishes with `termination: "refill_limit"`. This is a usable
result, but it may contain fewer circles than requested.

A batch that does improve the image is kept whole, so a single weak circle in
an otherwise useful batch no longer sends the job into a refill. Read the explicit counts from the job
resource instead of inferring production from the configuration:

```json
{
  "termination": "refill_limit",
  "requestedCircles": 2814,
  "actualCircles": 2813,
  "config": {"circles": 2814}
}
```

The checkpoint is complete at `actualCircles` and may be extended or polished.
`POST /api/v1/jobs/{id}/extend` treats that actual count as its starting point;
`{"additionalCircles": 1}` in the example above targets 2814 again, not 2815.
This means a chain driver can continue from the short job or retry the last
addition with another explicit seed. Older servers rejected such a continuation
as `400 invalid_checkpoint`; recovery there requires using the last complete
ancestor, then retrying the extension, preferably with a different seed.

The pipeline does not silently change seeds during refill. Its optimizer
contract has no stage-local reseed operation, and repurposing the persisted
resume counter would change established fixed-seed trajectories. Now that the
short checkpoint is both visible and continuable, a caller can choose whether
to retain the seed or deliberately retry with a new one.

## Resource and environment failures

These are the failures that come from the machine rather than from the request.

### A full or read-only filesystem

A write that cannot land is reported, not swallowed: `run` and `resume` close
every artifact they write and return the close error, so a truncated PNG or
checkpoint fails the command instead of exiting 0. The message names the path
and the suggestion names the cause:

```
Error: failed to write output: write out.png: no space left on device
Suggestion: the filesystem holding "out.png" is full or over quota; free space
or write elsewhere. Any artifact already written there may be truncated.
```

A partially written artifact may still be on disk — delete it before re-running
so a later `checkpoints` listing does not pick it up. Checkpoints themselves are
written through a temporary file that is synced and renamed, so a checkpoint is
either the previous good one or the new one, never a half of either.

`read-only file system` means the output directory is mounted read-only; pass a
different `--out` or `--data-root`.

### The server is not reachable

`status`, `resume`, and the `schedule` commands are HTTP clients. A refused
connection means nothing is listening:

```
Error: connect to server: Get "http://localhost:8080/api/v1/jobs": dial tcp
127.0.0.1:8080: connect: connection refused
Suggestion: no server is listening there; start one with `circlefit
serve`, or point the server flag (--server for `status`, --server-url for
`resume`) at the right address.
```

The client gives up on its own after 10 seconds rather than hanging, and reports
a timeout distinctly from a refusal. The timeout covers the whole request, from
name resolution and dialling through reading the response, so it does not by
itself say that a server accepted the connection — check both that the address
is right and that the server is running and not blocked on a long request. A
body that ends early (a server that died mid-response) is reported as a read
failure and never decoded as if it were complete.

### Out of memory

Go's heap exhaustion is a fatal runtime error. It cannot be caught, so there is
no graceful message for it: the process dies with `runtime: out of memory` and a
stack dump, and any running job is lost — resume from its last checkpoint.

The defence is therefore a bound, applied before anything is allocated:
references above 16,777,216 pixels and requests above 3000 circles are rejected
at validation, on the CLI and at the API alike. If you are hitting the limit
rather than the bound, the levers are the reference resolution, `--circles`, and
`--pop`, in that order of effect.

For a long-lived server, also set Go's soft heap limit below the memory the host
can spare. For example, on a machine where the server may use at most 8 GiB:

```sh
GOMEMLIMIT=8GiB ./circlefit serve --data-root ./data
```

`GOMEMLIMIT` makes the runtime collect more aggressively as the heap approaches
the limit. It is a backstop rather than a hard RSS cap: mapped files, stacks,
native/OpenCL allocations, and short-lived overshoot still need headroom.
Checkpoint and job collection endpoints are metadata projections, so opening a
dashboard does not deserialize or clone historical parameter vectors and trace
histories.

### GPU unavailable

A backend that cannot start reports `renderer backend unavailable` with the
reason appended. A build without the `gpu` tag says so directly; a build with it
reports what the OpenCL runtime said — no platform, no device, or a context that
would not initialise. See [`gpu-backends.md`](gpu-backends.md) and
[`support-matrix.md`](support-matrix.md). A CGO-disabled portable build has no
GPU backend at all, so `--backend gpu` there is always this error.

A build without the `gpu` tag now says this earlier rather than at run time. It
does not advertise `opencl` in `GET /api/v1/system`, `serve --backend opencl`
refuses to start, and a job that names the backend explicitly is rejected at
submit. Set `backendFallback: "cpu"` (`--backend-fallback cpu`) when you would
rather the run happened on the CPU than not at all; the job then records
`effectiveBackend: "cpu"` and a warning names the reason. Leave it unset — the
default — when the point of the run is to measure the device.

### GPU errors during a run

A device that fails after the renderer has started is a different problem. The
OpenCL renderer cannot report it: `Cost` and `Render` have no error return, so
it degrades permanently to its CPU fallback and logs

```
WARN OpenCL renderer degraded to CPU reason=...
```

once, followed by a server-side line naming the job:

```
WARN Renderer degraded to its CPU fallback mid-run job_id=... backend=opencl
```

The job carries `backendDegraded: true` from that point, the CLI prints
`Backend: opencl (degraded to CPU mid-run)`, and the job detail page shows the
same. **Treat the run's cost as unusable for comparison.** The device
accumulates the SSD in float32 and the CPU in float64, so the objective changed
scale partway through and the best-so-far spans both.

Common causes, in the order worth checking:

- **Out of device memory.** A large canvas, a large circle count, or another
  process holding the card. `clinfo` reports the global memory size; the
  renderer needs roughly `W*H*4` bytes twice plus the reduction partials.
- **A driver reset.** A long kernel can trip the display driver's watchdog on a
  card that is also driving a desktop. The system log records it; the OpenCL
  error is usually `CL_OUT_OF_RESOURCES` or `CL_DEVICE_NOT_AVAILABLE`.
- **The device disappeared.** A suspend/resume cycle or a driver upgrade under a
  running process. Restart the run.

Degradation is per renderer and permanent, so a staged run can pay one device
timeout per stage before every session has given up.

### Optimizer failures

An optimizer that fails does not return a partial fit. Every pipeline — joint,
sequential, and batch — propagates the failure, so a job fails rather than
reporting a cost that was never reached, and no final checkpoint is written from
it. Periodic checkpoints taken before the failure do remain, and are the point to
resume from. Over the API this surfaces as a failed job whose error is the
generic `job execution failed`; the specific cause is in the server log.

An optimizer that returns a malformed result — the wrong parameter-vector
length, a NaN cost — is reported as `optimizer produced an invalid result`
rather than being accepted.

## When you need more detail

Run with `--log-level debug`. The server logs the full error, the job
identifier, and the path involved for every response whose body is generic.
