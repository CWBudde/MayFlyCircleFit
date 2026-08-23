package opt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"slices"

	"github.com/cwbudde/mayfly"
)

// ErrUnknownVariant reports an unsupported Mayfly variant name.
var ErrUnknownVariant = errors.New("unknown mayfly variant")

// variantStandard is the default Mayfly variant. An empty variant name resolves
// to it so checkpoints written before the variant field existed still resume.
const variantStandard = "standard"

// supportedVariants lists the variant names RunContext can configure.
var supportedVariants = map[string]struct{}{
	variantStandard: {},
	"desma":         {},
	"olce":          {},
	"eobbma":        {},
	"gsasma":        {},
	"mpma":          {},
	"aoblmoa":       {},
}

// MayflyAdapter wraps the external Mayfly library to conform to our Optimizer interface.
type MayflyAdapter struct {
	maxIters int
	popSize  int
	seed     int64
	variant  string // "standard", "desma", "olce", "eobbma", "gsasma", "mpma", "aoblmoa"
	logger   *slog.Logger
	stop     Stop
	// parallelWorkers enables Mayfly's concurrent population evaluation and
	// bounds its worker pool. Values below two keep evaluation serial, which is
	// the default.
	parallelWorkers int
	// crossoverCount overrides Mayfly's offspring count per iteration. Zero
	// leaves the library's own scaling alone.
	crossoverCount int
	// danceDamp, aquilaWeight and oppositionProbability override library
	// parameters whose zero value is a meaningful setting rather than an
	// absence, so each is a pointer and nil means "leave the library's own
	// default alone". This is why they do not follow crossoverCount's
	// zero-as-unset convention: the library refuses an offspring count of
	// zero, which frees that value to act as a sentinel, while a damping
	// factor of zero, an Aquila weight of zero and an opposition rate of zero
	// are all configurations the library accepts and runs.
	danceDamp             *float64
	aquilaWeight          *float64
	oppositionProbability *float64
}

// Stop configures optimizer-level early stopping, evaluated per iteration
// within a single run. The zero value disables every criterion, which is the
// default and reproduces the behavior of runs configured without it.
type Stop struct {
	// TargetCost stops the run once the best cost reaches it. It is compared
	// against the objective's own value, not a normalized one, so it uses the
	// same units reported by status output and traces. Zero disables the check.
	TargetCost float64
	// MinImprovement is the absolute cost reduction that counts as progress and
	// resets the stagnation counter. Zero accepts any improvement.
	MinImprovement float64
	// StagnationIters stops the run after this many consecutive iterations
	// without progress. Zero disables stagnation detection.
	StagnationIters int
	// MinIters is the number of iterations that must complete before any
	// criterion can stop the run.
	MinIters int
}

// enabled reports whether any stopping criterion is configured.
func (s Stop) enabled() bool {
	return s.TargetCost > 0 || s.StagnationIters > 0
}

// MayflyOption customizes an adapter at construction time. Options are applied
// per optimizer rather than per run, so wrappers that rebuild RunOptions cannot
// drop them.
type MayflyOption func(*MayflyAdapter)

// WithCrossoverCount overrides how many crossover offspring Mayfly produces
// per iteration. Zero leaves the library's own scaling alone.
//
// The library derives its mutant pool from the offspring, so a count below the
// mutant count starves mutation rather than merely reducing recombination.
// An odd count yields one fewer offspring, because the library mates pairs.
func WithCrossoverCount(count int) MayflyOption {
	return func(m *MayflyAdapter) { m.crossoverCount = count }
}

// OptionalFloat adapts a pointer-valued setting to an optimizer option,
// yielding a no-op when the setting is nil.
//
// The advanced parameters below are configured as pointers because each has a
// meaningful zero, so nil is the only way to say "leave the library's own
// default alone". Every caller that reads one out of a JobConfig or a
// checkpoint needs the same nil check, so it lives here once.
func OptionalFloat(value *float64, build func(float64) MayflyOption) MayflyOption {
	if value == nil {
		return func(*MayflyAdapter) {}
	}

	return build(*value)
}

