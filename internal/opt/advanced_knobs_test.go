package opt_test

import (
	"context"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

func knobRastrigin(x []float64) float64 {
	total := 10 * float64(len(x))
	for _, v := range x {
		total += v*v - 10*math.Cos(2*math.Pi*v)
	}

	return total
}

func knobProblem(dim int) opt.Problem {
	lower := make([]float64, dim)
	upper := make([]float64, dim)

	for i := range dim {
		lower[i], upper[i] = -5.12, 5.12
	}

	return opt.Problem{Eval: knobRastrigin, Lower: lower, Upper: upper, Dim: dim}
}

func knobRun(t *testing.T, variant string, options ...opt.MayflyOption) float64 {
	t.Helper()

	optimizer, err := opt.NewMayflyVariant(variant, 30, 40, 7, options...)
	if err != nil {
		t.Fatalf("NewMayflyVariant: %v", err)
	}

	lifecycle, ok := optimizer.(opt.LifecycleOptimizer)
	if !ok {
		t.Fatal("NewMayflyVariant did not return a LifecycleOptimizer")
	}

	result, err := lifecycle.RunContext(context.Background(), knobProblem(4), opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	return result.BestCost
}

// The seed is fixed, so a differing cost is only explicable by the option
// reaching the library. This is what separates a wired knob from a dropped one.
func TestDanceDampReachesTheLibrary(t *testing.T) {
	t.Parallel()

	if retired, sustained := knobRun(t, "standard", opt.WithDanceDamp(0)),
		knobRun(t, "standard", opt.WithDanceDamp(1)); retired == sustained {
		t.Fatalf("dance damping 0 and 1 produced the same cost %v, so the option never reached the library",
			retired)
	}
}

func TestDanceDampUnsetMatchesTheLibraryDefault(t *testing.T) {
	t.Parallel()

	// 0.8 is the library's own default, so stating it must be indistinguishable
	// from leaving the knob alone.
	if stated, unset := knobRun(t, "standard", opt.WithDanceDamp(0.8)), knobRun(t, "standard"); stated != unset {
		t.Fatalf("stating the library default changed the run: %v versus %v", stated, unset)
	}
}

func TestAquilaWeightReachesTheLibrary(t *testing.T) {
	t.Parallel()

	// At 1.0 every individual takes the Aquila step and the Mayfly velocity
	// update never runs; at 0 the reverse. The two cannot coincide.
	if pureMayfly, pureAquila := knobRun(t, "aoblmoa", opt.WithAquilaWeight(0)),
		knobRun(t, "aoblmoa", opt.WithAquilaWeight(1)); pureMayfly == pureAquila {
		t.Fatalf("Aquila weights 0 and 1 produced the same cost %v, so the option never reached the library",
			pureMayfly)
	}
}

func TestOppositionProbabilityReachesTheLibrary(t *testing.T) {
	t.Parallel()

	if none, always := knobRun(t, "aoblmoa", opt.WithOppositionProbability(0)),
		knobRun(t, "aoblmoa", opt.WithOppositionProbability(1)); none == always {
		t.Fatalf("opposition rates 0 and 1 produced the same cost %v, so the option never reached the library",
			none)
	}
}

func TestOptionalFloatIsANoOpWhenUnset(t *testing.T) {
	t.Parallel()

	if applied, unset := knobRun(t, "standard", opt.OptionalFloat(nil, opt.WithDanceDamp)),
		knobRun(t, "standard"); applied != unset {
		t.Fatalf("a nil knob changed the run: %v versus %v", applied, unset)
	}
}
