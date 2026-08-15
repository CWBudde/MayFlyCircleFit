package opt

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

// Sphere function: f(x) = sum(x_i^2), minimum at origin
func sphere(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return sum
}

func TestMayflyAdapterLifecycleProgressAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	optimizer := NewMayfly(100, 20, 42).(*MayflyAdapter)
	var updates []Progress
	result, err := optimizer.RunContext(ctx, Problem{
		Eval: sphere, Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2,
	}, RunOptions{Observer: func(progress Progress) {
		updates = append(updates, progress)
		progress.BestParams[0] = 999
		cancel()
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext error = %v, want context.Canceled", err)
	}
	if result.Termination != TerminationCancelled || result.Iterations != 1 || result.Evaluations == 0 {
		t.Fatalf("unexpected cancelled result: %+v", result)
	}
	if len(updates) != 1 {
		t.Fatalf("observer updates = %d, want 1", len(updates))
	}
	if len(result.BestParams) == 0 || result.BestParams[0] == 999 {
		t.Fatal("observer received a mutable optimizer snapshot")
	}
}

func TestMayflyAdapterRepairsEvaluationsProgressAndResult(t *testing.T) {
	const repairedValue = 0.75
	optimizer := NewMayfly(2, 20, 42).(*MayflyAdapter)
	observations := 0
	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: func(params []float64) float64 {
			if params[0] != repairedValue {
				t.Fatalf("evaluated parameter = %g, want repaired value %g", params[0], repairedValue)
			}
			return params[0]
		},
		Repair: func(params []float64) { params[0] = repairedValue },
		Lower:  []float64{0},
		Upper:  []float64{1},
		Dim:    1,
	}, RunOptions{Observer: func(progress Progress) {
		observations++
		if len(progress.BestParams) != 1 || progress.BestParams[0] != repairedValue {
			t.Fatalf("progress params = %v, want [%g]", progress.BestParams, repairedValue)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if observations == 0 {
		t.Fatal("observer received no progress")
	}
	if len(result.BestParams) != 1 || result.BestParams[0] != repairedValue {
		t.Fatalf("result params = %v, want [%g]", result.BestParams, repairedValue)
	}
}

func TestMayflyAdapterMapsProgressWithoutChangingLocalResult(t *testing.T) {
	optimizer := NewMayfly(2, 20, 42).(*MayflyAdapter)
	var mapped Progress
	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: sphere, Lower: []float64{-1}, Upper: []float64{1}, Dim: 1,
	}, RunOptions{
		ProgressMapper: func(progress Progress) Progress {
			progress.BestParams = append([]float64{7}, progress.BestParams...)
			return progress
		},
		Observer: func(progress Progress) { mapped = progress },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.BestParams) != 2 || mapped.BestParams[0] != 7 {
		t.Fatalf("mapped progress params = %v, want staged prefix", mapped.BestParams)
	}
	if len(result.BestParams) != 1 {
		t.Fatalf("optimizer-local result params = %v, want one dimension", result.BestParams)
	}
}

func TestMayflyAdapterEvaluatesInequalitiesOnCanonicalParameters(t *testing.T) {
	const repairedValue = 7.5
	var constraintCalls int
	optimizer := NewMayfly(2, 20, 42).(*MayflyAdapter)
	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: func(params []float64) float64 {
			if params[0] != repairedValue {
				t.Fatalf("objective parameter = %g, want repaired value %g", params[0], repairedValue)
			}
			return params[0] * params[0]
		},
		Repair: func(params []float64) { params[0] = repairedValue },
		Inequalities: []InequalityConstraint{func(params []float64) float64 {
			constraintCalls++
			if params[0] != repairedValue {
				t.Fatalf("constraint parameter = %g, want repaired value %g", params[0], repairedValue)
			}
			return 7 - params[0]
		}},
		Lower: []float64{0},
		Upper: []float64{10},
		Dim:   1,
	}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if constraintCalls == 0 {
		t.Fatal("inequality constraint was not evaluated")
	}
	if result.BestParams[0] != repairedValue || result.BestCost != repairedValue*repairedValue {
		t.Fatalf("result = (%v, %g), want ([%g], %g)",
			result.BestParams, result.BestCost, repairedValue, repairedValue*repairedValue)
	}
}

func TestMayflyAdapterUsesFeasibilityAndReportsRawCost(t *testing.T) {
	const minimum = 0.75
	var updates []Progress
	problem := Problem{
		Eval: func(params []float64) float64 { return params[0] * params[0] },
		Inequalities: []InequalityConstraint{func(params []float64) float64 {
			return minimum - params[0]
		}},
		Lower: []float64{0},
		Upper: []float64{1},
		Dim:   1,
	}
	optimizer := NewMayfly(20, 20, 42).(*MayflyAdapter)
	result, err := optimizer.RunContext(context.Background(), problem, RunOptions{
		Observer: func(progress Progress) { updates = append(updates, progress) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) == 0 {
		t.Fatal("observer received no progress")
	}
	for _, progress := range updates {
		if progress.BestParams[0] < minimum {
			t.Fatalf("progress exposed infeasible best: %+v", progress)
		}
		wantCost := progress.BestParams[0] * progress.BestParams[0]
		if math.Abs(progress.BestCost-wantCost) > 1e-12 {
			t.Fatalf("progress cost = %g, want raw objective %g", progress.BestCost, wantCost)
		}
	}
	if result.BestParams[0] < minimum {
		t.Fatalf("result params = %v, want a feasible value >= %g", result.BestParams, minimum)
	}
	wantCost := result.BestParams[0] * result.BestParams[0]
	if math.Abs(result.BestCost-wantCost) > 1e-12 {
		t.Fatalf("result cost = %g, want raw objective %g", result.BestCost, wantCost)
	}
}

func TestMayflyAdapterFeasibleResultReplacesCheaperInfeasibleResumeSeed(t *testing.T) {
	const minimum = 0.5
	problem := Problem{
		Eval: func(params []float64) float64 { return params[0] },
		Inequalities: []InequalityConstraint{func(params []float64) float64 {
			return minimum - params[0]
		}},
		Lower: []float64{0},
		Upper: []float64{1},
		Dim:   1,
	}
	optimizer := NewMayfly(5, 20, 42).(*MayflyAdapter)
	result, err := optimizer.RunContext(context.Background(), problem, RunOptions{
		Initial: &Candidate{Params: []float64{0}, Cost: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BestParams[0] < minimum {
		t.Fatalf("result retained infeasible resume seed: %+v", result)
	}
	if result.BestCost != result.BestParams[0] {
		t.Fatalf("result cost = %g, want raw objective %g", result.BestCost, result.BestParams[0])
	}
}

func TestMayflyAdapterRejectsNilInequality(t *testing.T) {
	optimizer := NewMayfly(1, 20, 42).(*MayflyAdapter)
	_, err := optimizer.RunContext(context.Background(), Problem{
		Eval:         sphere,
		Inequalities: []InequalityConstraint{nil},
		Lower:        []float64{-1},
		Upper:        []float64{1},
		Dim:          1,
	}, RunOptions{})
	if err == nil || err.Error() != "inequality constraint 0 is nil" {
		t.Fatalf("RunContext error = %v, want nil-constraint validation error", err)
	}
}

func TestMayflyAdapterSeedsResumePopulationAroundBest(t *testing.T) {
	initial := Candidate{Params: []float64{2, -3}, Cost: 13}
	var firstEvaluation []float64
	objective := func(params []float64) float64 {
		if firstEvaluation == nil {
			firstEvaluation = append([]float64(nil), params...)
		}
		return sphere(params)
	}
	optimizer := NewMayfly(1, 20, 42).(*MayflyAdapter)
	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: objective, Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2,
	}, RunOptions{Initial: &initial, ResumeCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if firstEvaluation[0] != initial.Params[0] || firstEvaluation[1] != initial.Params[1] {
		t.Fatalf("first population member = %v, want exact saved best %v", firstEvaluation, initial.Params)
	}
	if result.BestCost > initial.Cost {
		t.Fatalf("resume worsened cost: got %v, initial %v", result.BestCost, initial.Cost)
	}
	if result.Evaluations == 0 || result.Iterations != 1 {
		t.Fatalf("missing measured work: %+v", result)
	}
}

func TestMayflyAdapterMixesIncumbentAndAlternativeSeedPopulations(t *testing.T) {
	incumbent := Candidate{Params: []float64{-3, 2}, Cost: 13}
	alternative := Candidate{Params: []float64{4, -1}, Cost: 17}
	var evaluations [][]float64
	optimizer := NewMayfly(1, 20, 42).(*MayflyAdapter)
	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: func(params []float64) float64 {
			evaluations = append(evaluations, append([]float64(nil), params...))
			return sphere(params)
		},
		Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2,
	}, RunOptions{
		Initial:         &incumbent,
		AdditionalSeeds: []Candidate{alternative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluations) < 2 || !reflect.DeepEqual(evaluations[0], incumbent.Params) || !reflect.DeepEqual(evaluations[1], alternative.Params) {
		t.Fatalf("first mixed seeds = %v, want incumbent %v then alternative %v", evaluations[:min(2, len(evaluations))], incumbent.Params, alternative.Params)
	}
	if result.BestCost > incumbent.Cost {
		t.Fatalf("mixed continuation lost incumbent: %+v", result)
	}
}

func TestContinuationSeedIsStableAndAdvances(t *testing.T) {
	first := continuationSeed(42, 1)
	if first != continuationSeed(42, 1) {
		t.Fatal("continuation seed is not deterministic")
	}
	if first == continuationSeed(42, 2) || first == 42 {
		t.Fatal("continuation seed did not advance")
	}
}

func TestSeededPopulationHonorsLocalContinuationProfile(t *testing.T) {
	profile := &ContinuationProfile{
		LocalFraction:  1,
		Sigma:          0.01,
		CoordinateRate: 0.1,
		MaxVelocity:    0.02,
	}
	males, females := seededPopulationFromCandidates(
		[][]float64{{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5}},
		20,
		rand.New(rand.NewSource(42)),
		profile,
	)
	if len(males) != 20 || len(females) != 20 {
		t.Fatalf("local seeded populations = %d/%d, want 20/20", len(males), len(females))
	}
	if !reflect.DeepEqual(males[0], []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5}) {
		t.Fatalf("exact incumbent seed = %v", males[0])
	}
	for populationIndex, population := range [][][]float64{males[1:], females} {
		for seedIndex, seed := range population {
			changed := 0
			for _, value := range seed {
				if value != 0.5 {
					changed++
				}
			}
			if changed == 0 || changed == len(seed) {
				t.Fatalf("population %d seed %d changed %d coordinates, want a non-empty sparse perturbation", populationIndex, seedIndex, changed)
			}
		}
	}
}

func TestMayflyAdapterRejectsInvalidContinuationProfile(t *testing.T) {
	optimizer := NewMayfly(1, 20, 42).(*MayflyAdapter)
	_, err := optimizer.RunContext(context.Background(), Problem{
		Eval: sphere, Lower: []float64{-1}, Upper: []float64{1}, Dim: 1,
	}, RunOptions{
		Initial:      &Candidate{Params: []float64{0}, Cost: 0},
		Continuation: &ContinuationProfile{LocalFraction: 1, Sigma: 0.02, CoordinateRate: 0},
	})
	if err == nil || err.Error() != "continuation coordinate rate must be in (0,1]" {
		t.Fatalf("invalid continuation error = %v", err)
	}
}

func TestMayflyAdapterOnSphere(t *testing.T) {
	optimizer := NewMayfly(100, 20, 42) // maxIters, popSize, seed

	dim := 3
	lower := make([]float64, dim)
	upper := make([]float64, dim)
	for i := 0; i < dim; i++ {
		lower[i] = -10
		upper[i] = 10
	}

	best, cost := optimizer.Run(sphere, lower, upper, dim)

	if len(best) != dim {
		t.Fatalf("Expected %d parameters, got %d", dim, len(best))
	}

	// Should converge close to zero
	if cost > 0.1 {
		t.Errorf("Expected cost near 0, got %f", cost)
	}

	// Check that best params are near origin
	for i, v := range best {
		if math.Abs(v) > 1.0 {
			t.Errorf("Parameter %d = %f, expected near 0", i, v)
		}
	}
}

func TestMayflyAdapterDeterministic(t *testing.T) {
	dim := 2
	lower := []float64{-5, -5}
	upper := []float64{5, 5}

	// Run twice with same seed (popSize must be >=20 for mayfly v0.1.0)
	optimizer1 := NewMayfly(50, 20, 123)
	_, cost1 := optimizer1.Run(sphere, lower, upper, dim)

	optimizer2 := NewMayfly(50, 20, 123)
	_, cost2 := optimizer2.Run(sphere, lower, upper, dim)

	if cost1 != cost2 {
		t.Errorf("Non-deterministic: cost1=%f, cost2=%f", cost1, cost2)
	}
}

func TestMayflyAdapter_RunWithInitial(t *testing.T) {
	dim := 3
	lower := make([]float64, dim)
	upper := make([]float64, dim)
	for i := 0; i < dim; i++ {
		lower[i] = -10
		upper[i] = 10
	}

	// Start with a suboptimal initial solution
	initialParams := []float64{5.0, 5.0, 5.0}
	initialCost := sphere(initialParams) // Should be 75.0

	// Cast to ResumableOptimizer
	optimizer := NewMayfly(100, 20, 42)
	resumable, ok := optimizer.(ResumableOptimizer)
	if !ok {
		t.Fatal("MayflyAdapter should implement ResumableOptimizer")
	}

	// Run with initial solution
	best, cost := resumable.RunWithInitial(initialParams, initialCost, sphere, lower, upper, dim)

	// Should improve from initial cost
	if cost >= initialCost {
		t.Errorf("Expected improvement: initial=%f, final=%f", initialCost, cost)
	}

	// Should converge close to zero
	if cost > 0.1 {
		t.Errorf("Expected cost near 0, got %f", cost)
	}

	// Check that best params are near origin
	for i, v := range best {
		if math.Abs(v) > 1.0 {
			t.Errorf("Parameter %d = %f, expected near 0", i, v)
		}
	}
}

func TestMayflyAdapter_RunWithInitial_AlreadyOptimal(t *testing.T) {
	dim := 2
	lower := []float64{-10, -10}
	upper := []float64{10, 10}

	// Start with optimal solution (at origin)
	initialParams := []float64{0.0, 0.0}
	initialCost := sphere(initialParams) // Should be 0.0

	optimizer := NewMayfly(50, 20, 42)
	resumable := optimizer.(ResumableOptimizer)

	// Run with initial solution
	_, cost := resumable.RunWithInitial(initialParams, initialCost, sphere, lower, upper, dim)

	// Should stay at or near optimal
	if cost > 0.01 {
		t.Errorf("Expected cost near 0, got %f", cost)
	}
}

func TestMayflyAdapter_RunWithInitial_VsFromScratch(t *testing.T) {
	dim := 3
	lower := make([]float64, dim)
	upper := make([]float64, dim)
	for i := 0; i < dim; i++ {
		lower[i] = -10
		upper[i] = 10
	}

	// Run from scratch with limited iterations
	optimizer1 := NewMayfly(50, 20, 42)
	_, costFromScratch := optimizer1.Run(sphere, lower, upper, dim)

	// Get intermediate solution after 50 iterations (simulated checkpoint)
	optimizer2 := NewMayfly(50, 20, 42)
	intermediateParams, intermediateCost := optimizer2.Run(sphere, lower, upper, dim)

	// Resume from intermediate solution with more iterations
	optimizer3 := NewMayfly(100, 20, 43) // Different seed for resumed run
	resumable := optimizer3.(ResumableOptimizer)
	_, costResumed := resumable.RunWithInitial(intermediateParams, intermediateCost, sphere, lower, upper, dim)

	// Resumed run should do at least as well as intermediate (may improve)
	if costResumed > intermediateCost*1.1 { // Allow 10% tolerance for stochastic variation
		t.Errorf("Resumed cost (%f) worse than intermediate (%f)", costResumed, intermediateCost)
	}

	t.Logf("From scratch (50 iters): %f", costFromScratch)
	t.Logf("Intermediate (50 iters): %f", intermediateCost)
	t.Logf("Resumed (100 iters): %f", costResumed)
}

func TestMayflyAdapter_RunWithInitial_KeepsCheckpointIfBetter(t *testing.T) {
	dim := 2
	lower := []float64{-10, -10}
	upper := []float64{10, 10}

	// Start with a very good solution
	initialParams := []float64{0.01, 0.01}
	initialCost := sphere(initialParams) // Very close to optimal

	// Use very few iterations so optimizer unlikely to beat initial solution
	optimizer := NewMayfly(5, 20, 42)
	resumable := optimizer.(ResumableOptimizer)

	// Run with initial solution
	best, cost := resumable.RunWithInitial(initialParams, initialCost, sphere, lower, upper, dim)

	// Should keep the initial solution if optimizer didn't find better
	// (Allow either optimizer found better OR kept initial)
	if cost > initialCost {
		t.Errorf("Resume should never worsen: initial=%f, final=%f", initialCost, cost)
	}

	t.Logf("Initial cost: %f, Final cost: %f", initialCost, cost)
	t.Logf("Initial params: %v, Final params: %v", initialParams, best)
}

func TestNewMayflyVariantSelectsVariant(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "", want: "standard"},
		{name: "standard", want: "standard"},
		{name: "desma", want: "desma"},
		{name: "olce", want: "olce"},
		{name: "aoblmoa", want: "aoblmoa"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			optimizer, err := NewMayflyVariant(test.name, 10, 20, 1)
			if err != nil {
				t.Fatalf("NewMayflyVariant(%q) error = %v", test.name, err)
			}
			adapter, ok := optimizer.(*MayflyAdapter)
			if !ok {
				t.Fatalf("NewMayflyVariant returned %T, want *MayflyAdapter", optimizer)
			}
			if adapter.variant != test.want {
				t.Fatalf("variant = %q, want %q", adapter.variant, test.want)
			}
		})
	}
}

func TestNewMayflyVariantRejectsUnknownName(t *testing.T) {
	optimizer, err := NewMayflyVariant("nope", 10, 20, 1)
	if !errors.Is(err, ErrUnknownVariant) {
		t.Fatalf("error = %v, want ErrUnknownVariant", err)
	}
	if optimizer != nil {
		t.Fatalf("optimizer = %v, want nil on error", optimizer)
	}
}

// TestNewMayflyVariantMatchesNamedConstructors pins that routing a variant name
// through the factory is identical to calling the dedicated constructor, so the
// three call sites gain variant support without changing optimizer behavior.
func TestNewMayflyVariantMatchesNamedConstructors(t *testing.T) {
	tests := []struct {
		name  string
		named Optimizer
	}{
		{name: "", named: NewMayfly(30, 20, 7)},
		{name: "standard", named: NewMayfly(30, 20, 7)},
		{name: "desma", named: NewMayflyDESMA(30, 20, 7)},
		{name: "olce", named: NewMayflyOLCE(30, 20, 7)},
	}

	problem := Problem{Eval: sphere, Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			byName, err := NewMayflyVariant(test.name, 30, 20, 7)
			if err != nil {
				t.Fatalf("NewMayflyVariant(%q) error = %v", test.name, err)
			}
			want, err := test.named.(LifecycleOptimizer).RunContext(context.Background(), problem, RunOptions{})
			if err != nil {
				t.Fatalf("named constructor run error = %v", err)
			}
			got, err := byName.(LifecycleOptimizer).RunContext(context.Background(), problem, RunOptions{})
			if err != nil {
				t.Fatalf("variant factory run error = %v", err)
			}
			if got.BestCost != want.BestCost || !slices.Equal(got.BestParams, want.BestParams) {
				t.Fatalf("variant %q result = (%v, %v), want (%v, %v)",
					test.name, got.BestParams, got.BestCost, want.BestParams, want.BestCost)
			}
		})
	}
}

