package renderer

import (
	"image"
	"math"
	"sync"

	"github.com/cwbudde/circlefit/internal/fit"
)

// polishDirtyMaxFraction stays conservatively below the measured
// affected-pixel crossover at which a complete render becomes cheaper than
// rebuilding a fragmented region. Keep this tied to
// BenchmarkPolishDirtyCrossover when changing the compositor or SSD kernels.
const (
	polishDirtyMaxFraction          = 0.05
	polishDirtyPreflightMaxFraction = 0.50
)

// polishDirtyEnabled gates the dirty-region evaluator. Production always leaves
// it set; the end-to-end fixture harness clears it to drive the same polishing
// sweep through the full-canvas evaluator and compare the two.
//
//nolint:gochecknoglobals // a test-only switch; a parameter would put it in the production API.
var polishDirtyEnabled = true

// polishDirtySessionHook, when set, receives every dirty session a polishing
// sweep creates. It exists so the end-to-end fixture harness can read the
// affected-pixel telemetry a real sweep accumulates, which is otherwise sealed
// inside PolishCircleBatchContext. Production leaves it nil.
//
//nolint:gochecknoglobals // a test-only hook; nil in production, see polishDirtyEnabled.
var polishDirtySessionHook func(*polishDirtySession)

// polishDirtyFractionEdges are the exclusive upper bounds of the affected-pixel
// histogram a session accumulates. They are spaced by decade rather than
// linearly because a real polishing sweep spends nearly every evaluation far
// below the 5% fallback gate, where linear buckets would show one column.
//
//nolint:gochecknoglobals // an immutable table; an array constant is not expressible in Go.
var polishDirtyFractionEdges = [...]float64{
	0.0001, 0.0005, 0.001, 0.005, 0.01, 0.02, 0.05, 0.10, 0.25, 0.50, 1.01,
}

// polishDirtySession scores a polishing candidate by rebuilding only pixels
// covered by an active circle before or after the candidate mutation. Pixels
// outside that union are bit-identical to the incumbent, so their SSD is the
// constant part of baselineSSD.
//
// The embedded renderer still owns the reusable canvas and exact compositing
// implementation. A session is leased to one evaluation at a time by the
// polishing pool, so its scratch span sets and canvas need no locks.
type polishDirtySession struct {
	*CPURenderer

	baseline             *image.NRGBA
	baselineSSD          uint64
	incumbent            []float64
	activeCircles        []int
	dirty                dirtySpanSet
	previousDirty        dirtySpanSet
	fullRestore          bool
	maxFraction          float64
	preflightMaxFraction float64
	evaluations          int
	fallbacks            int

	// Affected-pixel telemetry. fractionCounts covers only evaluations that
	// built a scanline mask; a preflight rejection never learns its true
	// fraction and is counted separately.
	fractionCounts     [len(polishDirtyFractionEdges)]int
	fractionSum        float64
	fractionMax        float64
	preflightFallbacks int
}

func newPolishDirtySession(
	session Renderer,
	baseline *image.NRGBA,
	baselineSSD uint64,
	incumbent []float64,
	activeCircles []int,
) Renderer {
	cpu, ok := session.(*CPURenderer)
	if !polishDirtyEnabled || !ok || !cpu.fastCostSelected ||
		baseline == nil || len(incumbent) != cpu.Dim() {
		return session
	}

	for _, circle := range activeCircles {
		if circle < 0 || circle >= cpu.k {
			return session
		}
	}

	dirty := &polishDirtySession{
		CPURenderer:          cpu,
		baseline:             baseline,
		baselineSSD:          baselineSSD,
		incumbent:            append([]float64(nil), incumbent...),
		activeCircles:        append([]int(nil), activeCircles...),
		fullRestore:          true,
		maxFraction:          polishDirtyMaxFraction,
		preflightMaxFraction: polishDirtyPreflightMaxFraction,
	}
	dirty.dirty.reset(cpu.height, max(1, 2*len(activeCircles)))
	dirty.previousDirty.reset(cpu.height, max(1, 2*len(activeCircles)))

	if polishDirtySessionHook != nil {
		polishDirtySessionHook(dirty)
	}

	return dirty
}

