//go:build gpu

package renderer

import (
	"image"
	"math"
	"strconv"
	"testing"
)

const pipelineComparisonEvaluations = 8

// pipelineComparisonOptimizer submits a different in-bounds parameter vector
// on every evaluation. That prevents renderer hash caches from turning the
// backend comparison into a cached-call benchmark.
type pipelineComparisonOptimizer struct {
	evaluations int
	nonce       uint64
}

func (o *pipelineComparisonOptimizer) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	params := make([]float64, dim)
	for offset := 0; offset < dim; offset += paramsPerCircle {
		params[offset] = lower[offset] + (upper[offset]-lower[offset])*0.5
		params[offset+1] = lower[offset+1] + (upper[offset+1]-lower[offset+1])*0.5
		params[offset+2] = lower[offset+2] + (upper[offset+2]-lower[offset+2])*0.15
		params[offset+3] = 0.2
		params[offset+4] = 0.4
		params[offset+5] = 0.8
		params[offset+6] = 0.5
	}

	bestCost := math.Inf(1)
	for range o.evaluations {
		o.nonce++
		fraction := 0.45 + float64(o.nonce%17)/170
		params[0] = lower[0] + (upper[0]-lower[0])*fraction
		bestCost = eval(params)
	}
	return params, bestCost
}

// BenchmarkOptimizePipelineBackends compares complete pipeline executions.
// Renderer construction for the base is excluded, while per-stage session
// creation, objective evaluation, retained-state handling, and final image
// materialization are included.
func BenchmarkOptimizePipelineBackends(b *testing.B) {
	silencePipelineBenchmarkLogs(b)
	ref := benchmarkPipelineReference(64, 64)

	benchmarks := []struct {
		name    string
		circles int
		run     func(Renderer, *pipelineComparisonOptimizer) (*OptimizationResult, error)
	}{
		{
			name:    "joint",
			circles: 12,
			run: func(r Renderer, optimizer *pipelineComparisonOptimizer) (*OptimizationResult, error) {
				return OptimizeJoint(r, optimizer, 12, DisabledConvergenceConfig())
			},
		},
		{
			name:    "sequential",
			circles: 12,
			run: func(r Renderer, optimizer *pipelineComparisonOptimizer) (*OptimizationResult, error) {
				return OptimizeSequential(r, optimizer, 12, DisabledConvergenceConfig(), nil)
			},
		},
		{
			name:    "batch",
			circles: 12,
			run: func(r Renderer, optimizer *pipelineComparisonOptimizer) (*OptimizationResult, error) {
				return OptimizeBatch(r, optimizer, 12, 4, DisabledConvergenceConfig())
			},
		},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			for _, backend := range []string{"cpu", "opencl"} {
				b.Run(backend, func(b *testing.B) {
					r, cleanup, err := NewRendererForBackend(backend, ref, benchmark.circles)
					if err != nil {
						b.Skipf("%s backend unavailable: %v", backend, err)
					}
					defer cleanup()

					if adapter, ok := r.(openCLAdapter); ok {
						runtime := adapter.Runtime()
						b.Logf(
							"OpenCL platform=%q device=%q type=%s compute_units=%d",
							runtime.Platform.Name,
							runtime.Device.Name,
							runtime.Device.Type,
							runtime.Device.MaxComputeUnits,
						)
					}

					optimizer := &pipelineComparisonOptimizer{evaluations: pipelineComparisonEvaluations}
					var result *OptimizationResult
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						result, err = benchmark.run(r, optimizer)
						if err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					if result != nil {
						b.ReportMetric(float64(result.Evaluations), "evaluations/pipeline")
					}
				})
			}
		})
	}
}

// stagedGrowthCircles is the circle-count sweep. It exists because every other
// pipeline benchmark in this package fixes K at 12, which is the one regime
// where the backends' staged costs cannot diverge: the CPU renderer implements
// accumulatedSessionFactory and evaluates only a stage's new circles over a
// retained canvas, while the OpenCL renderer does not and replays every
// retained circle on every evaluation. That makes CPU staged work grow with K
// and OpenCL staged work grow with K squared, so the two only separate once K
// is large enough for the quadratic term to matter. Campaigns in
// docs/schedule-format.md run to 1000-3000 circles.
var stagedGrowthCircles = []int{8, 32, 128}

// BenchmarkOptimizeStagedGrowth measures how staged pipeline cost scales with
// circle count on each backend. The absolute times matter less than the shape:
// a backend that replays retained circles bends upward against one that does
// not.
func BenchmarkOptimizeStagedGrowth(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	for _, size := range []int{128, 256} {
		ref := benchmarkPipelineReference(size, size)

		b.Run(strconv.Itoa(size), func(b *testing.B) {
			for _, circles := range stagedGrowthCircles {
				b.Run("K="+strconv.Itoa(circles), func(b *testing.B) {
					for _, mode := range []struct {
						name string
						run  func(Renderer, *pipelineComparisonOptimizer, int) (*OptimizationResult, error)
					}{
						{
							name: "sequential",
							run: func(r Renderer, o *pipelineComparisonOptimizer, k int) (*OptimizationResult, error) {
								return OptimizeSequential(r, o, k, DisabledConvergenceConfig(), nil)
							},
						},
						{
							name: "batch",
							run: func(r Renderer, o *pipelineComparisonOptimizer, k int) (*OptimizationResult, error) {
								return OptimizeBatch(r, o, k, 8, DisabledConvergenceConfig())
							},
						},
					} {
						b.Run(mode.name, func(b *testing.B) {
							for _, backend := range benchmarkBackends {
								b.Run(string(backend), func(b *testing.B) {
									benchmarkStagedGrowth(b, backend, ref, circles, mode.run)
								})
							}
						})
					}
				})
			}
		})
	}
}

func benchmarkStagedGrowth(
	b *testing.B,
	backend Backend,
	ref *image.NRGBA,
	circles int,
	run func(Renderer, *pipelineComparisonOptimizer, int) (*OptimizationResult, error),
) {
	b.Helper()

	r, cleanup, err := NewRendererForBackend(string(backend), ref, circles)
	if err != nil {
		// A reproduction run asks for OpenCL explicitly, so an unavailable
		// backend has to fail rather than leave the sweep silently half empty.
		if backend == BackendOpenCL && requireOpenCLBenchmarks() {
			b.Fatalf("required %s backend unavailable: %v", backend, err)
		}

		b.Skipf("%s backend unavailable: %v", backend, err)
	}

	// Registered rather than deferred: a deferred release runs inside the
	// measured region, and it also has to run when an assertion below fails.
	b.Cleanup(cleanup)

	requireBenchmarkGPUDevice(b, r)
	requireLiveDevice(b, r, backend, "before")

	optimizer := &pipelineComparisonOptimizer{evaluations: pipelineComparisonEvaluations}

	var result *OptimizationResult

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		result, err = run(r, optimizer, circles)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.StopTimer()

	// Cost and Render have no error return, so a device lost mid-loop degrades
	// the renderer to its CPU fallback and every later answer is a CPU answer
	// under an OpenCL label.
	requireLiveDevice(b, r, backend, "after")

	if result != nil {
		b.ReportMetric(float64(result.Evaluations), "evaluations/pipeline")
	}
}
