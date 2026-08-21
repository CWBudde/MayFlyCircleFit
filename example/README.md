# Example: Sequential Circle Optimization

This example demonstrates sequential circle fitting - adding circles one at a time to progressively approximate the reference image. This is the recommended approach for iterative refinement with convergence detection.

## Files

- `Ref.png` - Reference image (1024x1024 RGB portrait)
- `Canvas.png` - Optional starting canvas (if you want to continue from a previous result)
- `config.json` - Optimization configuration

## Configuration

The `config.json` contains:

### Required Fields
- **refPath**: Path to reference image (`example/Ref.png`)
- **mode**: Optimization strategy (`sequential`, `joint`, or `batch`)
- **circles**: Maximum number of circles to optimize (may stop early due to convergence)
- **iters**: Maximum iterations per circle (for sequential) or total (for joint)
- **popSize**: Optimizer population size (30 is a good default)
- **seed**: Random seed for reproducibility (0 = random)

### Optional Fields
- **canvasPath**: Path to existing canvas image to continue from (empty = start with white background)
- **checkpointInterval**: Save checkpoint every N seconds (0 = disabled, only save on completion)
- **enableTrace**: Enable cost history logging to trace.jsonl (default: true)
- **saveSnapshots**: Save intermediate snapshots and circle data in sequential mode (default: false)

### Convergence Settings
- **convergenceEnabled**: Enable early stopping when improvement plateaus (default: true)
- **convergencePatience**: Stop after N circles with minimal improvement (default: 3)
- **convergenceThreshold**: Minimum relative improvement required (default: 0.001 = 0.1%)

## Optimization Modes

### Sequential Mode (Recommended)

Sequential mode adds circles **one at a time**, optimizing each circle while keeping previous circles fixed. This provides:

- **Progressive refinement**: Each circle improves upon the previous result
- **Convergence detection**: Automatically stops when adding more circles provides diminishing returns
- **Snapshots**: Can save intermediate images showing how each circle contributes (enable with `saveSnapshots: true`)
- **Canvas continuation**: Can continue from a previous optimization result using `canvasPath`

**How it works:**
1. Start with blank canvas (or load existing canvas if `canvasPath` specified)
2. Optimize position, size, color, and opacity of first circle
3. Fix first circle, optimize second circle on top
4. Continue until max circles reached OR convergence detected
5. Save final result with all circles

**Convergence example:** If patience=3 and threshold=0.001, optimization stops if 3 consecutive circles each improve cost by less than 0.1%.

### Joint Mode

Joint mode optimizes **all K circles simultaneously**. This can find better global solutions but:
- Takes longer to converge
- Doesn't support progressive refinement
- No convergence detection (optimizes all circles)
- Suitable for small K (< 20 circles)

### Batch Mode

Batch mode is a hybrid: optimizes circles in **batches** (e.g., 5 at a time). Provides a balance between sequential and joint.

## Running the Example

### Option 1: Sequential Optimization (CLI)

**Basic usage:**
```bash
just build
./bin/mayflycirclefit run \
  --ref example/Ref.png \
  --mode sequential \
  --circles 10 \
  --iters 1000 \
  --pop 30 \
  --seed 42 \
  --out example/output.png
```

**Continue from existing canvas:**
```bash
./bin/mayflycirclefit run \
  --ref example/Ref.png \
  --canvas example/Canvas.png \
  --mode sequential \
  --circles 10 \
  --iters 1000 \
  --pop 30 \
  --out example/output-v2.png
```

The optimization starts with a white background (or loads Canvas.png if specified) and progressively adds circles to approximate the reference image.

### Option 2: Server-based Optimization with Web UI & Snapshots

**Start the server:**
```bash
just build
./bin/mayflycirclefit serve --port 8080
```

**Create a job via web UI:**
Visit http://localhost:8080/create and configure your optimization, then monitor real-time progress at http://localhost:8080/jobs/<job-id>

**Create a job via API:**
```bash
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d @example/config.json
```

**Monitor progress:**
```bash
# Get job status
curl http://localhost:8080/api/v1/jobs/<job-id>/status

# Download result
curl http://localhost:8080/api/v1/jobs/<job-id>/best.png -o example/result.png

# Stream real-time updates (Server-Sent Events)
curl -N http://localhost:8080/api/v1/jobs/<job-id>/stream
```

**Access snapshots (if saveSnapshots enabled):**
```bash
# Snapshots are saved to ./data/jobs/<job-id>/snapshots/
ls ./data/jobs/<job-id>/snapshots/
# Output: canvas-01.png, canvas-02.png, ..., canvas-10.png

# Circle metadata (position, size, color, opacity, cost)
cat ./data/jobs/<job-id>/circles.json
```

## Snapshot Feature (Sequential Mode Only)

When `saveSnapshots: true` is enabled in config.json (server mode only), the system saves:

