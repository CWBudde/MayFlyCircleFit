package server

import (
	"image"
	"image/color"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
)

func TestComputeDiffImageUsesNormalizedAbsoluteRGBError(t *testing.T) {
	t.Parallel()

	ref := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	best := image.NewNRGBA(ref.Bounds())

	ref.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	best.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 1})
	ref.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	best.SetNRGBA(1, 0, color.NRGBA{A: 255})
	ref.SetNRGBA(2, 0, color.NRGBA{R: 255, A: 255})
	best.SetNRGBA(2, 0, color.NRGBA{A: 255})

	diff := computeDiffImage(ref, best, fit.ColormapTurbo)

	tests := []struct {
		x           int
		error       float64
		maxError    float64
		colormap    fit.Colormap
		description string
	}{
		{x: 0, error: 0, maxError: 255, colormap: fit.ColormapTurbo, description: "matching RGB ignores alpha"},
		{x: 1, error: 255, maxError: 255, colormap: fit.ColormapTurbo, description: "maximum RGB error"},
		{x: 2, error: 85, maxError: 255, colormap: fit.ColormapTurbo, description: "mean absolute channel error"},
	}
	for _, test := range tests {
		got := diff.NRGBAAt(test.x, 0)

		want := fit.MapErrorColor(test.error, test.maxError, test.colormap)
		if got != want {
			t.Errorf("%s pixel = %#v, want %#v", test.description, got, want)
		}
	}
}

func TestComputeDiffImageSupportsMagma(t *testing.T) {
	t.Parallel()

	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	best := image.NewNRGBA(ref.Bounds())
	ref.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	got := computeDiffImage(ref, best, fit.ColormapMagma).NRGBAAt(0, 0)

	want := fit.MapErrorColor(255, 255, fit.ColormapMagma)
	if got != want {
		t.Errorf("Magma maximum-error pixel = %#v, want %#v", got, want)
	}
}
