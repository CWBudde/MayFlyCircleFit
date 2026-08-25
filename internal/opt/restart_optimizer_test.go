package opt

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
)

// recordingOptimizer returns a scripted cost per attempt and records the run
// options it was handed, which is how the independence and seeding contracts
// are asserted without depending on Mayfly's own behaviour.
type recordingOptimizer struct {
	costs      []float64
	seen       []RunOptions
	iterations int
}

func (o *recordingOptimizer) ParallelEvaluationWorkers() int { return 0 }

func (o *recordingOptimizer) Run(_ func([]float64) float64, _, _ []float64, _ int) ([]float64, float64) {
	result, _ := o.RunContext(context.Background(), Problem{}, RunOptions{})
	return result.BestParams, result.BestCost
}

func (o *recordingOptimizer) RunContext(_ context.Context, _ Problem, options RunOptions) (Result, error) {
	index := len(o.seen)
	o.seen = append(o.seen, options)

	cost := math.Inf(1)
	if index < len(o.costs) {
		cost = o.costs[index]
	}

	if options.Observer != nil {
		// A real optimizer walks down to its best; the second value is what a
		// fresh attempt's early progress looks like. The diagnostics describe
		// the attempt's own population, so they differ per attempt and per
		// report even where the cost does not improve on an earlier attempt.
		options.Observer(Progress{
			Iterations: 1, Evaluations: 10,
			BestParams: []float64{cost + 100}, BestCost: cost + 100,
			Diagnostics: &SearchDiagnostics{PopulationSpread: cost + 100},
		})
		options.Observer(Progress{
			Iterations: o.iterations, Evaluations: 100,
			BestParams: []float64{cost}, BestCost: cost,
			Diagnostics: &SearchDiagnostics{PopulationSpread: cost},
		})
	}

	return Result{
		BestParams:  []float64{cost},
		BestCost:    cost,
		Iterations:  o.iterations,
		Evaluations: 100,
		Termination: TerminationCompleted,
	}, nil
}

func TestWithRestartsKeepsTheBestAttempt(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{10, 3, 7}, iterations: 5}

	result, err := WithRestarts(base, 3).(LifecycleOptimizer).
		RunContext(context.Background(), Problem{}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	if result.BestCost != 3 {
		t.Fatalf("best cost = %v, want 3 (the best attempt, not the last)", result.BestCost)
	}

	if len(base.seen) != 3 {
		t.Fatalf("ran %d attempts, want 3", len(base.seen))
	}

	if result.Iterations != 15 || result.Evaluations != 300 {
		t.Fatalf("work = %d iterations / %d evaluations, want 15/300 accumulated across attempts",
			result.Iterations, result.Evaluations)
	}
}

// The defining difference from WithEpochs: an attempt must not be handed the
// previous attempt's result, because inheriting it inherits its basin.
func TestWithRestartsDoesNotChainFromThePreviousBest(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{10, 3}, iterations: 5}

	_, err := WithRestarts(base, 2).(LifecycleOptimizer).
		RunContext(context.Background(), Problem{}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	for i, options := range base.seen {
		if options.Initial != nil {
			t.Fatalf("attempt %d was seeded with a previous result; restarts must start cold", i+1)
		}
	}
}

// A caller-supplied candidate is different: a resumed or staged run must not
// have the work it was handed thrown away.
func TestWithRestartsPreservesACallerSuppliedInitial(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{10, 3}, iterations: 5}
	initial := &Candidate{Params: []float64{1, 2}, Cost: 42}

	_, err := WithRestarts(base, 2).(LifecycleOptimizer).
		RunContext(context.Background(), Problem{}, RunOptions{Initial: initial})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	for i, options := range base.seen {
		if options.Initial != initial {
			t.Fatalf("attempt %d lost the caller's initial candidate", i+1)
		}
	}
}

func TestWithRestartsGivesEachAttemptADistinctSeedOffset(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{10, 3, 7}, iterations: 5}

	_, err := WithRestarts(base, 3).(LifecycleOptimizer).
		RunContext(context.Background(), Problem{}, RunOptions{ResumeCount: 2})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	offsets := map[int]bool{}
	for i, options := range base.seen {
		if options.ResumeCount != 2 {
			t.Fatalf("attempt %d changed ResumeCount to %d; restarts vary SeedOffset so nested "+
				"epochs cannot alias onto another attempt's seed", i+1, options.ResumeCount)
		}

		offsets[options.SeedOffset] = true
	}

	if len(offsets) != 3 {
		t.Fatalf("attempts shared a seed offset: %v", offsets)
	}
}

