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
