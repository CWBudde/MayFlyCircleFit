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
