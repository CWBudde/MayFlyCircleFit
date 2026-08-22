package renderer

import (
	"image"
	"image/color"
	"log/slog"
	"math"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

type pipelineBenchmarkOptimizer struct {
	evaluations int
	radiusScale float64
}

func (o pipelineBenchmarkOptimizer) Run(eval func([]float64) float64, lower, upper []float64, _ int) ([]float64, float64) {
	bestCost := math.Inf(1)
	var best []float64

	for i := range o.evaluations {
		params := incrementalValidationCandidate(lower, upper, i)
		for offset := 0; offset < len(params); offset += paramsPerCircle {
			params[offset+2] = max(lower[offset+2], upper[offset+2]*o.radiusScale)
		}

		cost := eval(params)
		if cost < bestCost {
			bestCost = cost

			best = append(best[:0], params...)
		}
	}

	return best, bestCost
}

func benchmarkPipelineReference(width, height int) *image.NRGBA {
	ref := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			ref.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 255 / width),
				G: uint8(y * 255 / height),
				B: uint8((x + y) * 255 / (width + height)),
				A: 255,
			})
		}
	}

	return ref
}

func silencePipelineBenchmarkLogs(b *testing.B) {
	b.Helper()

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.DiscardHandler))
	b.Cleanup(func() { slog.SetDefault(previous) })
}

// BenchmarkOptimizeSequentialPipeline measures the complete staged pipeline,
// including renderer sessions, objective evaluations, retained results, and
// final replay. ReportAllocs makes parameter-vector regressions visible.
func BenchmarkOptimizeSequentialPipeline(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	ref := benchmarkPipelineReference(64, 64)

	for _, radius := range []float64{0.08, 0.15, 0.25} {
		optimizer := pipelineBenchmarkOptimizer{evaluations: 8, radiusScale: radius}
		dirtyPercent := benchmarkPipelineDirtyPercent(ref, 1, optimizer)

		b.Run(radiusName(radius), func(b *testing.B) {
			for _, test := range []struct {
				name        string
				incremental bool
			}{
				{name: "full_image"},
				{name: "incremental", incremental: true},
			} {
				b.Run(test.name, func(b *testing.B) {
					base := NewCPURenderer(ref, 12)
					base.stagedIncremental = test.incremental

					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						result, err := OptimizeSequential(base, optimizer, 12, DisabledConvergenceConfig(), nil)
						if err != nil {
							b.Fatal(err)
						}

						b.ReportMetric(float64(result.Evaluations), "evaluations/pipeline")
					}

					b.ReportMetric(dirtyPercent, "%dirty")
				})
			}
		})
	}
}

// BenchmarkOptimizeBatchPipeline measures an end-to-end multi-stage batch run
// whose final stage is partial, exercising exact remainder handling as well as
// accumulated parameter and image construction.
func BenchmarkOptimizeBatchPipeline(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	ref := benchmarkPipelineReference(64, 64)

	for _, radius := range []float64{0.04, 0.08, 0.15} {
		optimizer := pipelineBenchmarkOptimizer{evaluations: 8, radiusScale: radius}
		dirtyPercent := benchmarkPipelineDirtyPercent(ref, 5, optimizer)

		b.Run(radiusName(radius), func(b *testing.B) {
			for _, test := range []struct {
				name        string
				incremental bool
			}{
				{name: "full_image"},
				{name: "incremental", incremental: true},
			} {
				b.Run(test.name, func(b *testing.B) {
					base := NewCPURenderer(ref, 18)
					base.stagedIncremental = test.incremental

					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						result, err := OptimizeBatch(base, optimizer, 18, 5, DisabledConvergenceConfig())
						if err != nil {
							b.Fatal(err)
						}

						b.ReportMetric(float64(result.Evaluations), "evaluations/pipeline")
					}

					b.ReportMetric(dirtyPercent, "%dirty")
				})
			}
		})
	}
}

