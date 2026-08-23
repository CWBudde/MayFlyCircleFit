package opt

import (
	"context"
	"math"
)

// WithRestarts repeats each optimizer invocation as independent attempts and
// keeps the best result. A restart count of one preserves the original
// optimizer exactly.
//
// This is the sibling of WithEpochs, and the difference is the whole point:
// an epoch reseeds the next run from the best candidate found so far, so it
// inherits that candidate's basin. A restart does not chain. Each attempt
// draws a fresh population from a different seed and explores independently.
//
// A caller-supplied Initial candidate is still honored by every attempt. That
// keeps a resumed or staged run from discarding work it was handed, while the
// cold base stage -- where Initial is nil -- gets the fully independent
// attempts that the measurement in docs/restart-vs-budget-report.md covers.
func WithRestarts(base Optimizer, restarts int) Optimizer {
	if base == nil || restarts <= 1 {
		return base
	}

	return &restartOptimizer{base: base, restarts: restarts}
}

type restartOptimizer struct {
	base     Optimizer
	restarts int
}

// ParallelEvaluationWorkers forwards the wrapped optimizer's evaluation width.
// A wrapper that dropped it would hide concurrency from callers that refuse to
// run a non-re-entrant objective, turning their guard into a silent no-op.
func (o *restartOptimizer) ParallelEvaluationWorkers() int {
	return ParallelEvaluationWidth(o.base)
}

func (o *restartOptimizer) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
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
	for range o.restarts - 1 {
		params, cost := o.base.Run(eval, lower, upper, dim)
		if cost < bestCost {
			bestParams, bestCost = params, cost
		}
	}

	return bestParams, bestCost
}

func (o *restartOptimizer) RunContext(ctx context.Context, problem Problem, options RunOptions) (Result, error) {
	lifecycle, ok := o.base.(LifecycleOptimizer)
	if !ok {
		params, cost := o.Run(problem.Eval, problem.Lower, problem.Upper, problem.Dim)
		return Result{BestParams: params, BestCost: cost, Termination: TerminationCompleted}, nil
	}

	totalIterations := 0
	totalEvaluations := 0
	best := Result{BestCost: math.Inf(1), Termination: TerminationCompleted}

	for attempt := range o.restarts {
		iterationOffset := totalIterations
		evaluationOffset := totalEvaluations

		attemptOptions := RunOptions{
			Initial:         options.Initial,
			AdditionalSeeds: options.AdditionalSeeds,
			ProgressMapper:  options.ProgressMapper,
			ResumeCount:     options.ResumeCount,
			// SeedOffset is the dimension the adapter varies the run seed on
			// without implying a continuation, which is what makes the
			// attempts independent while staying reproducible for a fixed
			// base seed. ResumeCount is left alone so epochs nested inside an
			// attempt cannot alias onto another attempt's seed.
			SeedOffset:   options.SeedOffset + attempt,
			Continuation: options.Continuation,
		}

		if options.Observer != nil {
			// Progress is documented as best-so-far. A later attempt starts
			// from a fresh population and its early costs are far worse than
			// what earlier attempts already reached, so forwarding them raw
			// would break that contract and make a monitored run look like it
			// regressed. Only improvements are forwarded.
			bestReported := best.BestCost
			attemptOptions.Observer = func(progress Progress) {
				if progress.BestCost >= bestReported {
					return
				}

				bestReported = progress.BestCost
				progress.Iterations += iterationOffset
				progress.Evaluations += evaluationOffset
				options.Observer(progress)
			}
		}

		result, err := lifecycle.RunContext(ctx, problem, attemptOptions)
		totalIterations += result.Iterations
		totalEvaluations += result.Evaluations

		if len(result.BestParams) > 0 && result.BestCost < best.BestCost {
			best = result
		}

		best.Iterations = totalIterations
		best.Evaluations = totalEvaluations

		if err != nil {
			return best, err
		}

		if options.EpochObserver != nil {
			// The boundary carries the running best, not this attempt's
			// result. An observer that persists a checkpoint must never be
			// handed a worse candidate than one it has already stored.
			progress := Progress{
				Iterations:  totalIterations,
				Evaluations: totalEvaluations,
				BestParams:  append([]float64(nil), best.BestParams...),
				BestCost:    best.BestCost,
			}
			if options.ProgressMapper != nil {
				progress = options.ProgressMapper(progress)
			}

			err := options.EpochObserver(EpochBoundary{
				Epoch:       attempt + 1,
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
	}

	// Consuming the requested number of attempts is a completed run even if an
	// individual attempt used stagnation stopping before its iteration cap.
	best.Termination = TerminationCompleted

	return best, nil
}
