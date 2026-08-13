package fit

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestPSNR(t *testing.T) {
	tests := []struct {
		name string
		mse  float64
		want float64
	}{
		{name: "maximum error", mse: 65025, want: 0},
		{name: "unit MSE", mse: 1, want: 48.1308036086791},
		{name: "quarter scale", mse: 255 * 255 / 4.0, want: 6.020599913279624},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := PSNR(test.mse); math.Abs(got-test.want) > 1e-12 {
				t.Fatalf("PSNR(%v) = %.15f, want %.15f", test.mse, got, test.want)
			}
		})
	}
	if got := PSNR(0); !math.IsInf(got, 1) {
		t.Fatalf("PSNR(0) = %v, want +Inf", got)
	}
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		if got := PSNR(invalid); !math.IsNaN(got) {
			t.Errorf("PSNR(%v) = %v, want NaN", invalid, got)
		}
	}
}

func TestSSIMIdenticalAndAlphaIgnored(t *testing.T) {
	left := patternedNRGBA(image.Rect(3, 5, 20, 18))
	right := image.NewNRGBA(image.Rect(30, 40, 47, 53))
	for y := range left.Bounds().Dy() {
		for x := range left.Bounds().Dx() {
			pixel := left.NRGBAAt(left.Bounds().Min.X+x, left.Bounds().Min.Y+y)
			pixel.A = uint8((x*31 + y*17) % 256)
			right.SetNRGBA(right.Bounds().Min.X+x, right.Bounds().Min.Y+y, pixel)
		}
	}
	if got, err := SSIM(left, right); err != nil || math.Abs(got-1) > 1e-12 {
		t.Fatalf("SSIM identical RGB = (%v, %v), want (1, nil)", got, err)
	}
}

func TestSSIMStructuralDifferenceIsSymmetricAndBounded(t *testing.T) {
	left := patternedNRGBA(image.Rect(0, 0, 24, 19))
	right := image.NewNRGBA(left.Bounds())
	for y := range left.Bounds().Dy() {
		for x := range left.Bounds().Dx() {
			pixel := left.NRGBAAt(x, y)
			right.SetNRGBA(x, y, color.NRGBA{R: 255 - pixel.B, G: pixel.R / 2, B: 255 - pixel.G, A: 255})
		}
	}
	forward, err := SSIM(left, right)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := SSIM(right, left)
	if err != nil {
		t.Fatal(err)
	}
	if forward < -1 || forward > 1 || forward >= 0.95 {
		t.Fatalf("SSIM structural difference = %v, want a bounded value below 0.95", forward)
	}
	if math.Abs(forward-reverse) > 1e-12 {
		t.Fatalf("SSIM is not symmetric: forward=%v reverse=%v", forward, reverse)
	}
}

func TestSSIMConstantImagesMatchesAnalyticalLuminanceTerm(t *testing.T) {
	black := image.NewNRGBA(image.Rect(0, 0, 13, 9))
	gray := image.NewNRGBA(black.Bounds())
	for y := range gray.Bounds().Dy() {
		for x := range gray.Bounds().Dx() {
			gray.SetNRGBA(x, y, color.NRGBA{R: 100, G: 100, B: 100, A: 255})
		}
	}
	got, err := SSIM(black, gray)
	if err != nil {
		t.Fatal(err)
	}
	c1 := (0.01 * 255) * (0.01 * 255)
	want := c1 / (100*100 + c1)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("constant-image SSIM = %.15f, want %.15f", got, want)
	}
}

func TestSSIMSupportsSmallImagesAndRejectsInvalidInput(t *testing.T) {
	one := image.NewNRGBA(image.Rect(4, 7, 5, 8))
	two := image.NewNRGBA(image.Rect(10, 12, 11, 13))
	one.SetNRGBA(4, 7, color.NRGBA{R: 20, G: 40, B: 60, A: 10})
	two.SetNRGBA(10, 12, color.NRGBA{R: 20, G: 40, B: 60, A: 250})
	if got, err := SSIM(one, two); err != nil || math.Abs(got-1) > 1e-12 {
		t.Fatalf("one-pixel SSIM = (%v, %v), want (1, nil)", got, err)
	}

	empty := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	mismatch := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	for name, images := range map[string][2]*image.NRGBA{
		"nil":      {nil, two},
		"empty":    {empty, empty},
		"mismatch": {one, mismatch},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SSIM(images[0], images[1]); err == nil {
				t.Fatal("SSIM accepted invalid input")
			}
		})
	}
}

func patternedNRGBA(bounds image.Rectangle) *image.NRGBA {
	img := image.NewNRGBA(bounds)
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			img.SetNRGBA(bounds.Min.X+x, bounds.Min.Y+y, color.NRGBA{
				R: uint8((x*29 + y*7) % 256),
				G: uint8((x*11 + y*23) % 256),
				B: uint8((x*3 + y*41) % 256),
				A: uint8((x + y) % 256),
			})
		}
	}
	return img
}
