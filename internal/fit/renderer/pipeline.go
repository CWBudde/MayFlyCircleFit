package renderer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"

	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/opt"
)

var (
	// ErrStagedOptimizationUnsupported indicates that a renderer cannot create
	// same-backend sessions while preserving its initial canvas.
	ErrStagedOptimizationUnsupported = errors.New("renderer does not support sequential or batch optimization")
	// ErrInvalidOptimizationInput indicates invalid pipeline dimensions or results.
	ErrInvalidOptimizationInput = errors.New("invalid optimization input")
)

// TerminationStageConvergence reports that the stage-level convergence tracker
// stopped a sequential or batch run before its circle budget was consumed. It
// is a pipeline outcome rather than an optimizer outcome, so it is defined here
// instead of in the opt package.
const TerminationStageConvergence opt.Termination = "stage_convergence"

const (
	// TerminationRefillLimit reports that batch mode exhausted its bounded
	// replacement attempts before every requested slot became useful.
	TerminationRefillLimit opt.Termination = "refill_limit"
	// MaxExtraBatchStages is the bounded number of residual-refill attempts
	// available after the initially planned batch stages.
	MaxExtraBatchStages     = 3
	minBatchMSEContribution = 0.01
)

// OptimizationResult holds the output of an optimization run.
type OptimizationResult struct {
	BestParams       []float64
	BestCost         float64
	InitialCost      float64
	Iterations       int // Exact when Optimizer implements opt.LifecycleOptimizer.
	Evaluations      int // Objective evaluations, including pipeline validation evaluations.
	Stages           int // Completed optimizer runs (one for joint, circles/batches for staged modes).
	OptimizedCircles int
	BestImage        *image.NRGBA

	// Termination reports why the run stopped. Joint mode reports the single
	// optimizer run's reason verbatim. Staged modes report
	// TerminationStageConvergence when the stage-level tracker stopped the loop
	// and opt.TerminationCompleted when the circle budget was consumed: an
	// individual stage that stopped early is not why the run ended, because the
	// loop went on to the next circle or batch.
	Termination opt.Termination
	// StagesStoppedEarly counts stages whose optimizer stopped before its own
	// iteration cap. It is diagnostic and never changes Termination.
	StagesStoppedEarly int
}

// stageOutcome is the measured result of one optimizer run within a pipeline.
type stageOutcome struct {
	Params      []float64
	Iterations  int
	Termination opt.Termination
}

// stoppedEarly reports whether the optimizer ended a stage before exhausting
// its iteration budget.
func (o stageOutcome) stoppedEarly() bool {
	return o.Termination == opt.TerminationTargetCost ||
		o.Termination == opt.TerminationStagnation ||
		o.Termination == opt.TerminationConvergence
}

// CircleCallback is called after each circle is optimized in sequential mode.
// Parameters:
//   - circleNum: 1-indexed circle number
//   - params: all circle parameters up to and including this circle (7*circleNum floats)
//   - cost: the best cost retained after this circle
//   - img: a stable copy of the retained image
type CircleCallback func(circleNum int, params []float64, cost float64, img image.Image)

// rendererSessionFactory creates an independent renderer with the same
// reference, initial canvas, cost function, and backend as the receiver.
// CPURenderer and OpenCL implement it. Backends that also implement
// accumulatedSessionFactory can avoid replaying retained circles in staged
// optimization.
type rendererSessionFactory interface {
	newSession(circleCount int) (Renderer, func(), error)
}

// accumulatedSessionFactory can create a stage over the already-retained
// canvas. The optimizer then evaluates only the new circle parameters instead
// of replaying and rebuilding every prior parameter vector on every call.
type accumulatedSessionFactory interface {
	rendererSessionFactory
	newSessionWithCanvas(canvas *image.NRGBA, circleCount int) (Renderer, func(), error)
	initialCanvas() *image.NRGBA
}

type stagedAccumulator struct {
	factory accumulatedSessionFactory
	canvas  *image.NRGBA
}

// OptimizeJoint optimizes all circles simultaneously.
func OptimizeJoint(base Renderer, optimizer opt.Optimizer, circleCount int, convergenceConfig ConvergenceConfig) (*OptimizationResult, error) {
	return OptimizeJointContext(context.Background(), base, optimizer, circleCount, convergenceConfig)
}