1. **Intermediate canvas images**: `./data/jobs/<job-id>/snapshots/canvas-NN.png`
   - Shows the rendered result after each circle is optimized
   - NN is zero-padded (01, 02, 03, ...)
   - Useful for visualizing progressive refinement

2. **Circle metadata**: `./data/jobs/<job-id>/circles.json`
   - JSON array with one entry per circle
   - Fields: `circleNum`, `x`, `y`, `r`, `cr`, `cg`, `cb`, `opacity`, `costAfter`, `timestamp`
   - Useful for analysis and reproducibility

**Example circles.json:**
```json
[
  {
    "circleNum": 1,
    "x": 512.3,
    "y": 256.7,
    "r": 150.2,
    "cr": 0.85,
    "cg": 0.42,
    "cb": 0.12,
    "opacity": 0.75,
    "costAfter": 0.234,
    "timestamp": "2025-10-30T10:15:23Z"
  },
  ...
]
```

**Note:** Snapshots are only available in **server mode** (not CLI `run` command) and only for **sequential mode** optimization.

## Expected Results

With 10 circles in sequential mode, the optimizer will progressively refine the approximation:

- **Circle 1**: Rough overall color/shape
- **Circles 2-5**: Major features and color regions
- **Circles 6-10**: Fine details and refinement

The optimizer determines for each circle:
- Position (X, Y) within image bounds
- Radius (R)
- Color (RGB) in [0, 1] range
- Opacity (alpha) in [0, 1] range

**Convergence:** If convergence is enabled (recommended), optimization may stop before reaching 10 circles if improvement plateaus. Check the logs or job status to see actual circles used.

## Tips

- **Start small**: Try 5-10 circles first, then increase if needed
- **Use sequential mode**: Best for iterative refinement with convergence
- **Enable snapshots**: Great for understanding how optimization progresses
- **Set seed for reproducibility**: Same seed = same results (for testing/debugging)
- **Canvas continuation**: Use canvasPath to continue from previous optimization
- **Monitor convergence**: Check if optimization stopped early due to convergence (logged in job status)

## Handcrafted campaign: `christian-16-handcrafted-v6.json`

The campaigns in this directory all target `Christian_after.jpeg` and all start
from a random population. This one does not: its first eight circles were placed
by hand, from looking at the image, and the campaign grows them to sixteen.

The eight, painted back to front — a light gray backdrop, a navy shirt whose
centre sits below the canvas so only its cap shows, a dark hair mass, the face
over it, the beard, the brighter bald forehead, the neck wedge that punches
through the shirt, and the cream V-neck collar:

| # | Role | x | y | r | colour |
| --- | --- | --- | --- | --- | --- |
| 1 | Background | 256 | 256 | 400 | `#c8cbd0` |
| 2 | T-shirt | 256 | 660 | 300 | `#232650` |
| 3 | Hair mass | 256 | 240 | 165 | `#4a3226` |
| 4 | Face | 258 | 215 | 115 | `#eda587` |
| 5 | Beard | 256 | 330 | 78 | `#a06a52` |
| 6 | Forehead | 256 | 155 | 72 | `#f8b294` |
| 7 | Neck / chin | 268 | 480 | 70 | `#b98a72` |
| 8 | Collar | 352 | 445 | 40 | `#ece0d3` |

Score them without running anything:

```sh
mayflycirclefit score --ref example/Christian_after.jpeg \
    --circles example/christian-16-handcrafted-v6.json --out handcrafted.png
```

```
circles:    8
canvas:     512x512
cost:       2417.8846
psnr:       14.2964 dB
blank cost: 15738.7879
```

The base stage runs `"iters": 1`, so it records that arrangement and its cost
rather than searching — the campaign's first stage is the handcrafted result.
Fourteen stages follow the usual shape: four polish sweeps over the eight, `+4`
to twelve, three sweeps, `+4` to sixteen, four more sweeps.

```sh
mayflycirclefit schedule create --dry-run example/christian-16-handcrafted-v6.json
mayflycirclefit schedule create example/christian-16-handcrafted-v6.json
```

The point of the experiment is the comparison: how much of the final cost is
structure a human can supply in a minute, and how much is search.

## Demo campaign: `mayfly-3000-campaign.json`

The reference campaign for this project. It takes ten hand-placed circles to
3000 on `MayFly-512.png` — a macro photograph of a mayfly on a wet rock — and is
the worked example for growing a fit to the `MaxCircles` ceiling.

### The image

`MayFly.png` is the 1024x1024 original. The campaign targets `MayFly-512.png`,
a Lanczos downsample, so its costs are directly comparable to the
`Christian_after.jpeg` campaigns above and a circle costs the same to fit.
Measured on the Ryzen 5 4600H box, the same extend recipe costs 5.74 s/circle at
512x512 and 13 s/circle at 1024x1024, for a picture that is not four times more
interesting.

