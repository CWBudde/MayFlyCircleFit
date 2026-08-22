package fit

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestMSECost(t *testing.T) {
	// Create two identical 2x2 white images
	img1 := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img2 := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	white := color.NRGBA{255, 255, 255, 255}

	for y := range 2 {
		for x := range 2 {
			img1.Set(x, y, white)
			img2.Set(x, y, white)
		}
	}

	cost := MSECost(img1, img2)
	if cost != 0 {
		t.Errorf("Identical images should have cost 0, got %f", cost)
	}
}

func TestCostsSupportIndependentOriginsAndStrides(t *testing.T) {
	currentParent := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	referenceParent := image.NewNRGBA(image.Rect(10, 20, 21, 28))
	current := currentParent.SubImage(image.Rect(2, 1, 6, 4)).(*image.NRGBA)
	reference := referenceParent.SubImage(image.Rect(13, 22, 17, 25)).(*image.NRGBA)
	value := color.NRGBA{R: 17, G: 42, B: 99, A: 255}

	for y := range 3 {
		for x := range 4 {
			current.SetNRGBA(current.Rect.Min.X+x, current.Rect.Min.Y+y, value)
			reference.SetNRGBA(reference.Rect.Min.X+x, reference.Rect.Min.Y+y, value)
		}
	}

	if got := MSECost(current, reference); got != 0 {
		t.Fatalf("MSECost = %v, want 0", got)
	}

	if got := FastSSD(current, reference); got != 0 {
		t.Fatalf("FastSSD = %v, want 0", got)
	}

	if got, ok := ExactSSD(current, reference); !ok || got != 0 {
		t.Fatalf("ExactSSD = (%d, %v), want (0, true)", got, ok)
	}

	if got := FastSAD(current, reference); got != 0 {
		t.Fatalf("FastSAD = %v, want 0", got)
	}
}

func TestExactSSD(t *testing.T) {
	white := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	black := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	for y := range 2 {
		for x := range 2 {
			white.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: uint8(x + y)})
			black.SetNRGBA(x, y, color.NRGBA{A: 255})
		}
	}

	const want = uint64(2 * 2 * 3 * 255 * 255)
	if got, ok := ExactSSD(white, black); !ok || got != want {
		t.Fatalf("ExactSSD = (%d, %v), want (%d, true)", got, ok, want)
	}

	if got := FastSSD(white, black) * float64(2*2*3); got != float64(want) {
		t.Fatalf("FastSSD raw total = %v, want %d", got, want)
	}

	empty := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	mismatch := image.NewNRGBA(image.Rect(0, 0, 1, 1))

	if _, ok := ExactSSD(empty, empty); ok {
		t.Fatal("ExactSSD accepted empty images")
	}

	if _, ok := ExactSSD(white, mismatch); ok {
		t.Fatal("ExactSSD accepted mismatched images")
	}
}

func TestCostsRejectEmptyAndMismatchedImages(t *testing.T) {
	empty := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	nonempty := image.NewNRGBA(image.Rect(0, 0, 1, 1))

	for name, cost := range map[string]CostFunc{"mse": MSECost, "fast": FastMSECost} {
		if got := cost(empty, empty); !math.IsInf(got, 1) {
			t.Errorf("%s empty cost = %v, want +Inf", name, got)
		}

		if got := cost(empty, nonempty); !math.IsInf(got, 1) {
			t.Errorf("%s mismatch cost = %v, want +Inf", name, got)
		}
	}
}

func TestMSECostDifferent(t *testing.T) {
	// Create white and black 2x2 images
	white := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	black := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	for y := range 2 {
		for x := range 2 {
			white.Set(x, y, color.NRGBA{255, 255, 255, 255})
			black.Set(x, y, color.NRGBA{0, 0, 0, 255})
		}
	}

	cost := MSECost(white, black)
	if cost <= 0 {
		t.Errorf("Different images should have cost > 0, got %f", cost)
	}

	// MSE of white vs black over 3 channels (RGB)
	// Each pixel diff: 255^2 * 3 channels = 195075
	// 4 pixels total: 195075 * 4 / 4 pixels / 3 channels = 65025
	expected := 65025.0
	if cost != expected {
		t.Errorf("Expected cost %f, got %f", expected, cost)
	}
}

func TestMSECostSinglePixel(t *testing.T) {
	// Two identical images except one red pixel
	img1 := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img2 := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	white := color.NRGBA{255, 255, 255, 255}

	for y := range 2 {
		for x := range 2 {
			img1.Set(x, y, white)
			img2.Set(x, y, white)
		}
	}

	// Change one pixel to red in img2
	img2.Set(0, 0, color.NRGBA{255, 0, 0, 255})

	cost := MSECost(img1, img2)

	// One pixel differs: white (255,255,255) vs red (255,0,0)
	// Diff: R=0, G=255^2, B=255^2
	// MSE = (0 + 65025 + 65025) / (4 pixels * 3 channels) = 10837.5
	expected := 10837.5
	if cost != expected {
		t.Errorf("Expected cost %f, got %f", expected, cost)
	}
}
