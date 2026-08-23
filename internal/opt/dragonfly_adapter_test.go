package opt

import (
	"context"
	"errors"
	"math"
	"testing"
)

// dragonflyProblem is the smallest bounded problem that still exercises
// normalization: asymmetric bounds per dimension, minimum away from the origin.
func dragonflyProblem() Problem {
	return Problem{
		Eval:  sphere,
		Lower: []float64{-5, -1, 0},
		Upper: []float64{5, 9, 2},
		Dim:   3,
	}
}

func TestDragonflyAdapterSatisfiesTheOptimizerInterfaces(t *testing.T) {
	optimizer := NewDragonfly(10, 8, 1)

	if _, ok := optimizer.(LifecycleOptimizer); !ok {
		t.Error("NewDragonfly does not return a LifecycleOptimizer")
	}

	if _, ok := optimizer.(ResumableOptimizer); !ok {
		t.Error("NewDragonfly does not return a ResumableOptimizer")
	}

	if budget := StageIterationBudget(optimizer); budget != 10 {
		t.Errorf("StageIterationBudget() = %d, want 10", budget)
	}

	if width := ParallelEvaluationWidth(optimizer); width != 1 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 1", width)
	}

	parallel := NewDragonfly(10, 8, 1, WithDragonflyParallelEvaluation(4))
	if width := ParallelEvaluationWidth(parallel); width != 4 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 4", width)
	}
}

func TestDragonflyAdapterRunStaysInBoundsAndReportsWork(t *testing.T) {
	optimizer := NewDragonfly(20, 10, 42).(*DragonflyAdapter)
	problem := dragonflyProblem()

	result, err := optimizer.RunContext(context.Background(), problem, RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != TerminationCompleted {
		t.Errorf("Termination = %q, want %q", result.Termination, TerminationCompleted)
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

	if math.Abs(result.BestCost-sphere(result.BestParams)) > 1e-9 {
		t.Errorf("BestCost = %v, does not match the objective at BestParams", result.BestCost)
	}
}

func TestDragonflyAdapterIsDeterministicForAFixedSeed(t *testing.T) {
	first, err := NewDragonfly(15, 10, 7).(*DragonflyAdapter).
		RunContext(context.Background(), dragonflyProblem(), RunOptions{})
	if err != nil {
		t.Fatalf("first RunContext() error = %v", err)
	}

	second, err := NewDragonfly(15, 10, 7).(*DragonflyAdapter).
		RunContext(context.Background(), dragonflyProblem(), RunOptions{})
	if err != nil {
		t.Fatalf("second RunContext() error = %v", err)
	}

	if first.BestCost != second.BestCost {
		t.Errorf("best cost = %v and %v for one seed, want identical runs", first.BestCost, second.BestCost)
	}
}

func TestDragonflyAdapterReportsProgressAndHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	optimizer := NewDragonfly(100, 10, 42).(*DragonflyAdapter)

	var updates []Progress

	result, err := optimizer.RunContext(ctx, dragonflyProblem(), RunOptions{
		Observer: func(progress Progress) {
			updates = append(updates, progress)
			progress.BestParams[0] = 999

			cancel()
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext() error = %v, want context.Canceled", err)
	}

	if result.Termination != TerminationCancelled {
		t.Errorf("Termination = %q, want %q", result.Termination, TerminationCancelled)
	}

	if len(updates) != 1 {
		t.Fatalf("observer updates = %d, want 1", len(updates))
	}

	if len(result.BestParams) == 0 || result.BestParams[0] == 999 {
		t.Error("observer received a mutable optimizer snapshot")
	}
}

func TestDragonflyAdapterRepairsEveryCandidateItExposes(t *testing.T) {
	const repairedValue = 0.75

	optimizer := NewDragonfly(3, 10, 42).(*DragonflyAdapter)
	observed := 0

	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: func(params []float64) float64 {
			if params[0] != repairedValue {
				t.Errorf("objective saw an unrepaired candidate: %v", params)
			}

			return sphere(params)
		},
		Repair: func(params []float64) { params[0] = repairedValue },
		Lower:  []float64{-5, -5},
		Upper:  []float64{5, 5},
		Dim:    2,
	}, RunOptions{Observer: func(progress Progress) {
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
	problem := dragonflyProblem()
	initial := Candidate{Params: []float64{0, 0, 0}, Cost: sphere([]float64{0, 0, 0})}

	optimizer := NewDragonfly(5, 10, 42).(*DragonflyAdapter)

	result, err := optimizer.RunContext(context.Background(), problem, RunOptions{
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
	optimizer := NewDragonfly(5, 10, 42).(*DragonflyAdapter)

	_, err := optimizer.RunContext(context.Background(), dragonflyProblem(), RunOptions{
		Initial: &Candidate{Params: []float64{0, 0}, Cost: 0},
	})
	if err == nil {
		t.Fatal("RunContext() accepted a candidate with the wrong dimension")
	}
}

func TestDragonflyAdapterStopsOnATargetCost(t *testing.T) {
	optimizer := NewDragonfly(200, 10, 42, WithDragonflyEarlyStop(Stop{TargetCost: 1e9})).(*DragonflyAdapter)

	result, err := optimizer.RunContext(context.Background(), dragonflyProblem(), RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != TerminationTargetCost {
		t.Errorf("Termination = %q, want %q", result.Termination, TerminationTargetCost)
	}

	if result.Iterations >= 200 {
		t.Errorf("Iterations = %d, want an early stop below the cap", result.Iterations)
	}
}

func TestDragonflyAdapterRunReturnsInfinityOnAnInvalidProblem(t *testing.T) {
	optimizer := NewDragonfly(5, 10, 42)

	params, cost := optimizer.Run(nil, []float64{0}, []float64{1}, 1)
	if params != nil || !math.IsInf(cost, 1) {
		t.Errorf("Run() = %v, %v, want nil and +Inf", params, cost)
	}
}
