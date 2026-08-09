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
