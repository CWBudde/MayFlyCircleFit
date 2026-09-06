package app

import (
	"errors"
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
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	R     float64 `json:"r"`
	Color string  `json:"color,omitempty"`
	// RGB is the exact colour, as three [0,1] channels in red, green, blue
	// order. It is the alternative to Color and exactly one of the two must be
	// present.
	//
	// It exists because the hex form is lossy and the loss is now larger than
	// the effects being measured: a channel round trips through eight bits, so
	// re-seeding a solution costs more cost than a polishing sweep gains --
	// measured at 3.73 against a gain of 3.34 on the eight-circle record. The
	// output side never had this problem, because store.CircleData and the
	// params.json export both carry float channels; the loss lived entirely in
	// the one direction that had no float form to parse.
	//
	// Hex stays the default for a hand-authored arrangement, which is what
	// Color exists for. RGB is what a solution produced by a run is carried
	// back in.
	RGB     *[3]float64 `json:"rgb,omitempty"`
	Opacity float64     `json:"opacity,omitempty"`
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

		err := spec.validateColor(field)
		if err != nil {
			return err
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
		channels, err := spec.channels()
		if err != nil {
			return nil, invalid(fmt.Sprintf("initialCircles[%d].color", i), err.Error())
		}

		opacity := spec.Opacity
		if opacity == 0 {
			opacity = 1
		}

		params = append(params, spec.X, spec.Y, spec.R, channels[0], channels[1], channels[2], opacity)
	}

	return params, nil
}

// validateColor enforces that exactly one colour form is present and that it
// is one the renderer can composite. Accepting both would leave the precedence
// invisible in the document, which is the failure the exact form exists to
// avoid.
func (c CircleSpec) validateColor(field func(string) string) error {
	switch {
	case c.Color == "" && c.RGB == nil:
		return invalid(field("color"), "must be set, or give exact channels in rgb")
	case c.Color != "" && c.RGB != nil:
		return invalid(field("rgb"), "cannot be combined with color; give one or the other")
	case c.RGB != nil:
		return validateChannels(field, *c.RGB)
	}

	_, err := parseHexColor(c.Color)
	if err != nil {
		return invalid(field("color"), err.Error())
	}

	return nil
}

// validateChannels checks an exact colour. Every channel is reported by index,
// because "rgb must be between 0 and 1" does not say which of the three is not.
func validateChannels(field func(string) string, channels [3]float64) error {
	for i, channel := range channels {
		if math.IsNaN(channel) || channel < 0 || channel > 1 {
			return invalid(fmt.Sprintf("%s[%d]", field("rgb"), i), "must be between 0 and 1")
		}
	}

	return nil
}

// channels reports the colour as three [0,1] values, preferring the exact form.
func (c CircleSpec) channels() ([3]float64, error) {
	if c.RGB != nil {
		return *c.RGB, nil
	}

	return parseHexColor(c.Color)
}

// errHexColor is the one thing that can be wrong with a hex colour, so it is a
// value rather than a string built at each return: the field name the caller
// prefixes is what distinguishes the two call sites.
var errHexColor = errors.New("must be a six digit hex colour such as #4a3226")

// parseHexColor converts "#rrggbb" to three channels in [0,1], in red, green
// and blue order. The leading hash is optional so a colour copied out of an
// editor pastes either way.
//
// The conversion is lossy, and CircleSpec.RGB exists because the loss is now
// larger than the effects being measured; see the field's own comment.
func parseHexColor(value string) ([3]float64, error) {
	channels := [3]float64{}

	digits := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(digits) != 6 {
		return channels, errHexColor
	}

	for i := range channels {
		parsed, parseErr := strconv.ParseUint(digits[i*2:i*2+2], 16, 8)
		if parseErr != nil {
			return [3]float64{}, errHexColor
		}

		channels[i] = float64(parsed) / 255
	}

	return channels, nil
}
