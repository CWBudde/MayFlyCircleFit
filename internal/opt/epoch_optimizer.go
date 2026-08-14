package opt

import (
	"context"
	"math"
)

// WithEpochs repeats each optimizer invocation while retaining and reseeding
// from the best candidate. An epoch count of one preserves the original
// optimizer exactly.
func WithEpochs(base Optimizer, epochs int) Optimizer {
	if base == nil || epochs <= 1 {
		return base
	}
	return &epochOptimizer{base: base, epochs: epochs}
}

type epochOptimizer struct {
	base   Optimizer
	epochs int
}

func (o *epochOptimizer) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	if _, ok := o.base.(LifecycleOptimizer); ok {
		result, err := o.RunContext(
			context.Background(),
			Problem{Eval: eval, Lower: lower, Upper: upper, Dim: dim},
			RunOptions{},
		)
		if err == nil {
			return result.BestParams, result.BestCost
		}
	}

	bestParams, bestCost := o.base.Run(eval, lower, upper, dim)
	for epoch := 1; epoch < o.epochs; epoch++ {
		var params []float64
		var cost float64
		if resumable, ok := o.base.(ResumableOptimizer); ok {
			params, cost = resumable.RunWithInitial(bestParams, bestCost, eval, lower, upper, dim)
		} else {
			params, cost = o.base.Run(eval, lower, upper, dim)
		}
		if cost < bestCost {
			bestParams, bestCost = params, cost
		}
	}
	return bestParams, bestCost
}

func (o *epochOptimizer) RunContext(ctx context.Context, problem Problem, options RunOptions) (Result, error) {
	lifecycle, ok := o.base.(LifecycleOptimizer)
	if !ok {
		params, cost := o.Run(problem.Eval, problem.Lower, problem.Upper, problem.Dim)
		return Result{BestParams: params, BestCost: cost, Termination: TerminationCompleted}, nil
	}

	totalIterations := 0
	totalEvaluations := 0
	best := Result{BestCost: math.Inf(1), Termination: TerminationCompleted}
	initial := options.Initial

	for epoch := 0; epoch < o.epochs; epoch++ {
		iterationOffset := totalIterations
		evaluationOffset := totalEvaluations
		epochOptions := RunOptions{
			Initial:     initial,
			ResumeCount: options.ResumeCount + epoch,
		}
		if options.Observer != nil {
			epochOptions.Observer = func(progress Progress) {
				progress.Iterations += iterationOffset
				progress.Evaluations += evaluationOffset
				options.Observer(progress)
			}
		}

		result, err := lifecycle.RunContext(ctx, problem, epochOptions)
		totalIterations += result.Iterations
		totalEvaluations += result.Evaluations
		if len(result.BestParams) > 0 {
			best = result
		}
		best.Iterations = totalIterations
		best.Evaluations = totalEvaluations
		if err != nil {
			return best, err
		}
		if result.Termination == TerminationTargetCost {
			return best, nil
		}
		initial = &Candidate{
			Params: append([]float64(nil), result.BestParams...),
			Cost:   result.BestCost,
		}
	}

	// Consuming the requested number of restart epochs is a completed run even
	// if an individual epoch used stagnation stopping before its iteration cap.
	best.Termination = TerminationCompleted
	return best, nil
}
