package renderer

import (
	"context"
	"fmt"
	"image"
	"runtime"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// The polishing budget is inherited rather than chosen: the optimizer that
// polishes an active set is built with the job-wide population, which was sized
// for the whole vector. A 512-circle run therefore polishes eight circles -- 56
// free parameters -- with a population of 200. These benchmarks measure what
// that budget actually buys, against the dimensionality of the active set
// instead of the dimensionality of the vector.
//
// The comparison metric is cost removed per second, not cost removed per sweep.
// A larger population or a longer run always removes at least as much error per
// sweep; the question is whether it removes more per second than spending the
// same wall clock differently.
//
// The axes are swept one at a time around a fixed centre rather than as a full
// grid, because a full grid at these budgets does not finish in a sitting.
// Cross terms are therefore not measured; see docs/polishing-budget-report.md.
const (
	// polishBudgetSeed is the seed every configuration shares, so the axes
	// differ only by budget.
	polishBudgetSeed = 4242
	// The centre of the sweep: the value each axis holds while another axis
	// moves. It is not the shipped configuration, and must not be read as one.
	// The population and the iteration count coincide with what
	// internal/app/config.go now ships, because this measurement is where those
	// two defaults came from; the epoch and sweep counts deliberately do not.
	// One epoch keeps an axis point costing what its budget says, and three
	// sweeps was the sweep default at the time of measurement -- the sweep
	// budget the report then argues up to the shipped eight.
	// BenchmarkPolishBudgetShippedConfiguration is the benchmark that runs
	// whole shipped configurations.
	polishBudgetDefaultIters  = 200
	polishBudgetDefaultPop    = 30
	polishBudgetDefaultEpochs = 1
	polishBudgetSweeps        = 3
)

// polishBudget is one point on an axis.
type polishBudget struct {
	population int
	iters      int
	epochs     int
	sweeps     int
}

func (p polishBudget) name() string {
	return fmt.Sprintf("pop-%d/iters-%d/epochs-%d", p.population, p.iters, p.epochs)
}

// BenchmarkPolishBudgetShape sweeps population, iterations, and epochs on the
// input polishing actually sees: the output of a real batch fit. The fixture is
// the one BenchmarkPolishStrategyQualityAfterBatchFit uses, so the two are
// directly comparable.
//
// Both the shipped default strategy (replacement) and the strategy the long
// incremental runs use (residual-region) are measured, because a default derived
// from one strategy is only a default if it also holds for the other.
func BenchmarkPolishBudgetShape(b *testing.B) {
	discardPolishBenchmarkLogs(b)

	const width, height, circleCount, batchSize = 128, 128, 64, 8
	truth := polishQualityTruth(circleCount, width, height)
	reference := polishQualityReference(truth, width, height)
	fitted, fittedCircles := fittedPolishVector(b, reference, circleCount, batchSize)

	for _, strategy := range []BatchPolishStrategy{BatchPolishWeakestReplacement, BatchPolishResidualRegion} {
		for _, budget := range polishBudgetAxes() {
			b.Run(string(strategy)+"/"+budget.name(), func(b *testing.B) {
				runPolishBudgetBenchmark(b, reference, fitted, fittedCircles,
					polishQualityActiveSetSize, strategy, budget, 1)
			})
		}
	}
}

// BenchmarkPolishBudgetProductionShape repeats the population axis at the shape
// a long incremental run polishes at: a 512x512 reference, 256 circles, and
// eight active circles, which is the 56-parameter active set the inherited
// population of 200 is spent on.
//
// The vector is the truth-plus-jitter fixture rather than a batch fit, because
// fitting 256 circles at 512x512 inside a benchmark is not practical. Rendering
// uses every thread, so a point on this axis costs a sweep of real evaluations
// rather than a serial one.
func BenchmarkPolishBudgetProductionShape(b *testing.B) {
	discardPolishBenchmarkLogs(b)

	const width, height, circleCount, activeSetSize = 512, 512, 256, 8
	truth := polishQualityTruth(circleCount, width, height)
	reference := polishQualityReference(truth, width, height)
	initial := polishQualityStart(truth, width, height)
	threads := runtime.GOMAXPROCS(0)
	b.Logf("GOMAXPROCS=%d render threads per candidate", threads)

	for _, population := range polishBudgetPopulations() {
		budget := polishBudget{
			population: population,
			iters:      polishBudgetDefaultIters,
			epochs:     polishBudgetDefaultEpochs,
			sweeps:     2,
		}
		b.Run(budget.name(), func(b *testing.B) {
			runPolishBudgetBenchmark(b, reference, initial, circleCount,
				activeSetSize, BatchPolishResidualRegion, budget, threads)
		})
	}
}

// BenchmarkPolishBudgetSweepFalloff reports what each individual sweep removed,
// which is the axis PolishingMaxSweeps is set on. The live run this task quotes
// dropped from 85.5 cost units in sweep one to 16.8 in sweep two; this measures
// the same curve on a fixture, out to twice the shipped budget.
//
// Per-sweep costs come from the BatchPolishOptions observer rather than from the
// log, so the measurement does not depend on log formatting.
func BenchmarkPolishBudgetSweepFalloff(b *testing.B) {
	discardPolishBenchmarkLogs(b)

	const width, height, circleCount, batchSize, sweeps = 128, 128, 64, 8, 8
	truth := polishQualityTruth(circleCount, width, height)
	reference := polishQualityReference(truth, width, height)
	fitted, fittedCircles := fittedPolishVector(b, reference, circleCount, batchSize)

	for _, strategy := range []BatchPolishStrategy{BatchPolishWeakestReplacement, BatchPolishResidualRegion} {
		b.Run(string(strategy), func(b *testing.B) {
			costRenderer := NewCPURenderer(reference, fittedCircles)
			costRenderer.SetThreads(1)
			startCost := costRenderer.Cost(fitted)

			removedPerSweep := make([]float64, sweeps)

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				previous := startCost

				result, err := polishAtBudget(reference, fitted, fittedCircles,
					polishQualityActiveSetSize, strategy,
					polishBudget{
						population: polishBudgetDefaultPop,
						iters:      polishBudgetDefaultIters,
						epochs:     polishBudgetDefaultEpochs,
						sweeps:     sweeps,
					}, 1,
					func(progress BatchPolishProgress) error {
						if progress.Sweep >= 1 && progress.Sweep <= sweeps {
							removedPerSweep[progress.Sweep-1] += previous - progress.BestCost
						}

						previous = progress.BestCost

						return nil
					})
				if err != nil {
					b.Fatalf("PolishCircleBatchContext(%s) error = %v", strategy, err)
				}

				polishStrategyBenchmarkSink += result.BestCost
			}

			for sweep, removed := range removedPerSweep {
				b.ReportMetric(removed/float64(b.N), fmt.Sprintf("sweep%d_removed", sweep+1))
			}
		})
	}
}

