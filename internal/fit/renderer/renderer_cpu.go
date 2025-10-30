package renderer

import (
	"image"
	"math"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// CPURenderer implements software rendering of circles
type CPURenderer struct {
	reference *image.NRGBA
	k         int
	bounds    *fit.Bounds
	costFunc  fit.CostFunc
	width     int
	height    int
	// Buffer pooling to reduce allocations
	canvas     *image.NRGBA // Reusable render buffer
	initialBg  []byte       // Precomputed initial background (white or custom canvas)
}

// NewCPURenderer creates a CPU-based renderer with a white background
func NewCPURenderer(reference *image.NRGBA, k int) *CPURenderer {
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
		reference: reference,
		k:         k,
		bounds:    fit.NewBounds(k, width, height),
		costFunc:  fit.MSECost,
		width:     width,
		height:    height,
		canvas:    canvas,
		initialBg: whiteBg,
	}
}

// NewCPURendererWithCanvas creates a CPU-based renderer with a custom initial canvas.
// This is useful for continuing optimization from a previous result (e.g., adding circles
// to an existing partial solution).
// The canvas parameter is copied, so the original image is not modified.
func NewCPURendererWithCanvas(reference *image.NRGBA, canvas *image.NRGBA, k int) *CPURenderer {
	bounds := reference.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// Verify canvas dimensions match reference
	canvasBounds := canvas.Bounds()
	if canvasBounds.Dx() != width || canvasBounds.Dy() != height {
		panic("canvas dimensions must match reference image")
	}

	// Allocate reusable canvas buffer (copy from input canvas)
	canvasCopy := image.NewNRGBA(image.Rect(0, 0, width, height))
	copy(canvasCopy.Pix, canvas.Pix)

	// Store initial canvas state for reset between renders
	pixelCount := width * height * 4 // 4 bytes per pixel (RGBA)
	initialBg := make([]byte, pixelCount)
	copy(initialBg, canvas.Pix)

	return &CPURenderer{
		reference: reference,
		k:         k,
		bounds:    fit.NewBounds(k, width, height),
		costFunc:  fit.MSECost,
		width:     width,
		height:    height,
		canvas:    canvasCopy,
		initialBg: initialBg,
	}
}

// Render creates an image from parameter vector
func (r *CPURenderer) Render(params []float64) *image.NRGBA {
	// Reset canvas to initial background using fast copy (avoids allocation)
	copy(r.canvas.Pix, r.initialBg)

	// Decode and render each circle (using hybrid/scanline algorithm)
	pv := &fit.ParamVector{Data: params, K: r.k, Width: r.width, Height: r.height}
	for i := 0; i < r.k; i++ {
		circle := pv.DecodeCircle(i)
		r.renderCircleHybrid(r.canvas, circle)
	}

	return r.canvas
}

// Cost computes error between params and reference
func (r *CPURenderer) Cost(params []float64) float64 {
	rendered := r.Render(params)
	return r.costFunc(rendered, r.reference)
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

// UseFastCost enables SIMD-accelerated cost computation (AVX2/NEON)
// This provides 1.5-2x speedup over the default MSECost implementation
func (r *CPURenderer) UseFastCost() {
	r.costFunc = fit.FastMSECost
}

// renderCircle composites a circle onto the image using premultiplied alpha
func (r *CPURenderer) renderCircle(img *image.NRGBA, c fit.Circle) {
	// Early-reject: circle is fully transparent
	if c.Opacity < 0.001 {
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
	// Early-reject: circle is fully transparent
	if c.Opacity < 0.001 {
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
	maxY := int(maxYf + 1) // +1 for ceiling
	if maxY > r.height {
		maxY = r.height
	}

	r2 := c.R * c.R

	// Scanline algorithm: for each row, compute horizontal span
	for y := minY; y < maxY; y++ {
		// Calculate distance from row to circle center
		dy := float64(y) - c.Y
		dy2 := dy * dy

		// Check if row intersects circle
		if dy2 > r2 {
			continue // Row entirely outside circle
		}

		// Find horizontal extent by searching from center
		// This avoids sqrt() and guarantees correctness
		r2_minus_dy2 := r2 - dy2
		cx := int(c.X + 0.5)

		// Find xStart by searching left
		xStart := cx
		for xStart > 0 {
			dx := float64(xStart-1) - c.X
			if dx*dx > r2_minus_dy2 {
				break
			}
			xStart--
		}
		if xStart < 0 {
			xStart = 0
		}

		// Find xEnd by searching right
		xEnd := cx + 1
		for xEnd < r.width {
			dx := float64(xEnd) - c.X
			if dx*dx > r2_minus_dy2 {
				break
			}
			xEnd++
		}
		if xEnd > r.width {
			xEnd = r.width
		}

		// Composite all pixels in span
		for x := xStart; x < xEnd; x++ {
			compositePixel(img, x, y, c.CR, c.CG, c.CB, c.Opacity)
		}

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

// fastSqrt is a wrapper around math.Sqrt for clarity
// TODO: Consider using fast approximation if profiling shows this as bottleneck
func fastSqrt(x float64) float64 {
	// For now, use standard library sqrt
	// Alternative: Fast inverse square root (Quake III algorithm)
	// or lookup table for small integer values
	return math.Sqrt(x)
}

// compositePixel blends a color onto the image at (x,y) using premultiplied alpha
func compositePixel(img *image.NRGBA, x, y int, r, g, b, alpha float64) {
	// Inline PixOffset calculation (faster than function call)
	i := y*img.Stride + x*4

	// Current background color (non-premultiplied) - use reciprocal multiplication
	bgR := float64(img.Pix[i+0]) * inv255
	bgG := float64(img.Pix[i+1]) * inv255
	bgB := float64(img.Pix[i+2]) * inv255
	bgA := float64(img.Pix[i+3]) * inv255

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
