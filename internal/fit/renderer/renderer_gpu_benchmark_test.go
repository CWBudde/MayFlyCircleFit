//go:build gpu

package renderer

import (
	"image"
	"testing"
)

func BenchmarkRendererCost(b *testing.B) {
	ref := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for i := range ref.Pix {
		ref.Pix[i] = 255
	}

	const circles = 64
	params := randomParams(circles, ref.Bounds().Dx(), ref.Bounds().Dy())

	b.Run("CPU", func(b *testing.B) {
		renderer := NewCPURenderer(ref, circles)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = renderer.Cost(params)
		}
	})

	b.Run("OpenCL", func(b *testing.B) {
		rend, cleanup, err := NewRendererForBackend("opencl", ref, circles)
		if err != nil {
			b.Skipf("GPU backend unavailable: %v", err)
		}
		defer cleanup()

		// Warm-up once so command queue and buffers are ready before timing.
		rend.Cost(params)
		rend.Render(params)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = rend.Cost(params)
		}
	})
}
