package renderer

import (
	"context"
	"fmt"
	"image"
	"math"
	"slices"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// BatchPolishOptions controls transactional active-set polishing after a
// complete batch solution has been found. The weakest circles are optimized
// together while every other circle remains fixed in its original draw slot.
type BatchPolishOptions struct {
	ActiveSetSize int
	MaxSweeps     int
	Observer      opt.Observer
	OnEpoch       func(BatchPolishEpoch) error
	OnSweep       func(BatchPolishProgress) error
}

// BatchPolishEpoch reports a durable full-vector optimizer epoch boundary.
type BatchPolishEpoch struct {
	Sweep       int
	Epoch       int
	BestParams  []float64
	BestCost    float64
	Iterations  int
	Evaluations int
}

// BatchPolishProgress is emitted after each committed or rejected sweep.
// BestParams always describes the complete image, never only the active set.
type BatchPolishProgress struct {
	Sweep       int
	Accepted    bool
	BestParams  []float64
	BestCost    float64
	Iterations  int
	Evaluations int
}

// BatchPolishResult is the best complete solution retained by active-set
// polishing. A rejected sweep is never reflected in BestParams or BestImage.
type BatchPolishResult struct {
	BestParams     []float64
	BestCost       float64
	BestImage      *image.NRGBA
	Iterations     int
	Evaluations    int
	Sweeps         int
	AcceptedSweeps int
}

// PolishCircleBatchContext repeatedly re-optimizes the weakest circle group.
// Each sweep is transactional: it is committed only when every circle remains
// useful and the cost of the complete, original-order parameter vector falls.
// The first rejected sweep stops polishing.
func PolishCircleBatchContext(
	ctx context.Context,
	base Renderer,
	optimizer opt.Optimizer,
	initialParams []float64,
	options BatchPolishOptions,
) (*BatchPolishResult, error) {
	if base == nil {
		return nil, fmt.Errorf("%w: renderer is nil", ErrInvalidOptimizationInput)
	}
	if optimizer == nil {
		return nil, fmt.Errorf("%w: optimizer is nil", ErrInvalidOptimizationInput)
	}
	if len(initialParams) == 0 || len(initialParams)%paramsPerCircle != 0 {
		return nil, fmt.Errorf("%w: polishing parameters must contain complete circles", ErrInvalidOptimizationInput)
	}
	circleCount := len(initialParams) / paramsPerCircle
	if options.ActiveSetSize < 1 || options.ActiveSetSize > circleCount {
		return nil, fmt.Errorf("%w: active set size must be in [1, %d]", ErrInvalidOptimizationInput, circleCount)
	}
	if options.MaxSweeps < 0 {
		return nil, fmt.Errorf("%w: maximum polishing sweeps cannot be negative", ErrInvalidOptimizationInput)
	}
	fullSession, cleanup, err := sessionForJoint(base, circleCount)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if _, _, err := exactBounds(fullSession, len(initialParams)); err != nil {
		return nil, err
	}

	bestParams := append([]float64(nil), initialParams...)
	evaluations := 1
	bestCost := fullSession.Cost(bestParams)
	if math.IsNaN(bestCost) || math.IsInf(bestCost, 0) {
		return nil, fmt.Errorf("%w: evaluated polishing cost must be finite", ErrInvalidOptimizationInput)
	}
	result := &BatchPolishResult{
		BestParams: append([]float64(nil), bestParams...),
		BestCost:   bestCost,
	}
	if options.MaxSweeps == 0 {
		result.Evaluations = evaluations
		result.BestImage = cloneNRGBA(fullSession.Render(bestParams))
		return result, nil
	}

	for sweep := 1; sweep <= options.MaxSweeps; sweep++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		pruned, err := PruneCircleBatch(fullSession, bestParams, CirclePruneOptions{
			MinChangedPixels:   1,
			MinMSEContribution: math.MaxFloat64,
			MaxRemoved:         options.ActiveSetSize,
		})
		if err != nil {
			return nil, fmt.Errorf("select polishing active set: %w", err)
		}
		if len(pruned.Removed) != options.ActiveSetSize {
			return nil, fmt.Errorf("%w: selected %d polishing circles, want %d", ErrInvalidOptimizationInput, len(pruned.Removed), options.ActiveSetSize)
		}

		activeCircles := make([]int, len(pruned.Removed))
		for i, removal := range pruned.Removed {
			activeCircles[i] = removal.OriginalCircle - 1
		}
		slices.Sort(activeCircles)

		retainedSession, retainedCleanup, err := newStagedSession(base, circleCount-options.ActiveSetSize)
		if err != nil {
			return nil, err
		}
		residualCanvas := cloneNRGBA(retainedSession.Render(pruned.Params))
		retainedCleanup()

		seedParams, err := SeedParamsFromResidual(residualCanvas, base.Reference(), options.ActiveSetSize, ResidualSeedOptions{})
		if err != nil {
			return nil, fmt.Errorf("seed polishing sweep %d: %w", sweep, err)
		}
		bounds := fit.NewBounds(options.ActiveSetSize, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy())
		candidateFull := append([]float64(nil), bestParams...)
		mergeActiveCircleParams(candidateFull, activeCircles, seedParams)
		evaluate := func(activeParams []float64) float64 {
			evaluations++
			bounds.ClampIndependentVector(activeParams)
			mergeActiveCircleParams(candidateFull, activeCircles, activeParams)
			return fullSession.Cost(candidateFull)
		}
		seedCost := evaluate(seedParams)

		iterationOffset := result.Iterations
		evaluationOffset := evaluations
		observer := options.Observer
		runOptions := opt.RunOptions{
			Initial:     &opt.Candidate{Params: seedParams, Cost: seedCost},
			ResumeCount: sweep,
		}
		if observer != nil {
			snapshot := append([]float64(nil), bestParams...)
			runOptions.Observer = func(progress opt.Progress) {
				fullParams := append([]float64(nil), snapshot...)
				mergeActiveCircleParams(fullParams, activeCircles, progress.BestParams)
				observer(opt.Progress{
					Iterations:  iterationOffset + progress.Iterations,
					Evaluations: evaluationOffset + progress.Evaluations,
					BestParams:  fullParams,
					BestCost:    progress.BestCost,
				})
			}
		}
		if options.OnEpoch != nil {
			snapshot := append([]float64(nil), bestParams...)
			runOptions.EpochObserver = func(boundary opt.EpochBoundary) error {
				fullParams := append([]float64(nil), snapshot...)
				mergeActiveCircleParams(fullParams, activeCircles, boundary.Progress.BestParams)
				return options.OnEpoch(BatchPolishEpoch{
					Sweep:       sweep,
					Epoch:       boundary.Epoch,
					BestParams:  fullParams,
					BestCost:    boundary.Progress.BestCost,
					Iterations:  iterationOffset + boundary.Progress.Iterations,
					Evaluations: evaluationOffset + boundary.Progress.Evaluations,
				})
			}
		}

		outcome, err := runOptimizer(
			ctx,
			optimizer,
			evaluate,
			bounds.ClampIndependentVector,
			bounds.ClampVector,
			radiusConstraints(bounds, options.ActiveSetSize),
			bounds.Lower,
			bounds.Upper,
			options.ActiveSetSize*paramsPerCircle,
			runOptions,
		)
		if err != nil {
			return nil, err
		}
		result.Iterations += outcome.Iterations
		result.Sweeps++

		accepted := false
		if len(outcome.Params) == options.ActiveSetSize*paramsPerCircle && bounds.ValidVector(outcome.Params) {
			candidate := append([]float64(nil), bestParams...)
			mergeActiveCircleParams(candidate, activeCircles, outcome.Params)
			candidateCost := fullSession.Cost(candidate)
			evaluations++
			if candidateCost < bestCost {
				audit, auditErr := AuditCircleBatch(fullSession, candidate)
				if auditErr != nil {
					return nil, fmt.Errorf("audit polishing sweep %d: %w", sweep, auditErr)
				}
				if allCirclesUseful(audit, minBatchMSEContribution) {
					bestParams = candidate
					bestCost = candidateCost
					result.AcceptedSweeps++
					accepted = true
				}
			}
		}

		result.BestParams = append(result.BestParams[:0], bestParams...)
		result.BestCost = bestCost
		result.Evaluations = evaluations
		progress := BatchPolishProgress{
			Sweep:       sweep,
			Accepted:    accepted,
			BestParams:  append([]float64(nil), bestParams...),
			BestCost:    bestCost,
			Iterations:  result.Iterations,
			Evaluations: evaluations,
		}
		if options.OnSweep != nil {
			if err := options.OnSweep(progress); err != nil {
				return nil, fmt.Errorf("persist polishing sweep %d: %w", sweep, err)
			}
		}
		if !accepted {
			break
		}
	}

	result.BestImage = cloneNRGBA(fullSession.Render(bestParams))
	return result, nil
}

func mergeActiveCircleParams(fullParams []float64, activeCircles []int, activeParams []float64) {
	for active, circle := range activeCircles {
		fullOffset := circle * paramsPerCircle
		activeOffset := active * paramsPerCircle
		copy(fullParams[fullOffset:fullOffset+paramsPerCircle], activeParams[activeOffset:activeOffset+paramsPerCircle])
	}
}

func allCirclesUseful(audit BatchAudit, minContribution float64) bool {
	for _, circle := range audit.Circles {
		if !circle.Valid || circle.FinalChangedPixels < 1 || circle.MSEContribution <= minContribution {
			return false
		}
	}
	return true
}
