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

## Phase 4: Pipelines (Joint, Sequential, Batch)

### Task 4.1: Implement Joint Optimization Pipeline

**Files:**
- Create: `internal/fit/pipeline.go`
- Create: `internal/fit/pipeline_test.go`

**Step 1: Write the failing test**

Create: `internal/fit/pipeline_test.go`

```go
package fit

import (
  "image"
  "image/color"
  "testing"

  "github.com/cwbudde/mayflycirclefit/internal/opt"
)

func TestOptimizeJoint(t *testing.T) {
  // Create simple 10x10 reference with red circle
  ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
  white := color.NRGBA{255, 255, 255, 255}
  for y := 0; y < 10; y++ {
    for x := 0; x < 10; x++ {
      ref.Set(x, y, white)
    }
  }

  // Add a red center pixel
  ref.Set(5, 5, color.NRGBA{255, 0, 0, 255})

  renderer := NewCPURenderer(ref, 1)

  optimizer := opt.NewMayfly(50, 10, 42) // maxIters, popSize, seed

  result := OptimizeJoint(renderer, optimizer, 1)

  if result.BestCost >= result.InitialCost {
    t.Errorf("Optimization did not improve: initial=%f, best=%f", result.InitialCost, result.BestCost)
  }

  if len(result.BestParams) != 7 {
    t.Errorf("Expected 7 parameters for 1 circle, got %d", len(result.BestParams))
  }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/fit -v -run TestOptimizeJoint
```

Expected: FAIL - OptimizeJoint not defined

**Step 3: Write minimal implementation**

Create: `internal/fit/pipeline.go`

```go
package fit

import (
  "log/slog"

  "github.com/cwbudde/mayflycirclefit/internal/opt"
)

// OptimizationResult holds the output of an optimization run
type OptimizationResult struct {
  BestParams  []float64
  BestCost    float64
  InitialCost float64
  Iterations  int
}

// OptimizeJoint optimizes all K circles simultaneously
func OptimizeJoint(renderer Renderer, optimizer opt.Optimizer, k int) *OptimizationResult {
  slog.Info("Starting joint optimization", "circles", k)

  dim := k * paramsPerCircle
  lower, upper := renderer.Bounds()

  // Ensure bounds match dimension
  if len(lower) < dim || len(upper) < dim {
    // Extend bounds if needed (renderer created with fewer circles)
    newRenderer := NewCPURenderer(renderer.Reference(), k)
    renderer = newRenderer
    lower, upper = renderer.Bounds()
  }

  // Trim to actual dimension
  lower = lower[:dim]
  upper = upper[:dim]

  // Initial cost (white canvas)
  initialParams := make([]float64, dim)
  initialCost := renderer.Cost(initialParams)

  // Run optimizer
  bestParams, bestCost := optimizer.Run(renderer.Cost, lower, upper, dim)

  slog.Info("Joint optimization complete", "initial_cost", initialCost, "best_cost", bestCost)

  return &OptimizationResult{
    BestParams:  bestParams,
    BestCost:    bestCost,
    InitialCost: initialCost,
  }
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/fit -v -run TestOptimizeJoint
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/fit/pipeline.go internal/fit/pipeline_test.go
git commit -m "feat: implement joint optimization pipeline"
```

---

### Task 4.2: Implement Sequential Optimization Pipeline

**Files:**
- Modify: `internal/fit/pipeline.go`
- Modify: `internal/fit/pipeline_test.go`

**Step 1: Write the failing test**

Add to `internal/fit/pipeline_test.go`:

```go
func TestOptimizeSequential(t *testing.T) {
  // Create simple reference
  ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
  white := color.NRGBA{255, 255, 255, 255}
  for y := 0; y < 10; y++ {
    for x := 0; x < 10; x++ {
      ref.Set(x, y, white)
    }
  }
  ref.Set(5, 5, color.NRGBA{255, 0, 0, 255})

  renderer := NewCPURenderer(ref, 1)

  optimizer := opt.NewMayfly(30, 10, 42) // maxIters, popSize, seed

  result := OptimizeSequential(renderer, optimizer, 2)

  if result.BestCost >= result.InitialCost {
    t.Errorf("Optimization did not improve")
  }

  if len(result.BestParams) != 14 { // 2 circles * 7 params
    t.Errorf("Expected 14 parameters for 2 circles, got %d", len(result.BestParams))
  }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/fit -v -run TestOptimizeSequential
```

Expected: FAIL - OptimizeSequential not defined

**Step 3: Write minimal implementation**

Add to `internal/fit/pipeline.go`:

```go
// OptimizeSequential optimizes circles one at a time (greedy)
func OptimizeSequential(renderer Renderer, optimizer opt.Optimizer, totalK int) *OptimizationResult {
  slog.Info("Starting sequential optimization", "total_circles", totalK)

  ref := renderer.Reference()
  allParams := []float64{}

  initialCost := MSECost(
    NewCPURenderer(ref, 0).Render([]float64{}),
    ref,
  )

  for k := 1; k <= totalK; k++ {
    slog.Info("Optimizing circle", "index", k, "of", totalK)

    // Create renderer with k circles
    currentRenderer := NewCPURenderer(ref, k)

    // Objective: optimize only the new circle, keeping previous ones fixed
    dim := paramsPerCircle
    lower := make([]float64, dim)
    upper := make([]float64, dim)
    bounds := NewBounds(1, ref.Bounds().Dx(), ref.Bounds().Dy())
    copy(lower, bounds.Lower)
    copy(upper, bounds.Upper)

    evalFunc := func(newCircleParams []float64) float64 {
      // Combine previous circles + new circle
      combined := append(append([]float64{}, allParams...), newCircleParams...)
      return currentRenderer.Cost(combined)
    }

    bestNew, _ := optimizer.Run(evalFunc, lower, upper, dim)
    allParams = append(allParams, bestNew...)
  }

  finalRenderer := NewCPURenderer(ref, totalK)
  finalCost := finalRenderer.Cost(allParams)

  slog.Info("Sequential optimization complete", "initial_cost", initialCost, "final_cost", finalCost)

  return &OptimizationResult{
    BestParams:  allParams,
    BestCost:    finalCost,
    InitialCost: initialCost,
  }
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/fit -v -run TestOptimizeSequential
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/fit/pipeline.go internal/fit/pipeline_test.go
git commit -m "feat: implement sequential optimization pipeline"
```

---

### Task 4.3: Implement Batch Optimization Pipeline

**Files:**
- Modify: `internal/fit/pipeline.go`
- Modify: `internal/fit/pipeline_test.go`

**Step 1: Write the failing test**

Add to `internal/fit/pipeline_test.go`:

```go
func TestOptimizeBatch(t *testing.T) {
  ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
  white := color.NRGBA{255, 255, 255, 255}
  for y := 0; y < 10; y++ {
    for x := 0; x < 10; x++ {
      ref.Set(x, y, white)
    }
  }
  ref.Set(5, 5, color.NRGBA{255, 0, 0, 255})

  renderer := NewCPURenderer(ref, 1)

  optimizer := opt.NewMayfly(30, 10, 42) // maxIters, popSize, seed

  // 2 passes of 2 circles each = 4 circles total
  result := OptimizeBatch(renderer, optimizer, 2, 2)

  if len(result.BestParams) != 28 { // 4 circles * 7 params
    t.Errorf("Expected 28 parameters for 4 circles, got %d", len(result.BestParams))
  }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/fit -v -run TestOptimizeBatch
```

Expected: FAIL - OptimizeBatch not defined

**Step 3: Write minimal implementation**

Add to `internal/fit/pipeline.go`:

```go
// OptimizeBatch adds batchK circles per pass for multiple passes
func OptimizeBatch(renderer Renderer, optimizer opt.Optimizer, batchK, passes int) *OptimizationResult {
  slog.Info("Starting batch optimization", "batch_size", batchK, "passes", passes)

  ref := renderer.Reference()
  allParams := []float64{}

  initialCost := MSECost(
    NewCPURenderer(ref, 0).Render([]float64{}),
    ref,
  )

  for pass := 0; pass < passes; pass++ {
    slog.Info("Batch pass", "pass", pass+1, "of", passes)

    currentK := len(allParams) / paramsPerCircle
    newK := currentK + batchK

    // Optimize batch of circles jointly
    batchRenderer := NewCPURenderer(ref, newK)

    dim := batchK * paramsPerCircle
    lower := make([]float64, dim)
    upper := make([]float64, dim)
    bounds := NewBounds(batchK, ref.Bounds().Dx(), ref.Bounds().Dy())
    copy(lower, bounds.Lower)
    copy(upper, bounds.Upper)

    evalFunc := func(newBatchParams []float64) float64 {
      combined := append(append([]float64{}, allParams...), newBatchParams...)
      return batchRenderer.Cost(combined)
    }

    bestBatch, _ := optimizer.Run(evalFunc, lower, upper, dim)
    allParams = append(allParams, bestBatch...)
  }

  totalK := len(allParams) / paramsPerCircle
  finalRenderer := NewCPURenderer(ref, totalK)
  finalCost := finalRenderer.Cost(allParams)

  slog.Info("Batch optimization complete", "total_circles", totalK, "final_cost", finalCost)

  return &OptimizationResult{
    BestParams:  allParams,
    BestCost:    finalCost,
    InitialCost: initialCost,
  }
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/fit -v -run TestOptimizeBatch
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/fit/pipeline.go internal/fit/pipeline_test.go
git commit -m "feat: implement batch optimization pipeline"
```

---

## Phase 5: CLI with Cobra (Log-only UX)

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
