package renderer

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"runtime"
	"sync"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// CPURenderer implements software rendering of circles
type CPURenderer struct {
	reference    *image.NRGBA
	k            int
	bounds       *fit.Bounds
	costFunc     fit.CostFunc
	width        int
	height       int
	threads      int
	opaqueCanvas bool
	// forceFloatGeometry is reserved for oracle tests and benchmarks. Normal
	// renderers use Q16.16 when the decoded circle is in its safe range.
	forceFloatGeometry bool
	// forceFloat32Geometry is reserved for reduced-precision SIMD tests and
	// benchmarks. On AVX2 hosts it exercises the vectorized span-edge search.
	forceFloat32Geometry bool
	// Buffer pooling to reduce allocations
	canvas    *image.NRGBA // Reusable render buffer
	initialBg []byte       // Precomputed initial background (white or custom canvas)
}

// NewCPURenderer creates a CPU-based renderer with a white background
func NewCPURenderer(reference *image.NRGBA, k int) *CPURenderer {
	if k < 0 {
		k = 0
	}
	bounds := reference.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// Allocate reusable canvas buffer
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))

	// Precompute white background (NRGBA: 255,255,255,255 repeated)
	pixelCount := width * height * 4 // 4 bytes per pixel (RGBA)
	whiteBg := make([]byte, pixelCount)
	for i := 0; i < pixelCount; i++ {
		whiteBg[i] = 255
	}

	return &CPURenderer{
		reference:    reference,
		k:            k,
		bounds:       fit.NewBounds(k, width, height),
		costFunc:     fit.FastMSECost,
		width:        width,
		height:       height,
		threads:      effectiveThreadCount(runtime.GOMAXPROCS(0), height),
		opaqueCanvas: true,
		canvas:       canvas,
		initialBg:    whiteBg,
	}
}

// NewCPURendererWithCanvas creates a CPU-based renderer with a custom initial canvas.
// This is useful for continuing optimization from a previous result (e.g., adding circles
// to an existing partial solution).
// The canvas parameter is copied, so the original image is not modified.
func NewCPURendererWithCanvas(reference *image.NRGBA, canvas *image.NRGBA, k int) *CPURenderer {
	if k < 0 {
		k = 0
	}
	bounds := reference.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// Verify canvas dimensions match reference
	canvasBounds := canvas.Bounds()
	if canvasBounds.Dx() != width || canvasBounds.Dy() != height {
		panic("canvas dimensions must match reference image")
	}

	// Allocate reusable canvas buffer (copy from input canvas)
	canvasCopy := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvasCopy, canvasCopy.Bounds(), canvas, canvas.Bounds().Min, draw.Src)

	// Store initial canvas state for reset between renders
	pixelCount := width * height * 4 // 4 bytes per pixel (RGBA)
	initialBg := make([]byte, pixelCount)
	copy(initialBg, canvasCopy.Pix)

	return &CPURenderer{
		reference:    reference,
		k:            k,
		bounds:       fit.NewBounds(k, width, height),
		costFunc:     fit.FastMSECost,
		width:        width,
		height:       height,
		threads:      effectiveThreadCount(runtime.GOMAXPROCS(0), height),
		opaqueCanvas: pixelsAreOpaque(initialBg),
		canvas:       canvasCopy,
		initialBg:    initialBg,
	}
}

// Render creates an image from parameter vector
func (r *CPURenderer) Render(params []float64) *image.NRGBA {
	// Reset canvas to initial background using fast copy (avoids allocation)
	copy(r.canvas.Pix, r.initialBg)
	if len(params) != r.Dim() {
		return r.canvas
	}
	if r.k == 0 || r.height == 0 {
		return r.canvas
	}

	// Each worker owns a disjoint band of rows and composites every circle in
	// the original order. This keeps the output pixel-exact without locks.
	if r.threads <= 1 {
		r.renderRows(r.canvas, params, 0, r.height)
		return r.canvas
	}

	var workers sync.WaitGroup
	workers.Add(r.threads - 1)
	for worker := 0; worker < r.threads-1; worker++ {
		minY := worker * r.height / r.threads
		maxY := (worker + 1) * r.height / r.threads
		go func() {
			defer workers.Done()
			r.renderRows(r.canvas, params, minY, maxY)
		}()
	}
	r.renderRows(r.canvas, params, (r.threads-1)*r.height/r.threads, r.height)
	workers.Wait()

	return r.canvas
}

