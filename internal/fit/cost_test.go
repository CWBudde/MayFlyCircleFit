package fit

import (
	"image"
	"image/color"
	"testing"
)

func TestMSECost(t *testing.T) {
	// Create two identical 2x2 white images
	img1 := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img2 := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	white := color.NRGBA{255, 255, 255, 255}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img1.Set(x, y, white)
			img2.Set(x, y, white)
		}
	}

	cost := MSECost(img1, img2)
	if cost != 0 {
		t.Errorf("Identical images should have cost 0, got %f", cost)
	}
}

func TestMSECostDifferent(t *testing.T) {
	// Create white and black 2x2 images
	white := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	black := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
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
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
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
