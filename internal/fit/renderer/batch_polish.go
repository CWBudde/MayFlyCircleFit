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
	Strategy      BatchPolishStrategy
	Observer      opt.Observer
	OnEpoch       func(BatchPolishEpoch) error
	OnSweep       func(BatchPolishProgress) error
}

// BatchPolishStrategy selects how a polishing active set and its population
// are formed.
type BatchPolishStrategy string

const (
	// BatchPolishWeakestReplacement replaces the weakest circles with residual
	// seeds. It preserves the original polishing behavior.
	BatchPolishWeakestReplacement BatchPolishStrategy = "replacement"
	// BatchPolishHybridOverlap retains weak anchors, adds their strongest
	// overlap partners, and mixes incumbent-local and residual populations.
	BatchPolishHybridOverlap BatchPolishStrategy = "hybrid-overlap"
)

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
	if options.Strategy == "" {
		options.Strategy = BatchPolishWeakestReplacement
	}
	if options.Strategy != BatchPolishWeakestReplacement && options.Strategy != BatchPolishHybridOverlap {
		return nil, fmt.Errorf("%w: unsupported polishing strategy %q", ErrInvalidOptimizationInput, options.Strategy)
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

		activeCircles, retainedParams, err := selectPolishingActiveSet(
			fullSession,
			bestParams,
			options.ActiveSetSize,
			options.Strategy,
		)
		if err != nil {
			return nil, fmt.Errorf("select polishing active set: %w", err)
		}

		retainedSession, retainedCleanup, err := newStagedSession(base, circleCount-options.ActiveSetSize)
		if err != nil {
			return nil, err
		}
		residualCanvas := cloneNRGBA(retainedSession.Render(retainedParams))
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
		initial := opt.Candidate{Params: seedParams, Cost: seedCost}
		var additionalSeeds []opt.Candidate
		if options.Strategy == BatchPolishHybridOverlap {
			incumbentParams := extractActiveCircleParams(bestParams, activeCircles)
			initial = opt.Candidate{Params: incumbentParams, Cost: bestCost}
			additionalSeeds = []opt.Candidate{{Params: seedParams, Cost: seedCost}}
		}

		iterationOffset := result.Iterations
		evaluationOffset := evaluations
		observer := options.Observer
		runOptions := opt.RunOptions{
			Initial:         &initial,
			AdditionalSeeds: additionalSeeds,
			ResumeCount:     sweep,
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

func extractActiveCircleParams(fullParams []float64, activeCircles []int) []float64 {
	active := make([]float64, len(activeCircles)*paramsPerCircle)
	for index, circle := range activeCircles {
		copy(active[index*paramsPerCircle:(index+1)*paramsPerCircle], fullParams[circle*paramsPerCircle:(circle+1)*paramsPerCircle])
	}
	return active
}

func selectPolishingActiveSet(
	base Renderer,
	params []float64,
	activeSetSize int,
	strategy BatchPolishStrategy,
) ([]int, []float64, error) {
	if strategy == BatchPolishWeakestReplacement {
		pruned, err := PruneCircleBatch(base, params, CirclePruneOptions{
			MinChangedPixels:   1,
			MinMSEContribution: math.MaxFloat64,
			MaxRemoved:         activeSetSize,
		})
		if err != nil {
			return nil, nil, err
		}
		if len(pruned.Removed) != activeSetSize {
			return nil, nil, fmt.Errorf("%w: selected %d polishing circles, want %d", ErrInvalidOptimizationInput, len(pruned.Removed), activeSetSize)
		}
		active := make([]int, len(pruned.Removed))
		for i, removal := range pruned.Removed {
			active[i] = removal.OriginalCircle - 1
		}
		slices.Sort(active)
		return active, pruned.Params, nil
	}

	audit, err := AuditCircleBatch(base, params)
	if err != nil {
		return nil, nil, err
	}
	active := selectHybridOverlapCircles(params, audit, activeSetSize, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy())
	return active, removeActiveCircleParams(params, active), nil
}

func selectHybridOverlapCircles(params []float64, audit BatchAudit, activeSetSize, width, height int) []int {
	weakest := make([]CircleAudit, len(audit.Circles))
	copy(weakest, audit.Circles)
	slices.SortFunc(weakest, func(left, right CircleAudit) int {
		if left.MSEContribution < right.MSEContribution {
			return -1
		}
		if left.MSEContribution > right.MSEContribution {
			return 1
		}
		if left.FinalChangedPixels != right.FinalChangedPixels {
			return left.FinalChangedPixels - right.FinalChangedPixels
		}
		return left.OriginalCircle - right.OriginalCircle
	})

	anchorCount := min((activeSetSize+1)/2, len(weakest))
	selected := make(map[int]bool, activeSetSize)
	anchors := make([]int, 0, anchorCount)
	for _, circle := range weakest[:anchorCount] {
		index := circle.OriginalCircle - 1
		selected[index] = true
		anchors = append(anchors, index)
	}
	vector := fit.ParamVector{Data: params, K: len(params) / paramsPerCircle, Width: width, Height: height}
	for len(selected) < activeSetSize {
		bestCircle, bestOverlap := -1, -1
		for candidate := 0; candidate < vector.K; candidate++ {
			if selected[candidate] {
				continue
			}
			overlap := 0
			for _, anchor := range anchors {
				overlap += circleRasterOverlap(vector.DecodeCircle(candidate), vector.DecodeCircle(anchor), width, height)
			}
			if overlap > bestOverlap || overlap == bestOverlap && weakerAuditCircle(audit.Circles[candidate], audit.Circles[bestCircle]) {
				bestCircle, bestOverlap = candidate, overlap
			}
		}
		if bestCircle < 0 {
			break
		}
		selected[bestCircle] = true
	}
	active := make([]int, 0, len(selected))
	for circle := range selected {
		active = append(active, circle)
	}
	slices.Sort(active)
	return active
}

func weakerAuditCircle(left, right CircleAudit) bool {
	if right.Circle == 0 {
		return true
	}
	if left.MSEContribution != right.MSEContribution {
		return left.MSEContribution < right.MSEContribution
	}
	if left.FinalChangedPixels != right.FinalChangedPixels {
		return left.FinalChangedPixels < right.FinalChangedPixels
	}
	return left.OriginalCircle < right.OriginalCircle
}

func circleRasterOverlap(left, right fit.Circle, width, height int) int {
	overlap := 0
	for y := 0; y < height; y++ {
		leftStart, leftEnd, leftOK := circleRasterSpan(left, y, width)
		rightStart, rightEnd, rightOK := circleRasterSpan(right, y, width)
		if leftOK && rightOK {
			overlap += max(0, min(leftEnd, rightEnd)-max(leftStart, rightStart))
		}
	}
	return overlap
}

func circleRasterSpan(circle fit.Circle, y, width int) (int, int, bool) {
	if fixed, ok := newFixedCircleQ16(circle); ok {
		return fixed.span(y, width)
	}
	dy := float64(y) - circle.Y
	remaining := circle.R*circle.R - dy*dy
	if remaining < 0 {
		return 0, 0, false
	}
	start, end := circleSpanFloat64(circle.X, remaining, width)
	start = max(0, start)
	end = min(width, end)
	return start, end, end > start
}

func removeActiveCircleParams(params []float64, activeCircles []int) []float64 {
	active := make(map[int]bool, len(activeCircles))
	for _, circle := range activeCircles {
		active[circle] = true
	}
	retained := make([]float64, 0, len(params)-len(activeCircles)*paramsPerCircle)
	for circle := 0; circle < len(params)/paramsPerCircle; circle++ {
		if !active[circle] {
			retained = append(retained, params[circle*paramsPerCircle:(circle+1)*paramsPerCircle]...)
		}
	}
	return retained
}

func allCirclesUseful(audit BatchAudit, minContribution float64) bool {
	for _, circle := range audit.Circles {
		if !circle.Valid || circle.FinalChangedPixels < 1 || circle.MSEContribution <= minContribution {
			return false
		}
	}
	return true
}
