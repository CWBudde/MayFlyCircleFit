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
	// boundaryBest is the best candidate any epoch boundary has already
	// carried. It is tracked separately from best because epochs nested inside
	// an attempt report boundaries before that attempt has finished, so best
	// is not yet up to date when they do.
	boundaryBest := Candidate{Cost: math.Inf(1)}
	// Boundaries are numbered by one counter that runs over the whole set of
	// attempts instead of restarting at one per attempt. An observer that
	// persists a checkpoint per boundary would otherwise see the epoch number
	// jump back to 1 with every attempt and read as if the run had gone
	// backwards. With epochs nested inside attempts the sequence is therefore
	// 1..epochs for the first attempt, epochs+1..2*epochs for the second, and
	// so on.
	boundaryCount := 0

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

		reportedInnerBoundary := false

		if options.EpochObserver != nil {
			// Forward the nested optimizer's per-epoch boundaries instead of
			// swallowing them. Without this the caller's observer -- for the
			// server, the checkpoint writer -- would only run once the entire
			// epoch chain of an attempt had finished, dropping the durable
			// checkpoint every epoch is supposed to leave behind and
			// postponing the abort a persistence error is meant to trigger.
			attemptOptions.EpochObserver = func(boundary EpochBoundary) error {
				reportedInnerBoundary = true
				boundary.Progress.Iterations += iterationOffset
				boundary.Progress.Evaluations += evaluationOffset

				return reportBoundary(options.EpochObserver, &boundaryCount, &boundaryBest, boundary)
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

		if options.EpochObserver != nil && !reportedInnerBoundary {
			// A nested epoch optimizer reports one boundary per epoch, and the
			// last of those already carries the attempt's complete cumulative
			// work and the running best, so a second report here would
			// duplicate it. The outer boundary therefore fires only for an
			// attempt whose inner optimizer reported none of its own -- the
			// epochs == 1 case, where the attempt itself is the boundary.
			progress := Progress{
				Iterations:  totalIterations,
				Evaluations: totalEvaluations,
				BestParams:  append([]float64(nil), best.BestParams...),
				BestCost:    best.BestCost,
			}
			if options.ProgressMapper != nil {
				progress = options.ProgressMapper(progress)
			}

			boundary := EpochBoundary{Progress: progress, Termination: result.Termination}
			if err := reportBoundary(options.EpochObserver, &boundaryCount, &boundaryBest, boundary); err != nil {
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

// reportBoundary numbers a boundary from the run-wide counter, clamps it to the
// best candidate any earlier boundary already carried, and hands it to the
// caller's observer. Clamping is what keeps the contract that an observer
// persisting a checkpoint is never handed a regression: a fresh attempt's early
// epochs are far worse than what an earlier attempt already reached.
func reportBoundary(observer EpochObserver, count *int, running *Candidate, boundary EpochBoundary) error {
	if boundary.Progress.BestCost < running.Cost {
		running.Cost = boundary.Progress.BestCost
		running.Params = append([]float64(nil), boundary.Progress.BestParams...)
	} else {
		boundary.Progress.BestCost = running.Cost
		boundary.Progress.BestParams = append([]float64(nil), running.Params...)
	}

	*count++
	boundary.Epoch = *count

	return observer(boundary)
}
