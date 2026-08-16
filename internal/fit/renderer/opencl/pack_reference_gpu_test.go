//go:build gpu

package opencl

import (
	"image"
	"image/color"
	"testing"
)

func TestPackReferenceNRGBAPreservesOriginStrideAndChannels(t *testing.T) {
	parent := image.NewNRGBA(image.Rect(-3, -2, 8, 7))
	bounds := image.Rect(1, 1, 6, 5)
	reference := parent.SubImage(bounds).(*image.NRGBA)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			reference.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*17 + y),
				G: uint8(x + y*19),
				B: uint8(x*11 + y*7),
				A: uint8(x*13 + y*5),
			})
		}
	}

	packed := packReferenceNRGBA(reference)
	if got, want := len(packed), bounds.Dx()*bounds.Dy()*4; got != want {
		t.Fatalf("packed byte count = %d, want %d", got, want)
	}
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			gotOffset := (y*bounds.Dx() + x) * 4
			wantOffset := reference.PixOffset(bounds.Min.X+x, bounds.Min.Y+y)
			for channel := 0; channel < 4; channel++ {
				if got, want := packed[gotOffset+channel], reference.Pix[wantOffset+channel]; got != want {
					t.Fatalf("packed pixel (%d,%d) channel %d = %d, want %d", x, y, channel, got, want)
				}
			}
		}
	}
}

func TestPackReferenceNRGBAEmpty(t *testing.T) {
	if got := packReferenceNRGBA(nil); got != nil {
		t.Fatalf("packReferenceNRGBA(nil) = %v, want nil", got)
	}
	if got := packReferenceNRGBA(image.NewNRGBA(image.Rect(0, 0, 0, 0))); got != nil {
		t.Fatalf("packReferenceNRGBA(empty) = %v, want nil", got)
	}
}
