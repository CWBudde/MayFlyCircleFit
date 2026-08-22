package renderer

// deltaSSDSpanScalar returns the exact signed change in RGB SSD between the
// candidate and retained-base versions of one contiguous NRGBA span. Alpha is
// intentionally ignored, matching fit.FastMSECost.
func deltaSSDSpanScalar(candidate, base, reference []byte, pixels int) int64 {
	var delta int64

	end := pixels * 4
	for offset := 0; offset < end; offset += 4 {
		candidateR := int64(candidate[offset+0]) - int64(reference[offset+0])
		candidateG := int64(candidate[offset+1]) - int64(reference[offset+1])
		candidateB := int64(candidate[offset+2]) - int64(reference[offset+2])
		baseR := int64(base[offset+0]) - int64(reference[offset+0])
		baseG := int64(base[offset+1]) - int64(reference[offset+1])
		baseB := int64(base[offset+2]) - int64(reference[offset+2])
		delta += candidateR*candidateR + candidateG*candidateG + candidateB*candidateB
		delta -= baseR*baseR + baseG*baseG + baseB*baseB
	}

	return delta
}