The subject is a harder target than a portrait in one specific way: the
antennae, cerci, and legs are one to three pixels wide. No circle can represent
a line, so that detail stays in the residual permanently. What circles *can*
take is the out-of-focus background, the bokeh disc, the wing, and the rock —
which is what the ten seed circles are for.

### The ten circles

Placed by hand from looking at the image, painted back to front. Each value was
checked against `score` and kept only where it helped; five of seven later
"improvements" made the cost worse and were dropped, which is the useful part of
the exercise.

| # | Role | x | y | r | colour | opacity |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | Background | 256 | 256 | 400 | `#474128` | 1 |
| 2 | Dark upper band | 256 | 20 | 210 | `#302c0e` | 0.75 |
| 3 | Bokeh disc | 400 | 110 | 42 | `#d2b182` | 0.95 |
| 4 | Bokeh halo | 399 | 152 | 26 | `#8a7a52` | 0.5 |
| 5 | Rock | 237 | 470 | 130 | `#383523` | 1 |
| 6 | Foreground water | 256 | 640 | 190 | `#4b4830` | 1 |
| 7 | Wing, upper | 245 | 165 | 70 | `#7d9085` | 0.85 |
| 8 | Wing, lower | 275 | 275 | 60 | `#a8b99e` | 0.8 |
| 9 | Abdomen | 205 | 325 | 14 | `#6f6044` | 0.8 |
| 10 | Thorax and head | 330 | 308 | 16 | `#776c50` | 0.85 |

Circles 5 and 6 have centres below the canvas so only their caps show — the rock
and the water are edges, not discs.

```sh
mayflycirclefit score --ref example/MayFly-512.png \
    --circles example/mayfly-3000-campaign.json --out example/MayFly-seed.png
```

```
circles:    10
canvas:     512x512
cost:       1187.1344
psnr:       17.3858 dB
blank cost: 38732.1225
```

Ten circles remove **96.9%** of the blank cost. The comparison with
`christian-16-handcrafted-v6.json` is the interesting one: eight hand-placed
circles removed 84.6% of a portrait's blank cost, ten remove 96.9% here. The
difference is not skill, it is subject — most of this frame is a smooth
defocused background, and a circle is a very good model of a smooth defocused
background.

### The budget tiers

The campaign spends three different per-circle budgets, and the boundaries are
where they are because of measurements, not taste:

| Tier | Circles | epochs | iters | popSize | s/circle | Why |
| --- | --- | --- | --- | --- | --- | --- |
| A | 10 → 128 | 2 | 1000 | 50 | 17.7 | The foundation is worth buying; it is only 118 circles. |
| B | 128 → 2000 | 1 | 500 | 30 | 5.7 → 8.2 | Search effort saturates here — at 733 circles a 6x wall-clock range moved cost by 0.36 units. Cheapest wins. |
| C | 2000 → 3000 | 2 | 900 | 60 | ~32 | Levers pay again past 2000: over a 50-circle sample, `iters` 500 gained 1.0348 against `iters` 50's 0.5635. |

`popSize` and `epochs` move together in every tier. Raising population without
epochs to exploit it bought 0.03 cost units for 2.2x the wall clock when it was
measured directly.

Tier C is the one extrapolation in the document. That levers matter again past
2000 is measured; that *this particular* 3.4x budget is the right amount is not.
It is also the expensive tier — dropping it to tier B settings saves roughly
7 hours and is a one-line change.

### Polishing

Three polish stages, at 32, 64 and 128 circles, each behind a `when` guard.
Polishing only pays while the canvas is small enough for the foundation to still
move: the same stage returns ~2597 cost units per hour at 32 circles, 113 at
128, and 1.26 at 1000. Every stage is sized so `activeSetSize * maxSweeps >=
circles`, because efficiency tracks coverage.

`minGain: 2.0` with `abortAfterBarren: 2` switches polishing off for the rest of
the campaign once it stops earning, so the schedule does not need to know in
advance which of the three is the last useful one.

### Cost

2994 stages. Roughly **13 hours** on 12 cores: 0.6 h for tier A, 3.5 h for
tier B, 8.9 h for tier C, and about 12 minutes of polishing.

```sh
mayflycirclefit schedule create --dry-run example/mayfly-3000-campaign.json
mayflycirclefit schedule create example/mayfly-3000-campaign.json
```

### Why the base stage searches

The base runs a real 600-iteration batch over the ten hand-placed circles rather
than `"iters": 1`. That is forced, not preferred: the optimizer-level stop
fields live on the base `JobConfig` and are validated against the base's own
`iters`, so a record-only base cannot carry `stopStagnationIters: 250` — and
those fields cannot be overridden per step, so the whole 2990-stage campaign
would then run without early stopping, at roughly twice the wall clock. Scoring
the untouched arrangement is what `score` is for, and its number is above.
