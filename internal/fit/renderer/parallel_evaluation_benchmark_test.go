package renderer

import (
	"fmt"
	"log/slog"
	"runtime"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// parallelEvaluationBenchmarkSink retains benchmark results so the optimizer
// work cannot be eliminated, matching the convention the SSD kernel benchmarks
// use.
var parallelEvaluationBenchmarkSink float64

// BenchmarkParallelEvaluationScaling measures what --parallel-evaluation is for:
// optimization throughput as the number of concurrent cost evaluations rises.
//
// The baseline is the default configuration rather than a one-thread renderer,
// because the default is what a user gives up by setting the flag. Row-band
// sharding already spends every core on a single render, so the honest question
// is not "is concurrency faster than none" but "is one render per core faster
// than one render split across cores". The two strategies compete for the same
// cores and cannot be added together, which is why each pooled session renders
// single-threaded.
//
// Keep the workload fixed when recording results, and record the machine: these
// are wall-clock comparisons, not portable constants.
func BenchmarkParallelEvaluationScaling(b *testing.B) {
	// The pipeline logs one record per optimizer run at info level, which would
	// otherwise dominate the benchmark output and the measurement itself.
	previous := slog.Default()

	slog.SetDefault(slog.New(slog.DiscardHandler))
	b.Cleanup(func() { slog.SetDefault(previous) })

	maxWorkers := runtime.GOMAXPROCS(0)

	for _, workload := range []struct {
		name          string
		width, height int
		circles       int
		iters         int
		popSize       int
	}{
		{name: "128x128_20circles", width: 128, height: 128, circles: 20, iters: 10, popSize: 20},
		{name: "512x512_60circles", width: 512, height: 512, circles: 60, iters: 10, popSize: 20},
	} {
		ref := randomNRGBA(workload.width, workload.height, 42)

		// The default path: every core shards the rows of one render, and
		// evaluations run one at a time.
		b.Run(workload.name+"/serial", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				base := NewCPURenderer(ref, workload.circles)
				base.SetThreads(maxWorkers)
				parallelEvaluationBenchmarkSink += runJointBenchmark(b, base, workload.circles, workload.iters, workload.popSize)
			}
		})

		for _, workers := range benchmarkWorkerCounts(maxWorkers) {
			b.Run(workload.name+fmt.Sprintf("/eval=%d", workers), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					base := NewCPURenderer(ref, workload.circles)
					ConfigureCPUParallelism(base, maxWorkers, workers, true)
					parallelEvaluationBenchmarkSink += runJointBenchmark(b, base, workload.circles, workload.iters, workload.popSize)
				}
			})
		}
	}
}

// benchmarkWorkerCounts returns the evaluation widths worth measuring on this
// machine: the powers of two up to GOMAXPROCS, plus GOMAXPROCS itself.
func benchmarkWorkerCounts(maxWorkers int) []int {
	counts := []int{}
	for workers := 2; workers < maxWorkers; workers *= 2 {
		counts = append(counts, workers)
	}

	if maxWorkers > 1 {
		counts = append(counts, maxWorkers)
	}

	return counts
}

// runJointBenchmark drives one complete joint optimization, including the
// optimizer, so the measurement covers the evaluation pool in the arrangement a
// real run uses rather than a synthetic loop over Cost.
func runJointBenchmark(b *testing.B, base *CPURenderer, circles, iters, popSize int) float64 {
	b.Helper()

	options := []opt.MayflyOption{}
	if option, enabled := ParallelEvaluationOption(base, true); enabled {
		options = append(options, option)
	}

	optimizer, err := opt.NewMayflyVariant("standard", iters, popSize, 4242, options...)
	if err != nil {
		b.Fatalf("NewMayflyVariant() error = %v", err)
	}

	result, err := OptimizeJoint(base, optimizer, circles, DisabledConvergenceConfig())
	if err != nil {
		b.Fatalf("OptimizeJoint() error = %v", err)
	}

	return result.BestCost
}
