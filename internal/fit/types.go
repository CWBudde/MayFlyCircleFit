package fit

import (
	"errors"
	"fmt"
	"math"
)

// Circle represents a colored circle with opacity.
type Circle struct {
	X, Y, R    float64 // Position and radius
	CR, CG, CB float64 // Color in [0,1]
	Opacity    float64 // Optimized opacity is in [MinCircleOpacity, 1]
}

// ParamVector encodes K circles as a flat float64 slice.
type ParamVector struct {
	Data   []float64
	K      int // Number of circles
	Width  int // Image width
	Height int // Image height
}

const paramsPerCircle = 7

// NewParamVector creates a parameter vector for K circles.
func NewParamVector(k, width, height int) *ParamVector {
	return &ParamVector{
		Data:   make([]float64, k*paramsPerCircle),
		K:      k,
		Width:  width,
		Height: height,
	}
}

// EncodeCircle writes a circle to position i in the vector.
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

// DecodeCircle reads a circle from position i in the vector.
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

const (
	// MinCircleRadius keeps optimized circles large enough to cover at least one
	// pixel when their center is on the canvas.
	MinCircleRadius = 1.0
	// MinCircleOpacity is the smallest opacity that can affect an 8-bit output
	// channel. It also makes the optimizer's opacity bound strictly positive.
	MinCircleOpacity = 1.0 / 255.0
	// MaxCenterOffsetFraction permits centers up to half a canvas dimension
	// beyond each corresponding edge.
	MaxCenterOffsetFraction = 0.5
	circleQ16Scale          = 65536.0
)

// Bounds defines valid parameter ranges. Radius has an additional dynamic
// lower bound: a center outside the canvas must have a radius large enough to
// cover the nearest integer pixel sample after renderer quantization.
type Bounds struct {
	Lower  []float64
	Upper  []float64
	K      int
	Width  int
	Height int
}

// NewBounds creates bounds for K circles in a WxH image.
func NewBounds(k, width, height int) *Bounds {
	maxDim := float64(max(width, height))
	maxX := float64(max(width-1, 0))
	maxY := float64(max(height-1, 0))
	xOffset := MaxCenterOffsetFraction * float64(width)
	yOffset := MaxCenterOffsetFraction * float64(height)

	lower := make([]float64, k*paramsPerCircle)
	upper := make([]float64, k*paramsPerCircle)

	for i := 0; i < k; i++ {
		offset := i * paramsPerCircle
		// Centers may lie up to half the canvas width/height beyond an edge.
		lower[offset+0] = -xOffset
		upper[offset+0] = maxX + xOffset
		lower[offset+1] = -yOffset
		upper[offset+1] = maxY + yOffset
		// R bounds [1, max(W,H)]
		lower[offset+2] = MinCircleRadius
		upper[offset+2] = maxDim
		// Color bounds [0, 1].
		for j := 3; j < 6; j++ {
			lower[offset+j] = 0
			upper[offset+j] = 1
		}
		// Opacity must be positive and no greater than one.
		lower[offset+6] = MinCircleOpacity
		upper[offset+6] = 1
	}

	return &Bounds{
		Lower:  lower,
		Upper:  upper,
		K:      k,
		Width:  width,
		Height: height,
	}
}

// RequiredCircleRadius returns the minimum radius for a circle centered at
// (x,y). For an on-canvas center it is one. For an outside center it is the
// distance to the nearest integer pixel sample. The result is also raised to
// the exact Q16.16 radius needed after the renderer independently quantizes
// the center and radius.
// Using an actual sample is important when, for example, x is outside while y
// lies between two pixel rows: distance to the continuous canvas rectangle
// would ignore the fractional y distance and could produce an invisible
// tangent circle.
func RequiredCircleRadius(x, y float64, width, height int) float64 {
	maxX := float64(max(width-1, 0))
	maxY := float64(max(height-1, 0))
	nearestX := clamp(math.Round(x), 0, maxX)
	nearestY := clamp(math.Round(y), 0, maxY)
	rasterDistance := math.Hypot(x-nearestX, y-nearestY)
	xQ := math.Round(x * circleQ16Scale)
	yQ := math.Round(y * circleQ16Scale)
	nearestXQ := nearestX * circleQ16Scale
	nearestYQ := nearestY * circleQ16Scale
	quantizedDistance := math.Ceil(math.Hypot(xQ-nearestXQ, yQ-nearestYQ)) / circleQ16Scale
	required := math.Max(MinCircleRadius, math.Max(rasterDistance, quantizedDistance))
	dx32 := float32(x) - float32(nearestX)
	dy32 := float32(y) - float32(nearestY)

	radius32 := float32(required)
	if radius32*radius32 < dx32*dx32+dy32*dy32 {
		required = math.Max(required, float64(math.Nextafter32(radius32, float32(math.Inf(1)))))
	}

	return required
}

