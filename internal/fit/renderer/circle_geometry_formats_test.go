package renderer //nolint:testpackage // the harness compares against unexported production geometry

// Task 10.20: signed Q24.8 and normalized Q8.24 measured against production
// Q16.16.
//
// Everything in this file is test-only on purpose. The two alternate formats
// are implemented here, next to the production path, so the comparison can be
// re-run; none of it is reachable from production code, has a build tag, or
// exports anything. The conclusion the numbers support - Q16.16 stays - is
// written up in docs/fixed-point-geometry-formats.md. Read that before
// re-opening the question.
//
// The alternates duplicate fixedCircleQ16.span instead of sharing one
// implementation parameterized by fraction count. The duplication is
// deliberate: a variable shift amount would make BenchmarkCircleSpanFormats
// measure the parameterization rather than the format, and a like-for-like
// throughput comparison is one of the four things Task 10.20 asks for.

import (
	"fmt"
	"image"
	"math"
	"math/big"
	"math/rand"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
)

const (
	// The format names used by every table and benchmark in this file.
	formatQ16     = "q16.16"
	formatQ24     = "q24.8"
	formatQ8      = "q8.24"
	formatFloat64 = "float64"

	circleQ24FractionBits = 8
	circleQ24Scale        = int64(1 << circleQ24FractionBits)
	circleQ8FractionBits  = 24
	circleQ8Scale         = int64(1 << circleQ8FractionBits)

	// Each format stores its coordinates in an int32, so the largest magnitude
	// it can hold is 2^31 divided by its scale. These are the derived numbers
	// TestFixedPointGeometryFormatRange verifies against the code.
	circleQ16MaxMagnitude = 32768.0   // 2^31 / 2^16
	circleQ24MaxMagnitude = 8388608.0 // 2^31 / 2^8
	circleQ8MaxMagnitude  = 128.0     // 2^31 / 2^24

	// The largest square canvas whose entire legal parameter box - centers half
	// a canvas beyond each edge, radius up to max(W,H) - each format can
	// represent. Derived from the magnitudes above and re-derived by binary
	// search in TestFixedPointGeometryFormatRange.
	circleQ16MaxCanvas = 21845
	circleQ24MaxCanvas = 5592405
	circleQ8MaxCanvas  = 127

	// The normalized format anchors its center on an integer pixel, so its
	// center range is bounded by that anchor rather than by the fraction bits.
	// Anything larger falls back to the exact float64 path, as an unrepresentable
	// circle does today.
	circleQ8MaxAnchorMagnitude = 2147483648.0 // 2^31
)

// spanFormatSink keeps the benchmarked span searches from being optimized away.
var spanFormatSink int

// spanFunc is one format's answer to "which pixels of row y does this circle
// cover", in the half-open form the production row walkers consume.
type spanFunc func(y, width int) (xStart, xEnd int, intersects bool)

// geometryFormat names one candidate representation and builds its span
// function for a circle, reporting false when the circle is outside the
// format's representable range - the case where production falls back to the
// exact float64 oracle.
type geometryFormat struct {
	name string
	span func(circle fit.Circle) (spanFunc, bool)
}

// geometryFormats lists the three fixed-point candidates plus the production
// float64 fallback, which is included as context: it is the path an
// unrepresentable circle already takes, and it is not exact either.
func geometryFormats() []geometryFormat {
	return []geometryFormat{
		{
			name: formatQ16,
			span: func(circle fit.Circle) (spanFunc, bool) {
				geometry, ok := newFixedCircleQ16(circle)
				return geometry.span, ok
			},
		},
		{
			name: formatQ24,
			span: func(circle fit.Circle) (spanFunc, bool) {
				geometry, ok := newFixedCircleQ24(circle)
				return geometry.span, ok
			},
		},
		{
			name: formatQ8,
			span: func(circle fit.Circle) (spanFunc, bool) {
				geometry, ok := newFixedCircleQ8(circle)
				return geometry.span, ok
			},
		},
		{
			name: formatFloat64,
			span: func(circle fit.Circle) (spanFunc, bool) {
				return func(y, width int) (int, int, bool) {
					return float64RowSpan(circle, y, width)
				}, true
			},
		},
	}
}

// fixedCircleQ24 holds signed Q24.8 circle geometry. It is the same structure
// as fixedCircleQ16 with eight fraction bits instead of sixteen: products widen
// to int64 at Q48.16, and the row reject happens before the square, so a
// squared value never exceeds (2^31)^2.
type fixedCircleQ24 struct {
	xQ, yQ, radiusQ int32
	centerX         int
	radiusSquared   int64
}

