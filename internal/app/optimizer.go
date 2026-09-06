package app

import (
	"fmt"
	"slices"
	"strings"
)

// Optimizer selects the optimization library a job runs with.
//
// It is deliberately separate from Variant: Variant names an algorithm inside
// the MayFly library, while this names the library itself. A job that runs
// Dragonfly has no MayFly variant at all, which is why an empty Variant is the
// correct value there rather than a default.
type Optimizer string

const (
	// OptimizerMayfly is the default engine and the only one every entry point
	// supports. An empty optimizer resolves to it, so configurations and
	// checkpoints written before this field existed keep their meaning.
	OptimizerMayfly Optimizer = "mayfly"
	// OptimizerDragonfly runs the continuous Dragonfly Algorithm. It is a
	// proof of concept: see docs/known-limitations.md for what it does not
	// support.
	OptimizerDragonfly Optimizer = "dragonfly"
	// OptimizerCMAES runs covariance matrix adaptation. Its algorithm-specific
	// settings live beside JobConfig because CLI, server, schedules, and
	// checkpoints must all agree on them.
	OptimizerCMAES Optimizer = "cmaes"
)

// SupportedOptimizers returns the engines a JobConfig may select, in the order
// they are reported to the caller. It must stay in step with the engines
// internal/opt can construct; internal/opt owns a contract test that fails if
// the two drift apart, because app is dependency-free and cannot import it.
func SupportedOptimizers() []Optimizer {
	return []Optimizer{
		OptimizerMayfly,
		OptimizerDragonfly,
		OptimizerCMAES,
	}
}

// ResolvedOptimizer reports the engine this configuration runs, treating an
// empty value as MayFly.
//
// Every consumer needs that fallback, because a checkpoint written before the
// field existed carries no engine and must resume exactly as it did. Reading
// the field directly is the one way to run the wrong optimizer, so nothing
// outside this package should.
func (c *JobConfig) ResolvedOptimizer() Optimizer {
	if c.Optimizer == "" {
		return OptimizerMayfly
	}

	return c.Optimizer
}

// ResolvedPolishingOptimizer reports the engine a polishing sweep searches its
// active set with, treating an empty value as MayFly.
//
// The fallback is the same one ResolvedOptimizer needs and it exists for the
// same reason: every checkpoint written before the field existed carries no
// polishing engine, and must keep running the standard-variant MayFly polisher
// the measurements in docs/polishing-budget-report.md and
// docs/contiguous-window-polish-report.md describe.
func (c *JobConfig) ResolvedPolishingOptimizer() Optimizer {
	if c.PolishingOptimizer == "" {
		return OptimizerMayfly
	}

	return c.PolishingOptimizer
}

// ResolvedPolishingSigma reports the seeded perturbation width a polishing
// sweep searches at, treating zero as the recorded default.
func (c *JobConfig) ResolvedPolishingSigma() float64 {
	if c.PolishingSigma == 0 {
		return DefaultPolishingSigma
	}

	return c.PolishingSigma
}

// engineOnlyField pairs a setting only one engine reads with the JSON field
// that carries it, so one list drives the refusal. Both mayflyOnlyFields and
// cmaesOnlyFields build such a list; the type names the shape, not the engine.
type engineOnlyField struct {
	field string
	// detail explains a restriction naming the owning engine does not, and is
	// appended to the refusal. It stays empty where the owner is the whole
	// explanation: a variant belongs to MayFly because only MayFly has
	// variants, and a reader who is told that knows what to do about it.
	detail string
	set    bool
}

// mayflyOnlyFields lists the settings only the MayFly engine reads, in a fixed
// order, so a configuration setting more than one always reports the same one
// first. The three advanced knobs come from advancedKnobs, so this list and the
// range check cannot disagree about what they are called.
//
// Polishing is deliberately not on the list. A sweep names its own engine
// through polishingOptimizer, which validatePolishingEngine checks separately,
// so a CMA-ES base stage may enable polishing; the remaining polishing fields
// are inert without polishingEnabled and are defaulted for every job, so
// refusing them would reject configurations nobody wrote.
func (c *JobConfig) mayflyOnlyFields() []engineOnlyField {
	fields := []engineOnlyField{
		{field: "variant", set: c.Variant != ""},
		{field: "qmcInit", set: c.QMCInit != ""},
		{field: "crossoverCount", set: c.CrossoverCount != 0},
	}

	for _, knob := range c.advancedKnobs() {
		fields = append(fields, engineOnlyField{field: knob.field, set: knob.value != nil})
	}

	return fields
}

// validatePolishingEngine checks the engine a sweep names, independently of the
// engine the base stage runs.
//
// Dragonfly is refused rather than merely unmeasured. The adapter is a proof of
// concept that loses all twelve blocks to MayFly in every arm of
// docs/dragonfly-poc-report.md, so offering it here would add a configuration
// surface, a cost projection and a checkpoint value for a stage nothing has
// shown it could serve.
func (c *JobConfig) validatePolishingEngine() error {
	switch c.ResolvedPolishingOptimizer() {
	case OptimizerMayfly, OptimizerCMAES:
		return nil
	case OptimizerDragonfly:
		return invalid("polishingOptimizer", `must be one of "mayfly", "cmaes"; `+
			`the "dragonfly" adapter is a proof of concept and does not polish`)
	}

	return invalid("polishingOptimizer", `must be one of "mayfly", "cmaes"`)
}

// validateOptimizerEngine rejects an unknown engine, and refuses the settings
// an engine other than MayFly cannot honor.
//
// Rejecting rather than ignoring them is the point, and it is the same rule
// validateAOBLMOAOnlyKnobs applies within MayFly: a Dragonfly job that carried
// a variant would otherwise be accepted, persisted into a checkpoint and
// reported back unchanged while never reaching the optimizer, which makes
// every cost it produces impossible to compare.
func (c *JobConfig) validateOptimizerEngine() error {
	supported := SupportedOptimizers()
	if !slices.Contains(supported, c.ResolvedOptimizer()) {
		names := make([]string, len(supported))
		for i, optimizer := range supported {
			names[i] = string(optimizer)
		}

		return invalid("optimizer", "must be one of "+strings.Join(names, ", "))
	}

	if c.ResolvedOptimizer() == OptimizerMayfly {
		err := c.refuseCMAESOnlyFields()
		if err != nil {
			return err
		}

		return c.validateVariant()
	}

	for _, field := range c.mayflyOnlyFields() {
		if !field.set {
			continue
		}

		return engineOnlyFieldError(field, OptimizerMayfly, c.ResolvedOptimizer())
	}

	if c.ResolvedOptimizer() == OptimizerCMAES {
		return c.validateCMAESConfig()
	}

	return c.refuseCMAESOnlyFields()
}

func engineOnlyFieldError(field engineOnlyField, owner, running Optimizer) error {
	reason := fmt.Sprintf("is read only by the %q optimizer, but this job runs %q", owner, running)
	if field.detail != "" {
		reason += "; " + field.detail
	}

	return invalid(field.field, reason)
}

// validateVariant enforces the MayFly variant set. It runs only for MayFly
// jobs, because a Dragonfly job legitimately has no variant.
func (c *JobConfig) validateVariant() error {
	if slices.Contains(variants, c.Variant) {
		return nil
	}

	names := make([]string, len(variants))
	for i, variant := range variants {
		names[i] = string(variant)
	}

	return invalid("variant", "must be one of "+strings.Join(names, ", "))
}
