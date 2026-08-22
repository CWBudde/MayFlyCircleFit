package fit

import (
	"image"
	"math"
)

// CostFunc computes the error between current and reference images.
type CostFunc func(current, reference *image.NRGBA) float64

// MSECost computes Mean Squared Error over sRGB channels.
func MSECost(current, reference *image.NRGBA) float64 {
	bounds := current.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		return math.Inf(1)
	}

	var sum float64

	numPixels := width * height
	if numPixels == 0 {
		return math.Inf(1)
	}

	for y := range height {
		for x := range width {
			currentOffset := y*current.Stride + x*4
			referenceOffset := y*reference.Stride + x*4

			// Extract RGB (ignore alpha for cost)
			r1, g1, b1 := current.Pix[currentOffset], current.Pix[currentOffset+1], current.Pix[currentOffset+2]
			r2, g2, b2 := reference.Pix[referenceOffset], reference.Pix[referenceOffset+1], reference.Pix[referenceOffset+2]

			// Squared differences
			dr := float64(r1) - float64(r2)
			dg := float64(g1) - float64(g2)
			db := float64(b1) - float64(b2)

			sum += dr*dr + dg*dg + db*db
		}
	}

	// Mean over pixels and channels
	return sum / float64(numPixels*3)
}
