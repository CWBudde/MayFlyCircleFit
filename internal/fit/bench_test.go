package fit_test

import (
	"image"
	"image/color"
	"log/slog"
	"math/rand"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/opt"
)

const benchmarkParamsPerCircle = 7

var (
	benchmarkImageSink  *image.NRGBA
	benchmarkCostSink   float64
	benchmarkResultSink *renderer.OptimizationResult
)

// BenchmarkFit is the canonical CPU benchmark suite. Keep its name and case
// labels stable so benchstat can compare results across revisions.
func BenchmarkFit(b *testing.B) {
	silenceBenchmarkLogs(b)
	b.Run("Render", benchmarkRendering)
	b.Run("Cost", benchmarkCostComputation)
	b.Run("Pipeline", benchmarkOptimizationPipelines)
}

func benchmarkRendering(b *testing.B) {
	workloads := []struct {
		name          string
		width, height int
		circles       int
	}{
		{name: "64x64/K4", width: 64, height: 64, circles: 4},
		{name: "128x128/K20", width: 128, height: 128, circles: 20},
		{name: "256x256/K50", width: 256, height: 256, circles: 50},
		{name: "512x512/K100", width: 512, height: 512, circles: 100},
	}

	for _, workload := range workloads {
		b.Run(workload.name, func(b *testing.B) {
			reference := benchmarkImage(workload.width, workload.height, 42)
			cpu := renderer.NewCPURenderer(reference, workload.circles)
			cpu.SetThreads(1)

			params := benchmarkParams(workload.circles, workload.width, workload.height, 99)

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				benchmarkImageSink = cpu.Render(params)
			}
		})
	}
}

func benchmarkCostComputation(b *testing.B) {
	workloads := []struct {
		name          string
		width, height int
	}{
		{name: "64x64", width: 64, height: 64},
		{name: "256x256", width: 256, height: 256},
		{name: "512x512", width: 512, height: 512},
	}
	costs := []struct {
		name string
		cost fit.CostFunc
	}{
		{name: "MSE", cost: fit.MSECost},
		{name: "FastMSE", cost: fit.FastMSECost},
	}

	for _, workload := range workloads {
		current := benchmarkImage(workload.width, workload.height, 42)

		reference := benchmarkImage(workload.width, workload.height, 99)
		for _, cost := range costs {
			b.Run(workload.name+"/"+cost.name, func(b *testing.B) {
				b.SetBytes(int64(workload.width * workload.height * 8))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					benchmarkCostSink = cost.cost(current, reference)
				}
			})
		}
	}
}

func benchmarkOptimizationPipelines(b *testing.B) {
	reference := benchmarkImage(64, 64, 42)

	b.Run("Joint/K8", func(b *testing.B) {
		base := renderer.NewCPURenderer(reference, 8)
		base.SetThreads(1)

		optimizer := opt.NewMayfly(2, 20, 20250812)

		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			result, err := renderer.OptimizeJoint(base, optimizer, 8, renderer.DisabledConvergenceConfig())
			if err != nil {
				b.Fatal(err)
			}

			benchmarkResultSink = result
		}
	})

	b.Run("Sequential/K4", func(b *testing.B) {
		base := renderer.NewCPURenderer(reference, 4)
		base.SetThreads(1)

		optimizer := opt.NewMayfly(1, 20, 20250812)

		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			result, err := renderer.OptimizeSequential(base, optimizer, 4, renderer.DisabledConvergenceConfig(), nil)
			if err != nil {
				b.Fatal(err)
			}

			benchmarkResultSink = result
		}
	})

	b.Run("Batch/K6/B2", func(b *testing.B) {
		base := renderer.NewCPURenderer(reference, 6)
		base.SetThreads(1)

		optimizer := opt.NewMayfly(1, 20, 20250812)

		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			result, err := renderer.OptimizeBatch(base, optimizer, 6, 2, renderer.DisabledConvergenceConfig())
			if err != nil {
				b.Fatal(err)
			}

			benchmarkResultSink = result
		}
	})
}

func benchmarkImage(width, height int, seed int64) *image.NRGBA {
	rng := rand.New(rand.NewSource(seed))

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*3 + rng.Intn(64)) % 256),
				G: uint8((y*5 + rng.Intn(64)) % 256),
				B: uint8(((x+y)*2 + rng.Intn(64)) % 256),
				A: 255,
			})
		}
	}

	return img
}

func benchmarkParams(circles, width, height int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	params := make([]float64, circles*benchmarkParamsPerCircle)
	maxRadius := max(1, min(width, height)/4)

	for circle := range circles {
		offset := circle * benchmarkParamsPerCircle
		params[offset] = rng.Float64() * float64(width)
		params[offset+1] = rng.Float64() * float64(height)
		params[offset+2] = 1 + rng.Float64()*float64(maxRadius-1)
		params[offset+3] = rng.Float64()
		params[offset+4] = rng.Float64()
		params[offset+5] = rng.Float64()
		params[offset+6] = 0.25 + rng.Float64()*0.75
	}

	return params
}

func silenceBenchmarkLogs(b *testing.B) {
	b.Helper()

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.DiscardHandler))
	b.Cleanup(func() { slog.SetDefault(previous) })
}
