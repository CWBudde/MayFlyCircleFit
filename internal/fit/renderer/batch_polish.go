package renderer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"slices"
	"sync"

	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/opt"
)

// BatchPolishOptions controls transactional active-set polishing after a
// complete batch solution has been found. Selected circles are optimized
// together while every other circle remains fixed in its original draw slot.
type BatchPolishOptions struct {
	ActiveSetSize int
	MaxSweeps     int
	Strategy      BatchPolishStrategy
	// InitialVisitCounts carries zero-based draw-slot selection counts from
	// compatible completed polishing calls. The slice is copied, never mutated.
	InitialVisitCounts []int
	Observer           opt.Observer
	OnEpoch            func(BatchPolishEpoch) error
	OnSweep            func(BatchPolishProgress) error
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
	// order. Full-coverage budgets start at the front of the vector, where a
	// greedy fit leaves the most value; partial budgets retain the cheaper
	// latest-first traversal.
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

	visitCounts, err := polishingVisitCounts(circleCount, options.InitialVisitCounts)
	if err != nil {
		return nil, err
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

	_, _, err = exactBounds(fullSession, len(initialParams))
	if err != nil {
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
	preferEarlierContiguousWindows := contiguousWindowBudgetCoversVector(circleCount, options.ActiveSetSize, options.MaxSweeps)
	incumbentAudit := &incumbentAuditCache{session: fullSession}
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

		err := ctx.Err()
		if err != nil {
			return nil, err
		}

		selection, err := selectPolishingActiveSet(
			fullSession,
			incumbentAudit,
			bestParams,
			options.ActiveSetSize,
			options.Strategy,
			visitedRegions,
			visitCounts,
			preferEarlierContiguousWindows,
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
		rawSlotSession := func() (Renderer, func(), error) {
			return newStagedSession(base, circleCount)
		}

		if baked, ok := bakePrefixCanvas(base, bestParams, prefixCircles, circleCount); ok {
			suffixOffset = prefixCircles * paramsPerCircle
			suffixCircles := circleCount - prefixCircles
			rawSlotSession = func() (Renderer, func(), error) {
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
				session, cleanup, err := rawSlotSession()
				if err != nil {
					return nil, err
				}

				sweepCleanups = append(sweepCleanups, cleanup)
				primarySession = session
			}
		}
		// The incumbent image and its exact SSD are constant for every candidate
		// in this sweep. CPU sessions use them to rebuild and rescore only the
		// old/new active-disc union; other backends retain their normal full-cost
		// path.
		var baselineImage *image.NRGBA
		var baselineSSD uint64

		if _, cpu := base.(*CPURenderer); cpu {
			baselineImage = cloneNRGBA(fullSession.Render(bestParams))
			if exact, ok := fit.ExactSSD(baselineImage, base.Reference()); ok {
				baselineSSD = exact
			} else {
				baselineImage = nil
			}
		}

		localActiveCircles := make([]int, len(activeCircles))
		for i, circle := range activeCircles {
			localActiveCircles[i] = circle - suffixOffset/paramsPerCircle
		}

		configureSession := func(session Renderer) Renderer {
			return newPolishDirtySession(
				session,
				baselineImage,
				baselineSSD,
				bestParams[suffixOffset:],
				localActiveCircles,
			)
		}
		primarySession = configureSession(primarySession)
		newSlotSession := func() (Renderer, func(), error) {
			session, cleanup, err := rawSlotSession()
			if err != nil {
				return nil, cleanup, err
			}

			return configureSession(session), cleanup, nil
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

		// admitSweep audits the candidate and compares it against the incumbent's
		// cached audit. It is consulted only after the candidate has already beaten
		// the incumbent on cost, so the audits it pays for are never wasted on a
		// sweep the cost check would have rejected anyway.
		// It also hands the candidate's audit back, because a committed candidate
		// becomes the incumbent and that audit already describes it exactly.
		admitSweep := func(candidate []float64) (bool, []int, BatchAudit, error) {
			candidateAudit, auditErr := AuditCircleBatch(fullSession, candidate)
			if auditErr != nil {
				return false, nil, BatchAudit{}, fmt.Errorf("audit polishing sweep %d: %w", sweep, auditErr)
			}

			incumbent, auditErr := incumbentAudit.get(bestParams)
			if auditErr != nil {
				return false, nil, BatchAudit{}, fmt.Errorf("audit polishing incumbent before sweep %d: %w", sweep, auditErr)
			}

			commit, blockers := sweepKeepsCirclesUseful(
				incumbent, candidateAudit, activeCircles, minBatchMSEContribution)

			return commit, blockers, candidateAudit, nil
		}

		// Every path out of the acceptance decision says why at Info, so a stalled
		// run is diagnosable from the server log alone: the sweep either lost on
		// cost, named the circles the usefulness gate refused, or committed.
		accepted := false

		if len(outcome.Params) != options.ActiveSetSize*paramsPerCircle || !bounds.ValidVector(outcome.Params) {
			slog.Info("Rejected polishing sweep",
				"sweep", sweep,
				"reason", "invalid-candidate",
				"active_circles", oneBasedCircleIndices(activeCircles),
			)
		} else {
			candidate := append([]float64(nil), bestParams...)
			mergeActiveCircleParams(candidate, activeCircles, outcome.Params)
			candidateCost := fullSession.Cost(candidate)
			evaluations++
			commit, blockers := false, []int(nil)
			var candidateAudit BatchAudit

			if candidateCost < bestCost {
				var gateErr error

				commit, blockers, candidateAudit, gateErr = admitSweep(candidate)
				if gateErr != nil {
					return nil, gateErr
				}
			}

			switch {
			case candidateCost >= bestCost:
				slog.Info("Rejected polishing sweep",
					"sweep", sweep,
					"reason", "cost",
					"candidate_cost", candidateCost,
					"best_cost", bestCost,
					"active_circles", oneBasedCircleIndices(activeCircles),
				)
			case !commit:
				slog.Info("Rejected polishing sweep",
					"sweep", sweep,
					"reason", "usefulness-gate",
					"candidate_cost", candidateCost,
					"best_cost", bestCost,
					"active_circles", oneBasedCircleIndices(activeCircles),
					"blocking_circles", blockers,
				)
			default:
				slog.Info("Accepted polishing sweep",
					"sweep", sweep,
					"best_cost", candidateCost,
					"previous_cost", bestCost,
					"cost_removed", bestCost-candidateCost,
					"active_circles", oneBasedCircleIndices(activeCircles),
				)

				bestParams = candidate
				bestCost = candidateCost
				// The incumbent changed, but the gate has just audited exactly this
				// vector, so hand that audit over instead of discarding it and paying
				// for another full render per circle on the next sweep.
				incumbentAudit.adopt(candidateAudit)

				result.AcceptedSweeps++
				accepted = true
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
			err := options.OnSweep(progress)
			if err != nil {
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

// incumbentAuditCache holds the leave-one-out audit of the current incumbent.
//
// AuditCircleBatch is one full render per omitted circle and is the dominant
// cost of a sweep, while both active-set selection and the acceptance gate need
// the incumbent's audit. A rejected sweep leaves the incumbent untouched, so the
// audit is computed once per incumbent rather than once per consumer or once per
// sweep. A committing sweep replaces the incumbent, but the acceptance gate has
// already audited that exact candidate, so the cache adopts that audit and no
// incumbent is ever audited twice.
//
// The cached BatchAudit is shared by value; nothing may mutate audit.Circles in
// place.
type incumbentAuditCache struct {
	session Renderer
	audit   BatchAudit
	valid   bool
}

func (c *incumbentAuditCache) get(params []float64) (BatchAudit, error) {
	if c.valid {
		return c.audit, nil
	}

	audit, err := AuditCircleBatch(c.session, params)
	if err != nil {
		return BatchAudit{}, err
	}

	c.audit, c.valid = audit, true

	return audit, nil
}

// adopt installs an audit the caller has already computed for the vector that
// is about to become the incumbent. A committed sweep is audited by the
// acceptance gate immediately before it commits, so the cache can carry that
// result forward rather than invalidate and re-render.
func (c *incumbentAuditCache) adopt(audit BatchAudit) {
	c.audit, c.valid = audit, true
}

func selectPolishingActiveSet(
	base Renderer,
	incumbentAudit *incumbentAuditCache,
	params []float64,
	activeSetSize int,
	strategy BatchPolishStrategy,
	visitedRegions map[int]bool,
	visitCounts map[int]int,
	preferEarlierContiguousWindows bool,
) (polishingActiveSet, error) {
	if strategy == BatchPolishWeakestReplacement {
		audit, err := incumbentAudit.get(params)
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
		return selectResidualRegionActiveSet(base, incumbentAudit, params, activeSetSize, visitedRegions)
	}

	if strategy == BatchPolishContiguousWindow {
		circleCount := len(params) / paramsPerCircle
		active := selectContiguousWindowCircles(circleCount, activeSetSize, visitCounts, preferEarlierContiguousWindows)

		return polishingActiveSet{Circles: active, RetainedParams: removeActiveCircleParams(params, active)}, nil
	}

	audit, err := incumbentAudit.get(params)
	if err != nil {
		return polishingActiveSet{}, err
	}

	active := selectHybridOverlapCircles(params, audit, activeSetSize, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy(), visitCounts)

	return polishingActiveSet{Circles: active, RetainedParams: removeActiveCircleParams(params, active)}, nil
}

// PlanContiguousWindows returns the deterministic active sets and resulting
// visit counts for a contiguous-window polishing call. It is shared with the
// server's continuation reconstruction so persisted lineage cannot drift from
// the renderer's selector. Active sets contain zero-based draw slots.
func PlanContiguousWindows(circleCount, activeSetSize, maxSweeps int, initialVisitCounts []int) ([][]int, []int, error) {
	if circleCount < 1 {
		return nil, nil, fmt.Errorf("%w: circle count must be positive", ErrInvalidOptimizationInput)
	}

	if activeSetSize < 1 || activeSetSize > circleCount {
		return nil, nil, fmt.Errorf("%w: active set size must be in [1, %d]", ErrInvalidOptimizationInput, circleCount)
	}

	if maxSweeps < 0 {
		return nil, nil, fmt.Errorf("%w: maximum polishing sweeps cannot be negative", ErrInvalidOptimizationInput)
	}

	visits, err := polishingVisitCounts(circleCount, initialVisitCounts)
	if err != nil {
		return nil, nil, err
	}

	preferEarlier := contiguousWindowBudgetCoversVector(circleCount, activeSetSize, maxSweeps)

	activeSets := make([][]int, 0, maxSweeps)
	for range maxSweeps {
		active := selectContiguousWindowCircles(circleCount, activeSetSize, visits, preferEarlier)

		activeSets = append(activeSets, active)
		for _, circle := range active {
			visits[circle]++
		}
	}

	resultingVisits := make([]int, circleCount)
	for circle, count := range visits {
		resultingVisits[circle] = count
	}

	return activeSets, resultingVisits, nil
}

func polishingVisitCounts(circleCount int, initial []int) (map[int]int, error) {
	visits := make(map[int]int, circleCount)
	if initial == nil {
		return visits, nil
	}

	if len(initial) != circleCount {
		return nil, fmt.Errorf("%w: initial polishing visit counts contain %d circles, want %d", ErrInvalidOptimizationInput, len(initial), circleCount)
	}

	for circle, count := range initial {
		if count < 0 {
			return nil, fmt.Errorf("%w: initial polishing visit count for circle %d cannot be negative", ErrInvalidOptimizationInput, circle+1)
		}

		if count > 0 {
			visits[circle] = count
		}
	}

	return visits, nil
}

func contiguousWindowBudgetCoversVector(circleCount, activeSetSize, maxSweeps int) bool {
	return maxSweeps >= (circleCount+activeSetSize-1)/activeSetSize
}

// selectContiguousWindowCircles returns activeSetSize consecutive draw slots,
// preferring the window whose circles have been polished least. Ties go to the
// earliest start when the caller's budget covers the vector and to the latest
// start for a partial budget.
//
// Preferring the latest start keeps a partial pass cheap. The caller
// bakes circles before the first active slot into a reusable canvas, so a
// window starting at s costs circleCount-s circle rasterizations per candidate;
// the last window costs exactly activeSetSize, the same as extending a batch.
// When the configured budget covers every draw slot, early fitted circles are
// more valuable and earliest-first is also no more expensive across the whole
// cycle. Visit counts keep either traversal from repeating a window while a
// less-visited one remains and carry that coverage across continuations.
//
// That coverage has to be paid for in sweeps, and the sweep budget is bounded:
// app.MaxPolishingSweeps is 32, so a vector with more than 32*activeSetSize
// circles cannot be covered at all, and a fresh pass at the shipped default of
// eight sweeps only offers the last 8*activeSetSize slots to the optimizer. The
// strategy is therefore cheaper per sweep but not better per second; see
// docs/contiguous-window-polish-report.md for the measurement.
func selectContiguousWindowCircles(circleCount, activeSetSize int, visitCounts map[int]int, preferEarlier bool) []int {
	if activeSetSize >= circleCount {
		active := make([]int, circleCount)
		for i := range active {
			active[i] = i
		}

		return active
	}

	bestStart, bestVisits := 0, math.MaxInt

	first, last, step := circleCount-activeSetSize, 0, -1
	if preferEarlier {
		first, last, step = 0, circleCount-activeSetSize, 1
	}

	for start := first; ; start += step {
		visits := 0
		for circle := start; circle < start+activeSetSize; circle++ {
			visits += visitCounts[circle]
		}

		if visits < bestVisits {
			bestStart, bestVisits = start, visits
		}

		if start == last {
			break
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

		for candidate := range vector.K {
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

	for y := range height {
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

	remaining := float64(circle.R*circle.R) - float64(dy*dy)
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
	for circle := range len(params) / paramsPerCircle {
		if !active[circle] {
			retained = append(retained, params[circle*paramsPerCircle:(circle+1)*paramsPerCircle]...)
		}
	}

	return retained
}

func selectResidualRegionActiveSet(
	base Renderer,
	incumbentAudit *incumbentAuditCache,
	params []float64,
	activeSetSize int,
	visitedRegions map[int]bool,
) (polishingActiveSet, error) {
	fullImage := cloneNRGBA(base.Render(params))

	region, regionIndex, err := highestResidualRegion(fullImage, base.Reference(), visitedRegions)
	if err != nil {
		return polishingActiveSet{}, err
	}

	audit, err := incumbentAudit.get(params)
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

	candidates := make([]int, 0, len(audit.Circles)-len(replacements))
	for circle := range audit.Circles {
		if !selected[circle] {
			candidates = append(candidates, circle)
		}
	}
	type regionInfluence struct {
		circle int
		energy uint64
	}
	energies := regionInfluenceEnergies(base, params, fullImage, region, candidates)

	influences := make([]regionInfluence, len(candidates))
	for index, circle := range candidates {
		influences[index] = regionInfluence{circle: circle, energy: energies[index]}
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

// rowBandCompositor can composite a run of circles onto a caller-owned canvas
// one row band at a time. It is what lets a caller that reads only part of the
// canvas render only that part.
type rowBandCompositor interface {
	compositeParamsRows(img *image.NRGBA, params []float64, count, minY, maxY int)
	initialCanvas() *image.NRGBA
}

// regionInfluenceEnergies measures, for every candidate circle, how much the
// image inside region changes when that circle alone is removed. It is the
// ranking residual-region selection uses to keep the circles that actually
// paint the worst tile.
//
// Two facts shrink this far below the one full render per circle the definition
// suggests. Compositing only ever writes pixels inside a circle's own raster,
// so removing a circle cannot change a pixel outside it: the comparison
// rectangle is region intersected with the circle's bounding box, and a circle
// whose box misses the region has energy zero and needs no render at all. And
// because nothing outside that rectangle is ever read, only its rows have to be
// composited -- on the 4x4 grid the region is a sixteenth of the canvas, and a
// circle usually covers a small part of even that.
//
// Backends that cannot composite a row band in place keep rendering the whole
// canvas per circle. Both paths return the same energies, because the pixels
// the fast path skips contribute exactly zero to the slow path's sum.
func regionInfluenceEnergies(
	base Renderer,
	params []float64,
	fullImage *image.NRGBA,
	region image.Rectangle,
	candidates []int,
) []uint64 {
	energies := make([]uint64, len(candidates))
	circleCount := len(params) / paramsPerCircle
	compositor, banded := base.(rowBandCompositor)

	var initial *image.NRGBA
	if banded {
		initial = compositor.initialCanvas()
	}

	if initial == nil {
		for index, circle := range candidates {
			without := append([]float64(nil), params...)
			without[circle*paramsPerCircle+6] = 0
			energies[index] = imageDifferenceEnergy(fullImage, base.Render(without), region)
		}

		return energies
	}

	bounds := fullImage.Bounds()
	region = region.Intersect(bounds)
	vector := fit.ParamVector{Data: params, K: circleCount, Width: bounds.Dx(), Height: bounds.Dy()}
	measure := func(banded rowBandCompositor, scratch *image.NRGBA, without []float64, index int) {
		circle := candidates[index]

		compare := region.Intersect(circleRasterBounds(vector.DecodeCircle(circle)))
		if compare.Empty() {
			return
		}

		copy(without, params)
		without[circle*paramsPerCircle+6] = 0

		copyRowBand(scratch, initial, compare.Min.Y, compare.Max.Y)
		banded.compositeParamsRows(scratch, without, circleCount, compare.Min.Y, compare.Max.Y)
		energies[index] = imageDifferenceEnergy(fullImage, scratch, compare)
	}

	// Each worker owns its scratch canvas, its scratch vector, and its own
	// session, so the circles are independent measurements from here on.
	compositors := []rowBandCompositor{compositor}

	sessions, release := concurrentSessions(base, circleCount, min(renderWorkers(base), len(candidates)))
	defer release()

	if pooled := rowBandCompositors(sessions); len(pooled) > 1 {
		compositors = pooled
	}

	walk := func(worker int) {
		scratch := image.NewNRGBA(bounds)

		without := make([]float64, len(params))
		for index := worker; index < len(candidates); index += len(compositors) {
			measure(compositors[worker], scratch, without, index)
		}
	}
	var workers sync.WaitGroup
	workers.Add(len(compositors) - 1)

	for worker := 1; worker < len(compositors); worker++ {
		go func() {
			defer workers.Done()

			walk(worker)
		}()
	}

	walk(0)
	workers.Wait()

	return energies
}

// rowBandCompositors returns the sessions as row-band compositors, or nil if
// any of them is not one. All or nothing: a partial set would leave some
// circles measured on a session and the rest queued behind the caller's own
// renderer, which is exactly the serialization the sessions exist to avoid.
func rowBandCompositors(sessions []Renderer) []rowBandCompositor {
	compositors := make([]rowBandCompositor, 0, len(sessions))
	for _, session := range sessions {
		compositor, ok := session.(rowBandCompositor)
		if !ok {
			return nil
		}

		compositors = append(compositors, compositor)
	}

	return compositors
}

// circleRasterBounds is the pixel box a circle can write. It is widened by one
// pixel on every side so that no rounding in the rasterizer's span arithmetic
// can put a written pixel outside it: the box is used to skip work, so it has
// to err toward being too large.
func circleRasterBounds(circle fit.Circle) image.Rectangle {
	if circle.Opacity == 0 {
		return image.Rectangle{}
	}

	return image.Rect(
		int(math.Floor(circle.X-circle.R))-1,
		int(math.Floor(circle.Y-circle.R))-1,
		int(math.Ceil(circle.X+circle.R))+2,
		int(math.Ceil(circle.Y+circle.R))+2,
	)
}

// copyRowBand copies rows [minY, maxY) from src to dst, which must share their
// geometry. It restores a scratch canvas to the base background over just the
// rows a measurement is about to composite and read.
func copyRowBand(dst, src *image.NRGBA, minY, maxY int) {
	if dst.Stride != src.Stride || !dst.Bounds().Eq(src.Bounds()) {
		return
	}

	minY = max(minY, dst.Bounds().Min.Y)

	maxY = min(maxY, dst.Bounds().Max.Y)
	for y := minY; y < maxY; y++ {
		row := dst.PixOffset(dst.Bounds().Min.X, y)
		copy(dst.Pix[row:row+dst.Stride], src.Pix[row:row+src.Stride])
	}
}

func highestResidualRegion(canvas, reference *image.NRGBA, visited map[int]bool) (image.Rectangle, int, error) {
	if canvas == nil || reference == nil || !canvas.Bounds().Eq(reference.Bounds()) || canvas.Bounds().Empty() {
		return image.Rectangle{}, -1, errors.New("canvas and reference must have matching non-empty bounds")
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

// circleUseful reports whether a circle earns its place in the vector: it is
// within bounds, it paints at least one pixel of the final image, and removing
// it costs more than minContribution in MSE.
func circleUseful(circle CircleAudit, minContribution float64) bool {
	return circle.Valid && circle.FinalChangedPixels >= 1 && circle.MSEContribution > minContribution
}

// sweepKeepsCirclesUseful decides whether a cost-improving sweep may commit,
// and names the one-based circles that block it when it may not.
//
// The invariant polishing protects is that it never makes a circle dead: no
// sweep may kill a circle that was alive in the incumbent. It does not promise
// that the committed vector is free of dead circles, because a dead circle
// inherited from an earlier stage and left outside the active set stays exactly
// as the sweep found it.
//
// The gate used to spell the invariant as allCirclesUseful over the entire
// candidate, which also demanded that a sweep repair dead circles it never
// touched: circles outside the active set are copied through a sweep byte for
// byte, so no amount of optimizer budget can clear them. Incrementally grown
// vectors carry such circles as their steady state, because PruneCircleBatch
// runs per stage against that stage's canvas while later stages composite on
// top and nothing re-audits the assembled result. On a real 64-circle fit three
// occluded circles held contributions of -0.41, -0.18, and -0.07 against a
// threshold of 0.01, and residual-region reseeds max(1, activeSetSize/5) = 1
// circle per sweep, so the gate was structurally impossible to satisfy and
// vetoed every sweep forever.
//
// The rule is therefore non-regression rather than absolute:
//
//   - every circle in the active set, where the sweep has agency, must be
//     useful afterwards, exactly as strictly as before;
//   - outside the active set the set of non-useful circles may not grow. A
//     circle that was useful in the incumbent and is not in the candidate blocks
//     the sweep; one that was already not useful does not.
//
// Containment is checked per circle, so the count cannot grow either. The rule
// deliberately does not require the contribution of an inherited dead circle to
// improve: those values drift by fractions as neighbouring circles move, and
// demanding monotone improvement on circles the sweep does not control would
// restore the stall this removes.
//
// When the incumbent has no dead circles it has nothing to excuse, so the rule
// reduces to the old absolute predicate exactly.
func sweepKeepsCirclesUseful(incumbent, candidate BatchAudit, activeCircles []int, minContribution float64) (bool, []int) {
	active := make(map[int]bool, len(activeCircles))
	for _, circle := range activeCircles {
		active[circle] = true
	}
	var blockers []int

	for index, circle := range candidate.Circles {
		if circleUseful(circle, minContribution) {
			continue
		}
		// An incumbent audit that does not cover this circle describes a different
		// vector and cannot excuse anything, so the circle blocks.
		if !active[index] && index < len(incumbent.Circles) &&
			!circleUseful(incumbent.Circles[index], minContribution) {
			continue
		}

		blockers = append(blockers, index+1)
	}

	return len(blockers) == 0, blockers
}