func newFixedCircleQ24(circle fit.Circle) (fixedCircleQ24, bool) {
	xQ, ok := floatToQ24(circle.X) //nolint:varnamelen // mirrors fixedCircleQ16's field naming
	if !ok {
		return fixedCircleQ24{}, false
	}

	yQ, ok := floatToQ24(circle.Y) //nolint:varnamelen // mirrors fixedCircleQ16's field naming
	if !ok {
		return fixedCircleQ24{}, false
	}

	radiusQ, ok := floatToQ24(circle.R)
	if !ok || radiusQ < 0 {
		return fixedCircleQ24{}, false
	}

	radius64 := int64(radiusQ)

	return fixedCircleQ24{
		xQ:            xQ,
		yQ:            yQ,
		radiusQ:       radiusQ,
		centerX:       int(circle.X + 0.5),
		radiusSquared: radius64 * radius64,
	}, true
}

func floatToQ24(value float64) (int32, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}

	scaled := math.Round(value * float64(circleQ24Scale))
	if scaled < math.MinInt32 || scaled > math.MaxInt32 {
		return 0, false
	}

	return int32(scaled), true
}

// span is fixedCircleQ16.span with Q24.8 constants. Keeping the search, the
// eight-pixel batching, and the finite differences identical is what makes the
// comparison a comparison of number formats and nothing else.
func (g fixedCircleQ24) span(y, width int) (int, int, bool) {
	dyQ := (int64(y) << circleQ24FractionBits) - int64(g.yQ)

	radiusQ := int64(g.radiusQ)
	if dyQ < -radiusQ || dyQ > radiusQ {
		return 0, 0, false
	}

	remaining := g.radiusSquared - dyQ*dyQ

	xStart := g.centerX

	minimumBatchDistanceQ := 15 * circleQ24Scale / 2
	if remaining >= minimumBatchDistanceQ*minimumBatchDistanceQ {
		for xStart >= 8 {
			dxQ := (int64(xStart-8) << circleQ24FractionBits) - int64(g.xQ)
			if dxQ*dxQ > remaining {
				break
			}

			xStart -= 8
		}
	}

	dxQ := (int64(xStart-1) << circleQ24FractionBits) - int64(g.xQ)
	distanceSquared := dxQ * dxQ
	distanceDelta := circleQ24Scale*circleQ24Scale - 2*dxQ*circleQ24Scale
	distanceSecondDelta := 2 * circleQ24Scale * circleQ24Scale

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

	xEnd := g.centerX + 1
	if remaining >= minimumBatchDistanceQ*minimumBatchDistanceQ {
		for xEnd+7 < width {
			dxQ := (int64(xEnd+7) << circleQ24FractionBits) - int64(g.xQ)
			if dxQ*dxQ > remaining {
				break
			}

			xEnd += 8
		}
	}

	dxQ = (int64(xEnd) << circleQ24FractionBits) - int64(g.xQ)
	distanceSquared = dxQ * dxQ
	distanceDelta = circleQ24Scale*circleQ24Scale + 2*dxQ*circleQ24Scale

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

// fixedCircleQ8 holds normalized Q8.24 circle geometry.
//
// Q8.24 has 24 fraction bits and only about +/-128 of integer range, which no
// canvas coordinate fits, so the format is only usable at all if the stored
// numbers are made small. This is that normalization: the center is stored as
// its *offset* from an integer pixel anchor - centerX for the horizontal axis,
// which the search already rounds to, and floor(Y) for the vertical one - and
// every distance is formed relative to the same anchor. Absolute coordinates
// then never enter the fixed-point domain, so the center range is bounded by
// the anchor's int32 rather than by the fraction bits.
//
// What stays bounded by the fraction bits is the radius, because dx and dy grow
// to it: the format cannot hold a circle of radius 128 or more at any center.
// That is the cost the report weighs, and it is not recoverable by a different
// anchor choice.
type fixedCircleQ8 struct {
	fracXQ, fracYQ int32
	radiusQ        int32
	centerX        int
	anchorY        int
	radiusSquared  int64
}

func newFixedCircleQ8(circle fit.Circle) (fixedCircleQ8, bool) {
	if math.IsNaN(circle.X) || math.IsInf(circle.X, 0) ||
		math.IsNaN(circle.Y) || math.IsInf(circle.Y, 0) {
		return fixedCircleQ8{}, false
	}

	// The anchors are formed in float64 and must fit an int32 before anything
	// is converted; the fractional remainders are exact float64 subtractions.
	if math.Abs(circle.X) >= circleQ8MaxAnchorMagnitude ||
		math.Abs(circle.Y) >= circleQ8MaxAnchorMagnitude {
		return fixedCircleQ8{}, false
	}

	centerX := int(circle.X + 0.5)
	anchorY := int(math.Floor(circle.Y))

	fracXQ, ok := floatToQ8(circle.X - float64(centerX))
	if !ok {
		return fixedCircleQ8{}, false
	}

	fracYQ, ok := floatToQ8(circle.Y - float64(anchorY))
	if !ok {
		return fixedCircleQ8{}, false
	}

	radiusQ, ok := floatToQ8(circle.R)
	if !ok || radiusQ < 0 {
		return fixedCircleQ8{}, false
	}

	radius64 := int64(radiusQ)

	return fixedCircleQ8{
		fracXQ:        fracXQ,
		fracYQ:        fracYQ,
		radiusQ:       radiusQ,
		centerX:       centerX,
		anchorY:       anchorY,
		radiusSquared: radius64 * radius64,
	}, true
}

func floatToQ8(value float64) (int32, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}

	scaled := math.Round(value * float64(circleQ8Scale))
	if scaled < math.MinInt32 || scaled > math.MaxInt32 {
		return 0, false
	}

	return int32(scaled), true
}

