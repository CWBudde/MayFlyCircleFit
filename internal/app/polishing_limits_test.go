package app_test

import (
	"math"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
)

// TestPolishingSigmaRejectsNaN pins the one value the range check cannot see.
// Every comparison against NaN is false, so "must be between 0 and 1" would
// accept it and the sweep's continuation profile would carry a NaN width into
// candidate generation, where nothing else looks at it again.
func TestPolishingSigmaRejectsNaN(t *testing.T) {
	t.Parallel()

	config := polishingBaseConfig(t, app.OptimizerMayfly)
	config.PolishingSigma = math.NaN()

	assertInvalidField(t, config.Validate(), "polishingSigma")
}

// TestPolishingSigmaRejectsInfinities keeps the range doing the work it already
// did, so a later reader does not conclude the NaN test replaced it.
func TestPolishingSigmaRejectsInfinities(t *testing.T) {
	t.Parallel()

	for name, sigma := range map[string]float64{
		"positive": math.Inf(1),
		"negative": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := polishingBaseConfig(t, app.OptimizerMayfly)
			config.PolishingSigma = sigma

			assertInvalidField(t, config.Validate(), "polishingSigma")
		})
	}
}

// TestPolishingSigmaAcceptsTheNormalizedBox is the counterweight: the finiteness
// test must not narrow the range the field was given.
func TestPolishingSigmaAcceptsTheNormalizedBox(t *testing.T) {
	t.Parallel()

	for _, sigma := range []float64{0, 0.02, 0.5, 1} {
		config := polishingBaseConfig(t, app.OptimizerMayfly)
		config.PolishingSigma = sigma

		err := config.Validate()
		if err != nil {
			t.Errorf("Validate() rejected polishingSigma %v: %v", sigma, err)
		}
	}
}

// TestCMAESPolishingRefusesAnOversizedActiveSet holds the sweep to the same
// full-covariance limit the base stage is held to.
//
// The sweep pins full covariance, and polishingActiveSetSize is bounded by
// MaxBatchSize rather than by that limit, so without this an active set of 74
// circles and up launches a 518-dimension dense search -- past the size the
// limit exists to refuse, and reachable from a MayFly base stage that never
// touches the CMA-ES covariance check at all.
func TestCMAESPolishingRefusesAnOversizedActiveSet(t *testing.T) {
	t.Parallel()

	oversized := app.MaxCMAESFullDimensions/app.ParametersPerCircle + 1

	for _, base := range []app.Optimizer{app.OptimizerMayfly, app.OptimizerCMAES} {
		t.Run(string(base), func(t *testing.T) {
			t.Parallel()

			config := polishingBaseConfig(t, base)
			config.Circles = oversized
			config.BatchSize = 8
			config.PolishingOptimizer = app.OptimizerCMAES
			config.PolishingActiveSetSize = oversized

			err := config.Validate()
			assertInvalidField(t, err, "polishingActiveSetSize")

			if !strings.Contains(err.Error(), "full covariance") {
				t.Errorf("error %q does not name the covariance mode that forces the limit", err)
			}
		})
	}
}

// TestCMAESPolishingAcceptsTheLargestFittingActiveSet pins the boundary from the
// other side, so the refusal cannot quietly become one circle too strict.
func TestCMAESPolishingAcceptsTheLargestFittingActiveSet(t *testing.T) {
	t.Parallel()

	largest := app.MaxCMAESFullDimensions / app.ParametersPerCircle

	config := polishingBaseConfig(t, app.OptimizerMayfly)
	config.Circles = largest
	config.BatchSize = 8
	config.PolishingOptimizer = app.OptimizerCMAES
	config.PolishingActiveSetSize = largest

	err := config.Validate()
	if err != nil {
		t.Fatalf("Validate() rejected an active set of %d circles: %v", largest, err)
	}
}

// TestMayflyPolishingIgnoresTheFullCovarianceLimit keeps the new check where it
// belongs. A MayFly sweep learns no covariance matrix, so the limit means
// nothing to it and an active set it can afford must stay legal.
func TestMayflyPolishingIgnoresTheFullCovarianceLimit(t *testing.T) {
	t.Parallel()

	oversized := app.MaxCMAESFullDimensions/app.ParametersPerCircle + 1

	config := polishingBaseConfig(t, app.OptimizerMayfly)
	config.Circles = oversized
	config.BatchSize = 8
	config.PolishingActiveSetSize = oversized

	err := config.Validate()
	if err != nil {
		t.Fatalf("Validate() rejected a MayFly sweep over %d circles: %v", oversized, err)
	}
}

// polishingBaseConfig is a defaulted batch-mode configuration with polishing on,
// which is the smallest shape every test above starts from.
func polishingBaseConfig(t *testing.T, engine app.Optimizer) app.JobConfig {
	t.Helper()

	config := app.JobConfig{RefPath: referenceImage, Optimizer: engine}

	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("ApplyDefaults() error = %v", err)
	}

	config.Mode = app.ModeBatch
	config.PolishingEnabled = true

	return config
}
