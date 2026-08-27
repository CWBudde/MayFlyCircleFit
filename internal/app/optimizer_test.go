package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// referenceImage is the reference path every configuration under test names.
// The file never has to exist: Validate checks the field, not the filesystem.
const (
	referenceImage             = "ref.png"
	fieldVariant               = "variant"
	fieldQMCInit               = "qmcInit"
	fieldDanceDamp             = "danceDamp"
	fieldAquilaWeight          = "aquilaWeight"
	fieldOppositionProbability = "oppositionProbability"
	fieldPolishingEnabled      = "polishingEnabled"
)

// TestApplyDefaultsResolvesTheEngine pins the backward-compatible default: a
// configuration or checkpoint written before the optimizer field existed
// carries no engine and must keep running MayFly with the standard variant.
func TestApplyDefaultsResolvesTheEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		optimizer   app.Optimizer
		wantEngine  app.Optimizer
		wantVariant app.Variant
	}{
		{name: "absent", optimizer: "", wantEngine: app.OptimizerMayfly, wantVariant: app.VariantStandard},
		{name: "mayfly", optimizer: app.OptimizerMayfly, wantEngine: app.OptimizerMayfly, wantVariant: app.VariantStandard},
		// Only MayFly has variants, so a Dragonfly job must not inherit one.
		{name: "dragonfly", optimizer: app.OptimizerDragonfly, wantEngine: app.OptimizerDragonfly, wantVariant: ""},
		{name: "cmaes", optimizer: app.OptimizerCMAES, wantEngine: app.OptimizerCMAES, wantVariant: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			config := app.JobConfig{RefPath: referenceImage, Optimizer: test.optimizer}

			err := config.ApplyDefaults()
			if err != nil {
				t.Fatalf("ApplyDefaults() error = %v", err)
			}

			if config.Optimizer != test.wantEngine {
				t.Errorf("app.Optimizer = %q, want %q", config.Optimizer, test.wantEngine)
			}

			if config.Variant != test.wantVariant {
				t.Errorf("app.Variant = %q, want %q", config.Variant, test.wantVariant)
			}

			if config.ResolvedOptimizer() != test.wantEngine {
				t.Errorf("ResolvedOptimizer() = %q, want %q", config.ResolvedOptimizer(), test.wantEngine)
			}

			err = config.Validate()
			if err != nil {
				t.Errorf("Validate() error = %v", err)
			}
		})
	}
}

// TestResolvedOptimizerTreatsAnEmptyEngineAsMayfly covers the value a
// checkpoint predating the field decodes with, without ApplyDefaults in the
// way: resume reads a persisted configuration directly.
func TestResolvedOptimizerTreatsAnEmptyEngineAsMayfly(t *testing.T) {
	t.Parallel()

	if got := (app.JobConfig{}).ResolvedOptimizer(); got != app.OptimizerMayfly {
		t.Errorf("ResolvedOptimizer() = %q, want %q", got, app.OptimizerMayfly)
	}
}

func TestValidateRejectsAnUnknownOptimizer(t *testing.T) {
	t.Parallel()

	config := dragonflyBaseConfig(t)
	config.Optimizer = "swarm"

	assertInvalidField(t, config.Validate(), "optimizer")
}