// WithDanceDamp overrides the per-iteration decay applied to the nuptial-dance
// term, whose library default is 0.8.
//
// The dance is the random walk the leading male takes on top of its velocity,
// so the damping factor sets how fast the swarm's exploration decays: at 0.8
// the term retains about a tenth of its initial size after ten iterations.
// Raising it slows that decay. This is an advanced parameter, and the library
// itself does not range-check it -- app.JobConfig does. Above 1 the
// coefficient grows each iteration, but velocity and position clamping keep
// the run finite, so the effect is a saturated random walk, not divergence.
func WithDanceDamp(damp float64) MayflyOption {
	return func(m *MayflyAdapter) { m.danceDamp = &damp }
}

// WithAquilaWeight overrides the probability that an AOBLMOA individual takes
// an Aquila step instead of the ordinary Mayfly velocity and position update.
//
// The library default is 1.0, following the paper, which means every
// individual takes the Aquila step every iteration and the Mayfly dynamics
// never run. Lowering it mixes the two. It has no effect on any other variant.
func WithAquilaWeight(weight float64) MayflyOption {
	return func(m *MayflyAdapter) { m.aquilaWeight = &weight }
}

// WithOppositionProbability sets the AOBLMOA opposition-based-learning rate.
//
// The value is inert as of MayFly v0.6.0: the library applies stochastic
// opposition to every offspring instead of a sampled share, so it range-checks
// this setting and then never reads it. The option is kept so stored
// configurations and checkpoints continue to load. It has no effect on any
// other variant.
func WithOppositionProbability(probability float64) MayflyOption {
	return func(m *MayflyAdapter) { m.oppositionProbability = &probability }
}

// WithLogger reports Mayfly lifecycle events through logger. Per-iteration
// events are demoted to debug so an ordinary run logs one completion record per
// optimizer run. A nil logger disables optimizer logging entirely.
func WithLogger(logger *slog.Logger) MayflyOption {
	return func(m *MayflyAdapter) { m.logger = logger }
}

// WithEarlyStop enables Mayfly's per-iteration stopping criteria. A zero Stop
// leaves the optimizer configured exactly as it is without this option.
func WithEarlyStop(stop Stop) MayflyOption {
	return func(m *MayflyAdapter) { m.stop = stop }
}

// WithParallelEvaluation makes Mayfly evaluate population members concurrently
// with at most workers goroutines. The caller must guarantee that the objective
// function is safe to call from several goroutines at once; the renderer
// pipeline does that by leasing an independent session per evaluation.
//
// Parallel evaluation is reproducible for a fixed seed but is not bit-identical
// to a serial run of the same seed. Mayfly's serial male loop lets a member
// that improves the global best steer the very next member of the same
// generation, while the parallel loop holds the global best fixed for the whole
// generation and merges afterwards. Both orders are deterministic; they are
// different search trajectories.
func WithParallelEvaluation(workers int) MayflyOption {
	return func(m *MayflyAdapter) { m.parallelWorkers = workers }
}

// ParallelEvaluationWorkers reports the concurrent evaluation width this
// optimizer will drive, which is one unless WithParallelEvaluation raised it.
//
// It exists so a caller whose objective is not re-entrant can refuse the run
// instead of corrupting it. Concurrency is invisible at the opt.Optimizer
// interface, and an objective that shares a buffer or a canvas fails silently
// rather than loudly: wrong costs, a wrong image, no error.
func (m *MayflyAdapter) ParallelEvaluationWorkers() int {
	if m.parallelWorkers < 1 {
		return 1
	}

	return m.parallelWorkers
}

// parallelEvaluationReporter is implemented by optimizers that can say how many
// goroutines will call the objective, and by every wrapper around one.
type parallelEvaluationReporter interface {
	ParallelEvaluationWorkers() int
}

