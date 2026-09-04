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
//
// The sign of restarts selects between two shapes that share one cap.
//
// A positive count runs exactly that many attempts. Each attempt may stop
// early on its engine's own convergence or stagnation test, and whatever it
// leaves unused is simply not spent: the restart-ladder campaign's arms
// reached only 29-44% of their cap that way, which made every contrast
// involving them cap-matched but not spend-matched. See
// docs/cmaes-restart-ladder-report.md.
//
// A negative count asks for the same cap -- |restarts| times the base
// optimizer's own iteration budget -- and spends it instead of merely bounding
// it, by starting a further attempt whenever a whole one still fits. So it
// runs at least |restarts| attempts and more when they converge early, which
// is the shape that report asks a follow-up campaign to measure. It never
// overruns the cap, and it therefore leaves the last partial slot unused: an
// attempt is only started if its full budget fits, following the same rule the
// refill pipeline already applies to a stage. That residue is bounded by one
// attempt's budget, against the majority of the cap a fixed count can waste.
//
// The two shapes coincide wherever attempts consume their whole per-run
// budget, and |restarts| == 1 is one attempt under either sign, so both return
// the base optimizer unwrapped.
//
// Filling needs the cap, so it needs the base optimizer to report an iteration
// budget. One that does not -- StageIterationBudget returns zero -- leaves the
// cap unknowable and the count is all the wrapper has left, so it falls back
// to running exactly |restarts| attempts.
func WithRestarts(base Optimizer, restarts int) Optimizer {
	attempts := restarts
	if attempts < 0 {
		attempts = -attempts
	}

	if base == nil || attempts <= 1 {
		return base
	}

	return &restartOptimizer{base: base, restarts: attempts, fill: restarts < 0}
}

type restartOptimizer struct {
	base     Optimizer
	restarts int
	// fill spends the cap rather than bounding it, by starting a further
	// attempt whenever a whole one still fits. It is the negative count
	// documented on WithRestarts; restarts always holds the magnitude, so the
	// cap and every plan derived from it are identical under both shapes.
	fill bool
}

// ParallelEvaluationWorkers forwards the wrapped optimizer's evaluation width.
// A wrapper that dropped it would hide concurrency from callers that refuse to
// run a non-re-entrant objective, turning their guard into a silent no-op.
func (o *restartOptimizer) ParallelEvaluationWorkers() int {
	return ParallelEvaluationWidth(o.base)
}

// IterationBudget reports what one Run of every attempt may consume, for the
// same reason the epoch wrapper does: an attempt is a whole optimizer run, so
// the attempts multiply the inner cap.
//
// It is the same figure under both shapes, because restarts holds the
// magnitude of the configured count. A fixed count treats it as a bound it may
// leave unspent and a filling schedule treats it as a budget to consume, but
// neither exceeds it, so every plan derived from this stays an exact upper
// bound.
func (o *restartOptimizer) IterationBudget() int {
	return o.restarts * StageIterationBudget(o.base)
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

	// The legacy interface reports no work done, so there is nothing for a
	// filling schedule to measure its cap against. restarts holds the
	// magnitude, so both shapes fall back to the same fixed number of attempts
	// here, exactly as one wrapping an optimizer that reports no iteration
	// budget does.
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
	// Restart records are collected over every attempt for the same reason the
	// boundaries are numbered over every attempt: one invocation of this
	// method is one run, and a history covering only the attempt that happened
	// to win describes a fraction of the work its totals already claim.
	var records restartRecords

	// attemptBudget is what one attempt may consume, and the cap is that
	// multiplied by the count under both shapes. Filling needs the cap, so a
	// base optimizer that reports no budget leaves nothing to fill against and
	// the count is all the wrapper has left.
	attemptBudget := StageIterationBudget(o.base)
	filling := o.fill && attemptBudget > 0
	iterationCap := o.restarts * attemptBudget

	for attempt := 0; ; attempt++ {
		if filling {
			// Start an attempt only if its whole budget still fits. That keeps
			// the filling shape inside the cap it shares with the fixed count,
			// at the price of leaving the last partial slot unused -- the same
			// rule the refill pipeline applies to a stage, and a residue
			// bounded by one attempt against the majority of the cap a fixed
			// count can leave behind.
			if totalIterations+attemptBudget > iterationCap {
				break
			}
		} else if attempt >= o.restarts {
			break
		}

		iterationOffset := totalIterations
		evaluationOffset := totalEvaluations
		restartOffset := records.offset()

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
			// regressed.
			//
			// Dropping those reports instead would censor everything else the
			// progress carries. Search diagnostics describe the population an
			// attempt currently holds, not the incumbent, so suppressing a
			// non-improving report hides exactly the fresh high-spread
			// populations a restart exists to create and biases a recorded
			// mechanism trajectory toward already-collapsed ones. Every report
			// is therefore forwarded, with the cost and parameters clamped to
			// the running best so the reported incumbent stays monotonic.
			bestReported := best.BestCost
			paramsReported := append([]float64(nil), best.BestParams...)
			attemptOptions.Observer = func(progress Progress) {
				switch {
				case progress.BestCost < bestReported:
					bestReported = progress.BestCost
					paramsReported = append([]float64(nil), progress.BestParams...)
				case len(paramsReported) > 0:
					progress.BestCost = bestReported

					progress.BestParams = append([]float64(nil), paramsReported...)
				}

				progress.Iterations += iterationOffset
				progress.Evaluations += evaluationOffset
				options.Observer(shiftRestart(progress, restartOffset))
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

		records.record(o.attemptRuns(filling, result))

		if len(result.BestParams) > 0 && result.BestCost < best.BestCost {
			best = result
		}

		best.Iterations = totalIterations
		best.Evaluations = totalEvaluations
		best.Restarts = records.runs

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

			err := reportBoundary(options.EpochObserver, &boundaryCount, &boundaryBest, boundary)
			if err != nil {
				return best, err
			}
		}

		if result.Termination == TerminationTargetCost {
			return best, nil
		}

		// Two guards a fixed count does not need, because its bound is the
		// count itself while a filling schedule's is the work it observes.
		//
		// An attempt that consumed nothing cannot bring the cap any closer, so
		// the schedule would start attempts against an unmoving total forever.
		// And a cancelled attempt must not fund another one: both engines
		// report cancellation as an error and are caught above, but an engine
		// that reports it as a termination instead would otherwise have the
		// remaining cap spent on attempts that cannot run either.
		if filling && (result.Iterations <= 0 || result.Termination == TerminationCancelled) {
			break
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

// attemptRuns is the restart history one attempt contributes. An engine with a
// restart schedule of its own reports it, and that record is always preferred.
//
// Where it reports none, a filling schedule synthesizes one record per attempt
// and a fixed count synthesizes nothing. The asymmetry is deliberate: how many
// attempts a fixed count ran is recoverable from the configuration, while how
// many a filling schedule chose is decided at run time and observable nowhere
// else. Emitting these for a fixed count as well would change what every
// already-recorded campaign persists, for a number those campaigns already
// know.
//
// Population is left zero because the wrapper genuinely does not know it: the
// attempt's population belongs to the engine that sampled it, which is the
// same engine that reported no records. Termination carries this package's
// coarse value rather than an engine string, for the same reason.
func (o *restartOptimizer) attemptRuns(filling bool, result Result) []RestartRun {
	if len(result.Restarts) > 0 || !filling {
		return result.Restarts
	}

	return []RestartRun{{
		Iterations:  result.Iterations,
		Evaluations: result.Evaluations,
		BestCost:    result.BestCost,
		Termination: string(result.Termination),
	}}
}