func (r *CPURenderer) renderRows(img *image.NRGBA, params []float64, minY, maxY int) {
	pv := fit.ParamVector{Data: params, K: r.k, Width: r.width, Height: r.height}
	for i := 0; i < r.k; i++ {
		circle := pv.DecodeCircle(i)
		r.renderCircleScanlineRows(img, circle, minY, maxY)
	}
}

// Cost computes error between params and reference
func (r *CPURenderer) Cost(params []float64) float64 {
	if len(params) != r.Dim() || r.width == 0 || r.height == 0 {
		return math.Inf(1)
	}
	rendered := r.Render(params)
	return r.costFunc(rendered, r.reference)
}

// newSession creates an independent CPU renderer that preserves the reference,
// base canvas, and selected cost function.
func (r *CPURenderer) newSession(circleCount int) (Renderer, func(), error) {
	if circleCount < 0 {
		return nil, noopCleanup, fmt.Errorf("circle count cannot be negative")
	}

	canvas := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
	initialBg := append([]byte(nil), r.initialBg...)
	return &CPURenderer{
		reference:            r.reference,
		k:                    circleCount,
		bounds:               fit.NewBounds(circleCount, r.width, r.height),
		costFunc:             r.costFunc,
		width:                r.width,
		height:               r.height,
		threads:              r.threads,
		opaqueCanvas:         r.opaqueCanvas,
		forceFloatGeometry:   r.forceFloatGeometry,
		forceFloat32Geometry: r.forceFloat32Geometry,
		canvas:               canvas,
		initialBg:            initialBg,
	}, noopCleanup, nil
}

// newSessionWithCanvas creates a staged session that renders only newly
// optimized circles over an already-retained canvas.
func (r *CPURenderer) newSessionWithCanvas(canvas *image.NRGBA, circleCount int) (Renderer, func(), error) {
	if canvas == nil {
		return nil, noopCleanup, fmt.Errorf("canvas cannot be nil")
	}
	if circleCount < 0 {
		return nil, noopCleanup, fmt.Errorf("circle count cannot be negative")
	}
	if canvas.Bounds().Dx() != r.width || canvas.Bounds().Dy() != r.height {
		return nil, noopCleanup, fmt.Errorf("canvas dimensions must match reference image")
	}

	session := NewCPURendererWithCanvas(r.reference, canvas, circleCount)
	session.costFunc = r.costFunc
	session.threads = r.threads
	session.forceFloatGeometry = r.forceFloatGeometry
	session.forceFloat32Geometry = r.forceFloat32Geometry
	return session, noopCleanup, nil
}

// initialCanvas returns an independent snapshot of the configured base canvas.
func (r *CPURenderer) initialCanvas() *image.NRGBA {
	canvas := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
	copy(canvas.Pix, r.initialBg)
	return canvas
}

// Dim returns the dimensionality of the parameter space
func (r *CPURenderer) Dim() int {
	return r.k * 7 // paramsPerCircle
}

// Bounds returns lower and upper bounds for parameters
func (r *CPURenderer) Bounds() (lower, upper []float64) {
	return r.bounds.Lower, r.bounds.Upper
}

// Reference returns the reference image
func (r *CPURenderer) Reference() *image.NRGBA {
	return r.reference
}

// SetCostFunc sets the cost function used for evaluation
func (r *CPURenderer) SetCostFunc(costFunc fit.CostFunc) {
	r.costFunc = costFunc
}

// UseFastCost restores the runtime-dispatched SIMD cost implementation after a
// custom cost function has been selected. New CPU renderers use this by default.
func (r *CPURenderer) UseFastCost() {
	r.costFunc = fit.FastMSECost
}

// SetThreads configures CPU rendering parallelism. Non-positive values select
// GOMAXPROCS. Values above GOMAXPROCS or the image height are capped to avoid
// oversubscription and empty row shards.
// Call SetThreads before starting an optimization; changing renderer settings
// concurrently with Render is unsupported.
func (r *CPURenderer) SetThreads(threads int) {
	r.threads = effectiveThreadCount(threads, r.height)
}