func sphereProblem() Problem {
	return Problem{Eval: sphere, Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2}
}

func runAdapter(t *testing.T, optimizer Optimizer) Result {
	t.Helper()

	result, err := optimizer.(LifecycleOptimizer).RunContext(context.Background(), sphereProblem(), RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}
	return result
}

// TestMayflyAdapterDefaultsAreUnchanged is the reproducibility gate for the
// option surface: constructing an adapter with zero-valued options must be
// bit-identical to constructing one without them, so that an unconfigured run
// behaves exactly as it did before early stopping and logging existed.
func TestMayflyAdapterDefaultsAreUnchanged(t *testing.T) {
	want := runAdapter(t, NewMayfly(40, 20, 42))

	withZeroOptions := runAdapter(t, NewMayfly(40, 20, 42, WithEarlyStop(Stop{}), WithLogger(nil)))
	if withZeroOptions.BestCost != want.BestCost || !slices.Equal(withZeroOptions.BestParams, want.BestParams) {
		t.Fatalf("zero options changed the result: got (%v, %v), want (%v, %v)",
			withZeroOptions.BestParams, withZeroOptions.BestCost, want.BestParams, want.BestCost)
	}
	if withZeroOptions.Iterations != want.Iterations || withZeroOptions.Evaluations != want.Evaluations {
		t.Fatalf("zero options changed measured work: got %d/%d, want %d/%d",
			withZeroOptions.Iterations, withZeroOptions.Evaluations, want.Iterations, want.Evaluations)
	}

	viaVariant, err := NewMayflyVariant("", 40, 20, 42)
	if err != nil {
		t.Fatalf("NewMayflyVariant() error = %v", err)
	}
	if got := runAdapter(t, viaVariant); got.BestCost != want.BestCost || !slices.Equal(got.BestParams, want.BestParams) {
		t.Fatalf("variant factory changed the result: got (%v, %v), want (%v, %v)",
			got.BestParams, got.BestCost, want.BestParams, want.BestCost)
	}

	if want.Termination != TerminationCompleted {
		t.Fatalf("Termination = %q, want %q", want.Termination, TerminationCompleted)
	}
	if want.Iterations != 40 {
		t.Fatalf("Iterations = %d, want the full budget of 40", want.Iterations)
	}
}