// span is fixedCircleQ16.span with Q8.24 constants and anchored distances. The
// finite-difference loops are unchanged, because the anchor cancels out of a
// difference; the anchor subtraction survives only in the batch probes and in
// the two loop entry points, which is the whole per-span cost of normalizing.
//
//nolint:nonamedreturns // mirrors fixedCircleQ16.span's signature exactly
func (g fixedCircleQ8) span(y, width int) (xStart, xEnd int, intersects bool) {
	dyQ := (int64(y-g.anchorY) << circleQ8FractionBits) - int64(g.fracYQ)

	radiusQ := int64(g.radiusQ)
	if dyQ < -radiusQ || dyQ > radiusQ {
		return 0, 0, false
	}

	remaining := g.radiusSquared - dyQ*dyQ

	xStart = g.centerX

	minimumBatchDistanceQ := 15 * circleQ8Scale / 2
	if remaining >= minimumBatchDistanceQ*minimumBatchDistanceQ {
		for xStart >= 8 {
			dxQ := (int64(xStart-8-g.centerX) << circleQ8FractionBits) - int64(g.fracXQ)
			if dxQ*dxQ > remaining {
				break
			}

			xStart -= 8
		}
	}

	dxQ := (int64(xStart-1-g.centerX) << circleQ8FractionBits) - int64(g.fracXQ)
	distanceSquared := dxQ * dxQ
	distanceDelta := circleQ8Scale*circleQ8Scale - 2*dxQ*circleQ8Scale
	distanceSecondDelta := 2 * circleQ8Scale * circleQ8Scale

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
			dxQ := (int64(xEnd+7-g.centerX) << circleQ8FractionBits) - int64(g.fracXQ)
			if dxQ*dxQ > remaining {
				break
			}

			xEnd += 8
		}
	}

	dxQ = (int64(xEnd-g.centerX) << circleQ8FractionBits) - int64(g.fracXQ)
	distanceSquared = dxQ * dxQ
	distanceDelta = circleQ8Scale*circleQ8Scale + 2*dxQ*circleQ8Scale

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

// float64RowSpan is the production float64 row exactly as
// renderCircleScanlineRowsTracked runs it, including the rounded radiusSquared
// and dy2 products that keep that path architecture-independent.
//
//nolint:nonamedreturns // mirrors the production span signature
func float64RowSpan(circle fit.Circle, y, width int) (xStart, xEnd int, intersects bool) {
	radiusSquared := float64(circle.R * circle.R)

	dy := float64(y) - circle.Y
	dy2 := float64(dy * dy)

	if dy2 > radiusSquared {
		return 0, 0, false
	}

	xStart, xEnd = circleSpanFloat64(circle.X, radiusSquared-dy2, width)
	if xStart < 0 {
		xStart = 0
	}

	return xStart, xEnd, xEnd > xStart
}

// exactSpanOracle answers the same question as every span function above, with
// exact rational arithmetic on the circle's float64 parameters.
//
// A float64 span search is not a valid oracle here: it is itself an
// approximation, and on top of that Go may contract a*b+c into one fused
// multiply-add, so a float comparison could be measuring contraction rather
// than format precision. Every accept/reject below is decided by a big.Rat
// comparison. float64 is used only to seed the search window, and the window is
// wide enough that a wrong seed changes nothing but the number of comparisons.
type exactSpanOracle struct {
	centerX       *big.Rat
	centerY       *big.Rat
	radiusSquared *big.Rat
	centerXFloat  float64
	roundedCenter int
}

