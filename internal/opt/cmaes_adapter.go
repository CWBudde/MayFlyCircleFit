package opt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"slices"

	cmaes "github.com/CWBudde/go-cma-es"
)

// CMAESAdapter runs covariance matrix adaptation behind this package's
// optimizer contracts. The adapter works in a normalized unit box so one
// initial step size has the same meaning for coordinates with different
// physical ranges.
type CMAESAdapter struct {
	logger *slog.Logger
	stop   Stop

	seed int64

	maxIters        int
	popSize         int
	parallelWorkers int
}

// CMAESOption customizes a CMA-ES adapter at construction time.
type CMAESOption func(*CMAESAdapter)

// WithCMAESLogger reports CMA-ES lifecycle events through logger. A nil logger
// disables optimizer logging.
func WithCMAESLogger(logger *slog.Logger) CMAESOption {
	return func(adapter *CMAESAdapter) { adapter.logger = logger }
}

// WithCMAESEarlyStop enables target-cost and stagnation stopping in addition
// to CMA-ES's distribution-aware convergence criteria.
func WithCMAESEarlyStop(stop Stop) CMAESOption {
	return func(adapter *CMAESAdapter) { adapter.stop = stop }
}

// WithCMAESParallelEvaluation makes CMA-ES evaluate population members
// concurrently with at most workers goroutines. The objective must be safe for
// concurrent calls. A seeded parallel run is bit-identical to its serial run.
func WithCMAESParallelEvaluation(workers int) CMAESOption {
	return func(adapter *CMAESAdapter) { adapter.parallelWorkers = workers }
}

// NewCMAES creates a CMA-ES optimizer adapter.
func NewCMAES(maxIters, popSize int, seed int64, options ...CMAESOption) Optimizer {
	adapter := &CMAESAdapter{maxIters: maxIters, popSize: popSize, seed: seed}

	for _, option := range options {
		if option != nil {
			option(adapter)
		}
	}

	return adapter
}

// ParallelEvaluationWorkers reports the concurrent evaluation width this
// optimizer drives. Values below two select the serial path.
func (adapter *CMAESAdapter) ParallelEvaluationWorkers() int {
	if adapter.parallelWorkers < 1 {
		return 1
	}

	return adapter.parallelWorkers
}

// IterationBudget reports the maximum generations one adapter invocation may
// consume. CMA-ES can stop below this cap on a convergence criterion.
func (adapter *CMAESAdapter) IterationBudget() int {
	return adapter.maxIters
}

// Run executes CMA-ES with a background context.
func (adapter *CMAESAdapter) Run(
	eval func([]float64) float64,
	lower, upper []float64,
	dim int,
) ([]float64, float64) {
	result, err := adapter.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{})
	if err != nil {
		return nil, math.Inf(1)
	}

	return result.BestParams, result.BestCost
}

// RunWithInitial executes CMA-ES with the saved candidate as its initial mean
// and as an exact member of the first population.
func (adapter *CMAESAdapter) RunWithInitial(
	initialParams []float64,
	initialCost float64,
	eval func([]float64) float64,
	lower, upper []float64,
	dim int,
) ([]float64, float64) {
	result, err := adapter.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{
		Initial:     &Candidate{Params: initialParams, Cost: initialCost},
		ResumeCount: 1,
	})
	if err != nil {
		return append([]float64(nil), initialParams...), initialCost
	}

	return result.BestParams, result.BestCost
}

// RunContext executes CMA-ES with cancellation, progress, measured work,
// nonlinear constraints, and optional continuation seeds.
func (adapter *CMAESAdapter) RunContext(
	ctx context.Context,
	problem Problem,
	options RunOptions,
) (Result, error) {
	err := validateCMAESRun(problem, options)
	if err != nil {
		return Result{}, err
	}

	canonicalize := canonicalizer(problem)
	config := adapter.buildCMAESConfig(problem, options, canonicalize)
	// A reproducible optimizer needs a deterministic rather than cryptographic
	// random source, and the library never reads it from worker goroutines.
	rng := rand.New(rand.NewSource(adapter.runSeed(options))) //nolint:gosec
	config.Rand = rng

	best := Result{BestCost: math.Inf(1), Termination: TerminationCompleted}
	bestViolation := math.Inf(1)

	seedState, err := cmaesSeeds(problem, options, config.Constraints, &best, &bestViolation)
	if err != nil {
		return Result{}, err
	}

	runOptions := adapter.cmaesRunOptions(
		config, options, seedState, canonicalize, rng, &best, &bestViolation,
	)

	result, err := cmaes.OptimizeContext(ctx, config, runOptions...)
	if err != nil {
		return cmaesErrorResult(best, err)
	}

	updateCMAESBest(
		&best,
		&bestViolation,
		canonicalize(result.GlobalBest.Position),
		result.GlobalBest.Cost,
		result.GlobalBest.ConstraintViolation,
		config.Constraints,
	)
	best.Iterations = result.IterationCount
	best.Evaluations = result.FuncEvalCount
	best.Termination = terminationFromCMAES(result.TerminationReason)

	if result.TerminationReason == cmaes.TerminationCancelled {
		ctxErr := ctx.Err()
		if ctxErr != nil {
			return best, fmt.Errorf("cma-es optimization: %w", ctxErr)
		}
	}

	epochErr := notifyCMAESEpoch(options, best)
	if epochErr != nil {
		return best, epochErr
	}

	return best, nil
}