func TestMayflyAdapterStopsOnTargetCost(t *testing.T) {
	result := runAdapter(t, NewMayfly(500, 20, 42, WithEarlyStop(Stop{TargetCost: 1e-2, MinIters: 1})))

	if result.Termination != TerminationTargetCost {
		t.Fatalf("Termination = %q, want %q", result.Termination, TerminationTargetCost)
	}
	if result.Iterations >= 500 {
		t.Fatalf("Iterations = %d, want fewer than the 500 budget", result.Iterations)
	}
	if result.BestCost > 1e-2 {
		t.Fatalf("BestCost = %v, want at or below the 0.01 target", result.BestCost)
	}
}

func TestMayflyAdapterStopsOnStagnation(t *testing.T) {
	constant := func([]float64) float64 { return 1 }
	optimizer := NewMayfly(100, 20, 42, WithEarlyStop(Stop{StagnationIters: 3, MinIters: 1}))
	result, err := optimizer.(LifecycleOptimizer).RunContext(context.Background(), Problem{
		Eval: constant, Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != TerminationStagnation {
		t.Fatalf("Termination = %q, want %q", result.Termination, TerminationStagnation)
	}
	if result.Iterations >= 100 {
		t.Fatalf("Iterations = %d, want far fewer than the 100 budget", result.Iterations)
	}
}

func TestMayflyAdapterMinItersDelaysStop(t *testing.T) {
	const minIters = 20
	constant := func([]float64) float64 { return 1 }
	optimizer := NewMayfly(100, 20, 42, WithEarlyStop(Stop{StagnationIters: 1, MinIters: minIters}))
	result, err := optimizer.(LifecycleOptimizer).RunContext(context.Background(), Problem{
		Eval: constant, Lower: []float64{-10, -10}, Upper: []float64{10, 10}, Dim: 2,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Iterations < minIters {
		t.Fatalf("Iterations = %d, want at least the %d minimum", result.Iterations, minIters)
	}
}

// TestMayflyAdapterTargetCostUsesObjectiveUnits pins that the target is compared
// against the objective's own value. The adapter normalizes positions to [0,1],
// so a target interpreted in normalized space would behave differently.
func TestMayflyAdapterTargetCostUsesObjectiveUnits(t *testing.T) {
	// Costs are always 500: above the target in objective units, but below it if
	// anything were to rescale them.
	constant := func([]float64) float64 { return 500 }
	optimizer := NewMayfly(15, 20, 42, WithEarlyStop(Stop{TargetCost: 100, MinIters: 1}))
	result, err := optimizer.(LifecycleOptimizer).RunContext(context.Background(), Problem{
		Eval: constant, Lower: []float64{-1000, -1000}, Upper: []float64{1000, 1000}, Dim: 2,
	}, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != TerminationCompleted {
		t.Fatalf("Termination = %q, want %q: a cost of 500 must not satisfy a target of 100",
			result.Termination, TerminationCompleted)
	}
	if result.Iterations != 15 {
		t.Fatalf("Iterations = %d, want the full budget of 15", result.Iterations)
	}
}

// TestMayflyAdapterClampsMinItersToMaxIterations covers resume, which reads a
// persisted configuration without renormalizing it. Mayfly rejects a minimum
// above the iteration cap, so the adapter clamps instead of erroring.
func TestMayflyAdapterClampsMinItersToMaxIterations(t *testing.T) {
	// Without the clamp this run fails with an opaque optimizer validation
	// error. With it, the minimum lands on the last iteration, so the run still
	// consumes its whole budget; the reported reason may be either "completed"
	// or a criterion that first became eligible on that final iteration.
	result := runAdapter(t, NewMayfly(10, 20, 42, WithEarlyStop(Stop{StagnationIters: 1, MinIters: 1000})))

	if result.Iterations != 10 {
		t.Fatalf("Iterations = %d, want the full budget of 10", result.Iterations)
	}
	if result.Termination == TerminationCancelled {
		t.Fatalf("Termination = %q, want a non-cancelled reason", result.Termination)
	}
}
