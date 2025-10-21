# MayFlyCircleFit Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Build a high-performance circle-fitting optimization tool with CPU/GPU backends, web UI, and live progress visualization.

**Architecture:** Go-based CLI with Cobra, modular optimizer/renderer interfaces, HTTP server with SSE streaming, templ-based UI, and SIMD-accelerated evaluation kernels.

**Tech Stack:** Go 1.21+, Cobra (CLI), templ (frontend), slog (logging), net/http (server), optional chi (routing), cgo+SIMD (performance), OpenGL/OpenCL (GPU)

---

## Phase 1: Core Domain Model ✅ COMPLETE

**Implemented:**
- `Circle` struct: Position (X, Y, R) + Color (CR, CG, CB) + Opacity
- `ParamVector`: Flat float64 encoding of K circles (7 params per circle)
- `Bounds`: Parameter validation with configurable ranges
  - X, Y: [0, width/height]
  - R: [1, max(width, height)]
  - Color/Opacity: [0, 1]
- `MSECost`: Mean Squared Error cost function over RGB channels
- Helper functions: `EncodeCircle`, `DecodeCircle`, `ClampCircle`, `ClampVector`

**Test Coverage:** 6 passing tests (encoding, bounds, clamping, MSE)

---

## Phase 2: CPU Renderer ✅ COMPLETE

**Implemented:**
- `Renderer` interface: Render(), Cost(), Dim(), Bounds(), Reference()
- `CPURenderer` struct: Software rendering with Porter-Duff alpha compositing
- `renderCircle()`: Bounding-box optimized circle rasterization
- `compositePixel()`: Premultiplied alpha blending

**Test Coverage:** 2 passing tests (white canvas, single circle rendering)

---

## Phase 3: Optimizer (Mayfly - Using External Library) ✅ COMPLETE

**Implemented:**
- `Optimizer` interface: Run() method for optimization algorithms
- `MayflyAdapter` struct: Wrapper for external Mayfly library with configurable variants
- Variant support: Standard, DESMA, OLCE, and other Mayfly algorithm variants
- Constructor functions: `NewMayfly()`, `NewMayflyDESMA()`, `NewMayflyOLCE()`

**Test Coverage:** 2 passing tests (sphere function optimization, deterministic behavior)

---

## Phase 4: Pipelines (Joint, Sequential, Batch) ✅ COMPLETE

**Implemented:**
- `OptimizationResult` struct: Holds best parameters, costs, and iteration info
- `OptimizeJoint()`: Optimizes all circles simultaneously
- `OptimizeSequential()`: Adds circles one at a time greedily
- `OptimizeBatch()`: Adds batches of circles in multiple passes

**Test Coverage:** 3 passing tests (joint, sequential, batch optimization pipelines)

---

## Phase 5: CLI with Cobra (Log-only UX) ✅ COMPLETE

**Implemented:**
- `run` command: Single-shot optimization with configurable modes (joint/sequential/batch)
  - Flags: --ref, --out, --mode, --circles, --iters, --pop, --seed
  - Image loading, optimization, and output saving
  - Metrics reporting (cost improvement, circles/sec throughput)
- Stub commands: `serve`, `status`, `resume` (placeholders for future phases)
- Test image: assets/test.png for validation

**Commands:**
- `mayflycirclefit run --ref <image>` - Run optimization
- `mayflycirclefit serve` - Stub for Phase 6
- `mayflycirclefit status` - Stub for Phase 6
- `mayflycirclefit resume <job-id>` - Stub for Phase 7

---

## Phase 5: CLI with Cobra (Log-only UX) - Implementation Details

### Task 5.1: Implement 'run' Command

**Files:**
- Create: `cmd/mayflycirclefit/run.go`
- Modify: `cmd/mayflycirclefit/root.go`

**Step 1: Write run command**

Create: `cmd/mayflycirclefit/run.go`