// BenchmarkOptimizeBatchPipeline256 covers the large-canvas K5 sessions that
// remain eligible for production incremental dispatch.
func BenchmarkOptimizeBatchPipeline256(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	ref := benchmarkPipelineReference(256, 256)

	for _, radius := range []float64{0.02, 0.04, 0.08} {
		optimizer := pipelineBenchmarkOptimizer{evaluations: 8, radiusScale: radius}
		dirtyPercent := benchmarkPipelineDirtyPercent(ref, 5, optimizer)

		b.Run(radiusName(radius), func(b *testing.B) {
			for _, test := range []struct {
				name        string
				incremental bool
			}{
				{name: "full_image"},
				{name: "incremental", incremental: true},
			} {
				b.Run(test.name, func(b *testing.B) {
					base := NewCPURenderer(ref, 18)
					base.SetThreads(1)
					base.stagedIncremental = test.incremental

					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						result, err := OptimizeBatch(base, optimizer, 18, 5, DisabledConvergenceConfig())
						if err != nil {
							b.Fatal(err)
						}

						b.ReportMetric(float64(result.Evaluations), "evaluations/pipeline")
					}

					b.ReportMetric(dirtyPercent, "%dirty")
				})
			}
		})
	}
}

// BenchmarkOptimizeJointPipeline is the production fallback control: joint
// sessions intentionally use full-image SSD even when staged incremental mode
// is enabled on the base renderer.
func BenchmarkOptimizeJointPipeline(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	ref := benchmarkPipelineReference(64, 64)
	optimizer := pipelineBenchmarkOptimizer{evaluations: 8, radiusScale: 0.15}
	dirtyPercent := benchmarkPipelineDirtyPercent(ref, 12, optimizer)

	for _, test := range []struct {
		name        string
		incremental bool
	}{
		{name: "full_image"},
		{name: "production", incremental: true},
	} {
		b.Run(test.name, func(b *testing.B) {
			base := NewCPURenderer(ref, 12)
			base.stagedIncremental = test.incremental

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				result, err := OptimizeJoint(base, optimizer, 12, DisabledConvergenceConfig())
				if err != nil {
					b.Fatal(err)
				}

				b.ReportMetric(float64(result.Evaluations), "evaluations/pipeline")
			}

			b.ReportMetric(dirtyPercent, "%dirty")
		})
	}
}

// BenchmarkMayflyIncrementalPipelines checks the production policy with the
// repository's seeded optimizer. The ratio-controlled benchmarks above isolate
// crossover behavior; this benchmark includes genuine population evolution.
func BenchmarkMayflyIncrementalPipelines(b *testing.B) {
	silencePipelineBenchmarkLogs(b)
	const totalCircles = 6
	ref := benchmarkPipelineReference(64, 64)
	tests := []struct {
		name string
		run  func(Renderer, opt.Optimizer) (*OptimizationResult, error)
	}{
		{name: "joint", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeJoint(r, optimizer, totalCircles, DisabledConvergenceConfig())
		}},
		{name: "sequential", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeSequential(r, optimizer, totalCircles, DisabledConvergenceConfig(), nil)
		}},
		{name: "batch", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeBatch(r, optimizer, totalCircles, 2, DisabledConvergenceConfig())
		}},
	}

	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			for _, policy := range []struct {
				name        string
				incremental bool
			}{
				{name: "full_image"},
				{name: "production", incremental: true},
			} {
				b.Run(policy.name, func(b *testing.B) {
					base := NewCPURenderer(ref, totalCircles)
					base.SetThreads(1)
					base.stagedIncremental = policy.incremental

					b.ReportAllocs()
					b.ResetTimer()

					for iteration := range b.N {
						optimizer := opt.NewMayfly(2, 10, int64(10_160+iteration))

						result, err := test.run(base, optimizer)
						if err != nil {
							b.Fatal(err)
						}

						b.ReportMetric(float64(result.Evaluations), "evaluations/pipeline")
					}
				})
			}
		})
	}
}

func radiusName(radius float64) string {
	return "radius_" + fmtInt(int(math.Round(radius*100))) + "pct"
}

func benchmarkPipelineDirtyPercent(reference *image.NRGBA, circles int, optimizer pipelineBenchmarkOptimizer) float64 {
	renderer := NewCPURenderer(reference, circles)
	renderer.SetThreads(1)
	lower, upper := renderer.Bounds()
	totalDirty := 0

	for candidate := range optimizer.evaluations {
		params := incrementalValidationCandidate(lower, upper, candidate)
		for offset := 0; offset < len(params); offset += paramsPerCircle {
			params[offset+2] = max(lower[offset+2], upper[offset+2]*optimizer.radiusScale)
		}

		renderer.render(params, &renderer.dirtySpans)
		dirtyPixels, _ := renderer.dirtySpans.metrics()
		totalDirty += dirtyPixels
	}

	return 100 * float64(totalDirty) / float64(optimizer.evaluations*reference.Bounds().Dx()*reference.Bounds().Dy())
}
