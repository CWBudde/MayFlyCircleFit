package renderer

import "image"

type incrementalCostMode uint8

const (
	incrementalCostDisabled incrementalCostMode = iota
	incrementalCostAuto
	incrementalCostForce
)

// incrementalCostWorthwhile is deliberately conservative while the dirty
// update is scalar and the full-image fallback is SIMD. Eight units per pixel
// approximate the measured AVX2/scalar throughput gap; the per-span charge
// penalizes fragmented batches. Task 10.16c will replace this provisional model
// with native scalar/SIMD crossover measurements.
func incrementalCostWorthwhile(dirty *dirtySpanSet, totalPixels int) bool {
	pixels, spans := dirty.metrics()
	if pixels == 0 {
		return true
	}
	estimatedWork := uint64(pixels)*8 + uint64(spans)*32
	return estimatedWork <= uint64(totalPixels)
}

func (r *CPURenderer) incrementalSSDTotal(rendered *image.NRGBA, dirty *dirtySpanSet) (uint64, bool) {
	if !r.initialSSDValid || dirty.height != r.height || !dirty.normalize() {
		return 0, false
	}

	var delta int64
	for y := 0; y < dirty.height; y++ {
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

	if delta < 0 {
		decrease := uint64(-delta)
		if decrease > r.initialSSD {
			return 0, false
		}
		return r.initialSSD - decrease, true
	}
	increase := uint64(delta)
	if increase > ^uint64(0)-r.initialSSD {
		return 0, false
	}
	return r.initialSSD + increase, true
}
