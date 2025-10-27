# MayFlyCircleFit

High-performance circle fitting optimization tool using evolutionary algorithms.

## Overview

MayFlyCircleFit approximates images with colored circles using the Mayfly Algorithm and Differential Evolution. Features CPU/GPU backends, live web visualization, and SIMD-accelerated evaluation.

## Quick Start

```bash
# Build
just build

# Run help
./bin/mayflycirclefit --help

# (More commands coming in later phases)
```

## Project Status

See current status and roadmap in the [PLAN.md](PLAN.md) file.

## Development

```bash
# Format code
just fmt

# Run tests
just test

# Run linter
just lint

# Clean build artifacts
just clean
```

## GPU Backend (Experimental)

OpenCL support is under active development. Build with GPU hooks via:

```bash
GOFLAGS="-tags=gpu" go build -o bin/mayflycirclefit .
```

Install OpenCL headers/runtime for your platform and run with `--backend opencl` once the renderer is available. See `docs/gpu-backends.md` for current status and setup notes.

## Architecture

```plain
/internal/fit           # Rendering, cost functions, pipelines
/internal/opt           # Mayfly/DE optimizers
/internal/server        # HTTP server, jobs, SSE
/internal/ui            # templ components
/internal/store         # Persistence, checkpoints
/internal/pkg           # Utility helpers
/assets                 # Example reference images
```
