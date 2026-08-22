package renderer

import (
	"image"
	"image/color"
	"math"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestAuditCircleBatchDistinguishesIntroducedAndFinalVisibility(t *testing.T) {
	const width, height = 9, 9
	params := encodeAuditCircles(width, height, []fit.Circle{
		{X: 2, Y: 2, R: 2, Opacity: 1},
		// This fully covers the first circle in the final image.
		{X: 2, Y: 2, R: 2, CR: 1, Opacity: 1},
		// White over the untouched white canvas changes nothing.
		{X: 7, Y: 7, R: 1, CR: 1, CG: 1, CB: 1, Opacity: 1},
	})
	targetRenderer := NewCPURenderer(image.NewNRGBA(image.Rect(0, 0, width, height)), 3)
	targetRenderer.SetThreads(1)
	reference := cloneNRGBA(targetRenderer.Render(params))
	r := NewCPURenderer(reference, 3)
	r.SetThreads(1)

	audit, err := AuditCircleBatch(r, params)
	if err != nil {
		t.Fatalf("AuditCircleBatch() error = %v", err)
	}

	if audit.MSE != 0 {
		t.Fatalf("audit MSE = %g, want 0", audit.MSE)
	}

	if len(audit.Circles) != 3 {
		t.Fatalf("circle audit count = %d, want 3", len(audit.Circles))
	}

	first, second, third := audit.Circles[0], audit.Circles[1], audit.Circles[2]
	if first.IntroducedChangedPixels == 0 || first.FinalChangedPixels != 0 || first.MSEContribution != 0 {
		t.Errorf("covered first circle audit = %#v", first)
	}

	if second.IntroducedChangedPixels == 0 || second.FinalChangedPixels == 0 || second.MSEContribution <= 0 {
		t.Errorf("visible second circle audit = %#v", second)
	}

	if third.IntroducedChangedPixels != 0 || third.FinalChangedPixels != 0 || third.MSEContribution != 0 {
		t.Errorf("invisible third circle audit = %#v", third)
	}

	for _, circle := range audit.Circles {
		if !circle.Valid || circle.ValidationError != "" {
			t.Errorf("circle audit validity = %#v", circle)
		}
	}
}

func TestAuditCircleBatchReportsHarmfulCircle(t *testing.T) {
	const width, height = 5, 5
	reference := opaqueAuditImage(width, height, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	r := NewCPURenderer(reference, 1)
	r.SetThreads(1)

	params := encodeAuditCircles(width, height, []fit.Circle{{X: 2, Y: 2, R: 1, Opacity: 1}})

	audit, err := AuditCircleBatch(r, params)
	if err != nil {
		t.Fatalf("AuditCircleBatch() error = %v", err)
	}

	if got := audit.Circles[0].MSEContribution; got >= 0 {
		t.Fatalf("harmful circle contribution = %g, want < 0", got)
	}
}

// replayOnlyRenderer hides the in-place compositor so AuditCircleBatch takes the
// portable path that re-renders the complete vector for every circle.
type replayOnlyRenderer struct {
	Renderer
}

func TestAuditCircleBatchAccumulatedMatchesReplay(t *testing.T) {
	const width, height = 24, 20
	circles := auditParityCircles()
	params := encodeAuditCircles(width, height, circles)
	reference := opaqueAuditImage(width, height, color.NRGBA{R: 40, G: 90, B: 200, A: 255})

	for _, threads := range []int{1, 4} {
		for _, custom := range []bool{false, true} {
			t.Run(auditParityName(threads, custom), func(t *testing.T) {
				build := func() *CPURenderer {
					var r *CPURenderer

					if custom {
						canvas := opaqueAuditImage(width, height, color.NRGBA{R: 10, G: 220, B: 30, A: 255})
						r = NewCPURendererWithCanvas(reference, canvas, len(circles))
					} else {
						r = NewCPURenderer(reference, len(circles))
					}

					r.SetThreads(threads)

					return r
				}

				accumulated, err := AuditCircleBatch(build(), params)
				if err != nil {
					t.Fatalf("accumulated AuditCircleBatch() error = %v", err)
				}

				replayed, err := AuditCircleBatch(replayOnlyRenderer{Renderer: build()}, params)
				if err != nil {
					t.Fatalf("replayed AuditCircleBatch() error = %v", err)
				}

				if !reflect.DeepEqual(accumulated, replayed) {
					t.Fatalf("accumulated audit = %+v, want replayed audit %+v", accumulated, replayed)
				}
			})
		}
	}
}

// auditParityCircles mixes visible, overlapping, fully occluded, translucent, and
// clipped circles so both audit paths meet every compositing case.
func auditParityCircles() []fit.Circle {
	return []fit.Circle{
		{X: 8, Y: 8, R: 6, CR: 0.9, CG: 0.1, CB: 0.2, Opacity: 1},
		{X: 9, Y: 9, R: 3, CR: 0.1, CG: 0.8, CB: 0.4, Opacity: 0.5},
		{X: 8, Y: 8, R: 6, CR: 0.2, CG: 0.2, CB: 0.9, Opacity: 1},
		{X: 18, Y: 6, R: 4, CR: 0.5, CG: 0.5, CB: 0.5, Opacity: 0.25},
		{X: 0, Y: 19, R: 5, CR: 0.3, CG: 0.7, CB: 0.1, Opacity: 0.8},
		{X: 15, Y: 15, R: 2, CR: 0.6, CG: 0.4, CB: 0.4, Opacity: 0},
	}
}

func auditParityName(threads int, custom bool) string {
	canvas := "white"
	if custom {
		canvas = "custom"
	}

	return canvas + "-threads-" + strconv.Itoa(threads)
}

func BenchmarkAuditCircleBatch(b *testing.B) {
	const width, height = 128, 128
	for _, circleCount := range []int{64, 256} {
		reference := opaqueAuditImage(width, height, color.NRGBA{R: 60, G: 120, B: 180, A: 255})

		circles := make([]fit.Circle, circleCount)
		for i := range circles {
			circles[i] = fit.Circle{
				X:       float64((i*37)%width) + 0.5,
				Y:       float64((i*53)%height) + 0.5,
				R:       4 + float64(i%7),
				CR:      float64(i%5) / 5,
				CG:      float64(i%3) / 3,
				CB:      float64(i%7) / 7,
				Opacity: 0.4 + float64(i%4)/10,
			}
		}

		params := encodeAuditCircles(width, height, circles)

		newRenderer := func() *CPURenderer {
			r := NewCPURenderer(reference, circleCount)
			r.SetThreads(1)

			return r
		}

		b.Run("accumulated-"+strconv.Itoa(circleCount), func(b *testing.B) {
			r := newRenderer()

			b.ReportAllocs()

			for range b.N {
				if _, err := AuditCircleBatch(r, params); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("replay-"+strconv.Itoa(circleCount), func(b *testing.B) {
			r := replayOnlyRenderer{Renderer: newRenderer()}

			b.ReportAllocs()

			for range b.N {
				if _, err := AuditCircleBatch(r, params); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestAuditCircleBatchRejectsInvalidInput(t *testing.T) {
	if _, err := AuditCircleBatch(nil, nil); err == nil {
		t.Fatal("AuditCircleBatch(nil) error = nil")
	}

	r := NewCPURenderer(image.NewNRGBA(image.Rect(0, 0, 2, 2)), 1)
	if _, err := AuditCircleBatch(r, make([]float64, paramsPerCircle-1)); err == nil {
		t.Fatal("AuditCircleBatch(short params) error = nil")
	}
}

func TestSeedCirclesFromResidualRestrictsCentersToRegion(t *testing.T) {
	canvas := solidImage(8, 8, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	reference := cloneNRGBA(canvas)
	reference.SetNRGBA(1, 1, color.NRGBA{A: 255})
	reference.SetNRGBA(6, 6, color.NRGBA{A: 255})

	circles, err := SeedCirclesFromResidual(canvas, reference, 1, ResidualSeedOptions{
		Region: image.Rect(4, 4, 8, 8),
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(circles) != 1 || circles[0].X != 6 || circles[0].Y != 6 {
		t.Fatalf("regional residual seed = %+v, want center (6,6)", circles)
	}
}

func TestPruneCircleBatchIteratesAndPreservesDrawOrder(t *testing.T) {
	const width, height = 9, 9
	circles := []fit.Circle{
		{X: 2, Y: 2, R: 2, Opacity: 1},
		{X: 2, Y: 2, R: 2, CR: 1, Opacity: 1},
		{X: 7, Y: 7, R: 1, CR: 1, CG: 1, CB: 1, Opacity: 1},
	}
	params := encodeAuditCircles(width, height, circles)
	targetRenderer := NewCPURenderer(image.NewNRGBA(image.Rect(0, 0, width, height)), len(circles))
	targetRenderer.SetThreads(1)
	reference := cloneNRGBA(targetRenderer.Render(params))
	r := NewCPURenderer(reference, len(circles))
	r.SetThreads(1)

	result, err := PruneCircleBatch(r, params, CirclePruneOptions{})
	if err != nil {
		t.Fatalf("PruneCircleBatch() error = %v", err)
	}

	if got := len(result.Params) / paramsPerCircle; got != 1 {
		t.Fatalf("retained circle count = %d, want 1", got)
	}

	if len(result.Removed) != 2 {
		t.Fatalf("removal count = %d, want 2", len(result.Removed))
	}

	if got := []int{result.Removed[0].OriginalCircle, result.Removed[1].OriginalCircle}; !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("removed original circles = %v, want [1 3]", got)
	}

	want := params[paramsPerCircle : 2*paramsPerCircle]
	if !slices.Equal(result.Params, want) {
		t.Fatalf("retained params = %v, want original second circle %v", result.Params, want)
	}

	if len(result.Audit.Circles) != 1 || result.Audit.Circles[0].OriginalCircle != 2 {
		t.Fatalf("final audit = %#v, want original circle 2", result.Audit)
	}
}

func TestPruneCircleBatchRespectsContributionThresholdAndRemovalLimit(t *testing.T) {
	const width, height = 8, 4
	params := encodeAuditCircles(width, height, []fit.Circle{
		{X: 1, Y: 1, R: 1, Opacity: 1},
		{X: 6, Y: 1, R: 1, Opacity: 1},
	})
	targetRenderer := NewCPURenderer(image.NewNRGBA(image.Rect(0, 0, width, height)), 2)
	targetRenderer.SetThreads(1)
	reference := cloneNRGBA(targetRenderer.Render(params))
	r := NewCPURenderer(reference, 2)
	r.SetThreads(1)

	result, err := PruneCircleBatch(r, params, CirclePruneOptions{
		MinMSEContribution: math.MaxFloat64,
		MaxRemoved:         1,
	})
	if err != nil {
		t.Fatalf("PruneCircleBatch() error = %v", err)
	}

	if len(result.Removed) != 1 || len(result.Params) != paramsPerCircle {
		t.Fatalf("retained params = %d and removals = %d, want one each", len(result.Params)/paramsPerCircle, len(result.Removed))
	}
}

func TestSeedCirclesFromResidualTargetsSeparatedHighErrorPixels(t *testing.T) {
	const width, height = 10, 6
	canvas := opaqueAuditImage(width, height, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	reference := cloneNRGBA(canvas)
	reference.SetNRGBA(2, 1, color.NRGBA{A: 255})
	reference.SetNRGBA(8, 4, color.NRGBA{R: 32, G: 32, B: 32, A: 255})
	reference.SetNRGBA(3, 1, color.NRGBA{R: 64, G: 64, B: 64, A: 255})

	circles, err := SeedCirclesFromResidual(canvas, reference, 2, ResidualSeedOptions{
		Radius: 1, Opacity: 0.5, MinSeparation: 4,
	})
	if err != nil {
		t.Fatalf("SeedCirclesFromResidual() error = %v", err)
	}

	if len(circles) != 2 {
		t.Fatalf("seed count = %d, want 2", len(circles))
	}

	if circles[0].X != 2 || circles[0].Y != 1 || circles[1].X != 8 || circles[1].Y != 4 {
		t.Fatalf("seed centers = (%g,%g), (%g,%g), want (2,1), (8,4)", circles[0].X, circles[0].Y, circles[1].X, circles[1].Y)
	}

	for i, circle := range circles {
		if circle.R != 1 || circle.Opacity != 0.5 || circle.CR != 0 || circle.CG != 0 || circle.CB != 0 {
			t.Errorf("circle %d = %#v, want black radius-1 half-opacity seed", i, circle)
		}
	}
}

func TestSeedParamsFromResidualReturnsValidCandidate(t *testing.T) {
	const width, height = 20, 10
	canvas := opaqueAuditImage(width, height, color.NRGBA{R: 200, G: 200, B: 200, A: 255})
	reference := opaqueAuditImage(width, height, color.NRGBA{R: 50, G: 100, B: 150, A: 255})

	params, err := SeedParamsFromResidual(canvas, reference, 4, ResidualSeedOptions{})
	if err != nil {
		t.Fatalf("SeedParamsFromResidual() error = %v", err)
	}

	if got, want := len(params), 4*paramsPerCircle; got != want {
		t.Fatalf("parameter count = %d, want %d", got, want)
	}

	if !fit.NewBounds(4, width, height).ValidVector(params) {
		t.Fatalf("seed params are outside circle bounds: %v", params)
	}
}

func TestSeedCirclesFromResidualRejectsInvalidInput(t *testing.T) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	reference := image.NewNRGBA(image.Rect(0, 0, 3, 2))

	tests := []struct {
		name      string
		canvas    *image.NRGBA
		reference *image.NRGBA
		count     int
		options   ResidualSeedOptions
	}{
		{name: "nil canvas", reference: reference, count: 1},
		{name: "mismatched dimensions", canvas: canvas, reference: reference, count: 1},
		{name: "negative count", canvas: canvas, reference: canvas, count: -1},
		{name: "too many distinct centers", canvas: canvas, reference: canvas, count: 5},
		{name: "radius below minimum", canvas: canvas, reference: canvas, count: 1, options: ResidualSeedOptions{Radius: 0.5}},
		{name: "opacity above one", canvas: canvas, reference: canvas, count: 1, options: ResidualSeedOptions{Opacity: 1.1}},
		{name: "negative separation", canvas: canvas, reference: canvas, count: 1, options: ResidualSeedOptions{MinSeparation: -1}},
		{name: "non-finite radius", canvas: canvas, reference: canvas, count: 1, options: ResidualSeedOptions{Radius: math.Inf(1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := SeedCirclesFromResidual(test.canvas, test.reference, test.count, test.options); err == nil {
				t.Fatal("SeedCirclesFromResidual() error = nil")
			}
		})
	}
}

func encodeAuditCircles(width, height int, circles []fit.Circle) []float64 {
	params := make([]float64, len(circles)*paramsPerCircle)

	vector := fit.ParamVector{Data: params, K: len(circles), Width: width, Height: height}
	for i, circle := range circles {
		vector.EncodeCircle(i, circle)
	}

	return params
}

func opaqueAuditImage(width, height int, fill color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, fill)
		}
	}

	return img
}
