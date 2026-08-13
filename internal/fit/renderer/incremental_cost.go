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
	if !r.initialSSDValid || len(dirty.rows) != r.height {
		return 0, false
	}

	var delta int64
	for y, row := range dirty.rows {
		referenceRow := y * r.reference.Stride
		canvasRow := y * rendered.Stride
		initialRow := y * r.width * 4
		for _, span := range row {
			for x := span.start; x < span.end; x++ {
				referenceOffset := referenceRow + x*4
				canvasOffset := canvasRow + x*4
				initialOffset := initialRow + x*4
				before := rgbSquaredError(r.initialBg, initialOffset, r.reference.Pix, referenceOffset)
				after := rgbSquaredError(rendered.Pix, canvasOffset, r.reference.Pix, referenceOffset)
				delta += int64(after) - int64(before)
			}
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

func rgbSquaredError(pixels []byte, offset int, reference []byte, referenceOffset int) uint64 {
	red := int64(pixels[offset+0]) - int64(reference[referenceOffset+0])
	green := int64(pixels[offset+1]) - int64(reference[referenceOffset+1])
	blue := int64(pixels[offset+2]) - int64(reference[referenceOffset+2])
	return uint64(red*red + green*green + blue*blue)
}
