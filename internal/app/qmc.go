package app

import (
	"slices"
	"strings"
)

// QMCInit names the strategy that draws a MayFly run's initial population.
//
// It is a MayFly-only setting, and it is deliberately not a Variant: every
// variant runs one of these, because the choice is about how the first
// generation samples the search box rather than about the update rules applied
// afterwards.
type QMCInit string

const (
	// QMCInitUniform draws every coordinate independently from the run's
	// generator. It is the historical behavior and the default, so an empty
	// value resolves to it and every configuration and checkpoint written
	// before this field existed keeps its meaning.
	QMCInitUniform QMCInit = "uniform"
	// QMCInitSobol seeds the first generation from a scrambled Sobol
	// sequence. It is the one strategy the library has evidence for, and the
	// evidence is thin: two significant results in sixteen benchmark
	// problems, which is close to what chance produces. See
	// docs/qmc-initialization.md in the MayFly repository.
	QMCInitSobol QMCInit = "sobol"
	// QMCInitHalton seeds the first generation from a scrambled Halton
	// sequence. Nothing in the library's study distinguishes it from uniform;
	// it is offered because it has no dimension ceiling and Sobol does.
	QMCInitHalton QMCInit = "halton"
)

// SupportedQMCInits returns the initial-population strategies a JobConfig may
// select, in the order they are reported to the caller. It must stay in step
// with what internal/opt can configure; internal/opt owns a contract test that
// fails if the two drift apart, because app is dependency-free and cannot
// import it.
func SupportedQMCInits() []QMCInit {
	return []QMCInit{
		QMCInitUniform,
		QMCInitSobol,
		QMCInitHalton,
	}
}

// ResolvedQMCInit reports the strategy this configuration runs, treating an
// empty value as uniform.
//
// Every consumer needs that fallback for the same reason ResolvedOptimizer
// does: a checkpoint written before the field existed carries no strategy and
// must resume exactly as it did.
func (c JobConfig) ResolvedQMCInit() QMCInit {
	if c.QMCInit == "" {
		return QMCInitUniform
	}

	return c.QMCInit
}

// validateQMCInit enforces the strategy set. It runs only for MayFly jobs,
// because mayflyOnlyFields has already refused the field for any other engine.
func (c JobConfig) validateQMCInit() error {
	supported := SupportedQMCInits()
	if slices.Contains(supported, c.ResolvedQMCInit()) {
		return nil
	}

	names := make([]string, len(supported))
	for i, init := range supported {
		names[i] = string(init)
	}

	return invalid("qmcInit", "must be one of "+strings.Join(names, ", "))
}
