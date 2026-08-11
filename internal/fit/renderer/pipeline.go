package renderer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

var (
	// ErrStagedOptimizationUnsupported indicates that a renderer cannot create
	// same-backend sessions while preserving its initial canvas.
	ErrStagedOptimizationUnsupported = errors.New("renderer does not support sequential or batch optimization")
	// ErrInvalidOptimizationInput indicates invalid pipeline dimensions or results.
	ErrInvalidOptimizationInput = errors.New("invalid optimization input")
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

	evaluations := 0
	evaluate := func(params []float64) float64 {
		evaluations++
		return session.Cost(params)
	}

	baseline := transparentParams(circleCount)
	initialCost := evaluate(baseline)
	bestParams := baseline
	bestCost := initialCost
	stages := 0
	iterations := 0

	if circleCount > 0 {
		candidate, stageIterations, err := runOptimizer(ctx, optimizer, evaluate, lower, upper, dim)
		if err != nil {
			return nil, err
		}
		iterations += stageIterations
		stages = 1
		if err := validateParamLength(candidate, dim); err != nil {
			return nil, fmt.Errorf("%w: optimizer result: %v", ErrInvalidOptimizationInput, err)
		}
		candidateCost := evaluate(candidate)
		if candidateCost < bestCost {
			bestParams = append([]float64(nil), candidate...)
			bestCost = candidateCost
		}
	}

	result, err := finishResult(session, bestParams, bestCost, initialCost, evaluations, stages, circleCount)
	if err != nil {
		return nil, err
	}
	result.Iterations = iterations

	slog.Info("Joint optimization complete", "initial_cost", initialCost, "best_cost", bestCost, "evaluations", evaluations)
	return result, nil
}

// OptimizeSequential optimizes circles one at a time while retaining the best
// historical solution. A worsening stage is represented by a transparent circle.
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

	for circleNum := 1; circleNum <= totalCircles; circleNum++ {
		sessionCircleCount := circleNum
		if accumulator != nil {
			sessionCircleCount = 1
		}
		session, cleanup, err := newStagedSessionForAccumulator(base, accumulator, sessionCircleCount)
		if err != nil {
			return nil, err
		}

		bounds := fit.NewBounds(1, base.Reference().Bounds().Dx(), base.Reference().Bounds().Dy())
		var combined []float64
		if accumulator == nil {
			combined = make([]float64, len(bestParams)+paramsPerCircle)
			copy(combined, bestParams)
		}
		evaluate := func(newCircle []float64) float64 {
			evaluations++
			if len(newCircle) != paramsPerCircle {
				return math.Inf(1)
			}
			params := newCircle
			if combined != nil {
				copy(combined[len(bestParams):], newCircle)
				params = combined
			}
			return session.Cost(params)
		}

		candidateCircle, stageIterations, err := runOptimizer(ctx, optimizer, evaluate, bounds.Lower, bounds.Upper, paramsPerCircle)
		if err != nil {
			cleanup()
			return nil, err
		}
		iterations += stageIterations
		stages++
		if err := validateParamLength(candidateCircle, paramsPerCircle); err != nil {
			cleanup()
			return nil, fmt.Errorf("%w: optimizer result for circle %d: %v", ErrInvalidOptimizationInput, circleNum, err)
		}

		candidateCost := evaluate(candidateCircle)
		if candidateCost <= bestCost {
			bestParams = append(bestParams, candidateCircle...)
			bestCost = candidateCost
			if accumulator != nil {
				accumulator.retain(session.Render(candidateCircle))
			}
		} else {
			bestParams = appendTransparentCircles(bestParams, 1)
		}

		var retainedImage *image.NRGBA
		if callback != nil {
			if accumulator != nil {
				retainedImage = cloneNRGBA(accumulator.canvas)
			} else {
				retainedImage = cloneNRGBA(session.Render(bestParams))
			}
		}
		cleanup()

		if callback != nil {
			callback(circleNum, append([]float64(nil), bestParams...), bestCost, retainedImage)
		}
		if tracker.Update(bestCost) {
			slog.Info("Sequential convergence detected", "circles_optimized", circleNum, "circles_requested", totalCircles)
			break
		}
	}

	result, err := finishStagedResult(base, bestParams, bestCost, initialCost, evaluations, stages)
	if err != nil {
		return nil, err
	}
	result.OptimizedCircles = len(bestParams) / paramsPerCircle
	result.Iterations = iterations
	return result, nil
}

// OptimizeBatch optimizes exactly totalCircles, adding at most batchSize circles
// per stage. The final stage uses the remaining circle count.
func OptimizeBatch(base Renderer, optimizer opt.Optimizer, totalCircles, batchSize int, convergenceConfig ConvergenceConfig) (*OptimizationResult, error) {
	return OptimizeBatchContext(context.Background(), base, optimizer, totalCircles, batchSize, convergenceConfig)
}

