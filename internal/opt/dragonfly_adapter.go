package opt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"slices"

	"github.com/CWBudde/dragonfly"
)

// errNegativeResumeCount reports a resume count below zero, which is a caller
// bug rather than an optimizer outcome.
var errNegativeResumeCount = errors.New("resume count cannot be negative")

// dragonflyModulePath is the module the proof-of-concept adapter optimizes
// with.
const dragonflyModulePath = "github.com/CWBudde/dragonfly"

// DragonflyAdapter runs the continuous Dragonfly Algorithm behind this
// package's Optimizer interface. It is a proof of concept, not a peer of
// MayflyAdapter.
//
// What it supports is deliberately the subset the renderer pipelines actually
// call: bounded minimization, Repair, inequality constraints, per-iteration
// progress, early stopping, parallel evaluation, and seeding from known
// candidates so WithEpochs and WithRestarts work. It is not selectable from
// the server, the schedule format, resume, or the web UI, and no checkpoint
// records that a run used it, so a resumed run silently becomes a Mayfly run.
// Only cmd/run's --optimizer flag reaches it today.
//
// Fit quality is expected to be worse than Mayfly's. Dragonfly's shared
// convergence factor reaches zero at the halfway point of a run, after which
// only the food term and inertia still move a dragonfly, so the algorithm
// explores well and exploits poorly. That is the published behavior of the
// paper's algorithm, not a defect of this adapter, and nothing here tunes
// around it.
type DragonflyAdapter struct {
	maxIters int
	popSize  int
	seed     int64
	logger   *slog.Logger
	stop     Stop
	// parallelWorkers enables Dragonfly's concurrent population evaluation and
	// bounds its worker pool. Values below two keep evaluation serial, which is
	// the default.
	parallelWorkers int
}

// DragonflyOption customizes an adapter at construction time, mirroring
// MayflyOption.
type DragonflyOption func(*DragonflyAdapter)

// WithDragonflyLogger reports Dragonfly lifecycle events through logger. The
// event names and levels match Mayfly's, so the same demotion of
// per-iteration records applies. A nil logger disables optimizer logging.
func WithDragonflyLogger(logger *slog.Logger) DragonflyOption {
	return func(d *DragonflyAdapter) { d.logger = logger }
}

// WithDragonflyEarlyStop enables Dragonfly's per-iteration stopping criteria.
// A zero Stop leaves the optimizer configured exactly as it is without it.
func WithDragonflyEarlyStop(stop Stop) DragonflyOption {
	return func(d *DragonflyAdapter) { d.stop = stop }
}

// WithDragonflyParallelEvaluation makes Dragonfly evaluate swarm members
// concurrently with at most workers goroutines. The caller must guarantee that
// the objective is safe to call from several goroutines at once, exactly as
// WithParallelEvaluation requires.
func WithDragonflyParallelEvaluation(workers int) DragonflyOption {
	return func(d *DragonflyAdapter) { d.parallelWorkers = workers }
}

// NewDragonfly creates a Dragonfly optimizer adapter.
func NewDragonfly(maxIters, popSize int, seed int64, options ...DragonflyOption) Optimizer {
	adapter := &DragonflyAdapter{maxIters: maxIters, popSize: popSize, seed: seed}

	for _, option := range options {
		if option != nil {
			option(adapter)
		}
	}

	return adapter
}

// ParallelEvaluationWorkers reports the concurrent evaluation width this
// optimizer will drive, which is one unless WithDragonflyParallelEvaluation
// raised it.
func (d *DragonflyAdapter) ParallelEvaluationWorkers() int {
	if d.parallelWorkers < 1 {
		return 1
	}

	return d.parallelWorkers
}

// IterationBudget reports the iteration cap one run of this adapter consumes.
func (d *DragonflyAdapter) IterationBudget() int {
	return d.maxIters
}

