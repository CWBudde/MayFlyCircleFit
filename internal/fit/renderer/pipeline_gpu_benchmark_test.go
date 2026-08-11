//go:build gpu

package renderer

import (
	"math"
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

					if opencl, ok := r.(*openCLRenderer); ok {
						reportOpenCLBenchmarkDevice(b, opencl)
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
