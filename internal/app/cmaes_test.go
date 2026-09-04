package app_test

import (
	"math"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
)

const (
	fieldInitialSigma   = "initialSigma"
	fieldCovarianceMode = "covarianceMode"
)

func TestCMAESDefaultsAndExplicitFalse(t *testing.T) {
	t.Parallel()

	config := app.JobConfig{RefPath: referenceImage, Optimizer: app.OptimizerCMAES}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults() error = %v", err)
	}

	if config.ResolvedCMAESInitialSigma() != app.DefaultCMAESInitialSigma {
		t.Errorf("initial sigma = %v, want %v", config.ResolvedCMAESInitialSigma(), app.DefaultCMAESInitialSigma)
	}

	if config.ResolvedCMAESCovarianceMode() != app.CMAESCovarianceFull {
		t.Errorf("covariance mode = %q, want full", config.ResolvedCMAESCovarianceMode())
	}

	if !config.ResolvedCMAESActive() {
		t.Error("active CMA defaulted off, want on")
	}

	if config.ResolvedCMAESRestartStrategy() != app.CMAESRestartNone {
		t.Errorf("restart strategy = %q, want none", config.ResolvedCMAESRestartStrategy())
	}

	active := false

	config.ActiveCMA = &active
	if config.ResolvedCMAESActive() {
		t.Error("explicit activeCMA=false was replaced by the default")
	}

	err = config.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestCMAESValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		apply func(*app.JobConfig)
	}{
		{name: "zero sigma", field: fieldInitialSigma, apply: func(c *app.JobConfig) {
			value := 0.0
			c.InitialSigma = &value
		}},
		{name: "NaN sigma", field: fieldInitialSigma, apply: func(c *app.JobConfig) {
			value := math.NaN()
			c.InitialSigma = &value
		}},
		{name: "covariance", field: fieldCovarianceMode, apply: func(c *app.JobConfig) {
			c.CovarianceMode = "sparse"
		}},
		{name: "restart", field: "restartStrategy", apply: func(c *app.JobConfig) { c.RestartStrategy = "random" }},
		{name: "nested restarts", field: "optimizerRestarts", apply: func(c *app.JobConfig) {
			c.RestartStrategy = app.CMAESRestartIPOP
			c.OptimizerRestarts = 2
		}},
		// A budget-filling cap around a ladder that already restarts is refused
		// for the same reason a fixed outer count is; the guard tests the value
		// and not its magnitude on purpose.
		{name: "filling cap around ipop", field: "optimizerRestarts", apply: func(c *app.JobConfig) {
			c.RestartStrategy = app.CMAESRestartIPOP
			c.OptimizerRestarts = -4
		}},
		{name: "filling cap around bipop", field: "optimizerRestarts", apply: func(c *app.JobConfig) {
			c.RestartStrategy = app.CMAESRestartBIPOP
			c.OptimizerRestarts = -1
		}},
		{name: "dense dimensions", field: fieldCovarianceMode, apply: func(c *app.JobConfig) {
			c.Circles = app.MaxCMAESFullDimensions/app.ParametersPerCircle + 1
			c.BatchSize = c.Circles
		}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			config := cmaesBaseConfig(t)
			testCase.apply(&config)
			assertInvalidField(t, config.Validate(), testCase.field)
		})
	}
}

func TestCMAESScalableCovarianceModesAcceptLargeProblems(t *testing.T) {
	t.Parallel()

	for _, mode := range []app.CMAESCovarianceMode{
		app.CMAESCovarianceBlock,
		app.CMAESCovarianceSeparable,
	} {
		config := cmaesBaseConfig(t)
		config.Circles = app.MaxCMAESFullDimensions/app.ParametersPerCircle + 1
		config.BatchSize = config.Circles
		config.CovarianceMode = mode

		err := config.Validate()
		if err != nil {
			t.Errorf("Validate(%q) error = %v", mode, err)
		}
	}
}

