# Animation export

`circlefit animate` replays a finished circle arrangement as a sequence of PNG
frames, and optionally encodes them to MP4.

It is a port of the animation export in this project's Pascal predecessor,
[CircledPictureDrawing](https://github.com/CWBudde/Circled-Picture-Drawing) —
the four `SaveAnimation*` procedures in `MainUnit.pas`, chosen in a dialog and
run from a menu.

## What it animates, and what it cannot

The original animates a **finished arrangement, not the search**. Every one of
its four procedures replays the completed `FCircles` array after the run is
over; none of them writes a frame while the optimizer is working. This port
keeps that property, which is what makes it work on any circle list at all.

It is also the only thing it could do. Nothing in this project records the
search: a checkpoint is overwritten in place at every epoch boundary, and
`store.TraceEntry.Params` — the one field that could carry geometry over time —
exists and round-trips but is never populated by the code that writes traces.
So an animation of the optimizer actually converging, circles sliding into
place, is **not** possible today. It would need a geometry history that is not
being recorded. What you can animate is the finished picture being drawn.

## Styles

| Style | Original | What it does |
|---|---|---|
| `static` | Static | One frame per circle. Frame N is the first N circles. |
| `grow` | Animated | Each circle in turn expands from a point on an ease-out curve, fading in as it grows. |
| `cascade` | Advanced | Up to `--max-active` circles grow at once in a sliding window; the head becomes permanent as it reaches full size. |
| `inflate` | Blow All | Every circle expands together, radius linearly and opacity as its square root. |

Both growing styles share one curve, transcribed from the original:

```
radius += (finalRadius - radius) * coefficient   // 0.3333 for grow, 0.2 for cascade
radius *= 1.1
radius += 1
```

The constant `+1` is what makes it work: the ease-out term shrinks with the
remaining distance, so a circle starting at radius one would otherwise never get
going.

## Examples

A campaign result as a video:

```sh
circlefit animate \
  --ref example/MayFly-512.png \
  --checkpoint data/jobs/<job-id>/checkpoint.json \
  --out-dir frames/ --style grow --mp4 fit.mp4
```

A hand-authored arrangement, or a schedule's `base.initialCircles`:

```sh
circlefit animate --ref example/MayFly-512.png \
  --circles schedule.json --out-dir frames/ --style cascade
```

Reproducing the original's look — twice the size, picture in the middle quarter,
closing vignette:

```sh
circlefit animate --ref example/MayFly-512.png --circles circles.json \
  --out-dir frames/ --style grow --scale 2 --margin 0.5 --outro 41
```

## Flags

| Flag | Default | Notes |
|---|---|---|
| `--ref` | — | Required. Sets the frame size; the image itself is never drawn. |
| `--circles` | — | A bare circle array or a schedule document. One of this or `--checkpoint`. |
| `--checkpoint` | — | A `checkpoint.json`; animates its `bestParams`. |
| `--out-dir` | — | Required. Created if absent. |
| `--style` | `static` | `static`, `grow`, `cascade`, `inflate`. |
| `--scale` | `1` | Multiplies every coordinate and radius. |
| `--margin` | `0` | Space on each side as a fraction of the scaled size. `0.5` gives the original framing. |
| `--half-life` | `500` | `grow` only. Drops a rising share of frames, so the animation accelerates. `0` keeps every frame. |
| `--max-active` | `32` | `cascade` only. |
| `--frames` | `0` | `inflate` only. `0` derives the count from the largest radius. |
| `--outro` | `0` | Closing vignette frames. `41` reproduces the original. |
| `--reverse` | `false` | `inflate` only; deflates to nothing. |
| `--background` | `#FFFFFF` | Fill behind the arrangement. |
| `--canvas` | — | Base canvas the fit started from. Overrides whatever the source records. |
| `--ignore-canvas` | `false` | Animate on `--background`, ignoring the canvas the source records. |
| `--supersample` | `1` | Render this many times larger and average down, to antialias the circle edges. |
| `--workers` | `0` | Goroutines that average and encode frames alongside the render. `0` uses every core. |
| `--mp4`, `--fps` | —, `30` | Encode with ffmpeg. |

Frames are written as `frame-000000.png`. The padding is deliberate: the
original wrote `Frame7.png`, which does not sort, and `ffmpeg -i
frame-%06d.png` needs the fixed width.

## Antialiasing

The span compositor draws no partial pixels — a circle's edge is a hard
boundary, because the byte-exact parity contract in
[`renderer-correctness.md`](renderer-correctness.md) requires it to be. At the
reference's own size that is invisible; scaled up for a video it is a visible
staircase.

`--supersample N` renders at N times the geometry and box-averages each frame
back down, so an edge resolves to 1/N² of a pixel. A 512² fit as a 1024² video
with 4× supersampling renders at 4096² — which is exactly `app.MaxImagePixels`,
so that is the ceiling for a square canvas:

```sh
circlefit animate --ref example/MayFly-512.png --checkpoint checkpoint.json \
  --out-dir frames/ --style cascade --scale 2 --supersample 4 --fps 60 --mp4 fit.mp4
```

Measured on one circle over a plain background: without it a frame holds two
colours and no edge pixels at all; at `--supersample 4`, seventeen colours and
982 graded edge pixels.

It multiplies render cost by roughly N², and only the downsampled frame is ever
written, so the PNGs stay the size you asked for.

## Where the time goes

Drawing the animation is the cheap part, and it is the only part that has to
happen in order. Every frame composites onto the canvas the frame before it
committed — the original's `BackDraw`/`Drawing` split — so the render loop is
one sequential chain. Nothing after the composite is: averaging frame N down and
encoding it as a PNG does not depend on frame N-1.

So that is where the work is moved. `Downsample` splits its output rows across
goroutines, and each finished frame is handed to a pool of encoders while the
renderer draws the next one. Frames are therefore *written* out of order, which
is invisible because each one is its own numbered file.

`--workers` bounds both, capped at `GOMAXPROCS` like `run --threads`; `0` means
every core. It is a throughput knob only — the frames are byte-identical at any
setting, which `TestAnimateProducesTheSameFramesAtEveryWorkerCount` asserts by
running the same arrangement at one worker and at eight.

The queue holds one frame per worker, deliberately. An unbounded one would let
the renderer run ahead of the encoders and pile up whole frames, and a
supersampled frame is large: 4096x4096 is 67 MB. Only the averaged-down frame is
queued, so memory stays flat in the sequence length.

Measured on the 3130-frame `example/mayfly-3000.mp4` sequence — 3000 circles,
`--style cascade --scale 2 --supersample 4`, so 4096x4096 down to 1024x1024 —
on a Ryzen 5 4600H, 6 cores and 12 threads, the two binaries run back to back on
an otherwise idle machine:

| | wall | CPU | peak RSS |
|---|---|---|---|
| before | 645.18 s | 100% | 783 MB |
| after | 238.25 s | 352% | 972 MB |

2.71x, and the two runs' 6260 PNGs compare equal. 352% rather than 1100% is the
honest ceiling here and says where the remaining time is: the render loop is
still serial by construction, and most of what it spends is not drawing circles
but the three full-canvas copies and two full-canvas scans that
`NewCPURendererWithCanvas` does on every commit — and cascade commits on nearly
every frame. Cutting that is a separate change; it would also raise this
ceiling, because it is the serial half of Amdahl's law here.

## Two things worth knowing about the output

**The base canvas comes from the source.** A schedule document's
`base.canvasPath` and a checkpoint's `config.canvasPath` are both used
automatically, because a solution fitted over a canvas does not describe the
finished image without it — animating those circles on white would produce
something that never matches the run they came from. `--canvas` overrides it.

A checkpoint records the path as it was on the machine that ran the job, so it
is often absent locally. That is an **error**, not a silent fall back to white:

```
the source was fitted over canvas "/home/ewws/cf/base.png", which is not readable here: ...
pass --canvas with a local copy, or --ignore-canvas to animate on the background colour
```

**The output directory is cleared of previous frames first.** Overwriting only
the new prefix is not enough: a shorter run would leave the old tail behind, and
because the numbering is contiguous those leftovers are not inert — `ffmpeg`
reads `frame-%06d.png` until the sequence breaks, so the previous run's ending
would be spliced onto this one's. Only names this command writes are removed —
`frame-` followed by exactly six digits and `.png` — and only in the directory
named by `--out-dir`. Anything else there is left alone.

## Where this differs from the original

Four deliberate departures, and one thing it cannot reproduce.

- **The background defaults to white, not black.** The original defaulted to
  `$FF000000` because its own fits were made over black. This project fits over
  white, so animating over black would show the circles against a background
  they were never fitted against. `--background 000000` restores it.
- **Clean frames by default.** The original always rendered onto a canvas twice
  the scaled reference with the picture in the middle quarter, purely so its
  closing vignette had a border to darken. That is `--margin 0.5 --outro 41`
  here; without them the frames are the scaled reference and nothing else.
- **The last frame is always shown.** `--half-life` can drop the frame that
  completes the final circle, and the original would then simply not write it.
  A sequence whose last frame is not the finished image is a bug, so that one
  frame is forced through.
- **Three dead controls are either revived or dropped.** The original's dialog
  persisted `Maximum Primitives` and `Skip Frames` that nothing read, and an
  `Inverted` checkbox wired to a parameter the call site never passed.
  `--max-active` and `--reverse` make the first and third real; `Skip Frames`
  has no counterpart because `--half-life` already does that job.

The one thing that cannot carry over is **frame de-duplication**. The original
worked in 24.8 fixed point, so consecutive growth steps often produced a
byte-identical raster, which `if not Drawing.Equals(BackDraw)` skipped. This
port computes in float64, where every step differs numerically even when it
rounds to the same pixels. The effect is minor — a few extra frames at the start
of a small circle — and no frame is ever wrong; there are just occasionally two
where the original had one.

## How it is put together

`internal/anim` splits planning from rendering.

`Plan` is a pure function from a circle list to `[]Frame`, where a frame says
which circles are made **permanent** before it (`Commit`) and which are drawn
over that background for this frame only (`Active`). That split is the
original's `BackDraw` and `Drawing` bitmaps. Because it touches no pixels, the
growth curves, frame counts and commit points are all unit-testable directly,
and `anim_test.go` pins the curve against values computed from the Pascal
formulas.

`Render` then executes a plan through `renderer.CPURenderer`, the same compositor
every run uses, so a frame is byte-identical to the image those circles produce
as `best.png`. `render_test.go` asserts exactly that for all four styles: the
last frame must equal a single-pass render of the whole arrangement. A scaling
error, a dropped commit or a reordered circle all fail there.

Two consequences worth knowing:

- **CPU only.** The OpenCL renderer computes in float32 and is outside the
  parity contract in [`renderer-correctness.md`](renderer-correctness.md), so a
  GPU frame would not match `best.png`.
- **`--canvas` cannot be combined with `--scale`.** Scaling a base canvas means
  resampling pixels the fit never saw. Rather than pick an interpolation and
  quietly change what the frames mean, the combination is refused. It *can* be
  combined with `--supersample`: the canvas is taken up by repeating each pixel
  as an N×N block, which the box filter on the way out reverses exactly, so the
  averaged frame carries the canvas byte for byte and only the circle edges
  drawn over it are softened.
