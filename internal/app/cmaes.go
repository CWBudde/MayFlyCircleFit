package app

import (
	"fmt"
	"math"
)

const (
	// DefaultCMAESInitialSigma is expressed in the adapter's normalized [0,1]
	// search box, so it has the same meaning for every circle parameter.
	DefaultCMAESInitialSigma = 0.3
	// MaxCMAESFullDimensions bounds both the quadratic covariance storage and
	// the cubic eigendecomposition. Larger searches must use block or
	// separable covariance.
	MaxCMAESFullDimensions = 512
)

// CMAESCovarianceMode selects the covariance representation learned by CMA-ES.
type CMAESCovarianceMode string

const (
	CMAESCovarianceFull      CMAESCovarianceMode = "full"
	CMAESCovarianceSeparable CMAESCovarianceMode = "separable"
	CMAESCovarianceBlock     CMAESCovarianceMode = "block"
)

// CMAESRestartStrategy selects CMA-ES's shared-budget restart schedule.
type CMAESRestartStrategy string

const (
	CMAESRestartNone  CMAESRestartStrategy = "none"
	CMAESRestartIPOP  CMAESRestartStrategy = "ipop"
	CMAESRestartBIPOP CMAESRestartStrategy = "bipop"
)

// ResolvedCMAESInitialSigma returns the configured normalized initial step.
func (c *JobConfig) ResolvedCMAESInitialSigma() float64 {
	if c.InitialSigma == nil {
		return DefaultCMAESInitialSigma
	}

	return *c.InitialSigma
}

// ResolvedCMAESActive reports whether negative rank-mu adaptation is enabled.
func (c *JobConfig) ResolvedCMAESActive() bool {
	if c.ActiveCMA == nil {
		return true
	}

	return *c.ActiveCMA
}

// ResolvedCMAESCovarianceMode returns the default dense representation for an
// omitted mode.
func (c *JobConfig) ResolvedCMAESCovarianceMode() CMAESCovarianceMode {
	if c.CovarianceMode == "" {
		return CMAESCovarianceFull
	}

	return c.CovarianceMode
}

// ResolvedCMAESRestartStrategy returns the single-run default for an omitted
// restart strategy.
func (c *JobConfig) ResolvedCMAESRestartStrategy() CMAESRestartStrategy {
	if c.RestartStrategy == "" {
		return CMAESRestartNone
	}

	return c.RestartStrategy
}

func (c *JobConfig) cmaesOnlyFields() []engineOnlyField {
	return []engineOnlyField{
		{field: "initialSigma", set: c.InitialSigma != nil},
		{field: "covarianceMode", set: c.CovarianceMode != ""},
		{field: "activeCMA", set: c.ActiveCMA != nil},
		{field: "restartStrategy", set: c.RestartStrategy != ""},
	}
}

func (c *JobConfig) refuseCMAESOnlyFields() error {
	for _, field := range c.cmaesOnlyFields() {
		if field.set {
			return engineOnlyFieldError(field, OptimizerCMAES, c.ResolvedOptimizer())
		}
	}

	return nil
}

func (c *JobConfig) validateCMAESConfig() error {
	err := c.validateCMAESInitialSigma()
	if err != nil {
		return err
	}

	err = c.validateCMAESCovariance()
	if err != nil {
		return err
	}

	return c.validateCMAESRestarts()
}

func (c *JobConfig) validateCMAESInitialSigma() error {
	sigma := c.ResolvedCMAESInitialSigma()
	if math.IsNaN(sigma) || math.IsInf(sigma, 0) || sigma <= 0 {
		return invalid("initialSigma", "must be finite and positive")
	}

	return nil
}

func (c *JobConfig) validateCMAESCovariance() error {
	mode := c.ResolvedCMAESCovarianceMode()
	switch mode {
	case CMAESCovarianceFull, CMAESCovarianceSeparable, CMAESCovarianceBlock:
	default:
		return invalid("covarianceMode", "must be one of full, separable, block")
	}

	if mode == CMAESCovarianceFull && c.optimizerDimensions() > MaxCMAESFullDimensions {
		return invalid("covarianceMode", fmt.Sprintf(
			"full covariance supports at most %d optimizer dimensions; use block or separable",
			MaxCMAESFullDimensions))
	}

	return nil
}

func (c *JobConfig) validateCMAESRestarts() error {
	strategy := c.ResolvedCMAESRestartStrategy()
	switch strategy {
	case CMAESRestartNone, CMAESRestartIPOP, CMAESRestartBIPOP:
	default:
		return invalid("restartStrategy", "must be one of none, ipop, bipop")
	}

	if strategy != CMAESRestartNone && c.OptimizerRestarts != 1 {
		return invalid("optimizerRestarts", "must be 1 when CMA-ES restartStrategy is ipop or bipop")
	}

	return nil
}
