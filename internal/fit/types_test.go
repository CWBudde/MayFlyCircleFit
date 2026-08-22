package fit

import (
	"math"
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

func TestBoundsValidation(t *testing.T) {
	width, height := 100, 100
	bounds := NewBounds(1, width, height)

	if len(bounds.Lower) != 7 {
		t.Errorf("Expected 7 lower bounds, got %d", len(bounds.Lower))
	}

	// A center may sit half a dimension beyond either edge. The upper bound is
	// relative to the final pixel coordinate, width-1/height-1.
	if bounds.Lower[0] != -50 || bounds.Upper[0] != 149 {
		t.Errorf("X bounds incorrect: [%f, %f]", bounds.Lower[0], bounds.Upper[0])
	}

	if bounds.Lower[1] != -50 || bounds.Upper[1] != 149 {
		t.Errorf("Y bounds incorrect: [%f, %f]", bounds.Lower[1], bounds.Upper[1])
	}

	// Test color bounds [0,1]
	for i := 3; i < 6; i++ {
		if bounds.Lower[i] != 0 || bounds.Upper[i] != 1 {
			t.Errorf("color bounds[%d] incorrect: [%f, %f]", i, bounds.Lower[i], bounds.Upper[i])
		}
	}

	if bounds.Lower[6] != MinCircleOpacity || bounds.Upper[6] != 1 {
		t.Errorf("opacity bounds incorrect: [%f, %f]", bounds.Lower[6], bounds.Upper[6])
	}
}

func TestClampCircle(t *testing.T) {
	bounds := NewBounds(1, 100, 100)

	// Out of bounds circle
	circle := Circle{
		X: -100, Y: 200, R: 200,
		CR: 1.5, CG: -0.5, CB: 0.5,
		Opacity: 2.0,
	}

	clamped := bounds.ClampCircle(circle)

	if clamped.X != -50 {
		t.Errorf("X not clamped: %f", clamped.X)
	}

	if clamped.Y != 149 {
		t.Errorf("Y not clamped: %f", clamped.Y)
	}

	if clamped.R != 100 {
		t.Errorf("radius not clamped: %f", clamped.R)
	}

	if clamped.CR < 0 || clamped.CR > 1 {
		t.Errorf("CR not clamped: %f", clamped.CR)
	}

	if clamped.Opacity < MinCircleOpacity || clamped.Opacity > 1 {
		t.Errorf("Opacity not clamped: %f", clamped.Opacity)
	}
}

func TestRequiredCircleRadius(t *testing.T) {
	tests := []struct {
		name       string
		x, y       float64
		width      int
		height     int
		wantRadius float64
	}{
		{name: "inside", x: 20, y: 10, width: 100, height: 80, wantRadius: 1},
		{name: "outside left", x: -10, y: 10, width: 100, height: 80, wantRadius: 10},
		{name: "outside left between rows", x: -10, y: 10.5, width: 100, height: 80, wantRadius: math.Ceil(math.Hypot(10, 0.5)*circleQ16Scale) / circleQ16Scale},
		{name: "outside bottom between columns", x: 20.25, y: 84, width: 100, height: 80, wantRadius: math.Ceil(math.Hypot(0.25, 5)*circleQ16Scale) / circleQ16Scale},
		{name: "outside corner", x: -3, y: -4, width: 100, height: 80, wantRadius: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RequiredCircleRadius(tt.x, tt.y, tt.width, tt.height); math.Abs(got-tt.wantRadius) > 1e-12 {
				t.Fatalf("RequiredCircleRadius() = %g, want %g", got, tt.wantRadius)
			}
		})
	}
}

