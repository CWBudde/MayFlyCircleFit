# Advanced Quality Metrics

CircleFit reports peak signal-to-noise ratio (PSNR) for every server job
and CLI run. Structural similarity (SSIM) is available as an opt-in metric when
perceptual structure matters more than raw pixel error.

## PSNR

PSNR is derived from the optimizer's RGB mean squared error (MSE):

```text
PSNR = 20 * log10(255 / sqrt(MSE))
```

The implementation uses the same 8-bit sRGB channels as the optimizer and
ignores alpha. It is measured in decibels (dB); higher values indicate a closer
pixel match. A perfect match has infinite PSNR.

The JSON status, SSE, and trace formats cannot encode IEEE infinity as a JSON
number. They therefore represent a perfect match as:

```json
{"psnr": null, "psnrInfinite": true}
```

Before a metric is available, `psnr` is also `null`, but `psnrInfinite` is
absent or false. Consumers must use the flag to distinguish those states.

## SSIM

SSIM compares luminance, contrast, and structure using the standard local
formula:

```text
SSIM(x,y) = ((2*muX*muY + C1) * (2*sigmaXY + C2)) /
            ((muX^2 + muY^2 + C1) * (sigmaX^2 + sigmaY^2 + C2))
```

CircleFit uses an 11×11 Gaussian window with sigma 1.5, reflected image
borders, `K1=0.01`, `K2=0.03`, and an 8-bit dynamic range of 255. SSIM is
calculated independently over R, G, and B and then averaged; alpha is ignored.
Results normally range from 0 to 1, with 1 indicating identical RGB images.
The mathematical lower bound is -1.

SSIM requires rendering and filtering the current best image, so it is disabled
by default. Enable it for a local CLI run with:

```sh
circlefit run --ref assets/reference.png --enable-ssim
```

For server jobs, send `"enableSSIM": true` in the job configuration or select
**Enable SSIM** on the creation page. When enabled, the server samples SSIM for
the initial image, at most once per second after the best cost improves, and for
the final image. A metric failure is logged and omitted without failing the
optimization.

## API and history

`GET /api/v1/jobs/:id/status` and progress SSE events include:

```json
{
  "bestCost": 123.4,
  "psnr": 27.219,
  "ssim": 0.8123
}
```

The detail page shows current values and a bounded 100-point chart switchable
between Cost, PSNR, and enabled SSIM. PSNR history follows the live SSE cadence;
SSIM points are intentionally sparser because of its throttle.

When tracing is enabled, `trace.jsonl` contains the complete optimizer progress
history with `cost`, `psnr`, `psnrInfinite`, and optional `ssim` values. Turning
off tracing suppresses the persistent file but does not disable current metrics
or the bounded live UI history.

PSNR and SSIM should be interpreted together with the rendered image. PSNR is
excellent for exact pixel fidelity but can disagree with perceived quality;
SSIM better captures local structural preservation but adds computation and is
not a substitute for visual inspection.
