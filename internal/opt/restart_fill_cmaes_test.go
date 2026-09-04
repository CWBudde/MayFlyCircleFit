package opt_test

import (
	"context"
	"testing"

	"github.com/cwbudde/circlefit/internal/opt"
)

// TestFillingRestartsReclaimBudgetFromAConvergingEngine is the mechanism check
// the synthetic wrapper tests cannot make. CMA-ES on a sphere trips its own
// TolFun long before its iteration cap, which is exactly the behaviour that
// left the restart ladder's arms spending 29-44% of their budget
// (docs/cmaes-restart-ladder-report.md). A fixed count of cold attempts
// inherits that waste; the filling shape spends the same cap.
func TestFillingRestartsReclaimBudgetFromAConvergingEngine(t *testing.T) {
	t.Parallel()

	const (
		attempts = 4
		iters    = 200
		popSize  = 8
	)

	run := func(restarts int) opt.Result {
		t.Helper()

		optimizer := opt.WithRestarts(opt.NewCMAES(iters, popSize, 4242), restarts)

		lifecycle, ok := optimizer.(opt.LifecycleOptimizer)
		if !ok {
			t.Fatalf("optimizer %T is not a LifecycleOptimizer", optimizer)
		}

		result, err := lifecycle.RunContext(context.Background(), opt.Problem{
			Eval:  cmaesSphere,
			Lower: []float64{-5, -5, -5, -5}, Upper: []float64{5, 5, 5, 5}, Dim: 4,
		}, opt.RunOptions{})
		if err != nil {
			t.Fatalf("RunContext(%d): %v", restarts, err)
		}

		return result
	}

	fixed := run(attempts)
	filling := run(-attempts)

	iterationCap := attempts * iters

	// The premise. If CMA-ES ran to its cap here there would be nothing to
	// reclaim and the rest of the test would prove nothing.
	if fixed.Iterations >= iterationCap {
		t.Fatalf("fixed count spent %d of %d iterations; the engine did not converge early", fixed.Iterations, iterationCap)
	}

	if filling.Iterations <= fixed.Iterations {
		t.Fatalf("filling spent %d iterations, fixed count spent %d; the cap was not reclaimed",
			filling.Iterations, fixed.Iterations)
	}

	if filling.Iterations > iterationCap {
		t.Fatalf("filling spent %d iterations, overrunning the cap of %d", filling.Iterations, iterationCap)
	}

	// Reclaimed budget buys more independent draws, and every one of them is
	// recorded. Fewer records than a fixed count would mean the wrapper lost
	// the history the campaign reads.
	if len(filling.Restarts) <= attempts {
		t.Fatalf("filling recorded %d runs, want more than the fixed count's %d attempts",
			len(filling.Restarts), attempts)
	}

	if filling.BestCost > fixed.BestCost {
		t.Fatalf("filling best %v is worse than the fixed count's %v on the same seed",
			filling.BestCost, fixed.BestCost)
	}
}
