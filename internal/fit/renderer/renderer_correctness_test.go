package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"runtime"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
)

// TestCPURendererMatchesPreOptimizationBaseline protects the rendering
// semantics that existed before the Phase 9 CPU optimizations. The reference
// implementation below intentionally mirrors revision 3650d61: it uses the
// original bounding-box traversal, Porter-Duff arithmetic, and math.Round.
func TestCPURendererMatchesPreOptimizationBaseline(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		circles       []fit.Circle
		customCanvas  bool
	}{
		{name: "empty_1x1", width: 1, height: 1},
		{
			name: "single_circle", width: 16, height: 16,
			circles: []fit.Circle{
				{X: 7.5, Y: 8.25, R: 1, CR: 0.25, CG: 0.5, CB: 0.75, Opacity: 0.4},
			},
		},
		{
			name: "minimum_radius_small_image", width: 7, height: 5,
			circles: []fit.Circle{
				{X: 0, Y: 0, R: 1, CR: 1, Opacity: 1},
				{X: 6, Y: 4, R: 1, CG: 1, Opacity: 0.5},
			},
		},
		{
			name: "oversized_and_clipped", width: 9, height: 6,
			circles: []fit.Circle{
				{X: 0.25, Y: 0.75, R: 12, CR: 0.2, CG: 0.4, CB: 0.8, Opacity: 0.35},
				{X: 8.75, Y: 5.25, R: 4.5, CR: 0.9, CG: 0.1, CB: 0.3, Opacity: 1},
			},
		},
		{
			name: "fractional_overlaps", width: 31, height: 23, customCanvas: true,
			circles: []fit.Circle{
				{X: 15.5, Y: 11.5, R: 10.25, CR: 1, CG: 0.1, CB: 0.2, Opacity: 0.6},
				{X: 10.125, Y: 9.875, R: 7.75, CR: 0.1, CG: 1, CB: 0.2, Opacity: 0.7},
				{X: 20.875, Y: 13.125, R: 8.5, CR: 0.1, CG: 0.2, CB: 1, Opacity: 0.8},
				{X: 15, Y: 11, R: 2, CR: 0.8, CG: 0.7, CB: 0.1, Opacity: 0},
			},
		},
		{
			name: "opacity_rejection_boundary", width: 17, height: 13, customCanvas: true,
			circles: []fit.Circle{
				{X: 4, Y: 4, R: 3, CR: 0, CG: 0, CB: 0, Opacity: 0.000999},
				{X: 8, Y: 6, R: 4, CR: 1, CG: 1, CB: 1, Opacity: 0.001},
				{X: 12, Y: 8, R: 3, CR: 0.3, CG: 0.6, CB: 0.9, Opacity: 1},
			},
		},
		{
			name: "fully_outside", width: 13, height: 11,
			circles: []fit.Circle{
				{X: -4, Y: 5, R: 2, CR: 1, Opacity: 1},
				{X: 17, Y: 5, R: 2, CG: 1, Opacity: 1},
				{X: 6, Y: -4, R: 2, CB: 1, Opacity: 1},
				{X: 6, Y: 15, R: 2, CR: 1, CG: 1, Opacity: 1},
			},
		},
		{
			name: "odd_rectangular_many_circles", width: 127, height: 91,
			circles: decodeCircles(deterministicParams(24, 127, 91, 910), 24, 127, 91),
		},
		{
			name: "large_many_circles", width: 257, height: 193, customCanvas: true,
			circles: decodeCircles(deterministicParams(48, 257, 193, 911), 48, 257, 193),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reference := randomNRGBA(test.width, test.height, 901)

			var initial *image.NRGBA
			if test.customCanvas {
				initial = randomNRGBA(test.width, test.height, 902)
			}

			params := encodeCircles(test.circles)
			want := renderPreOptimizationBaseline(test.width, test.height, initial, params)
			wantCost := fit.MSECost(want, reference)

			threadCounts := []int{1, runtime.GOMAXPROCS(0)}
			for _, threads := range threadCounts {
				t.Run(fmt.Sprintf("threads_%d", threads), func(t *testing.T) {
					var renderer *CPURenderer
					if initial == nil {
						renderer = NewCPURenderer(reference, len(test.circles))
					} else {
						renderer = NewCPURendererWithCanvas(reference, initial, len(test.circles))
					}

					renderer.SetThreads(threads)

					assertRenderBytes(t, renderer.Render(params), want)
					// A second call verifies that the reusable buffer is restored to
					// its configured background before every render.
					assertRenderBytes(t, renderer.Render(params), want)

					if got := renderer.Cost(params); got != wantCost {
						t.Fatalf("FastMSECost = %v, baseline MSECost = %v", got, wantCost)
					}

					renderer.SetCostFunc(fit.MSECost)

					if got := renderer.Cost(params); got != wantCost {
						t.Fatalf("MSECost = %v, baseline MSECost = %v", got, wantCost)
					}
				})
			}
		})
	}
}