// ParallelEvaluationWidth reports how many goroutines optimizer will call the
// objective from. Optimizers that cannot report it are assumed serial, which is
// what the plain Optimizer interface has always promised.
func ParallelEvaluationWidth(optimizer Optimizer) int {
	reporter, ok := optimizer.(parallelEvaluationReporter)
	if !ok {
		return 1
	}

	if workers := reporter.ParallelEvaluationWorkers(); workers > 1 {
		return workers
	}

	return 1
}

func newAdapter(variant string, maxIters, popSize int, seed int64, options ...MayflyOption) *MayflyAdapter {
	adapter := &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
		variant:  variant,
	}

	for _, option := range options {
		if option != nil {
			option(adapter)
		}
	}

	return adapter
}

// NewMayfly creates a new Mayfly optimizer adapter.
func NewMayfly(maxIters, popSize int, seed int64, options ...MayflyOption) Optimizer {
	return newAdapter(variantStandard, maxIters, popSize, seed, options...)
}

// NewMayflyDESMA creates a Mayfly optimizer using the DESMA variant.
func NewMayflyDESMA(maxIters, popSize int, seed int64, options ...MayflyOption) Optimizer {
	return newAdapter("desma", maxIters, popSize, seed, options...)
}

// NewMayflyOLCE creates a Mayfly optimizer using the OLCE-MA variant.
func NewMayflyOLCE(maxIters, popSize int, seed int64, options ...MayflyOption) Optimizer {
	return newAdapter("olce", maxIters, popSize, seed, options...)
}

// NewMayflyVariant creates an adapter for a named Mayfly variant. An empty name
// selects the standard variant, because resume reads a persisted configuration
// directly and checkpoints predating the variant field carry no name.
func NewMayflyVariant(variant string, maxIters, popSize int, seed int64, options ...MayflyOption) (Optimizer, error) {
	if variant == "" {
		variant = variantStandard
	}

	if _, ok := supportedVariants[variant]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownVariant, variant)
	}

	return newAdapter(variant, maxIters, popSize, seed, options...), nil
}

