package renderer

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"math"
	"slices"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// BatchPolishOptions controls transactional active-set polishing after a
// complete batch solution has been found. Selected circles are optimized
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
	// BatchPolishResidualRegion visits high-error image regions, retaining the
	// circles that influence each region while residual-seeding weak draw slots.
	BatchPolishResidualRegion BatchPolishStrategy = "residual-region"
	// BatchPolishContiguousWindow polishes a contiguous run of circles in draw
	// order, sliding the window toward the front of the vector across sweeps.
	//
	// The other strategies pick circles by image-space merit, which scatters the
	// active set through the draw order. Because only the circles before the
	// first active slot can be baked into a reusable canvas, an active set that
	// contains an early circle bakes nothing and every candidate rasterizes the
	// whole image. Selecting a contiguous window instead makes the baked prefix
	// exactly the window start, so per-candidate render cost is
	// circleCount-windowStart rather than always circleCount.
	BatchPolishContiguousWindow BatchPolishStrategy = "contiguous-window"
)

const residualPolishGridSize = 4

type polishingActiveSet struct {
	Circles            []int
	RetainedParams     []float64
	ReplacementCircles []int
	Region             image.Rectangle
	RegionIndex        int
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
	Region      image.Rectangle
	ActiveSet   []int
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

// PolishCircleBatchContext repeatedly re-optimizes coverage-aware circle
// groups. Each sweep is transactional: it is committed only when every circle
// remains useful and the cost of the complete, original-order parameter vector
// falls. Rejected groups are rolled back, but do not prevent later sweeps from
// visiting other circles.
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
	// Polishing's sweep evaluator is re-entrant only through a session pool: each
	// evaluation leases a slot carrying its own scratch parameter vector and its
	// own session, so no two candidates merge into the same vector or composite
	// into the same canvas. A pool needs a backend that can hand out independent
	// sessions. Without one there is nothing to lease, and driving the shared
	// evaluator from a parallel optimizer would race the vector, the canvas, and
	// the evaluation counter -- silently, as a plausible wrong cost and a wrong
	// image, with no error. Refuse that combination rather than run it.
	//
	// Handing out sessions is necessary but not sufficient: OpenCL can create
	// sessions too, yet each one carries its own device state and the backend
	// has never been validated with several of them evaluating at once. So the
	// backend must also advertise concurrent evaluation by implementing
	// parallelEvaluationRenderer, which is exactly the marker OpenCL withholds.
	optimizerWorkers := opt.ParallelEvaluationWidth(optimizer)
	if optimizerWorkers > 1 {
		_, poolable := base.(rendererSessionFactory)
		_, concurrent := base.(parallelEvaluationRenderer)
		if !poolable || !concurrent {
			return nil, fmt.Errorf(
				"%w: polishing cannot pool concurrent sessions for %T, so it requires a serial optimizer, got one configured for %d concurrent evaluations",
				ErrInvalidOptimizationInput, base, optimizerWorkers)
		}
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
	if options.Strategy != BatchPolishWeakestReplacement &&
		options.Strategy != BatchPolishHybridOverlap &&
		options.Strategy != BatchPolishResidualRegion &&
		options.Strategy != BatchPolishContiguousWindow {
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

	visitedRegions := make(map[int]bool)
	visitCounts := make(map[int]int, circleCount)
	// Each sweep opens its own baked-prefix sessions and its own evaluation pool.
	// Draining the previous sweep's cleanups at the top of the next one, and
	// whatever is left on return, runs every cleanup exactly once and keeps no
	// more than one sweep's sessions alive, however the loop exits.
	var sweepCleanups []func()
	releaseSweep := func() {
		for _, sweepCleanup := range sweepCleanups {
			sweepCleanup()
		}
		sweepCleanups = nil
	}
	defer releaseSweep()
	for sweep := 1; sweep <= options.MaxSweeps; sweep++ {
		releaseSweep()
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		selection, err := selectPolishingActiveSet(
			fullSession,
			bestParams,
			options.ActiveSetSize,
			options.Strategy,
			visitedRegions,
			visitCounts,
		)
		if err != nil {
			return nil, fmt.Errorf("select polishing active set: %w", err)
		}
		activeCircles := selection.Circles
		if options.Strategy == BatchPolishResidualRegion {
			visitedRegions[selection.RegionIndex] = true
			slog.Info("Selected residual polishing region",
				"sweep", sweep,
				"region", selection.Region,
				"active_circles", oneBasedCircleIndices(selection.Circles),
				"replacement_circles", oneBasedCircleIndices(selection.ReplacementCircles),
			)
		}

		bounds := fit.NewBounds(options.ActiveSetSize, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy())
		candidateFull := append([]float64(nil), bestParams...)
		// Circles before the first active slot are fixed and drawn first, so they
		// can be baked into a canvas once per sweep instead of being rasterized
		// again for every candidate. The suffix still carries the inactive circles
		// that are drawn after an active one, which preserves draw order exactly.
		prefixCircles := slices.Min(activeCircles)
		// Every pooled slot needs a session of its own, because a canvas shared by
		// the pool would serialize every candidate behind it and defeat the point.
		// The prefix canvas itself is identical for all of them, though, so it is
		// rasterized once per sweep and every slot session starts from a copy of
		// it: baking per slot would redraw the same fixed prefix once per worker,
		// which for contiguous-window is nearly the whole vector. The bake depends
		// on prefixCircles, which the selector changes every sweep, so the slots
		// are rebuilt per sweep rather than reused.
		suffixOffset := 0
		primarySession := fullSession
		newSlotSession := func() (Renderer, func(), error) {
			return newStagedSession(base, circleCount)
		}
		if baked, ok := bakePrefixCanvas(base, bestParams, prefixCircles, circleCount); ok {
			suffixOffset = prefixCircles * paramsPerCircle
			suffixCircles := circleCount - prefixCircles
			newSlotSession = func() (Renderer, func(), error) {
				session, cleanup, created := sessionOverBakedCanvas(base, baked, suffixCircles)
				if !created {
					return nil, nil, ErrStagedOptimizationUnsupported
				}
				return session, cleanup, nil
			}
			// A pool wider than one slot leases only sessions it created itself and
			// never evaluates the primary, so opening a baked primary alongside them
			// would pay for a session and a canvas nothing reads. Width one does
			// lease the primary, so that path still gets one.
			//
			// The wide path therefore leaves primarySession on the full vector,
			// which does not match suffixOffset. That is only reachable if the pool
			// fails to create a single slot, and the width check below refuses the
			// run before any evaluation.
			if optimizerWorkers < 2 {
				session, cleanup, err := newSlotSession()
				if err != nil {
					return nil, err
				}
				sweepCleanups = append(sweepCleanups, cleanup)
				primarySession = session
			}
		}
		// A single-slot pool leases the sweep's own session and its own
		// candidateFull, so width one stays exactly the historical serial path.
		pool := newEvaluationPool(primarySession, candidateFull, optimizerWorkers, newSlotSession)
		sweepCleanups = append(sweepCleanups, pool.close)
		// newEvaluationPool degrades to one slot when a session cannot be created,
		// which is only a throughput loss for the staged pipelines but a silent
		// race here: the optimizer would still call the evaluator from
		// optimizerWorkers goroutines. Keep that failure loud.
		if pool.width() < optimizerWorkers {
			return nil, fmt.Errorf(
				"%w: polishing leased %d evaluation sessions for an optimizer configured for %d concurrent evaluations",
				ErrInvalidOptimizationInput, pool.width(), optimizerWorkers)
		}
		// Evaluations run concurrently once the optimizer is configured for it, so
		// each one merges into its own leased vector and its own leased session.
		evaluate := func(activeParams []float64) float64 {
			slot := pool.acquire()
			defer pool.release(slot)
			bounds.ClampIndependentVector(activeParams)
			mergeActiveCircleParams(slot.combined, activeCircles, activeParams)
			return slot.session.Cost(slot.combined[suffixOffset:])
		}
		incumbentParams := extractActiveCircleParams(bestParams, activeCircles)
		initial := opt.Candidate{Params: incumbentParams, Cost: bestCost}
		var additionalSeeds []opt.Candidate
		switch options.Strategy {
		case BatchPolishWeakestReplacement, BatchPolishHybridOverlap, BatchPolishContiguousWindow:
			retainedSession, retainedCleanup, sessionErr := newStagedSession(base, circleCount-options.ActiveSetSize)
			if sessionErr != nil {
				return nil, sessionErr
			}
			residualCanvas := cloneNRGBA(retainedSession.Render(selection.RetainedParams))
			retainedCleanup()
			seedParams, seedErr := SeedParamsFromResidual(residualCanvas, base.Reference(), options.ActiveSetSize, ResidualSeedOptions{})
			if seedErr != nil {
				return nil, fmt.Errorf("seed polishing sweep %d: %w", sweep, seedErr)
			}
			seedCost := evaluate(seedParams)
			if options.Strategy == BatchPolishWeakestReplacement {
				initial = opt.Candidate{Params: seedParams, Cost: seedCost}
			} else {
				additionalSeeds = []opt.Candidate{{Params: seedParams, Cost: seedCost}}
			}
		case BatchPolishResidualRegion:
			seedCanvas := renderWithoutCircles(fullSession, bestParams, selection.ReplacementCircles)
			seedParams, seedErr := SeedParamsFromResidual(seedCanvas, base.Reference(), len(selection.ReplacementCircles), ResidualSeedOptions{
				Region: selection.Region,
			})
			if seedErr != nil {
				return nil, fmt.Errorf("seed residual region sweep %d: %w", sweep, seedErr)
			}
			alternative := append([]float64(nil), incumbentParams...)
			mergeReplacementSeedParams(alternative, activeCircles, selection.ReplacementCircles, seedParams)
			additionalSeeds = []opt.Candidate{{Params: alternative, Cost: evaluate(alternative)}}
		}

		iterationOffset := result.Iterations
		// The pool owns every sweep evaluation, including the seeds already
		// evaluated above, so its count is what the offsets and the running total
		// are made of. Counting inside the evaluator instead would race.
		evaluationOffset := evaluations + pool.count()
		observer := options.Observer
		runOptions := opt.RunOptions{
			Initial:         &initial,
			AdditionalSeeds: additionalSeeds,
			ResumeCount:     sweep,
			Continuation: &opt.ContinuationProfile{
				LocalFraction:  1,
				Sigma:          0.02,
				CoordinateRate: 0.2,
				MaxVelocity:    0.02,
			},
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
		evaluations += pool.count()
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
			Region:      selection.Region,
			ActiveSet:   oneBasedCircleIndices(selection.Circles),
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
		for _, circle := range activeCircles {
			visitCounts[circle]++
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

func oneBasedCircleIndices(circles []int) []int {
	indices := make([]int, len(circles))
	for i, circle := range circles {
		indices[i] = circle + 1
	}
	return indices
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
	visitedRegions map[int]bool,
	visitCounts map[int]int,
) (polishingActiveSet, error) {
	if strategy == BatchPolishWeakestReplacement {
		audit, err := AuditCircleBatch(base, params)
		if err != nil {
			return polishingActiveSet{}, err
		}
		weakest := append([]CircleAudit(nil), audit.Circles...)
		slices.SortFunc(weakest, func(left, right CircleAudit) int {
			leftVisits := visitCounts[left.OriginalCircle-1]
			rightVisits := visitCounts[right.OriginalCircle-1]
			if leftVisits != rightVisits {
				return leftVisits - rightVisits
			}
			if weakerAuditCircle(left, right) {
				return -1
			}
			if weakerAuditCircle(right, left) {
				return 1
			}
			return 0
		})
		if len(weakest) < activeSetSize {
			return polishingActiveSet{}, fmt.Errorf("%w: selected %d polishing circles, want %d", ErrInvalidOptimizationInput, len(weakest), activeSetSize)
		}
		active := make([]int, activeSetSize)
		for i, circle := range weakest[:activeSetSize] {
			active[i] = circle.OriginalCircle - 1
		}
		slices.Sort(active)
		return polishingActiveSet{Circles: active, RetainedParams: removeActiveCircleParams(params, active)}, nil
	}
	if strategy == BatchPolishResidualRegion {
		return selectResidualRegionActiveSet(base, params, activeSetSize, visitedRegions)
	}
	if strategy == BatchPolishContiguousWindow {
		circleCount := len(params) / paramsPerCircle
		active := selectContiguousWindowCircles(circleCount, activeSetSize, visitCounts)
		return polishingActiveSet{Circles: active, RetainedParams: removeActiveCircleParams(params, active)}, nil
	}

	audit, err := AuditCircleBatch(base, params)
	if err != nil {
		return polishingActiveSet{}, err
	}
	active := selectHybridOverlapCircles(params, audit, activeSetSize, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy(), visitCounts)
	return polishingActiveSet{Circles: active, RetainedParams: removeActiveCircleParams(params, active)}, nil
}

// selectContiguousWindowCircles returns activeSetSize consecutive draw slots,
// preferring the window whose circles have been polished least and, among
// equally unvisited windows, the one that starts latest.
//
// Preferring the latest start is what makes the strategy cheap. The caller
// bakes circles before the first active slot into a reusable canvas, so a
// window starting at s costs circleCount-s circle rasterizations per candidate;
// the last window costs exactly activeSetSize, the same as extending a batch.
// Visit counts then slide the window toward the front on later sweeps, which
// covers every circle in ceil(circleCount/activeSetSize) sweeps and makes the
// per-sweep cost rise predictably instead of being maximal from the start.
//
// That coverage has to be paid for in sweeps, and the sweep budget is bounded:
// app.MaxPolishingSweeps is 32, so a vector with more than 32*activeSetSize
// circles cannot be covered at all, and the shipped default of three sweeps
// only ever offers the last 3*activeSetSize slots to the optimizer. The
// strategy is therefore cheaper per sweep but not better per second; see
// docs/contiguous-window-polish-report.md for the measurement.
//
// The scan runs from the latest start downward and keeps a strictly better
// total, so ties resolve to the latest window without a separate tie-break.
func selectContiguousWindowCircles(circleCount, activeSetSize int, visitCounts map[int]int) []int {
	if activeSetSize >= circleCount {
		active := make([]int, circleCount)
		for i := range active {
			active[i] = i
		}
		return active
	}

	bestStart, bestVisits := 0, math.MaxInt
	for start := circleCount - activeSetSize; start >= 0; start-- {
		visits := 0
		for circle := start; circle < start+activeSetSize; circle++ {
			visits += visitCounts[circle]
		}
		if visits < bestVisits {
			bestStart, bestVisits = start, visits
		}
	}

	active := make([]int, activeSetSize)
	for i := range active {
		active[i] = bestStart + i
	}
	return active
}

func selectHybridOverlapCircles(params []float64, audit BatchAudit, activeSetSize, width, height int, visitCounts map[int]int) []int {
	weakest := make([]CircleAudit, len(audit.Circles))
	copy(weakest, audit.Circles)
	slices.SortFunc(weakest, func(left, right CircleAudit) int {
		leftVisits := visitCounts[left.OriginalCircle-1]
		rightVisits := visitCounts[right.OriginalCircle-1]
		if leftVisits != rightVisits {
			return leftVisits - rightVisits
		}
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
		bestCircle, bestOverlap, bestVisits := -1, -1, math.MaxInt
		for candidate := 0; candidate < vector.K; candidate++ {
			if selected[candidate] {
				continue
			}
			overlap := 0
			for _, anchor := range anchors {
				overlap += circleRasterOverlap(vector.DecodeCircle(candidate), vector.DecodeCircle(anchor), width, height)
			}
			visits := visitCounts[candidate]
			if visits < bestVisits || visits == bestVisits && (overlap > bestOverlap || overlap == bestOverlap && weakerAuditCircle(audit.Circles[candidate], audit.Circles[bestCircle])) {
				bestCircle, bestOverlap, bestVisits = candidate, overlap, visits
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

func selectResidualRegionActiveSet(
	base Renderer,
	params []float64,
	activeSetSize int,
	visitedRegions map[int]bool,
) (polishingActiveSet, error) {
	fullImage := cloneNRGBA(base.Render(params))
	region, regionIndex, err := highestResidualRegion(fullImage, base.Reference(), visitedRegions)
	if err != nil {
		return polishingActiveSet{}, err
	}
	audit, err := AuditCircleBatch(base, params)
	if err != nil {
		return polishingActiveSet{}, err
	}

	replacementCount := max(1, activeSetSize/5)
	weakest := append([]CircleAudit(nil), audit.Circles...)
	slices.SortFunc(weakest, func(left, right CircleAudit) int {
		if weakerAuditCircle(left, right) {
			return -1
		}
		if weakerAuditCircle(right, left) {
			return 1
		}
		return 0
	})
	replacements := make([]int, 0, replacementCount)
	selected := make(map[int]bool, activeSetSize)
	for _, circle := range weakest[:replacementCount] {
		index := circle.OriginalCircle - 1
		replacements = append(replacements, index)
		selected[index] = true
	}

	type regionInfluence struct {
		circle int
		energy uint64
	}
	influences := make([]regionInfluence, 0, len(audit.Circles)-len(replacements))
	for circle := range audit.Circles {
		if selected[circle] {
			continue
		}
		without := append([]float64(nil), params...)
		without[circle*paramsPerCircle+6] = 0
		withoutImage := base.Render(without)
		influences = append(influences, regionInfluence{
			circle: circle,
			energy: imageDifferenceEnergy(fullImage, withoutImage, region),
		})
	}
	slices.SortFunc(influences, func(left, right regionInfluence) int {
		if left.energy > right.energy {
			return -1
		}
		if left.energy < right.energy {
			return 1
		}
		if weakerAuditCircle(audit.Circles[left.circle], audit.Circles[right.circle]) {
			return -1
		}
		if weakerAuditCircle(audit.Circles[right.circle], audit.Circles[left.circle]) {
			return 1
		}
		return 0
	})
	for _, influence := range influences {
		if len(selected) == activeSetSize {
			break
		}
		selected[influence.circle] = true
	}

	active := make([]int, 0, len(selected))
	for circle := range selected {
		active = append(active, circle)
	}
	slices.Sort(active)
	slices.Sort(replacements)
	return polishingActiveSet{
		Circles:            active,
		RetainedParams:     removeActiveCircleParams(params, active),
		ReplacementCircles: replacements,
		Region:             region,
		RegionIndex:        regionIndex,
	}, nil
}

func highestResidualRegion(canvas, reference *image.NRGBA, visited map[int]bool) (image.Rectangle, int, error) {
	if canvas == nil || reference == nil || !canvas.Bounds().Eq(reference.Bounds()) || canvas.Bounds().Empty() {
		return image.Rectangle{}, -1, fmt.Errorf("canvas and reference must have matching non-empty bounds")
	}
	columns := min(residualPolishGridSize, canvas.Bounds().Dx())
	rows := min(residualPolishGridSize, canvas.Bounds().Dy())
	selectRegion := func(skipVisited bool) (image.Rectangle, int, bool) {
		bestIndex := -1
		var bestRegion image.Rectangle
		var bestEnergy uint64
		for row := range rows {
			for column := range columns {
				index := row*columns + column
				if skipVisited && visited[index] {
					continue
				}
				region := gridRegion(canvas.Bounds(), column, row, columns, rows)
				energy := imageDifferenceEnergy(canvas, reference, region)
				if bestIndex < 0 || energy > bestEnergy {
					bestIndex, bestRegion, bestEnergy = index, region, energy
				}
			}
		}
		return bestRegion, bestIndex, bestIndex >= 0
	}
	if region, index, ok := selectRegion(true); ok {
		return region, index, nil
	}
	region, index, _ := selectRegion(false)
	return region, index, nil
}

func gridRegion(bounds image.Rectangle, column, row, columns, rows int) image.Rectangle {
	x0 := bounds.Min.X + column*bounds.Dx()/columns
	x1 := bounds.Min.X + (column+1)*bounds.Dx()/columns
	y0 := bounds.Min.Y + row*bounds.Dy()/rows
	y1 := bounds.Min.Y + (row+1)*bounds.Dy()/rows
	return image.Rect(x0, y0, x1, y1)
}

func imageDifferenceEnergy(left, right *image.NRGBA, region image.Rectangle) uint64 {
	region = region.Intersect(left.Bounds()).Intersect(right.Bounds())
	var energy uint64
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			leftOffset := left.PixOffset(x, y)
			rightOffset := right.PixOffset(x, y)
			for channel := range 3 {
				delta := int(left.Pix[leftOffset+channel]) - int(right.Pix[rightOffset+channel])
				energy += uint64(delta * delta)
			}
		}
	}
	return energy
}

func renderWithoutCircles(base Renderer, params []float64, circles []int) *image.NRGBA {
	without := append([]float64(nil), params...)
	for _, circle := range circles {
		without[circle*paramsPerCircle+6] = 0
	}
	return cloneNRGBA(base.Render(without))
}

func mergeReplacementSeedParams(activeParams []float64, activeCircles, replacementCircles []int, seedParams []float64) {
	for replacement, circle := range replacementCircles {
		active := slices.Index(activeCircles, circle)
		if active < 0 {
			continue
		}
		copy(
			activeParams[active*paramsPerCircle:(active+1)*paramsPerCircle],
			seedParams[replacement*paramsPerCircle:(replacement+1)*paramsPerCircle],
		)
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