func newExactSpanOracle(circle fit.Circle) exactSpanOracle {
	radius := new(big.Rat).SetFloat64(circle.R)

	return exactSpanOracle{
		centerX:       new(big.Rat).SetFloat64(circle.X),
		centerY:       new(big.Rat).SetFloat64(circle.Y),
		radiusSquared: new(big.Rat).Mul(radius, radius),
		centerXFloat:  circle.X,
		roundedCenter: int(circle.X + 0.5),
	}
}

// span reproduces the center-out search the production span functions perform -
// the same starting pixel, the same inclusive boundary rule, the same clamps -
// with infinitely precise arithmetic.
//
//nolint:nonamedreturns // mirrors fixedCircleQ16.span's signature exactly
func (o exactSpanOracle) span(y, width int) (xStart, xEnd int, intersects bool) {
	dy := new(big.Rat).SetInt64(int64(y))
	dy.Sub(dy, o.centerY)
	dy.Mul(dy, dy)

	remaining := new(big.Rat).Sub(o.radiusSquared, dy)
	if remaining.Sign() < 0 {
		return 0, 0, false
	}

	left, right, covered := o.insideInterval(remaining)

	xStart = o.roundedCenter
	if covered && xStart > 0 && xStart-1 >= left && xStart-1 <= right {
		xStart = max(left, 0)
	}

	xEnd = o.roundedCenter + 1
	if covered && xEnd < width && xEnd >= left && xEnd <= right {
		xEnd = min(right+1, width)
	}

	if xStart < 0 {
		xStart = 0
	}

	if xEnd > width {
		xEnd = width
	}

	return xStart, xEnd, xEnd > xStart
}

// insideInterval returns the closed integer interval of pixel columns whose
// exact squared distance to the center is within remaining. The set is
// contiguous, so the two edges determine the whole search.
func (o exactSpanOracle) insideInterval(remaining *big.Rat) (int, int, bool) {
	inside := func(x int) bool {
		dx := new(big.Rat).SetInt64(int64(x))
		dx.Sub(dx, o.centerX)
		dx.Mul(dx, dx)

		return dx.Cmp(remaining) <= 0
	}

	// The seeds are within one pixel of the true edges, so a window of four
	// starts strictly outside the covered interval on both sides.
	const window = 4

	approximate, _ := remaining.Float64()
	reach := math.Sqrt(approximate)
	leftSeed := int(math.Ceil(o.centerXFloat - reach))
	rightSeed := int(math.Floor(o.centerXFloat + reach))

	left := leftSeed - window
	for !inside(left) {
		left++

		if left > leftSeed+window {
			return 0, 0, false
		}
	}

	right := rightSeed + window
	for !inside(right) {
		right--

		if right < rightSeed-window {
			return 0, 0, false
		}
	}

	return left, right, true
}

// spanDisagreement counts how a format's spans differ from the exact oracle
// over a row range.
//
// The two failure modes are kept apart on purpose. A row both agree intersects
// can have an edge displaced by a pixel or two; a row whose *intersects* answer
// flips gains or loses a whole span, which is a much larger output change than
// its pixel count suggests and would swamp the edge statistic if it were folded
// into it.
type spanDisagreement struct {
	rows      int
	changed   int
	flips     int
	worstEdge int
}

func compareSpans(circle fit.Circle, format geometryFormat, width, height int) (spanDisagreement, bool) {
	span, ok := format.span(circle)
	if !ok {
		return spanDisagreement{}, false
	}

	oracle := newExactSpanOracle(circle)

	var result spanDisagreement

	for y := range height {
		wantStart, wantEnd, wantIntersects := oracle.span(y, width)
		gotStart, gotEnd, gotIntersects := span(y, width)

		if !wantIntersects && !gotIntersects {
			continue
		}

		result.rows++

		if wantIntersects != gotIntersects {
			result.changed++
			result.flips++

			continue
		}

		if gotStart == wantStart && gotEnd == wantEnd {
			continue
		}

		result.changed++
		result.worstEdge = max(result.worstEdge, abs(gotStart-wantStart))
		result.worstEdge = max(result.worstEdge, abs(gotEnd-wantEnd))
	}

	return result, true
}

func abs(value int) int {
	if value < 0 {
		return -value
	}

	return value
}