// RunWithInitial executes optimization starting from an initial solution.
//
// The initial solution seeds half of each Mayfly population with local
// perturbations while the remaining slots stay random. The incumbent is also
// retained independently, so a continuation cannot lose its checkpoint.
func (m *MayflyAdapter) RunWithInitial(initialParams []float64, initialCost float64, eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	result, err := m.RunContext(context.Background(), Problem{
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

// Run executes the Mayfly optimization using the external library.
func (m *MayflyAdapter) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	result, err := m.RunContext(context.Background(), Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, RunOptions{})
	if err != nil {
		return nil, math.Inf(1)
	}

	return result.BestParams, result.BestCost
}

// RunContext executes Mayfly with cancellation, progress, measured work, and
// optional population seeding around a saved best candidate.
func (m *MayflyAdapter) RunContext(ctx context.Context, problem Problem, options RunOptions) (Result, error) {
	if err := validateProblem(problem); err != nil {
		return Result{}, err
	}

	if options.ResumeCount < 0 {
		return Result{}, errors.New("resume count cannot be negative")
	}

	if err := validateContinuationProfile(options.Continuation); err != nil {
		return Result{}, err
	}

	var config *mayfly.Config

	// Select variant
	switch m.variant {
	case "desma":
		config = mayfly.NewDESMAConfig()
	case "olce":
		config = mayfly.NewOLCEConfig()
	case "eobbma":
		config = mayfly.NewEOBBMAConfig()
	case "gsasma":
		config = mayfly.NewGSASMAConfig()
	case "mpma":
		config = mayfly.NewMPMAConfig()
	case "aoblmoa":
		config = mayfly.NewAOBLMOAConfig()
	default:
		config = mayfly.NewDefaultConfig()
	}

	// Denormalize parameters from [0,1] to actual bounds
	// (mayfly only supports uniform bounds, so we normalize to [0,1])
	denormalize := func(params []float64) []float64 {
		result := make([]float64, len(params))
		for i := range params {
			result[i] = problem.Lower[i] + params[i]*(problem.Upper[i]-problem.Lower[i])
		}

		return result
	}
	normalize := func(params []float64) []float64 {
		result := make([]float64, len(params))
		for i := range params {
			result[i] = (params[i] - problem.Lower[i]) / (problem.Upper[i] - problem.Lower[i])
		}

		return result
	}

	// Canonicalize every callback input identically. Mayfly operates in a
	// uniform [0,1] search space, while objectives and constraints use the
	// problem's denormalized, repaired representation.
	canonicalize := func(normalizedParams []float64) []float64 {
		params := denormalize(normalizedParams)
		repairCandidate(problem, params)

		return params
	}

	// Wrap eval function to handle normalization.
	normalizedEval := func(normalizedParams []float64) float64 {
		return problem.Eval(canonicalize(normalizedParams))
	}

	config.ObjectiveFunc = normalizedEval
	config.ProblemSize = problem.Dim
	config.MaxIterations = m.maxIters
	config.NPop = m.popSize
	config.NPopF = m.popSize

	if m.crossoverCount > 0 {
		config.NC = m.crossoverCount
	}

	if m.danceDamp != nil {
		config.DanceDamp = *m.danceDamp
	}

	if m.aquilaWeight != nil {
		// Writing the deprecated field is the point: this is the documented
		// escape hatch back to the pre-v0.6.0 probabilistic branch, and the
		// library still honours a value in [0,1]. Leaving the field untouched
		// is what selects the paper's fitness test, so there is no
		// non-deprecated way to express the override.
		//lint:ignore SA1019 deliberate use of the deprecated override; there is no non-deprecated equivalent.
		config.AquilaWeight = *m.aquilaWeight
	}

	if m.oppositionProbability != nil {
		config.OppositionProbability = *m.oppositionProbability
	}
	config.LowerBound = 0.0

	config.UpperBound = 1.0
	if m.parallelWorkers > 1 {
		config.EnableParallel = true
		config.MaxWorkers = m.parallelWorkers
	}

	if options.Continuation != nil && options.Continuation.MaxVelocity > 0 {
		config.VelMax = options.Continuation.MaxVelocity
		config.VelMin = -options.Continuation.MaxVelocity
	}

	if len(problem.Inequalities) > 0 {
		// Aggregate after canonicalization so Repair runs once per constraint
		// evaluation, regardless of how many inequalities a problem declares.
		// Mayfly then applies feasibility ranking while retaining the raw
		// objective cost in progress and results.
		config.Constraints = &mayfly.ConstraintConfig{
			Handling: mayfly.ConstraintHandlingFeasibility,
			Inequalities: []mayfly.ConstraintFunction{func(normalizedParams []float64) float64 {
				return inequalityViolation(problem.Inequalities, canonicalize(normalizedParams))
			}},
		}
	}

	// Leave Convergence nil unless a criterion is actually configured. An empty
	// convergence config is inert today, but it would still engage the
	// optimizer's per-iteration tracker for every caller.
	if m.stop.enabled() {
		convergence := &mayfly.ConvergenceConfig{
			MinImprovement:       m.stop.MinImprovement,
			StagnationIterations: m.stop.StagnationIters,
			// Mayfly rejects a minimum above the iteration cap. Resume reads a
			// persisted configuration without renormalizing it, so clamp here
			// rather than surfacing an opaque optimizer error.
			MinIterations: min(m.stop.MinIters, m.maxIters),
		}
		if m.stop.TargetCost > 0 {
			target := m.stop.TargetCost
			convergence.TargetCost = &target
		}

		config.Convergence = convergence
	}

	// Create RNG for population initialization
	runSeed := m.seed
	if options.ResumeCount > 0 || options.SeedOffset > 0 {
		runSeed = continuationSeed(m.seed, options.ResumeCount, options.SeedOffset)
	}

	rng := rand.New(rand.NewSource(runSeed))
	config.Rand = rng

	var runOptions []mayfly.RunOption
	best := Result{BestCost: math.Inf(1), Termination: TerminationCompleted}
	bestViolation := math.Inf(1)

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
			return Result{}, fmt.Errorf("initial candidate %d: %w", index, err)
		}

		violation := inequalityViolation(problem.Inequalities, candidate.Params)
		if betterCandidate(candidate.Cost, violation, best.BestCost, bestViolation, config.Constraints) {
			best.BestParams = append([]float64(nil), candidate.Params...)
			best.BestCost = candidate.Cost
			bestViolation = violation
		}

		normalizedSeeds = append(normalizedSeeds, normalize(candidate.Params))
	}

	if len(normalizedSeeds) > 0 {
		maleSeeds, femaleSeeds := seededPopulationFromCandidates(normalizedSeeds, m.popSize, rng, options.Continuation)
		runOptions = append(runOptions, mayfly.WithInitialPopulation(maleSeeds, femaleSeeds))
	}

	if m.logger != nil {
		runOptions = append(runOptions, mayfly.WithLogger(mayflyLogger{logger: m.logger}))
	}

	runOptions = append(runOptions, mayfly.WithProgressObserver(func(progress mayfly.Progress) {
		params := denormalize(progress.Best.Position)
		repairCandidate(problem, params)

		if betterCandidate(
			progress.Best.Cost,
			progress.Best.ConstraintViolation,
			best.BestCost,
			bestViolation,
			config.Constraints,
		) {
			best.BestParams = append([]float64(nil), params...)
			best.BestCost = progress.Best.Cost
			bestViolation = progress.Best.ConstraintViolation
		}

		best.Iterations = progress.Iteration

		best.Evaluations = progress.EvaluationCount
		if options.Observer != nil {
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
		}
	}))

	result, err := mayfly.OptimizeContext(ctx, config, runOptions...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			best.Termination = TerminationCancelled
			return best, err
		}

		return Result{}, fmt.Errorf("mayfly optimization: %w", err)
	}

	params := denormalize(result.GlobalBest.Position)
	repairCandidate(problem, params)

	if betterCandidate(
		result.GlobalBest.Cost,
		result.GlobalBest.ConstraintViolation,
		best.BestCost,
		bestViolation,
		config.Constraints,
	) {
		best.BestParams = params
		best.BestCost = result.GlobalBest.Cost
		bestViolation = result.GlobalBest.ConstraintViolation
	}

	best.Iterations = result.IterationCount
	best.Evaluations = result.FuncEvalCount
	best.Termination = terminationFromMayfly(result.TerminationReason)

	return best, nil
}

