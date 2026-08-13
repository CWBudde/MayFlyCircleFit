package server

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"math"
	"os"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// loadReferenceImage loads and converts an image to NRGBA
func loadReferenceImage(path string) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Convert to NRGBA
	bounds := img.Bounds()
	if err := app.ValidateImageDimensions(bounds.Dx(), bounds.Dy()); err != nil {
		return nil, err
	}
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, img.At(x, y))
		}
	}

	return ref, nil
}

// computeDiffImage creates a false-color image from mean absolute RGB error.
func computeDiffImage(ref, best *image.NRGBA, colormap fit.Colormap) *image.NRGBA {
	bounds := ref.Bounds()
	diff := image.NewNRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			refPixel := ref.NRGBAAt(x, y)
			bestPixel := best.NRGBAAt(x, y)
			dr := math.Abs(float64(int(refPixel.R) - int(bestPixel.R)))
			dg := math.Abs(float64(int(refPixel.G) - int(bestPixel.G)))
			db := math.Abs(float64(int(refPixel.B) - int(bestPixel.B)))
			absoluteError := (dr + dg + db) / 3
			diff.Set(x, y, fit.MapErrorColor(absoluteError, 255, colormap))
		}
	}

	return diff
}
