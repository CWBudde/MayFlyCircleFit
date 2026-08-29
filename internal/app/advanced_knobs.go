package app

import (
	"fmt"
	"math"
)

// advancedKnob pairs a low-level MayFly parameter with the JSON field that
// carries it, so the range check and the variant guard walk the same list.
type advancedKnob struct {
	field string
	value *float64
}

// advancedKnobs lists the three knobs in a fixed order, so a configuration
// setting more than one always reports the same one first.
func (c *JobConfig) advancedKnobs() []advancedKnob {
	return []advancedKnob{
		{"danceDamp", c.DanceDamp},
		{"aquilaWeight", c.AquilaWeight},
		{"oppositionProbability", c.OppositionProbability},
	}
}

// validateAdvancedOptimizerKnobs range-checks the three low-level MayFly
// parameters and refuses the two that only the aoblmoa variant reads.
func (c *JobConfig) validateAdvancedOptimizerKnobs() error {
	err := c.validateAdvancedKnobRanges()
	if err != nil {
		return err
	}

	return c.validateAOBLMOAOnlyKnobs()
}

// validateAdvancedKnobRanges enforces [0, 1] on every knob that is set.
//
// The library range-checks aquilaWeight and oppositionProbability itself, but
// silently clamps rather than reporting, and does not check danceDamp at all --
// a damping factor above 1 grows the dance coefficient every iteration. The
// library still returns finite results -- velocity and position are clamped --
// so this bound guards against a saturated random walk, not against a crash.
func (c *JobConfig) validateAdvancedKnobRanges() error {
	for _, knob := range c.advancedKnobs() {
		if knob.value == nil {
			continue
		}

		if math.IsNaN(*knob.value) || *knob.value < 0 || *knob.value > 1 {
			return invalid(knob.field, "must be between 0 and 1")
		}
	}

	return nil
}

// validateAOBLMOAOnlyKnobs refuses the two knobs no other variant reads.
//
// Rejecting rather than ignoring them is the point: a configuration that set
// aquilaWeight on a standard run would otherwise be accepted, persisted into a
// checkpoint and reported back unchanged while never reaching the optimizer.
func (c *JobConfig) validateAOBLMOAOnlyKnobs() error {
	if c.Variant == VariantAOBLMOA {
		return nil
	}

	running := c.Variant
	if running == "" {
		running = VariantStandard
	}

	for _, knob := range c.advancedKnobs() {
		if knob.field == "danceDamp" || knob.value == nil {
			continue
		}

		return invalid(knob.field, fmt.Sprintf(
			"is read only by the %q variant, but this job runs %q", VariantAOBLMOA, running))
	}

	return nil
}