```go
package main

import (
  "fmt"
  "image"
  "image/png"
  "log/slog"
  "os"
  "time"

  "github.com/spf13/cobra"
  "github.com/cwbudde/mayflycirclefit/internal/fit"
  "github.com/cwbudde/mayflycirclefit/internal/opt"
)

var (
  refPath   string
  outPath   string
  mode      string
  circles   int
  iters     int
  popSize   int
  seed      int64
)

var runCmd = &cobra.Command{
  Use:   "run",
  Short: "Run single-shot optimization",
  Long:  `Runs circle fitting optimization and writes output image and parameters.`,
  RunE:  runOptimization,
}

func init() {
  runCmd.Flags().StringVar(&refPath, "ref", "", "Reference image path (required)")
  runCmd.Flags().StringVar(&outPath, "out", "out.png", "Output image path")
  runCmd.Flags().StringVar(&mode, "mode", "joint", "Optimization mode: joint, sequential, batch")
  runCmd.Flags().IntVar(&circles, "circles", 10, "Number of circles")
  runCmd.Flags().IntVar(&iters, "iters", 100, "Max iterations")
  runCmd.Flags().IntVar(&popSize, "pop", 30, "Population size")
  runCmd.Flags().Int64Var(&seed, "seed", 42, "Random seed")

  runCmd.MarkFlagRequired("ref")
  rootCmd.AddCommand(runCmd)
}

func runOptimization(cmd *cobra.Command, args []string) error {
  slog.Info("Starting optimization", "mode", mode, "circles", circles, "iters", iters)

  // Load reference image
  f, err := os.Open(refPath)
  if err != nil {
    return fmt.Errorf("failed to open reference: %w", err)
  }
  defer f.Close()

  img, _, err := image.Decode(f)
  if err != nil {
    return fmt.Errorf("failed to decode image: %w", err)
  }

  // Convert to NRGBA
  bounds := img.Bounds()
  ref := image.NewNRGBA(bounds)
  for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
    for x := bounds.Min.X; x < bounds.Max.X; x++ {
      ref.Set(x, y, img.At(x, y))
    }
  }

  slog.Info("Loaded reference", "width", bounds.Dx(), "height", bounds.Dy())

  // Create renderer
  renderer := fit.NewCPURenderer(ref, circles)

  // Create optimizer
  optimizer := opt.NewMayfly(iters, popSize, seed)

  // Run optimization
  start := time.Now()
  var result *fit.OptimizationResult

  switch mode {
  case "joint":
    result = fit.OptimizeJoint(renderer, optimizer, circles)
  case "sequential":
    result = fit.OptimizeSequential(renderer, optimizer, circles)
  case "batch":
    batchSize := 5
    passes := circles / batchSize
    if circles%batchSize != 0 {
      passes++
    }
    result = fit.OptimizeBatch(renderer, optimizer, batchSize, passes)
  default:
    return fmt.Errorf("unknown mode: %s", mode)
  }

  elapsed := time.Since(start)

  // Render final image
  finalRenderer := fit.NewCPURenderer(ref, circles)
  output := finalRenderer.Render(result.BestParams)

  // Save output
  outFile, err := os.Create(outPath)
  if err != nil {
    return fmt.Errorf("failed to create output: %w", err)
  }
  defer outFile.Close()

  if err := png.Encode(outFile, output); err != nil {
    return fmt.Errorf("failed to encode output: %w", err)
  }

  // Compute throughput (circles rendered per second)
  // Each eval renders K circles, estimate total evals ~ iters * popSize
  totalEvals := iters * popSize
  totalCircles := totalEvals * circles
  cps := float64(totalCircles) / elapsed.Seconds()

  slog.Info("Optimization complete",
    "elapsed", elapsed,
    "initial_cost", result.InitialCost,
    "final_cost", result.BestCost,
    "improvement", result.InitialCost-result.BestCost,
    "circles_per_second", fmt.Sprintf("%.0f", cps),
  )

  fmt.Printf("✓ Wrote %s (cost: %.2f → %.2f, %.0f circles/sec)\n", outPath, result.InitialCost, result.BestCost, cps)

  return nil
}
```

**Step 2: Test run command**

First create a simple test image:

```bash
# Create a simple test reference (you can use any small image)
# For now, we'll create one programmatically in the next step
```

**Step 3: Build and test**

```bash
make build
# We'll need a test image - create one in assets/
```

**Step 4: Create test image helper**

Create a small Go program to generate a test image (temporary):

