package renderer

import (
	"bytes"
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

type refillSeedOptimizer struct {
	calls             int
	mappedParamCounts []int
}

func (o *refillSeedOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := transparentParams(dim / paramsPerCircle)
	return params, eval(params)
}

type constraintProbeOptimizer struct {
	sawUnprojectedRepair bool
	sawRadiusViolation   bool
}

func (o *constraintProbeOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := transparentParams(dim / paramsPerCircle)
	return params, eval(params)
}

func (o *constraintProbeOptimizer) RunContext(_ context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	params := append([]float64(nil), problem.Lower...)
	problem.Repair(params)

	o.sawUnprojectedRepair = params[2] == fit.MinCircleRadius
	for _, constraint := range problem.Inequalities {
		if constraint(params) > 0 {
			o.sawRadiusViolation = true
		}
	}

	for offset := 0; offset < problem.Dim; offset += paramsPerCircle {
		params[offset+2] = problem.Upper[offset+2]
		params[offset+6] = 1
	}

	return opt.Result{
		BestParams:  params,
		BestCost:    problem.Eval(params),
		Iterations:  1,
		Evaluations: 1,
		Termination: opt.TerminationCompleted,
	}, nil
}

func (o *refillSeedOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	params := append([]float64(nil), options.Initial.Params...)

	if o.calls == 0 {
		// Leave the first residual-guided circle active and make the remaining
		// slots white-on-white so the post-stage audit must refill them.
		for circle := 1; circle < problem.Dim/paramsPerCircle; circle++ {
			offset := circle * paramsPerCircle
			params[offset+3] = 1
			params[offset+4] = 1
			params[offset+5] = 1
			params[offset+6] = 1
		}
	}

	o.calls++

	if options.ProgressMapper != nil {
		mapped := options.ProgressMapper(opt.Progress{BestParams: append([]float64(nil), params...)})
		o.mappedParamCounts = append(o.mappedParamCounts, len(mapped.BestParams))
	}

	return opt.Result{
		BestParams:  params,
		BestCost:    problem.Eval(params),
		Iterations:  1,
		Evaluations: 1,
		Termination: opt.TerminationCompleted,
	}, nil
}

func (measuredOptimizer) RunContext(ctx context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	err := ctx.Err()
	if err != nil {
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

func solidColorOptimizer(c color.NRGBA, opacity float64) optimizerFunc {
	return func(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
		params := make([]float64, dim)
		for offset := 0; offset < dim; offset += paramsPerCircle {
			params[offset+0] = 1
			params[offset+1] = 1
			params[offset+2] = 1
			params[offset+3] = float64(c.R) / 255
			params[offset+4] = float64(c.G) / 255
			params[offset+5] = float64(c.B) / 255
			params[offset+6] = opacity
		}

		return params, eval(params)
	}
}

func solidImage(width, height int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
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

	result, err := OptimizeJoint(base, solidColorOptimizer(canvasColor, 1), 1, DisabledConvergenceConfig())
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

	result, err := OptimizeSequential(base, solidColorOptimizer(canvasColor, 1), 2, DisabledConvergenceConfig(), nil)
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

	result, err := OptimizeBatch(base, solidColorOptimizer(canvasColor, 1), 4, 3, DisabledConvergenceConfig())
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

func TestOptimizeBatchAppendPreservesPrefixOrder(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{A: 255})
	prefix := []float64{1, 1, 1, 1, 0, 0, 0.5}

	result, err := OptimizeBatchAppendContext(
		context.Background(),
		NewCPURenderer(ref, 2),
		opaqueBlackOptimizer(),
		prefix,
		2,
		1,
		DisabledConvergenceConfig(),
	)
	if err != nil {
		t.Fatalf("OptimizeBatchAppendContext() error = %v", err)
	}

	if result.OptimizedCircles != 2 || len(result.BestParams) != 2*paramsPerCircle {
		t.Fatalf("appended result has %d circles and %d params", result.OptimizedCircles, len(result.BestParams))
	}

	for i := range prefix {
		if result.BestParams[i] != prefix[i] {
			t.Fatalf("prefix parameter %d changed from %v to %v", i, prefix[i], result.BestParams[i])
		}
	}

	if result.BestCost >= result.InitialCost {
		t.Fatalf("append cost did not improve: initial=%v best=%v", result.InitialCost, result.BestCost)
	}

	if replayCost := fit.FastMSECost(result.BestImage, ref); replayCost != result.BestCost {
		t.Fatalf("final replay cost = %v, retained incremental cost = %v", replayCost, result.BestCost)
	}
}

func TestOptimizeBatchAppendFromCanvasMatchesPrefixReplay(t *testing.T) {
	ref := solidImage(5, 5, color.NRGBA{A: 255})
	prefix := []float64{2, 2, 2, 1, 0, 0, 0.5}
	prefixRenderer := NewCPURenderer(ref, 1)
	prefixCost := prefixRenderer.Cost(prefix)
	prefixCanvas := cloneNRGBA(prefixRenderer.Render(prefix))

	replayed, err := OptimizeBatchAppendContext(
		context.Background(), NewCPURenderer(ref, 2), opaqueBlackOptimizer(),
		prefix, 2, 1, DisabledConvergenceConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	cached, err := OptimizeBatchAppendFromCanvasContext(
		context.Background(), NewCPURenderer(ref, 2), opaqueBlackOptimizer(),
		prefix, prefixCanvas, prefixCost, 2, 1, DisabledConvergenceConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if cached.InitialCost != prefixCost || cached.BestCost != replayed.BestCost {
		t.Fatalf("cached costs = initial %v best %v, replayed = initial %v best %v",
			cached.InitialCost, cached.BestCost, replayed.InitialCost, replayed.BestCost)
	}

	if !bytes.Equal(cached.BestImage.Pix, replayed.BestImage.Pix) {
		t.Fatal("cached-prefix append image differs from parameter replay")
	}

	if len(cached.BestParams) != len(replayed.BestParams) {
		t.Fatalf("cached params = %d, replayed params = %d", len(cached.BestParams), len(replayed.BestParams))
	}

	for i := range cached.BestParams {
		if cached.BestParams[i] != replayed.BestParams[i] {
			t.Fatalf("cached parameter %d = %v, replayed = %v", i, cached.BestParams[i], replayed.BestParams[i])
		}
	}
}

func TestOptimizeBatchAppendFromCanvasRejectsInvalidRetainedState(t *testing.T) {
	ref := solidImage(5, 5, color.NRGBA{A: 255})
	prefix := []float64{2, 2, 2, 1, 0, 0, 0.5}

	for _, testCase := range []struct {
		name   string
		canvas *image.NRGBA
		cost   float64
	}{
		{name: "nil canvas", cost: 1},
		{name: "wrong dimensions", canvas: solidImage(4, 5, color.NRGBA{A: 255}), cost: 1},
		{name: "non-finite cost", canvas: solidImage(5, 5, color.NRGBA{A: 255}), cost: math.Inf(1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := OptimizeBatchAppendFromCanvasContext(
				context.Background(), NewCPURenderer(ref, 2), opaqueBlackOptimizer(),
				prefix, testCase.canvas, testCase.cost, 2, 1, DisabledConvergenceConfig(),
			)
			if !errors.Is(err, ErrInvalidOptimizationInput) {
				t.Fatalf("error = %v, want ErrInvalidOptimizationInput", err)
			}
		})
	}
}

func TestOptimizeBatchAppendRejectsInvalidPrefix(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{A: 255})

	base := NewCPURenderer(ref, 2)
	for _, prefix := range [][]float64{
		{1},
		{1, 1, 1, 0, 0, 0, 0},
		append(transparentParams(2), transparentParams(1)...),
	} {
		if _, err := OptimizeBatchAppendContext(context.Background(), base, opaqueBlackOptimizer(), prefix, 2, 1, DisabledConvergenceConfig()); !errors.Is(err, ErrInvalidOptimizationInput) {
			t.Fatalf("prefix %v error = %v, want ErrInvalidOptimizationInput", prefix, err)
		}
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
	const originalOpacity = 0.5

	result, err := OptimizeSequential(base, solidColorOptimizer(canvasColor, originalOpacity), 3, DisabledConvergenceConfig(), callback)
	if err != nil {
		t.Fatalf("OptimizeSequential() error = %v", err)
	}

	for offset := 0; offset < len(result.BestParams); offset += paramsPerCircle {
		if result.BestParams[offset+6] != originalOpacity {
			t.Fatalf("retained opacity for circle %d was mutated through callback", offset/paramsPerCircle+1)
		}
	}

	if got := result.BestImage.NRGBAAt(1, 1); got != canvasColor {
		t.Fatalf("best image pixel = %#v after callback mutation, want %#v", got, canvasColor)
	}
}

func TestOptimizeBatchRejectsIneffectiveCirclesAfterBoundedRefill(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	for _, total := range []int{1, 4, 6, 7} {
		t.Run(string(rune('0'+total)), func(t *testing.T) {
			base := NewCPURenderer(ref, total)

			result, err := OptimizeBatch(base, solidColorOptimizer(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 1), total, 5, DisabledConvergenceConfig())
			if err != nil {
				t.Fatalf("OptimizeBatch() error = %v", err)
			}

			if got := len(result.BestParams); got != 0 {
				t.Fatalf("parameter count = %d, want 0 ineffective circles", got)
			}

			if result.OptimizedCircles != 0 {
				t.Fatalf("optimized circles = %d, want 0", result.OptimizedCircles)
			}

			wantStages := (total+4)/5 + MaxExtraBatchStages
			if result.Stages != wantStages {
				t.Fatalf("stages = %d, want %d", result.Stages, wantStages)
			}

			if result.Termination != TerminationRefillLimit {
				t.Fatalf("termination = %q, want %q", result.Termination, TerminationRefillLimit)
			}
		})
	}
}

// TestOptimizeBatchKeepsWeakCirclesRatherThanRefillingThem replaces the check
// that a stage's weak slots are pruned and refilled from the residual seed.
// Refilling them is not free: a refill is a whole further optimizer run at the
// full configured iteration count, so replacing one weak circle doubled what
// the run spent, silently and only for the runs that happened to produce one.
// A batch that improves the image is now kept as the optimizer produced it,
// weak circles included, so the run costs the budget it was given and still
// produces the circle count its continuations expect.
func TestOptimizeBatchKeepsWeakCirclesRatherThanRefillingThem(t *testing.T) {
	t.Parallel()

	const total = 4
	ref := solidImage(32, 32, color.NRGBA{A: 255})
	optimizer := &refillSeedOptimizer{}

	result, err := OptimizeBatch(NewCPURenderer(ref, total), optimizer, total, total, DisabledConvergenceConfig())
	if err != nil {
		t.Fatal(err)
	}

	if result.OptimizedCircles != total || len(result.BestParams) != total*paramsPerCircle {
		t.Fatalf("result has %d circles and %d params", result.OptimizedCircles, len(result.BestParams))
	}

	if result.Stages != 1 || optimizer.calls != 1 {
		t.Fatalf("stages/calls = %d/%d, want 1/1", result.Stages, optimizer.calls)
	}

	for stage, count := range optimizer.mappedParamCounts {
		if count != total*paramsPerCircle {
			t.Fatalf("stage %d mapped progress parameter count = %d, want complete %d-circle vector", stage+1, count, total)
		}
	}

	if result.Termination != opt.TerminationCompleted {
		t.Fatalf("termination = %q, want completed", result.Termination)
	}
	// The audit still has an opinion about those circles; the pipeline reports
	// it and leaves them in place instead of spending a second budget on them.
	audit, err := AuditCircleBatch(NewCPURenderer(ref, total), result.BestParams)
	if err != nil {
		t.Fatal(err)
	}

	weak := 0

	for _, circle := range audit.Circles {
		if circle.FinalChangedPixels < 1 || circle.MSEContribution <= minBatchMSEContribution {
			weak++
		}
	}

	if weak == 0 {
		t.Fatal("expected the kept batch to still contain the circles the audit called weak")
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

	if len(result.BestParams) != 0 || result.OptimizedCircles != 0 {
		t.Fatalf("worsening candidate was retained: params=%v circles=%d", result.BestParams, result.OptimizedCircles)
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

	if len(batch.BestParams) != 0 || batch.OptimizedCircles != 0 {
		t.Fatalf("worsening batch was retained: params=%v circles=%d", batch.BestParams, batch.OptimizedCircles)
	}
}

func TestOptimizationRepairsDynamicCircleBounds(t *testing.T) {
	const width, height, circles = 20, 10, 2
	black := color.NRGBA{A: 255}
	optimizer := optimizerFunc(func(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
		if lower[0] != -10 || upper[0] != 29 || lower[1] != -5 || upper[1] != 14 {
			t.Fatalf("center bounds = x[%g,%g] y[%g,%g]", lower[0], upper[0], lower[1], upper[1])
		}

		if lower[2] != fit.MinCircleRadius || lower[6] != fit.MinCircleOpacity || upper[6] != 1 {
			t.Fatalf("radius/opacity bounds = radius %g opacity [%g,%g]", lower[2], lower[6], upper[6])
		}

		params := make([]float64, dim)
		for offset := 0; offset < dim; offset += paramsPerCircle {
			params[offset+0] = lower[offset+0]
			params[offset+1] = lower[offset+1]
			params[offset+2] = 1
			params[offset+6] = 0
		}

		cost := eval(params)

		return params, cost
	})

	runs := []struct {
		name string
		run  func(Renderer) (*OptimizationResult, error)
	}{
		{name: "joint", run: func(r Renderer) (*OptimizationResult, error) {
			return OptimizeJoint(r, optimizer, circles, DisabledConvergenceConfig())
		}},
		{name: "sequential", run: func(r Renderer) (*OptimizationResult, error) {
			return OptimizeSequential(r, optimizer, circles, DisabledConvergenceConfig(), nil)
		}},
		{name: "batch", run: func(r Renderer) (*OptimizationResult, error) {
			return OptimizeBatch(r, optimizer, circles, circles, DisabledConvergenceConfig())
		}},
	}
	for _, test := range runs {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.run(NewCPURenderer(solidImage(width, height, black), circles))
			if err != nil {
				t.Fatal(err)
			}

			bounds := fit.NewBounds(result.OptimizedCircles, width, height)
			if result.OptimizedCircles != circles || !bounds.ValidVector(result.BestParams) {
				t.Fatalf("result is not dynamically valid: circles=%d params=%v", result.OptimizedCircles, result.BestParams)
			}

			vector := fit.ParamVector{Data: result.BestParams, K: circles, Width: width, Height: height}
			first := vector.DecodeCircle(0)

			wantRadius := fit.RequiredCircleRadius(first.X, first.Y, width, height)
			if first.R != wantRadius || first.Opacity != fit.MinCircleOpacity {
				t.Fatalf("repaired circle = %+v, want radius %g and opacity %g", first, wantRadius, fit.MinCircleOpacity)
			}
		})
	}
}

func TestJointUsesContinuousRadiusConstraintWithoutTangencyProjection(t *testing.T) {
	const width, height = 20, 10
	optimizer := &constraintProbeOptimizer{}

	result, err := OptimizeJoint(
		NewCPURenderer(solidImage(width, height, color.NRGBA{A: 255}), 1),
		optimizer,
		1,
		DisabledConvergenceConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if !optimizer.sawUnprojectedRepair || !optimizer.sawRadiusViolation {
		t.Fatalf("constraint probe: unprojected=%t violation=%t", optimizer.sawUnprojectedRepair, optimizer.sawRadiusViolation)
	}

	if !fit.NewBounds(1, width, height).ValidVector(result.BestParams) {
		t.Fatalf("joint result is invalid: %v", result.BestParams)
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

	if result.Evaluations != 4 { // Baseline, residual seed, optimizer, retained candidate validation.
		t.Fatalf("evaluations = %d, want 4", result.Evaluations)
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
	err := ctx.Err()
	if err != nil {
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
	// The optimizer's zero opacity is repaired to the positive minimum; once no
	// further improvement is found, the tracker stalls at patience 1.
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