// TestValidateRefusesMayflyOnlyFieldsUnderDragonfly is the point of the
// engine field: a setting the running optimizer cannot read has to be refused,
// not persisted and reported back while never reaching the optimizer.
func TestValidateRefusesMayflyOnlyFieldsUnderDragonfly(t *testing.T) {
	t.Parallel()

	weight := 0.5

	tests := []struct {
		name  string
		apply func(*app.JobConfig)
		field string
	}{
		{fieldVariant, func(c *app.JobConfig) { c.Variant = app.VariantDESMA }, fieldVariant},
		{fieldQMCInit, func(c *app.JobConfig) { c.QMCInit = app.QMCInitSobol }, fieldQMCInit},
		{"crossoverCount", func(c *app.JobConfig) { c.CrossoverCount = 40 }, "crossoverCount"},
		{fieldDanceDamp, func(c *app.JobConfig) { c.DanceDamp = &weight }, fieldDanceDamp},
		{fieldAquilaWeight, func(c *app.JobConfig) { c.AquilaWeight = &weight }, fieldAquilaWeight},
		{
			fieldOppositionProbability,
			func(c *app.JobConfig) { c.OppositionProbability = &weight },
			fieldOppositionProbability,
		},
		{fieldPolishingEnabled, func(c *app.JobConfig) {
			c.Mode = app.ModeBatch
			c.PolishingEnabled = true
		}, fieldPolishingEnabled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			config := dragonflyBaseConfig(t)
			test.apply(&config)

			assertInvalidField(t, config.Validate(), test.field)
		})
	}
}

// TestPolishingRefusalExplainsTheRestriction pins the second half of the
// polishing message. Naming the owner is the whole story for a variant, which
// exists only inside MayFly; polishing is a stage any engine could plausibly
// grow, so an owner alone reads as wiring nobody has got to yet. The refusal
// has to say that a sweep runs its own MayFly population and what to do
// instead, or the decision recorded in docs/behavior-invariants.md is invisible
// at the one place it is enforced.
func TestPolishingRefusalExplainsTheRestriction(t *testing.T) {
	t.Parallel()

	for _, engine := range []app.Optimizer{app.OptimizerCMAES, app.OptimizerDragonfly} {
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()

			config := engineBaseConfig(t, engine)
			config.Mode = app.ModeBatch
			config.PolishingEnabled = true

			err := config.Validate()
			assertInvalidField(t, err, fieldPolishingEnabled)

			wants := []string{
				"own MayFly population",
				"decision rather than a missing feature",
				string(engine),
			}
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// TestEngineOnlyRefusalsWithoutPolishingStayBrief is the counterweight to the
// test above: only polishing carries an explanation. Every other engine-only
// field is refused by naming its owner and stopping, so the detail cannot leak
// onto refusals it does not describe.
func TestEngineOnlyRefusalsWithoutPolishingStayBrief(t *testing.T) {
	t.Parallel()

	config := dragonflyBaseConfig(t)
	config.Variant = app.VariantDESMA

	err := config.Validate()
	assertInvalidField(t, err, fieldVariant)

	if strings.Contains(err.Error(), ";") {
		t.Errorf("error %q carries an explanation, want the owning engine alone", err)
	}
}

// TestValidateAcceptsParallelEvaluationUnderDragonfly guards the one knob that
// is not MayFly-only: the adapter implements concurrent evaluation, so a
// campaign can be configured the same way for both engines.
func TestValidateAcceptsParallelEvaluationUnderDragonfly(t *testing.T) {
	t.Parallel()

	config := dragonflyBaseConfig(t)
	config.ParallelEvaluation = true
	config.EvaluationWorkers = 8

	err := config.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v, want parallel evaluation to be accepted", err)
	}
}

// TestValidateStillEnforcesTheVariantSetForMayfly keeps the pre-existing rule
// intact now that the engine owns the variant check.
func TestValidateStillEnforcesTheVariantSetForMayfly(t *testing.T) {
	t.Parallel()

	config := dragonflyBaseConfig(t)
	config.Optimizer = app.OptimizerMayfly
	config.Variant = "swarm"

	assertInvalidField(t, config.Validate(), fieldVariant)
}

func TestSupportedOptimizersListsEveryConstantOnce(t *testing.T) {
	t.Parallel()

	supported := app.SupportedOptimizers()
	want := []app.Optimizer{app.OptimizerMayfly, app.OptimizerDragonfly, app.OptimizerCMAES}

	if len(supported) != len(want) {
		t.Fatalf("app.SupportedOptimizers() = %v, want %v", supported, want)
	}

	for i, optimizer := range want {
		if supported[i] != optimizer {
			t.Errorf("app.SupportedOptimizers()[%d] = %q, want %q", i, supported[i], optimizer)
		}
	}
}

// dragonflyBaseConfig is a valid Dragonfly configuration with defaults applied.
func dragonflyBaseConfig(t *testing.T) app.JobConfig {
	t.Helper()

	return engineBaseConfig(t, app.OptimizerDragonfly)
}

// engineBaseConfig is a valid configuration for the named engine with defaults
// applied, so a test can set exactly the one field it is about.
func engineBaseConfig(t *testing.T, engine app.Optimizer) app.JobConfig {
	t.Helper()

	config := app.JobConfig{RefPath: referenceImage, Optimizer: engine}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults() error = %v", err)
	}

	return config
}

