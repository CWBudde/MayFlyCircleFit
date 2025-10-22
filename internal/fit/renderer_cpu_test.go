package fit

import (
	"image"
	"image/color"
	"testing"
)

func TestCPURendererWhiteCanvas(t *testing.T) {
	// Create a white 10x10 reference
	ref := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			ref.Set(x, y, white)
		}
	}

	renderer := NewCPURenderer(ref, 0) // 0 circles

	// Empty params should render white canvas
	result := renderer.Render([]float64{})

	// Check if result is all white
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			r, g, b, a := result.At(x, y).RGBA()
			if r != 65535 || g != 65535 || b != 65535 || a != 65535 {
				t.Errorf("Pixel (%d,%d) not white: got (%d,%d,%d,%d)", x, y, r, g, b, a)
			}
		}
	}

	cost := renderer.Cost([]float64{})
	if cost != 0 {
		t.Errorf("White canvas vs white reference should have cost 0, got %f", cost)
	}
}

func TestCPURendererSingleCircle(t *testing.T) {
	// Create a white 20x20 reference
	ref := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			ref.Set(x, y, white)
		}
	}

	renderer := NewCPURenderer(ref, 1)

	// Red circle at center
	params := []float64{
		10, 10, 5, // x, y, r
		1.0, 0.0, 0.0, // red
		1.0, // opaque
	}

	result := renderer.Render(params)

	// Center pixel should be red
	r, g, b, _ := result.At(10, 10).RGBA()
	if r != 65535 || g != 0 || b != 0 {
		t.Errorf("Center pixel should be red, got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}

	// Corner pixel should still be white
	r, g, b, _ = result.At(0, 0).RGBA()
	if r != 65535 || g != 65535 || b != 65535 {
		t.Errorf("Corner pixel should be white, got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}
}