// OptimizeJointContext is OptimizeJoint with cooperative cancellation when the
// optimizer implements opt.LifecycleOptimizer.
func OptimizeJointContext(ctx context.Context, base Renderer, optimizer opt.Optimizer, circleCount int, _ ConvergenceConfig) (*OptimizationResult, error) {
	if err := validatePipelineInputs(base, optimizer, circleCount); err != nil {
		return nil, err
	}

	slog.Info("Starting joint optimization", "circles", circleCount)

	session, cleanup, err := sessionForJoint(base, circleCount)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	dim := circleCount * paramsPerCircle

	lower, upper, err := exactBounds(session, dim)
	if err != nil {
		return nil, err
	}

	referenceBounds := base.Reference().Bounds()
	parameterBounds := fit.NewBounds(circleCount, referenceBounds.Dx(), referenceBounds.Dy())

	pool := newEvaluationPool(session, nil, evaluationWorkers(base), func() (Renderer, func(), error) {
		factory, ok := base.(rendererSessionFactory)
		if !ok {
			return nil, nil, ErrStagedOptimizationUnsupported
		}

		return factory.newSession(circleCount)
	})
	defer pool.close()
	// Evaluations run concurrently once the optimizer is configured for it, so
	// they must touch nothing but their own leased slot.
	evaluateRaw := func(params []float64) float64 {
		slot := pool.acquire()
		defer pool.release(slot)

		return slot.session.Cost(params)
	}
	evaluate := func(params []float64) float64 {
		parameterBounds.ClampIndependentVector(params)
		return evaluateRaw(params)
	}
	constraints := radiusConstraints(parameterBounds, circleCount)

	baseline := transparentParams(circleCount)
	initialCost := evaluateRaw(baseline)
	var bestParams []float64
	bestCost := initialCost
	stages := 0
	iterations := 0

	termination := opt.TerminationCompleted
	stagesStoppedEarly := 0

	if circleCount > 0 {
		runOptions := opt.RunOptions{}

		initialCanvas := cloneNRGBA(session.Render(baseline))
		if seedParams, seedErr := SeedParamsFromResidual(initialCanvas, base.Reference(), circleCount, ResidualSeedOptions{}); seedErr == nil {
			seedCost := evaluate(seedParams)
			runOptions.Initial = &opt.Candidate{Params: seedParams, Cost: seedCost}
		} else {
			slog.Warn("Could not build residual joint seed; using optimizer initialization", "error", seedErr)
		}

		outcome, err := runOptimizer(ctx, optimizer, evaluate, parameterBounds.ClampIndependentVector, parameterBounds.ClampVector, constraints, lower, upper, dim, runOptions)
		if err != nil {
			return nil, err
		}

		iterations += outcome.Iterations
		stages = 1
		// Joint runs one optimizer, so its reason is the run's reason.
		termination = outcome.Termination
		if outcome.stoppedEarly() {
			stagesStoppedEarly = 1
		}

		if err := validateParamLength(outcome.Params, dim); err != nil {
			return nil, fmt.Errorf("%w: optimizer result: %w", ErrInvalidOptimizationInput, err)
		}

		candidateCost := evaluate(outcome.Params)
		if parameterBounds.ValidVector(outcome.Params) && candidateCost <= bestCost {
			bestParams = append([]float64(nil), outcome.Params...)
			bestCost = candidateCost
		}
	}

	evaluations := pool.count()

	var result *OptimizationResult
	if len(bestParams) == 0 && circleCount > 0 {
		result, err = finishBaseResult(base, bestCost, initialCost, evaluations, stages)
	} else {
		result, err = finishResult(session, bestParams, bestCost, initialCost, evaluations, stages, circleCount)
	}

	if err != nil {
		return nil, err
	}

	result.Iterations = iterations
	result.Termination = termination
	result.StagesStoppedEarly = stagesStoppedEarly

	slog.Info("Joint optimization complete", "initial_cost", initialCost, "best_cost", bestCost, "evaluations", evaluations)

	return result, nil
}

// OptimizeSequential optimizes circles one at a time while retaining the best
// historical solution. Invalid or worsening candidates are omitted.
func OptimizeSequential(base Renderer, optimizer opt.Optimizer, totalCircles int, convergenceConfig ConvergenceConfig, callback CircleCallback) (*OptimizationResult, error) {
	return OptimizeSequentialContext(context.Background(), base, optimizer, totalCircles, convergenceConfig, callback)
}

