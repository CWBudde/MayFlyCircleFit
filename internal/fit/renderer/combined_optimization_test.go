package renderer

import (
	"bytes"
	"fmt"
	"image"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestFixedCircleQ16SymmetricRowSum(t *testing.T) {
	tests := []struct {
		name string
		y    float64
		want int
		ok   bool
	}{
		{name: "integer", y: 12, want: 24, ok: true},
		{name: "half_integer", y: 12.5, want: 25, ok: true},
		{name: "negative_half_integer", y: -2.5, want: -5, ok: true},
		{name: "q16_rounds_to_half", y: 12.500001, want: 25, ok: true},
		{name: "quarter", y: 12.25, ok: false},
		{name: "arbitrary_fraction", y: 12.12345, ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			geometry, ok := newFixedCircleQ16(fit.Circle{Y: test.y, R: 1})
			if !ok {
				t.Fatal("test circle unexpectedly outside Q16.16 range")
			}
			got, gotOK := geometry.symmetricRowSum()
			if got != test.want || gotOK != test.ok {
				t.Fatalf("symmetricRowSum() = (%d, %v), want (%d, %v)", got, gotOK, test.want, test.ok)
			}
		})
	}
}

func TestCPURendererSymmetricRowsMatchUnpairedRendering(t *testing.T) {
	const (
		width  = 97
		height = 73
	)
	reference := randomNRGBA(width, height, 1015)
	circles := []fit.Circle{
		{X: 30.25, Y: 24, R: 18.5, CR: 0.9, CG: 0.1, CB: 0.4, Opacity: 0.65},
		{X: 55.75, Y: 31.5, R: 23.25, CR: 0.2, CG: 0.8, CB: 0.3, Opacity: 0.4},
		{X: 4.5, Y: 0.5, R: 12.75, CR: 0.1, CG: 0.3, CB: 0.95, Opacity: 0.8},
		{X: 42.5, Y: -2.5, R: 11.75, CR: 0.4, CG: 0.2, CB: 0.85, Opacity: 0.7},
		// An ineligible fractional center verifies the ordinary loop remains
		// byte-identical when mixed with paired circles.
		{X: 88.125, Y: 65.25, R: 14.5, CR: 0.7, CG: 0.6, CB: 0.2, Opacity: 0.55},
	}
	params := encodeCircles(circles)

	translucent := randomNRGBA(width, height, 1510)
	for _, canvas := range []struct {
		name  string
		image *image.NRGBA
	}{
		{name: "opaque"},
		{name: "translucent", image: translucent},
	} {
		for _, threads := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/threads=%d", canvas.name, threads), func(t *testing.T) {
				paired := newCombinedTestRenderer(reference, canvas.image, len(circles))
				unpaired := newCombinedTestRenderer(reference, canvas.image, len(circles))
				paired.SetThreads(threads)
				unpaired.SetThreads(threads)
				paired.enableRowSymmetry = true

				got := append([]byte(nil), paired.Render(params).Pix...)
				want := unpaired.Render(params).Pix
				if !bytes.Equal(got, want) {
					t.Fatal("paired rendering differs from the previous row-by-row path")
				}

				paired.incrementalCostMode = incrementalCostForce
				unpaired.incrementalCostMode = incrementalCostDisabled
				if gotCost, wantCost := paired.Cost(params), unpaired.Cost(params); gotCost != wantCost {
					t.Fatalf("paired incremental cost = %.17g, full unpaired cost = %.17g", gotCost, wantCost)
				}
			})
		}
	}
}