// Threads returns the effective number of rendering workers.
func (r *CPURenderer) Threads() int {
	return r.threads
}

func effectiveThreadCount(threads, height int) int {
	if threads < 1 {
		threads = runtime.GOMAXPROCS(0)
	}
	if maxThreads := runtime.GOMAXPROCS(0); threads > maxThreads {
		threads = maxThreads
	}
	if height > 0 && threads > height {
		threads = height
	}
	if threads < 1 {
		return 1
	}
	return threads
}

// renderCircle composites a circle onto the image using premultiplied alpha
func (r *CPURenderer) renderCircle(img *image.NRGBA, c fit.Circle) {
	// Early-reject: circle is fully transparent
	if c.Opacity == 0 {
		return
	}

	// Compute AABB (Axis-Aligned Bounding Box)
	minXf := c.X - c.R
	maxXf := c.X + c.R
	minYf := c.Y - c.R
	maxYf := c.Y + c.R

	// Early-reject: circle completely outside image bounds
	if maxXf < 0 || minXf >= float64(r.width) || maxYf < 0 || minYf >= float64(r.height) {
		return
	}

	// Clamp AABB to image bounds (use integer arithmetic)
	minX := int(minXf)
	if minX < 0 {
		minX = 0
	}
	maxX := int(maxXf + 1) // +1 for ceiling
	if maxX > r.width {
		maxX = r.width
	}
	minY := int(minYf)
	if minY < 0 {
		minY = 0
	}
	maxY := int(maxYf + 1) // +1 for ceiling
	if maxY > r.height {
		maxY = r.height
	}

	r2 := c.R * c.R

	// Scan bounding box (note: using < instead of <= due to ceiling adjustment)
	for y := minY; y < maxY; y++ {
		for x := minX; x < maxX; x++ {
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

// renderCircleScanline uses scanline algorithm to avoid per-pixel distance checks
func (r *CPURenderer) renderCircleScanline(img *image.NRGBA, c fit.Circle) {
	r.renderCircleScanlineRows(img, c, 0, r.height)
}

// renderCircleScanlineRows composites the portion of c within [rowStart,
// rowEnd). Callers may safely process disjoint row ranges concurrently.
func (r *CPURenderer) renderCircleScanlineRows(img *image.NRGBA, c fit.Circle, rowStart, rowEnd int) {
	// Early-reject: circle is fully transparent
	if c.Opacity == 0 {
		return
	}

	// Compute vertical bounds
	minYf := c.Y - c.R
	maxYf := c.Y + c.R

	// Early-reject: circle completely outside image bounds
	if maxYf < 0 || minYf >= float64(r.height) {
		return
	}

	// Clamp to image bounds
	minY := int(minYf)
	if minY < 0 {
		minY = 0
	}
	if minY < rowStart {
		minY = rowStart
	}
	maxY := int(maxYf + 1) // +1 for ceiling
	if maxY > r.height {
		maxY = r.height
	}
	if maxY > rowEnd {
		maxY = rowEnd
	}

	r2 := c.R * c.R
	var fixedGeometry fixedCircleQ16
	useFixedGeometry := false
	if !r.forceFloatGeometry && !r.forceFloat32Geometry {
		fixedGeometry, useFixedGeometry = newFixedCircleQ16(c)
	}
	var center32, y32, radiusSquared32 float32
	if r.forceFloat32Geometry {
		center32 = float32(c.X)
		y32 = float32(c.Y)
		radius32 := float32(c.R)
		radiusSquared32 = radius32 * radius32
	}

	// Scanline algorithm: for each row, compute horizontal span
	for y := minY; y < maxY; y++ {
		if useFixedGeometry {
			xStart, xEnd, intersects := fixedGeometry.span(y, r.width)
			if intersects {
				r.compositeCircleSpan(img, c, y, xStart, xEnd)
			}
			continue
		}
		if r.forceFloat32Geometry {
			dy := float32(y) - y32
			remaining := radiusSquared32 - dy*dy
			if remaining < 0 {
				continue
			}
			xStart, xEnd := circleSpanFloat32Selected(center32, remaining, r.width)
			if xStart < 0 {
				xStart = 0
			}
			r.compositeCircleSpan(img, c, y, xStart, xEnd)
			continue
		}

		// Calculate distance from row to circle center
		dy := float64(y) - c.Y
		dy2 := dy * dy

		// Check if row intersects circle
		if dy2 > r2 {
			continue // Row entirely outside circle
		}

		// Find the horizontal extent with the float64 oracle. This path also
		// handles geometry outside the safe signed Q16.16 coordinate range.
		xStart, xEnd := circleSpanFloat64(c.X, r2-dy2, r.width)
		if xStart < 0 {
			xStart = 0
		}
		r.compositeCircleSpan(img, c, y, xStart, xEnd)
	}
}

func (r *CPURenderer) compositeCircleSpan(img *image.NRGBA, c fit.Circle, y, xStart, xEnd int) {
	// Opaque canvases remain opaque under source-over compositing, so their
	// spans can use the runtime-dispatched SIMD implementation.
	if r.opaqueCanvas {
		compositeOpaqueSpan(img.Pix, y*img.Stride+xStart*4, xEnd-xStart, c.CR, c.CG, c.CB, c.Opacity)
		return
	}
	for x := xStart; x < xEnd; x++ {
		compositePixel(img, x, y, c.CR, c.CG, c.CB, c.Opacity)
	}
}

// renderCircleHybrid uses bounding box for small circles and scanline for large ones
// This combines the best of both approaches: avoid search overhead for small circles,
// gain algorithmic advantage for large circles.
//
// BENCHMARK NOTE: Current benchmarks show scanline is faster for ALL circle sizes.
// Direct call to scanline for best performance.
func (r *CPURenderer) renderCircleHybrid(img *image.NRGBA, c fit.Circle) {
	// Benchmarking shows scanline is consistently faster across all circle sizes
	// No conditional needed - always use scanline
	r.renderCircleScanline(img, c)
}

// Optimization constants
const inv255 = 1.0 / 255.0 // Reciprocal for fast division

// compositePixel blends a color onto the image at (x,y) using premultiplied alpha
func compositePixel(img *image.NRGBA, x, y int, r, g, b, alpha float64) {
	// Inline PixOffset calculation (faster than function call)
	i := y*img.Stride + x*4

	// Current background color (non-premultiplied) - use reciprocal multiplication
	bgR := float64(img.Pix[i+0]) * inv255
	bgG := float64(img.Pix[i+1]) * inv255
	bgB := float64(img.Pix[i+2]) * inv255

	// Foreground premultiplied
	fgR := r * alpha
	fgG := g * alpha
	fgB := b * alpha

	// An opaque destination remains opaque under source-over compositing. This
	// is the common path for the default white canvas and avoids alpha
	// normalization, the output-alpha reciprocal, and the alpha store.
	if img.Pix[i+3] == 255 {
		bgBlend := 1 - alpha
		img.Pix[i+0] = uint8((fgR+bgR*bgBlend)*255 + 0.5)
		img.Pix[i+1] = uint8((fgG+bgG*bgBlend)*255 + 0.5)
		img.Pix[i+2] = uint8((fgB+bgB*bgBlend)*255 + 0.5)
		return
	}

	bgA := float64(img.Pix[i+3]) * inv255
	fgA := alpha

	// Porter-Duff "over" operator
	outA := fgA + bgA*(1-fgA)
	if outA == 0 {
		return // Transparent
	}

	// Hoist division: compute reciprocal once, multiply three times
	invOutA := 1.0 / outA

	// Precompute common subexpression
	bgBlend := bgA * (1 - fgA)

	outR := (fgR + bgR*bgBlend) * invOutA
	outG := (fgG + bgG*bgBlend) * invOutA
	outB := (fgB + bgB*bgBlend) * invOutA

	// Write back as 8-bit (use int conversion with +0.5 for rounding, faster than math.Round)
	img.Pix[i+0] = uint8(outR*255 + 0.5)
	img.Pix[i+1] = uint8(outG*255 + 0.5)
	img.Pix[i+2] = uint8(outB*255 + 0.5)
	img.Pix[i+3] = uint8(outA*255 + 0.5)
}
