package anim_test

import (
	"errors"
	"math"
	"testing"

	"github.com/cwbudde/circlefit/internal/anim"
	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
)

const (
	canvasWidth  = 64
	canvasHeight = 48
)

// testCircles is a small back-to-front arrangement with three clearly different
// radii, so a frame count derived from the largest one is unambiguous.
func testCircles() []fit.Circle {
	return []fit.Circle{
		{X: 10, Y: 10, R: 8, CR: 1, CG: 0, CB: 0, Opacity: 1},
		{X: 30, Y: 20, R: 20, CR: 0, CG: 1, CB: 0, Opacity: 0.5},
		{X: 50, Y: 30, R: 4, CR: 0, CG: 0, CB: 1, Opacity: 0.75},
	}
}

func planFor(t *testing.T, style anim.Style, adjust func(*anim.Options)) *anim.Sequence {
	t.Helper()

	opts := anim.DefaultOptions()
	opts.Style = style
	opts.HalfLife = 0

	if adjust != nil {
		adjust(&opts)
	}

	sequence, err := anim.Plan(testCircles(), canvasWidth, canvasHeight, opts)
	if err != nil {
		t.Fatalf("Plan(%s): %v", style, err)
	}

	return sequence
}

// circlesIn decodes a frame's parameter slice back into circles.
func circlesIn(params []float64) []fit.Circle {
	vector := fit.ParamVector{Data: params, K: len(params) / app.ParamsPerCircle}

	circles := make([]fit.Circle, vector.K)
	for i := range circles {
		circles[i] = vector.DecodeCircle(i)
	}

	return circles
}

func TestEveryStyleOpensOnTheBareCanvas(t *testing.T) {
	t.Parallel()

	for _, style := range anim.Styles() {
		t.Run(string(style), func(t *testing.T) {
			t.Parallel()

			sequence := planFor(t, style, nil)

			if len(sequence.Frames) < 2 {
				t.Fatalf("got %d frames, want at least two", len(sequence.Frames))
			}

			first := sequence.Frames[0]
			if len(first.Commit) != 0 || len(first.Active) != 0 {
				t.Errorf("first frame draws %d committed and %d active circles, want none",
					len(first.Commit)/app.ParamsPerCircle, len(first.Active)/app.ParamsPerCircle)
			}
		})
	}
}

// Every style has to make all three circles permanent by the end, whatever it
// does on the way there. Inflate is the exception by construction: it never
// commits, because each of its frames redraws the whole arrangement.
func TestEveryGrowingStyleCommitsEveryCircleOnce(t *testing.T) {
	t.Parallel()

	for _, style := range []anim.Style{anim.StyleStatic, anim.StyleGrow, anim.StyleCascade} {
		t.Run(string(style), func(t *testing.T) {
			t.Parallel()

			sequence := planFor(t, style, nil)

			committed := make([]fit.Circle, 0, len(testCircles()))

			for _, frame := range sequence.Frames {
				committed = append(committed, circlesIn(frame.Commit)...)
			}

			want := testCircles()
			if len(committed) != len(want) {
				t.Fatalf("committed %d circles, want %d", len(committed), len(want))
			}

			for i, got := range committed {
				if got != want[i] {
					t.Errorf("commit %d = %+v, want %+v (order and full size must be preserved)", i, got, want[i])
				}
			}
		})
	}
}

func TestStaticEmitsOneFrameForEachCircle(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleStatic, nil)

	want := len(testCircles()) + 1
	if len(sequence.Frames) != want {
		t.Fatalf("got %d frames, want %d", len(sequence.Frames), want)
	}

	for i, frame := range sequence.Frames[1:] {
		if len(frame.Active) != 0 {
			t.Errorf("frame %d draws %d active circles; static tweens nothing",
				i+1, len(frame.Active)/app.ParamsPerCircle)
		}

		if len(frame.Commit) != app.ParamsPerCircle {
			t.Errorf("frame %d commits %d circles, want exactly one",
				i+1, len(frame.Commit)/app.ParamsPerCircle)
		}
	}
}

