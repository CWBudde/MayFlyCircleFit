package opt_test

import (
	"context"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// TestEveryConfigurableQMCInitOptimizes is the initial-population sibling of
// TestEveryConfigurableOptimizerOptimizes: every strategy a JobConfig may name
// has to reach a MayFly run that actually completes. Without it a strategy
// could pass validation in app and then be rejected by the library at
// construction -- a configuration the server accepts and the optimizer
// refuses, which is the failure the engine and variant contracts exist to
// prevent.
//
// It also covers the dimension ceiling by construction: Sobol carries one and
// Halton does not, so a strategy that could not build at this problem size
// fails here rather than in a campaign.
func TestEveryConfigurableQMCInitOptimizes(t *testing.T) {
	t.Parallel()

	for _, init := range app.SupportedQMCInits() {
		t.Run(string(init), func(t *testing.T) {
			t.Parallel()

			built, err := opt.NewMayflyVariant(string(app.VariantStandard), 5, 20, 1234,
				opt.WithQMCInit(string(init)))
			if err != nil {
				t.Fatalf("opt.NewMayflyVariant() error = %v", err)
			}

			lifecycle, ok := built.(opt.LifecycleOptimizer)
			if !ok {
				t.Fatalf("qmc init %q did not yield a opt.LifecycleOptimizer", init)
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
		})
	}
}

// TestQMCInitIsDeterministicForAFixedSeed is the property the campaign
// harness depends on. The library draws the sequence's scramble from the run's
// generator when QMCSeed is left at zero, and the adapter always seeds that
// generator, so two runs of one seed have to agree exactly.
//
// A scramble drawn from the clock instead would make every quasi-random run
// irreproducible while leaving uniform runs reproducible, which is the kind of
// asymmetry that invalidates a paired comparison rather than announcing itself.
func TestQMCInitIsDeterministicForAFixedSeed(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T) float64 {
		t.Helper()

		built, err := opt.NewMayflyVariant(string(app.VariantStandard), 8, 20, 4242,
			opt.WithQMCInit(string(app.QMCInitSobol)))
		if err != nil {
			t.Fatalf("opt.NewMayflyVariant() error = %v", err)
		}

		lifecycle, ok := built.(opt.LifecycleOptimizer)
		if !ok {
			t.Fatal("adapter does not implement opt.LifecycleOptimizer")
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

		return result.BestCost
	}

	if first, second := run(t), run(t); first != second {
		t.Errorf("BestCost = %v then %v, want one seed to reproduce one run", first, second)
	}
}

// TestQMCInitChangesTheSearch guards against the option being accepted and
// then dropped. A quasi-random initial population is a different starting
// sample, so it must not reproduce the uniform run of the same seed -- if it
// did, every measurement of this knob would compare a configuration against
// itself and report a null result no matter what the sequence does.
func TestQMCInitChangesTheSearch(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, options ...opt.MayflyOption) float64 {
		t.Helper()

		built, err := opt.NewMayflyVariant(string(app.VariantStandard), 8, 20, 99, options...)
		if err != nil {
			t.Fatalf("opt.NewMayflyVariant() error = %v", err)
		}

		lifecycle, ok := built.(opt.LifecycleOptimizer)
		if !ok {
			t.Fatal("adapter does not implement opt.LifecycleOptimizer")
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

		return result.BestCost
	}

	uniform := run(t)
	sobol := run(t, opt.WithQMCInit(string(app.QMCInitSobol)))

	if uniform == sobol {
		t.Errorf("sobol reproduced the uniform run exactly (%v); the option is not reaching the library", sobol)
	}
}