// OptimizeBatchContext is OptimizeBatch with cooperative cancellation when
// the optimizer implements opt.LifecycleOptimizer.
func OptimizeBatchContext(ctx context.Context, base Renderer, optimizer opt.Optimizer, totalCircles, batchSize int, convergenceConfig ConvergenceConfig) (*OptimizationResult, error) {
	if err := validatePipelineInputs(base, optimizer, totalCircles); err != nil {
		return nil, err
	}
	if batchSize <= 0 {
		return nil, fmt.Errorf("%w: batch size must be positive", ErrInvalidOptimizationInput)
	}
	if _, ok := base.(rendererSessionFactory); !ok {
		return nil, fmt.Errorf("%w: %T", ErrStagedOptimizationUnsupported, base)
	}

	slog.Info("Starting batch optimization", "total_circles", totalCircles, "batch_size", batchSize)

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
	optimizedCircles := 0

	for currentCircles := 0; currentCircles < totalCircles; {
		stageCircles := min(batchSize, totalCircles-currentCircles)
		newTotal := currentCircles + stageCircles
		sessionCircleCount := newTotal
		if accumulator != nil {
			sessionCircleCount = stageCircles
		}
		session, cleanup, err := newStagedSessionForAccumulator(base, accumulator, sessionCircleCount)
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
		evaluate := func(newBatch []float64) float64 {
			evaluations++
			if len(newBatch) != dim {
				return math.Inf(1)
			}
			params := newBatch
			if combined != nil {
				copy(combined[len(bestParams):], newBatch)
				params = combined
			}
			return session.Cost(params)
		}

		candidateBatch, stageIterations, err := runOptimizer(ctx, optimizer, evaluate, bounds.Lower, bounds.Upper, dim)
		if err != nil {
			cleanup()
			return nil, err
		}
		iterations += stageIterations
		stages++
		if err := validateParamLength(candidateBatch, dim); err != nil {
			cleanup()
			return nil, fmt.Errorf("%w: optimizer result for batch %d: %v", ErrInvalidOptimizationInput, stages, err)
		}

		candidateCost := evaluate(candidateBatch)
		if candidateCost <= bestCost {
			bestParams = append(bestParams, candidateBatch...)
			bestCost = candidateCost
			if accumulator != nil {
				accumulator.retain(session.Render(candidateBatch))
			}
		} else {
			bestParams = appendTransparentCircles(bestParams, stageCircles)
		}
		cleanup()

		currentCircles = newTotal
		optimizedCircles = newTotal
		if tracker.Update(bestCost) {
			// Keep the result cardinality exact while avoiding further optimizer work.
			bestParams = appendTransparentCircles(bestParams, totalCircles-currentCircles)
			break
		}
	}

	result, err := finishStagedResult(base, bestParams, bestCost, initialCost, evaluations, stages)
	if err != nil {
		return nil, err
	}
	result.OptimizedCircles = optimizedCircles
	result.Iterations = iterations
	return result, nil
}

func runOptimizer(ctx context.Context, optimizer opt.Optimizer, evaluate func([]float64) float64, lower, upper []float64, dim int) ([]float64, int, error) {
	if lifecycle, ok := optimizer.(opt.LifecycleOptimizer); ok {
		result, err := lifecycle.RunContext(ctx, opt.Problem{
			Eval:  evaluate,
			Lower: lower,
			Upper: upper,
			Dim:   dim,
		}, opt.RunOptions{})
		if err != nil {
			return nil, result.Iterations, err
		}
		return result.BestParams, result.Iterations, nil
	}

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	params, _ := optimizer.Run(evaluate, lower, upper, dim)
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	return params, 0, nil
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

func (a *stagedAccumulator) retain(canvas *image.NRGBA) {
	a.canvas = cloneNRGBA(canvas)
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

func finishStagedResult(base Renderer, params []float64, bestCost, initialCost float64, evaluations, stages int) (*OptimizationResult, error) {
	session, cleanup, err := newStagedSession(base, len(params)/paramsPerCircle)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return finishResult(session, params, bestCost, initialCost, evaluations, stages, len(params)/paramsPerCircle)
}

func finishResult(session Renderer, params []float64, bestCost, initialCost float64, evaluations, stages, optimizedCircles int) (*OptimizationResult, error) {
	if err := validateParamLength(params, session.Dim()); err != nil {
		return nil, fmt.Errorf("%w: final result: %v", ErrInvalidOptimizationInput, err)
	}
	return &OptimizationResult{
		BestParams:       append([]float64(nil), params...),
		BestCost:         bestCost,
		InitialCost:      initialCost,
		Evaluations:      evaluations,
		Stages:           stages,
		OptimizedCircles: optimizedCircles,
		BestImage:        cloneNRGBA(session.Render(params)),
	}, nil
}

func transparentParams(circleCount int) []float64 {
	params := make([]float64, circleCount*paramsPerCircle)
	for i := 0; i < circleCount; i++ {
		params[i*paramsPerCircle+2] = 1 // Valid radius; opacity remains zero.
	}
	return params
}

func appendTransparentCircles(params []float64, circleCount int) []float64 {
	if circleCount <= 0 {
		return params
	}
	additional := circleCount * paramsPerCircle
	start := len(params)
	if cap(params)-start < additional {
		params = append(params, make([]float64, additional)...)
	} else {
		params = params[:start+additional]
		clear(params[start:])
	}
	for offset := start; offset < len(params); offset += paramsPerCircle {
		params[offset+2] = 1
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
