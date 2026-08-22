package fit

import (
	"image/color"
	"math"
	"testing"
)

func TestParseColormap(t *testing.T) {
	tests := []struct {
		input string
		want  Colormap
		ok    bool
	}{
		{input: "turbo", want: ColormapTurbo, ok: true},
		{input: " MAGMA ", want: ColormapMagma, ok: true},
		{input: "viridis", ok: false},
		{input: "", ok: false},
	}

	for _, test := range tests {
		got, ok := ParseColormap(test.input)
		if got != test.want || ok != test.ok {
			t.Errorf("ParseColormap(%q) = (%q, %v), want (%q, %v)", test.input, got, ok, test.want, test.ok)
		}
	}
}

func TestMapErrorColorClampsAndNormalizes(t *testing.T) {
	zero := color.NRGBA{R: 35, G: 23, B: 27, A: 255}
	maximum := color.NRGBA{R: 144, G: 12, B: 0, A: 255}

	tests := []struct {
		name     string
		value    float64
		maxError float64
		want     color.NRGBA
	}{
		{name: "zero", value: 0, maxError: 255, want: zero},
		{name: "negative clamps", value: -1, maxError: 255, want: zero},
		{name: "maximum", value: 255, maxError: 255, want: maximum},
		{name: "above maximum clamps", value: 500, maxError: 255, want: maximum},
		{name: "zero range", value: 10, maxError: 0, want: zero},
		{name: "nan", value: math.NaN(), maxError: 255, want: zero},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MapErrorColor(test.value, test.maxError, ColormapTurbo); got != test.want {
				t.Errorf("MapErrorColor(%v, %v, turbo) = %#v, want %#v", test.value, test.maxError, got, test.want)
			}
		})
	}
}

func TestMagmaEndpointsAndInterpolation(t *testing.T) {
	if got, want := MapNormalizedColor(0, ColormapMagma), magmaStops[0]; got != want {
		t.Errorf("magma(0) = %#v, want %#v", got, want)
	}

	if got, want := MapNormalizedColor(1, ColormapMagma), magmaStops[len(magmaStops)-1]; got != want {
		t.Errorf("magma(1) = %#v, want %#v", got, want)
	}

	middle := MapNormalizedColor(0.5, ColormapMagma)
	if middle == magmaStops[0] || middle == magmaStops[len(magmaStops)-1] || middle.A != 255 {
		t.Errorf("magma(0.5) = %#v, want an opaque intermediate color", middle)
	}
}

func TestUnknownColormapFallsBackToTurbo(t *testing.T) {
	if got, want := MapNormalizedColor(0.4, Colormap("unknown")), MapNormalizedColor(0.4, ColormapTurbo); got != want {
		t.Errorf("unknown colormap = %#v, want Turbo %#v", got, want)
	}
}
