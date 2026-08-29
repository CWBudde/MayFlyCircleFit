package opt

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
)

// app.SupportedVariants duplicates this package's supportedVariants because app
// is dependency-free and cannot import opt, while opt has no reason to import
// application configuration. The duplicate is load-bearing: app validates the
// configured name and opt constructs the algorithm from it, so a name accepted
// by one and not the other is either an unreachable variant or a request that
// passes validation and then fails at optimizer construction. This test is the
// reason the duplication is safe. It lives here, not in app, for the same
// reason internal/fit owns the ParametersPerCircle contract: the test needs the
// unexported set, and only this side may import the other.
func TestSupportedVariantsMatchTheApplicationConfiguration(t *testing.T) {
	t.Parallel()

	configurable := make([]string, 0, len(app.SupportedVariants()))
	for _, variant := range app.SupportedVariants() {
		configurable = append(configurable, string(variant))

		if _, ok := supportedVariants[string(variant)]; !ok {
			t.Errorf(
				"app accepts variant %q that opt cannot construct; "+
					"a configuration using it would pass validation and fail at optimizer construction",
				variant,
			)
		}
	}

	for variant := range supportedVariants {
		if !slices.Contains(configurable, variant) {
			t.Errorf(
				"opt implements variant %q that app rejects; "+
					"it is unreachable from the CLI, the server, and schedule documents",
				variant,
			)
		}
	}
}

// TestEveryConfigurableVariantOptimizes proves reachability past validation:
// each name a JobConfig may carry has to construct an optimizer that actually
// runs and returns a finite cost. Validation agreeing with the dispatch table
// is not enough, because a name could route to a constructor that fails or
// diverges. The budget is deliberately tiny so this stays a -short test.
func TestEveryConfigurableVariantOptimizes(t *testing.T) {
	t.Parallel()

	for _, variant := range app.SupportedVariants() {
		t.Run(string(variant), func(t *testing.T) {
			t.Parallel()

			optimizer, err := NewMayflyVariant(string(variant), 5, 20, 1234)
			if err != nil {
				t.Fatalf("NewMayflyVariant(%q) error = %v", variant, err)
			}

			lifecycle, ok := optimizer.(LifecycleOptimizer)
			if !ok {
				t.Fatalf("NewMayflyVariant(%q) returned %T, want a LifecycleOptimizer", variant, optimizer)
			}

			result, err := lifecycle.RunContext(context.Background(), Problem{
				Eval:  sphere,
				Lower: []float64{-5, -5, -5},
				Upper: []float64{5, 5, 5},
				Dim:   3,
			}, RunOptions{})
			if err != nil {
				t.Fatalf("RunContext() error = %v", err)
			}

			if math.IsNaN(result.BestCost) || math.IsInf(result.BestCost, 0) {
				t.Fatalf("best cost = %v, want a finite value", result.BestCost)
			}

			if len(result.BestParams) != 3 {
				t.Fatalf("best params = %v, want 3 values", result.BestParams)
			}

			for _, param := range result.BestParams {
				if math.IsNaN(param) || math.IsInf(param, 0) {
					t.Fatalf("best params = %v, want finite values", result.BestParams)
				}
			}

			if result.Evaluations == 0 {
				t.Fatal("evaluations = 0, want the objective to have been called")
			}
		})
	}
}