func renderPreOptimizationBaseline(width, height int, initial *image.NRGBA, params []float64) *image.NRGBA {
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	if initial == nil {
		draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.NRGBA{R: 255, G: 255, B: 255, A: 255}}, image.Point{}, draw.Src)
	} else {
		draw.Draw(canvas, canvas.Bounds(), initial, initial.Bounds().Min, draw.Src)
	}

	circleCount := len(params) / paramsPerCircle

	pv := fit.ParamVector{Data: params, K: circleCount, Width: width, Height: height}
	for i := range circleCount {
		circle := pv.DecodeCircle(i)
		minX := int(math.Max(0, math.Floor(circle.X-circle.R)))
		maxX := int(math.Min(float64(width-1), math.Ceil(circle.X+circle.R)))
		minY := int(math.Max(0, math.Floor(circle.Y-circle.R)))
		maxY := int(math.Min(float64(height-1), math.Ceil(circle.Y+circle.R)))
		radiusSquared := circle.R * circle.R

		for y := minY; y <= maxY; y++ {
			for x := minX; x <= maxX; x++ {
				dx := float64(x) - circle.X

				dy := float64(y) - circle.Y
				if dx*dx+dy*dy <= radiusSquared {
					compositePreOptimizationPixel(canvas, x, y, circle)
				}
			}
		}
	}

	return canvas
}

func compositePreOptimizationPixel(img *image.NRGBA, x, y int, circle fit.Circle) {
	offset := img.PixOffset(x, y)
	backgroundRed := float64(img.Pix[offset]) / 255
	backgroundGreen := float64(img.Pix[offset+1]) / 255
	backgroundBlue := float64(img.Pix[offset+2]) / 255
	backgroundAlpha := float64(img.Pix[offset+3]) / 255
	foregroundAlpha := circle.Opacity

	outputAlpha := foregroundAlpha + backgroundAlpha*(1-foregroundAlpha)
	if outputAlpha == 0 {
		return
	}

	img.Pix[offset] = uint8(math.Round((circle.CR*foregroundAlpha + backgroundRed*backgroundAlpha*(1-foregroundAlpha)) / outputAlpha * 255))
	img.Pix[offset+1] = uint8(math.Round((circle.CG*foregroundAlpha + backgroundGreen*backgroundAlpha*(1-foregroundAlpha)) / outputAlpha * 255))
	img.Pix[offset+2] = uint8(math.Round((circle.CB*foregroundAlpha + backgroundBlue*backgroundAlpha*(1-foregroundAlpha)) / outputAlpha * 255))
	img.Pix[offset+3] = uint8(math.Round(outputAlpha * 255))
}

func encodeCircles(circles []fit.Circle) []float64 {
	params := make([]float64, len(circles)*paramsPerCircle)

	pv := fit.ParamVector{Data: params, K: len(circles)}
	for i, circle := range circles {
		pv.EncodeCircle(i, circle)
	}

	return params
}

func decodeCircles(params []float64, count, width, height int) []fit.Circle {
	pv := fit.ParamVector{Data: params, K: count, Width: width, Height: height}

	circles := make([]fit.Circle, count)
	for i := range circles {
		circles[i] = pv.DecodeCircle(i)
	}

	return circles
}

func assertRenderBytes(t *testing.T, got, want *image.NRGBA) {
	t.Helper()

	if len(got.Pix) != len(want.Pix) {
		t.Fatalf("rendered pixel buffer length = %d, baseline = %d", len(got.Pix), len(want.Pix))
	}

	if bytes.Equal(got.Pix, want.Pix) {
		return
	}

	for i := range want.Pix {
		if got.Pix[i] != want.Pix[i] {
			pixel := i / 4
			t.Fatalf("pixel (%d,%d) channel %d = %d, baseline = %d", pixel%want.Bounds().Dx(), pixel/want.Bounds().Dx(), i%4, got.Pix[i], want.Pix[i])
		}
	}

	t.Fatal("rendered pixel buffers differ")
}
