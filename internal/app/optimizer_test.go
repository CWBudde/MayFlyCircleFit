package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
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

	empty := app.JobConfig{}

	if got := empty.ResolvedOptimizer(); got != app.OptimizerMayfly {
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

// TestPolishingIsReachableFromEveryBaseEngine pins the rule that replaced the
// MayFly-only refusal. A sweep names its own engine through polishingOptimizer,
// so the engine the base stage runs no longer decides whether the job may
// polish at all. An unset field still resolves to MayFly, which is what keeps
// every checkpoint and every recorded polishing figure describing the stage
// that ran.
func TestPolishingIsReachableFromEveryBaseEngine(t *testing.T) {
	t.Parallel()

	for _, engine := range []app.Optimizer{app.OptimizerCMAES, app.OptimizerDragonfly} {
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()

			config := engineBaseConfig(t, engine)
			config.Mode = app.ModeBatch
			config.PolishingEnabled = true

			err := config.Validate()
			if err != nil {
				t.Fatalf("Validate() rejected polishing under %q: %v", engine, err)
			}

			if got := config.ResolvedPolishingOptimizer(); got != app.OptimizerMayfly {
				t.Errorf("ResolvedPolishingOptimizer() = %q, want %q", got, app.OptimizerMayfly)
			}
		})
	}
}

// TestPolishingEngineRefusesDragonfly pins the one engine a sweep may not name.
// The adapter loses every block in docs/dragonfly-poc-report.md, so the refusal
// says that rather than only naming the field, or the decision reads as wiring
// nobody has got to yet.
func TestPolishingEngineRefusesDragonfly(t *testing.T) {
	t.Parallel()

	config := engineBaseConfig(t, app.OptimizerMayfly)
	config.Mode = app.ModeBatch
	config.PolishingEnabled = true
	config.PolishingOptimizer = app.OptimizerDragonfly

	err := config.Validate()
	assertInvalidField(t, err, "polishingOptimizer")

	for _, want := range []string{"proof of concept", "does not polish"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// TestPolishingEngineAcceptsCMAESUnderACMAESBase is the case the change exists
// for: the block-coordinate sweep reachable from the engine that holds the
// record.
func TestPolishingEngineAcceptsCMAESUnderACMAESBase(t *testing.T) {
	t.Parallel()

	config := engineBaseConfig(t, app.OptimizerCMAES)
	config.Mode = app.ModeBatch
	config.PolishingEnabled = true
	config.PolishingOptimizer = app.OptimizerCMAES

	err := config.Validate()
	if err != nil {
		t.Fatalf("Validate() rejected a CMA-ES polisher under a CMA-ES base: %v", err)
	}
}

// TestEngineOnlyRefusalsStayBrief pins the shape of an engine-only refusal now
// that none of them carries a detail: the message names the field's owner and
// stops. It is the counterweight to the polishing tests above -- polishing left
// this list, and no explanation may leak onto the refusals that remain.
func TestEngineOnlyRefusalsStayBrief(t *testing.T) {
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

// TestScheduleAcceptsAPolishStepUnderCMAES is the campaign-level half of the
// change: a document may now alternate extend and polish stages under the
// engine that holds the record. ParseSchedule expands the document to validate
// it, so acceptance here means every stage validated, not only the base.
func TestScheduleAcceptsAPolishStepUnderCMAES(t *testing.T) {
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
    "optimizer": "cmaes"
  },
  "steps": [
    {"type": "extend", "additionalCircles": 1},
    {"type": "polish", "polishOptimizer": "cmaes"}
  ]
}`

	parsed, err := app.ParseSchedule([]byte(document))
	if err != nil {
		t.Fatalf("app.ParseSchedule() rejected a polish step under cmaes: %v", err)
	}

	stages, err := parsed.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	polish := stages[len(stages)-1]
	if polish.Kind != app.ScheduleStagePolish {
		t.Fatalf("last stage kind = %q, want %q", polish.Kind, app.ScheduleStagePolish)
	}

	if got := polish.Config.ResolvedPolishingOptimizer(); got != app.OptimizerCMAES {
		t.Errorf("ResolvedPolishingOptimizer() = %q, want %q", got, app.OptimizerCMAES)
	}
}