func TestCPURendererSymmetricRowsMatchEveryShard(t *testing.T) {
	const (
		width  = 41
		height = 31
	)
	circles := []fit.Circle{
		{X: 20.25, Y: -2.5, R: 12.75, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.65},
		{X: 20.25, Y: 0, R: 12.75, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.65},
		{X: 20.25, Y: 0.5, R: 12.75, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.65},
		{X: 20.25, Y: 15, R: 12.75, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.65},
		{X: 20.25, Y: 15.5, R: 12.75, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.65},
		{X: 20.25, Y: 30, R: 12.75, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.65},
		{X: 20.25, Y: 32.5, R: 12.75, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.65},
	}

	paired := &CPURenderer{width: width, height: height, opaqueCanvas: true, enableRowSymmetry: true}
	unpaired := &CPURenderer{width: width, height: height, opaqueCanvas: true}
	base := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range base.Pix {
		base.Pix[i] = 255
	}

	for _, circle := range circles {
		for rowStart := 0; rowStart <= height; rowStart++ {
			for rowEnd := rowStart; rowEnd <= height; rowEnd++ {
				got := &image.NRGBA{Pix: append([]byte(nil), base.Pix...), Stride: base.Stride, Rect: base.Rect}
				want := &image.NRGBA{Pix: append([]byte(nil), base.Pix...), Stride: base.Stride, Rect: base.Rect}
				paired.renderCircleScanlineRows(got, circle, rowStart, rowEnd)
				unpaired.renderCircleScanlineRows(want, circle, rowStart, rowEnd)
				if !bytes.Equal(got.Pix, want.Pix) {
					t.Fatalf("Y=%g shard [%d,%d) differs from ordinary rendering", circle.Y, rowStart, rowEnd)
				}
			}
		}
	}
}

func TestCPURendererCombinedSettingsPropagateToSessions(t *testing.T) {
	reference := randomNRGBA(32, 24, 1015)
	base := NewCPURenderer(reference, 2)
	base.enableRowSymmetry = true

	session, cleanup, err := base.newSession(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !session.(*CPURenderer).enableRowSymmetry {
		t.Fatal("ordinary session did not preserve row-symmetry setting")
	}

	staged, stagedCleanup, err := base.newSessionWithCanvas(base.initialCanvas(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer stagedCleanup()
	if !staged.(*CPURenderer).enableRowSymmetry {
		t.Fatal("staged session did not preserve row-symmetry setting")
	}
}

func BenchmarkCPURendererCombinedOptimizations(b *testing.B) {
	const (
		width   = 512
		height  = 512
		circles = 100
	)
	reference := randomNRGBA(width, height, 1015)

	for _, fixture := range []struct {
		name      string
		symmetric bool
		radius    float64
		threads   int
	}{
		{name: "fractional_centers"},
		{name: "half_pixel_centers", symmetric: true},
		{name: "half_pixel_centers_R5", symmetric: true, radius: 5},
		{name: "half_pixel_centers_R25", symmetric: true, radius: 25},
		{name: "half_pixel_centers_threads4", symmetric: true, threads: 4},
	} {
		params := combinedBenchmarkParams(circles, width, height, fixture.symmetric, fixture.radius)
		for _, variant := range []struct {
			name           string
			forceFloat     bool
			opaqueSpan     bool
			enableSymmetry bool
		}{
			{name: "scanline_float64_pixel_loop", forceFloat: true},
			{name: "span_float64", forceFloat: true, opaqueSpan: true},
			{name: "production_span_q16.16", opaqueSpan: true},
			{name: "experimental_with_symmetry", opaqueSpan: true, enableSymmetry: true},
		} {
			b.Run(fixture.name+"/"+variant.name, func(b *testing.B) {
				renderer := NewCPURenderer(reference, circles)
				threads := fixture.threads
				if threads == 0 {
					threads = 1
				}
				renderer.SetThreads(threads)
				renderer.forceFloatGeometry = variant.forceFloat
				renderer.opaqueCanvas = variant.opaqueSpan
				renderer.enableRowSymmetry = variant.enableSymmetry
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					renderer.Render(params)
				}
				b.ReportMetric(float64(renderer.Threads()), "threads")
				if fixture.symmetric {
					b.ReportMetric(100, "%symmetric")
				} else {
					b.ReportMetric(0, "%symmetric")
				}
			})
		}
	}
}

func newCombinedTestRenderer(reference, canvas *image.NRGBA, circles int) *CPURenderer {
	if canvas == nil {
		return NewCPURenderer(reference, circles)
	}
	return NewCPURendererWithCanvas(reference, canvas, circles)
}

func combinedBenchmarkParams(circles, width, height int, symmetric bool, radius float64) []float64 {
	params := deterministicParams(circles, width, height, 1015)
	for circle := range circles {
		offset := circle * paramsPerCircle
		if symmetric {
			params[offset+1] = float64((circle*37)%(height*2)) / 2
		} else {
			params[offset+1] = float64((circle*37)%height) + 0.12345
		}
		if radius != 0 {
			params[offset+2] = radius
		}
	}
	return params
}
