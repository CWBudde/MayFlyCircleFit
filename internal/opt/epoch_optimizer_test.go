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
		progress := Progress{Iterations: 2, Evaluations: 3, BestParams: result.BestParams, BestCost: result.BestCost}
		if options.ProgressMapper != nil {
			progress = options.ProgressMapper(progress)
		}

		options.Observer(progress)
	}

	return result, nil
}

func TestEpochOptimizerReportsMappedDurableBoundaries(t *testing.T) {
	base := &epochProbeOptimizer{}
	optimizer := WithEpochs(base, 3).(LifecycleOptimizer)
	var boundaries []EpochBoundary

	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: func([]float64) float64 { return 0 }, Lower: []float64{0}, Upper: []float64{1}, Dim: 1,
	}, RunOptions{
		ProgressMapper: func(progress Progress) Progress {
			progress.BestParams = append([]float64{99}, progress.BestParams...)
			return progress
		},
		EpochObserver: func(boundary EpochBoundary) error {
			boundaries = append(boundaries, boundary)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Iterations != 6 || len(boundaries) != 3 {
		t.Fatalf("result/boundaries = %+v/%d, want 6 iterations and 3 boundaries", result, len(boundaries))
	}

	for epoch, boundary := range boundaries {
		if boundary.Epoch != epoch+1 || boundary.Progress.Iterations != (epoch+1)*2 || boundary.Progress.Evaluations != (epoch+1)*3 {
			t.Fatalf("boundary %d = %+v", epoch+1, boundary)
		}

		if len(boundary.Progress.BestParams) != 2 || boundary.Progress.BestParams[0] != 99 {
			t.Fatalf("boundary %d params = %v, want complete mapped vector", epoch+1, boundary.Progress.BestParams)
		}
	}
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
	alternative := Candidate{Params: []float64{9}, Cost: 11}
	var progress []Progress

	result, err := optimizer.RunContext(context.Background(), Problem{
		Eval: func([]float64) float64 { return 0 }, Lower: []float64{0}, Upper: []float64{1}, Dim: 1,
	}, RunOptions{
		Initial: initial, AdditionalSeeds: []Candidate{alternative}, ResumeCount: 5,
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

	if len(base.options[0].AdditionalSeeds) != 1 || base.options[0].AdditionalSeeds[0].Params[0] != 9 {
		t.Fatal("first epoch did not receive alternative seed")
	}

	for epoch := 1; epoch < 4; epoch++ {
		want := float64(epoch)
		if got := base.options[epoch].Initial.Params[0]; got != want {
			t.Fatalf("epoch %d seed = %v, want prior best %v", epoch+1, got, want)
		}

		if len(base.options[epoch].AdditionalSeeds) != 0 {
			t.Fatalf("epoch %d unexpectedly retained alternative basins", epoch+1)
		}
	}

	if len(progress) != 4 || progress[0].Iterations != 2 || progress[3].Iterations != 8 || progress[3].Evaluations != 12 {
		t.Fatalf("progress = %+v, want cumulative samples", progress)
	}
}
