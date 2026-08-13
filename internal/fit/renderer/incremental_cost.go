package renderer

import (
	"image"
	"math"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

type incrementalCostMode uint8

const (
	incrementalCostDisabled incrementalCostMode = iota
	incrementalCostAuto
	incrementalCostForce

	incrementalSmallImagePixels = 128 * 128
)

// incrementalCostWorthwhile models native AVX2 measurements. Three dirty-pixel
// units account for reading candidate, base, and reference instead of the full
// kernel's two inputs; 16 units per span cover SIMD call/setup and fragmented
// short tails. The resulting boundary is conservative relative to the measured
// 30.6%-to-44.1% single-span crossover.
func incrementalCostWorthwhile(dirty *dirtySpanSet, totalPixels int) bool {
	pixels, spans := dirty.metrics()
	if pixels == 0 {
		return true
	}
	if totalPixels < incrementalSmallImagePixels {
		// At small dimensions the fixed per-span and dispatch costs are a larger
		// share of a full AVX2 replay. Native 64x64 pipeline measurements put the
		// useful K1 crossover below the large-image policy.
		estimatedWork := uint64(pixels)*6 + uint64(spans)*16
		return estimatedWork <= uint64(totalPixels)
	}
	estimatedWork := uint64(pixels)*3 + uint64(spans)*16
	return estimatedWork <= uint64(totalPixels)
}

// incrementalCandidateWorthwhile avoids span collection entirely for obvious
// fallback cases. Summed circle area is an upper bound apart from scanline
// quantization (and deliberately ignores overlap/clipping). The 30% large-image
// and 15% small-image limits leave margin below their measured native
// crossovers.
func (r *CPURenderer) incrementalCandidateWorthwhile(params []float64) bool {
	maxAreaFraction := 0.30
	if r.width*r.height < incrementalSmallImagePixels {
		maxAreaFraction = 0.15
	}
	limit := float64(r.width*r.height) * maxAreaFraction
	pv := fit.ParamVector{Data: params, K: r.k, Width: r.width, Height: r.height}
	area := 0.0
	for i := 0; i < r.k; i++ {
		circle := pv.DecodeCircle(i)
		if circle.Opacity == 0 || circle.R <= 0 {
			continue
		}
		area += math.Pi * circle.R * circle.R
		if area > limit {
			return false
		}
	}
	return true
}

// incrementalStagedSessionEligible avoids paying per-candidate preflight costs
// where full-image AVX2 is already very cheap. Small single-circle stages keep
// the path because their measured end-to-end gain remains above 10%.
func (r *CPURenderer) incrementalStagedSessionEligible() bool {
	return r.width*r.height >= incrementalSmallImagePixels || r.k == 1
}

func (r *CPURenderer) incrementalSSDTotal(rendered *image.NRGBA, dirty *dirtySpanSet) (uint64, bool) {
	if !r.initialSSDValid || dirty.height != r.height || !dirty.normalize() {
		return 0, false
	}

	delta := r.incrementalSSDDeltaRows(rendered, dirty, 0, dirty.height)
	return addSSDDelta(r.initialSSD, delta)
}

func (r *CPURenderer) incrementalSSDDeltaRows(rendered *image.NRGBA, dirty *dirtySpanSet, minY, maxY int) int64 {
	var delta int64
	for y := minY; y < maxY; y++ {
		referenceRow := y * r.reference.Stride
		canvasRow := y * rendered.Stride
		initialRow := y * r.width * 4
		for _, span := range dirty.row(y) {
			canvasOffset := canvasRow + span.start*4
			initialOffset := initialRow + span.start*4
			referenceOffset := referenceRow + span.start*4
			delta += deltaSSDSpan(
				rendered.Pix[canvasOffset:],
				r.initialBg[initialOffset:],
				r.reference.Pix[referenceOffset:],
				span.end-span.start,
			)
		}
	}
	return delta
}

func addSSDDelta(initialSSD uint64, delta int64) (uint64, bool) {
	if delta < 0 {
		decrease := uint64(-delta)
		if decrease > initialSSD {
			return 0, false
		}
		return initialSSD - decrease, true
	}
	increase := uint64(delta)
	if increase > ^uint64(0)-initialSSD {
		return 0, false
	}
	return initialSSD + increase, true
}