// The growth curve has to start at the minimum radius and rise strictly, or the
// constant floor that the original relies on has been dropped.
func TestGrowExpandsEachCircleFromTheMinimumRadius(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleGrow, nil)

	runs := 0
	previous := math.Inf(1)

	for _, frame := range sequence.Frames {
		if len(frame.Active) == 0 {
			continue
		}

		circle := circlesIn(frame.Active)[0]

		switch {
		case circle.R == fit.MinCircleRadius:
			runs++
		case circle.R <= previous:
			t.Fatalf("radius went from %v to %v; growth must be strictly increasing", previous, circle.R)
		}

		previous = circle.R
	}

	if runs != len(testCircles()) {
		t.Errorf("%d circles started at the minimum radius, want %d", runs, len(testCircles()))
	}
}

// Opacity tracks how far a circle has grown, so a frame drawn at half the final
// radius is drawn at half the final opacity.
func TestGrowFadesInWithTheRadius(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleGrow, nil)

	checked := 0

	for _, frame := range sequence.Frames {
		if len(frame.Active) == 0 {
			continue
		}

		circle := circlesIn(frame.Active)[0]

		final, ok := finalFor(circle)
		if !ok {
			t.Fatalf("active circle at (%v,%v) matches no arrangement circle", circle.X, circle.Y)
		}

		want := final.Opacity * (circle.R / final.R)
		if math.Abs(circle.Opacity-want) > 1e-12 {
			t.Errorf("opacity %v at radius %v, want %v", circle.Opacity, circle.R, want)
		}

		checked++
	}

	if checked == 0 {
		t.Fatal("no in-between frames to check")
	}
}

// finalFor identifies which arrangement circle an in-between frame is growing,
// by its centre, which the growth never moves.
func finalFor(drawn fit.Circle) (fit.Circle, bool) {
	for _, circle := range testCircles() {
		if circle.X == drawn.X && circle.Y == drawn.Y {
			return circle, true
		}
	}

	return fit.Circle{}, false
}

// The half-life control drops a rising share of frames, so a short half-life
// produces a shorter animation than none at all -- and the finished image is
// still the last thing shown.
func TestGrowHalfLifeShortensTheAnimation(t *testing.T) {
	t.Parallel()

	full := planFor(t, anim.StyleGrow, func(o *anim.Options) { o.HalfLife = 0 })
	dropped := planFor(t, anim.StyleGrow, func(o *anim.Options) { o.HalfLife = 1 })

	if len(dropped.Frames) >= len(full.Frames) {
		t.Errorf("half-life 1 produced %d frames and no half-life %d; it must drop some",
			len(dropped.Frames), len(full.Frames))
	}

	last := dropped.Frames[len(dropped.Frames)-1]
	if len(last.Active) != 0 {
		t.Error("the last frame still tweens; it must be the finished image")
	}
}

// The window never exceeds its limit, and a commit only ever carries the head,
// so nothing half-grown is made permanent.
func TestCascadeKeepsItsWindowAndCommitsOnlyTheHead(t *testing.T) {
	t.Parallel()

	const maxActive = 2

	sequence := planFor(t, anim.StyleCascade, func(o *anim.Options) { o.MaxActive = maxActive })

	for i, frame := range sequence.Frames {
		active := len(frame.Active) / app.ParamsPerCircle
		if active > maxActive {
			t.Errorf("frame %d draws %d circles, want at most %d", i, active, maxActive)
		}

		for _, committed := range circlesIn(frame.Commit) {
			final, ok := finalFor(committed)
			if !ok || committed.R != final.R || committed.Opacity != final.Opacity {
				t.Errorf("frame %d commits %+v, which is not an arrangement circle at full size", i, committed)
			}
		}
	}
}