// BenchmarkPolishBudgetShippedConfiguration compares whole configurations
// rather than axes, and unlike the axis benchmarks it carries the early stopping
// a real run configures (PolishingStagnationIters, PolishingMinImprovement).
// That matters for the iteration axis: a 1000-iteration epoch that stagnates at
// 500 does not cost what 1000 iterations cost, so the axis figures alone cannot
// justify changing PolishingIters.
//
// The three configurations are the shipped defaults, the same defaults as a
// large run used to inherit them (population 200, which is what a 512-circle
// job's popSize dragged into polishing), and the budget the axes recommend:
// the same population, a shorter epoch, and the sweeps that buys.
func BenchmarkPolishBudgetShippedConfiguration(b *testing.B) {
	discardPolishBenchmarkLogs(b)

	const width, height, circleCount, batchSize = 128, 128, 64, 8
	truth := polishQualityTruth(circleCount, width, height)
	reference := polishQualityReference(truth, width, height)
	fitted, fittedCircles := fittedPolishVector(b, reference, circleCount, batchSize)

	configurations := []struct {
		name string
		// stagnation is the configuration's own PolishingStagnationIters, which
		// is half its epoch in every default this project has shipped. It has to
		// travel with the budget: a stopping rule copied from a longer epoch
		// never fires inside a shorter one, and a row measured that way would not
		// be the row that ships.
		stagnation int
		budget     polishBudget
		strategies []BatchPolishStrategy
	}{
		{
			name:       "previous-defaults",
			stagnation: 500,
			budget:     polishBudget{population: 30, iters: 1000, epochs: 2, sweeps: 3},
			strategies: []BatchPolishStrategy{BatchPolishWeakestReplacement, BatchPolishResidualRegion},
		},
		{
			name:       "inherited-population",
			stagnation: 500,
			budget:     polishBudget{population: 200, iters: 1000, epochs: 2, sweeps: 3},
			strategies: []BatchPolishStrategy{BatchPolishResidualRegion},
		},
		{
			name:       "current-defaults",
			stagnation: 100,
			budget:     polishBudget{population: 30, iters: 200, epochs: 2, sweeps: 8},
			strategies: []BatchPolishStrategy{BatchPolishWeakestReplacement, BatchPolishResidualRegion},
		},
	}
	for _, configuration := range configurations {
		for _, strategy := range configuration.strategies {
			b.Run(configuration.name+"/"+string(strategy), func(b *testing.B) {
				runPolishBudgetBenchmark(b, reference, fitted, fittedCircles,
					polishQualityActiveSetSize, strategy, configuration.budget, 1,
					polishBudgetEarlyStop(configuration.stagnation)...)
			})
		}
	}
}

// polishBudgetEarlyStop is the stopping rule a configuration ships:
// PolishingStagnationIters against PolishingMinImprovement 0.001.
func polishBudgetEarlyStop(stagnationIters int) []opt.MayflyOption {
	return []opt.MayflyOption{opt.WithEarlyStop(opt.Stop{
		MinImprovement:  0.001,
		StagnationIters: stagnationIters,
	})}
}

