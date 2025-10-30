# Example: Single Circle Optimization

This example demonstrates circle fitting with a single circle to approximate the reference image.

## Files

- `Ref.png` - Reference image (1024x1024 RGB portrait)
- `config.json` - Optimization configuration

## Configuration

The `config.json` contains:
- **refPath**: Path to reference image
- **mode**: `joint` - optimize all circles simultaneously
- **circles**: `1` - single circle to approximate the image
- **iters**: `1000` - maximum iterations
- **pop**: `30` - optimizer population size
- **seed**: `42` - random seed for reproducibility

## Running the Example

### Option 1: Single-shot optimization (CLI)

```bash
just build
./bin/mayflycirclefit run \
  --ref example/Ref.png \
  --circles 1 \
  --iters 1000 \
  --pop 30 \
  --seed 42 \
  --out example/output.png
```

The optimization will start from a random initial circle on a white background and attempt to approximate the reference image. The output will be saved to `example/output.png`.

### Option 2: Server-based optimization with web UI

```bash
just build
./bin/mayflycirclefit serve --port 8080
```

Then create a job via the web UI at http://localhost:8080/create or via API:

```bash
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d @example/config.json
```

Monitor progress in the web UI or retrieve the result:

```bash
# Get job status
curl http://localhost:8080/api/v1/jobs/<job-id>/status

# Download result
curl http://localhost:8080/api/v1/jobs/<job-id>/best.png -o example/result.png
```

## Expected Results

With a single circle, the optimizer will find the best-fitting circle that minimizes the mean squared error against the reference image. This will be a very rough approximation, as one circle cannot capture the complexity of a portrait.

The optimizer will determine:
- Position (X, Y)
- Radius (R)
- Color (RGB)
- Opacity

To get better results, increase the number of circles (e.g., 10, 50, 100).

## Notes

- The renderer starts with a white background by default (not black)
- Initial circle parameters are randomized based on the seed
- With seed=42, results are reproducible
- For better approximations, try increasing circles to 10+ in config.json
