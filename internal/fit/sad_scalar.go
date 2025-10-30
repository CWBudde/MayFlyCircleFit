package fit

// Scalar implementation of SAD with quadratic weighting.
// This matches the Delphi ErrorWeightingLoopNative function.

const (
	// CScale from Delphi: 1.5378700499807766243752402921953E-6
	// This normalizes the weighted cost to a reasonable range
	sadScale = 1.5378700499807766243752402921953e-6
)

// fastSAD_Scalar computes SAD with quadratic weighting (scalar reference).
//
// Algorithm (matching Delphi ErrorWeightingLoopNative):
//   For each pixel:
//     value = |R1-R2| + |G1-G2| + |B1-B2|
//     cost += scale × value × (255 + 9×value)
//
// This expands to: scale × (255×value + 9×value²)
//
// The quadratic term (9×value²) provides perceptual weighting - larger
// differences contribute disproportionately more to the cost, which better
// matches human perception of image differences.
func fastSAD_Scalar(a, b []uint8, stride, width, height int) float64 {
	var totalCost float64

	for y := 0; y < height; y++ {
		rowStart := y * stride

		for x := 0; x < width; x++ {
			i := rowStart + x*4

			// Compute absolute differences for RGB channels (ignore alpha)
			dr := int(a[i+0]) - int(b[i+0])
			if dr < 0 {
				dr = -dr
			}

			dg := int(a[i+1]) - int(b[i+1])
			if dg < 0 {
				dg = -dg
			}

			db := int(a[i+2]) - int(b[i+2])
			if db < 0 {
				db = -db
			}

			// Sum of absolute differences for this pixel
			value := dr + dg + db

			// Apply quadratic weighting: value × (255 + 9×value)
			// This can be computed as: 255×value + 9×value²
			weighted := value * (255 + 9*value)

			totalCost += float64(weighted)
		}
	}

	// Apply final scale factor
	return totalCost * sadScale
}

// sadScalarPerPixel computes SAD cost for a single pixel (for testing).
func sadScalarPerPixel(current, reference [4]uint8) float64 {
	// Compute absolute differences for RGB (ignore alpha)
	dr := int(current[0]) - int(reference[0])
	if dr < 0 {
		dr = -dr
	}

	dg := int(current[1]) - int(reference[1])
	if dg < 0 {
		dg = -dg
	}

	db := int(current[2]) - int(reference[2])
	if db < 0 {
		db = -db
	}

	value := dr + dg + db
	weighted := value * (255 + 9*value)

	return float64(weighted) * sadScale
}
