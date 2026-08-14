package renderer

import (
	"image"
	"image/color"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestAnalyzeCircleVisibilityCountsChangedCanvasPixels(t *testing.T) {
	const width, height = 12, 8
	reference := image.NewNRGBA(image.Rect(0, 0, width, height))
	renderer := NewCPURenderer(reference, 8)
	renderer.SetThreads(1)

	circles := []fit.Circle{
		{X: 2, Y: 2, R: 1, Opacity: 1},
		// Repainting the same pixels with the same opaque color is invisible.
		{X: 2, Y: 2, R: 1, Opacity: 1},
		{X: 6, Y: 2, R: 1, CR: 1, Opacity: 1},
		// A transparent circle is always invisible.
		{X: 9, Y: 2, R: 1, CR: 1, CG: 1, Opacity: 0},
		// A center on the right boundary still changes the tangent edge pixel.
		{X: width, Y: 6, R: 1, Opacity: 1},
		// Opaque white over the untouched white canvas is invisible.
		{X: 6, Y: 6, R: 1, CR: 1, CG: 1, CB: 1, Opacity: 1},
		// A circle entirely beyond the right edge cannot change the canvas.
		{X: width + 2, Y: 6, R: 1, Opacity: 1},
		// Positive opacity can still be too small to alter an 8-bit pixel.
		{X: 9, Y: 6, R: 1, Opacity: 1e-12},
	}
	params := make([]float64, len(circles)*paramsPerCircle)
	vector := fit.ParamVector{Data: params, K: len(circles), Width: width, Height: height}
	for i, circle := range circles {
		vector.EncodeCircle(i, circle)
	}

	got, err := AnalyzeCircleVisibility(renderer, params)
	if err != nil {
		t.Fatalf("AnalyzeCircleVisibility() error = %v", err)
	}
	want := []CircleVisibility{
		{Circle: 1, ChangedPixels: 5, Valid: true},
		{Circle: 2, ChangedPixels: 0, Valid: true},
		{Circle: 3, ChangedPixels: 5, Valid: true},
		{Circle: 4, ChangedPixels: 0, Valid: false},
		{Circle: 5, ChangedPixels: 1, Valid: true},
		{Circle: 6, ChangedPixels: 0, Valid: true},
		{Circle: 7, ChangedPixels: 0, Valid: false},
		{Circle: 8, ChangedPixels: 0, Valid: false},
	}
	if len(got) != len(want) {
		t.Fatalf("visibility count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Circle != want[i].Circle || got[i].ChangedPixels != want[i].ChangedPixels || got[i].Valid != want[i].Valid {
			t.Errorf("circle %d visibility = %#v, want %#v", i+1, got[i], want[i])
		}
		if got[i].Valid && got[i].ValidationError != "" {
			t.Errorf("valid circle %d has validation error %q", i+1, got[i].ValidationError)
		}
		if !got[i].Valid && got[i].ValidationError == "" {
			t.Errorf("invalid circle %d has no validation error", i+1)
		}
	}
}

func TestAnalyzeCircleVisibilityAcceptsOutsideCenterThatReachesCanvas(t *testing.T) {
	const width, height = 20, 10
	reference := image.NewNRGBA(image.Rect(0, 0, width, height))
	renderer := NewCPURenderer(reference, 1)
	renderer.SetThreads(1)
	params := []float64{
		-width / 2, -height / 2, 12, 0, 0, 0, 1,
	}

	got, err := AnalyzeCircleVisibility(renderer, params)
	if err != nil {
		t.Fatalf("AnalyzeCircleVisibility() error = %v", err)
	}
	if len(got) != 1 || !got[0].Valid || got[0].ChangedPixels == 0 {
		t.Fatalf("visibility = %#v, want a valid circle changing the canvas", got)
	}
}

func TestAnalyzeCircleVisibilityRejectsInvalidInput(t *testing.T) {
	if _, err := AnalyzeCircleVisibility(nil, nil); err == nil {
		t.Fatal("AnalyzeCircleVisibility(nil) error = nil")
	}

	reference := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	reference.SetNRGBA(0, 0, color.NRGBA{A: 255})
	renderer := NewCPURenderer(reference, 1)
	if _, err := AnalyzeCircleVisibility(renderer, make([]float64, paramsPerCircle-1)); err == nil {
		t.Fatal("AnalyzeCircleVisibility(short params) error = nil")
	}
}
