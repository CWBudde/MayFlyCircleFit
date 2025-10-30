//go:build gpu

package renderer

import (
	"image"
	"math/rand"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
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

func randomParams(k, width, height int) []float64 {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	params := make([]float64, k*paramsPerCircle)
	for i := 0; i < k; i++ {
		offset := i * paramsPerCircle
		params[offset+0] = r.Float64() * float64(width)
		params[offset+1] = r.Float64() * float64(height)
		params[offset+2] = 5 + r.Float64()*float64(width/4)
		params[offset+3] = r.Float64()
		params[offset+4] = r.Float64()
		params[offset+5] = r.Float64()
		params[offset+6] = 0.5 + 0.5*r.Float64()
	}
	return params
}
