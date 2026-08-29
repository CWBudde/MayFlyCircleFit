package opt

import (
	"context"
	"math"
	"slices"
	"sync/atomic"
	"testing"
)

// rippledSphere is a deterministic multimodal objective. A plain sphere is too
// easy: every configuration converges to the same optimum and would hide a
// trajectory difference between the serial and parallel optimizer loops.
func rippledSphere(params []float64) float64 {
	sum := 0.0

	for i, value := range params {
		offset := value - float64(i)*0.3
		sum += offset * offset * math.Abs(math.Sin(value))
	}

	return sum
}

func runRippledSphere(t *testing.T, workers int) Result {
	t.Helper()
	return runRippledSphereVariant(t, variantStandard, workers)
}

func runRippledSphereVariant(t *testing.T, variant string, workers int) Result {
	t.Helper()
	const dim = 8
	lower := make([]float64, dim)

	upper := make([]float64, dim)
	for i := range lower {
		lower[i], upper[i] = -5, 5
	}

	var options []MayflyOption
	if workers > 1 {
		options = append(options, WithParallelEvaluation(workers))
	}

	optimizer, err := NewMayflyVariant(variant, 25, 20, 4242, options...)
	if err != nil {
		t.Fatalf("NewMayflyVariant() error = %v", err)
	}

	lifecycle, ok := optimizer.(LifecycleOptimizer)
	if !ok {
		t.Fatal("adapter does not implement LifecycleOptimizer")
	}

	result, err := lifecycle.RunContext(context.Background(), Problem{
		Eval: rippledSphere, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	return result
}

// TestParallelEvaluationIsReproducible pins the reproducibility guarantee that
// actually holds: with parallel evaluation enabled, a fixed seed produces a
// bit-identical result, and the result does not depend on how many workers the
// pool used. Evaluation order changes, but every merge is deterministic —
// Mayfly's batch best breaks ties by population index and its RNG is only ever
// advanced from the serial phase code.
func TestParallelEvaluationIsReproducible(t *testing.T) {
	t.Parallel()

	first := runRippledSphere(t, 4)
	again := runRippledSphere(t, 4)
	wider := runRippledSphere(t, 7)

	if first.BestCost != again.BestCost || !slices.Equal(first.BestParams, again.BestParams) {
		t.Fatalf("repeated parallel run differs: %.17g %v vs %.17g %v",
			first.BestCost, first.BestParams, again.BestCost, again.BestParams)
	}

	if wider.BestCost != first.BestCost || !slices.Equal(wider.BestParams, first.BestParams) {
		t.Fatalf("worker count changed the result: 4 workers %.17g, 7 workers %.17g",
			first.BestCost, wider.BestCost)
	}
}

// TestParallelEvaluationMatchesSerial pins the property MayFly v0.7.0
// introduced: parallel and serial evaluation now walk the same trajectory, so
// a seed reproduces across the flag rather than only with it held fixed.
//
// Before v0.7.0 the two diverged, because the serial male loop updated the
// global best in the middle of a generation and steered the rest of it, while
// the parallel loop held the best fixed for the whole generation and merged
// afterwards. v0.7.0 gave the modes the same proposal and commit semantics.
//
// This is the previous tripwire inverted. It was written to fail once the
// divergence stopped being real, which is how the stale caveat was found on the
// version bump; it now fails if the divergence returns, because the
// comparability rules in docs/behavior-invariants.md and docs/schedule-format.md
// depend on the equivalence holding.
//
// Every variant is covered rather than standard alone: the modes are shared
// machinery, but each variant commits its own updates, so one could regress by
// itself. Ranging over supportedVariants rather than a literal list means a
// variant added to the adapter is covered without this test being remembered.
func TestParallelEvaluationMatchesSerial(t *testing.T) {
	t.Parallel()

	for variant := range supportedVariants {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()

			serial := runRippledSphereVariant(t, variant, 1)
			repeated := runRippledSphereVariant(t, variant, 1)
			parallel := runRippledSphereVariant(t, variant, 4)

			if serial.BestCost != repeated.BestCost || !slices.Equal(serial.BestParams, repeated.BestParams) {
				t.Fatalf("serial run is not reproducible: %.17g vs %.17g", serial.BestCost, repeated.BestCost)
			}

			if serial.BestCost != parallel.BestCost || !slices.Equal(serial.BestParams, parallel.BestParams) {
				t.Fatalf("parallel diverged from serial: serial %.17g, parallel %.17g; "+
					"the equivalence documented in docs/behavior-invariants.md no longer holds",
					serial.BestCost, parallel.BestCost)
			}
		})
	}
}

// TestParallelEvaluationCallsObjectiveConcurrently proves the option actually
// reaches Mayfly's worker pool. Without it the objective is only ever called
// from one goroutine, and enabling the pipeline's session pool would buy
// nothing.
//
//nolint:paralleltest // asserts the peak count of concurrent in-flight evaluations, which test load would skew
func TestParallelEvaluationCallsObjectiveConcurrently(t *testing.T) {
	const dim = 4
	lower := make([]float64, dim)

	upper := make([]float64, dim)
	for i := range lower {
		lower[i], upper[i] = -1, 1
	}

	var inFlight, peak atomic.Int64
	eval := func(params []float64) float64 {
		current := inFlight.Add(1)

		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		// Hold the slot briefly so concurrent callers actually overlap.
		sum := 0.0
		for range 2000 {
			sum += rippledSphere(params)
		}

		inFlight.Add(-1)

		return sum
	}

	optimizer, err := NewMayflyVariant(variantStandard, 5, 16, 99, WithParallelEvaluation(4))
	if err != nil {
		t.Fatalf("NewMayflyVariant() error = %v", err)
	}

	lifecycle := optimizer.(LifecycleOptimizer)

	_, err = lifecycle.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if peak.Load() < 2 {
		t.Fatalf("peak concurrent evaluations = %d, want at least 2", peak.Load())
	}
}

