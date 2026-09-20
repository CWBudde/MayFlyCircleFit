package anim_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/cwbudde/circlefit/internal/anim"
)

func filled(width, height int, shade uint8) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		img.Pix[i] = shade
	}

	return img
}

func TestDownsampleLeavesAFactorOfOneAlone(t *testing.T) {
	t.Parallel()

	img := filled(4, 4, 0x40)

	if anim.Downsample(img, 1) != img {
		t.Error("factor one copied the image instead of returning it")
	}
}

func TestDownsampleShrinksByTheFactor(t *testing.T) {
	t.Parallel()

	got := anim.Downsample(filled(32, 16, 0xFF), 4).Bounds()

	if got.Dx() != 8 || got.Dy() != 4 {
		t.Errorf("got %dx%d, want 8x4", got.Dx(), got.Dy())
	}
}

// A block of one colour has to come back as exactly that colour. Truncating the
// average instead of rounding would darken every flat region by a level.
func TestDownsampleReproducesAFlatColourExactly(t *testing.T) {
	t.Parallel()

	for _, shade := range []uint8{0, 1, 0x7F, 0x80, 0xFE, 0xFF} {
		out := anim.Downsample(filled(8, 8, shade), 4)

		if out.Pix[0] != shade {
			t.Errorf("a flat %d block averaged to %d", shade, out.Pix[0])
		}
	}
}

// The whole point: a hard edge becomes a graded one. Half the source pixels
// black and half white must land halfway, which is what turns a circle's
// staircase into a smooth boundary.
func TestDownsampleAveragesAnEdge(t *testing.T) {
	t.Parallel()

	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 255})
	img.SetNRGBA(1, 0, color.NRGBA{0, 0, 0, 255})
	img.SetNRGBA(0, 1, color.NRGBA{255, 255, 255, 255})
	img.SetNRGBA(1, 1, color.NRGBA{255, 255, 255, 255})

	out := anim.Downsample(img, 2)

	// (0 + 0 + 255 + 255 + 2) / 4 = 128
	const want = 128

	if out.Pix[0] != want || out.Pix[1] != want || out.Pix[2] != want {
		t.Errorf("got rgb(%d,%d,%d), want (%d,%d,%d)",
			out.Pix[0], out.Pix[1], out.Pix[2], want, want, want)
	}

	if out.Pix[3] != 255 {
		t.Errorf("alpha averaged to %d, want 255", out.Pix[3])
	}
}

// A factor the image is too small for would otherwise produce a zero-sized
// image, which no caller can use.
func TestDownsampleRefusesToVanish(t *testing.T) {
	t.Parallel()

	img := filled(2, 2, 0x10)

	if anim.Downsample(img, 8) != img {
		t.Error("a factor larger than the image did not return it unchanged")
	}
}
