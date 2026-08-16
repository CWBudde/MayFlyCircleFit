//go:build gpu

package renderer

import (
	"image"
	"strconv"
	"testing"
)

func BenchmarkRendererCost(b *testing.B) {
	for _, size := range []int{256, 512} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := image.NewNRGBA(image.Rect(0, 0, size, size))
			for i := range ref.Pix {
				ref.Pix[i] = 255
			}

			const circles = 64
			params := benchmarkParams(circles, size, size, 20260816)
			for _, backend := range []string{"cpu", "opencl"} {
				b.Run(backend, func(b *testing.B) {
					benchmarkRendererEvaluation(b, backend, ref, params, false)
				})
			}
		})
	}
}

func BenchmarkRendererCostThenRender(b *testing.B) {
	for _, size := range []int{256, 512} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := image.NewNRGBA(image.Rect(0, 0, size, size))
			for i := range ref.Pix {
				ref.Pix[i] = 255
			}

			const circles = 64
			params := benchmarkParams(circles, size, size, 20260816)
			for _, backend := range []string{"cpu", "opencl"} {
				b.Run(backend, func(b *testing.B) {
					benchmarkRendererEvaluation(b, backend, ref, params, true)
				})
			}
		})
	}
}

func benchmarkRendererEvaluation(b *testing.B, backend string, ref *image.NRGBA, params []float64, render bool) {
	b.Helper()
	r, cleanup, err := NewRendererForBackend(backend, ref, len(params)/paramsPerCircle)
	if err != nil {
		b.Skipf("%s backend unavailable: %v", backend, err)
	}
	defer cleanup()

	working := append([]float64(nil), params...)
	baseX := working[0]
	_ = r.Cost(working)
	if render {
		_ = r.Render(working)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Force a new hash and a real evaluation without changing the workload shape.
		working[0] = baseX + float64(i%32+1)*0.03125
		_ = r.Cost(working)
		if render {
			_ = r.Render(working)
		}
	}
}
