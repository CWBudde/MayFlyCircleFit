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

// ParallelEvaluationWorkers forwards the wrapped optimizer's evaluation width.
// A wrapper that dropped it would hide concurrency from callers that refuse to
// run a non-re-entrant objective, turning their guard into a silent no-op.
func (o *epochOptimizer) ParallelEvaluationWorkers() int {
	return ParallelEvaluationWidth(o.base)
}

// IterationBudget reports what one Run of the whole epoch chain may consume.
// A wrapper that reported only the inner optimizer's cap would let a pipeline
// budgeting from it underestimate every invocation by a factor of epochs.
func (o *epochOptimizer) IterationBudget() int {
	return o.epochs * StageIterationBudget(o.base)
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
	additionalSeeds := append([]Candidate(nil), options.AdditionalSeeds...)
	// Every epoch's restart records belong to the one invocation this method
	// reports, for the same reason its iteration and evaluation counts do.
	// Keeping only the last epoch's would describe a fraction of the work the
	// totals already claim.
	var records restartRecords

	for epoch := range o.epochs {
		iterationOffset := totalIterations
		evaluationOffset := totalEvaluations
		restartOffset := records.offset()

		epochOptions := RunOptions{
			Initial:         initial,
			AdditionalSeeds: additionalSeeds,
			ProgressMapper:  options.ProgressMapper,
			ResumeCount:     options.ResumeCount + epoch,
			SeedOffset:      options.SeedOffset,
			Continuation:    options.Continuation,
		}
		if options.Observer != nil {
			epochOptions.Observer = func(progress Progress) {
				progress.Iterations += iterationOffset
				progress.Evaluations += evaluationOffset
				options.Observer(shiftRestart(progress, restartOffset))
			}
		}

		result, err := lifecycle.RunContext(ctx, problem, epochOptions)
		totalIterations += result.Iterations
		totalEvaluations += result.Evaluations

		records.record(result.Restarts)

		if len(result.BestParams) > 0 {
			best = result
		}

		best.Iterations = totalIterations
		best.Evaluations = totalEvaluations
		best.Restarts = records.runs

		if err != nil {
			return best, err
		}

		if options.EpochObserver != nil {
			progress := Progress{
				Iterations:  totalIterations,
				Evaluations: totalEvaluations,
				BestParams:  append([]float64(nil), result.BestParams...),
				BestCost:    result.BestCost,
			}
			if options.ProgressMapper != nil {
				progress = options.ProgressMapper(progress)
			}

			err := options.EpochObserver(EpochBoundary{
				Epoch:       epoch + 1,
				Progress:    progress,
				Termination: result.Termination,
			})
			if err != nil {
				return best, err
			}
		}

		if result.Termination == TerminationTargetCost {
			return best, nil
		}

		initial = &Candidate{
			Params: append([]float64(nil), result.BestParams...),
			Cost:   result.BestCost,
		}
		// Alternative basins are useful for the first mixed restart. Later
		// epochs concentrate around the best candidate found so far.
		additionalSeeds = nil
	}

	// Consuming the requested number of restart epochs is a completed run even
	// if an individual epoch used stagnation stopping before its iteration cap.
	best.Termination = TerminationCompleted

	return best, nil
}
