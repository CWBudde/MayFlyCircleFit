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
//
// workers bounds the goroutines it splits the output rows across, on the same
// terms as the rest of the project: non-positive means every core.
func Downsample(img *image.NRGBA, factor, workers int) *image.NRGBA {
	if factor <= 1 {
		return img
	}

	bounds := img.Bounds()
	width, height := bounds.Dx()/factor, bounds.Dy()/factor

	if width == 0 || height == 0 {
		return img
	}

	out := image.NewNRGBA(image.Rect(0, 0, width, height))

	// Averaging is the second-largest cost in a supersampled animation -- about
	// 53 ms of a 202 ms frame at 4096x4096 down to 1024x1024 -- and every output
	// row reads a disjoint band of the input and writes only itself, so it
	// parallelises exactly. The result does not depend on the split.
	parallelRows(height, workers, func(from, to int) {
		downsampleRows(out, img, factor, width, from, to)
	})

	return out
}

// downsampleRows box-filters the output rows in [from, to).
func downsampleRows(out, img *image.NRGBA, factor, width, from, to int) {
	bounds := img.Bounds()
	samples := factor * factor

	for y := from; y < to; y++ {
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
}

// mean rounds to nearest so a uniform block reproduces its own value exactly
// rather than drifting down by truncation.
func mean(total, samples int) uint8 {
	return uint8(((total + samples/2) / samples) & 0xFF)
}

// Upsample enlarges an image by an integer factor by repeating each pixel as a
// factor-by-factor block. It is the exact inverse of Downsample for anything
// that has not been drawn over since: a uniform block averages to its own value,
// so a base canvas taken up by this and back down by Downsample returns byte for
// byte, which is what lets a supersampled animation carry the canvas a fit was
// made over without resampling a pixel of it.
func Upsample(img *image.NRGBA, factor int) *image.NRGBA {
	if factor <= 1 {
		return img
	}

	bounds := img.Bounds()
	width, height := bounds.Dx()*factor, bounds.Dy()*factor
	out := image.NewNRGBA(image.Rect(0, 0, width, height))

	for y := range height {
		row := out.PixOffset(0, y)
		source := img.PixOffset(bounds.Min.X, bounds.Min.Y+y/factor)

		for x := range width {
			copy(out.Pix[row:row+4], img.Pix[source+(x/factor)*4:source+(x/factor)*4+4])
			row += 4
		}
	}

	return out
}