func validateCMAESRun(problem Problem, options RunOptions) error {
	err := validateProblem(problem)
	if err != nil {
		return err
	}

	if options.ResumeCount < 0 {
		return errNegativeResumeCount
	}

	return validateContinuationProfile(options.Continuation)
}

func (adapter *CMAESAdapter) runSeed(options RunOptions) int64 {
	if options.ResumeCount > 0 || options.SeedOffset > 0 {
		return continuationSeed(adapter.seed, options.ResumeCount, options.SeedOffset)
	}

	return adapter.seed
}

func (adapter *CMAESAdapter) buildCMAESConfig(
	problem Problem,
	options RunOptions,
	canonicalize func([]float64) []float64,
) *cmaes.Config {
	config := cmaes.NewDefaultConfig(problem.Dim)
	config.ObjectiveFunc = func(normalized []float64) float64 {
		return problem.Eval(canonicalize(normalized))
	}

	config.InitialMean = make([]float64, problem.Dim)
	for index := range config.InitialMean {
		config.InitialMean[index] = 0.5
	}

	config.LowerBound = 0
	config.UpperBound = 1
	config.MaxIterations = adapter.maxIters
	config.Lambda = adapter.popSize
	config.Mu = adapter.popSize / 2

	if adapter.parallelWorkers > 1 {
		config.EnableParallel = true
		config.MaxWorkers = adapter.parallelWorkers
	}

	if len(problem.Inequalities) > 0 {
		config.Constraints = &cmaes.ConstraintConfig{
			Handling: cmaes.ConstraintHandlingFeasibility,
			Inequalities: []cmaes.ConstraintFunction{func(normalized []float64) float64 {
				return inequalityViolation(problem.Inequalities, canonicalize(normalized))
			}},
		}
	}

	if adapter.stop.enabled() {
		config.Convergence.MinImprovement = adapter.stop.MinImprovement
		config.Convergence.StagnationIterations = adapter.stop.StagnationIters

		config.Convergence.MinIterations = min(adapter.stop.MinIters, adapter.maxIters)
		if adapter.stop.TargetCost > 0 {
			target := adapter.stop.TargetCost
			config.Convergence.TargetCost = &target
		}
	}

	if options.Continuation != nil {
		config.InitialSigma = options.Continuation.Sigma
	}

	return config
}

type cmaesSeedState struct {
	initialMean []float64
	positions   [][]float64
}

func cmaesSeeds(
	problem Problem,
	options RunOptions,
	constraints *cmaes.ConstraintConfig,
	best *Result,
	bestViolation *float64,
) (cmaesSeedState, error) {
	candidates := make([]Candidate, 0, len(options.AdditionalSeeds)+1)
	if options.Initial != nil {
		candidates = append(candidates, *options.Initial)
	}

	candidates = append(candidates, options.AdditionalSeeds...)
	normalized := make([][]float64, 0, len(candidates))

	for index, candidate := range candidates {
		candidate.Params = append([]float64(nil), candidate.Params...)
		original := append([]float64(nil), candidate.Params...)
		repairCandidate(problem, candidate.Params)

		if !slices.Equal(original, candidate.Params) {
			candidate.Cost = problem.Eval(candidate.Params)
		}

		candidateErr := validateCandidate(candidate, problem)
		if candidateErr != nil {
			return cmaesSeedState{}, fmt.Errorf("initial candidate %d: %w", index, candidateErr)
		}

		violation := inequalityViolation(problem.Inequalities, candidate.Params)
		updateCMAESBest(
			best, bestViolation, candidate.Params, candidate.Cost, violation, constraints,
		)
		normalized = append(normalized, normalizeCandidate(problem, candidate.Params))
	}

	state := cmaesSeedState{}
	if options.Initial != nil && len(normalized) > 0 {
		state.initialMean = append([]float64(nil), normalized[0]...)
	}

	state.positions = normalized

	return state, nil
}

