package renderer

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

type optimizerFunc func(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)

func (f optimizerFunc) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	return f(eval, lower, upper, dim)
}

type measuredOptimizer struct{}

func (measuredOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := transparentParams(dim / paramsPerCircle)
	return params, eval(params)
}

func (measuredOptimizer) RunContext(ctx context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	if err := ctx.Err(); err != nil {
		return opt.Result{Termination: opt.TerminationCancelled}, err
	}
	params := transparentParams(problem.Dim / paramsPerCircle)
	cost := problem.Eval(params)
	return opt.Result{
		BestParams:  params,
		BestCost:    cost,
		Iterations:  7,
		Evaluations: 1,
		Termination: opt.TerminationCompleted,
	}, nil
}

func transparentOptimizer() optimizerFunc {
	return func(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
		params := transparentParams(dim / paramsPerCircle)
		return params, eval(params)
	}
}

func opaqueBlackOptimizer() optimizerFunc {
	return func(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
		params := make([]float64, dim)
		for offset := 0; offset < dim; offset += paramsPerCircle {
			params[offset+0] = 1
			params[offset+1] = 1
			params[offset+2] = 10
			params[offset+6] = 1
		}
		return params, eval(params)
	}
}

func solidImage(width, height int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestOptimizeJointPreservesCustomCanvas(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{R: 255, A: 255})
	canvasColor := color.NRGBA{G: 128, B: 255, A: 255}
	canvas := solidImage(3, 3, canvasColor)
	base := NewCPURendererWithCanvas(ref, canvas, 1)

	result, err := OptimizeJoint(base, transparentOptimizer(), 1, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeJoint() error = %v", err)
	}
	if got := result.BestImage.NRGBAAt(1, 1); got != canvasColor {
		t.Fatalf("best image pixel = %#v, want custom canvas %#v", got, canvasColor)
	}
	if result.BestCost != result.InitialCost {
		t.Fatalf("best cost = %v, want initial cost %v", result.BestCost, result.InitialCost)
	}
}

func TestOptimizeSequentialPreservesCustomCanvas(t *testing.T) {
	canvasColor := color.NRGBA{R: 10, G: 80, B: 160, A: 255}
	ref := solidImage(3, 3, canvasColor)
	base := NewCPURendererWithCanvas(ref, solidImage(3, 3, canvasColor), 2)

	result, err := OptimizeSequential(base, transparentOptimizer(), 2, DisabledConvergenceConfig(), nil)
	if err != nil {
		t.Fatalf("OptimizeSequential() error = %v", err)
	}
	if result.BestCost != 0 {
		t.Fatalf("best cost = %v, want 0", result.BestCost)
	}
	if got := result.BestImage.NRGBAAt(1, 1); got != canvasColor {
		t.Fatalf("best image pixel = %#v, want custom canvas %#v", got, canvasColor)
	}
}

func TestOptimizeBatchPreservesCustomCanvas(t *testing.T) {
	canvasColor := color.NRGBA{R: 70, G: 30, B: 190, A: 255}
	ref := solidImage(3, 3, canvasColor)
	base := NewCPURendererWithCanvas(ref, solidImage(3, 3, canvasColor), 4)

	result, err := OptimizeBatch(base, transparentOptimizer(), 4, 3, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeBatch() error = %v", err)
	}
	if result.BestCost != 0 {
		t.Fatalf("best cost = %v, want 0", result.BestCost)
	}
	if got := result.BestImage.NRGBAAt(1, 1); got != canvasColor {
		t.Fatalf("best image pixel = %#v, want custom canvas %#v", got, canvasColor)
	}
}

func TestAccumulatedStagesMatchFinalFullReplay(t *testing.T) {
	ref := solidImage(5, 5, color.NRGBA{A: 255})

	sequential, err := OptimizeSequential(
		NewCPURenderer(ref, 3),
		opaqueBlackOptimizer(),
		3,
		DisabledConvergenceConfig(),
		nil,
	)
	if err != nil {
		t.Fatalf("OptimizeSequential() error = %v", err)
	}
	if replayCost := fit.FastMSECost(sequential.BestImage, ref); replayCost != sequential.BestCost {
		t.Fatalf("sequential final replay cost = %v, retained incremental cost = %v", replayCost, sequential.BestCost)
	}

	batch, err := OptimizeBatch(
		NewCPURenderer(ref, 5),
		opaqueBlackOptimizer(),
		5,
		2,
		DisabledConvergenceConfig(),
	)
	if err != nil {
		t.Fatalf("OptimizeBatch() error = %v", err)
	}
	if replayCost := fit.FastMSECost(batch.BestImage, ref); replayCost != batch.BestCost {
		t.Fatalf("batch final replay cost = %v, retained incremental cost = %v", replayCost, batch.BestCost)
	}
}