// ValidateCircle checks both the independent optimizer bounds and the dynamic
// radius requirement for an outside center.
func (b *Bounds) ValidateCircle(c Circle) error {
	if b == nil || len(b.Lower) < paramsPerCircle || len(b.Upper) < paramsPerCircle {
		return errors.New("circle bounds are not initialized")
	}

	values := []float64{c.X, c.Y, c.R, c.CR, c.CG, c.CB, c.Opacity}
	for i, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("parameter %d is not finite", i)
		}

		if value < b.Lower[i] || value > b.Upper[i] {
			return fmt.Errorf("parameter %d is %g, outside [%g, %g]", i, value, b.Lower[i], b.Upper[i])
		}
	}

	requiredRadius := RequiredCircleRadius(c.X, c.Y, b.Width, b.Height)
	if c.R < requiredRadius {
		return fmt.Errorf("radius %g is smaller than required radius %g for center (%g, %g)", c.R, requiredRadius, c.X, c.Y)
	}

	return nil
}

// ValidVector reports whether data contains exactly K valid circles.
func (b *Bounds) ValidVector(data []float64) bool {
	if b == nil || len(data) != b.K*paramsPerCircle {
		return false
	}

	vector := ParamVector{Data: data, K: b.K, Width: b.Width, Height: b.Height}
	for i := 0; i < b.K; i++ {
		if b.ValidateCircle(vector.DecodeCircle(i)) != nil {
			return false
		}
	}

	return true
}

// RadiusViolation returns the dynamic raster-coverage constraint value for c.
// Values at or below zero are feasible; positive values are the missing radius
// in pixels. This form is suitable for continuous constrained optimizers.
func (b *Bounds) RadiusViolation(c Circle) float64 {
	return RequiredCircleRadius(c.X, c.Y, b.Width, b.Height) - c.R
}

// ClampCircle clamps circle parameters to valid bounds.
func (b *Bounds) ClampCircle(c Circle) Circle {
	clamped := Circle{
		X:       clamp(c.X, b.Lower[0], b.Upper[0]),
		Y:       clamp(c.Y, b.Lower[1], b.Upper[1]),
		CR:      clamp(c.CR, 0, 1),
		CG:      clamp(c.CG, 0, 1),
		CB:      clamp(c.CB, 0, 1),
		Opacity: clamp(c.Opacity, MinCircleOpacity, 1),
	}
	requiredRadius := RequiredCircleRadius(clamped.X, clamped.Y, b.Width, b.Height)
	clamped.R = clamp(c.R, requiredRadius, b.Upper[2])

	return clamped
}

// ClampVector clamps all parameters in a vector.
func (b *Bounds) ClampVector(data []float64) {
	circleCount := min(len(data)/paramsPerCircle, b.K)

	vector := ParamVector{Data: data, K: circleCount, Width: b.Width, Height: b.Height}
	for i := 0; i < circleCount; i++ {
		vector.EncodeCircle(i, b.ClampCircle(vector.DecodeCircle(i)))
	}
}

// ClampIndependentVector applies only the rectangular optimizer bounds. It
// deliberately leaves the center/radius relationship untouched so constrained
// optimizers can measure and navigate its continuous violation instead of
// collapsing invalid candidates onto an exact tangent boundary.
func (b *Bounds) ClampIndependentVector(data []float64) {
	for i := 0; i < len(data) && i < len(b.Lower); i++ {
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

func min(a, b int) int {
	if a < b {
		return a
	}

	return b
}
