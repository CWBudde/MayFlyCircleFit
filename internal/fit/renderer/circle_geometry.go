package renderer

import (
	"math"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

const (
	circleQ16FractionBits = 16
	circleQ16Scale        = int64(1 << circleQ16FractionBits)
	circleBatchMinSquare  = 56.25 // 7.5²: smallest possible eighth-candidate distance.
)

// fixedCircleQ16 holds signed Q16.16 circle geometry. Products are widened to
// int64, giving them Q32.32 scale. Conversion is range-checked, and span rejects
// a row whose absolute Y distance exceeds the radius before squaring it.
type fixedCircleQ16 struct {
	xQ, yQ, radiusQ int32
	centerX         int
	radiusSquared   int64
}

func newFixedCircleQ16(c fit.Circle) (fixedCircleQ16, bool) {
	xQ, ok := floatToQ16(c.X)
	if !ok {
		return fixedCircleQ16{}, false
	}
	yQ, ok := floatToQ16(c.Y)
	if !ok {
		return fixedCircleQ16{}, false
	}
	radiusQ, ok := floatToQ16(c.R)
	if !ok || radiusQ < 0 {
		return fixedCircleQ16{}, false
	}

	radius64 := int64(radiusQ)
	return fixedCircleQ16{
		xQ:            xQ,
		yQ:            yQ,
		radiusQ:       radiusQ,
		centerX:       int(c.X + 0.5),
		radiusSquared: radius64 * radius64,
	}, true
}

func floatToQ16(value float64) (int32, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	scaled := math.Round(value * float64(circleQ16Scale))
	if scaled < math.MinInt32 || scaled > math.MaxInt32 {
		return 0, false
	}
	return int32(scaled), true
}

// symmetricRowSum reports the integer sum of two sampled row coordinates that
// are equidistant from the Q16.16 center. Such a pairing exists only when the
// quantized Y coordinate lies on an integer or half-integer pixel row. For all
// other fractional centers, mirroring a span would change circle coverage.
func (g fixedCircleQ16) symmetricRowSum() (int, bool) {
	halfPixelQ := int32(circleQ16Scale / 2)
	if g.yQ%halfPixelQ != 0 {
		return 0, false
	}
	return int((int64(g.yQ) * 2) / circleQ16Scale), true
}

// span returns the half-open horizontal pixel span covered on row y. It uses
// the same center-out search and inclusive boundary rule as the float64 oracle,
// but all hot-loop distance checks are fixed-point integer operations.
func (g fixedCircleQ16) span(y, width int) (xStart, xEnd int, intersects bool) {
	dyQ := (int64(y) << circleQ16FractionBits) - int64(g.yQ)
	radiusQ := int64(g.radiusQ)
	if dyQ < -radiusQ || dyQ > radiusQ {
		return 0, 0, false
	}
	remaining := g.radiusSquared - dyQ*dyQ

	xStart = g.centerX
	// Distance grows monotonically away from the rounded center. If the eighth
	// pixel to the left is inside, all seven pixels before it are also inside.
	// This provides the useful part of SIMD batching without lanes or masks.
	minimumBatchDistanceQ := 15 * circleQ16Scale / 2
	if remaining >= minimumBatchDistanceQ*minimumBatchDistanceQ {
		for xStart >= 8 {
			dxQ := (int64(xStart-8) << circleQ16FractionBits) - int64(g.xQ)
			if dxQ*dxQ > remaining {
				break
			}
			xStart -= 8
		}
	}
	dxQ := (int64(xStart-1) << circleQ16FractionBits) - int64(g.xQ)
	distanceSquared := dxQ * dxQ
	// Moving one pixel left changes dx by -scale. Keep the square in Q32.32
	// with first and second finite differences, avoiding a multiply per pixel.
	distanceDelta := circleQ16Scale*circleQ16Scale - 2*dxQ*circleQ16Scale
	distanceSecondDelta := 2 * circleQ16Scale * circleQ16Scale
	for xStart > 0 {
		if distanceSquared > remaining {
			break
		}
		xStart--
		distanceSquared += distanceDelta
		distanceDelta += distanceSecondDelta
	}
	if xStart < 0 {
		xStart = 0
	}

	xEnd = g.centerX + 1
	if remaining >= minimumBatchDistanceQ*minimumBatchDistanceQ {
		for xEnd+7 < width {
			dxQ := (int64(xEnd+7) << circleQ16FractionBits) - int64(g.xQ)
			if dxQ*dxQ > remaining {
				break
			}
			xEnd += 8
		}
	}
	dxQ = (int64(xEnd) << circleQ16FractionBits) - int64(g.xQ)
	distanceSquared = dxQ * dxQ
	// Moving one pixel right changes dx by +scale.
	distanceDelta = circleQ16Scale*circleQ16Scale + 2*dxQ*circleQ16Scale
	for xEnd < width {
		if distanceSquared > remaining {
			break
		}
		xEnd++
		distanceSquared += distanceDelta
		distanceDelta += distanceSecondDelta
	}
	if xEnd > width {
		xEnd = width
	}
	return xStart, xEnd, xEnd > xStart
}

func circleSpanFloat64(centerX, radiusSquaredMinusDY float64, width int) (xStart, xEnd int) {
	if radiusSquaredMinusDY < circleBatchMinSquare {
		return circleSpanFloat64OnePixel(centerX, radiusSquaredMinusDY, width)
	}
	return circleSpanFloat64Batched(centerX, radiusSquaredMinusDY, width)
}

func circleSpanFloat64Batched(centerX, radiusSquaredMinusDY float64, width int) (xStart, xEnd int) {
	cx := int(centerX + 0.5)
	xStart = cx
	for xStart >= 8 {
		dx := float64(xStart-8) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xStart -= 8
	}
	for xStart > 0 {
		dx := float64(xStart-1) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xStart--
	}

	xEnd = cx + 1
	for xEnd+7 < width {
		dx := float64(xEnd+7) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xEnd += 8
	}
	for xEnd < width {
		dx := float64(xEnd) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xEnd++
	}
	if xEnd > width {
		xEnd = width
	}
	return xStart, xEnd
}

func circleSpanFloat32(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	if radiusSquaredMinusDY < circleBatchMinSquare {
		return circleSpanFloat32OnePixel(centerX, radiusSquaredMinusDY, width)
	}
	return circleSpanFloat32Batched(centerX, radiusSquaredMinusDY, width)
}

func circleSpanFloat32Batched(centerX, radiusSquaredMinusDY float32, width int) (xStart, xEnd int) {
	cx := int(centerX + 0.5)
	xStart = cx
	for xStart >= 8 {
		dx := float32(xStart-8) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xStart -= 8
	}
	for xStart > 0 {
		dx := float32(xStart-1) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xStart--
	}

	xEnd = cx + 1
	for xEnd+7 < width {
		dx := float32(xEnd+7) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xEnd += 8
	}
	for xEnd < width {
		dx := float32(xEnd) - centerX
		if dx*dx > radiusSquaredMinusDY {
			break
		}
		xEnd++
	}
	if xEnd > width {
		xEnd = width
	}
	return xStart, xEnd
}

func circleSpanFloat64OnePixel(centerX, remaining float64, width int) (xStart, xEnd int) {
	xStart = int(centerX + 0.5)
	for xStart > 0 {
		dx := float64(xStart-1) - centerX
		if dx*dx > remaining {
			break
		}
		xStart--
	}
	xEnd = int(centerX+0.5) + 1
	for xEnd < width {
		dx := float64(xEnd) - centerX
		if dx*dx > remaining {
			break
		}
		xEnd++
	}
	return xStart, min(xEnd, width)
}

func circleSpanFloat32OnePixel(centerX, remaining float32, width int) (xStart, xEnd int) {
	xStart = int(centerX + 0.5)
	for xStart > 0 {
		dx := float32(xStart-1) - centerX
		if dx*dx > remaining {
			break
		}
		xStart--
	}
	xEnd = int(centerX+0.5) + 1
	for xEnd < width {
		dx := float32(xEnd) - centerX
		if dx*dx > remaining {
			break
		}
		xEnd++
	}
	return xStart, min(xEnd, width)
}