func TestSequentialCallbackCannotMutateAccumulatedState(t *testing.T) {
	canvasColor := color.NRGBA{R: 30, G: 90, B: 150, A: 255}
	ref := solidImage(3, 3, canvasColor)
	base := NewCPURendererWithCanvas(ref, solidImage(3, 3, canvasColor), 3)

	callback := func(_ int, params []float64, _ float64, img image.Image) {
		params[len(params)-1] = 1
		if mutable, ok := img.(*image.NRGBA); ok {
			mutable.SetNRGBA(1, 1, color.NRGBA{A: 255})
		}
	}
	result, err := OptimizeSequential(base, transparentOptimizer(), 3, DisabledConvergenceConfig(), callback)
	if err != nil {
		t.Fatalf("OptimizeSequential() error = %v", err)
	}

	for offset := 0; offset < len(result.BestParams); offset += paramsPerCircle {
		if result.BestParams[offset+6] != 0 {
			t.Fatalf("retained opacity for circle %d was mutated through callback", offset/paramsPerCircle+1)
		}
	}
	if got := result.BestImage.NRGBAAt(1, 1); got != canvasColor {
		t.Fatalf("best image pixel = %#v after callback mutation, want %#v", got, canvasColor)
	}
}

func TestOptimizeBatchReturnsExactCircleCount(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	for _, total := range []int{1, 4, 6, 7} {
		t.Run(string(rune('0'+total)), func(t *testing.T) {
			base := NewCPURenderer(ref, total)
			result, err := OptimizeBatch(base, transparentOptimizer(), total, 5, DisabledConvergenceConfig())
			if err != nil {
				t.Fatalf("OptimizeBatch() error = %v", err)
			}
			if got, want := len(result.BestParams), total*paramsPerCircle; got != want {
				t.Fatalf("parameter count = %d, want %d", got, want)
			}
			if result.OptimizedCircles != total {
				t.Fatalf("optimized circles = %d, want %d", result.OptimizedCircles, total)
			}
			wantStages := (total + 4) / 5
			if result.Stages != wantStages {
				t.Fatalf("stages = %d, want %d", result.Stages, wantStages)
			}
		})
	}
}

func TestStagedOptimizationRollsBackWorseningStage(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	ref := solidImage(3, 3, white)
	base := NewCPURenderer(ref, 1)

	result, err := OptimizeSequential(base, opaqueBlackOptimizer(), 1, DisabledConvergenceConfig(), nil)
	if err != nil {
		t.Fatalf("OptimizeSequential() error = %v", err)
	}
	if result.BestCost != 0 {
		t.Fatalf("best cost regressed to %v, want 0", result.BestCost)
	}
	if got := result.BestParams[6]; got != 0 {
		t.Fatalf("retained opacity = %v, want transparent rollback", got)
	}
	if got := result.BestImage.NRGBAAt(1, 1); got != white {
		t.Fatalf("best image pixel = %#v, want %#v", got, white)
	}

	batch, err := OptimizeBatch(NewCPURenderer(ref, 4), opaqueBlackOptimizer(), 4, 3, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeBatch() error = %v", err)
	}
	if batch.BestCost != 0 {
		t.Fatalf("batch best cost regressed to %v, want 0", batch.BestCost)
	}
	for offset := 0; offset < len(batch.BestParams); offset += paramsPerCircle {
		if got := batch.BestParams[offset+6]; got != 0 {
			t.Fatalf("batch retained opacity at circle %d = %v, want 0", offset/paramsPerCircle+1, got)
		}
	}
}

func TestPipelineRejectsShortOptimizerResults(t *testing.T) {
	ref := solidImage(2, 2, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	short := optimizerFunc(func(_ func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
		return make([]float64, dim-1), 0
	})

	if _, err := OptimizeJoint(base, short, 1, DisabledConvergenceConfig()); !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("OptimizeJoint() error = %v, want ErrInvalidOptimizationInput", err)
	}
	if _, err := OptimizeSequential(base, short, 1, DisabledConvergenceConfig(), nil); !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("OptimizeSequential() error = %v, want ErrInvalidOptimizationInput", err)
	}
	if _, err := OptimizeBatch(base, short, 1, 1, DisabledConvergenceConfig()); !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("OptimizeBatch() error = %v, want ErrInvalidOptimizationInput", err)
	}
}

