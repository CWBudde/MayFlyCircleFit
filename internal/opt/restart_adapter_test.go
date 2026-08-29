package opt

import (
	"context"
	"math"
	"testing"
)

func rastrigin(x []float64) float64 {
	total := 10 * float64(len(x))
	for _, v := range x {
		total += v*v - 10*math.Cos(2*math.Pi*v)
	}

	return total
}

func restartProblem(dim int) Problem {
	lower := make([]float64, dim)
	upper := make([]float64, dim)

	for i := range dim {
		lower[i], upper[i] = -5.12, 5.12
	}

	return Problem{Eval: rastrigin, Lower: lower, Upper: upper, Dim: dim}
}

// Restarts must stay reproducible for a fixed base seed; the attempts vary the
// seed deterministically rather than drawing fresh entropy.
func TestRestartsAreReproducibleForAFixedSeed(t *testing.T) {
	t.Parallel()

	run := func() Result {
		optimizer := WithRestarts(NewMayfly(20, 12, 7), 3).(LifecycleOptimizer)

		result, err := optimizer.RunContext(context.Background(), restartProblem(4), RunOptions{})
		if err != nil {
			t.Fatalf("RunContext: %v", err)
		}

		return result
	}

	first, second := run(), run()
	if first.BestCost != second.BestCost {
		t.Fatalf("restarts are not reproducible: %v then %v", first.BestCost, second.BestCost)
	}

	for i := range first.BestParams {
		if first.BestParams[i] != second.BestParams[i] {
			t.Fatalf("restart parameters differ at %d: %v vs %v", i, first.BestParams[i], second.BestParams[i])
		}
	}
}

// The attempts must actually differ. If they collapsed onto one seed, every
// attempt would return the same cost and the wrapper would be dead weight.
func TestRestartAttemptsExploreDifferently(t *testing.T) {
	t.Parallel()

	base := NewMayfly(20, 12, 7).(LifecycleOptimizer)
	problem := restartProblem(4)

	costs := map[float64]bool{}

	for attempt := range 3 {
		result, err := base.RunContext(context.Background(), problem, RunOptions{SeedOffset: attempt})
		if err != nil {
			t.Fatalf("RunContext: %v", err)
		}

		costs[result.BestCost] = true
	}

	if len(costs) < 2 {
		t.Fatalf("every attempt returned the same cost %v; SeedOffset did not change the run", costs)
	}
}

// A restarted run must never return worse than its own best attempt.
func TestRestartsNeverReturnWorseThanTheBestAttempt(t *testing.T) {
	t.Parallel()

	problem := restartProblem(4)

	best := math.Inf(1)

	for attempt := range 4 {
		single := NewMayfly(20, 12, 7).(LifecycleOptimizer)

		result, err := single.RunContext(context.Background(), problem, RunOptions{SeedOffset: attempt})
		if err != nil {
			t.Fatalf("RunContext: %v", err)
		}

		best = math.Min(best, result.BestCost)
	}

	restarted, err := WithRestarts(NewMayfly(20, 12, 7), 4).(LifecycleOptimizer).
		RunContext(context.Background(), problem, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	if restarted.BestCost > best {
		t.Fatalf("restarted run returned %v, worse than the best individual attempt %v",
			restarted.BestCost, best)
	}
}
