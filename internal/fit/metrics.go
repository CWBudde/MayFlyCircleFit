package fit

import (
	"fmt"
	"image"
	"math"
)

const (
	ssimWindowRadius = 5
	ssimWindowSize   = 2*ssimWindowRadius + 1
	ssimSigma        = 1.5
	ssimStats        = 5
)

// PSNR converts an RGB mean squared error on the 0-255 channel scale to peak
// signal-to-noise ratio in decibels. A perfect match has positive-infinite
// PSNR. Invalid MSE values return NaN.
func PSNR(mse float64) float64 {
	if math.IsNaN(mse) || math.IsInf(mse, 0) || mse < 0 {
		return math.NaN()
	}
	if mse == 0 {
		return math.Inf(1)
	}
	return 20 * math.Log10(255/math.Sqrt(mse))
}

// SSIM computes the mean structural similarity index over the RGB channels.
// It uses an 11x11 Gaussian window with sigma 1.5, reflected borders, and the
// conventional K1=0.01 and K2=0.03 constants for 8-bit samples. Alpha is
// deliberately ignored, matching MSECost and FastMSECost.
func SSIM(current, reference *image.NRGBA) (float64, error) {
	if current == nil || reference == nil {
		return 0, fmt.Errorf("SSIM requires two images")
	}
	width, height := current.Bounds().Dx(), current.Bounds().Dy()
	if width == 0 || height == 0 {
		return 0, fmt.Errorf("SSIM requires non-empty images")
	}
	if width != reference.Bounds().Dx() || height != reference.Bounds().Dy() {
		return 0, fmt.Errorf("SSIM image dimensions do not match")
	}

	weights := gaussianWeights()
	const (
		c1 = (0.01 * 255) * (0.01 * 255)
		c2 = (0.03 * 255) * (0.03 * 255)
	)

	var total float64
	for channel := 0; channel < 3; channel++ {
		ring := make([][]float64, ssimWindowSize)
		for i := range ring {
			ring[i] = make([]float64, width*ssimStats)
		}
		ringWrite := 0
		rowsBuffered := 0

		for paddedY := -ssimWindowRadius; paddedY < height+ssimWindowRadius; paddedY++ {
			horizontalSSIMRow(ring[ringWrite], current, reference, reflectIndex(paddedY, height), channel, weights)
			ringWrite = (ringWrite + 1) % ssimWindowSize
			if rowsBuffered < ssimWindowSize {
				rowsBuffered++
			}
			if rowsBuffered < ssimWindowSize {
				continue
			}

			for x := 0; x < width; x++ {
				var blurred [ssimStats]float64
				for tap, weight := range weights {
					row := ring[(ringWrite+tap)%ssimWindowSize]
					base := x * ssimStats
					for stat := range ssimStats {
						blurred[stat] += weight * row[base+stat]
					}
				}

				meanCurrent, meanReference := blurred[0], blurred[1]
				varianceCurrent := math.Max(0, blurred[2]-meanCurrent*meanCurrent)
				varianceReference := math.Max(0, blurred[3]-meanReference*meanReference)
				covariance := blurred[4] - meanCurrent*meanReference

				numerator := (2*meanCurrent*meanReference + c1) * (2*covariance + c2)
				denominator := (meanCurrent*meanCurrent + meanReference*meanReference + c1) *
					(varianceCurrent + varianceReference + c2)
				total += numerator / denominator
			}
		}
	}

	value := total / float64(width*height*3)
	return math.Min(1, math.Max(-1, value)), nil
}

func gaussianWeights() [ssimWindowSize]float64 {
	var weights [ssimWindowSize]float64
	var sum float64
	for i := range weights {
		x := float64(i - ssimWindowRadius)
		weights[i] = math.Exp(-(x * x) / (2 * ssimSigma * ssimSigma))
		sum += weights[i]
	}
	for i := range weights {
		weights[i] /= sum
	}
	return weights
}

func horizontalSSIMRow(dst []float64, current, reference *image.NRGBA, y, channel int, weights [ssimWindowSize]float64) {
	currentY := current.Bounds().Min.Y + y
	referenceY := reference.Bounds().Min.Y + y
	width := current.Bounds().Dx()
	for x := 0; x < width; x++ {
		var values [ssimStats]float64
		for tap, weight := range weights {
			sampleX := reflectIndex(x+tap-ssimWindowRadius, width)
			currentOffset := current.PixOffset(current.Bounds().Min.X+sampleX, currentY)
			referenceOffset := reference.PixOffset(reference.Bounds().Min.X+sampleX, referenceY)
			a := float64(current.Pix[currentOffset+channel])
			b := float64(reference.Pix[referenceOffset+channel])
			values[0] += weight * a
			values[1] += weight * b
			values[2] += weight * a * a
			values[3] += weight * b * b
			values[4] += weight * a * b
		}
		copy(dst[x*ssimStats:(x+1)*ssimStats], values[:])
	}
}

// reflectIndex implements reflect-101 border extension and also supports
// images smaller than the SSIM window.
func reflectIndex(index, size int) int {
	if size <= 1 {
		return 0
	}
	period := 2*size - 2
	index %= period
	if index < 0 {
		index += period
	}
	if index >= size {
		index = period - index
	}
	return index
}
