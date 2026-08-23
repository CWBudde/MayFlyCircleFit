package opt_test

import (
	"context"
	"errors"
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
	// update never runs; at 0 the reverse. The two cannot coincide. This is
	// the deprecated override path: as of MayFly v0.6.0 an unset weight
	// selects the paper's fitness test instead of any probability.
	if pureMayfly, pureAquila := knobRun(t, "aoblmoa", opt.WithAquilaWeight(0)),
		knobRun(t, "aoblmoa", opt.WithAquilaWeight(1)); pureMayfly == pureAquila {
		t.Fatalf("Aquila weights 0 and 1 produced the same cost %v, so the option never reached the library",
			pureMayfly)
	}
}

// OppositionProbability is inert as of MayFly v0.6.0, which applies stochastic
// opposition to every offspring rather than to a sampled share. Both halves are
// asserted: an in-range value must not move the result, and an out-of-range one
// must still be rejected. Together they pin "reaches the library's validation
// but not its algorithm", which is the whole of the field's remaining contract.
func TestOppositionProbabilityIsInertButStillValidated(t *testing.T) {
	t.Parallel()

	if none, always := knobRun(t, "aoblmoa", opt.WithOppositionProbability(0)),
		knobRun(t, "aoblmoa", opt.WithOppositionProbability(1)); none != always {
		t.Fatalf("opposition rates 0 and 1 produced different costs %v and %v; "+
			"the library is reading the setting again, so the field is no longer inert",
			none, always)
	}

	err := knobRunErr("aoblmoa", opt.WithOppositionProbability(1.5))
	if err == nil {
		t.Fatal("an opposition rate of 1.5 was accepted, so the option no longer reaches validation")
	}
}

// knobRunErr mirrors knobRun but hands back the error instead of failing, so a
// test can assert that the library rejects a setting.
func knobRunErr(variant string, options ...opt.MayflyOption) error {
	optimizer, err := opt.NewMayflyVariant(variant, 30, 40, 7, options...)
	if err != nil {
		return err
	}

	lifecycle, ok := optimizer.(opt.LifecycleOptimizer)
	if !ok {
		return errors.New("NewMayflyVariant did not return a LifecycleOptimizer")
	}

	_, err = lifecycle.RunContext(context.Background(), knobProblem(4), opt.RunOptions{})

	return err
}

func TestOptionalFloatIsANoOpWhenUnset(t *testing.T) {
	t.Parallel()

	if applied, unset := knobRun(t, "standard", opt.OptionalFloat(nil, opt.WithDanceDamp)),
		knobRun(t, "standard"); applied != unset {
		t.Fatalf("a nil knob changed the run: %v versus %v", applied, unset)
	}
}
