package renderer

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"math/rand"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// polishStrategyBenchmarkSink retains benchmark results so the optimizer work
// cannot be eliminated, matching the convention the SSD kernel benchmarks use.
var polishStrategyBenchmarkSink float64

const (
	// A real run polishes with 1000 iterations at population 30 over two epochs.
	// That is too slow to sweep four strategies over several sweep budgets, so
	// the budget is scaled down uniformly. Every strategy gets the identical
	// budget, which is what makes the comparison fair; the absolute costs are
	// not comparable to a default-configuration run.
	polishQualityIters   = 60
	polishQualityPopSize = 20
	// The shipped default for --polishing-active-set-size.
	polishQualityActiveSetSize = 5
)

// polishStrategies is the full set a user can select.
var polishStrategies = []BatchPolishStrategy{
	BatchPolishWeakestReplacement,
	BatchPolishHybridOverlap,
	BatchPolishResidualRegion,
	BatchPolishContiguousWindow,
}

// BenchmarkPolishStrategyQuality answers the question BenchmarkPolishCircleBatchStrategy
// cannot. That benchmark drives a recording stub, so it measures rendering and
// nothing else. contiguous-window is cheaper per sweep, but it selects by draw
// position instead of image-space merit, and a cheaper sweep is only worth
// having if it still removes error.
//
// Every configuration here runs the real MayFly optimizer against the same
// reference, the same starting vector, and the same per-sweep optimizer budget,
// and reports the cost it reached and the sweeps it got accepted alongside the
// wall clock it spent. Compare strategies at equal wall clock rather than at
// equal sweeps: a strategy that halves the cost of a sweep can afford twice as
// many of them.
//
// The reference is rendered from a known circle vector and the starting vector
// is that truth jittered by 2%, so the error is genuinely recoverable. Sweep
// budgets are the historical three-sweep default, the
// ceil(circles/activeSetSize) budget
// at which contiguous-window has offered every draw slot to the optimizer once,
// and a longer budget that costs contiguous-window roughly what 13 sweeps cost
// the merit-based strategies.
//
// These are wall-clock comparisons on one machine. Record the machine with any
// result and do not treat the numbers as portable constants. See
// docs/contiguous-window-polish-report.md for a recorded run and its reading.
func BenchmarkPolishStrategyQuality(b *testing.B) {
	discardPolishBenchmarkLogs(b)

	const width, height, circleCount = 128, 128, 64
	truth := polishQualityTruth(circleCount, width, height)
	reference := polishQualityReference(truth, width, height)
	initial := polishQualityStart(truth, width, height)

	for _, sweeps := range []int{3, 13, 19} {
		for _, strategy := range polishStrategies {
			b.Run(fmt.Sprintf("64circles/sweeps=%d/%s", sweeps, strategy), func(b *testing.B) {
				runPolishQualityBenchmark(b, reference, initial, circleCount, sweeps, strategy)
			})
		}
	}
}

// BenchmarkPolishStrategyQualityAfterBatchFit polishes the output of a real
// batch fit rather than a synthetic vector, because that is the only input
// PolishCircleBatchContext ever sees in production. The fit runs once, outside
// the timer.
//
// The distinction matters. A fitted vector can contain circles whose
// MSEContribution has gone negative, because PruneCircleBatch runs per stage
// against that stage's canvas while the polishing accept gate is global over the
// final vector. Under the old absolute gate a single such circle blocked every
// sweep until some active set repaired it, which dominated any difference
// between the strategies. sweepKeepsCirclesUseful now excuses a circle the sweep
// neither touched nor worsened, so the strategies are comparable on this input
// again -- but the numbers in docs/contiguous-window-polish-report.md were taken
// under the old gate and are not a prediction of what this benchmark reports
// today.
func BenchmarkPolishStrategyQualityAfterBatchFit(b *testing.B) {
	discardPolishBenchmarkLogs(b)

	const width, height, circleCount, batchSize = 128, 128, 64, 8
	truth := polishQualityTruth(circleCount, width, height)
	reference := polishQualityReference(truth, width, height)
	fitted, fittedCircles := fittedPolishVector(b, reference, circleCount, batchSize)

	for _, sweeps := range []int{3, 13} {
		for _, strategy := range polishStrategies {
			b.Run(fmt.Sprintf("fitted/sweeps=%d/%s", sweeps, strategy), func(b *testing.B) {
				runPolishQualityBenchmark(b, reference, fitted, fittedCircles, sweeps, strategy)
			})
		}
	}
}

