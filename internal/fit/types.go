package fit

import "math"

// Circle represents a colored circle with opacity
type Circle struct {
	X, Y, R    float64 // Position and radius
	CR, CG, CB float64 // Color in [0,1]
	Opacity    float64 // Opacity in [0,1]
}

// ParamVector encodes K circles as a flat float64 slice
type ParamVector struct {
	Data   []float64
	K      int // Number of circles
	Width  int // Image width
	Height int // Image height
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
