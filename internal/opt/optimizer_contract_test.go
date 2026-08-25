package opt_test

import (
	"context"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// TestEveryConfigurableOptimizerOptimizes is the engine-level sibling of
// TestEveryConfigurableVariantOptimizes: every engine a JobConfig may name has
// to construct an optimizer that actually runs and returns a finite cost.
// Without it, an engine could pass validation in app and then have nothing in
// opt to build it -- a configuration accepted by the server and refused at
// optimizer construction, which is the failure the variant contract exists to
// prevent. The budget is deliberately tiny so this stays a -short test.
func TestEveryConfigurableOptimizerOptimizes(t *testing.T) {
	t.Parallel()

	for _, optimizer := range app.SupportedOptimizers() {
		t.Run(string(optimizer), func(t *testing.T) {
			t.Parallel()

			lifecycle, ok := newContractOptimizer(t, optimizer).(opt.LifecycleOptimizer)
			if !ok {
				t.Fatalf("optimizer %q is not a opt.LifecycleOptimizer", optimizer)
			}

			result, err := lifecycle.RunContext(context.Background(), opt.Problem{
				Eval:  dragonflySphere,
				Lower: []float64{-5, -5},
				Upper: []float64{5, 5},
				Dim:   2,
			}, opt.RunOptions{})
			if err != nil {
				t.Fatalf("RunContext() error = %v", err)
			}

			if math.IsNaN(result.BestCost) || math.IsInf(result.BestCost, 0) {
				t.Fatalf("BestCost = %v, want a finite cost", result.BestCost)
			}

			if result.Iterations != 5 {
				t.Errorf("Iterations = %d, want 5", result.Iterations)
			}
		})
	}
}

// newContractOptimizer builds each engine the way its production caller does.
// A new engine in app.SupportedOptimizers that nothing here can build fails the
// test rather than silently going untested.
func newContractOptimizer(t *testing.T, optimizer app.Optimizer) opt.Optimizer {
	t.Helper()

	switch optimizer {
	case app.OptimizerMayfly:
		built, err := opt.NewMayflyVariant(string(app.VariantStandard), 5, 20, 1234)
		if err != nil {
			t.Fatalf("opt.NewMayflyVariant() error = %v", err)
		}

		return built
	case app.OptimizerDragonfly:
		return opt.NewDragonfly(5, 20, 1234)
	case app.OptimizerCMAES:
		return opt.NewCMAES(5, 20, 1234)
	default:
		t.Fatalf("app accepts optimizer %q that opt cannot construct", optimizer)

		return nil
	}
}
