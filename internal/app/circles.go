package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParamsPerCircle is how many float64 slots one circle occupies in the flat
// parameter vector the optimizer and the checkpoint both use: X, Y, R, then the
// three colour channels, then opacity. It restates fit.paramsPerCircle rather
// than importing it, because internal/app is the leaf configuration package
// every other package depends on and must not grow an internal dependency.
const ParamsPerCircle = 7

// CircleSpec is a hand-authored circle. It exists so a run can start from a
// known arrangement instead of a random one: the optimizer's own vector packs
// colour as three [0,1] floats, which is the wrong shape to write by hand, so
// the authored form takes a hex colour and the conversion happens once, in
// ToParams.
//
// Specs are painted back to front, exactly like the parameter vector they
// become: entry zero is the backdrop and the last entry is on top.
type CircleSpec struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	R       float64 `json:"r"`
	Color   string  `json:"color"`
	Opacity float64 `json:"opacity,omitempty"`
}

// CircleSpecs is an ordered, back-to-front list of hand-authored circles.
type CircleSpecs []CircleSpec

// ApplyDefaults fills the one omittable field. Opacity zero cannot be meant
// literally — a fully transparent circle is a circle that was not authored — so
// it reads as "opaque", which is what almost every hand-placed circle wants.
func (s CircleSpecs) ApplyDefaults() {
	for i := range s {
		if s[i].Opacity == 0 {
			s[i].Opacity = 1
		}
	}
}

// Validate checks what can be checked without knowing the canvas. Position and
// radius are only meaningful against the reference image's dimensions, so the
// bounds check on those belongs to whoever has loaded the reference; here the
// concern is that every value is finite, the colour parses, and the opacity is
// one the renderer can actually composite.
func (s CircleSpecs) Validate() error {
	for i, spec := range s {
		field := func(name string) string { return fmt.Sprintf("initialCircles[%d].%s", i, name) }
		for name, value := range map[string]float64{"x": spec.X, "y": spec.Y, "r": spec.R} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return invalid(field(name), "must be finite")
			}
		}
		if spec.R < 1 {
			return invalid(field("r"), "must be at least 1")
		}
		if _, _, _, err := parseHexColor(spec.Color); err != nil {
			return invalid(field("color"), err.Error())
		}
		if math.IsNaN(spec.Opacity) || spec.Opacity <= 0 || spec.Opacity > 1 {
			return invalid(field("opacity"), "must be greater than 0 and no greater than 1")
		}
	}
	return nil
}

// ToParams flattens the specs into the optimizer's parameter vector. It is the
// inverse of store.ParamVectorToCircles, which decomposes the same layout for
// display.
func (s CircleSpecs) ToParams() ([]float64, error) {
	params := make([]float64, 0, len(s)*ParamsPerCircle)
	for i, spec := range s {
		red, green, blue, err := parseHexColor(spec.Color)
		if err != nil {
			return nil, invalid(fmt.Sprintf("initialCircles[%d].color", i), err.Error())
		}
		opacity := spec.Opacity
		if opacity == 0 {
			opacity = 1
		}
		params = append(params, spec.X, spec.Y, spec.R, red, green, blue, opacity)
	}
	return params, nil
}

// parseHexColor converts "#rrggbb" to three channels in [0,1]. The leading hash
// is optional so a colour copied out of an editor pastes either way.
func parseHexColor(value string) (red, green, blue float64, err error) {
	digits := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(digits) != 6 {
		return 0, 0, 0, fmt.Errorf("must be a six digit hex colour such as #4a3226")
	}
	channels := [3]float64{}
	for i := range channels {
		parsed, parseErr := strconv.ParseUint(digits[i*2:i*2+2], 16, 8)
		if parseErr != nil {
			return 0, 0, 0, fmt.Errorf("must be a six digit hex colour such as #4a3226")
		}
		channels[i] = float64(parsed) / 255
	}
	return channels[0], channels[1], channels[2], nil
}
