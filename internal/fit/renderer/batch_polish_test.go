package renderer

import (
	"context"
	"image/color"
	"reflect"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

type fixedPolishOptimizer struct {
	params  []float64
	calls   int
	options []opt.RunOptions
}

func (o *fixedPolishOptimizer) Run(eval func([]float64) float64, _, _ []float64, _ int) ([]float64, float64) {
	params := append([]float64(nil), o.params...)
	return params, eval(params)
}

func (o *fixedPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	o.calls++
	o.options = append(o.options, options)
	params := append([]float64(nil), o.params...)
	cost := problem.Eval(params)
	if options.Observer != nil {
		options.Observer(opt.Progress{Iterations: 3, Evaluations: 7, BestParams: params, BestCost: cost})
	}
	return opt.Result{
		BestParams:  params,
		BestCost:    cost,
		Iterations:  3,
		Evaluations: 7,
		Termination: opt.TerminationCompleted,
	}, nil
}

func TestPolishCircleBatchCommitsStrictImprovement(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	replacement := circleParams(2, 2, 5, black, 1)
	optimizer := &fixedPolishOptimizer{params: replacement}

	var observed opt.Progress
	var boundary BatchPolishProgress
	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     2,
		Observer:      func(progress opt.Progress) { observed = progress },
		OnSweep:       func(progress BatchPolishProgress) error { boundary = progress; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 1 || result.Sweeps != 2 || optimizer.calls != 2 {
		t.Fatalf("accepted/sweeps/calls = %d/%d/%d, want 1/2/2", result.AcceptedSweeps, result.Sweeps, optimizer.calls)
	}
	if result.BestCost != 0 || !reflect.DeepEqual(result.BestParams, replacement) {
		t.Fatalf("best result = cost %v params %v, want zero-cost replacement %v", result.BestCost, result.BestParams, replacement)
	}
	if observed.Iterations != 6 || len(observed.BestParams) != len(initial) {
		t.Fatalf("observer progress = %+v, want cumulative iterations and complete params", observed)
	}
	if boundary.Accepted || boundary.Sweep != 2 || !reflect.DeepEqual(boundary.BestParams, replacement) {
		t.Fatalf("rejected boundary did not preserve committed solution: %+v", boundary)
	}
	if got := result.BestImage.NRGBAAt(2, 2); got != black {
		t.Fatalf("best image center = %#v, want %#v", got, black)
	}
}

func TestPolishCircleBatchRollsBackRejectedSweepExactly(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	worse := circleParams(2, 2, 5, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 1)
	initialCost := base.Cost(initial)

	result, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: worse}, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sweeps != 1 || result.AcceptedSweeps != 0 {
		t.Fatalf("sweeps/accepted = %d/%d, want 1/0", result.Sweeps, result.AcceptedSweeps)
	}
	if result.BestCost != initialCost || !reflect.DeepEqual(result.BestParams, initial) {
		t.Fatalf("rejected sweep changed result: cost %v params %v", result.BestCost, result.BestParams)
	}
	if got, want := result.BestImage.NRGBAAt(2, 2), base.Render(initial).NRGBAAt(2, 2); got != want {
		t.Fatalf("rollback image center = %#v, want %#v", got, want)
	}
}

func TestPolishCircleBatchMapsEpochBoundariesToCompleteVector(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	replacement := circleParams(2, 2, 5, black, 1)
	optimizer := opt.WithEpochs(&fixedPolishOptimizer{params: replacement}, 2)
	var boundaries []BatchPolishEpoch

	_, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		OnEpoch: func(boundary BatchPolishEpoch) error {
			boundaries = append(boundaries, boundary)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(boundaries) != 2 {
		t.Fatalf("epoch boundaries = %d, want 2", len(boundaries))
	}
	for epoch, boundary := range boundaries {
		if boundary.Epoch != epoch+1 || len(boundary.BestParams) != len(initial) {
			t.Fatalf("boundary %d = %+v, want complete parameter vector", epoch+1, boundary)
		}
	}
	if boundaries[1].Iterations != 6 || boundaries[1].Evaluations <= boundaries[0].Evaluations {
		t.Fatalf("epoch work is not cumulative: %+v", boundaries)
	}
}

func TestMergeActiveCircleParamsPreservesOriginalDrawSlots(t *testing.T) {
	full := make([]float64, 4*paramsPerCircle)
	for circle := range 4 {
		for parameter := range paramsPerCircle {
			full[circle*paramsPerCircle+parameter] = float64(circle*10 + parameter)
		}
	}
	before := append([]float64(nil), full...)
	active := append(circleParams(1, 1, 1, color.NRGBA{R: 10, A: 255}, 0.5), circleParams(2, 2, 2, color.NRGBA{B: 20, A: 255}, 0.75)...)

	mergeActiveCircleParams(full, []int{1, 3}, active)

	if !reflect.DeepEqual(full[:paramsPerCircle], before[:paramsPerCircle]) ||
		!reflect.DeepEqual(full[2*paramsPerCircle:3*paramsPerCircle], before[2*paramsPerCircle:3*paramsPerCircle]) {
		t.Fatal("inactive circle draw slots changed")
	}
	if !reflect.DeepEqual(full[paramsPerCircle:2*paramsPerCircle], active[:paramsPerCircle]) ||
		!reflect.DeepEqual(full[3*paramsPerCircle:], active[paramsPerCircle:]) {
		t.Fatal("active circles were not restored to their original draw slots")
	}
}

func TestSelectHybridOverlapCirclesCombinesWeakAnchorsAndPartners(t *testing.T) {
	params := append(circleParams(2, 2, 2, color.NRGBA{A: 255}, 1), circleParams(17, 17, 2, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(2, 2, 4, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(17, 17, 4, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(10, 10, 1, color.NRGBA{A: 255}, 1)...)
	audit := BatchAudit{Circles: []CircleAudit{
		{Circle: 1, OriginalCircle: 1, MSEContribution: 1, FinalChangedPixels: 10},
		{Circle: 2, OriginalCircle: 2, MSEContribution: 2, FinalChangedPixels: 10},
		{Circle: 3, OriginalCircle: 3, MSEContribution: 20, FinalChangedPixels: 10},
		{Circle: 4, OriginalCircle: 4, MSEContribution: 30, FinalChangedPixels: 10},
		{Circle: 5, OriginalCircle: 5, MSEContribution: 3, FinalChangedPixels: 10},
	}}

	got := selectHybridOverlapCircles(params, audit, 4, 20, 20)
	want := []int{0, 1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hybrid active set = %v, want weak anchors plus overlap partners %v", got, want)
	}
	retained := removeActiveCircleParams(params, got)
	if !reflect.DeepEqual(retained, params[4*paramsPerCircle:]) {
		t.Fatal("hybrid removal did not preserve the retained draw order")
	}
}

func TestPolishCircleBatchHybridSeedsIncumbentAndResidualAlternative(t *testing.T) {
	ref := solidImage(7, 7, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	initial := circleParams(3, 3, 3, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	optimizer := &fixedPolishOptimizer{params: initial}

	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		Strategy:      BatchPolishHybridOverlap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 0 || len(optimizer.options) != 1 {
		t.Fatalf("hybrid result/options = %+v/%d", result, len(optimizer.options))
	}
	options := optimizer.options[0]
	if options.Initial == nil || !reflect.DeepEqual(options.Initial.Params, initial) {
		t.Fatalf("hybrid incumbent seed = %+v, want current active parameters", options.Initial)
	}
	if len(options.AdditionalSeeds) != 1 || reflect.DeepEqual(options.AdditionalSeeds[0].Params, initial) {
		t.Fatalf("hybrid residual alternatives = %+v, want distinct seed", options.AdditionalSeeds)
	}
}

func TestPolishCircleBatchValidatesOptions(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	params := circleParams(1, 1, 1, color.NRGBA{A: 255}, 1)
	tests := []BatchPolishOptions{
		{ActiveSetSize: 0, MaxSweeps: 1},
		{ActiveSetSize: 2, MaxSweeps: 1},
		{ActiveSetSize: 1, MaxSweeps: -1},
		{ActiveSetSize: 1, MaxSweeps: 1, Strategy: "unsupported"},
	}
	for _, options := range tests {
		if _, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: params}, params, options); err == nil {
			t.Fatalf("PolishCircleBatchContext options %+v returned nil error", options)
		}
	}
}

func circleParams(x, y, radius float64, c color.NRGBA, opacity float64) []float64 {
	return []float64{
		x,
		y,
		radius,
		float64(c.R) / 255,
		float64(c.G) / 255,
		float64(c.B) / 255,
		opacity,
	}
}