func (s *polishDirtySession) Cost(params []float64) float64 {
	s.evaluations++
	if len(params) != s.Dim() || s.width == 0 || s.height == 0 {
		return math.Inf(1)
	}

	s.dirty.reset(s.height, max(1, 2*len(s.activeCircles)))
	incumbentVector := fit.ParamVector{Data: s.incumbent, K: s.k, Width: s.width, Height: s.height}
	candidateVector := fit.ParamVector{Data: params, K: s.k, Width: s.width, Height: s.height}
	// Reject obviously large unions before building their scanline mask. This
	// conservative disc-area sum intentionally ignores overlap and clipping: a
	// false fallback costs only the optimization, while walking a canvas-sized
	// proposed circle merely to decide to do a full render can itself cost as
	// much as that render.
	areaLimit := float64(s.width*s.height) * s.preflightMaxFraction
	area := 0.0

	for _, circle := range s.activeCircles {
		incumbentCircle := incumbentVector.DecodeCircle(circle)
		if incumbentCircle.Opacity != 0 && incumbentCircle.R > 0 {
			area += math.Pi * incumbentCircle.R * incumbentCircle.R
		}

		candidateCircle := candidateVector.DecodeCircle(circle)
		if candidateCircle.Opacity != 0 && candidateCircle.R > 0 {
			area += math.Pi * candidateCircle.R * candidateCircle.R
		}

		if area > areaLimit {
			s.fallbacks++
			s.preflightFallbacks++
			s.fullRestore = true
			s.previousDirty.reset(0, 1)

			return s.CPURenderer.Cost(params)
		}
	}

	for _, circle := range s.activeCircles {
		s.collectCircleSpans(incumbentVector.DecodeCircle(circle), &s.dirty)
		s.collectCircleSpans(candidateVector.DecodeCircle(circle), &s.dirty)
	}

	pixels, _ := s.dirty.metrics()
	s.recordAffectedFraction(float64(pixels) / float64(s.width*s.height))

	if s.dirty.overflow || float64(pixels) > float64(s.width*s.height)*s.maxFraction {
		s.fallbacks++
		s.fullRestore = true
		s.previousDirty.reset(0, 1)

		return s.CPURenderer.Cost(params)
	}

	if s.fullRestore {
		copy(s.canvas.Pix, s.baseline.Pix)
		s.fullRestore = false
	} else {
		s.copyImageSpans(s.canvas, s.baseline, &s.previousDirty)
	}

	s.copyPackedSpans(s.canvas, s.initialBg, &s.dirty)
	s.compositeDirty(params, &s.dirty)
	delta := s.ssdDeltaFromBaseline(&s.dirty)

	total, ok := addSSDDelta(s.baselineSSD, delta)
	if !ok {
		s.fallbacks++
		s.fullRestore = true

		return s.CPURenderer.Cost(params)
	}

	s.previousDirty, s.dirty = s.dirty, s.previousDirty

	return float64(total) / float64(s.width*s.height*3)
}

func (s *polishDirtySession) collectCircleSpans(circle fit.Circle, dirty *dirtySpanSet) {
	if circle.Opacity == 0 {
		return
	}

	minY, maxY, ok := s.circleVerticalBounds(circle, 0, s.height)
	if !ok {
		return
	}

	// Rounded at the assignment, not only where it is used: this is a product,
	// so leaving it unrounded lets arm64 fuse it straight into the subtraction
	// below as FNMSUBD and decide a span boundary differently than amd64.
	radiusSquared := float64(circle.R * circle.R)

	fixed, useFixed := fixedCircleQ16{}, false
	if !s.forceFloatGeometry && !s.forceFloat32Geometry {
		fixed, useFixed = newFixedCircleQ16(circle)
	}

	if useFixed {
		for y := minY; y < maxY; y++ {
			start, end, intersects := fixed.span(y, s.width)
			if intersects {
				dirty.add(y, start, end)
			}
		}

		return
	}

	if s.forceFloat32Geometry {
		center := float32(circle.X)
		centerY := float32(circle.Y)
		radius := float32(circle.R)
		radiusSquared32 := float32(radius * radius)

		for y := minY; y < maxY; y++ {
			dy := float32(y) - centerY

			remaining := radiusSquared32 - float32(dy*dy)
			if remaining < 0 {
				continue
			}

			start, end := circleSpanFloat32Selected(center, remaining, s.width)
			dirty.add(y, max(0, start), end)
		}

		return
	}

	for y := minY; y < maxY; y++ {
		dy := float64(y) - circle.Y

		remaining := radiusSquared - float64(dy*dy)
		if remaining < 0 {
			continue
		}

		start, end := circleSpanFloat64(circle.X, remaining, s.width)
		dirty.add(y, max(0, start), end)
	}
}