// Progress is documented as best-so-far, and a fresh attempt's early costs are
// worse than what an earlier attempt already reached.
func TestWithRestartsReportsMonotonicProgress(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{10, 3}, iterations: 5}

	var reported []float64

	_, err := WithRestarts(base, 2).(LifecycleOptimizer).RunContext(
		context.Background(), Problem{},
		RunOptions{Observer: func(p Progress) { reported = append(reported, p.BestCost) }},
	)
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	if len(reported) == 0 {
		t.Fatal("no progress was reported")
	}

	for i := 1; i < len(reported); i++ {
		if reported[i] > reported[i-1] {
			t.Fatalf("progress regressed: %v", reported)
		}
	}

	if reported[len(reported)-1] != 3 {
		t.Fatalf("final reported cost = %v, want 3", reported[len(reported)-1])
	}
}

// Diagnostics describe the population an attempt currently holds, not the
// incumbent. A later attempt whose costs never beat an earlier one must still
// reach the observer, or a recorded mechanism trajectory only ever samples
// collapsed populations.
func TestWithRestartsForwardsDiagnosticsFromNonImprovingAttempts(t *testing.T) {
	t.Parallel()

	// The second attempt is worse than the first throughout, so every one of
	// its reports is non-improving.
	base := &recordingOptimizer{costs: []float64{3, 10}, iterations: 5}

	var (
		costs   []float64
		spreads []float64
	)

	lifecycle, ok := WithRestarts(base, 2).(LifecycleOptimizer)
	if !ok {
		t.Fatal("WithRestarts did not preserve lifecycle optimization")
	}

	_, err := lifecycle.RunContext(
		context.Background(), Problem{},
		RunOptions{Observer: func(progress Progress) {
			costs = append(costs, progress.BestCost)
			if progress.Diagnostics == nil {
				t.Errorf("progress %v lost its diagnostics", progress)
				return
			}

			spreads = append(spreads, progress.Diagnostics.PopulationSpread)
		}},
	)
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	wantSpreads := []float64{103, 3, 110, 10}
	if !slices.Equal(spreads, wantSpreads) {
		t.Fatalf("diagnostics = %v, want %v (every attempt's population, uncensored)", spreads, wantSpreads)
	}

	// The reported incumbent stays monotonic even though the raw reports did not.
	wantCosts := []float64{103, 3, 3, 3}
	if !slices.Equal(costs, wantCosts) {
		t.Fatalf("reported costs = %v, want %v (clamped to the running best)", costs, wantCosts)
	}
}