func repairCandidate(problem Problem, params []float64) {
	if problem.Repair != nil {
		problem.Repair(params)
	}
}

// inequalityViolation mirrors Mayfly's inequality aggregation for parameters
// already expressed in problem space. Keeping it here lets resume candidates
// participate in the same feasibility ordering as the optimizer population.
func inequalityViolation(constraints []InequalityConstraint, params []float64) float64 {
	violation := 0.0

	for _, constraint := range constraints {
		value := constraint(params)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return math.Inf(1)
		}

		violation += max(0, value)
		if math.IsInf(violation, 1) {
			return math.Inf(1)
		}
	}

	return violation
}

func betterCandidate(cost, violation, incumbentCost, incumbentViolation float64, constraints *mayfly.ConstraintConfig) bool {
	return mayfly.BetterConstrainedCandidate(
		mayfly.CandidateEvaluation{Cost: cost, ConstraintViolation: violation},
		mayfly.CandidateEvaluation{Cost: incumbentCost, ConstraintViolation: incumbentViolation},
		constraints,
	)
}

// terminationFromMayfly maps a Mayfly termination reason onto the application
// vocabulary. maximum_iterations maps onto the existing "completed" value so
// checkpoints written before early stopping existed keep their meaning.
func terminationFromMayfly(reason mayfly.TerminationReason) Termination {
	switch reason {
	case mayfly.TerminationTargetCost:
		return TerminationTargetCost
	case mayfly.TerminationStagnation:
		return TerminationStagnation
	default:
		return TerminationCompleted
	}
}