func (s *polishDirtySession) circleVerticalBounds(circle fit.Circle, rowStart, rowEnd int) (int, int, bool) {
	minYf := circle.Y - circle.R

	maxYf := circle.Y + circle.R
	if maxYf < 0 || minYf >= float64(s.height) {
		return 0, 0, false
	}

	minY := max(rowStart, max(0, int(minYf)))
	maxY := min(rowEnd, min(s.height, int(maxYf+1)))

	return minY, maxY, minY < maxY
}

func (s *polishDirtySession) compositeDirty(params []float64, dirty *dirtySpanSet) {
	if s.threads <= 1 {
		s.compositeDirtyRows(params, dirty, 0, s.height)
		return
	}
	var workers sync.WaitGroup
	workers.Add(s.threads - 1)

	for worker := range s.threads - 1 {
		minY := worker * s.height / s.threads
		maxY := (worker + 1) * s.height / s.threads

		go func() {
			defer workers.Done()

			s.compositeDirtyRows(params, dirty, minY, maxY)
		}()
	}

	s.compositeDirtyRows(params, dirty, (s.threads-1)*s.height/s.threads, s.height)
	workers.Wait()
}

func (s *polishDirtySession) compositeDirtyRows(params []float64, dirty *dirtySpanSet, minY, maxY int) {
	vector := fit.ParamVector{Data: params, K: s.k, Width: s.width, Height: s.height}
	for circle := range s.k {
		s.compositeCircleDirtyRows(vector.DecodeCircle(circle), dirty, minY, maxY)
	}
}

func (s *polishDirtySession) compositeCircleDirtyRows(circle fit.Circle, dirty *dirtySpanSet, rowStart, rowEnd int) {
	if circle.Opacity == 0 {
		return
	}

	minY, maxY, ok := s.circleVerticalBounds(circle, rowStart, rowEnd)
	if !ok {
		return
	}

	// Built once per circle for the same reason as in renderCircleScanlineRowsTracked,
	// and it matters more here: compositeDirtySpan reaches compositeCircleSpan once
	// per dirty sub-span, so a single row could rebuild the block several times.
	blend := newSpanBlend(circle.CR, circle.CG, circle.CB, circle.Opacity)

	// Rounded at the assignment, not only where it is used: this is a product,
	// so leaving it unrounded lets arm64 fuse it straight into the subtraction
	// below as FNMSUBD and decide a span boundary differently than amd64.
	radiusSquared := float64(circle.R * circle.R)

	fixed, useFixed := fixedCircleQ16{}, false
	if !s.forceFloatGeometry && !s.forceFloat32Geometry {
		fixed, useFixed = newFixedCircleQ16(circle)
	}

	if useFixed {
		for y := minY; y < maxY; y++ {
			start, end, intersects := fixed.span(y, s.width)
			if intersects {
				s.compositeDirtySpan(circle, &blend, dirty, y, start, end)
			}
		}

		return
	}

	if s.forceFloat32Geometry {
		center := float32(circle.X)
		centerY := float32(circle.Y)
		radius := float32(circle.R)
		radiusSquared32 := float32(radius * radius)

		for y := minY; y < maxY; y++ {
			dy := float32(y) - centerY

			remaining := radiusSquared32 - float32(dy*dy)
			if remaining < 0 {
				continue
			}

			start, end := circleSpanFloat32Selected(center, remaining, s.width)
			s.compositeDirtySpan(circle, &blend, dirty, y, max(0, start), end)
		}

		return
	}

	for y := minY; y < maxY; y++ {
		dy := float64(y) - circle.Y

		remaining := radiusSquared - float64(dy*dy)
		if remaining < 0 {
			continue
		}

		start, end := circleSpanFloat64(circle.X, remaining, s.width)
		s.compositeDirtySpan(circle, &blend, dirty, y, max(0, start), end)
	}
}