// polishBudgetAxes is the one-at-a-time sweep: each axis moves through its range
// while the other two hold the shipped default.
func polishBudgetAxes() []polishBudget {
	var budgets []polishBudget
	for _, population := range polishBudgetPopulations() {
		budgets = append(budgets, polishBudget{
			population: population,
			iters:      polishBudgetDefaultIters,
			epochs:     polishBudgetDefaultEpochs,
			sweeps:     polishBudgetSweeps,
		})
	}

	for _, iters := range []int{50, 100, 400, 800} {
		budgets = append(budgets, polishBudget{
			population: polishBudgetDefaultPop,
			iters:      iters,
			epochs:     polishBudgetDefaultEpochs,
			sweeps:     polishBudgetSweeps,
		})
	}

	for _, epochs := range []int{2, 4} {
		budgets = append(budgets, polishBudget{
			population: polishBudgetDefaultPop,
			iters:      polishBudgetDefaultIters,
			epochs:     epochs,
			sweeps:     polishBudgetSweeps,
		})
	}

	return budgets
}

// polishBudgetPopulations starts at app.MinPopulation, which is the floor a
// configuration is allowed to request, and ends at app.MaxPopulation, which is
// what a large incremental run inherits today. The renderer package cannot
// import internal/app, so the two ends are repeated here rather than referenced.
func polishBudgetPopulations() []int {
	return []int{20, 30, 50, 100, 200}
}

// runPolishBudgetBenchmark times one budget and reports what it bought. The
// metrics are per run rather than per b.N, matching runPolishQualityBenchmark,
// so they stay comparable across -benchtime values while ns/op does not.
func runPolishBudgetBenchmark(
	b *testing.B,
	reference *image.NRGBA,
	initial []float64,
	circleCount, activeSetSize int,
	strategy BatchPolishStrategy,
	budget polishBudget,
	threads int,
	options ...opt.MayflyOption,
) {
	b.Helper()

	costRenderer := NewCPURenderer(reference, circleCount)
	costRenderer.SetThreads(threads)
	startCost := costRenderer.Cost(initial)

	var costSum float64
	var acceptedSum, evaluationSum int

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		result, err := polishAtBudget(reference, initial, circleCount, activeSetSize, strategy, budget, threads, nil, options...)
		if err != nil {
			b.Fatalf("PolishCircleBatchContext(%s, %s) error = %v", strategy, budget.name(), err)
		}

		costSum += result.BestCost
		acceptedSum += result.AcceptedSweeps
		evaluationSum += result.Evaluations
		polishStrategyBenchmarkSink += result.BestCost
	}

	b.StopTimer()

	finalCost := costSum / float64(b.N)
	removed := startCost - finalCost
	seconds := b.Elapsed().Seconds() / float64(b.N)
	b.ReportMetric(finalCost, "final_cost")
	b.ReportMetric(removed/startCost*100, "reduction_pct")
	b.ReportMetric(float64(acceptedSum)/float64(b.N), "accepted_sweeps")
	b.ReportMetric(float64(evaluationSum)/float64(b.N), "evaluations")
	// The comparison metric: a budget is worth having only if it removes more
	// error for the wall clock it costs than a smaller one does.
	b.ReportMetric(removed/seconds, "removed_per_s")
}

// polishAtBudget runs one polish at the given budget, with a fresh renderer and
// a fresh optimizer so no state carries between runs.
func polishAtBudget(
	reference *image.NRGBA,
	initial []float64,
	circleCount, activeSetSize int,
	strategy BatchPolishStrategy,
	budget polishBudget,
	threads int,
	onSweep func(BatchPolishProgress) error,
	options ...opt.MayflyOption,
) (*BatchPolishResult, error) {
	base := NewCPURenderer(reference, circleCount)
	base.SetThreads(threads)

	optimizer, err := opt.NewMayflyVariant("standard", budget.iters, budget.population, polishBudgetSeed, options...)
	if err != nil {
		return nil, err
	}

	if budget.epochs > 1 {
		optimizer = opt.WithEpochs(optimizer, budget.epochs)
	}

	return PolishCircleBatchContext(context.Background(), base, optimizer,
		append([]float64(nil), initial...), BatchPolishOptions{
			ActiveSetSize: activeSetSize,
			MaxSweeps:     budget.sweeps,
			Strategy:      strategy,
			OnSweep:       onSweep,
		})
}

// fittedPolishVector runs the batch fit whose output polishing sees in
// production. It is shared with BenchmarkPolishStrategyQualityAfterBatchFit so
// both report against the same starting vector.
func fittedPolishVector(b *testing.B, reference *image.NRGBA, circleCount, batchSize int) ([]float64, int) {
	b.Helper()

	fitOptimizer, err := opt.NewMayflyVariant("standard", 400, 30, polishBudgetSeed)
	if err != nil {
		b.Fatalf("NewMayflyVariant() error = %v", err)
	}

	fitRenderer := NewCPURenderer(reference, circleCount)
	fitRenderer.SetThreads(1)

	fitted, err := OptimizeBatch(fitRenderer, fitOptimizer, circleCount, batchSize, DisabledConvergenceConfig())
	if err != nil {
		b.Fatalf("OptimizeBatch() error = %v", err)
	}

	return fitted.BestParams, len(fitted.BestParams) / paramsPerCircle
}
