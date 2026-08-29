package opt_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/cwbudde/circlefit/internal/opt"
)

// convergedReason is the per-run termination the probe reports. It is a
// library reason rather than one of this package's coarser values, which is
// the distinction the records exist to preserve.
const convergedReason = "tol_fun"

// restartProbeOptimizer reports a fixed number of restart records per
// invocation, numbered from zero the way an engine numbers its own schedule,
// and observes one progress sample per record carrying that same index.
type restartProbeOptimizer struct {
	costs  []float64
	runs   int
	perRun int
}

func (o *restartProbeOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *restartProbeOptimizer) RunContext(
	_ context.Context, _ opt.Problem, options opt.RunOptions,
) (opt.Result, error) {
	cost := o.costs[o.runs]
	o.runs++

	result := opt.Result{
		BestParams:  []float64{cost},
		BestCost:    cost,
		Iterations:  2,
		Evaluations: 3,
		Termination: opt.TerminationCompleted,
	}

	for restart := range o.perRun {
		result.Restarts = append(result.Restarts, opt.RestartRun{
			Termination: convergedReason,
			Restart:     restart,
			Population:  8,
			BestCost:    cost,
		})

		if options.Observer != nil {
			options.Observer(opt.Progress{
				Iterations:  restart + 1,
				Evaluations: restart + 1,
				BestParams:  result.BestParams,
				BestCost:    cost,
				Diagnostics: &opt.SearchDiagnostics{Sigma: 1, Restart: restart},
			})
		}
	}

	return result, nil
}

func probeProblem() opt.Problem {
	return opt.Problem{
		Eval:  func([]float64) float64 { return 0 },
		Lower: []float64{0},
		Upper: []float64{1},
		Dim:   1,
	}
}

func lifecycleOf(t *testing.T, optimizer opt.Optimizer) opt.LifecycleOptimizer {
	t.Helper()

	runner, ok := optimizer.(opt.LifecycleOptimizer)
	if !ok {
		t.Fatalf("%T does not run with a lifecycle", optimizer)
	}

	return runner
}

// restartIndexes reads the identity the records claim, which is what a trace
// sample has to be joined on.
func restartIndexes(runs []opt.RestartRun) []int {
	indexes := make([]int, 0, len(runs))
	for _, run := range runs {
		indexes = append(indexes, run.Restart)
	}

	return indexes
}

func TestWithEpochsAccumulatesRestartRecordsOntoOneSequence(t *testing.T) {
	t.Parallel()

	base := &restartProbeOptimizer{perRun: 2, costs: []float64{5, 4, 3}}

	var observed []int

	result, err := lifecycleOf(t, opt.WithEpochs(base, 3)).RunContext(context.Background(), probeProblem(), opt.RunOptions{
		Observer: func(progress opt.Progress) {
			observed = append(observed, progress.Diagnostics.Restart)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []int{0, 1, 2, 3, 4, 5}
	if got := restartIndexes(result.Restarts); !reflect.DeepEqual(got, want) {
		t.Fatalf("record restart indexes = %v, want %v", got, want)
	}

	// The trace has to name a run the same way the record does, or the two
	// cannot be joined at all.
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("observed restart indexes = %v, want %v", observed, want)
	}
}

func TestWithRestartsKeepsTheRecordsOfALosingAttempt(t *testing.T) {
	t.Parallel()

	// The second attempt is worse, so it does not become the reported best.
	// Its runs still happened, and its evaluations are still counted.
	base := &restartProbeOptimizer{perRun: 2, costs: []float64{1, 9}}

	result, err := lifecycleOf(t, opt.WithRestarts(base, 2)).
		RunContext(context.Background(), probeProblem(), opt.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if result.BestCost != 1 {
		t.Fatalf("BestCost = %v, want the winning attempt's 1", result.BestCost)
	}

	want := []int{0, 1, 2, 3}
	if got := restartIndexes(result.Restarts); !reflect.DeepEqual(got, want) {
		t.Fatalf("record restart indexes = %v, want %v", got, want)
	}

	if result.Restarts[3].BestCost != 9 {
		t.Fatalf("last record best cost = %v, want the losing attempt's 9", result.Restarts[3].BestCost)
	}
}

func TestRestartRecordsStayUniqueThroughNestedEpochsAndAttempts(t *testing.T) {
	t.Parallel()

	base := &restartProbeOptimizer{perRun: 2, costs: []float64{4, 3, 2, 1}}

	var observed []int

	result, err := lifecycleOf(t, opt.WithRestarts(opt.WithEpochs(base, 2), 2)).
		RunContext(context.Background(), probeProblem(), opt.RunOptions{
			Observer: func(progress opt.Progress) {
				observed = append(observed, progress.Diagnostics.Restart)
			},
		})
	if err != nil {
		t.Fatal(err)
	}

	want := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if got := restartIndexes(result.Restarts); !reflect.DeepEqual(got, want) {
		t.Fatalf("record restart indexes = %v, want %v", got, want)
	}

	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("observed restart indexes = %v, want %v", observed, want)
	}
}

func TestAppendContinuedRestartRunsStampsTheResumeItBelongsTo(t *testing.T) {
	t.Parallel()

	prior := []opt.RestartRun{{Termination: convergedReason, Restart: 0}, {Termination: "max_iter", Restart: 1}}
	next := []opt.RestartRun{{Termination: "condition_number", Restart: 0}}

	combined := opt.AppendContinuedRestartRuns(prior, next, 1)

	if len(combined) != 3 {
		t.Fatalf("len(combined) = %d, want the two earlier runs kept alongside the new one", len(combined))
	}

	if combined[0].Resume != 0 || combined[1].Resume != 0 {
		t.Fatalf("earlier runs = %+v, want their own resume counts untouched", combined[:2])
	}

	if combined[2].Resume != 1 || combined[2].Termination != "condition_number" {
		t.Fatalf("continued run = %+v, want resume 1", combined[2])
	}

	// The caller's slices must not be edited in place: the prior records come
	// from a checkpoint the caller still holds.
	if next[0].Resume != 0 {
		t.Fatalf("next[0].Resume = %d, want the caller's record left alone", next[0].Resume)
	}
}

func TestAppendContinuedRestartRunsKeepsAnEmptyContinuationHarmless(t *testing.T) {
	t.Parallel()

	prior := []opt.RestartRun{{Termination: convergedReason}}

	if got := opt.AppendContinuedRestartRuns(prior, nil, 2); !reflect.DeepEqual(got, prior) {
		t.Fatalf("AppendContinuedRestartRuns(prior, nil) = %+v, want %+v", got, prior)
	}

	if got := opt.AppendContinuedRestartRuns(nil, nil, 2); got != nil {
		t.Fatalf("AppendContinuedRestartRuns(nil, nil) = %+v, want nil", got)
	}
}
