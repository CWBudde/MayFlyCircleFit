//nolint:testpackage // exercises unexported optimizer construction, as the other cmd tests do
package cmd

import (
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// Adapter type names the engine-selection table compares against.
const (
	mayflyAdapterType    = "*opt.MayflyAdapter"
	dragonflyAdapterType = "*opt.DragonflyAdapter"
	cmaesAdapterType     = "*opt.CMAESAdapter"
)

func TestNewStageOptimizerSelectsTheConfiguredEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		optimizer app.Optimizer
		variant   app.Variant
		want      string
	}{
		// An absent optimizer is what every checkpoint written before the
		// field existed carries, and it has to keep resuming as MayFly.
		{name: "absent", optimizer: "", variant: app.VariantStandard, want: mayflyAdapterType},
		{name: "mayfly", optimizer: app.OptimizerMayfly, variant: app.VariantDESMA, want: mayflyAdapterType},
		{name: "dragonfly", optimizer: app.OptimizerDragonfly, want: dragonflyAdapterType},
		{name: "cmaes", optimizer: app.OptimizerCMAES, want: cmaesAdapterType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			optimizer, err := newStageOptimizer(app.JobConfig{
				Optimizer: test.optimizer,
				Variant:   test.variant,
				Iters:     10,
				PopSize:   20,
				Seed:      7,
			}, nil)
			if err != nil {
				t.Fatalf("newStageOptimizer() error = %v", err)
			}

			switch optimizer.(type) {
			case *opt.MayflyAdapter:
				if test.want != mayflyAdapterType {
					t.Errorf("optimizer = %T, want %s", optimizer, test.want)
				}
			case *opt.DragonflyAdapter:
				if test.want != dragonflyAdapterType {
					t.Errorf("optimizer = %T, want %s", optimizer, test.want)
				}
			case *opt.CMAESAdapter:
				if test.want != cmaesAdapterType {
					t.Errorf("optimizer = %T, want %s", optimizer, test.want)
				}
			default:
				t.Errorf("optimizer = %T, want %s", optimizer, test.want)
			}
		})
	}
}

func TestNewStageOptimizerReportsAnUnknownVariant(t *testing.T) {
	t.Parallel()

	_, err := newStageOptimizer(app.JobConfig{
		Optimizer: app.OptimizerMayfly,
		Variant:   "swarm",
		Iters:     10,
		PopSize:   20,
	}, nil)
	if err == nil {
		t.Fatal("newStageOptimizer() accepted an unknown variant")
	}
}

func TestVariantFlagKeepsTheMayflyDefaultOffOtherEngines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		optimizer string
		want      app.Variant
	}{
		{name: "mayfly keeps the flag default", optimizer: "mayfly", want: app.VariantStandard},
		{name: "empty keeps the flag default", optimizer: "", want: app.VariantStandard},
		// An unnamed flag must reach a Dragonfly configuration empty, so
		// validation can tell an unasked-for variant from a refused one.
		{name: "dragonfly drops the flag default", optimizer: "dragonfly", want: ""},
		// An unknown engine keeps the variant so the engine, not the variant,
		// is what the operator is told about.
		{name: "unknown engine drops the flag default", optimizer: "swarm", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := variantFlag(nil, test.optimizer, string(app.VariantStandard))
			if got != test.want {
				t.Errorf("variantFlag(%q) = %q, want %q", test.optimizer, got, test.want)
			}
		})
	}
}

func TestOptimizerLibraryVersionFollowsTheEngine(t *testing.T) {
	t.Parallel()

	if got := optimizerLibraryVersion(app.OptimizerDragonfly); got != opt.DragonflyLibraryVersion() {
		t.Errorf("optimizerLibraryVersion(dragonfly) = %q, want the Dragonfly version", got)
	}

	if got := optimizerLibraryVersion(app.OptimizerMayfly); got != opt.LibraryVersion() {
		t.Errorf("optimizerLibraryVersion(mayfly) = %q, want the MayFly version", got)
	}

	if got := optimizerLibraryVersion(app.OptimizerCMAES); got != opt.CMAESLibraryVersion() {
		t.Errorf("optimizerLibraryVersion(cmaes) = %q, want the CMA-ES version", got)
	}
}

func TestRunCommandExposesCMAESFlags(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"optimizer", "initial-sigma", "covariance-mode", "active-cma", "restart-strategy",
	} {
		if runCmd.Flags().Lookup(name) == nil {
			t.Errorf("run command has no --%s flag", name)
		}
	}
}