// OptimizeSequentialContext is OptimizeSequential with cooperative
// cancellation when the optimizer implements opt.LifecycleOptimizer.
func OptimizeSequentialContext(ctx context.Context, base Renderer, optimizer opt.Optimizer, totalCircles int, convergenceConfig ConvergenceConfig, callback CircleCallback) (*OptimizationResult, error) {
	if err := validatePipelineInputs(base, optimizer, totalCircles); err != nil {
		return nil, err
	}

	if _, ok := base.(rendererSessionFactory); !ok {
		return nil, fmt.Errorf("%w: %T", ErrStagedOptimizationUnsupported, base)
	}

	slog.Info("Starting sequential optimization", "total_circles", totalCircles)

	evaluations := 0

	initialCost, err := baseCanvasCost(base, &evaluations)
	if err != nil {
		return nil, err
	}

	bestCost := initialCost
	bestParams := make([]float64, 0, totalCircles*paramsPerCircle)
	accumulator := newStagedAccumulator(base)
	tracker := NewConvergenceTracker(convergenceConfig)
	stages := 0
	iterations := 0
	stagesStoppedEarly := 0
	termination := opt.TerminationCompleted

	for circleNum := 1; circleNum <= totalCircles; circleNum++ {
		retainedCircles := len(bestParams) / paramsPerCircle

		sessionCircleCount := retainedCircles + 1
		if accumulator != nil {
			sessionCircleCount = 1
		}

		session, sessionCleanup, err := newStagedSessionForAccumulator(base, accumulator, sessionCircleCount)
		if err != nil {
			return nil, err
		}

		bounds := fit.NewBounds(1, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy())

		var combined []float64
		if accumulator == nil {
			combined = make([]float64, len(bestParams)+paramsPerCircle)
			copy(combined, bestParams)
		}

		stageCircleCount := sessionCircleCount
		pool := newEvaluationPool(session, combined, evaluationWorkers(base), func() (Renderer, func(), error) {
			return newStagedSessionForAccumulator(base, accumulator, stageCircleCount)
		})
		// The pool owns every evaluation of this stage, so its count is the
		// stage's evaluation total and must be folded in wherever it is closed.
		cleanup := func() {
			evaluations += pool.count()
			pool.close()
			sessionCleanup()
		}
		// Evaluations run concurrently once the optimizer is configured for it,
		// so each one assembles its parameters in its own leased buffer.
		evaluate := func(newCircle []float64) float64 {
			slot := pool.acquire()
			defer pool.release(slot)

			bounds.ClampIndependentVector(newCircle)

			params := newCircle
			if slot.combined != nil {
				copy(slot.combined[len(bestParams):], newCircle)
				params = slot.combined
			}

			return slot.session.Cost(params)
		}
		constraints := radiusConstraints(bounds, 1)
		currentCanvas := currentStageCanvas(session, accumulator, combined)

		progressPrefix := append([]float64(nil), bestParams...)
		runOptions := opt.RunOptions{
			ProgressMapper: func(progress opt.Progress) opt.Progress {
				if len(progress.BestParams) == paramsPerCircle {
					progress.BestParams = append(append([]float64(nil), progressPrefix...), progress.BestParams...)
				}

				return progress
			},
		}

		if seedParams, seedErr := SeedParamsFromResidual(currentCanvas, base.Reference(), 1, ResidualSeedOptions{}); seedErr == nil {
			seedCost := evaluate(seedParams)
			runOptions.Initial = &opt.Candidate{Params: seedParams, Cost: seedCost}
		} else {
			slog.Warn("Could not build residual sequential seed; using optimizer initialization", "circle", circleNum, "error", seedErr)
		}

		outcome, err := runOptimizer(ctx, optimizer, evaluate, bounds.ClampIndependentVector, bounds.ClampVector, constraints, bounds.Lower, bounds.Upper, paramsPerCircle, runOptions)
		if err != nil {
			cleanup()
			return nil, err
		}

		candidateCircle := outcome.Params
		iterations += outcome.Iterations
		stages++

		if outcome.stoppedEarly() {
			stagesStoppedEarly++
		}

		if err := validateParamLength(candidateCircle, paramsPerCircle); err != nil {
			cleanup()
			return nil, fmt.Errorf("%w: optimizer result for circle %d: %w", ErrInvalidOptimizationInput, circleNum, err)
		}

		candidateCost := evaluate(candidateCircle)
		if bounds.ValidVector(candidateCircle) && candidateCost < bestCost {
			bestParams = append(bestParams, candidateCircle...)
			bestCost = candidateCost

			if accumulator != nil {
				accumulator.retain(session.Render(candidateCircle))
			}
		}

		var retainedImage *image.NRGBA

		candidateRetained := len(bestParams)/paramsPerCircle > retainedCircles
		if callback != nil && candidateRetained {
			if accumulator != nil {
				retainedImage = cloneNRGBA(accumulator.canvas)
			} else {
				retainedImage = cloneNRGBA(session.Render(bestParams))
			}
		}

		cleanup()

		if callback != nil && candidateRetained {
			callback(len(bestParams)/paramsPerCircle, append([]float64(nil), bestParams...), bestCost, retainedImage)
		}

		if tracker.Update(bestCost) {
			slog.Info("Sequential convergence detected", "circles_optimized", len(bestParams)/paramsPerCircle, "circles_attempted", circleNum, "circles_requested", totalCircles)

			termination = TerminationStageConvergence

			break
		}
	}

	result, err := finishStagedResult(base, accumulator, bestParams, bestCost, initialCost, evaluations, stages)
	if err != nil {
		return nil, err
	}

	result.OptimizedCircles = len(bestParams) / paramsPerCircle
	result.Iterations = iterations
	result.Termination = termination
	result.StagesStoppedEarly = stagesStoppedEarly
	slog.Info("Sequential optimization complete",
		"stages", stages,
		"stages_stopped_early", stagesStoppedEarly,
		"termination", termination,
	)

	return result, nil
}

// OptimizeBatch attempts totalCircles, adding at most batchSize circles per
// stage. Invalid or worsening batches are omitted, so a result can contain
// fewer circles than requested. The final stage uses the remaining budget.
func OptimizeBatch(base Renderer, optimizer opt.Optimizer, totalCircles, batchSize int, convergenceConfig ConvergenceConfig) (*OptimizationResult, error) {
	return OptimizeBatchContext(context.Background(), base, optimizer, totalCircles, batchSize, convergenceConfig)
}

