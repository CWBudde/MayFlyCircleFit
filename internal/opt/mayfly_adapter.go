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

// MayflyAdapter wraps the external Mayfly library to conform to our Optimizer interface
type MayflyAdapter struct {
	maxIters int
	popSize  int
	seed     int64
	variant  string // "standard", "desma", "olce", "eobbma", "gsasma", "mpma", "aoblmoa"
	logger   *slog.Logger
	stop     Stop
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

// NewMayfly creates a new Mayfly optimizer adapter
func NewMayfly(maxIters, popSize int, seed int64, options ...MayflyOption) Optimizer {
	return newAdapter(variantStandard, maxIters, popSize, seed, options...)
}

// NewMayflyDESMA creates a Mayfly optimizer using the DESMA variant
func NewMayflyDESMA(maxIters, popSize int, seed int64, options ...MayflyOption) Optimizer {
	return newAdapter("desma", maxIters, popSize, seed, options...)
}

// NewMayflyOLCE creates a Mayfly optimizer using the OLCE-MA variant
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
// IMPLEMENTATION NOTE: The external Mayfly library does not support custom
// population initialization. Therefore, this implementation uses a simple
// strategy: run the optimizer and return the better of (optimizer result, initial solution).
//
// This ensures we never lose progress when resuming from a checkpoint:
//   - If the optimizer finds a better solution, we use it
//   - If not, we keep the checkpoint solution
//
// Future improvement: Switch to a different optimizer library that supports
// population seeding, or fork the Mayfly library to add this feature.
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

// Run executes the Mayfly optimization using the external library
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
		return Result{}, fmt.Errorf("resume count cannot be negative")
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
	config.LowerBound = 0.0
	config.UpperBound = 1.0
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
	if options.ResumeCount > 0 {
		runSeed = continuationSeed(m.seed, options.ResumeCount)
	}
	rng := rand.New(rand.NewSource(runSeed))
	config.Rand = rng

	var runOptions []mayfly.RunOption
	best := Result{BestCost: math.Inf(1), Termination: TerminationCompleted}
	bestViolation := math.Inf(1)
	if options.Initial != nil {
		initial := Candidate{Params: append([]float64(nil), options.Initial.Params...), Cost: options.Initial.Cost}
		originalParams := append([]float64(nil), initial.Params...)
		repairCandidate(problem, initial.Params)
		if !slices.Equal(originalParams, initial.Params) {
			initial.Cost = problem.Eval(initial.Params)
		}
		if err := validateCandidate(initial, problem); err != nil {
			return Result{}, err
		}
		best.BestParams = append([]float64(nil), initial.Params...)
		best.BestCost = initial.Cost
		bestViolation = inequalityViolation(problem.Inequalities, initial.Params)
		maleSeeds, femaleSeeds := seededPopulation(normalize(initial.Params), m.popSize, rng)
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
			options.Observer(Progress{
				Iterations:  progress.Iteration,
				Evaluations: progress.EvaluationCount,
				BestParams:  append([]float64(nil), best.BestParams...),
				BestCost:    best.BestCost,
			})
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
		return fmt.Errorf("objective function is required")
	}
	if problem.Dim <= 0 || len(problem.Lower) != problem.Dim || len(problem.Upper) != problem.Dim {
		return fmt.Errorf("problem dimensions and bounds do not match")
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
		return fmt.Errorf("initial candidate has invalid dimensions or cost")
	}
	for i, value := range candidate.Params {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < problem.Lower[i] || value > problem.Upper[i] {
			return fmt.Errorf("initial candidate dimension %d is outside bounds", i)
		}
	}
	return nil
}

func seededPopulation(best []float64, population int, rng *rand.Rand) ([][]float64, [][]float64) {
	seedCount := population / 2
	if seedCount < 1 {
		seedCount = 1
	}
	makeSeeds := func(exact bool) [][]float64 {
		seeds := make([][]float64, seedCount)
		for i := range seeds {
			seeds[i] = append([]float64(nil), best...)
			if !exact || i > 0 {
				for dimension := range seeds[i] {
					seeds[i][dimension] += rng.NormFloat64() * 0.05
					seeds[i][dimension] = math.Max(0, math.Min(1, seeds[i][dimension]))
				}
			}
		}
		return seeds
	}
	return makeSeeds(true), makeSeeds(false)
}

func continuationSeed(seed int64, resumeCount int) int64 {
	value := uint64(seed) + uint64(resumeCount)*0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	return int64(value & math.MaxInt64)
}