```bash
cat > /tmp/gen_test_img.go << 'EOF'
package main
import (
  "image"
  "image/color"
  "image/png"
  "os"
)
func main() {
  img := image.NewNRGBA(image.Rect(0, 0, 50, 50))
  for y := 0; y < 50; y++ {
    for x := 0; x < 50; x++ {
      img.Set(x, y, color.NRGBA{255, 255, 255, 255})
    }
  }
  // Red square in center
  for y := 20; y < 30; y++ {
    for x := 20; x < 30; x++ {
      img.Set(x, y, color.NRGBA{255, 0, 0, 255})
    }
  }
  f, _ := os.Create("assets/test.png")
  png.Encode(f, img)
  f.Close()
}
EOF
mkdir -p assets
go run /tmp/gen_test_img.go
```

**Step 5: Test and commit**

```bash
./bin/mayflycirclefit run --ref assets/test.png --circles 3 --iters 50 --pop 10
# Check that out.png is created
git add cmd/mayflycirclefit/run.go assets/
git commit -m "feat: implement 'run' command for single-shot optimization"
```

---

### Task 5.2: Add Stub Commands for serve, status, resume

**Files:**
- Create: `cmd/mayflycirclefit/serve.go`
- Create: `cmd/mayflycirclefit/status.go`
- Create: `cmd/mayflycirclefit/resume.go`

**Step 1: Create serve stub**

Create: `cmd/mayflycirclefit/serve.go`

```go
package main

import (
  "fmt"

  "github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
  Use:   "serve",
  Short: "Start HTTP server (coming in Phase 6)",
  RunE: func(cmd *cobra.Command, args []string) error {
    return fmt.Errorf("serve command not yet implemented (Phase 6)")
  },
}

func init() {
  rootCmd.AddCommand(serveCmd)
}
```

**Step 2: Create status stub**

Create: `cmd/mayflycirclefit/status.go`

```go
package main

import (
  "fmt"

  "github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
  Use:   "status",
  Short: "Query server status (coming in Phase 6)",
  RunE: func(cmd *cobra.Command, args []string) error {
    return fmt.Errorf("status command not yet implemented (Phase 6)")
  },
}

func init() {
  rootCmd.AddCommand(statusCmd)
}
```

**Step 3: Create resume stub**

Create: `cmd/mayflycirclefit/resume.go`

```go
package main

import (
  "fmt"

  "github.com/spf13/cobra"
)

var resumeCmd = &cobra.Command{
  Use:   "resume [job-id]",
  Short: "Resume from checkpoint (coming in Phase 7)",
  Args:  cobra.ExactArgs(1),
  RunE: func(cmd *cobra.Command, args []string) error {
    return fmt.Errorf("resume command not yet implemented (Phase 7)")
  },
}

func init() {
  rootCmd.AddCommand(resumeCmd)
}
```

**Step 4: Test help**

```bash
make build
./bin/mayflycirclefit --help
# Verify all commands appear
```

**Step 5: Commit**

```bash
git add cmd/mayflycirclefit/serve.go cmd/mayflycirclefit/status.go cmd/mayflycirclefit/resume.go
git commit -m "feat: add stub commands for serve, status, resume"
```

---

## Summary and Next Steps

This plan covers **Phases 0-5** in detail with bite-sized, testable tasks. Each task follows TDD principles:
1. Write failing test
2. Run test to verify failure
3. Write minimal implementation
4. Run test to verify pass
5. Commit

**Remaining Phases (6-13)** would follow the same structure:
- **Phase 6**: HTTP server with job management and SSE
- **Phase 7**: templ-based frontend UI
- **Phase 8**: Persistence and checkpoints
- **Phase 9**: CPU profiling and optimizations
- **Phase 10**: SIMD/intrinsics for SSD
- **Phase 11**: GPU backends
- **Phase 12**: UX polish and visualization
- **Phase 13**: Documentation and packaging

**Total estimated tasks**: ~80-100 tasks across all phases

Would you like me to:
1. Continue expanding Phases 6-13 in the same detail?
2. Create a separate PLAN.md for each phase?
3. Focus on specific phases you want to tackle first?