func TestZeroCirclePipelines(t *testing.T) {
	ref := solidImage(2, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	base := NewCPURenderer(ref, 0)

	joint, err := OptimizeJoint(base, nil, 0, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeJoint(0) error = %v", err)
	}
	sequential, err := OptimizeSequential(base, nil, 0, DisabledConvergenceConfig(), nil)
	if err != nil {
		t.Fatalf("OptimizeSequential(0) error = %v", err)
	}
	batch, err := OptimizeBatch(base, nil, 0, 5, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeBatch(0) error = %v", err)
	}

	for name, result := range map[string]*OptimizationResult{"joint": joint, "sequential": sequential, "batch": batch} {
		if len(result.BestParams) != 0 || result.BestCost != 0 || result.BestImage == nil {
			t.Errorf("%s zero result = %#v", name, result)
		}
	}
}

func TestPipelineRejectsInvalidAndEmptyInputs(t *testing.T) {
	ref := solidImage(2, 2, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)

	if _, err := OptimizeJoint(base, transparentOptimizer(), -1, DisabledConvergenceConfig()); !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("OptimizeJoint(-1) error = %v, want ErrInvalidOptimizationInput", err)
	}
	if _, err := OptimizeBatch(base, transparentOptimizer(), 1, 0, DisabledConvergenceConfig()); !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("OptimizeBatch(batchSize=0) error = %v, want ErrInvalidOptimizationInput", err)
	}
	empty := NewCPURenderer(image.NewNRGBA(image.Rect(0, 0, 0, 0)), 0)
	if _, err := OptimizeJoint(empty, nil, 0, DisabledConvergenceConfig()); !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("OptimizeJoint(empty reference) error = %v, want ErrInvalidOptimizationInput", err)
	}
}

func TestStagedOptimizationRejectsUnsupportedRenderer(t *testing.T) {
	ref := solidImage(2, 2, color.NRGBA{A: 255})
	base := struct{ Renderer }{Renderer: NewCPURenderer(ref, 1)}

	if _, err := OptimizeSequential(base, transparentOptimizer(), 1, DisabledConvergenceConfig(), nil); !errors.Is(err, ErrStagedOptimizationUnsupported) {
		t.Fatalf("OptimizeSequential() error = %v, want ErrStagedOptimizationUnsupported", err)
	}
	if _, err := OptimizeBatch(base, transparentOptimizer(), 1, 1, DisabledConvergenceConfig()); !errors.Is(err, ErrStagedOptimizationUnsupported) {
		t.Fatalf("OptimizeBatch() error = %v, want ErrStagedOptimizationUnsupported", err)
	}
}

