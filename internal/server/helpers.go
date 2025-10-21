package server

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
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
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, img.At(x, y))
		}
	}

	return ref, nil
}

// computeDiffImage creates a false-color difference image
func computeDiffImage(ref, best *image.NRGBA) *image.NRGBA {
	bounds := ref.Bounds()
	diff := image.NewNRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r1, g1, b1, _ := ref.At(x, y).RGBA()
			r2, g2, b2, _ := best.At(x, y).RGBA()

			// Compute per-channel differences (0-65535 range)
			dr := int(r1) - int(r2)
			dg := int(g1) - int(g2)
			db := int(b1) - int(b2)

			// Compute magnitude
			diffMag := math.Sqrt(float64(dr*dr + dg*dg + db*db))

			// Normalize to 0-255 (max diff is ~113k for 16-bit)
			normalized := uint8(math.Min(255, diffMag/443.0))

			// Create false-color: black = no diff, red = high diff
			diff.Set(x, y, color.NRGBA{normalized, 0, 0, 255})
		}
	}

	return diff
}
