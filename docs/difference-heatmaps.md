# Difference heatmaps

The job detail page's **Difference** view visualizes the per-pixel difference
between the reference image and the current best rendering. It is intended to
show where an approximation is wrong; it is not a replacement for the job's
aggregate cost.

## Error scale

For each pixel, MayFlyCircleFit computes the mean absolute error across the red,
green, and blue channels:

```text
(|reference.R - best.R| + |reference.G - best.G| + |reference.B - best.B|) / 3
```

Alpha is ignored, matching the RGB-focused optimization metric. The result has
a fixed range from `0` (identical RGB values) to `255` (maximum difference in
all three channels), which is normalized to `[0, 1]` before color mapping. The
fixed scale makes heatmaps comparable across jobs and progress updates; a small
error is not stretched to look severe merely because it is the largest error in
an otherwise close match.

## Colormaps

- **Turbo** is the default. It offers strong visual separation across the scale
  and is useful for locating fine changes quickly.
- **Magma** progresses from near-black through purple and orange to pale yellow.
  Its monotonic lightness is often easier to interpret in grayscale or for
  viewers who find rainbow-style palettes distracting.

The legend beneath the image always labels the normalized palette with the
underlying error endpoints, `0` and `255`. Changing the dropdown requests a new
PNG without restarting or modifying the optimization.

The image API accepts the same selection:

```text
GET /api/v1/jobs/:id/diff.png?colormap=turbo
GET /api/v1/jobs/:id/diff.png?colormap=magma
```

Omitting `colormap` uses Turbo. Other values return a `400` response with the
`invalid_colormap` error code. Saved checkpoint and completion `diff.png`
artifacts also use Turbo so their appearance is deterministic.
