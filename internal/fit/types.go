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
