package renderer

import (
	"image"
	"image/color"
	"math"
	"slices"
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

func TestAuditCircleBatchRejectsInvalidInput(t *testing.T) {
	if _, err := AuditCircleBatch(nil, nil); err == nil {
		t.Fatal("AuditCircleBatch(nil) error = nil")
	}
	r := NewCPURenderer(image.NewNRGBA(image.Rect(0, 0, 2, 2)), 1)
	if _, err := AuditCircleBatch(r, make([]float64, paramsPerCircle-1)); err == nil {
		t.Fatal("AuditCircleBatch(short params) error = nil")
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
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, fill)
		}
	}
	return img
}
