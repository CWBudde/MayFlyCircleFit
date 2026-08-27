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
}

func newPolishDirtySession(
	session Renderer,
	baseline *image.NRGBA,
	baselineSSD uint64,
	incumbent []float64,
	activeCircles []int,
) Renderer {
	cpu, ok := session.(*CPURenderer)
	if !ok || !cpu.fastCostSelected || baseline == nil || len(incumbent) != cpu.Dim() {
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
