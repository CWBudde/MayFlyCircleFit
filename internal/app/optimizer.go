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
func (c JobConfig) ResolvedOptimizer() Optimizer {
	if c.Optimizer == "" {
		return OptimizerMayfly
	}

	return c.Optimizer
}

// mayflyOnlyField pairs a MayFly-only setting with the JSON field that carries
// it, so one list drives the refusal.
type mayflyOnlyField struct {
	field string
	set   bool
}

// mayflyOnlyFields lists the settings only the MayFly engine reads, in a fixed
// order, so a configuration setting more than one always reports the same one
// first. The three advanced knobs come from advancedKnobs, so this list and the
// range check cannot disagree about what they are called.
//
// Polishing is on the list because the polishing stage runs its own MayFly
// population by construction; the remaining polishing fields are inert without
// polishingEnabled and are defaulted for every job, so refusing them would
// reject configurations nobody wrote.
func (c JobConfig) mayflyOnlyFields() []mayflyOnlyField {
	fields := []mayflyOnlyField{
		{"variant", c.Variant != ""},
		{"qmcInit", c.QMCInit != ""},
		{"crossoverCount", c.CrossoverCount != 0},
	}

	for _, knob := range c.advancedKnobs() {
		fields = append(fields, mayflyOnlyField{knob.field, knob.value != nil})
	}

	return append(fields, mayflyOnlyField{"polishingEnabled", c.PolishingEnabled})
}

// validateOptimizerEngine rejects an unknown engine, and refuses the settings
// an engine other than MayFly cannot honor.
//
// Rejecting rather than ignoring them is the point, and it is the same rule
// validateAOBLMOAOnlyKnobs applies within MayFly: a Dragonfly job that carried
// a variant would otherwise be accepted, persisted into a checkpoint and
// reported back unchanged while never reaching the optimizer, which makes
// every cost it produces impossible to compare.
func (c JobConfig) validateOptimizerEngine() error {
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

		return engineOnlyFieldError(field.field, OptimizerMayfly, c.ResolvedOptimizer())
	}

	if c.ResolvedOptimizer() == OptimizerCMAES {
		return c.validateCMAESConfig()
	}

	return c.refuseCMAESOnlyFields()
}

func engineOnlyFieldError(field string, owner, running Optimizer) error {
	return invalid(field, fmt.Sprintf(
		"is read only by the %q optimizer, but this job runs %q", owner, running))
}

// validateVariant enforces the MayFly variant set. It runs only for MayFly
// jobs, because a Dragonfly job legitimately has no variant.
func (c JobConfig) validateVariant() error {
	if slices.Contains(variants, c.Variant) {
		return nil
	}

	names := make([]string, len(variants))
	for i, variant := range variants {
		names[i] = string(variant)
	}

	return invalid("variant", "must be one of "+strings.Join(names, ", "))
}