func TestFixedPointGeometryFormatRange(t *testing.T) {
	t.Parallel()

	accepts := map[string]func(fit.Circle) bool{
		formatQ16: func(circle fit.Circle) bool { _, ok := newFixedCircleQ16(circle); return ok },
		formatQ24: func(circle fit.Circle) bool { _, ok := newFixedCircleQ24(circle); return ok },
		formatQ8:  func(circle fit.Circle) bool { _, ok := newFixedCircleQ8(circle); return ok },
	}

	tests := []struct {
		name   string
		format string
		circle fit.Circle
		want   bool
	}{
		{
			name:   "q16.16/largest_representable_center",
			format: formatQ16,
			circle: fit.Circle{X: circleQ16MaxMagnitude - 1.0/65536, Y: 1, R: 1},
			want:   true,
		},
		{name: "q16.16/center_at_magnitude", format: formatQ16, circle: fit.Circle{X: circleQ16MaxMagnitude, R: 1}},
		{name: "q16.16/radius_at_magnitude", format: formatQ16, circle: fit.Circle{R: circleQ16MaxMagnitude}},
		{
			name:   "q24.8/largest_representable_center",
			format: formatQ24,
			circle: fit.Circle{X: circleQ24MaxMagnitude - 1.0/256, Y: 1, R: 1},
			want:   true,
		},
		{name: "q24.8/center_at_magnitude", format: formatQ24, circle: fit.Circle{X: circleQ24MaxMagnitude, R: 1}},
		{
			name:   "q24.8/center_beyond_q16",
			format: formatQ24,
			circle: fit.Circle{X: 40000, Y: 40000, R: 1000},
			want:   true,
		},
		{
			name:   "q8.24/largest_representable_radius",
			format: formatQ8,
			circle: fit.Circle{X: 10, Y: 10, R: circleQ8MaxMagnitude - 1.0/16777216},
			want:   true,
		},
		{name: "q8.24/radius_at_magnitude", format: formatQ8, circle: fit.Circle{X: 10, Y: 10, R: circleQ8MaxMagnitude}},
		{
			name:   "q8.24/far_center_small_radius",
			format: formatQ8,
			circle: fit.Circle{X: 1e6, Y: -1e6, R: 100},
			want:   true,
		},
		{name: "q8.24/center_beyond_anchor", format: formatQ8, circle: fit.Circle{X: circleQ8MaxAnchorMagnitude, R: 1}},
		{
			name:   "q8.24/legal_radius_on_512_canvas",
			format: formatQ8,
			circle: fit.Circle{X: 256, Y: 256, R: 512},
		},
		{
			name:   "q16.16/legal_radius_on_512_canvas",
			format: formatQ16,
			circle: fit.Circle{X: 256, Y: 256, R: 512},
			want:   true,
		},
		{name: "q24.8/legal_radius_on_512_canvas", format: formatQ24, circle: fit.Circle{X: 256, Y: 256, R: 512}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := accepts[test.format](test.circle); got != test.want {
				t.Fatalf("%s accepts %+v = %v, want %v", test.format, test.circle, got, test.want)
			}
		})
	}
}

func TestFixedPointGeometryFormatCanvasLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		accepts func(fit.Circle) bool
		want    int64
	}{
		{
			name:    formatQ16,
			accepts: func(circle fit.Circle) bool { _, ok := newFixedCircleQ16(circle); return ok },
			want:    circleQ16MaxCanvas,
		},
		{
			name:    formatQ24,
			accepts: func(circle fit.Circle) bool { _, ok := newFixedCircleQ24(circle); return ok },
			want:    circleQ24MaxCanvas,
		},
		{
			name:    formatQ8,
			accepts: func(circle fit.Circle) bool { _, ok := newFixedCircleQ8(circle); return ok },
			want:    circleQ8MaxCanvas,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := largestFullyRepresentableCanvas(test.accepts)
			if got != test.want {
				t.Fatalf("largest fully representable square canvas = %d, want %d", got, test.want)
			}

			t.Logf("%s covers every bounds-legal circle up to a %dx%d canvas", test.name, got, got)
		})
	}
}

// largestFullyRepresentableCanvas binary-searches the largest square canvas
// whose complete legal parameter box the format can hold. fit.NewBounds lets a
// center sit half a canvas beyond an edge and a radius reach max(W,H), so the
// extreme circles are the ones tested here.
func largestFullyRepresentableCanvas(accepts func(fit.Circle) bool) int64 {
	representable := func(dimension int64) bool {
		size := float64(dimension)
		extremes := []fit.Circle{
			{X: 1.5*size - 1, Y: 1.5*size - 1, R: size},
			{X: -0.5 * size, Y: -0.5 * size, R: size},
			{X: 0.5 * size, Y: 0.5 * size, R: size},
		}

		for _, circle := range extremes {
			if !accepts(circle) {
				return false
			}
		}

		return true
	}

	low, high := int64(1), int64(1)<<32
	if !representable(low) {
		return 0
	}

	for low < high {
		middle := (low + high + 1) / 2
		if representable(middle) {
			low = middle
		} else {
			high = middle - 1
		}
	}

	return low
}