func TestRequiredCircleRadiusCoversRasterSampleAfterQ16Quantization(t *testing.T) {
	const q16Scale = 1 << 16
	tests := []struct {
		name          string
		x, y          float64
		width, height int
	}{
		{name: "left between rows", x: -50, y: 40.5, width: 100, height: 80},
		{name: "right fractional row", x: 148.875, y: 17.49999, width: 100, height: 80},
		{name: "above fractional column", x: 73.50001, y: -40, width: 100, height: 80},
		{name: "outside fractional corner", x: -30.125, y: -39.875, width: 100, height: 80},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nearestX := clamp(math.Round(tt.x), 0, float64(tt.width-1))
			nearestY := clamp(math.Round(tt.y), 0, float64(tt.height-1))
			xQ := int64(math.Round(tt.x * q16Scale))
			yQ := int64(math.Round(tt.y * q16Scale))
			radiusQ := int64(math.Round(RequiredCircleRadius(tt.x, tt.y, tt.width, tt.height) * q16Scale))
			dxQ := int64(nearestX*q16Scale) - xQ

			dyQ := int64(nearestY*q16Scale) - yQ
			if dxQ*dxQ+dyQ*dyQ > radiusQ*radiusQ {
				t.Fatalf("nearest sample (%g,%g) is outside quantized radius", nearestX, nearestY)
			}

			dx32 := float32(tt.x) - float32(nearestX)
			dy32 := float32(tt.y) - float32(nearestY)

			radius32 := float32(RequiredCircleRadius(tt.x, tt.y, tt.width, tt.height))
			if dx32*dx32+dy32*dy32 > radius32*radius32 {
				t.Fatalf("nearest sample (%g,%g) is outside float32 radius", nearestX, nearestY)
			}
		})
	}
}

func TestBoundsValidateCircle(t *testing.T) {
	bounds := NewBounds(1, 100, 80)

	tests := []struct {
		name   string
		circle Circle
		valid  bool
	}{
		{name: "minimum on canvas", circle: Circle{X: 0, Y: 0, R: 1, Opacity: MinCircleOpacity}, valid: true},
		{name: "exact edge tangency reaches raster sample", circle: Circle{X: -50, Y: 40, R: 50, Opacity: 1}, valid: true},
		{name: "continuous edge ignores fractional row", circle: Circle{X: -50, Y: 40.5, R: 50, Opacity: 1}, valid: false},
		{name: "outside corner exact hypot reaches sample", circle: Circle{X: -30, Y: -40, R: 50, Opacity: 1}, valid: true},
		{name: "outside radius too small", circle: Circle{X: -30, Y: -40, R: 49.9, Opacity: 1}, valid: false},
		{name: "center too far left", circle: Circle{X: -50.1, Y: 40, R: 100, Opacity: 1}, valid: false},
		{name: "center too far right", circle: Circle{X: 149.1, Y: 40, R: 100, Opacity: 1}, valid: false},
		{name: "zero radius", circle: Circle{X: 20, Y: 20, R: 0, Opacity: 1}, valid: false},
		{name: "zero opacity", circle: Circle{X: 20, Y: 20, R: 1, Opacity: 0}, valid: false},
		{name: "opacity above one", circle: Circle{X: 20, Y: 20, R: 1, Opacity: 1.01}, valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bounds.ValidateCircle(tt.circle)
			if (err == nil) != tt.valid {
				t.Fatalf("ValidateCircle() error = %v, valid = %t", err, tt.valid)
			}
		})
	}
}

func TestClampCircleRaisesRadiusForOutsideCenter(t *testing.T) {
	bounds := NewBounds(1, 100, 80)
	clamped := bounds.ClampCircle(Circle{X: -30, Y: -40, R: 1, Opacity: 0})

	wantRadius := 50.0
	if clamped.R != wantRadius {
		t.Fatalf("radius = %g, want %g", clamped.R, wantRadius)
	}

	if clamped.Opacity != MinCircleOpacity {
		t.Fatalf("opacity = %g, want %g", clamped.Opacity, MinCircleOpacity)
	}

	err := bounds.ValidateCircle(clamped)
	if err != nil {
		t.Fatalf("clamped circle is invalid: %v", err)
	}
}

func TestClampIndependentVectorPreservesDynamicRadiusViolation(t *testing.T) {
	bounds := NewBounds(1, 100, 80)
	params := []float64{-100, 200, 0, -1, 2, 0.5, 0}
	bounds.ClampIndependentVector(params)

	circle := (&ParamVector{Data: params, K: 1, Width: 100, Height: 80}).DecodeCircle(0)
	if circle.X != bounds.Lower[0] || circle.Y != bounds.Upper[1] || circle.R != MinCircleRadius {
		t.Fatalf("independently clamped geometry = (%g,%g,%g)", circle.X, circle.Y, circle.R)
	}

	if violation := bounds.RadiusViolation(circle); violation <= 0 {
		t.Fatalf("radius violation = %g, want positive", violation)
	}

	if circle.Opacity != MinCircleOpacity {
		t.Fatalf("opacity = %g, want %g", circle.Opacity, MinCircleOpacity)
	}
}