// OptimizeBatchContext is OptimizeBatch with cooperative cancellation when
// the optimizer implements opt.LifecycleOptimizer.
func OptimizeBatchContext(ctx context.Context, base Renderer, optimizer opt.Optimizer, totalCircles, batchSize int, convergenceConfig ConvergenceConfig) (*OptimizationResult, error) {
	return optimizeBatchContext(ctx, base, optimizer, nil, nil, totalCircles, batchSize, convergenceConfig)
}

// OptimizeBatchAppendContext preserves an already-rendered prefix and appends
// circles after it. The prefix order is immutable: staged optimization only
// receives the remaining suffix dimensions, while progress and the final
// result contain the complete parameter vector.
func OptimizeBatchAppendContext(ctx context.Context, base Renderer, optimizer opt.Optimizer, prefixParams []float64, totalCircles, batchSize int, convergenceConfig ConvergenceConfig) (*OptimizationResult, error) {
	return optimizeBatchContext(ctx, base, optimizer, prefixParams, nil, totalCircles, batchSize, convergenceConfig)
}

// OptimizeBatchAppendFromCanvasContext is OptimizeBatchAppendContext with an
// already-rendered prefix. A completed server checkpoint stores that exact
// image as best.png, so a one-circle extension can restore the retained canvas
// without replaying thousands of immutable circles. The supplied cost must be
// the cost of prefixCanvas against base.Reference(); callers that cannot prove
// that relationship should use OptimizeBatchAppendContext.
func OptimizeBatchAppendFromCanvasContext(
	ctx context.Context,
	base Renderer,
	optimizer opt.Optimizer,
	prefixParams []float64,
	prefixCanvas *image.NRGBA,
	prefixCost float64,
	totalCircles, batchSize int,
	convergenceConfig ConvergenceConfig,
) (*OptimizationResult, error) {
	return optimizeBatchContext(ctx, base, optimizer, prefixParams, &retainedBatchPrefix{
		canvas: prefixCanvas,
		cost:   prefixCost,
	}, totalCircles, batchSize, convergenceConfig)
}

type retainedBatchPrefix struct {
	canvas *image.NRGBA
	cost   float64
}