func validateProblem(problem Problem) error {
	if problem.Eval == nil {
		return errors.New("objective function is required")
	}

	if problem.Dim <= 0 || len(problem.Lower) != problem.Dim || len(problem.Upper) != problem.Dim {
		return errors.New("problem dimensions and bounds do not match")
	}

	for i := range problem.Dim {
		if math.IsNaN(problem.Lower[i]) || math.IsNaN(problem.Upper[i]) || math.IsInf(problem.Lower[i], 0) || math.IsInf(problem.Upper[i], 0) || problem.Lower[i] >= problem.Upper[i] {
			return fmt.Errorf("invalid bounds at dimension %d", i)
		}
	}

	for i, constraint := range problem.Inequalities {
		if constraint == nil {
			return fmt.Errorf("inequality constraint %d is nil", i)
		}
	}

	return nil
}

func validateCandidate(candidate Candidate, problem Problem) error {
	if len(candidate.Params) != problem.Dim || math.IsNaN(candidate.Cost) || math.IsInf(candidate.Cost, 0) {
		return errors.New("initial candidate has invalid dimensions or cost")
	}

	for i, value := range candidate.Params {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < problem.Lower[i] || value > problem.Upper[i] {
			return fmt.Errorf("initial candidate dimension %d is outside bounds", i)
		}
	}

	return nil
}

func seededPopulationFromCandidates(candidates [][]float64, population int, rng *rand.Rand, profile *ContinuationProfile) ([][]float64, [][]float64) {
	if len(candidates) == 0 {
		return nil, nil
	}

	localFraction := 0.5
	sigma := 0.05
	coordinateRate := 1.0

	if profile != nil {
		localFraction = profile.LocalFraction
		sigma = profile.Sigma
		coordinateRate = profile.CoordinateRate
	}

	seedCount := max(int(math.Ceil(float64(population)*localFraction)), 1)

	seedCount = min(seedCount, population)
	makeSeeds := func(exact bool) [][]float64 {
		seeds := make([][]float64, seedCount)
		for i := range seeds {
			seeds[i] = append([]float64(nil), candidates[i%len(candidates)]...)
			if !exact || i >= len(candidates) {
				perturbed := false

				for dimension := range seeds[i] {
					if coordinateRate < 1 && rng.Float64() > coordinateRate {
						continue
					}

					seeds[i][dimension] += rng.NormFloat64() * sigma
					seeds[i][dimension] = math.Max(0, math.Min(1, seeds[i][dimension]))
					perturbed = true
				}

				if !perturbed && len(seeds[i]) > 0 {
					dimension := rng.Intn(len(seeds[i]))
					seeds[i][dimension] += rng.NormFloat64() * sigma
					seeds[i][dimension] = math.Max(0, math.Min(1, seeds[i][dimension]))
				}
			}
		}

		return seeds
	}

	return makeSeeds(true), makeSeeds(false)
}

func validateContinuationProfile(profile *ContinuationProfile) error {
	if profile == nil {
		return nil
	}

	if profile.LocalFraction <= 0 || profile.LocalFraction > 1 {
		return errors.New("continuation local fraction must be in (0,1]")
	}

	if profile.Sigma <= 0 || profile.Sigma > 1 {
		return errors.New("continuation sigma must be in (0,1]")
	}

	if profile.CoordinateRate <= 0 || profile.CoordinateRate > 1 {
		return errors.New("continuation coordinate rate must be in (0,1]")
	}

	if profile.MaxVelocity < 0 || profile.MaxVelocity > 1 {
		return errors.New("continuation maximum velocity must be in [0,1]")
	}

	return nil
}

func continuationSeed(seed int64, resumeCount, seedOffset int) int64 {
	// A zero seedOffset contributes nothing, so seeds chosen before restarts
	// existed are unchanged.
	value := uint64(seed) + uint64(resumeCount)*0x9e3779b97f4a7c15 +
		uint64(seedOffset)*0xc2b2ae3d27d4eb4f
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31

	return int64(value & math.MaxInt64)
}