func TestFixedPointGeometryFormatAdversarialBoundaries(t *testing.T) {
	t.Parallel()

	const (
		width  = 129
		height = 97
	)

	// The three epsilon cases are the precision ladder made explicit. Each
	// radius is a hair below a value whose span edge lands exactly on a pixel,
	// with one rung per format's own resolution: 2^-12 is below Q24.8's,
	// 2^-20 is below Q16.16's as well, and 2^-28 is below Q8.24's too. No
	// format is exact below its own resolution, so the third rung is the one
	// that shows Q8.24 failing the same way the other two already do.
	tests := []struct {
		name        string
		circle      fit.Circle
		wantQ16     int
		wantQ24     int
		wantQ8      int
		wantFloat64 int
	}{
		{name: "integer_center_integer_radius", circle: fit.Circle{X: 64, Y: 48, R: 20}},
		{name: "fractional_center", circle: fit.Circle{X: 64.37, Y: 48.61, R: 20.5}},
		{name: "half_pixel_center", circle: fit.Circle{X: 64.5, Y: 48.5, R: 20}},
		{name: "radius_one", circle: fit.Circle{X: 64.25, Y: 48.75, R: 1}},
		{name: "maximum_radius", circle: fit.Circle{X: 64, Y: 48, R: 129}},
		{name: "negative_center", circle: fit.Circle{X: -12.25, Y: -8.5, R: 30}},
		{name: "clipped_off_canvas", circle: fit.Circle{X: 140.75, Y: 100.5, R: 45}},
		{name: "tangent_row_fractional_center", circle: fit.Circle{X: 64.25, Y: 48, R: 20}},
		{name: "tangent_half_rows", circle: fit.Circle{X: 64.5, Y: 48.5, R: 20.5}},
		{
			name:    "radius_one_ulp_below_q16_boundary",
			circle:  fit.Circle{X: 64, Y: 48, R: 20 - 1.0/1048576},
			wantQ16: 7,
			wantQ24: 7,
		},
		{
			name:    "radius_one_ulp_below_q24_boundary",
			circle:  fit.Circle{X: 64, Y: 48, R: 20 - 1.0/4096},
			wantQ24: 7,
		},
		{
			name:    "radius_one_ulp_below_q8_boundary",
			circle:  fit.Circle{X: 64, Y: 48, R: 20 - 1.0/268435456},
			wantQ16: 7,
			wantQ24: 7,
			wantQ8:  7,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			want := map[string]int{
				formatQ16:     test.wantQ16,
				formatQ24:     test.wantQ24,
				formatQ8:      test.wantQ8,
				formatFloat64: test.wantFloat64,
			}

			for _, format := range geometryFormats() {
				result, ok := compareSpans(test.circle, format, width, height)
				if !ok {
					t.Logf("%s: outside the representable range, production falls back to float64", format.name)

					continue
				}

				if result.changed != want[format.name] {
					t.Fatalf("%s changed %d of %d intersecting rows, want %d",
						format.name, result.changed, result.rows, want[format.name])
				}

				t.Logf("%s: %d of %d rows changed (%d intersect flips), worst edge displacement %d px",
					format.name, result.changed, result.rows, result.flips, result.worstEdge)
			}
		})
	}
}

// randomFormatCorpus builds a deterministic corpus of circles inside the
// optimizer's own bounds for a canvas of the given size.
func randomFormatCorpus(seed int64, count, width, height int, maxRadius float64) []fit.Circle {
	source := rand.New(rand.NewSource(seed)) //nolint:gosec // a reproducible fixture, not a security context
	circles := make([]fit.Circle, 0, count)

	for range count {
		circles = append(circles, fit.Circle{
			X:       source.Float64()*2*float64(width) - 0.5*float64(width),
			Y:       source.Float64()*2*float64(height) - 0.5*float64(height),
			R:       fit.MinCircleRadius + source.Float64()*(maxRadius-fit.MinCircleRadius),
			CR:      source.Float64(),
			CG:      source.Float64(),
			CB:      source.Float64(),
			Opacity: fit.MinCircleOpacity + source.Float64()*(1-fit.MinCircleOpacity),
		})
	}

	return circles
}