// runPolishQualityBenchmark times one polishing configuration and reports what
// it achieved, not only what it cost.
func runPolishQualityBenchmark(
	b *testing.B,
	reference *image.NRGBA,
	initial []float64,
	circleCount, sweeps int,
	strategy BatchPolishStrategy,
) {
	b.Helper()

	costRenderer := NewCPURenderer(reference, circleCount)
	costRenderer.SetThreads(1)
	startCost := costRenderer.Cost(initial)

	var costSum float64
	var acceptedSum int
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		base := NewCPURenderer(reference, circleCount)
		// One render thread keeps the measurement about selection and baking
		// rather than about row-band scaling.
		base.SetThreads(1)
		optimizer, err := opt.NewMayflyVariant("standard", polishQualityIters, polishQualityPopSize, 4242)
		if err != nil {
			b.Fatalf("NewMayflyVariant() error = %v", err)
		}
		result, err := PolishCircleBatchContext(context.Background(), base, optimizer,
			append([]float64(nil), initial...), BatchPolishOptions{
				ActiveSetSize: polishQualityActiveSetSize,
				MaxSweeps:     sweeps,
				Strategy:      strategy,
			})
		if err != nil {
			b.Fatalf("PolishCircleBatchContext(%s) error = %v", strategy, err)
		}
		costSum += result.BestCost
		acceptedSum += result.AcceptedSweeps
		polishStrategyBenchmarkSink += result.BestCost
	}
	// These are per run rather than per b.N, so they stay comparable across
	// -benchtime values. reduction_pct is the share of the starting error the
	// strategy removed; accepted_sweeps is how many sweeps survived
	// sweepKeepsCirclesUseful, and a zero there means the run was a no-op that
	// still spent its whole optimizer budget.
	finalCost := costSum / float64(b.N)
	b.ReportMetric(finalCost, "final_cost")
	b.ReportMetric((startCost-finalCost)/startCost*100, "reduction_pct")
	b.ReportMetric(float64(acceptedSum)/float64(b.N), "accepted_sweeps")
}

// discardPolishBenchmarkLogs silences the one info record the pipeline emits per
// optimizer run, which would otherwise dominate both the output and the
// measurement.
func discardPolishBenchmarkLogs(b *testing.B) {
	b.Helper()
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(previous) })
}

// polishQualityTruth builds a deterministic vector of overlapping translucent
// circles, so later draw slots genuinely occlude earlier ones while every
// circle still contributes.
func polishQualityTruth(circleCount, width, height int) []float64 {
	random := rand.New(rand.NewSource(7))
	params := make([]float64, 0, circleCount*paramsPerCircle)
	for range circleCount {
		params = append(params, circleParams(
			random.Float64()*float64(width),
			random.Float64()*float64(height),
			4+random.Float64()*float64(width)/12,
			color.NRGBA{
				R: uint8(random.Intn(256)),
				G: uint8(random.Intn(256)),
				B: uint8(random.Intn(256)),
				A: 255,
			},
			0.25+random.Float64()*0.3,
		)...)
	}
	return params
}

// polishQualityReference renders the truth vector, so the starting error is
// error the optimizer can actually remove.
func polishQualityReference(truth []float64, width, height int) *image.NRGBA {
	circleCount := len(truth) / paramsPerCircle
	source := NewCPURenderer(solidImage(width, height, color.NRGBA{R: 255, G: 255, B: 255, A: 255}), circleCount)
	source.SetThreads(1)
	return cloneNRGBA(source.Render(truth))
}

// polishQualityStart jitters every circle by 2% of the frame. That is mild
// enough to leave the vector recognizable and still drives one circle's
// MSEContribution negative, which is the situation the accept gate keys on.
func polishQualityStart(truth []float64, width, height int) []float64 {
	const jitter = 0.02
	random := rand.New(rand.NewSource(11))
	start := append([]float64(nil), truth...)
	for circle := range len(truth) / paramsPerCircle {
		offset := circle * paramsPerCircle
		start[offset] += (random.Float64()*2 - 1) * jitter * float64(width)
		start[offset+1] += (random.Float64()*2 - 1) * jitter * float64(height)
		start[offset+2] *= 1 + (random.Float64()*2-1)*jitter
		for channel := 3; channel < paramsPerCircle; channel++ {
			start[offset+channel] += (random.Float64()*2 - 1) * jitter
		}
	}
	// Keep the jittered vector inside the bounds the optimizer enforces, so the
	// starting cost is one a real run could hold.
	fit.NewBounds(len(start)/paramsPerCircle, width, height).ClampVector(start)
	return start
}
