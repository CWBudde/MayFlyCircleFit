package app_test

import (
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// TestValidateAcceptsEveryQMCInit keeps the accepted set and the constants in
// step: a strategy app exports but refuses would be unreachable, and one it
// accepts without internal/opt being able to configure it is caught by the
// contract test in that package.
func TestValidateAcceptsEveryQMCInit(t *testing.T) {
	t.Parallel()

	for _, init := range app.SupportedQMCInits() {
		t.Run(string(init), func(t *testing.T) {
			t.Parallel()

			config := mayflyQMCConfig(t, init)

			err := config.Validate()
			if err != nil {
				t.Fatalf("Validate() error = %v, want %q to be accepted", err, init)
			}
		})
	}
}

func TestValidateRejectsAnUnknownQMCInit(t *testing.T) {
	t.Parallel()

	assertInvalidField(t, mayflyQMCConfig(t, "hammersley").Validate(), fieldQMCInit)
}

// TestResolvedQMCInitTreatsAnEmptyStrategyAsUniform covers the value a
// checkpoint predating the field decodes with. Resume reads a persisted
// configuration directly, without ApplyDefaults in the way, so the fallback
// has to live on the accessor rather than in defaulting.
func TestResolvedQMCInitTreatsAnEmptyStrategyAsUniform(t *testing.T) {
	t.Parallel()

	if got := (app.JobConfig{}).ResolvedQMCInit(); got != app.QMCInitUniform {
		t.Errorf("ResolvedQMCInit() = %q, want %q", got, app.QMCInitUniform)
	}
}

// TestApplyDefaultsLeavesTheQMCInitEmpty pins that defaulting does not write
// the field. An absent strategy has to stay absent so a Dragonfly job never
// inherits a MayFly-only setting that validation would then refuse.
func TestApplyDefaultsLeavesTheQMCInitEmpty(t *testing.T) {
	t.Parallel()

	config := app.JobConfig{RefPath: referenceImage}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults() error = %v", err)
	}

	if config.QMCInit != "" {
		t.Errorf("app.QMCInit = %q, want it left empty", config.QMCInit)
	}
}

func TestSupportedQMCInitsListsEveryConstantOnce(t *testing.T) {
	t.Parallel()

	supported := app.SupportedQMCInits()
	want := []app.QMCInit{app.QMCInitUniform, app.QMCInitSobol, app.QMCInitHalton}

	if len(supported) != len(want) {
		t.Fatalf("app.SupportedQMCInits() = %v, want %v", supported, want)
	}

	for i, init := range want {
		if supported[i] != init {
			t.Errorf("app.SupportedQMCInits()[%d] = %q, want %q", i, supported[i], init)
		}
	}
}

// mayflyQMCConfig is a valid MayFly configuration carrying one strategy.
func mayflyQMCConfig(t *testing.T, init app.QMCInit) app.JobConfig {
	t.Helper()

	config := app.JobConfig{RefPath: referenceImage, Optimizer: app.OptimizerMayfly}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults() error = %v", err)
	}

	config.QMCInit = init

	return config
}