func (s *polishDirtySession) compositeDirtySpan(
	circle fit.Circle,
	blend *spanBlend,
	dirty *dirtySpanSet,
	y, start, end int,
) {
	for _, affected := range dirty.row(y) {
		if affected.end <= start {
			continue
		}

		if affected.start >= end {
			break
		}

		s.compositeCircleSpan(s.canvas, circle, blend, y, max(start, affected.start), min(end, affected.end))
	}
}

func (s *polishDirtySession) copyImageSpans(dst, src *image.NRGBA, dirty *dirtySpanSet) {
	for y := range s.height {
		for _, span := range dirty.row(y) {
			start := span.start * 4
			end := span.end * 4
			copy(dst.Pix[y*dst.Stride+start:y*dst.Stride+end], src.Pix[y*src.Stride+start:y*src.Stride+end])
		}
	}
}

func (s *polishDirtySession) copyPackedSpans(dst *image.NRGBA, src []byte, dirty *dirtySpanSet) {
	for y := range s.height {
		for _, span := range dirty.row(y) {
			start := span.start * 4
			end := span.end * 4
			copy(dst.Pix[y*dst.Stride+start:y*dst.Stride+end], src[y*s.width*4+start:y*s.width*4+end])
		}
	}
}

func (s *polishDirtySession) ssdDeltaFromBaseline(dirty *dirtySpanSet) int64 {
	var delta int64

	for y := range s.height {
		for _, span := range dirty.row(y) {
			candidateOffset := y*s.canvas.Stride + span.start*4
			baselineOffset := y*s.baseline.Stride + span.start*4
			referenceOffset := y*s.reference.Stride + span.start*4
			delta += deltaSSDSpan(
				s.canvas.Pix[candidateOffset:],
				s.baseline.Pix[baselineOffset:],
				s.reference.Pix[referenceOffset:],
				span.end-span.start,
			)
		}
	}

	return delta
}

func (s *polishDirtySession) fallbackRate() float64 {
	if s.evaluations == 0 {
		return 0
	}

	return float64(s.fallbacks) / float64(s.evaluations)
}

// recordAffectedFraction files one evaluation into the affected-pixel
// histogram. The mask is already built and measured at the call site, so this
// costs one comparison loop over eleven edges and no allocation.
func (s *polishDirtySession) recordAffectedFraction(fraction float64) {
	s.fractionSum += fraction
	if fraction > s.fractionMax {
		s.fractionMax = fraction
	}

	for i, edge := range polishDirtyFractionEdges {
		if fraction < edge {
			s.fractionCounts[i]++

			return
		}
	}

	s.fractionCounts[len(polishDirtyFractionEdges)-1]++
}

// maskedEvaluations counts the evaluations that built a scanline mask and so
// contributed to the affected-pixel histogram. It includes the ones the mask
// gate then rejected, which is what makes it the right denominator for the
// histogram and the wrong one for "did the dirty path score anything".
func (s *polishDirtySession) maskedEvaluations() int {
	return s.evaluations - s.preflightFallbacks
}

// scoredEvaluations counts the evaluations the dirty path carried all the way
// to a cost, taking neither the preflight, the mask gate, nor the delta
// overflow out to the full canvas. Only these say the evaluator was exercised.
func (s *polishDirtySession) scoredEvaluations() int {
	return s.evaluations - s.fallbacks
}
