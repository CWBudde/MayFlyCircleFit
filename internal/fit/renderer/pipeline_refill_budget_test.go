package renderer_test

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// paramsPerCircle mirrors the renderer's own stride: X, Y, R, CR, CG, CB,
// Opacity. It is restated here because these tests build parameter vectors from
// outside the package, and it is the one thing they would otherwise need from
// inside it.
const paramsPerCircle = 7

// budgetedBatchOptimizer answers every stage with a batch of a declared shape
// and reports the whole iteration budget it declares, as a Mayfly run that is
// not stopped early does. useless makes every circle invisible against the
// canvas; otherwise all but the first circle earn their place.
type budgetedBatchOptimizer struct {
	budget  int
	useless bool
	calls   int
}

func (o *budgetedBatchOptimizer) IterationBudget() int { return o.budget }

func (o *budgetedBatchOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := o.batch(dim)
	return params, eval(params)
}

func (o *budgetedBatchOptimizer) RunContext(
	_ context.Context,
	problem opt.Problem,
	_ opt.RunOptions,
) (opt.Result, error) {
	o.calls++

	params := o.batch(problem.Dim)

	return opt.Result{
		BestParams:  params,
		BestCost:    problem.Eval(params),
		Iterations:  o.budget,
		Evaluations: o.budget,
		Termination: opt.TerminationCompleted,
	}, nil
}

// batch draws opaque black circles over the reference's dark half, which earn
// their place, and prefixes them with one opaque white circle over the light
// half, which changes no pixel at all.
func (o *budgetedBatchOptimizer) batch(dim int) []float64 {
	params := make([]float64, dim)
	for offset := 0; offset < dim; offset += paramsPerCircle {
		params[offset+0] = 9
		params[offset+1] = 6
		params[offset+2] = 2
		params[offset+3] = 1
		params[offset+4] = 1
		params[offset+5] = 1
		params[offset+6] = 1

		if offset > 0 && !o.useless {
			params[offset+0] = 3
			params[offset+3] = 0
			params[offset+4] = 0
			params[offset+5] = 0
		}
	}

	return params
}

func splitToneReference(size int) *image.NRGBA {
	ref := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			shade := uint8(0)
			if x >= size/2 {
				shade = 255
			}

			ref.SetNRGBA(x, y, color.NRGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}

	return ref
}

// TestOptimizeBatchSpendsOneBudgetOnABatchWithAWeakCircle is the regression
// this file exists for. A refill stage is a whole further optimizer run at the
// full configured iteration count, so a run that dropped one weak circle used
// to buy that slot back with a second complete budget: exactly twice the
// iterations and evaluations the configuration asked for, recorded as a normal
// completed run, and only for the runs whose batch happened to contain a weak
// circle. Two campaigns were measured with a fraction of their jobs spending
// twice the compute of their siblings before it was noticed.
func TestOptimizeBatchSpendsOneBudgetOnABatchWithAWeakCircle(t *testing.T) {
	t.Parallel()

	const (
		totalCircles = 3
		batchSize    = 3
		budget       = 64
	)

	optimizer := &budgetedBatchOptimizer{budget: budget}
	base := renderer.NewCPURenderer(splitToneReference(12), totalCircles)

	result, err := renderer.OptimizeBatchContext(context.Background(), base, optimizer,
		totalCircles, batchSize, renderer.DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeBatchContext() error = %v", err)
	}

	if result.Iterations != budget {
		t.Fatalf("iterations = %d, want %d: one planned stage is one budget", result.Iterations, budget)
	}

	if result.Stages != 1 || optimizer.calls != 1 {
		t.Fatalf("stages/optimizer runs = %d/%d, want 1/1", result.Stages, optimizer.calls)
	}
	// The weak circle is kept rather than bought back, so the run still
	// produces the circle count every continuation and campaign stage expects.
	if result.OptimizedCircles != totalCircles {
		t.Fatalf("optimized circles = %d, want %d", result.OptimizedCircles, totalCircles)
	}

	if result.Termination != opt.TerminationCompleted {
		t.Fatalf("termination = %q, want %q", result.Termination, opt.TerminationCompleted)
	}
}

// TestOptimizeBatchRefillsStayInsideTheRunBudget covers the batch nothing can
// be kept from: every circle is invisible, so the stage genuinely placed
// nothing and refilling is the whole point of the attempt. The refills are
// still bounded by what the run was given, so the worst case is the budget it
// asked for rather than four times it.
func TestOptimizeBatchRefillsStayInsideTheRunBudget(t *testing.T) {
	t.Parallel()

	const (
		totalCircles = 2
		batchSize    = 2
		budget       = 64
	)

	optimizer := &budgetedBatchOptimizer{budget: budget, useless: true}
	base := renderer.NewCPURenderer(splitToneReference(12), totalCircles)

	result, err := renderer.OptimizeBatchContext(context.Background(), base, optimizer,
		totalCircles, batchSize, renderer.DisabledConvergenceConfig())
	if err != nil {
		t.Fatalf("OptimizeBatchContext() error = %v", err)
	}

	if result.Iterations != budget {
		t.Fatalf("iterations = %d, want at most the run's own budget %d", result.Iterations, budget)
	}

	if result.Stages != 1 || optimizer.calls != 1 {
		t.Fatalf("stages/optimizer runs = %d/%d, want 1/1", result.Stages, optimizer.calls)
	}
	// The shortfall stays visible: a caller reads it from the circle count and
	// the termination reason instead of paying for it in unbudgeted iterations.
	if result.OptimizedCircles != 0 {
		t.Fatalf("optimized circles = %d, want 0", result.OptimizedCircles)
	}

	if result.Termination != renderer.TerminationRefillLimit {
		t.Fatalf("termination = %q, want %q", result.Termination, renderer.TerminationRefillLimit)
	}
}