func optimizeBatchContext(ctx context.Context, base Renderer, optimizer opt.Optimizer, prefixParams []float64, retained *retainedBatchPrefix, totalCircles, batchSize int, convergenceConfig ConvergenceConfig) (*OptimizationResult, error) {
	if err := validatePipelineInputs(base, optimizer, totalCircles); err != nil {
		return nil, err
	}

	if batchSize <= 0 {
		return nil, fmt.Errorf("%w: batch size must be positive", ErrInvalidOptimizationInput)
	}

	if _, ok := base.(rendererSessionFactory); !ok {
		return nil, fmt.Errorf("%w: %T", ErrStagedOptimizationUnsupported, base)
	}

	if len(prefixParams)%paramsPerCircle != 0 {
		return nil, fmt.Errorf("%w: prefix parameter length must be a multiple of %d", ErrInvalidOptimizationInput, paramsPerCircle)
	}

	prefixCircles := len(prefixParams) / paramsPerCircle
	if prefixCircles > totalCircles {
		return nil, fmt.Errorf("%w: prefix has %d circles but total is %d", ErrInvalidOptimizationInput, prefixCircles, totalCircles)
	}

	if prefixCircles > 0 {
		prefixBounds := fit.NewBounds(prefixCircles, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy())
		if !prefixBounds.ValidVector(prefixParams) {
			return nil, fmt.Errorf("%w: prefix parameters are outside valid circle bounds", ErrInvalidOptimizationInput)
		}
	}

	slog.Info("Starting batch optimization", "total_circles", totalCircles, "initial_circles", prefixCircles, "batch_size", batchSize)

	evaluations := 0
	var initialCost float64
	var err error
	accumulator := newStagedAccumulator(base)

	if retained != nil {
		if prefixCircles == 0 {
			return nil, fmt.Errorf("%w: retained prefix requires at least one circle", ErrInvalidOptimizationInput)
		}

		if accumulator == nil {
			return nil, fmt.Errorf("%w: %T cannot start from a retained prefix canvas", ErrStagedOptimizationUnsupported, base)
		}

		if retained.canvas == nil ||
			retained.canvas.Bounds().Dx() != base.Reference().Bounds().Dx() ||
			retained.canvas.Bounds().Dy() != base.Reference().Bounds().Dy() {
			return nil, fmt.Errorf("%w: retained prefix canvas dimensions do not match the reference", ErrInvalidOptimizationInput)
		}

		if math.IsNaN(retained.cost) || math.IsInf(retained.cost, 0) || retained.cost < 0 {
			return nil, fmt.Errorf("%w: retained prefix cost is invalid", ErrInvalidOptimizationInput)
		}

		initialCost = retained.cost
		accumulator.retain(retained.canvas)
	} else if prefixCircles == 0 {
		initialCost, err = baseCanvasCost(base, &evaluations)
	} else {
		var prefixSession Renderer
		var cleanup func()

		prefixSession, cleanup, err = newStagedSession(base, prefixCircles)
		if err == nil {
			evaluations++

			initialCost = prefixSession.Cost(prefixParams)
			if accumulator != nil {
				accumulator.retain(prefixSession.Render(prefixParams))
			}

			cleanup()
		}
	}

	if err != nil {
		return nil, err
	}

	if math.IsNaN(initialCost) || math.IsInf(initialCost, 0) {
		return nil, fmt.Errorf("%w: prefix cost is not finite", ErrInvalidOptimizationInput)
	}

	bestCost := initialCost
	bestParams := make([]float64, len(prefixParams), totalCircles*paramsPerCircle)
	copy(bestParams, prefixParams)

	tracker := NewConvergenceTracker(convergenceConfig)
	stages := 0
	iterations := 0
	optimizedCircles := prefixCircles
	stagesStoppedEarly := 0
	termination := opt.TerminationCompleted

	plannedStages := 0
	if remaining := totalCircles - optimizedCircles; remaining > 0 {
		plannedStages = (remaining + batchSize - 1) / batchSize
	}

	maxStages := plannedStages + MaxExtraBatchStages
	// Every stage costs a full optimizer run, refills included, so a run that
	// loses circles to the post-stage audit would otherwise spend two to four
	// times the iterations its configuration asked for -- silently, and only
	// for the jobs unlucky enough to need a refill. Campaign arms are compared
	// at equal budget, so a refill has to be paid for out of the run's own
	// budget rather than minted on top of it. stageBudget is zero for an
	// optimizer that does not declare a cap, which leaves the loop unbounded
	// exactly as before.
	stageBudget := opt.StageIterationBudget(optimizer)
	plannedIterations := plannedStages * stageBudget

	for optimizedCircles < totalCircles && stages < maxStages {
		if plannedIterations > 0 && iterations+stageBudget > plannedIterations {
			slog.Info("Skipping a refill stage that the run cannot afford",
				"stages", stages,
				"iterations", iterations,
				"planned_iterations", plannedIterations,
				"optimized_circles", optimizedCircles,
				"total_circles", totalCircles,
			)

			break
		}

		stageCircles := min(batchSize, totalCircles-optimizedCircles)
		retainedCircles := len(bestParams) / paramsPerCircle

		sessionCircleCount := retainedCircles + stageCircles
		if accumulator != nil {
			sessionCircleCount = stageCircles
		}

		session, sessionCleanup, err := newStagedSessionForAccumulator(base, accumulator, sessionCircleCount)
		if err != nil {
			return nil, err
		}

		dim := stageCircles * paramsPerCircle
		bounds := fit.NewBounds(stageCircles, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy())

		var combined []float64
		if accumulator == nil {
			combined = make([]float64, len(bestParams)+dim)
			copy(combined, bestParams)
		}

		currentCanvas := currentStageCanvas(session, accumulator, combined)
		stageSessionCircles := sessionCircleCount
		pool := newEvaluationPool(session, combined, evaluationWorkers(base), func() (Renderer, func(), error) {
			return newStagedSessionForAccumulator(base, accumulator, stageSessionCircles)
		})
		// The pool owns every evaluation of this stage, so its count is the
		// stage's evaluation total and must be folded in wherever it is closed.
		cleanup := func() {
			evaluations += pool.count()
			pool.close()
			sessionCleanup()
		}
		// Evaluations run concurrently once the optimizer is configured for it,
		// so each one assembles its parameters in its own leased buffer.
		evaluate := func(newBatch []float64) float64 {
			slot := pool.acquire()
			defer pool.release(slot)

			bounds.ClampIndependentVector(newBatch)

			params := newBatch
			if slot.combined != nil {
				copy(slot.combined[len(bestParams):], newBatch)
				params = slot.combined
			}

			return slot.session.Cost(params)
		}
		constraints := radiusConstraints(bounds, stageCircles)

		seedParams, err := SeedParamsFromResidual(currentCanvas, base.Reference(), stageCircles, ResidualSeedOptions{})
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("%w: seed batch %d: %w", ErrInvalidOptimizationInput, stages+1, err)
		}

		seedCost := evaluate(seedParams)

		progressPrefix := append([]float64(nil), bestParams...)
		runOptions := opt.RunOptions{
			Initial: &opt.Candidate{Params: seedParams, Cost: seedCost},
			ProgressMapper: func(progress opt.Progress) opt.Progress {
				if len(progress.BestParams) == dim {
					progress.BestParams = append(append([]float64(nil), progressPrefix...), progress.BestParams...)
				}

				return progress
			},
		}

		outcome, err := runOptimizer(ctx, optimizer, evaluate, bounds.ClampIndependentVector, bounds.ClampVector, constraints, bounds.Lower, bounds.Upper, dim, runOptions)
		if err != nil {
			cleanup()
			return nil, err
		}

		candidateBatch := outcome.Params
		iterations += outcome.Iterations
		stages++

		if outcome.stoppedEarly() {
			stagesStoppedEarly++
		}

		if err := validateParamLength(candidateBatch, dim); err != nil {
			cleanup()
			return nil, fmt.Errorf("%w: optimizer result for batch %d: %w", ErrInvalidOptimizationInput, stages, err)
		}

		retainedBatch := []float64(nil)

		if bounds.ValidVector(candidateBatch) {
			auditRenderer := NewCPURendererWithCanvas(base.Reference(), currentCanvas, stageCircles)
			if cpu, ok := session.(*CPURenderer); ok {
				auditRenderer.SetThreads(cpu.Threads())
				auditRenderer.SetFastCompositing(cpu.FastCompositing())
			}

			pruned, pruneErr := PruneCircleBatch(auditRenderer, candidateBatch, CirclePruneOptions{
				MinChangedPixels:   1,
				MinMSEContribution: minBatchMSEContribution,
			})
			if pruneErr != nil {
				cleanup()
				return nil, fmt.Errorf("audit batch %d: %w", stages, pruneErr)
			}

			retainedBatch = pruned.Params
			if len(pruned.Removed) > 0 && len(pruned.Params) > 0 {
				// Dropping a circle leaves the stage short of the count it was
				// asked to place, and the only way this loop can fill that hole
				// is a refill stage -- a whole further optimizer run at the
				// full configured budget. A batch that improves the image is
				// therefore kept as the optimizer produced it: a weak circle is
				// not worth doubling what the run spends, and the doubling is
				// invisible in every artifact except the iteration count.
				//
				// A batch the audit empties is a different matter. Nothing in
				// it earned its place, so there is nothing to keep and the
				// refill below is the whole point of the stage.
				slog.Info("Keeping weak batch circles a refill would have to buy back",
					"stage", stages,
					"weak", len(pruned.Removed),
					"circles", len(candidateBatch)/paramsPerCircle,
				)

				retainedBatch = candidateBatch
			}
		}

		cleanup()

		if len(retainedBatch) > 0 {
			retainedSessionCircles := retainedCircles + len(retainedBatch)/paramsPerCircle
			if accumulator != nil {
				retainedSessionCircles = len(retainedBatch) / paramsPerCircle
			}

			retainedSession, retainedCleanup, retainErr := newStagedSessionForAccumulator(base, accumulator, retainedSessionCircles)
			if retainErr != nil {
				return nil, retainErr
			}

			retainedParams := retainedBatch
			if accumulator == nil {
				retainedParams = append(append([]float64(nil), bestParams...), retainedBatch...)
			}

			evaluations++

			retainedCost := retainedSession.Cost(retainedParams)
			if retainedCost < bestCost {
				bestParams = append(bestParams, retainedBatch...)
				bestCost = retainedCost

				if accumulator != nil {
					accumulator.retain(retainedSession.Render(retainedBatch))
				}
			}

			retainedCleanup()
		}

		optimizedCircles = len(bestParams) / paramsPerCircle

		if tracker.Update(bestCost) {
			termination = TerminationStageConvergence
			break
		}
	}

	if optimizedCircles < totalCircles && termination == opt.TerminationCompleted {
		termination = TerminationRefillLimit
	}

	result, err := finishStagedResult(base, accumulator, bestParams, bestCost, initialCost, evaluations, stages)
	if err != nil {
		return nil, err
	}

	result.OptimizedCircles = optimizedCircles
	result.Iterations = iterations
	result.Termination = termination
	result.StagesStoppedEarly = stagesStoppedEarly
	slog.Info("Batch optimization complete",
		"stages", stages,
		"stages_stopped_early", stagesStoppedEarly,
		"termination", termination,
	)

	return result, nil
}

