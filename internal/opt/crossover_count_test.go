package opt_test

import (
	"context"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// crossoverRastrigin is a local multimodal objective. It is deliberately not
// shared with other test files so this package's tests stay independent.
func crossoverRastrigin(x []float64) float64 {
	total := 10 * float64(len(x))
	for _, v := range x {
		total += v*v - 10*math.Cos(2*math.Pi*v)
	}

	return total
}

func crossoverProblem(dim int) opt.Problem {
	lower := make([]float64, dim)
	upper := make([]float64, dim)

	for i := range dim {
		lower[i], upper[i] = -5.12, 5.12
	}

	return opt.Problem{Eval: crossoverRastrigin, Lower: lower, Upper: upper, Dim: dim}
}

func crossoverOptimizer(t *testing.T, options ...opt.MayflyOption) opt.LifecycleOptimizer {
	t.Helper()

	optimizer, ok := opt.NewMayfly(10, 40, 3, options...).(opt.LifecycleOptimizer)
	if !ok {
		t.Fatal("NewMayfly did not return a LifecycleOptimizer")
	}

	return optimizer
}

// The offspring count is the dominant per-iteration cost, so a lower count has
// to show up as fewer evaluations. This is what proves the option reaches the
// library rather than being silently dropped.
func TestCrossoverCountChangesTheEvaluationBudget(t *testing.T) {
	t.Parallel()

	run := func(count int) int {
		result, err := crossoverOptimizer(t, opt.WithCrossoverCount(count)).
			RunContext(context.Background(), crossoverProblem(4), opt.RunOptions{})
		if err != nil {
			t.Fatalf("RunContext: %v", err)
		}

		return result.Evaluations
	}

	libraryDefault := run(0)

	reduced := run(4)
	if reduced >= libraryDefault {
		t.Fatalf("crossover count 4 used %d evaluations, not fewer than the library default's %d",
			reduced, libraryDefault)
	}
}

func TestCrossoverCountZeroLeavesTheLibraryDefault(t *testing.T) {
	t.Parallel()

	run := func(options ...opt.MayflyOption) float64 {
		result, err := crossoverOptimizer(t, options...).
			RunContext(context.Background(), crossoverProblem(4), opt.RunOptions{})
		if err != nil {
			t.Fatalf("RunContext: %v", err)
		}

		return result.BestCost
	}

	if withZero, without := run(opt.WithCrossoverCount(0)), run(); withZero != without {
		t.Fatalf("crossover count 0 changed the run: %v versus %v", withZero, without)
	}
}
