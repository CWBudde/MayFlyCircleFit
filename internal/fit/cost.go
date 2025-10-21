package fit

import "image"

// CostFunc computes the error between current and reference images
type CostFunc func(current, reference *image.NRGBA) float64

// MSECost computes Mean Squared Error over sRGB channels
func MSECost(current, reference *image.NRGBA) float64 {
	bounds := current.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		panic("image dimensions must match")
	}

	var sum float64
	numPixels := width * height

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := current.PixOffset(x, y)

			// Extract RGB (ignore alpha for cost)
			r1, g1, b1 := current.Pix[i+0], current.Pix[i+1], current.Pix[i+2]
			r2, g2, b2 := reference.Pix[i+0], reference.Pix[i+1], reference.Pix[i+2]

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