// TestSerialEvaluationStaysSingleThreaded pins the default: an optimizer built
// without the option must never call the objective from two goroutines, because
// callers built against that guarantee share one renderer canvas.
//
//nolint:paralleltest // asserts the peak count of concurrent in-flight evaluations, which test load would skew
func TestSerialEvaluationStaysSingleThreaded(t *testing.T) {
	const dim = 4
	lower := make([]float64, dim)

	upper := make([]float64, dim)
	for i := range lower {
		lower[i], upper[i] = -1, 1
	}

	var inFlight, peak atomic.Int64
	eval := func(params []float64) float64 {
		current := inFlight.Add(1)

		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}

		defer inFlight.Add(-1)

		return rippledSphere(params)
	}

	optimizer, err := NewMayflyVariant(variantStandard, 5, 16, 99)
	if err != nil {
		t.Fatalf("NewMayflyVariant() error = %v", err)
	}

	lifecycle := optimizer.(LifecycleOptimizer)

	_, err = lifecycle.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if peak.Load() != 1 {
		t.Fatalf("peak concurrent evaluations = %d, want 1 without WithParallelEvaluation", peak.Load())
	}
}

// TestParallelEvaluationIsReproducibleAcrossVariants extends the reproducibility
// guarantee to every variant this adapter can construct, not just the default.
// Each variant reaches a different set of Mayfly phases, and the guarantee rests
// on all of them keeping their RNG draws on the optimizer goroutine; a variant
// that drew from a worker would be reproducible for the default and broken here.
func TestParallelEvaluationIsReproducibleAcrossVariants(t *testing.T) {
	t.Parallel()

	for variant := range supportedVariants {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()

			first := runRippledSphereVariant(t, variant, 4)
			again := runRippledSphereVariant(t, variant, 4)
			wider := runRippledSphereVariant(t, variant, 7)

			if first.BestCost != again.BestCost || !slices.Equal(first.BestParams, again.BestParams) {
				t.Fatalf("repeated parallel run differs: %.17g vs %.17g", first.BestCost, again.BestCost)
			}

			if wider.BestCost != first.BestCost || !slices.Equal(wider.BestParams, first.BestParams) {
				t.Fatalf("worker count changed the result: 4 workers %.17g, 7 workers %.17g",
					first.BestCost, wider.BestCost)
			}
		})
	}
}

// TestParallelEvaluationWidthSeesThroughWrappers pins that a caller can still
// discover the evaluation width after the optimizer has been wrapped. The
// pipeline wraps optimizers for epochs and, on the server, for progress
// reporting; a wrapper that dropped the report would turn polishing's
// serial-only guard into a silent no-op, which is exactly the failure the guard
// exists to prevent.
func TestParallelEvaluationWidthSeesThroughWrappers(t *testing.T) {
	t.Parallel()

	serial, err := NewMayflyVariant(variantStandard, 5, 16, 99)
	if err != nil {
		t.Fatal(err)
	}

	if got := ParallelEvaluationWidth(serial); got != 1 {
		t.Fatalf("ParallelEvaluationWidth() = %d, want 1 for a serial optimizer", got)
	}

	parallel, err := NewMayflyVariant(variantStandard, 5, 16, 99, WithParallelEvaluation(4))
	if err != nil {
		t.Fatal(err)
	}

	if got := ParallelEvaluationWidth(parallel); got != 4 {
		t.Fatalf("ParallelEvaluationWidth() = %d, want 4", got)
	}

	if got := ParallelEvaluationWidth(WithEpochs(parallel, 3)); got != 4 {
		t.Fatalf("ParallelEvaluationWidth() through WithEpochs = %d, want 4", got)
	}
}

// TestDragonflyParallelEvaluationMatchesSerial is the Dragonfly half of
// TestParallelEvaluationMatchesSerial. It is a separate library on a separate
// pin, so the equivalence has to be pinned separately rather than assumed from
// MayFly's; docs/known-limitations.md describes both engines in one sentence
// and would otherwise be one bump away from being wrong about one of them.
func TestDragonflyParallelEvaluationMatchesSerial(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, workers int) Result {
		t.Helper()

		const dim = 8

		lower := make([]float64, dim)

		upper := make([]float64, dim)
		for i := range lower {
			lower[i], upper[i] = -5, 5
		}

		var options []DragonflyOption
		if workers > 1 {
			options = append(options, WithDragonflyParallelEvaluation(workers))
		}

		lifecycle, ok := NewDragonfly(25, 20, 4242, options...).(LifecycleOptimizer)
		if !ok {
			t.Fatal("dragonfly adapter does not implement LifecycleOptimizer")
		}

		result, err := lifecycle.RunContext(
			context.Background(),
			Problem{Eval: rippledSphere, Lower: lower, Upper: upper, Dim: dim},
			RunOptions{},
		)
		if err != nil {
			t.Fatalf("RunContext() error = %v", err)
		}

		return result
	}

	serial := run(t, 1)
	parallel := run(t, 4)

	if serial.BestCost != parallel.BestCost || !slices.Equal(serial.BestParams, parallel.BestParams) {
		t.Errorf("parallel diverged from serial: serial %.17g, parallel %.17g",
			serial.BestCost, parallel.BestCost)
	}
}
