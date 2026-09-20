package anim

import "image"

// Downsample box-filters an image down by an integer factor.
//
// It exists so frames can be supersampled: the renderer draws no partial
// pixels -- a circle's edge is a hard boundary in the span compositor, which is
// what the byte-exact parity contract requires of it -- so the only way to get
// a smooth edge is to render larger and average down. At factor four, each
// output pixel is the mean of sixteen, which resolves an edge to a sixteenth of
// a pixel.
//
// The average is taken over the non-premultiplied channels, which is correct
// here because every canvas this package renders is opaque: the background is
// filled before the first circle and no circle can make it transparent. On an
// image with varying alpha this would darken the edges, so it is deliberately
// not a general-purpose resampler.
func Downsample(img *image.NRGBA, factor int) *image.NRGBA {
	if factor <= 1 {
		return img
	}

	bounds := img.Bounds()
	width, height := bounds.Dx()/factor, bounds.Dy()/factor

	if width == 0 || height == 0 {
		return img
	}

	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	samples := factor * factor

	for y := range height {
		for x := range width {
			var red, green, blue, alpha int

			for dy := range factor {
				row := img.PixOffset(bounds.Min.X+x*factor, bounds.Min.Y+y*factor+dy)
				for range factor {
					red += int(img.Pix[row+0])
					green += int(img.Pix[row+1])
					blue += int(img.Pix[row+2])
					alpha += int(img.Pix[row+3])
					row += 4
				}
			}

			target := out.PixOffset(x, y)
			out.Pix[target+0] = mean(red, samples)
			out.Pix[target+1] = mean(green, samples)
			out.Pix[target+2] = mean(blue, samples)
			out.Pix[target+3] = mean(alpha, samples)
		}
	}

	return out
}

// mean rounds to nearest so a uniform block reproduces its own value exactly
// rather than drifting down by truncation.
func mean(total, samples int) uint8 {
	return uint8(((total + samples/2) / samples) & 0xFF)
}