func runOptimizer(
	ctx context.Context,
	optimizer opt.Optimizer,
	evaluate func([]float64) float64,
	repair, fallbackRepair func([]float64),
	inequalities []opt.InequalityConstraint,
	lower, upper []float64,
	dim int,
	options opt.RunOptions,
) (stageOutcome, error) {
	if lifecycle, ok := optimizer.(opt.LifecycleOptimizer); ok {
		result, err := lifecycle.RunContext(ctx, opt.Problem{
			Eval:         evaluate,
			Repair:       repair,
			Inequalities: inequalities,
			Lower:        lower,
			Upper:        upper,
			Dim:          dim,
		}, options)
		if err != nil {
			return stageOutcome{Iterations: result.Iterations, Termination: result.Termination}, err
		}

		return stageOutcome{
			Params:      result.BestParams,
			Iterations:  result.Iterations,
			Termination: result.Termination,
		}, nil
	}

	select {
	case <-ctx.Done():
		return stageOutcome{}, ctx.Err()
	default:
	}

	plainEvaluate := evaluate
	if fallbackRepair != nil {
		plainEvaluate = func(params []float64) float64 {
			fallbackRepair(params)
			return evaluate(params)
		}
	}

	params, _ := optimizer.Run(plainEvaluate, lower, upper, dim)
	if fallbackRepair != nil {
		fallbackRepair(params)
	}

	select {
	case <-ctx.Done():
		return stageOutcome{}, ctx.Err()
	default:
	}
	// The plain Optimizer interface reports no reason; "completed" matches what
	// the pipeline assumed before termination reasons were propagated.
	return stageOutcome{Params: params, Termination: opt.TerminationCompleted}, nil
}

