# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MayFlyCircleFit is a high-performance circle fitting optimization tool that approximates images with colored circles using evolutionary algorithms (Mayfly Algorithm and Differential Evolution). It features CPU/GPU backends, live web visualization, and SIMD-accelerated evaluation.

## Build and Development Commands

```bash
# Build the binary
just build

# Build and run
just run

# Run tests
just test

# Run tests with coverage
just test-coverage

# Format code
just fmt

# Run linters (go vet + formatting check)
just lint

# Clean build artifacts
just clean
```

The binary is output to `./bin/mayflycirclefit`.

## Architecture

The codebase follows a modular, interface-driven design with clear separation of concerns:

### Core Domain Model (`internal/fit/`)
- **Circle representation**: 7-parameter encoding (X, Y, R, CR, CG, CB, Opacity)
- **ParamVector**: Flat float64 slice encoding K circles for optimizer consumption
- **Bounds**: Parameter validation and clamping with configurable ranges
- **MSECost**: Mean Squared Error cost function over RGB channels

### Rendering System (`internal/fit/`)
- **Renderer interface**: Defines contract for render backends (CPU/GPU)
  - `Render(params []float64) *image.NRGBA` - Renders circles to image
  - `Cost(params []float64) float64` - Computes MSE against reference
  - `Dim() int` - Returns parameter space dimensionality
  - `Bounds() (lower, upper []float64)` - Returns parameter bounds
  - `Reference() *image.NRGBA` - Returns reference image
- **CPURenderer**: Software rendering with Porter-Duff alpha compositing
  - Bounding-box optimized circle rasterization
  - Premultiplied alpha blending

### Optimization (`internal/opt/`)
- **Optimizer interface**: Pluggable optimization algorithms
  - `Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)`
- **Mayfly Algorithm**: Evolutionary algorithm with male/female populations
  - Males: attracted to females and global best
  - Females: attracted to global best
  - Mating with crossover and mutation
- **Configuration**: Supports deterministic runs via seed parameter

### Optimization Pipelines (`internal/fit/`)
Three strategies for adding circles:
1. **Joint**: Optimize all K circles simultaneously (planned)
2. **Sequential**: Add circles one at a time greedily (planned)
3. **Batch**: Add batchK circles per pass (planned)

### CLI (`cmd/`)
- **Cobra-based**: Structured command-line interface
- **Logging**: Structured logging via `slog` with configurable levels (debug, info, warn, error)
- Commands organized as separate files in `cmd/` directory

## Development Guidelines

### Testing
- All domain logic in `internal/` packages has corresponding `*_test.go` files
- Tests use table-driven patterns where appropriate
- Run single test: `go test ./internal/fit -v -run TestName`

### Code Organization
- `cmd/`: CLI entry points and command definitions
- `internal/fit/`: Core domain (circles, rendering, cost, pipelines)
- `internal/opt/`: Optimization algorithms
- `internal/server/`: HTTP server and job management (planned)
- `internal/ui/`: templ components for web UI (planned)
- `internal/store/`: Persistence and checkpoints (planned)
- `assets/`: Example reference images
- `docs/`: Documentation

### Parameter Encoding
Circles use 7 parameters in this order:
1. X - horizontal position [0, width]
2. Y - vertical position [0, height]
3. R - radius [1, max(width, height)]
4. CR - red channel [0, 1]
5. CG - green channel [0, 1]
6. CB - blue channel [0, 1]
7. Opacity - alpha [0, 1]

For K circles, the parameter vector has length K * 7.

### Renderer Interface Contract
When implementing new renderers:
- Render empty vector (all zeros) should produce white canvas
- Cost function must return MSE over all RGB channels
- Bounds must match the dimension returned by Dim()
- Reference image must be in NRGBA format

### Optimizer Interface Contract
When implementing new optimizers:
- Must respect provided bounds (lower/upper)
- Evaluation function is provided by caller
- Should support deterministic runs via configuration (seed)
- Returns best parameters and best cost

## Project Status

Currently implementing **Phase 3** (Mayfly Optimizer) according to PLAN.md. Phases 1-2 are complete:
- Phase 1: Core domain model (Circle, ParamVector, Bounds, MSECost) - COMPLETE
- Phase 2: CPU Renderer with alpha compositing - COMPLETE
- Phase 3: Mayfly Algorithm - IN PROGRESS

See PLAN.md for detailed implementation roadmap through Phase 13.
