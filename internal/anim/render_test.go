package anim_test

import (
	"bytes"
	"errors"
	"image"
	"testing"

	"github.com/cwbudde/circlefit/internal/anim"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
)

func whiteCanvas(width, height int) *image.NRGBA {
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range canvas.Pix {
		canvas.Pix[i] = 0xFF
	}

	return canvas
}

// collect renders a sequence and keeps a copy of every frame. Render hands out
// a buffer it reuses, so the copy is the point.
func collect(t *testing.T, sequence *anim.Sequence, canvas *image.NRGBA) []*image.NRGBA {
	t.Helper()

	var frames []*image.NRGBA

	err := anim.Render(sequence, canvas, func(_ int, img *image.NRGBA) error {
		copied := image.NewNRGBA(img.Bounds())
		copy(copied.Pix, img.Pix)
		frames = append(frames, copied)

		return nil
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	return frames
}

// This is the assertion the whole port rests on. Whatever a style does on the
// way, the animation has to arrive at exactly the image the same circles
// produce in one pass -- the one a run writes as best.png. A scaling mistake, a
// dropped commit or a circle composited out of order all show up here.
func TestEveryStyleEndsOnTheFinishedImage(t *testing.T) {
	t.Parallel()

	for _, style := range anim.Styles() {
		t.Run(string(style), func(t *testing.T) {
			t.Parallel()

			sequence := planFor(t, style, nil)
			canvas := whiteCanvas(sequence.Width, sequence.Height)
			frames := collect(t, sequence, canvas)

			reference := renderer.NewCPURendererWithCanvas(canvas, canvas, len(testCircles()))

			params := make([]float64, 0, len(testCircles())*7)
			for _, circle := range testCircles() {
				params = append(params,
					circle.X, circle.Y, circle.R, circle.CR, circle.CG, circle.CB, circle.Opacity)
			}

			want := reference.Render(params)
			got := frames[len(frames)-1]

			if !bytes.Equal(got.Pix, want.Pix) {
				t.Errorf("the last frame is not the finished arrangement (%d pixels differ)",
					differing(got.Pix, want.Pix))
			}
		})
	}
}

func differing(got, want []byte) int {
	count := 0

	for i := range got {
		if got[i] != want[i] {
			count++
		}
	}

	return count
}

func TestFirstFrameIsTheUntouchedCanvas(t *testing.T) {
	t.Parallel()

	for _, style := range anim.Styles() {
		t.Run(string(style), func(t *testing.T) {
			t.Parallel()

			sequence := planFor(t, style, nil)
			canvas := whiteCanvas(sequence.Width, sequence.Height)
			frames := collect(t, sequence, canvas)

			if !bytes.Equal(frames[0].Pix, canvas.Pix) {
				t.Error("the opening frame has already drawn something")
			}
		})
	}
}

// A growing style paints progressively, so consecutive frames have to differ.
// A plan that emitted the same image twice would still satisfy the endpoint
// assertion above.
func TestGrowingStylesAdvanceEachFrame(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleGrow, nil)
	frames := collect(t, sequence, whiteCanvas(sequence.Width, sequence.Height))

	identical := 0

	for i := 1; i < len(frames); i++ {
		if bytes.Equal(frames[i].Pix, frames[i-1].Pix) {
			identical++
		}
	}

	// The original skipped frames that rendered identically, which its
	// fixed-point radii produced often; in float64 they are rare but a circle
	// smaller than a pixel can still repeat.
	if identical > len(frames)/4 {
		t.Errorf("%d of %d frames repeat the previous one", identical, len(frames))
	}
}

// The vignette accumulates on its own image: the margin gets darker every step
// while the committed background is left alone.
func TestVignetteDarkensTheMarginProgressively(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleStatic, func(o *anim.Options) {
		o.Margin = 0.5
		o.Outro = 3
	})

	frames := collect(t, sequence, whiteCanvas(sequence.Width, sequence.Height))
	outro := frames[len(frames)-3:]

	corner := func(img *image.NRGBA) uint8 { return img.Pix[img.PixOffset(1, 1)] }

	for i := 1; i < len(outro); i++ {
		if corner(outro[i]) >= corner(outro[i-1]) {
			t.Errorf("vignette step %d left the margin at %d, no darker than %d",
				i, corner(outro[i]), corner(outro[i-1]))
		}
	}

	if corner(outro[len(outro)-1]) == 0xFF {
		t.Error("the margin was never darkened")
	}
}

func TestRenderRejectsAMismatchedCanvas(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleStatic, nil)

	err := anim.Render(sequence, whiteCanvas(sequence.Width+1, sequence.Height), func(int, *image.NRGBA) error {
		return nil
	})
	if !errors.Is(err, anim.ErrCanvasSize) {
		t.Errorf("got %v, want %v", err, anim.ErrCanvasSize)
	}
}

// A sink that fails stops the render rather than being called for every
// remaining frame; a full disk halfway through a long sequence is the case.
func TestRenderStopsWhenTheSinkFails(t *testing.T) {
	t.Parallel()

	sequence := planFor(t, anim.StyleStatic, nil)
	failure := errors.New("sink failed")
	calls := 0

	err := anim.Render(sequence, whiteCanvas(sequence.Width, sequence.Height),
		func(index int, _ *image.NRGBA) error {
			calls++

			if index == 1 {
				return failure
			}

			return nil
		})

	if !errors.Is(err, failure) {
		t.Fatalf("got %v, want %v", err, failure)
	}

	if calls != 2 {
		t.Errorf("sink called %d times, want 2", calls)
	}
}