func TestCPURendererShortParamsDoNotPanic(t *testing.T) {
	ref := solidImage(2, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	renderer := NewCPURenderer(ref, 1)

	img := renderer.Render(make([]float64, paramsPerCircle-1))
	if img == nil {
		t.Fatal("Render(short params) returned nil")
	}
	if cost := renderer.Cost(make([]float64, paramsPerCircle-1)); !math.IsInf(cost, 1) {
		t.Fatalf("Cost(short params) = %v, want +Inf", cost)
	}
}

func TestPipelineUsesLifecycleStatsAndCancellation(t *testing.T) {
	ref := solidImage(2, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	base := NewCPURenderer(ref, 1)

	result, err := OptimizeJointContext(context.Background(), base, measuredOptimizer{}, 1, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeJointContext() error = %v", err)
	}
	if result.Iterations != 7 {
		t.Fatalf("iterations = %d, want 7", result.Iterations)
	}
	if result.Evaluations != 3 { // Baseline, optimizer, retained candidate validation.
		t.Fatalf("evaluations = %d, want 3", result.Evaluations)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OptimizeJointContext(ctx, base, measuredOptimizer{}, 1, DisabledConvergenceConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled OptimizeJointContext() error = %v, want context.Canceled", err)
	}
}

// terminatingOptimizer reports a configurable termination reason so pipeline
// aggregation can be tested independently of a real optimizer.
type terminatingOptimizer struct {
	reason opt.Termination
}

func (t terminatingOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := transparentParams(dim / paramsPerCircle)
	return params, eval(params)
}

func (t terminatingOptimizer) RunContext(ctx context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	if err := ctx.Err(); err != nil {
		return opt.Result{Termination: opt.TerminationCancelled}, err
	}
	params := transparentParams(problem.Dim / paramsPerCircle)
	return opt.Result{
		BestParams:  params,
		BestCost:    problem.Eval(params),
		Iterations:  3,
		Evaluations: 1,
		Termination: t.reason,
	}, nil
}

func TestJointReportsOptimizerTerminationVerbatim(t *testing.T) {
	tests := []opt.Termination{
		opt.TerminationCompleted,
		opt.TerminationTargetCost,
		opt.TerminationStagnation,
	}

	for _, reason := range tests {
		t.Run(string(reason), func(t *testing.T) {
			ref := solidImage(4, 4, color.NRGBA{A: 255})
			result, err := OptimizeJoint(NewCPURenderer(ref, 2), terminatingOptimizer{reason: reason}, 2, DisabledConvergenceConfig())
			if err != nil {
				t.Fatalf("OptimizeJoint() error = %v", err)
			}
			if result.Termination != reason {
				t.Fatalf("Termination = %q, want %q", result.Termination, reason)
			}
			wantEarly := 0
			if reason != opt.TerminationCompleted {
				wantEarly = 1
			}
			if result.StagesStoppedEarly != wantEarly {
				t.Fatalf("StagesStoppedEarly = %d, want %d", result.StagesStoppedEarly, wantEarly)
			}
		})
	}
}

func TestJointZeroCirclesReportsCompleted(t *testing.T) {
	ref := solidImage(4, 4, color.NRGBA{A: 255})
	result, err := OptimizeJoint(NewCPURenderer(ref, 0), terminatingOptimizer{reason: opt.TerminationStagnation}, 0, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeJoint() error = %v", err)
	}
	if result.Termination != opt.TerminationCompleted {
		t.Fatalf("Termination = %q, want %q", result.Termination, opt.TerminationCompleted)
	}
}

// TestStagedRunToCompletionReportsCompleted pins the aggregation rule: a stage
// whose optimizer stopped early is not why a staged run ended, because the loop
// went on to the next circle or batch.
func TestStagedRunToCompletionReportsCompleted(t *testing.T) {
	ref := solidImage(5, 5, color.NRGBA{A: 255})
	optimizer := terminatingOptimizer{reason: opt.TerminationStagnation}

	sequential, err := OptimizeSequential(NewCPURenderer(ref, 3), optimizer, 3, DisabledConvergenceConfig(), nil)
	if err != nil {
		t.Fatalf("OptimizeSequential() error = %v", err)
	}
	if sequential.Termination != opt.TerminationCompleted {
		t.Fatalf("sequential Termination = %q, want %q", sequential.Termination, opt.TerminationCompleted)
	}
	if sequential.StagesStoppedEarly != sequential.Stages {
		t.Fatalf("sequential StagesStoppedEarly = %d, want %d", sequential.StagesStoppedEarly, sequential.Stages)
	}

	batch, err := OptimizeBatch(NewCPURenderer(ref, 4), optimizer, 4, 2, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeBatch() error = %v", err)
	}
	if batch.Termination != opt.TerminationCompleted {
		t.Fatalf("batch Termination = %q, want %q", batch.Termination, opt.TerminationCompleted)
	}
	if batch.StagesStoppedEarly != batch.Stages {
		t.Fatalf("batch StagesStoppedEarly = %d, want %d", batch.StagesStoppedEarly, batch.Stages)
	}
}

// TestStagedTrackerStopReportsStageConvergence covers the one staged case that
// genuinely ends the run early: the stage-level convergence tracker.
func TestStagedTrackerStopReportsStageConvergence(t *testing.T) {
	ref := solidImage(5, 5, color.NRGBA{A: 255})
	// A transparent optimizer never improves the cost, so the tracker stalls
	// immediately at patience 1.
	converging := ConvergenceConfig{Enabled: true, Patience: 1, Threshold: 0.5}

	sequential, err := OptimizeSequential(NewCPURenderer(ref, 5), transparentOptimizer(), 5, converging, nil)
	if err != nil {
		t.Fatalf("OptimizeSequential() error = %v", err)
	}
	if sequential.Termination != TerminationStageConvergence {
		t.Fatalf("sequential Termination = %q, want %q", sequential.Termination, TerminationStageConvergence)
	}

	batch, err := OptimizeBatch(NewCPURenderer(ref, 6), transparentOptimizer(), 6, 2, converging)
	if err != nil {
		t.Fatalf("OptimizeBatch() error = %v", err)
	}
	if batch.Termination != TerminationStageConvergence {
		t.Fatalf("batch Termination = %q, want %q", batch.Termination, TerminationStageConvergence)
	}
}

// TestNonLifecycleOptimizerReportsCompleted keeps the plain Optimizer interface
// reporting what the pipeline assumed before reasons were propagated.
func TestNonLifecycleOptimizerReportsCompleted(t *testing.T) {
	ref := solidImage(4, 4, color.NRGBA{A: 255})
	result, err := OptimizeJoint(NewCPURenderer(ref, 2), transparentOptimizer(), 2, DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeJoint() error = %v", err)
	}
	if result.Termination != opt.TerminationCompleted {
		t.Fatalf("Termination = %q, want %q", result.Termination, opt.TerminationCompleted)
	}
	if result.StagesStoppedEarly != 0 {
		t.Fatalf("StagesStoppedEarly = %d, want 0", result.StagesStoppedEarly)
	}
}