// Run executes the Dragonfly optimization using the external library.
func (d *DragonflyAdapter) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	result, err := d.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{})
	if err != nil {
		return nil, math.Inf(1)
	}

	return result.BestParams, result.BestCost
}

// RunWithInitial executes optimization starting from an initial solution,
// seeding part of the swarm with local perturbations of it.
func (d *DragonflyAdapter) RunWithInitial(
	initialParams []float64,
	initialCost float64,
	eval func([]float64) float64,
	lower, upper []float64,
	dim int,
) ([]float64, float64) {
	result, err := d.RunContext(context.Background(), Problem{
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

// RunContext executes Dragonfly with cancellation, progress, measured work,
// and optional swarm seeding around known candidates.
func (d *DragonflyAdapter) RunContext(ctx context.Context, problem Problem, options RunOptions) (Result, error) {
	err := validateDragonflyRun(problem, options)
	if err != nil {
		return Result{}, err
	}

	canonicalize := canonicalizer(problem)
	config := d.buildConfig(problem, options, canonicalize)

	// A seeded generator is the whole point: a run has to reproduce
	// bit-for-bit for a fixed seed, which a cryptographic source cannot do.
	rng := rand.New(rand.NewSource(d.runSeed(options))) //nolint:gosec // deterministic runs require a seeded PRNG
	config.Rand = rng

	best := Result{BestCost: math.Inf(1), Termination: TerminationCompleted}
	bestViolation := math.Inf(1)

	normalizedSeeds, err := d.normalizedSeeds(problem, options, config.Constraints, &best, &bestViolation)
	if err != nil {
		return Result{}, err
	}

	runOptions := d.buildRunOptions(buildRunOptionsInput{
		normalizedSeeds: normalizedSeeds,
		rng:             rng,
		config:          config,
		options:         options,
		canonicalize:    canonicalize,
		best:            &best,
		bestViolation:   &bestViolation,
	})

	result, err := dragonfly.OptimizeContext(ctx, config, runOptions...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			best.Termination = TerminationCancelled

			return best, fmt.Errorf("dragonfly optimization: %w", err)
		}

		return Result{}, fmt.Errorf("dragonfly optimization: %w", err)
	}

	params := canonicalize(result.GlobalBest.Position)
	if betterDragonflyCandidate(
		result.GlobalBest.Cost,
		result.GlobalBest.ConstraintViolation,
		best.BestCost,
		bestViolation,
		config.Constraints,
	) {
		best.BestParams = params
		best.BestCost = result.GlobalBest.Cost
	}

	best.Iterations = result.IterationCount
	best.Evaluations = result.FuncEvalCount
	best.Termination = terminationFromDragonfly(result.TerminationReason)

	return best, nil
}

// validateDragonflyRun rejects a problem or a set of run options the adapter
// cannot honor, before any optimizer state is built.
func validateDragonflyRun(problem Problem, options RunOptions) error {
	err := validateProblem(problem)
	if err != nil {
		return err
	}

	if options.ResumeCount < 0 {
		return errNegativeResumeCount
	}

	return validateContinuationProfile(options.Continuation)
}

// canonicalizer returns the conversion every callback applies to a candidate.
//
// Dragonfly, like Mayfly, only supports uniform bounds, so the search runs in
// [0,1] and every callback denormalizes and repairs before the candidate
// reaches the problem.
func canonicalizer(problem Problem) func([]float64) []float64 {
	return func(normalizedParams []float64) []float64 {
		params := make([]float64, len(normalizedParams))
		for i := range normalizedParams {
			params[i] = problem.Lower[i] + normalizedParams[i]*(problem.Upper[i]-problem.Lower[i])
		}

		repairCandidate(problem, params)

		return params
	}
}

// runSeed derives the seed one invocation runs with. A continuation or a
// restart attempt varies it without losing reproducibility for a fixed base
// seed.
func (d *DragonflyAdapter) runSeed(options RunOptions) int64 {
	if options.ResumeCount > 0 || options.SeedOffset > 0 {
		return continuationSeed(d.seed, options.ResumeCount, options.SeedOffset)
	}

	return d.seed
}

// buildRunOptionsInput carries the run-scoped state the library options close
// over. It is a struct because the observer needs the incumbent by pointer and
// a positional parameter list of that shape reads as an accident.
type buildRunOptionsInput struct {
	rng           *rand.Rand
	config        *dragonfly.Config
	canonicalize  func([]float64) []float64
	best          *Result
	bestViolation *float64
	options       RunOptions

	normalizedSeeds [][]float64
}

// buildRunOptions assembles the library's run options: swarm seeding, logging,
// and the progress observer that maintains the incumbent.
func (d *DragonflyAdapter) buildRunOptions(input buildRunOptionsInput) []dragonfly.RunOption {
	var runOptions []dragonfly.RunOption

	if len(input.normalizedSeeds) > 0 {
		// Dragonfly has a single swarm where Mayfly has male and female
		// populations, so only the exact-first half of the seeded pair is used.
		positions, _ := seededPopulationFromCandidates(
			input.normalizedSeeds, d.popSize, input.rng, input.options.Continuation)
		runOptions = append(runOptions, dragonfly.WithInitialPopulation(positions))
	}

	if d.logger != nil {
		runOptions = append(runOptions, dragonfly.WithLogger(mayflyLogger{logger: d.logger}))
	}

	return append(runOptions, dragonfly.WithProgressObserver(progressObserver(input)))
}

// progressObserver folds each iteration's best candidate into the incumbent and
// forwards a snapshot to the caller's observer.
func progressObserver(input buildRunOptionsInput) dragonfly.ProgressObserver {
	return func(progress dragonfly.Progress) {
		params := input.canonicalize(progress.Best.Position)

		if betterDragonflyCandidate(
			progress.Best.Cost,
			progress.Best.ConstraintViolation,
			input.best.BestCost,
			*input.bestViolation,
			input.config.Constraints,
		) {
			input.best.BestParams = params
			input.best.BestCost = progress.Best.Cost
			*input.bestViolation = progress.Best.ConstraintViolation
		}

		input.best.Iterations = progress.Iteration
		input.best.Evaluations = progress.EvaluationCount

		if input.options.Observer == nil {
			return
		}

		reported := Progress{
			Iterations:  progress.Iteration,
			Evaluations: progress.EvaluationCount,
			BestParams:  append([]float64(nil), input.best.BestParams...),
			BestCost:    input.best.BestCost,
		}
		if input.options.ProgressMapper != nil {
			reported = input.options.ProgressMapper(reported)
		}

		input.options.Observer(reported)
	}
}

// buildConfig translates the adapter's settings and the problem onto a
// Dragonfly configuration. canonicalize denormalizes and repairs a candidate,
// so every callback sees the problem's own representation.
func (d *DragonflyAdapter) buildConfig(
	problem Problem,
	options RunOptions,
	canonicalize func([]float64) []float64,
) *dragonfly.Config {
	config := dragonfly.NewDefaultConfig()
	config.ObjectiveFunc = func(normalizedParams []float64) float64 {
		return problem.Eval(canonicalize(normalizedParams))
	}
	config.ProblemSize = problem.Dim
	config.MaxIterations = d.maxIters
	config.NPop = d.popSize
	config.LowerBound = 0.0
	config.UpperBound = 1.0

	if d.parallelWorkers > 1 {
		config.EnableParallel = true
		config.MaxWorkers = d.parallelWorkers
	}

	// MaxStepRatio is the step clamp as a fraction of the search range, and the
	// range here is exactly one, so a normalized velocity cap transfers
	// directly. Dragonfly's step is the paper's dX, the velocity analog.
	if options.Continuation != nil && options.Continuation.MaxVelocity > 0 {
		config.MaxStepRatio = options.Continuation.MaxVelocity
	}

	if len(problem.Inequalities) > 0 {
		config.Constraints = &dragonfly.ConstraintConfig{
			Handling: dragonfly.ConstraintHandlingFeasibility,
			Inequalities: []dragonfly.ConstraintFunction{func(normalizedParams []float64) float64 {
				return inequalityViolation(problem.Inequalities, canonicalize(normalizedParams))
			}},
		}
	}

	if d.stop.enabled() {
		convergence := &dragonfly.ConvergenceConfig{
			MinImprovement:       d.stop.MinImprovement,
			StagnationIterations: d.stop.StagnationIters,
			MinIterations:        min(d.stop.MinIters, d.maxIters),
		}
		if d.stop.TargetCost > 0 {
			target := d.stop.TargetCost
			convergence.TargetCost = &target
		}

		config.Convergence = convergence
	}

	return config
}

// normalizedSeeds validates every caller-supplied candidate, folds the best of
// them into the incumbent, and returns them in the optimizer's [0,1] space.
func (d *DragonflyAdapter) normalizedSeeds(
	problem Problem,
	options RunOptions,
	constraints *dragonfly.ConstraintConfig,
	best *Result,
	bestViolation *float64,
) ([][]float64, error) {
	seedCandidates := make([]Candidate, 0, len(options.AdditionalSeeds)+1)
	if options.Initial != nil {
		seedCandidates = append(seedCandidates, *options.Initial)
	}

	seedCandidates = append(seedCandidates, options.AdditionalSeeds...)

	normalizedSeeds := make([][]float64, 0, len(seedCandidates))

	for index, candidate := range seedCandidates {
		candidate.Params = append([]float64(nil), candidate.Params...)
		originalParams := append([]float64(nil), candidate.Params...)
		repairCandidate(problem, candidate.Params)

		if !slices.Equal(originalParams, candidate.Params) {
			candidate.Cost = problem.Eval(candidate.Params)
		}

		err := validateCandidate(candidate, problem)
		if err != nil {
			return nil, fmt.Errorf("initial candidate %d: %w", index, err)
		}

		violation := inequalityViolation(problem.Inequalities, candidate.Params)
		if betterDragonflyCandidate(candidate.Cost, violation, best.BestCost, *bestViolation, constraints) {
			best.BestParams = append([]float64(nil), candidate.Params...)
			best.BestCost = candidate.Cost
			*bestViolation = violation
		}

		normalized := make([]float64, problem.Dim)
		for i := range normalized {
			normalized[i] = (candidate.Params[i] - problem.Lower[i]) / (problem.Upper[i] - problem.Lower[i])
		}

		normalizedSeeds = append(normalizedSeeds, normalized)
	}

	return normalizedSeeds, nil
}

func betterDragonflyCandidate(
	cost, violation, incumbentCost, incumbentViolation float64,
	constraints *dragonfly.ConstraintConfig,
) bool {
	return dragonfly.BetterConstrainedCandidate(
		dragonfly.CandidateEvaluation{Cost: cost, ConstraintViolation: violation},
		dragonfly.CandidateEvaluation{Cost: incumbentCost, ConstraintViolation: incumbentViolation},
		constraints,
	)
}

// terminationFromDragonfly maps a Dragonfly termination reason onto the
// application vocabulary, exactly as terminationFromMayfly does.
func terminationFromDragonfly(reason dragonfly.TerminationReason) Termination {
	switch reason {
	case dragonfly.TerminationTargetCost:
		return TerminationTargetCost
	case dragonfly.TerminationStagnation:
		return TerminationStagnation
	default:
		return TerminationCompleted
	}
}

// DragonflyLibraryVersion reports the Dragonfly module version compiled into
// this binary, for the same comparability reason LibraryVersion exists.
func DragonflyLibraryVersion() string { return moduleVersion(dragonflyModulePath) }
