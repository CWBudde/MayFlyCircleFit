package opt_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// dragonflySphere is f(x) = sum(x_i^2), minimized at the origin.
func dragonflySphere(params []float64) float64 {
	var sum float64
	for _, value := range params {
		sum += value * value
	}

	return sum
}

// dragonflyProblem is the smallest bounded problem that still exercises
// normalization: asymmetric bounds per dimension, minimum away from the origin.
func dragonflyProblem() opt.Problem {
	return opt.Problem{
		Eval:  dragonflySphere,
		Lower: []float64{-5, -1, 0},
		Upper: []float64{5, 9, 2},
		Dim:   3,
	}
}

// dragonflyLifecycle builds an adapter and asserts the lifecycle interface the
// pipelines actually drive it through.
func dragonflyLifecycle(t *testing.T, optimizer opt.Optimizer) opt.LifecycleOptimizer {
	t.Helper()

	lifecycle, ok := optimizer.(opt.LifecycleOptimizer)
	if !ok {
		t.Fatalf("optimizer %T is not a LifecycleOptimizer", optimizer)
	}

	return lifecycle
}

func TestDragonflyAdapterSatisfiesTheOptimizerInterfaces(t *testing.T) {
	t.Parallel()

	optimizer := opt.NewDragonfly(10, 8, 1)

	if _, ok := optimizer.(opt.LifecycleOptimizer); !ok {
		t.Error("NewDragonfly does not return a LifecycleOptimizer")
	}

	if _, ok := optimizer.(opt.ResumableOptimizer); !ok {
		t.Error("NewDragonfly does not return a ResumableOptimizer")
	}

	if budget := opt.StageIterationBudget(optimizer); budget != 10 {
		t.Errorf("StageIterationBudget() = %d, want 10", budget)
	}

	if width := opt.ParallelEvaluationWidth(optimizer); width != 1 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 1", width)
	}

	parallel := opt.NewDragonfly(10, 8, 1, opt.WithDragonflyParallelEvaluation(4))
	if width := opt.ParallelEvaluationWidth(parallel); width != 4 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 4", width)
	}
}

