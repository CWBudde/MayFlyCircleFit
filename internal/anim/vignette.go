package anim

import "image"

// The closing sequence, transcribed from the identical block the original
// repeats in SaveAnimation, SaveAnimationAdvanced and SaveAnimationBlow
// (MainUnit.pas:2376, 2553 and 2700). Its comment there says "blend over
// reference", but it does not blend anything in: it washes the margin with a
// little black and draws a border that brightens as the steps accumulate. The
// one procedure that really did fade to the reference, SaveAnimationStatic, has
// that code commented out.
const (
	// vignetteShade is the alpha of the black wash laid over the margin each
	// step, the original's pxShadeBlack32 = $10000000.
	vignetteShade = 0x10
	// vignetteHighlight is the alpha of the white border, pxShadeWhite32 =
	// $0FFFFFFF.
	vignetteHighlight = 0x0F
)

// applyVignetteStep darkens the four margin bands and draws the border, exactly
// once. Calling it repeatedly on the same image is what produces the fade.
//
// With no margin the bands are empty and only the border lines appear, which is
// the honest degenerate case rather than a special one: the original only ever
// ran this with a margin, because the margin exists for it.
func applyVignetteStep(img *image.NRGBA, offsetX, offsetY int) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// The original's four bands overlap the content edge by a pixel on the
	// inner side, which is what keeps a dark seam against the border.
	left, top := offsetX-1, offsetY-1
	right, bottom := width-offsetX+1, height-offsetY+1

	for _, band := range []image.Rectangle{
		image.Rect(0, 0, left, height),
		image.Rect(right, 0, width, height),
		image.Rect(left, 0, right, top),
		image.Rect(left, bottom, right, height),
	} {
		fillRect(img, band, 0, 0, 0, vignetteShade)
	}

	// Three passes over four progressively larger rectangles, the middle one
	// drawn twice, so the border reads as a soft edge rather than a line.
	border := image.Rect(offsetX, offsetY, width-offsetX, height-offsetY)
	frameRect(img, border)

	border = border.Inset(-1)
	frameRect(img, border)
	frameRect(img, border)

	border = border.Inset(-1)
	frameRect(img, border)
}

// fillRect blends a colour over every pixel of the rectangle. The canvas is
// opaque and stays opaque, so this is a straight interpolation towards the
// source and the alpha channel is left alone.
func fillRect(img *image.NRGBA, rect image.Rectangle, red, green, blue, alpha uint8) {
	rect = rect.Intersect(img.Bounds())
	if rect.Empty() || alpha == 0 {
		return
	}

	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		row := img.PixOffset(rect.Min.X, y)
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.Pix[row+0] = blend(img.Pix[row+0], red, alpha)
			img.Pix[row+1] = blend(img.Pix[row+1], green, alpha)
			img.Pix[row+2] = blend(img.Pix[row+2], blue, alpha)
			row += 4
		}
	}
}

// frameRect blends white over the one-pixel outline of the rectangle. The
// corners are covered by the horizontal edges, so the vertical ones skip them
// and no pixel is blended twice within a single call.
func frameRect(img *image.NRGBA, rect image.Rectangle) {
	if rect.Empty() {
		return
	}

	fillRect(img, image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1), 0xFF, 0xFF, 0xFF, vignetteHighlight)
	fillRect(img, image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y), 0xFF, 0xFF, 0xFF, vignetteHighlight)
	fillRect(img, image.Rect(rect.Min.X, rect.Min.Y+1, rect.Min.X+1, rect.Max.Y-1), 0xFF, 0xFF, 0xFF, vignetteHighlight)
	fillRect(img, image.Rect(rect.Max.X-1, rect.Min.Y+1, rect.Max.X, rect.Max.Y-1), 0xFF, 0xFF, 0xFF, vignetteHighlight)
}

// blend interpolates dst towards src by alpha/255, rounding to nearest so a
// repeated wash converges instead of stalling on truncation.
func blend(dst, src, alpha uint8) uint8 {
	const half = 127

	// Both terms are at most 255*255, so the quotient is a byte by
	// construction; the mask states that to the compiler and the reader.
	blended := (int(dst)*(255-int(alpha)) + int(src)*int(alpha) + half) / 255

	return uint8(blended & 0xFF)
}
