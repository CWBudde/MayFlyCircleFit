package fit

import (
	"image/color"
	"math"
	"strings"
)

// Colormap identifies a false-color palette for normalized scalar values.
type Colormap string

const (
	// ColormapTurbo is Google's perceptually smooth rainbow-style colormap.
	ColormapTurbo Colormap = "turbo"
	// ColormapMagma is a perceptually uniform black-purple-orange-yellow colormap.
	ColormapMagma Colormap = "magma"
)

// ParseColormap validates a user-facing colormap name.
func ParseColormap(name string) (Colormap, bool) {
	switch Colormap(strings.ToLower(strings.TrimSpace(name))) {
	case ColormapTurbo:
		return ColormapTurbo, true
	case ColormapMagma:
		return ColormapMagma, true
	default:
		return "", false
	}
}

// MapErrorColor maps an error in [0, maxError] to an opaque false color.
// Values outside the range are clamped. A non-positive maxError maps to the
// palette's zero value.
func MapErrorColor(value, maxError float64, colormap Colormap) color.NRGBA {
	normalized := 0.0
	if maxError > 0 && !math.IsNaN(maxError) {
		normalized = value / maxError
	}

	return MapNormalizedColor(normalized, colormap)
}

// MapNormalizedColor maps a normalized scalar to an opaque false color.
// Unknown colormaps fall back to Turbo, the application's default.
func MapNormalizedColor(value float64, colormap Colormap) color.NRGBA {
	value = clampUnit(value)
	if colormap == ColormapMagma {
		return interpolateMagma(value)
	}

	return turboColor(value)
}

func turboColor(value float64) color.NRGBA {
	// Polynomial approximation published with Google's Turbo colormap.
	r := 34.61 + value*(1172.33+value*(-10793.56+value*(33300.12+value*(-38394.49+value*14825.05))))
	g := 23.31 + value*(557.33+value*(1225.33+value*(-3574.96+value*(1073.77+value*707.56))))
	b := 27.20 + value*(3211.10+value*(-15327.97+value*(27814.00+value*(-22569.18+value*6838.66))))

	return color.NRGBA{R: byteChannel(r), G: byteChannel(g), B: byteChannel(b), A: 255}
}

var magmaStops = [...]color.NRGBA{
	{R: 0, G: 0, B: 4, A: 255},
	{R: 24, G: 15, B: 61, A: 255},
	{R: 61, G: 15, B: 112, A: 255},
	{R: 101, G: 21, B: 110, A: 255},
	{R: 140, G: 41, B: 97, A: 255},
	{R: 183, G: 55, B: 75, A: 255},
	{R: 222, G: 73, B: 47, A: 255},
	{R: 246, G: 112, B: 25, A: 255},
	{R: 253, G: 165, B: 10, A: 255},
	{R: 249, G: 220, B: 92, A: 255},
	{R: 252, G: 253, B: 191, A: 255},
}

func interpolateMagma(value float64) color.NRGBA {
	position := value * float64(len(magmaStops)-1)

	index := int(position)
	if index >= len(magmaStops)-1 {
		return magmaStops[len(magmaStops)-1]
	}

	fraction := position - float64(index)
	left := magmaStops[index]
	right := magmaStops[index+1]

	return color.NRGBA{
		R: interpolateChannel(left.R, right.R, fraction),
		G: interpolateChannel(left.G, right.G, fraction),
		B: interpolateChannel(left.B, right.B, fraction),
		A: 255,
	}
}

func interpolateChannel(left, right uint8, fraction float64) uint8 {
	return uint8(math.Round(float64(left) + (float64(right)-float64(left))*fraction))
}

func byteChannel(value float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(255, value))))
}

func clampUnit(value float64) float64 {
	if math.IsNaN(value) || value <= 0 {
		return 0
	}

	if value >= 1 {
		return 1
	}

	return value
}