func TestFixedPointGeometryFormatRandomizedRows(t *testing.T) {
	if testing.Short() {
		t.Skip("exact rational oracle sweep is slow; run without -short")
	}

	t.Parallel()

	const (
		width     = 513
		height    = 389
		circles   = 400
		maxRadius = 120
		seed      = 20260828
	)

	corpus := randomFormatCorpus(seed, circles, width, height, maxRadius)

	for _, format := range geometryFormats() {
		total := spanDisagreement{}
		fallbacks := 0

		for _, circle := range corpus {
			result, ok := compareSpans(circle, format, width, height)
			if !ok {
				fallbacks++

				continue
			}

			total.rows += result.rows
			total.changed += result.changed
			total.flips += result.flips
			total.worstEdge = max(total.worstEdge, result.worstEdge)
		}

		rate := 100 * float64(total.changed) / float64(max(total.rows, 1))
		t.Logf("%s: %d of %d rows changed (%.5f%%), %d intersect flips, worst edge %d px, %d/%d circles fell back",
			format.name, total.changed, total.rows, rate, total.flips, total.worstEdge, fallbacks, len(corpus))

		if format.name == formatQ8 && total.changed != 0 {
			t.Fatalf("q8.24 changed %d rows; it is expected to agree with the exact oracle everywhere", total.changed)
		}
	}
}

// renderCorpusWithFormat mirrors renderCircleScanlineRowsTracked's non-symmetric
// row walk - the same early rejects, the same vertical clamp, the same hoisted
// blend, the same compositor - with the span search swapped for one format's.
// TestFixedPointGeometryFormatFullRender pins the mirror by requiring the
// Q16.16 arm to be byte-identical to CPURenderer.Render.
func renderCorpusWithFormat(width, height int, circles []fit.Circle, format geometryFormat) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		img.Pix[i] = 255
	}

	for _, circle := range circles {
		if circle.Opacity == 0 {
			continue
		}

		minYf := circle.Y - circle.R
		maxYf := circle.Y + circle.R

		if maxYf < 0 || minYf >= float64(height) {
			continue
		}

		minY := max(int(minYf), 0)
		maxY := min(int(maxYf+1), height)

		if minY >= maxY {
			continue
		}

		blend := newSpanBlend(circle.CR, circle.CG, circle.CB, circle.Opacity)

		span, ok := format.span(circle)
		if !ok {
			span = func(row, canvasWidth int) (int, int, bool) {
				return float64RowSpan(circle, row, canvasWidth)
			}
		}

		for y := minY; y < maxY; y++ {
			xStart, xEnd, intersects := span(y, width)
			if !intersects {
				continue
			}

			compositeOpaqueSpan(
				&blend, img.Pix, y*img.Stride+xStart*4, xEnd-xStart,
				circle.CR, circle.CG, circle.CB, circle.Opacity,
			)
		}
	}

	return img
}

// renderCorpusProduction renders the same corpus through the real CPU renderer.
func renderCorpusProduction(width, height int, circles []fit.Circle) *image.NRGBA {
	reference := image.NewNRGBA(image.Rect(0, 0, width, height))
	renderer := NewCPURenderer(reference, len(circles))

	params := make([]float64, 0, len(circles)*7)
	for _, circle := range circles {
		params = append(params, circle.X, circle.Y, circle.R, circle.CR, circle.CG, circle.CB, circle.Opacity)
	}

	rendered := renderer.Render(params)

	copied := image.NewNRGBA(rendered.Bounds())
	copy(copied.Pix, rendered.Pix)

	return copied
}

type renderDifference struct {
	bytes      int
	pixels     int
	worstDelta int
	firstByte  int
}

func compareRenders(want, got *image.NRGBA) renderDifference {
	result := renderDifference{firstByte: -1}

	for i := 0; i < len(want.Pix); i += 4 {
		changed := false

		for channel := range 4 {
			delta := abs(int(want.Pix[i+channel]) - int(got.Pix[i+channel]))
			if delta == 0 {
				continue
			}

			changed = true
			result.bytes++
			result.worstDelta = max(result.worstDelta, delta)

			if result.firstByte < 0 {
				result.firstByte = i + channel
			}
		}

		if changed {
			result.pixels++
		}
	}

	return result
}

