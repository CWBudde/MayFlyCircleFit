package renderer

import (
	"image"
	"image/color"
	"io"
	"log/slog"
	"testing"
)

type pipelineBenchmarkOptimizer struct {
	evaluations int
}

func (o pipelineBenchmarkOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := make([]float64, dim)
	for offset := 0; offset < dim; offset += paramsPerCircle {
		params[offset] = 0.5
		params[offset+1] = 0.5
		params[offset+2] = 0.15
		params[offset+3] = 0.2
		params[offset+4] = 0.4
		params[offset+5] = 0.8
		params[offset+6] = 0.5
	}

	bestCost := 0.0
	for i := 0; i < o.evaluations; i++ {
		bestCost = eval(params)
	}
	return params, bestCost
}

func benchmarkPipelineReference(width, height int) *image.NRGBA {
	ref := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
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
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(previous) })
}

// BenchmarkOptimizeSequentialPipeline measures the complete staged pipeline,
// including renderer sessions, objective evaluations, retained results, and
// final replay. ReportAllocs makes parameter-vector regressions visible.
func BenchmarkOptimizeSequentialPipeline(b *testing.B) {
	silencePipelineBenchmarkLogs(b)
	ref := benchmarkPipelineReference(64, 64)
	base := NewCPURenderer(ref, 12)
	optimizer := pipelineBenchmarkOptimizer{evaluations: 8}
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		if _, err := OptimizeSequential(base, optimizer, 12, DisabledConvergenceConfig(), nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOptimizeBatchPipeline measures an end-to-end multi-stage batch run
// whose final stage is partial, exercising exact remainder handling as well as
// accumulated parameter and image construction.
func BenchmarkOptimizeBatchPipeline(b *testing.B) {
	silencePipelineBenchmarkLogs(b)
	ref := benchmarkPipelineReference(64, 64)
	base := NewCPURenderer(ref, 18)
	optimizer := pipelineBenchmarkOptimizer{evaluations: 8}
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		if _, err := OptimizeBatch(base, optimizer, 18, 5, DisabledConvergenceConfig()); err != nil {
			b.Fatal(err)
		}
	}
}