func radiusConstraints(bounds *fit.Bounds, circleCount int) []opt.InequalityConstraint {
	constraints := make([]opt.InequalityConstraint, circleCount)
	for circle := range circleCount {
		constraints[circle] = func(params []float64) float64 {
			if len(params) != circleCount*paramsPerCircle {
				return math.Inf(1)
			}

			vector := fit.ParamVector{Data: params, K: circleCount, Width: bounds.Width, Height: bounds.Height}

			return bounds.RadiusViolation(vector.DecodeCircle(circle))
		}
	}

	return constraints
}

func validatePipelineInputs(renderer Renderer, optimizer opt.Optimizer, circleCount int) error {
	if renderer == nil {
		return fmt.Errorf("%w: renderer is nil", ErrInvalidOptimizationInput)
	}

	if optimizer == nil && circleCount > 0 {
		return fmt.Errorf("%w: optimizer is nil", ErrInvalidOptimizationInput)
	}

	if circleCount < 0 {
		return fmt.Errorf("%w: circle count cannot be negative", ErrInvalidOptimizationInput)
	}

	ref := renderer.Reference()
	if ref == nil || ref.Bounds().Empty() {
		return fmt.Errorf("%w: reference image is empty", ErrInvalidOptimizationInput)
	}

	return nil
}

func validateParamLength(params []float64, expected int) error {
	if len(params) != expected {
		return fmt.Errorf("parameter length %d, want %d", len(params), expected)
	}

	return nil
}

func exactBounds(renderer Renderer, dim int) ([]float64, []float64, error) {
	lower, upper := renderer.Bounds()
	if len(lower) != dim || len(upper) != dim || renderer.Dim() != dim {
		return nil, nil, fmt.Errorf("%w: renderer dimension/bounds do not match %d", ErrInvalidOptimizationInput, dim)
	}

	return lower, upper, nil
}

func sessionForJoint(base Renderer, circleCount int) (Renderer, func(), error) {
	if base.Dim() == circleCount*paramsPerCircle {
		return base, noopCleanup, nil
	}

	factory, ok := base.(rendererSessionFactory)
	if !ok {
		return nil, noopCleanup, fmt.Errorf("%w: renderer dimension %d does not match %d", ErrInvalidOptimizationInput, base.Dim(), circleCount*paramsPerCircle)
	}

	return factory.newSession(circleCount)
}

func newStagedSession(base Renderer, circleCount int) (Renderer, func(), error) {
	factory, ok := base.(rendererSessionFactory)
	if !ok {
		return nil, noopCleanup, fmt.Errorf("%w: %T", ErrStagedOptimizationUnsupported, base)
	}

	return factory.newSession(circleCount)
}

func newStagedAccumulator(base Renderer) *stagedAccumulator {
	factory, ok := base.(accumulatedSessionFactory)
	if !ok {
		return nil
	}

	canvas := factory.initialCanvas()
	if canvas == nil {
		return nil
	}

	return &stagedAccumulator{factory: factory, canvas: canvas}
}

func newStagedSessionForAccumulator(base Renderer, accumulator *stagedAccumulator, circleCount int) (Renderer, func(), error) {
	if accumulator == nil {
		return newStagedSession(base, circleCount)
	}

	return accumulator.factory.newSessionWithCanvas(accumulator.canvas, circleCount)
}

// bakePrefixCanvas rasterizes an immutable draw-order prefix once and returns
// the canvas it produced. The canvas depends only on the fixed prefix, so one
// bake backs every session a caller needs over it -- rendering the prefix again
// per session would pay the very cost baking exists to remove.
//
// The second result reports whether baking applies. It does not for a
// zero-length prefix, or for backends that cannot start a session from a
// supplied canvas; the caller then keeps evaluating the complete vector.
func bakePrefixCanvas(base Renderer, params []float64, prefixCircles, circleCount int) (*image.NRGBA, bool) {
	if prefixCircles <= 0 || prefixCircles >= circleCount {
		return nil, false
	}

	factory, ok := base.(accumulatedSessionFactory)
	if !ok {
		return nil, false
	}

	prefixSession, prefixCleanup, err := factory.newSession(prefixCircles)
	if err != nil {
		return nil, false
	}
	defer prefixCleanup()

	return cloneNRGBA(prefixSession.Render(params[:prefixCircles*paramsPerCircle])), true
}

