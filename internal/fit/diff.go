package fit

import (
	"image"
	"math"
)

// DiffImage renders the residual between a reference and a candidate as a
// false-color image: mean absolute RGB error per pixel, run through a colormap.
//
// It lives here rather than in the server because it is the picture that
// explains a cost, and cost is this package's subject. The server serves it as
// diff.png and the score command writes it to a file; both want the same
// mapping, or the two would disagree about where a run is wrong.
func DiffImage(reference, candidate *image.NRGBA, colormap Colormap) *image.NRGBA {
	bounds := reference.Bounds()
	diff := image.NewNRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			referencePixel := reference.NRGBAAt(x, y)
			candidatePixel := candidate.NRGBAAt(x, y)
			red := math.Abs(float64(int(referencePixel.R) - int(candidatePixel.R)))
			green := math.Abs(float64(int(referencePixel.G) - int(candidatePixel.G)))
			blue := math.Abs(float64(int(referencePixel.B) - int(candidatePixel.B)))
			diff.Set(x, y, MapErrorColor((red+green+blue)/3, 255, colormap))
		}
	}

	return diff
}
