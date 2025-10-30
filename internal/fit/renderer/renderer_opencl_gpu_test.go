//go:build gpu

package renderer

import (
	"image"
	"math"
	"testing"
)

func TestOpenCLRendererMatchesCPU(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for i := range ref.Pix {
		ref.Pix[i] = 255
	}

	const circles = 1

	params := make([]float64, circles*7) // 7 params per circle
	params[0] = 16 // X
	params[1] = 16 // Y
	params[2] = 8  // Radius
	params[3] = 0.2
	params[4] = 0.4
	params[5] = 0.8
	params[6] = 0.9 // Opacity

	cpu := NewCPURenderer(ref, circles)
	cpuCost := cpu.Cost(params)

	gpuRenderer, cleanup, err := NewRendererForBackend("opencl", ref, circles)
	if err != nil {
		t.Skipf("GPU backend unavailable: %v", err)
	}
	defer cleanup()

	gpuCost := gpuRenderer.Cost(params)

	if diff := math.Abs(cpuCost - gpuCost); diff > 1e-3 {
		t.Fatalf("cost mismatch (cpu=%f gpu=%f diff=%f)", cpuCost, gpuCost, diff)
	}

	cpuImage := cpu.Render(params)
	gpuImage := gpuRenderer.Render(params)

	assertNRGBAWithin(t, cpuImage, gpuImage, 2)
}

func assertNRGBAWithin(t *testing.T, a, b *image.NRGBA, tolerance uint8) {
	t.Helper()

	if !a.Bounds().Eq(b.Bounds()) {
		t.Fatalf("bounds mismatch: %v vs %v", a.Bounds(), b.Bounds())
	}

	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			i := a.PixOffset(x, y)
			for c := 0; c < 4; c++ {
				va := a.Pix[i+c]
				vb := b.Pix[i+c]
				if diff := absUint8Diff(va, vb); diff > tolerance {
					t.Fatalf("pixel mismatch at (%d,%d) channel %d: %d vs %d", x, y, c, va, vb)
				}
			}
		}
	}
}

func absUint8Diff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