func TestCMAESEngineSpecificFieldsAreRefusedElsewhere(t *testing.T) {
	t.Parallel()

	sigma := 0.2
	active := false
	tests := []struct {
		field string
		apply func(*app.JobConfig)
	}{
		{field: fieldInitialSigma, apply: func(c *app.JobConfig) { c.InitialSigma = &sigma }},
		{field: "activeCMA", apply: func(c *app.JobConfig) { c.ActiveCMA = &active }},
		{field: fieldCovarianceMode, apply: func(c *app.JobConfig) { c.CovarianceMode = app.CMAESCovarianceBlock }},
		{field: "restartStrategy", apply: func(c *app.JobConfig) { c.RestartStrategy = app.CMAESRestartBIPOP }},
	}

	for _, optimizer := range []app.Optimizer{app.OptimizerMayfly, app.OptimizerDragonfly} {
		for _, testCase := range tests {
			config := app.JobConfig{RefPath: referenceImage, Optimizer: optimizer}

			err := config.ApplyDefaults()
			if err != nil {
				t.Fatalf("ApplyDefaults(%q) error = %v", optimizer, err)
			}

			testCase.apply(&config)
			assertInvalidField(t, config.Validate(), testCase.field)
		}
	}
}

func TestCMAESRefusesMayflyOnlySettings(t *testing.T) {
	t.Parallel()

	config := cmaesBaseConfig(t)
	config.Variant = app.VariantStandard
	err := config.Validate()
	assertInvalidField(t, err, fieldVariant)

	if !strings.Contains(err.Error(), string(app.OptimizerCMAES)) {
		t.Errorf("error = %v, want it to name the running CMA-ES engine", err)
	}
}

func cmaesBaseConfig(t *testing.T) app.JobConfig {
	t.Helper()

	config := app.JobConfig{RefPath: referenceImage, Optimizer: app.OptimizerCMAES}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults() error = %v", err)
	}

	return config
}

func TestScheduleCarriesCMAESConfigurationToEveryStage(t *testing.T) {
	t.Parallel()

	document := `{
  "schemaVersion": 1,
  "seed": 4242,
  "base": {
    "refPath": "assets/ref.png",
    "mode": "batch",
    "circles": 8,
    "batchSize": 8,
    "iters": 20,
    "popSize": 20,
    "optimizer": "cmaes",
    "initialSigma": 0.2,
    "covarianceMode": "block",
    "activeCMA": false,
    "restartStrategy": "ipop"
  },
  "steps": [{"type": "extend", "additionalCircles": 8, "repeat": 2}]
}`

	parsed, err := app.ParseSchedule([]byte(document))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	stages, err := parsed.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	for _, stage := range stages {
		config := stage.Config
		if config.ResolvedOptimizer() != app.OptimizerCMAES ||
			config.ResolvedCMAESInitialSigma() != 0.2 ||
			config.ResolvedCMAESCovarianceMode() != app.CMAESCovarianceBlock ||
			config.ResolvedCMAESActive() ||
			config.ResolvedCMAESRestartStrategy() != app.CMAESRestartIPOP {
			t.Errorf("stage %d lost CMA-ES configuration: %+v", stage.Index, config)
		}
	}
}

func TestScheduleRefusesPolishingUnderCMAES(t *testing.T) {
	t.Parallel()

	document := `{
  "schemaVersion": 1,
  "base": {
    "refPath": "assets/ref.png",
    "mode": "batch",
    "circles": 8,
    "batchSize": 8,
    "iters": 20,
    "popSize": 20,
    "optimizer": "cmaes"
  },
  "steps": [{"type": "polish"}]
}`

	_, err := app.ParseSchedule([]byte(document))
	if err == nil || !strings.Contains(err.Error(), fieldPolishingEnabled) {
		t.Fatalf("ParseSchedule() error = %v, want polishing refusal", err)
	}
}