func TestFixedPointGeometryFormatFullRender(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		width     int
		height    int
		circles   int
		maxRadius float64
		long      bool
	}{
		{name: "192x144/96_circles/r<=60", width: 192, height: 144, circles: 96, maxRadius: 60},
		{name: "512x384/200_circles/r<=200", width: 512, height: 384, circles: 200, maxRadius: 200, long: true},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if test.long && testing.Short() {
				t.Skip("large render corpus; run without -short")
			}

			const seed = 20260828

			corpus := randomFormatCorpus(seed, test.circles, test.width, test.height, test.maxRadius)
			production := renderCorpusProduction(test.width, test.height, corpus)
			totalBytes := len(production.Pix)

			for _, format := range geometryFormats() {
				rendered := renderCorpusWithFormat(test.width, test.height, corpus, format)
				difference := compareRenders(production, rendered)

				if format.name == formatQ16 && difference.bytes != 0 {
					t.Fatalf("the harness does not reproduce production output: %d bytes differ", difference.bytes)
				}

				t.Logf("%s: %d of %d bytes differ (%.4f%%), %d pixels, worst channel delta %d, first at byte %d",
					format.name, difference.bytes, totalBytes,
					100*float64(difference.bytes)/float64(totalBytes),
					difference.pixels, difference.worstDelta, difference.firstByte)

				if difference.worstDelta > 255 {
					t.Fatalf("%s produced an impossible channel delta %d", format.name, difference.worstDelta)
				}
			}
		})
	}
}

// TestFixedPointGeometryFormatQ8Fallback records how much of a real canvas'
// legal radius range normalized Q8.24 cannot represent.
func TestFixedPointGeometryFormatQ8Fallback(t *testing.T) {
	t.Parallel()

	sizes := []int{128, 256, 512, 1024}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dx%d", size, size), func(t *testing.T) {
			t.Parallel()

			corpus := randomFormatCorpus(20260828, 2000, size, size, float64(size))
			fallbacks := 0

			for _, circle := range corpus {
				if _, ok := newFixedCircleQ8(circle); !ok {
					fallbacks++
				}
			}

			share := 100 * float64(fallbacks) / float64(len(corpus))
			t.Logf("q8.24 cannot represent %d of %d bounds-legal circles (%.1f%%)", fallbacks, len(corpus), share)

			if size >= 256 && fallbacks == 0 {
				t.Fatalf("expected q8.24 to reject radii of 128 and above on a %dx%d canvas", size, size)
			}
		})
	}
}

// BenchmarkCircleSpanFormats walks every row of one circle per iteration, so a
// result is the cost of a complete circle's span search in that format. Radii
// match the ladder used for the AVX2 span comparison recorded in
// docs/rejected-optimizations.md.
//
// Each arm calls its span method **directly**. Measuring these through the
// geometryFormat registry's closure would measure the indirection instead: the
// same mistake that once made the exact span compositor look 5-9x slower than
// scalar (docs/exact-span-compositors.md).
func BenchmarkCircleSpanFormats(b *testing.B) {
	radii := []float64{5.25, 25.25, 100.25, 256.25}
	for _, radius := range radii {
		width := int(4*radius) + 1
		circle := fit.Circle{X: float64(width)/2 + 0.37, Y: float64(width)/2 + 0.61, R: radius}
		minY := max(int(circle.Y-radius), 0)
		maxY := min(int(circle.Y+radius+1), width)
		rows := maxY - minY

		b.Run(fmt.Sprintf("radius=%.2f/q16.16", radius), func(b *testing.B) {
			geometry, ok := newFixedCircleQ16(circle)
			if !ok {
				b.Skipf("q16.16 cannot represent radius %.2f", radius)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				for y := minY; y < maxY; y++ {
					xStart, xEnd, intersects := geometry.span(y, width)
					if intersects {
						spanFormatSink += xEnd - xStart
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*rows), "ns/row")
		})

		b.Run(fmt.Sprintf("radius=%.2f/q24.8", radius), func(b *testing.B) {
			geometry, ok := newFixedCircleQ24(circle)
			if !ok {
				b.Skipf("q24.8 cannot represent radius %.2f", radius)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				for y := minY; y < maxY; y++ {
					xStart, xEnd, intersects := geometry.span(y, width)
					if intersects {
						spanFormatSink += xEnd - xStart
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*rows), "ns/row")
		})

		b.Run(fmt.Sprintf("radius=%.2f/q8.24", radius), func(b *testing.B) {
			geometry, ok := newFixedCircleQ8(circle)
			if !ok {
				b.Skipf("q8.24 cannot represent radius %.2f", radius)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				for y := minY; y < maxY; y++ {
					xStart, xEnd, intersects := geometry.span(y, width)
					if intersects {
						spanFormatSink += xEnd - xStart
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*rows), "ns/row")
		})

		b.Run(fmt.Sprintf("radius=%.2f/float64", radius), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				for y := minY; y < maxY; y++ {
					xStart, xEnd, intersects := float64RowSpan(circle, y, width)
					if intersects {
						spanFormatSink += xEnd - xStart
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*rows), "ns/row")
		})
	}
}