func TestDragonflyAdapterRunStaysInBoundsAndReportsWork(t *testing.T) {
	t.Parallel()

	optimizer := dragonflyLifecycle(t, opt.NewDragonfly(20, 10, 42))
	problem := dragonflyProblem()

	result, err := optimizer.RunContext(context.Background(), problem, opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != opt.TerminationCompleted {
		t.Errorf("Termination = %q, want %q", result.Termination, opt.TerminationCompleted)
	}

	if result.Iterations != 20 || result.Evaluations == 0 {
		t.Errorf("unexpected measured work: %+v", result)
	}

	if len(result.BestParams) != problem.Dim {
		t.Fatalf("BestParams length = %d, want %d", len(result.BestParams), problem.Dim)
	}

	for i, value := range result.BestParams {
		if value < problem.Lower[i] || value > problem.Upper[i] {
			t.Errorf("BestParams[%d] = %v, outside [%v,%v]", i, value, problem.Lower[i], problem.Upper[i])
		}
	}

	if math.Abs(result.BestCost-dragonflySphere(result.BestParams)) > 1e-9 {
		t.Errorf("BestCost = %v, does not match the objective at BestParams", result.BestCost)
	}
}

func TestDragonflyAdapterIsDeterministicForAFixedSeed(t *testing.T) {
	t.Parallel()

	first, err := dragonflyLifecycle(t, opt.NewDragonfly(15, 10, 7)).
		RunContext(context.Background(), dragonflyProblem(), opt.RunOptions{})
	if err != nil {
		t.Fatalf("first RunContext() error = %v", err)
	}

	second, err := dragonflyLifecycle(t, opt.NewDragonfly(15, 10, 7)).
		RunContext(context.Background(), dragonflyProblem(), opt.RunOptions{})
	if err != nil {
		t.Fatalf("second RunContext() error = %v", err)
	}

	if first.BestCost != second.BestCost {
		t.Errorf("best cost = %v and %v for one seed, want identical runs", first.BestCost, second.BestCost)
	}
}

func TestDragonflyAdapterReportsProgressAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	optimizer := dragonflyLifecycle(t, opt.NewDragonfly(100, 10, 42))

	var updates []opt.Progress

	result, err := optimizer.RunContext(ctx, dragonflyProblem(), opt.RunOptions{
		Observer: func(progress opt.Progress) {
			updates = append(updates, progress)
			progress.BestParams[0] = 999

			cancel()
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext() error = %v, want context.Canceled", err)
	}

	if result.Termination != opt.TerminationCancelled {
		t.Errorf("Termination = %q, want %q", result.Termination, opt.TerminationCancelled)
	}

	if len(updates) != 1 {
		t.Fatalf("observer updates = %d, want 1", len(updates))
	}

	if len(result.BestParams) == 0 || result.BestParams[0] == 999 {
		t.Error("observer received a mutable optimizer snapshot")
	}
}

func TestDragonflyAdapterRepairsEveryCandidateItExposes(t *testing.T) {
	t.Parallel()

	const repairedValue = 0.75

	optimizer := dragonflyLifecycle(t, opt.NewDragonfly(3, 10, 42))
	observed := 0

	result, err := optimizer.RunContext(context.Background(), opt.Problem{
		Eval: func(params []float64) float64 {
			if params[0] != repairedValue {
				t.Errorf("objective saw an unrepaired candidate: %v", params)
			}

			return dragonflySphere(params)
		},
		Repair: func(params []float64) { params[0] = repairedValue },
		Lower:  []float64{-5, -5},
		Upper:  []float64{5, 5},
		Dim:    2,
	}, opt.RunOptions{Observer: func(progress opt.Progress) {
		observed++

		if progress.BestParams[0] != repairedValue {
			t.Errorf("progress reported an unrepaired candidate: %v", progress.BestParams)
		}
	}})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if observed == 0 {
		t.Error("no progress was reported")
	}

	if result.BestParams[0] != repairedValue {
		t.Errorf("BestParams = %v, want a repaired first component", result.BestParams)
	}
}

func TestDragonflyAdapterNeverLosesTheInitialCandidate(t *testing.T) {
	t.Parallel()

	initial := opt.Candidate{Params: []float64{0, 0, 0}, Cost: dragonflySphere([]float64{0, 0, 0})}
	optimizer := dragonflyLifecycle(t, opt.NewDragonfly(5, 10, 42))

	result, err := optimizer.RunContext(context.Background(), dragonflyProblem(), opt.RunOptions{
		Initial:     &initial,
		ResumeCount: 1,
	})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.BestCost > initial.Cost {
		t.Errorf("BestCost = %v, worse than the seeded candidate %v", result.BestCost, initial.Cost)
	}
}

func TestDragonflyAdapterRejectsAnInvalidInitialCandidate(t *testing.T) {
	t.Parallel()

	optimizer := dragonflyLifecycle(t, opt.NewDragonfly(5, 10, 42))

	_, err := optimizer.RunContext(context.Background(), dragonflyProblem(), opt.RunOptions{
		Initial: &opt.Candidate{Params: []float64{0, 0}, Cost: 0},
	})
	if err == nil {
		t.Fatal("RunContext() accepted a candidate with the wrong dimension")
	}
}

func TestDragonflyAdapterRejectsANegativeResumeCount(t *testing.T) {
	t.Parallel()

	optimizer := dragonflyLifecycle(t, opt.NewDragonfly(5, 10, 42))

	_, err := optimizer.RunContext(context.Background(), dragonflyProblem(), opt.RunOptions{ResumeCount: -1})
	if err == nil {
		t.Fatal("RunContext() accepted a negative resume count")
	}
}

func TestDragonflyAdapterStopsOnATargetCost(t *testing.T) {
	t.Parallel()

	stop := opt.WithDragonflyEarlyStop(opt.Stop{TargetCost: 1e9})
	optimizer := dragonflyLifecycle(t, opt.NewDragonfly(200, 10, 42, stop))

	result, err := optimizer.RunContext(context.Background(), dragonflyProblem(), opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != opt.TerminationTargetCost {
		t.Errorf("Termination = %q, want %q", result.Termination, opt.TerminationTargetCost)
	}

	if result.Iterations >= 200 {
		t.Errorf("Iterations = %d, want an early stop below the cap", result.Iterations)
	}
}

func TestDragonflyAdapterRunReturnsInfinityOnAnInvalidProblem(t *testing.T) {
	t.Parallel()

	optimizer := opt.NewDragonfly(5, 10, 42)

	params, cost := optimizer.Run(nil, []float64{0}, []float64{1}, 1)
	if params != nil || !math.IsInf(cost, 1) {
		t.Errorf("Run() = %v, %v, want nil and +Inf", params, cost)
	}
}
