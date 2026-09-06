package app_test

import (
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
)

// TestSecondaryOptimizerNamesTheSecondLibrary covers the three ways a job runs
// only one, and the one way it runs two.
func TestSecondaryOptimizerNamesTheSecondLibrary(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		base     app.Optimizer
		polisher app.Optimizer
		polish   bool
		want     app.Optimizer
		wantOK   bool
	}{
		"no polishing":      {base: app.OptimizerCMAES, polisher: app.OptimizerMayfly, polish: false},
		"same engine":       {base: app.OptimizerMayfly, polisher: app.OptimizerMayfly, polish: true},
		"same engine cmaes": {base: app.OptimizerCMAES, polisher: app.OptimizerCMAES, polish: true},
		"mayfly polishes cmaes": {
			base: app.OptimizerCMAES, polisher: app.OptimizerMayfly, polish: true,
			want: app.OptimizerMayfly, wantOK: true,
		},
		"cmaes polishes mayfly": {
			base: app.OptimizerMayfly, polisher: app.OptimizerCMAES, polish: true,
			want: app.OptimizerCMAES, wantOK: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := app.JobConfig{
				Optimizer:          test.base,
				PolishingOptimizer: test.polisher,
				PolishingEnabled:   test.polish,
			}

			got, ok := config.SecondaryOptimizer()
			if ok != test.wantOK || got != test.want {
				t.Errorf("SecondaryOptimizer() = (%q, %t), want (%q, %t)", got, ok, test.want, test.wantOK)
			}
		})
	}
}