// sessionOverBakedCanvas creates a session that evaluates only suffixCircles
// circles over an already-baked canvas. The caller keeps passing complete
// parameter vectors and hands the session the matching suffix, so the fixed
// prefix is never rasterized again per evaluation.
//
// The session copies the canvas it is given, so several independent sessions
// may be created from one baked canvas.
func sessionOverBakedCanvas(base Renderer, baked *image.NRGBA, suffixCircles int) (Renderer, func(), bool) {
	factory, ok := base.(accumulatedSessionFactory)
	if !ok {
		return nil, noopCleanup, false
	}

	session, cleanup, err := factory.newSessionWithCanvas(baked, suffixCircles)
	if err != nil {
		return nil, noopCleanup, false
	}

	return session, cleanup, true
}

// bakedSuffixSession bakes the prefix and opens a single session over it. Use
// bakePrefixCanvas directly when more than one session is needed, so the prefix
// is rendered once rather than once per session.
func bakedSuffixSession(base Renderer, params []float64, prefixCircles, circleCount int) (Renderer, func(), bool) {
	baked, ok := bakePrefixCanvas(base, params, prefixCircles, circleCount)
	if !ok {
		return nil, noopCleanup, false
	}

	return sessionOverBakedCanvas(base, baked, circleCount-prefixCircles)
}

func (a *stagedAccumulator) retain(canvas *image.NRGBA) {
	a.canvas = cloneNRGBA(canvas)
}

func currentStageCanvas(session Renderer, accumulator *stagedAccumulator, combined []float64) *image.NRGBA {
	if accumulator != nil {
		return cloneNRGBA(accumulator.canvas)
	}

	return cloneNRGBA(session.Render(combined))
}

func baseCanvasCost(base Renderer, evaluations *int) (float64, error) {
	session, cleanup, err := newStagedSession(base, 0)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	(*evaluations)++

	cost := session.Cost(nil)
	if math.IsNaN(cost) {
		return 0, fmt.Errorf("%w: base canvas cost is NaN", ErrInvalidOptimizationInput)
	}

	return cost, nil
}

func finishStagedResult(base Renderer, accumulator *stagedAccumulator, params []float64, bestCost, initialCost float64, evaluations, stages int) (*OptimizationResult, error) {
	if accumulator != nil {
		if accumulator.canvas == nil ||
			accumulator.canvas.Bounds().Dx() != base.Reference().Bounds().Dx() ||
			accumulator.canvas.Bounds().Dy() != base.Reference().Bounds().Dy() {
			return nil, fmt.Errorf("%w: retained final canvas dimensions do not match the reference", ErrInvalidOptimizationInput)
		}

		return &OptimizationResult{
			BestParams:       append([]float64(nil), params...),
			BestCost:         bestCost,
			InitialCost:      initialCost,
			Evaluations:      evaluations,
			Stages:           stages,
			OptimizedCircles: len(params) / paramsPerCircle,
			BestImage:        cloneNRGBA(accumulator.canvas),
			Termination:      opt.TerminationCompleted,
		}, nil
	}

	session, cleanup, err := newStagedSession(base, len(params)/paramsPerCircle)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return finishResult(session, params, bestCost, initialCost, evaluations, stages, len(params)/paramsPerCircle)
}

func finishResult(session Renderer, params []float64, bestCost, initialCost float64, evaluations, stages, optimizedCircles int) (*OptimizationResult, error) {
	err := validateParamLength(params, session.Dim())
	if err != nil {
		return nil, fmt.Errorf("%w: final result: %w", ErrInvalidOptimizationInput, err)
	}

	return &OptimizationResult{
		BestParams:       append([]float64(nil), params...),
		BestCost:         bestCost,
		InitialCost:      initialCost,
		Evaluations:      evaluations,
		Stages:           stages,
		OptimizedCircles: optimizedCircles,
		BestImage:        cloneNRGBA(session.Render(params)),
		// Callers overwrite this with the reason they observed; defaulting here
		// keeps every path from returning an empty termination.
		Termination: opt.TerminationCompleted,
	}, nil
}

func finishBaseResult(base Renderer, bestCost, initialCost float64, evaluations, stages int) (*OptimizationResult, error) {
	baseImage := base.Render(transparentParams(base.Dim() / paramsPerCircle))
	if factory, ok := base.(accumulatedSessionFactory); ok {
		baseImage = factory.initialCanvas()
	}

	return &OptimizationResult{
		BestCost:    bestCost,
		InitialCost: initialCost,
		Evaluations: evaluations,
		Stages:      stages,
		BestImage:   cloneNRGBA(baseImage),
		Termination: opt.TerminationCompleted,
	}, nil
}

func transparentParams(circleCount int) []float64 {
	params := make([]float64, circleCount*paramsPerCircle)
	for i := range circleCount {
		params[i*paramsPerCircle+2] = 1 // Valid radius; opacity remains zero.
	}

	return params
}

func cloneNRGBA(src *image.NRGBA) *image.NRGBA {
	if src == nil {
		return nil
	}

	bounds := src.Bounds()

	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		srcOffset := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		dstOffset := dst.PixOffset(0, y)
		copy(dst.Pix[dstOffset:dstOffset+bounds.Dx()*4], src.Pix[srcOffset:srcOffset+bounds.Dx()*4])
	}

	return dst
}