func TestInflateDerivesItsFrameCountFromTheLargestRadius(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleInflate, nil)

	// Iterations := FixedFloor(0.25 * MaxRadius) over a largest radius of 20,
	// and the loop runs while FrameIndex < Iterations, so four of the five
	// derived iterations are emitted on top of the bare opening frame.
	const wantFrames = 5

	if len(sequence.Frames) != wantFrames {
		t.Fatalf("got %d frames, want %d", len(sequence.Frames), wantFrames)
	}

	last := circlesIn(sequence.Frames[len(sequence.Frames)-1].Active)

	for i, circle := range last {
		want := testCircles()[i]
		if circle.R != want.R || circle.Opacity != want.Opacity {
			t.Errorf("final frame circle %d is r=%v a=%v, want r=%v a=%v",
				i, circle.R, circle.Opacity, want.R, want.Opacity)
		}
	}
}

func TestInflateOverridesItsFrameCount(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleInflate, func(o *anim.Options) { o.Frames = 10 })

	if len(sequence.Frames) != 10 {
		t.Errorf("got %d frames, want 10", len(sequence.Frames))
	}
}

func TestInflateReverseEndsOnTheBareCanvas(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleInflate, func(o *anim.Options) { o.Reverse = true })

	last := sequence.Frames[len(sequence.Frames)-1]
	if len(last.Active) != 0 || len(last.Commit) != 0 {
		t.Error("a reversed inflate must deflate to the bare canvas")
	}

	first := circlesIn(sequence.Frames[0].Active)
	if len(first) == 0 || first[0].R != testCircles()[0].R {
		t.Error("a reversed inflate must open on the finished image")
	}
}

// The outro is appended, so it lengthens the sequence without touching what came
// before it.
func TestOutroAppendsVignetteFrames(t *testing.T) {
	t.Parallel()

	plain := planFor(t, anim.StyleStatic, nil)
	withOutro := planFor(t, anim.StyleStatic, func(o *anim.Options) { o.Outro = 3 })

	if len(withOutro.Frames) != len(plain.Frames)+3 {
		t.Fatalf("got %d frames, want %d", len(withOutro.Frames), len(plain.Frames)+3)
	}

	for _, frame := range withOutro.Frames[len(plain.Frames):] {
		if !frame.Vignette {
			t.Error("appended frame is not a vignette step")
		}
	}
}

// Scale multiplies the geometry and Margin surrounds it, which together
// reproduce the original's canvas of twice the scaled reference with the
// picture in the middle quarter.
func TestScaleAndMarginPlaceTheContent(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleStatic, func(o *anim.Options) {
		o.Scale = 2
		o.Margin = 0.5
	})

	wantWidth, wantHeight := 4*canvasWidth, 4*canvasHeight
	if sequence.Width != wantWidth || sequence.Height != wantHeight {
		t.Errorf("canvas is %dx%d, want %dx%d", sequence.Width, sequence.Height, wantWidth, wantHeight)
	}

	if sequence.OffsetX != sequence.Width/4 || sequence.OffsetY != sequence.Height/4 {
		t.Errorf("content at (%d,%d), want the middle quarter at (%d,%d)",
			sequence.OffsetX, sequence.OffsetY, sequence.Width/4, sequence.Height/4)
	}

	first := circlesIn(sequence.Frames[1].Commit)[0]
	source := testCircles()[0]

	wantX := source.X*2 + float64(sequence.OffsetX)
	if first.X != wantX || first.R != source.R*2 {
		t.Errorf("circle placed at x=%v r=%v, want x=%v r=%v", first.X, first.R, wantX, source.R*2)
	}
}

