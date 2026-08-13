# HTML job reports

MayFlyCircleFit can download the current result of a server job as a portable,
self-contained HTML report. Use **Download Report** on the job detail page, or
request it directly:

```text
GET /api/v1/jobs/<job-id>/report.html
GET /api/v1/jobs/<job-id>/report.html?colormap=magma
```

The response has the media type `text/html; charset=utf-8` and an attachment
filename of `job-<job-id>-report.html`. The optional `colormap` query parameter
accepts `turbo` (the default) or `magma` and controls the difference image. A
report is available after the job has produced its first best parameter set.

## Contents

Each report captures one job snapshot and contains:

- job metadata, including the ID, state, mode, requested circle count, source
  path, start/end times, and report generation time;
- cost, PSNR, SSIM, iterations, evaluations, elapsed time, and difference
  colormap;
- reference, current-best, and difference images in a three-column layout; and
- every materialized circle's position, radius, RGB color, and opacity.

The three PNGs use base64 `data:` URLs. CSS is inline, and the document does not
load scripts, stylesheets, fonts, images, or other resources from the network.
That makes the downloaded file viewable offline, though its size is larger than
the three PNGs because base64 encoding adds overhead. The report reflects the
snapshot taken when the endpoint is requested; it does not update afterward.

## Printing

The inline print stylesheet switches to black text and borders on white,
removes scrolling from the parameter table, preserves color swatches, and puts
each major section on a new page. Browser print dialogs can save the result as
PDF without first hosting the HTML file.

## Customization

The report data contract is the `JobReport` type and the document markup and CSS
live in `internal/ui/report.templ`. To change fields or layout, update that
template and, when needed, the snapshot construction in
`internal/server/downloads.go`. Then regenerate the committed templ output and
run the focused tests:

```sh
just templ
go test ./internal/ui ./internal/server
```

Keep report resources inline to preserve offline use. If new data is added,
escape it through templ's normal expressions; only the server-generated image
data URLs should use `templ.SafeURL`.
