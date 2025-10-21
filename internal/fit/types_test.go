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

	if clamped.X < 0 || clamped.X > 100 {
		t.Errorf("X not clamped: %f", clamped.X)
	}
	if clamped.Y < 0 || clamped.Y > 100 {
		t.Errorf("Y not clamped: %f", clamped.Y)
	}
	if clamped.CR < 0 || clamped.CR > 1 {
		t.Errorf("CR not clamped: %f", clamped.CR)
	}
	if clamped.Opacity < 0 || clamped.Opacity > 1 {
		t.Errorf("Opacity not clamped: %f", clamped.Opacity)
	}
}
