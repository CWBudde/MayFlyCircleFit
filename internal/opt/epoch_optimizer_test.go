package opt

import (
	"context"
	"reflect"
	"testing"
)

type epochProbeOptimizer struct {
	runs    int
	options []RunOptions
}

func (o *epochProbeOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *epochProbeOptimizer) RunContext(_ context.Context, _ Problem, options RunOptions) (Result, error) {
	o.runs++
	o.options = append(o.options, options)
	result := Result{
		BestParams:  []float64{float64(o.runs)},
		BestCost:    10 - float64(o.runs),
		Iterations:  2,
		Evaluations: 3,
		Termination: TerminationCompleted,
	}
	if options.Observer != nil {
		options.Observer(Progress{Iterations: 2, Evaluations: 3, BestParams: result.BestParams, BestCost: result.BestCost})
	}
	return result, nil
}

func TestWithEpochsPreservesSingleEpochIdentity(t *testing.T) {
	base := &epochProbeOptimizer{}
	if got := WithEpochs(base, 1); got != base {
		t.Fatalf("WithEpochs(base, 1) = %T, want original optimizer", got)
	}
}

func TestEpochOptimizerReseedsAndReportsCumulativeProgress(t *testing.T) {
	base := &epochProbeOptimizer{}
	optimizer := WithEpochs(base, 4).(LifecycleOptimizer)
	initial := &Candidate{Params: []float64{0}, Cost: 10}
	var progress []Progress
	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: func([]float64) float64 { return 0 }, Lower: []float64{0}, Upper: []float64{1}, Dim: 1,
	}, RunOptions{
		Initial: initial, ResumeCount: 5,
		Observer: func(sample Progress) { progress = append(progress, sample) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if base.runs != 4 || result.Iterations != 8 || result.Evaluations != 12 || result.BestCost != 6 {
		t.Fatalf("runs/result = %d/%+v, want 4 runs and cumulative 8/12 with cost 6", base.runs, result)
	}
	if got := []int{base.options[0].ResumeCount, base.options[1].ResumeCount, base.options[2].ResumeCount, base.options[3].ResumeCount}; !reflect.DeepEqual(got, []int{5, 6, 7, 8}) {
		t.Fatalf("resume counts = %v", got)
	}
	if base.options[0].Initial != initial {
		t.Fatal("first epoch did not receive pipeline seed")
	}
	for epoch := 1; epoch < 4; epoch++ {
		want := float64(epoch)
		if got := base.options[epoch].Initial.Params[0]; got != want {
			t.Fatalf("epoch %d seed = %v, want prior best %v", epoch+1, got, want)
		}
	}
	if len(progress) != 4 || progress[0].Iterations != 2 || progress[3].Iterations != 8 || progress[3].Evaluations != 12 {
		t.Fatalf("progress = %+v, want cumulative samples", progress)
	}
}
