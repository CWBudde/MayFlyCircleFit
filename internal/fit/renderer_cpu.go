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