func normalizeCandidate(problem Problem, params []float64) []float64 {
	normalized := make([]float64, problem.Dim)
	for index := range normalized {
		normalized[index] = (params[index] - problem.Lower[index]) /
			(problem.Upper[index] - problem.Lower[index])
	}

	return normalized
}

func (adapter *CMAESAdapter) cmaesRunOptions(
	config *cmaes.Config,
	options RunOptions,
	seeds cmaesSeedState,
	canonicalize func([]float64) []float64,
	rng *rand.Rand,
	best *Result,
	bestViolation *float64,
) []cmaes.RunOption {
	runOptions := make([]cmaes.RunOption, 0, 4)

	if len(seeds.positions) > 0 {
		positions, _ := seededPopulationFromCandidates(
			seeds.positions, config.Lambda, rng, options.Continuation,
		)
		runOptions = append(runOptions, cmaes.WithInitialPopulation(positions))
	}

	if len(seeds.initialMean) > 0 {
		runOptions = append(runOptions,
			cmaes.WithInitialMean(seeds.initialMean, config.InitialSigma))
	}

	if adapter.logger != nil {
		runOptions = append(runOptions, cmaes.WithLogger(adapter.logger))
	}

	runOptions = append(runOptions, cmaes.WithProgressObserver(func(progress cmaes.Progress) {
		updateCMAESBest(
			best,
			bestViolation,
			canonicalize(progress.Best.Position),
			progress.Best.Cost,
			progress.Best.ConstraintViolation,
			config.Constraints,
		)
		best.Iterations = progress.Iteration
		best.Evaluations = progress.EvaluationCount

		if options.Observer == nil {
			return
		}

		reported := Progress{
			Iterations:  progress.Iteration,
			Evaluations: progress.EvaluationCount,
			BestParams:  append([]float64(nil), best.BestParams...),
			BestCost:    best.BestCost,
		}
		if options.ProgressMapper != nil {
			reported = options.ProgressMapper(reported)
		}

		options.Observer(reported)
	}))

	return runOptions
}

func updateCMAESBest(
	best *Result,
	bestViolation *float64,
	params []float64,
	cost, violation float64,
	constraints *cmaes.ConstraintConfig,
) {
	candidate := cmaes.CandidateEvaluation{Cost: cost, ConstraintViolation: violation}

	incumbent := cmaes.CandidateEvaluation{
		Cost: best.BestCost, ConstraintViolation: *bestViolation,
	}
	if !cmaes.BetterConstrainedCandidate(candidate, incumbent, constraints) {
		return
	}

	best.BestParams = append(best.BestParams[:0], params...)
	best.BestCost = cost
	*bestViolation = violation
}

func cmaesErrorResult(best Result, err error) (Result, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		best.Termination = TerminationCancelled

		return best, fmt.Errorf("cma-es optimization: %w", err)
	}

	return Result{}, fmt.Errorf("cma-es optimization: %w", err)
}

func terminationFromCMAES(reason cmaes.TerminationReason) Termination {
	switch reason {
	case cmaes.TerminationTargetCost:
		return TerminationTargetCost
	case cmaes.TerminationStagnation:
		return TerminationStagnation
	case cmaes.TerminationCancelled:
		return TerminationCancelled
	default:
		return TerminationCompleted
	}
}

func notifyCMAESEpoch(options RunOptions, result Result) error {
	if options.EpochObserver == nil {
		return nil
	}

	progress := Progress{
		Iterations:  result.Iterations,
		Evaluations: result.Evaluations,
		BestParams:  append([]float64(nil), result.BestParams...),
		BestCost:    result.BestCost,
	}
	if options.ProgressMapper != nil {
		progress = options.ProgressMapper(progress)
	}

	err := options.EpochObserver(EpochBoundary{
		Epoch: 1, Progress: progress, Termination: result.Termination,
	})
	if err != nil {
		return fmt.Errorf("cma-es epoch observer: %w", err)
	}

	return nil
}