func assertInvalidField(t *testing.T, err error, field string) {
	t.Helper()

	if err == nil {
		t.Fatalf("Validate() accepted a configuration that should fail on %q", field)
	}

	var validation *app.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Validate() error = %v (%T), want a app.ValidationError", err, err)
	}

	if validation.Field != field {
		t.Errorf("Validate() reported field %q, want %q", validation.Field, field)
	}

	if !strings.Contains(err.Error(), field) {
		t.Errorf("error %q does not name the field", err)
	}
}

// TestScheduleCarriesTheEngineToEveryStage covers the campaign surface: the
// base names the engine once and every extend stage inherits it, so a campaign
// cannot silently change optimizer halfway through.
func TestScheduleCarriesTheEngineToEveryStage(t *testing.T) {
	t.Parallel()

	document := `{
  "schemaVersion": 1,
  "seed": 4242,
  "base": {
    "refPath": "assets/ref.png",
    "mode": "batch",
    "circles": 8,
    "batchSize": 8,
    "iters": 200,
    "popSize": 30,
    "optimizer": "dragonfly"
  },
  "steps": [{"type": "extend", "additionalCircles": 8, "repeat": 2}]
}`

	parsed, err := app.ParseSchedule([]byte(document))
	if err != nil {
		t.Fatalf("app.ParseSchedule() error = %v", err)
	}

	stages, err := parsed.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if len(stages) != 3 {
		t.Fatalf("stages = %d, want 3", len(stages))
	}

	for _, stage := range stages {
		if stage.Config.ResolvedOptimizer() != app.OptimizerDragonfly {
			t.Errorf("stage %d runs %q, want %q", stage.Index, stage.Config.ResolvedOptimizer(), app.OptimizerDragonfly)
		}

		if stage.Config.Variant != "" {
			t.Errorf("stage %d carries variant %q, want none", stage.Index, stage.Config.Variant)
		}
	}
}

// TestScheduleRefusesAPolishStageUnderDragonfly is the campaign-level half of
// the polishing refusal: polishing runs its own MayFly population, so a
// document that asks for one under another engine has to fail at parse time
// rather than quietly running MayFly for those stages.
func TestScheduleRefusesAPolishStageUnderDragonfly(t *testing.T) {
	t.Parallel()

	document := `{
  "schemaVersion": 1,
  "seed": 4242,
  "base": {
    "refPath": "assets/ref.png",
    "mode": "batch",
    "circles": 8,
    "batchSize": 8,
    "iters": 200,
    "popSize": 30,
    "optimizer": "dragonfly"
  },
  "steps": [{"type": "polish"}]
}`

	// app.ParseSchedule expands the document to validate it, so the refusal
	// arrives before a campaign is ever queued.
	_, err := app.ParseSchedule([]byte(document))
	if err == nil {
		t.Fatal("app.ParseSchedule() accepted a polish stage under an engine that cannot polish")
	}

	if !strings.Contains(err.Error(), fieldPolishingEnabled) {
		t.Errorf("error = %v, want it to name polishingEnabled", err)
	}

	if !strings.Contains(err.Error(), "dragonfly") {
		t.Errorf("error = %v, want it to name the engine that runs", err)
	}
}
