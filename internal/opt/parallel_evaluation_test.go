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

// TestParallelEvaluationDiffersFromSerial documents the caveat deliberately, so
// that the difference is a pinned, reviewed property instead of a surprise.
//
// Mayfly's serial male loop updates the global best in the middle of the
// population, which steers the remaining members of the same generation. Its
// parallel loop clones the global best for the whole generation and merges
// afterwards. Both are deterministic search trajectories, but they are not the
// same trajectory, so a seed only reproduces with the flag held fixed. That is
// why ParallelEvaluation is opt-in and defaults to false.
func TestParallelEvaluationDiffersFromSerial(t *testing.T) {
	serial := runRippledSphere(t, 1)
	repeated := runRippledSphere(t, 1)
	parallel := runRippledSphere(t, 4)

	if serial.BestCost != repeated.BestCost || !slices.Equal(serial.BestParams, repeated.BestParams) {
		t.Fatalf("serial run is not reproducible: %.17g vs %.17g", serial.BestCost, repeated.BestCost)
	}

	if serial.BestCost == parallel.BestCost && slices.Equal(serial.BestParams, parallel.BestParams) {
		t.Fatal("parallel and serial results now match; the documented caveat in " +
			"docs/known-limitations.md and WithParallelEvaluation is stale and should be removed")
	}
}

// TestParallelEvaluationCallsObjectiveConcurrently proves the option actually
// reaches Mayfly's worker pool. Without it the objective is only ever called
// from one goroutine, and enabling the pipeline's session pool would buy
// nothing.
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
	if _, err := lifecycle.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{}); err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if peak.Load() < 2 {
		t.Fatalf("peak concurrent evaluations = %d, want at least 2", peak.Load())
	}
}

// TestSerialEvaluationStaysSingleThreaded pins the default: an optimizer built
// without the option must never call the objective from two goroutines, because
// callers built against that guarantee share one renderer canvas.
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
	if _, err := lifecycle.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{}); err != nil {
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
	for variant := range supportedVariants {
		t.Run(variant, func(t *testing.T) {
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