// An observer that persists a checkpoint must never be handed a candidate
// worse than one it has already stored.
func TestWithRestartsBoundaryCarriesTheRunningBest(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{3, 10}, iterations: 5}

	var boundaries []float64

	_, err := WithRestarts(base, 2).(LifecycleOptimizer).RunContext(
		context.Background(), Problem{},
		RunOptions{EpochObserver: func(b EpochBoundary) error {
			boundaries = append(boundaries, b.Progress.BestCost)
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	want := []float64{3, 3}
	if len(boundaries) != len(want) {
		t.Fatalf("boundaries = %v, want %v", boundaries, want)
	}

	for i := range want {
		if boundaries[i] != want[i] {
			t.Fatalf("boundary %d reported %v, want %v (the running best, not the attempt)",
				i+1, boundaries[i], want[i])
		}
	}
}

func TestWithRestartsBelowTwoIsTheIdentity(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{1}}
	for _, restarts := range []int{-1, 0, 1} {
		if got := WithRestarts(base, restarts); got != Optimizer(base) {
			t.Fatalf("WithRestarts(base, %d) wrapped the optimizer; one attempt must be unchanged", restarts)
		}
	}

	if WithRestarts(nil, 4) != nil {
		t.Fatal("WithRestarts(nil, 4) must stay nil")
	}
}

// Restarts wrap epochs at every construction site, so the composition has to
// hold: each attempt runs its own epoch chain and the attempts stay distinct.
func TestWithRestartsComposesWithEpochsWithoutSeedAliasing(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{9, 8, 7, 6}, iterations: 5}

	_, err := WithRestarts(WithEpochs(base, 2), 2).(LifecycleOptimizer).
		RunContext(context.Background(), Problem{}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	if len(base.seen) != 4 {
		t.Fatalf("ran %d underlying runs, want 4 (2 attempts x 2 epochs)", len(base.seen))
	}

	type key struct{ resume, offset int }

	seen := map[key]bool{}

	for _, options := range base.seen {
		k := key{options.ResumeCount, options.SeedOffset}
		if seen[k] {
			t.Fatalf("two runs shared (ResumeCount %d, SeedOffset %d)", k.resume, k.offset)
		}

		seen[k] = true
	}
}

// With epochs nested inside attempts the inner optimizer owns the boundaries.
// They have to reach the caller: the server persists a checkpoint per boundary,
// and swallowing them until the whole epoch chain finished would drop every
// intermediate checkpoint of an attempt.
func TestWithRestartsForwardsNestedEpochBoundaries(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{9, 8, 7, 6}, iterations: 5}

	var boundaries []EpochBoundary

	_, err := WithRestarts(WithEpochs(base, 2), 2).(LifecycleOptimizer).RunContext(
		context.Background(), Problem{},
		RunOptions{EpochObserver: func(b EpochBoundary) error {
			boundaries = append(boundaries, b)
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	if len(boundaries) != 4 {
		t.Fatalf("reported %d boundaries, want 4 (2 attempts x 2 epochs)", len(boundaries))
	}

	wantCosts := []float64{9, 8, 7, 6}
	for i, boundary := range boundaries {
		if boundary.Epoch != i+1 {
			t.Fatalf("boundary %d numbered %d; the count runs across attempts and must not restart",
				i+1, boundary.Epoch)
		}

		if boundary.Progress.BestCost != wantCosts[i] {
			t.Fatalf("boundary %d cost = %v, want %v", i+1, boundary.Progress.BestCost, wantCosts[i])
		}

		if boundary.Progress.Iterations != 5*(i+1) || boundary.Progress.Evaluations != 100*(i+1) {
			t.Fatalf("boundary %d work = %d/%d, want %d/%d accumulated across attempts",
				i+1, boundary.Progress.Iterations, boundary.Progress.Evaluations, 5*(i+1), 100*(i+1))
		}
	}
}

// A fresh attempt's early epochs are worse than what an earlier attempt already
// reached, and an observer that persists them must not be handed that
// regression.
func TestWithRestartsNestedBoundariesCarryTheRunningBest(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{3, 4, 10, 5}, iterations: 5}

	var boundaries []EpochBoundary

	_, err := WithRestarts(WithEpochs(base, 2), 2).(LifecycleOptimizer).RunContext(
		context.Background(), Problem{},
		RunOptions{EpochObserver: func(b EpochBoundary) error {
			boundaries = append(boundaries, b)
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}

	if len(boundaries) != 4 {
		t.Fatalf("reported %d boundaries, want 4", len(boundaries))
	}

	for i, boundary := range boundaries {
		if boundary.Progress.BestCost != 3 {
			t.Fatalf("boundary %d cost = %v, want the running best 3", i+1, boundary.Progress.BestCost)
		}

		if len(boundary.Progress.BestParams) != 1 || boundary.Progress.BestParams[0] != 3 {
			t.Fatalf("boundary %d params = %v, want the running best candidate [3]",
				i+1, boundary.Progress.BestParams)
		}
	}
}

// The documented contract: an observer error aborts the remaining work, so a
// persistence failure cannot be silently ignored for the rest of the run.
func TestWithRestartsNestedBoundaryErrorAborts(t *testing.T) {
	base := &recordingOptimizer{costs: []float64{9, 8, 7, 6}, iterations: 5}
	failure := errors.New("checkpoint failed")

	calls := 0

	_, err := WithRestarts(WithEpochs(base, 2), 2).(LifecycleOptimizer).RunContext(
		context.Background(), Problem{},
		RunOptions{EpochObserver: func(EpochBoundary) error {
			calls++
			if calls == 2 {
				return failure
			}

			return nil
		}},
	)

	if !errors.Is(err, failure) {
		t.Fatalf("RunContext error = %v, want the observer's error", err)
	}

	if calls != 2 {
		t.Fatalf("observer ran %d times, want 2; the error must abort the remaining epochs", calls)
	}

	if len(base.seen) != 2 {
		t.Fatalf("ran %d underlying runs after the abort, want 2", len(base.seen))
	}
}
