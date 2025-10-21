# MayFlyCircleFit Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Build a high-performance circle-fitting optimization tool with CPU/GPU backends, web UI, and live progress visualization.

**Architecture:** Go-based CLI with Cobra, modular optimizer/renderer interfaces, HTTP server with SSE streaming, templ-based UI, and SIMD-accelerated evaluation kernels.

**Tech Stack:** Go 1.21+, Cobra (CLI), templ (frontend), slog (logging), net/http (server), optional chi (routing), cgo+SIMD (performance), OpenGL/OpenCL (GPU)

---

## Phase 0: Project Scaffolding & Conventions

### Task 0.1: Initialize Go Module and Directory Structure

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `.gitignore`

**Step 1: Initialize Go module**

```bash
cd /mnt/e/Projects/MayFlyCircleFit
go mod init github.com/cwbudde/mayflycirclefit
```

Expected: Creates `go.mod` with module declaration

**Step 2: Create directory structure**

```bash
mkdir -p cmd/mayflycirclefit
mkdir -p internal/{fit,opt,server,ui,store,pkg}
mkdir -p assets
mkdir -p docs
```

Expected: All directories created

**Step 3: Create .gitignore**

Create: `.gitignore`

```
# Binaries
/mayflycirclefit
*.exe
*.dll
*.so
*.dylib

# Test binaries
*.test
*.out

# Go workspace file
go.work

# Build artifacts
/bin/
/dist/

# IDE
.vscode/
.idea/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Profiles
*.prof
*.pprof

# Output
out.png
diff.png

# Temp
/tmp/
```

**Step 4: Commit**

```bash
git init
git add .
git commit -m "chore: initialize project structure"
```

---

### Task 0.2: Add Core Dependencies

**Files:**
- Modify: `go.mod`

**Step 1: Add Cobra for CLI**

```bash
go get github.com/spf13/cobra@latest
```

Expected: Cobra added to go.mod

**Step 2: Add templ for frontend**

```bash
go get github.com/a-h/templ@latest
```

Expected: templ added to go.mod

**Step 3: Add chi for routing (optional)**

```bash
go get github.com/go-chi/chi/v5@latest
```

Expected: chi added to go.mod

**Step 4: Tidy dependencies**

```bash
go mod tidy
```

Expected: go.sum updated

**Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add cobra, templ, chi"
```

---

### Task 0.3: Create Basic CLI Scaffolding

**Files:**
- Create: `cmd/mayflycirclefit/main.go`
- Create: `cmd/mayflycirclefit/root.go`

**Step 1: Write main.go**

Create: `cmd/mayflycirclefit/main.go`

```go
package main

import (
	"log"
	"os"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error: %v\n", err)
		os.Exit(1)
	}
}
```

**Step 2: Write root command**

Create: `cmd/mayflycirclefit/root.go`

```go
package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var (
	logLevel string
	logger   *slog.Logger
)