func TestPlanRejectsUnusableOptions(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		adjust func(*anim.Options)
		want   error
	}{
		"unknown style":     {func(o *anim.Options) { o.Style = "spiral" }, anim.ErrUnknownStyle},
		"zero scale":        {func(o *anim.Options) { o.Scale = 0 }, anim.ErrScale},
		"infinite scale":    {func(o *anim.Options) { o.Scale = math.Inf(1) }, anim.ErrScale},
		"negative margin":   {func(o *anim.Options) { o.Margin = -1 }, anim.ErrMargin},
		"empty window":      {func(o *anim.Options) { o.Style = anim.StyleCascade; o.MaxActive = 0 }, anim.ErrMaxActive},
		"negative outro":    {func(o *anim.Options) { o.Outro = -1 }, anim.ErrNegative},
		"reverse elsewhere": {func(o *anim.Options) { o.Style = anim.StyleGrow; o.Reverse = true }, anim.ErrReverse},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := anim.DefaultOptions()
			testCase.adjust(&opts)

			_, err := anim.Plan(testCircles(), canvasWidth, canvasHeight, opts)
			if !errors.Is(err, testCase.want) {
				t.Errorf("got %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestPlanRejectsUnusableCircles(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		circles []fit.Circle
		want    error
	}{
		"none":          {nil, anim.ErrNoCircles},
		"zero radius":   {[]fit.Circle{{X: 1, Y: 1, R: 0, Opacity: 1}}, anim.ErrCircle},
		"radius is NaN": {[]fit.Circle{{X: 1, Y: 1, R: math.NaN(), Opacity: 1}}, anim.ErrCircle},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := anim.Plan(testCase.circles, canvasWidth, canvasHeight, anim.DefaultOptions())
			if !errors.Is(err, testCase.want) {
				t.Errorf("got %v, want %v", err, testCase.want)
			}
		})
	}
}

// The ease-out curve is the substance of the port, and the endpoint assertion
// in render_test.go cannot see it: every style arrives at the same finished
// image whatever it does on the way. These are the radii the original's three
// assignments produce for a circle of radius twenty, so a changed coefficient,
// a dropped gain or a missing floor is caught here and nowhere else.
func TestGrowthCurvesMatchTheOriginal(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		style anim.Style
		want  []float64
	}{
		// SaveAnimation, coefficient 0.3333.
		"grow": {anim.StyleGrow, []float64{1.0, 9.06597, 14.981310418900001, 19.319443621908697}},
		// SaveAnimationAdvanced, coefficient 0.2.
		"cascade": {
			anim.StyleCascade,
			[]float64{1.0, 6.280000000000001, 10.926400000000001, 15.015232000000001, 18.613404160000005},
		},
	}

	// One circle, so the frames belong to it alone and the window never has a
	// second occupant to interleave.
	only := []fit.Circle{{X: 32, Y: 24, R: 20, CR: 1, CG: 1, CB: 1, Opacity: 1}}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := anim.DefaultOptions()
			opts.Style = testCase.style
			opts.HalfLife = 0

			sequence, err := anim.Plan(only, canvasWidth, canvasHeight, opts)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}

			var got []float64

			for _, frame := range sequence.Frames {
				for _, circle := range circlesIn(frame.Active) {
					got = append(got, circle.R)
				}
			}

			if len(got) != len(testCase.want) {
				t.Fatalf("got %d in-between radii %v, want %d", len(got), got, len(testCase.want))
			}

			for i, radius := range got {
				if radius != testCase.want[i] {
					t.Errorf("radius %d = %v, want %v", i, radius, testCase.want[i])
				}
			}
		})
	}
}

// The original appended its closing vignette to every style but Static, so the
// settings that reproduce it have to leave the outro off there.
func TestPascalDefaultsSkipTheOutroForStatic(t *testing.T) {
	t.Parallel()

	for _, style := range anim.Styles() {
		opts := anim.PascalDefaults(style)

		if style == anim.StyleStatic {
			if opts.Outro != 0 {
				t.Errorf("static has an outro of %d; the original's static export had none", opts.Outro)
			}

			continue
		}

		if opts.Outro == 0 {
			t.Errorf("%s has no outro; the original appended one to every style but static", style)
		}
	}
}
