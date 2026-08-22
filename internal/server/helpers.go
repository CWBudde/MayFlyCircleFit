package server

import (
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"os"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// loadReferenceImage loads and converts an image to NRGBA.
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
	draw.Draw(ref, bounds, img, bounds.Min, draw.Src)

	return ref, nil
}

// computeDiffImage creates a false-color image from mean absolute RGB error.
//
// It forwards to fit.DiffImage, which is where the pixel maths lives now that
// the CLI needs the same picture the server serves. The wrapper stays because
// three call sites read better without the package qualifier.
func computeDiffImage(ref, best *image.NRGBA, colormap fit.Colormap) *image.NRGBA {
	return fit.DiffImage(ref, best, colormap)
}
