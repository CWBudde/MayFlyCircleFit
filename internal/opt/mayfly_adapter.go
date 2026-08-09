package opt

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"

	"github.com/cwbudde/mayfly"
)

// MayflyAdapter wraps the external Mayfly library to conform to our Optimizer interface
type MayflyAdapter struct {
	maxIters int
	popSize  int
	seed     int64
	variant  string // "standard", "desma", "olce", "eobbma", "gsasma", "mpma", "aoblmoa"
}

// NewMayfly creates a new Mayfly optimizer adapter
func NewMayfly(maxIters, popSize int, seed int64) Optimizer {
	return &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
		variant:  "standard",
	}
}

// NewMayflyDESMA creates a Mayfly optimizer using the DESMA variant
func NewMayflyDESMA(maxIters, popSize int, seed int64) Optimizer {
	return &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
		variant:  "desma",
	}
}

// NewMayflyOLCE creates a Mayfly optimizer using the OLCE-MA variant
func NewMayflyOLCE(maxIters, popSize int, seed int64) Optimizer {
	return &MayflyAdapter{
		maxIters: maxIters,
		popSize:  popSize,
		seed:     seed,
		variant:  "olce",
	}
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

	// Wrap eval function to handle normalization
	normalizedEval := func(normalizedParams []float64) float64 {
		denormalizedParams := denormalize(normalizedParams)
		return problem.Eval(denormalizedParams)
	}

	config.ObjectiveFunc = normalizedEval
	config.ProblemSize = problem.Dim
	config.MaxIterations = m.maxIters
	config.NPop = m.popSize
	config.NPopF = m.popSize
	config.LowerBound = 0.0
	config.UpperBound = 1.0

	// Create RNG for population initialization
	runSeed := m.seed
	if options.ResumeCount > 0 {
		runSeed = continuationSeed(m.seed, options.ResumeCount)
	}
	rng := rand.New(rand.NewSource(runSeed))
	config.Rand = rng

	var runOptions []mayfly.RunOption
	best := Result{BestCost: math.Inf(1), Termination: TerminationCompleted}
	if options.Initial != nil {
		if err := validateCandidate(*options.Initial, problem); err != nil {
			return Result{}, err
		}
		best.BestParams = append([]float64(nil), options.Initial.Params...)
		best.BestCost = options.Initial.Cost
		maleSeeds, femaleSeeds := seededPopulation(normalize(options.Initial.Params), m.popSize, rng)
		runOptions = append(runOptions, mayfly.WithInitialPopulation(maleSeeds, femaleSeeds))
	}
	runOptions = append(runOptions, mayfly.WithProgressObserver(func(progress mayfly.Progress) {
		params := denormalize(progress.Best.Position)
		if progress.Best.Cost < best.BestCost {
			best.BestParams = append([]float64(nil), params...)
			best.BestCost = progress.Best.Cost
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
	if result.GlobalBest.Cost < best.BestCost {
		best.BestParams = params
		best.BestCost = result.GlobalBest.Cost
	}
	best.Iterations = len(result.ConvergenceCurve)
	best.Evaluations = result.FuncEvalCount
	return best, nil
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