var rootCmd = &cobra.Command{
	Use:   "mayflycirclefit",
	Short: "High-performance circle fitting with mayfly optimization",
	Long: `MayFlyCircleFit uses evolutionary algorithms to approximate images
with colored circles, featuring CPU/GPU backends and live visualization.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Setup logger
		var level slog.Level
		switch logLevel {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			level = slog.LevelInfo
		}

		opts := &slog.HandlerOptions{Level: level}
		handler := slog.NewJSONHandler(os.Stdout, opts)
		logger = slog.New(handler)
		slog.SetDefault(logger)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
}
```

**Step 3: Build and test**

```bash
go build -o mayflycirclefit ./cmd/mayflycirclefit
./mayflycirclefit --help
```

Expected: Help message displays with persistent flags

**Step 4: Test logging**

```bash
./mayflycirclefit --log-level=debug
```

Expected: JSON log output appears

**Step 5: Commit**

```bash
git add cmd/
git commit -m "feat: add basic CLI scaffolding with cobra and slog"
```

---

### Task 0.4: Create Makefile

**Files:**
- Create: `Makefile`

**Step 1: Write Makefile**

Create: `Makefile`

```makefile
.PHONY: build run fmt test lint clean help

BINARY_NAME=mayflycirclefit
BUILD_DIR=./bin

help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the binary
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/mayflycirclefit

run: build ## Build and run the application
	$(BUILD_DIR)/$(BINARY_NAME)

fmt: ## Format Go code
	go fmt ./...
	gofmt -s -w .

test: ## Run tests
	go test -v ./...

test-coverage: ## Run tests with coverage
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint: ## Run linters
	go vet ./...
	test -z "$$(gofmt -s -l . | tee /dev/stderr)"

clean: ## Clean build artifacts
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	rm -f *.prof *.pprof

install: ## Install the binary
	go install ./cmd/mayflycirclefit

.DEFAULT_GOAL := help
```

**Step 2: Test build**

```bash
make build
```

Expected: Binary created in `bin/mayflycirclefit`

**Step 3: Test run**

```bash
make run
```

Expected: Runs without errors

**Step 4: Test fmt and lint**

```bash
make fmt
make lint
```

Expected: No errors

**Step 5: Commit**

```bash
git add Makefile
git commit -m "chore: add Makefile with common targets"
```

---

### Task 0.5: Create Basic README

**Files:**
- Create: `README.md`

**Step 1: Write README**

Create: `README.md`

```markdown
# MayFlyCircleFit

High-performance circle fitting optimization tool using evolutionary algorithms.

## Overview

MayFlyCircleFit approximates images with colored circles using the Mayfly Algorithm and Differential Evolution. Features CPU/GPU backends, live web visualization, and SIMD-accelerated evaluation.

## Quick Start

```bash
# Build
make build

# Run help
./bin/mayflycirclefit --help

# (More commands coming in later phases)
```

## Project Status

Currently in Phase 0: Project scaffolding

## Development

```bash
# Format code
make fmt

# Run tests
make test

# Run linter
make lint

# Clean build artifacts
make clean
```

## Architecture

```
/cmd/mayflycirclefit          # CLI entry point
/internal/fit           # Rendering, cost functions, pipelines
/internal/opt           # Mayfly/DE optimizers
/internal/server        # HTTP server, jobs, SSE
/internal/ui            # templ components
/internal/store         # Persistence, checkpoints
/internal/pkg           # Utility helpers
/assets                 # Example reference images
```

## License

MIT
```

**Step 2: Verify README renders correctly**

View README.md to ensure formatting is correct

**Step 3: Commit**

```bash
git add README.md
git commit -m "docs: add initial README"
```

---

## Phase 1: Core Domain Model

### Task 1.1: Define Circle and Parameter Types

**Files:**
- Create: `internal/fit/types.go`
- Create: `internal/fit/types_test.go`

**Step 1: Write the failing test**

Create: `internal/fit/types_test.go`

```go
package fit

import (
	"testing"
)

func TestCircleEncoding(t *testing.T) {
	tests := []struct {
		name   string
		circle Circle
		width  int
		height int
	}{
		{
			name: "basic circle",
			circle: Circle{
				X: 50, Y: 50, R: 25,
				CR: 1.0, CG: 0.5, CB: 0.0,
				Opacity: 0.8,
			},
			width:  100,
			height: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := NewParamVector(1, tt.width, tt.height)
			params.EncodeCircle(0, tt.circle)
			decoded := params.DecodeCircle(0)

			if decoded.X != tt.circle.X {
				t.Errorf("X mismatch: got %f, want %f", decoded.X, tt.circle.X)
			}
			if decoded.Y != tt.circle.Y {
				t.Errorf("Y mismatch: got %f, want %f", decoded.Y, tt.circle.Y)
			}
			if decoded.R != tt.circle.R {
				t.Errorf("R mismatch: got %f, want %f", decoded.R, tt.circle.R)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/fit -v
```

Expected: FAIL - package does not exist

**Step 3: Write minimal implementation**

Create: `internal/fit/types.go`

```go
package fit

// Circle represents a colored circle with opacity
type Circle struct {
	X, Y, R          float64 // Position and radius
	CR, CG, CB       float64 // Color in [0,1]
	Opacity          float64 // Opacity in [0,1]
}

// ParamVector encodes K circles as a flat float64 slice
type ParamVector struct {
	Data   []float64
	K      int     // Number of circles
	Width  int     // Image width
	Height int     // Image height
}

const paramsPerCircle = 7

// NewParamVector creates a parameter vector for K circles
func NewParamVector(k, width, height int) *ParamVector {
	return &ParamVector{
		Data:   make([]float64, k*paramsPerCircle),
		K:      k,
		Width:  width,
		Height: height,
	}
}

// EncodeCircle writes a circle to position i in the vector
func (pv *ParamVector) EncodeCircle(i int, c Circle) {
	offset := i * paramsPerCircle
	pv.Data[offset+0] = c.X
	pv.Data[offset+1] = c.Y
	pv.Data[offset+2] = c.R
	pv.Data[offset+3] = c.CR
	pv.Data[offset+4] = c.CG
	pv.Data[offset+5] = c.CB
	pv.Data[offset+6] = c.Opacity
}

// DecodeCircle reads a circle from position i in the vector
func (pv *ParamVector) DecodeCircle(i int) Circle {
	offset := i * paramsPerCircle
	return Circle{
		X:       pv.Data[offset+0],
		Y:       pv.Data[offset+1],
		R:       pv.Data[offset+2],
		CR:      pv.Data[offset+3],
		CG:      pv.Data[offset+4],
		CB:      pv.Data[offset+5],
		Opacity: pv.Data[offset+6],
	}
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/fit -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/fit/
git commit -m "feat: add Circle and ParamVector types with encoding"
```

---

### Task 1.2: Add Bounds and Validation

**Files:**
- Modify: `internal/fit/types.go`
- Modify: `internal/fit/types_test.go`

**Step 1: Write the failing test**

Modify: `internal/fit/types_test.go`

Add to the file:

```go
func TestBoundsValidation(t *testing.T) {
	width, height := 100, 100
	bounds := NewBounds(1, width, height)

	if len(bounds.Lower) != 7 {
		t.Errorf("Expected 7 lower bounds, got %d", len(bounds.Lower))
	}

	// Test X bounds
	if bounds.Lower[0] != 0 || bounds.Upper[0] != float64(width) {
		t.Errorf("X bounds incorrect: [%f, %f]", bounds.Lower[0], bounds.Upper[0])
	}

	// Test Y bounds
	if bounds.Lower[1] != 0 || bounds.Upper[1] != float64(height) {
		t.Errorf("Y bounds incorrect: [%f, %f]", bounds.Lower[1], bounds.Upper[1])
	}

	// Test color bounds [0,1]
	for i := 3; i < 7; i++ {
		if bounds.Lower[i] != 0 || bounds.Upper[i] != 1 {
			t.Errorf("Color/opacity bounds[%d] incorrect: [%f, %f]", i, bounds.Lower[i], bounds.Upper[i])
		}
	}
}

func TestClampCircle(t *testing.T) {
	bounds := NewBounds(1, 100, 100)

	// Out of bounds circle
	circle := Circle{
		X: -10, Y: 150, R: 200,
		CR: 1.5, CG: -0.5, CB: 0.5,
		Opacity: 2.0,
	}

	clamped := bounds.ClampCircle(circle)

	if clamped.X < 0 || clamped.X >= 100 {
		t.Errorf("X not clamped: %f", clamped.X)
	}
	if clamped.Y < 0 || clamped.Y >= 100 {
		t.Errorf("Y not clamped: %f", clamped.Y)
	}
	if clamped.CR < 0 || clamped.CR > 1 {
		t.Errorf("CR not clamped: %f", clamped.CR)
	}
	if clamped.Opacity < 0 || clamped.Opacity > 1 {
		t.Errorf("Opacity not clamped: %f", clamped.Opacity)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/fit -v
```

Expected: FAIL - NewBounds not defined

**Step 3: Write minimal implementation**

Modify: `internal/fit/types.go`

Add to the file:

```go
import "math"

// Bounds defines valid parameter ranges
type Bounds struct {
	Lower []float64
	Upper []float64
	K     int
}

// NewBounds creates bounds for K circles in a WxH image
func NewBounds(k, width, height int) *Bounds {
	maxDim := float64(max(width, height))

	lower := make([]float64, k*paramsPerCircle)
	upper := make([]float64, k*paramsPerCircle)

	for i := 0; i < k; i++ {
		offset := i * paramsPerCircle
		// X bounds [0, width)
		lower[offset+0] = 0
		upper[offset+0] = float64(width)
		// Y bounds [0, height)
		lower[offset+1] = 0
		upper[offset+1] = float64(height)
		// R bounds [1, max(W,H)]
		lower[offset+2] = 1
		upper[offset+2] = maxDim
		// Color and opacity [0, 1]
		for j := 3; j < 7; j++ {
			lower[offset+j] = 0
			upper[offset+j] = 1
		}
	}

	return &Bounds{
		Lower: lower,
		Upper: upper,
		K:     k,
	}
}

// ClampCircle clamps circle parameters to valid bounds
func (b *Bounds) ClampCircle(c Circle) Circle {
	return Circle{
		X:       clamp(c.X, b.Lower[0], b.Upper[0]),
		Y:       clamp(c.Y, b.Lower[1], b.Upper[1]),
		R:       clamp(c.R, b.Lower[2], b.Upper[2]),
		CR:      clamp(c.CR, 0, 1),
		CG:      clamp(c.CG, 0, 1),
		CB:      clamp(c.CB, 0, 1),
		Opacity: clamp(c.Opacity, 0, 1),
	}
}

// ClampVector clamps all parameters in a vector
func (b *Bounds) ClampVector(data []float64) {
	for i := range data {
		data[i] = clamp(data[i], b.Lower[i], b.Upper[i])
	}
}

func clamp(val, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, val))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/fit -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/fit/
git commit -m "feat: add bounds validation and clamping"
```

---

### Task 1.3: Implement MSE Cost Function

**Files:**
- Create: `internal/fit/cost.go`
- Create: `internal/fit/cost_test.go`

**Step 1: Write the failing test**

Create: `internal/fit/cost_test.go`

```go
package fit

import (
	"image"
	"image/color"
	"testing"
)

func TestMSECost(t *testing.T) {
	// Create two identical 2x2 white images
	img1 := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img2 := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img1.Set(x, y, white)
			img2.Set(x, y, white)
		}
	}

	cost := MSECost(img1, img2)
	if cost != 0 {
		t.Errorf("Identical images should have cost 0, got %f", cost)
	}
}

func TestMSECostDifferent(t *testing.T) {
	// Create white and black 2x2 images
	white := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	black := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			white.Set(x, y, color.NRGBA{255, 255, 255, 255})
			black.Set(x, y, color.NRGBA{0, 0, 0, 255})
		}
	}

	cost := MSECost(white, black)
	if cost <= 0 {
		t.Errorf("Different images should have cost > 0, got %f", cost)
	}

	// MSE of white vs black over 3 channels (RGB)
	// Each pixel diff: 255^2 * 3 channels = 195075
	// 4 pixels total: 195075 * 4 / 4 pixels / 3 channels = 65025
	expected := 65025.0
	if cost != expected {
		t.Errorf("Expected cost %f, got %f", expected, cost)
	}
}

func TestMSECostSinglePixel(t *testing.T) {
	// Two identical images except one red pixel
	img1 := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img2 := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img1.Set(x, y, white)
			img2.Set(x, y, white)
		}
	}

	// Change one pixel to red in img2
	img2.Set(0, 0, color.NRGBA{255, 0, 0, 255})

	cost := MSECost(img1, img2)

	// One pixel differs: white (255,255,255) vs red (255,0,0)
	// Diff: R=0, G=255^2, B=255^2
	// MSE = (0 + 65025 + 65025) / (4 pixels * 3 channels) = 10837.5
	expected := 10837.5
	if cost != expected {
		t.Errorf("Expected cost %f, got %f", expected, cost)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/fit -v
```

Expected: FAIL - MSECost not defined

**Step 3: Write minimal implementation**

Create: `internal/fit/cost.go`

```go
package fit

import "image"

// CostFunc computes the error between current and reference images
type CostFunc func(current, reference *image.NRGBA) float64

// MSECost computes Mean Squared Error over sRGB channels
func MSECost(current, reference *image.NRGBA) float64 {
	bounds := current.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		panic("image dimensions must match")
	}

	var sum float64
	numPixels := width * height

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := current.PixOffset(x, y)

			// Extract RGB (ignore alpha for cost)
			r1, g1, b1 := current.Pix[i+0], current.Pix[i+1], current.Pix[i+2]
			r2, g2, b2 := reference.Pix[i+0], reference.Pix[i+1], reference.Pix[i+2]

			// Squared differences
			dr := float64(r1) - float64(r2)
			dg := float64(g1) - float64(g2)
			db := float64(b1) - float64(b2)

			sum += dr*dr + dg*dg + db*db
		}
	}

	// Mean over pixels and channels
	return sum / float64(numPixels*3)
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/fit -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/fit/cost.go internal/fit/cost_test.go
git commit -m "feat: implement MSE cost function with tests"
```

---

## Phase 2: CPU Renderer

### Task 2.1: Define Renderer Interface

**Files:**
- Create: `internal/fit/renderer.go`

**Step 1: Write interface definition**

Create: `internal/fit/renderer.go`

```go
package fit

import "image"

// Renderer renders circles to an image and computes cost
type Renderer interface {
	// Render creates an image from parameter vector
	Render(params []float64) *image.NRGBA

	// Cost computes error between params and reference
	Cost(params []float64) float64

	// Dim returns the dimensionality of the parameter space
	Dim() int

	// Bounds returns lower and upper bounds for parameters
	Bounds() (lower, upper []float64)

	// Reference returns the reference image
	Reference() *image.NRGBA
}
```

**Step 2: Commit**

```bash
git add internal/fit/renderer.go
git commit -m "feat: define Renderer interface"
```

---

### Task 2.2: Implement CPU Renderer

**Files:**
- Create: `internal/fit/renderer_cpu.go`
- Create: `internal/fit/renderer_cpu_test.go`

**Step 1: Write the failing test**

Create: `internal/fit/renderer_cpu_test.go`

```go
package fit

import (
	"image"
	"image/color"
	"testing"
)

func TestCPURendererWhiteCanvas(t *testing.T) {
	// Create a white 10x10 reference
	ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			ref.Set(x, y, white)
		}
	}

	renderer := NewCPURenderer(ref, 0) // 0 circles

	// Empty params should render white canvas
	result := renderer.Render([]float64{})

	// Check if result is all white
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			r, g, b, a := result.At(x, y).RGBA()
			if r != 65535 || g != 65535 || b != 65535 || a != 65535 {
				t.Errorf("Pixel (%d,%d) not white: got (%d,%d,%d,%d)", x, y, r, g, b, a)
			}
		}
	}

	cost := renderer.Cost([]float64{})
	if cost != 0 {
		t.Errorf("White canvas vs white reference should have cost 0, got %f", cost)
	}
}

func TestCPURendererSingleCircle(t *testing.T) {
	// Create a white 20x20 reference
	ref := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			ref.Set(x, y, white)
		}
	}

	renderer := NewCPURenderer(ref, 1)

	// Red circle at center
	params := []float64{
		10, 10, 5,      // x, y, r
		1.0, 0.0, 0.0,  // red
		1.0,            // opaque
	}

	result := renderer.Render(params)

	// Center pixel should be red
	r, g, b, _ := result.At(10, 10).RGBA()
	if r != 65535 || g != 0 || b != 0 {
		t.Errorf("Center pixel should be red, got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}

	// Corner pixel should still be white
	r, g, b, _ = result.At(0, 0).RGBA()
	if r != 65535 || g != 65535 || b != 65535 {
		t.Errorf("Corner pixel should be white, got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/fit -v -run TestCPURenderer
```

Expected: FAIL - NewCPURenderer not defined

**Step 3: Write minimal implementation**

Create: `internal/fit/renderer_cpu.go`

```go
package fit

import (
	"image"
	"image/color"
	"math"
)

// CPURenderer implements software rendering of circles
type CPURenderer struct {
	reference *image.NRGBA
	k         int
	bounds    *Bounds
	costFunc  CostFunc
	width     int
	height    int
}

// NewCPURenderer creates a CPU-based renderer
func NewCPURenderer(reference *image.NRGBA, k int) *CPURenderer {
	bounds := reference.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	return &CPURenderer{
		reference: reference,
		k:         k,
		bounds:    NewBounds(k, width, height),
		costFunc:  MSECost,
		width:     width,
		height:    height,
	}
}

// Render creates an image from parameter vector
func (r *CPURenderer) Render(params []float64) *image.NRGBA {
	// Start with white canvas
	img := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < r.height; y++ {
		for x := 0; x < r.width; x++ {
			img.Set(x, y, white)
		}
	}

	// Decode and render each circle
	pv := &ParamVector{Data: params, K: r.k, Width: r.width, Height: r.height}
	for i := 0; i < r.k; i++ {
		circle := pv.DecodeCircle(i)
		r.renderCircle(img, circle)
	}

	return img
}

// Cost computes error between params and reference
func (r *CPURenderer) Cost(params []float64) float64 {
	rendered := r.Render(params)
	return r.costFunc(rendered, r.reference)
}

// Dim returns the dimensionality of the parameter space
func (r *CPURenderer) Dim() int {
	return r.k * paramsPerCircle
}

// Bounds returns lower and upper bounds for parameters
func (r *CPURenderer) Bounds() (lower, upper []float64) {
	return r.bounds.Lower, r.bounds.Upper
}

// Reference returns the reference image
func (r *CPURenderer) Reference() *image.NRGBA {
	return r.reference
}

// renderCircle composites a circle onto the image using premultiplied alpha
func (r *CPURenderer) renderCircle(img *image.NRGBA, c Circle) {
	// Compute bounding box
	minX := int(math.Max(0, math.Floor(c.X-c.R)))
	maxX := int(math.Min(float64(r.width-1), math.Ceil(c.X+c.R)))
	minY := int(math.Max(0, math.Floor(c.Y-c.R)))
	maxY := int(math.Min(float64(r.height-1), math.Ceil(c.Y+c.R)))

	r2 := c.R * c.R

	// Scan bounding box
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			// Check if inside circle
			dx := float64(x) - c.X
			dy := float64(y) - c.Y
			if dx*dx+dy*dy > r2 {
				continue
			}

			// Composite with premultiplied alpha
			compositePixel(img, x, y, c.CR, c.CG, c.CB, c.Opacity)
		}
	}
}

// compositePixel blends a color onto the image at (x,y) using premultiplied alpha
func compositePixel(img *image.NRGBA, x, y int, r, g, b, alpha float64) {
	i := img.PixOffset(x, y)

	// Current background color (non-premultiplied)
	bgR := float64(img.Pix[i+0]) / 255.0
	bgG := float64(img.Pix[i+1]) / 255.0
	bgB := float64(img.Pix[i+2]) / 255.0
	bgA := float64(img.Pix[i+3]) / 255.0

	// Foreground premultiplied
	fgR := r * alpha
	fgG := g * alpha
	fgB := b * alpha
	fgA := alpha

	// Porter-Duff "over" operator
	outA := fgA + bgA*(1-fgA)
	if outA == 0 {
		return // Transparent
	}

	outR := (fgR + bgR*bgA*(1-fgA)) / outA
	outG := (fgG + bgG*bgA*(1-fgA)) / outA
	outB := (fgB + bgB*bgA*(1-fgA)) / outA

	// Write back as 8-bit
	img.Pix[i+0] = uint8(math.Round(outR * 255))
	img.Pix[i+1] = uint8(math.Round(outG * 255))
	img.Pix[i+2] = uint8(math.Round(outB * 255))
	img.Pix[i+3] = uint8(math.Round(outA * 255))
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/fit -v -run TestCPURenderer
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/fit/renderer_cpu.go internal/fit/renderer_cpu_test.go
git commit -m "feat: implement CPU renderer with circle compositing"
```

---

## Phase 3: Optimizer (Mayfly Baseline)

### Task 3.1: Define Optimizer Interface

**Files:**
- Create: `internal/opt/optimizer.go`

**Step 1: Write interface definition**

Create: `internal/opt/optimizer.go`

```go
package opt

// Optimizer defines an optimization algorithm interface
type Optimizer interface {
	// Run executes the optimization
	// eval: objective function to minimize
	// lower, upper: parameter bounds
	// dim: dimensionality of parameter space
	// Returns: best parameters and best cost
	Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)
}

// Config holds common optimizer configuration
type Config struct {
	MaxIters    int     // Maximum iterations
	PopSize     int     // Population size
	Seed        int64   // Random seed for reproducibility
	Tolerance   float64 // Convergence tolerance
	Verbose     bool    // Enable progress logging
}
```

**Step 2: Commit**

```bash
git add internal/opt/
git commit -m "feat: define Optimizer interface and Config"
```

---

### Task 3.2: Implement Mayfly Algorithm

**Files:**
- Create: `internal/opt/mayfly.go`
- Create: `internal/opt/mayfly_test.go`

**Step 1: Write the failing test**

Create: `internal/opt/mayfly_test.go`

```go
package opt

import (
	"math"
	"testing"
)

// Sphere function: f(x) = sum(x_i^2), minimum at origin
func sphere(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return sum
}

func TestMayflyOnSphere(t *testing.T) {
	cfg := &MayflyConfig{
		Config: Config{
			MaxIters: 100,
			PopSize:  20,
			Seed:     42,
			Verbose:  false,
		},
		Nuptial: 1.5,
		Beta:    2.0,
		Delta:   0.95,
	}

	mayfly := NewMayfly(cfg)

	dim := 3
	lower := make([]float64, dim)
	upper := make([]float64, dim)
	for i := 0; i < dim; i++ {
		lower[i] = -10
		upper[i] = 10
	}

	best, cost := mayfly.Run(sphere, lower, upper, dim)

	if len(best) != dim {
		t.Fatalf("Expected %d parameters, got %d", dim, len(best))
	}

	// Should converge close to zero
	if cost > 0.1 {
		t.Errorf("Expected cost near 0, got %f", cost)
	}

	// Check that best params are near origin
	for i, v := range best {
		if math.Abs(v) > 1.0 {
			t.Errorf("Parameter %d = %f, expected near 0", i, v)
		}
	}
}

func TestMayflyDeterministic(t *testing.T) {
	cfg := &MayflyConfig{
		Config: Config{
			MaxIters: 50,
			PopSize:  10,
			Seed:     123,
			Verbose:  false,
		},
		Nuptial: 1.5,
		Beta:    2.0,
		Delta:   0.95,
	}

	dim := 2
	lower := []float64{-5, -5}
	upper := []float64{5, 5}

	// Run twice with same seed
	mayfly1 := NewMayfly(cfg)
	_, cost1 := mayfly1.Run(sphere, lower, upper, dim)

	mayfly2 := NewMayfly(cfg)
	_, cost2 := mayfly2.Run(sphere, lower, upper, dim)

	if cost1 != cost2 {
		t.Errorf("Non-deterministic: cost1=%f, cost2=%f", cost1, cost2)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/opt -v
```

Expected: FAIL - NewMayfly not defined

**Step 3: Write minimal implementation**

Create: `internal/opt/mayfly.go`

```go
package opt

import (
	"math"
	"math/rand"
)

// MayflyConfig holds Mayfly-specific parameters
type MayflyConfig struct {
	Config
	Nuptial float64 // Nuptial dance coefficient
	Beta    float64 // Attraction coefficient
	Delta   float64 // Velocity damping
}

// Mayfly implements the Mayfly Algorithm
type Mayfly struct {
	cfg  *MayflyConfig
	rng  *rand.Rand
}

type individual struct {
	pos  []float64
	vel  []float64
	cost float64
}

// NewMayfly creates a new Mayfly optimizer
func NewMayfly(cfg *MayflyConfig) *Mayfly {
	return &Mayfly{
		cfg: cfg,
		rng: rand.New(rand.NewSource(cfg.Seed)),
	}
}

// Run executes the Mayfly optimization
func (m *Mayfly) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	// Initialize male and female populations
	males := m.initPopulation(dim, lower, upper, eval)
	females := m.initPopulation(dim, lower, upper, eval)

	globalBest := m.findBest(males, females)

	for iter := 0; iter < m.cfg.MaxIters; iter++ {
		// Update males (attracted to females and global best)
		m.updateMales(males, females, globalBest, lower, upper, eval)

		// Update females (attracted to global best)
		m.updateFemales(females, globalBest, lower, upper, eval)

		// Mating and offspring
		m.mating(males, females, lower, upper, eval)

		// Update global best
		newBest := m.findBest(males, females)
		if newBest.cost < globalBest.cost {
			globalBest = newBest
		}
	}

	return globalBest.pos, globalBest.cost
}

func (m *Mayfly) initPopulation(dim int, lower, upper []float64, eval func([]float64) float64) []*individual {
	pop := make([]*individual, m.cfg.PopSize)
	for i := 0; i < m.cfg.PopSize; i++ {
		pos := make([]float64, dim)
		vel := make([]float64, dim)
		for j := 0; j < dim; j++ {
			pos[j] = lower[j] + m.rng.Float64()*(upper[j]-lower[j])
			vel[j] = 0
		}
		pop[i] = &individual{
			pos:  pos,
			vel:  vel,
			cost: eval(pos),
		}
	}
	return pop
}

func (m *Mayfly) updateMales(males, females []*individual, best *individual, lower, upper []float64, eval func([]float64) float64) {
	for i, male := range males {
		dim := len(male.pos)
		female := females[i%len(females)]

		for j := 0; j < dim; j++ {
			// Attraction to female and global best
			r1, r2 := m.rng.Float64(), m.rng.Float64()
			male.vel[j] = m.cfg.Delta*male.vel[j] +
				r1*m.cfg.Beta*(female.pos[j]-male.pos[j]) +
				r2*m.cfg.Nuptial*(best.pos[j]-male.pos[j])

			male.pos[j] += male.vel[j]
			male.pos[j] = clamp(male.pos[j], lower[j], upper[j])
		}

		male.cost = eval(male.pos)
	}
}

func (m *Mayfly) updateFemales(females []*individual, best *individual, lower, upper []float64, eval func([]float64) float64) {
	for _, female := range females {
		dim := len(female.pos)

		for j := 0; j < dim; j++ {
			r := m.rng.Float64()
			female.vel[j] = m.cfg.Delta*female.vel[j] +
				r*m.cfg.Nuptial*(best.pos[j]-female.pos[j])

			female.pos[j] += female.vel[j]
			female.pos[j] = clamp(female.pos[j], lower[j], upper[j])
		}

		female.cost = eval(female.pos)
	}
}

func (m *Mayfly) mating(males, females []*individual, lower, upper []float64, eval func([]float64) float64) {
	// Simple mating: best males and females produce offspring
	// Replace worst individuals
	dim := len(males[0].pos)

	for i := 0; i < m.cfg.PopSize/4; i++ {
		// Crossover
		offspring := make([]float64, dim)
		for j := 0; j < dim; j++ {
			if m.rng.Float64() < 0.5 {
				offspring[j] = males[i].pos[j]
			} else {
				offspring[j] = females[i].pos[j]
			}

			// Mutation
			if m.rng.Float64() < 0.1 {
				offspring[j] = lower[j] + m.rng.Float64()*(upper[j]-lower[j])
			}
		}

		cost := eval(offspring)

		// Replace worst male
		worstIdx := m.findWorstIdx(males)
		if cost < males[worstIdx].cost {
			males[worstIdx].pos = offspring
			males[worstIdx].cost = cost
			males[worstIdx].vel = make([]float64, dim)
		}
	}
}

func (m *Mayfly) findBest(males, females []*individual) *individual {
	best := males[0]
	for _, ind := range males {
		if ind.cost < best.cost {
			best = ind
		}
	}
	for _, ind := range females {
		if ind.cost < best.cost {
			best = ind
		}
	}

	// Return a copy
	pos := make([]float64, len(best.pos))
	copy(pos, best.pos)
	return &individual{pos: pos, cost: best.cost}
}

func (m *Mayfly) findWorstIdx(pop []*individual) int {
	worstIdx := 0
	for i, ind := range pop {
		if ind.cost > pop[worstIdx].cost {
			worstIdx = i
		}
	}
	return worstIdx
}

func clamp(val, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, val))
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/opt -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/opt/
git commit -m "feat: implement Mayfly algorithm optimizer"
```

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

	mayflyConfig := &opt.MayflyConfig{
		Config: opt.Config{
			MaxIters: 50,
			PopSize:  10,
			Seed:     42,
		},
		Nuptial: 1.5,
		Beta:    2.0,
		Delta:   0.95,
	}

	optimizer := opt.NewMayfly(mayflyConfig)

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

	mayflyConfig := &opt.MayflyConfig{
		Config: opt.Config{
			MaxIters: 30,
			PopSize:  10,
			Seed:     42,
		},
		Nuptial: 1.5,
		Beta:    2.0,
		Delta:   0.95,
	}

	optimizer := opt.NewMayfly(mayflyConfig)

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

	mayflyConfig := &opt.MayflyConfig{
		Config: opt.Config{
			MaxIters: 30,
			PopSize:  10,
			Seed:     42,
		},
		Nuptial: 1.5,
		Beta:    2.0,
		Delta:   0.95,
	}

	optimizer := opt.NewMayfly(mayflyConfig)

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
	mayflyConfig := &opt.MayflyConfig{
		Config: opt.Config{
			MaxIters: iters,
			PopSize:  popSize,
			Seed:     seed,
		},
		Nuptial: 1.5,
		Beta:    2.0,
		Delta:   0.95,
	}
	optimizer := opt.NewMayfly(mayflyConfig)

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
