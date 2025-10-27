package fit

import (
	"image"
)

// CPURenderer implements software rendering of circles
type CPURenderer struct {
	reference *image.NRGBA
	k         int
	bounds    *Bounds
	costFunc  CostFunc
	width     int
	height    int
	// Buffer pooling to reduce allocations
	canvas  *image.NRGBA // Reusable render buffer
	whiteBg []byte       // Precomputed white background pattern
}

// NewCPURenderer creates a CPU-based renderer
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
		bounds:    NewBounds(k, width, height),
		costFunc:  MSECost,
		width:     width,
		height:    height,
		canvas:    canvas,
		whiteBg:   whiteBg,
	}
}

// Render creates an image from parameter vector
func (r *CPURenderer) Render(params []float64) *image.NRGBA {
	// Reset canvas to white using fast copy (avoids allocation)
	copy(r.canvas.Pix, r.whiteBg)

	// Decode and render each circle
	pv := &ParamVector{Data: params, K: r.k, Width: r.width, Height: r.height}
	for i := 0; i < r.k; i++ {
		circle := pv.DecodeCircle(i)
		r.renderCircle(r.canvas, circle)
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

	// Write back as 8-bit (use int conversion with +0.5 for rounding, faster than math.Round)
	img.Pix[i+0] = uint8(outR*255 + 0.5)
	img.Pix[i+1] = uint8(outG*255 + 0.5)
	img.Pix[i+2] = uint8(outB*255 + 0.5)
	img.Pix[i+3] = uint8(outA*255 + 0.5)
}
