//go:build gpu

package renderer

import (
	"image"
	"os"
	"strconv"
	"testing"
)

func BenchmarkOpenCLParameterPackAndUpload(b *testing.B) {
	for _, circles := range []int{1, 10, 50, 100} {
		b.Run("K="+strconv.Itoa(circles), func(b *testing.B) {
			ref := patternedReference(image.Rect(0, 0, 1, 1))
			r := newOpenCLBenchmarkRenderer(b, ref, circles)
			defer r.release()

			params := randomParams(circles, 1, 1)
			reportOpenCLBenchmarkDevice(b, r)
			b.SetBytes(int64(len(params) * 4))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Exercise both float64-to-float32 packing and the blocking OpenCL
				// write even when consecutive uploads have the same shape.
				params[0] = float64(i%32) * 0.03125
				if err := r.uploadParams(params); err != nil {
					b.Fatalf("upload parameters: %v", err)
				}
			}
		})
	}
}

func BenchmarkOpenCLResidentImageReadback(b *testing.B) {
	for _, size := range []int{64, 256, 512, 1024} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := patternedReference(image.Rect(0, 0, size, size))
			r := newOpenCLBenchmarkRenderer(b, ref, 1)
			defer r.release()

			params := randomParams(1, size, size)
			if err := r.ensure(params); err != nil {
				b.Fatalf("prepare resident output: %v", err)
			}
			if err := r.materializeImage(r.deviceHash); err != nil {
				b.Fatalf("warm image readback: %v", err)
			}

			reportOpenCLBenchmarkDevice(b, r)
			b.SetBytes(int64(r.pixelCount * 4))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Keep the rendered output resident while invalidating only the
				// host-side cache, so this measures the image readback boundary.
				r.imageValid = false
				if err := r.materializeImage(r.deviceHash); err != nil {
					b.Fatalf("read resident output: %v", err)
				}
			}
		})
	}
}

func BenchmarkRendererCost(b *testing.B) {
	for _, size := range []int{256, 512} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := image.NewNRGBA(image.Rect(0, 0, size, size))
			for i := range ref.Pix {
				ref.Pix[i] = 255
			}

			const circles = 64
			params := randomParams(circles, size, size)
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
			params := randomParams(circles, size, size)
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

func newOpenCLBenchmarkRenderer(b *testing.B, ref *image.NRGBA, circles int) *openCLRenderer {
	b.Helper()
	renderer, cleanup, err := NewOpenCLRenderer(ref, circles)
	if err != nil {
		if os.Getenv("MAYFLY_REQUIRE_OPENCL") == "1" {
			b.Fatalf("required OpenCL backend unavailable: %v", err)
		}
		b.Skipf("OpenCL backend unavailable: %v", err)
	}

	r, ok := renderer.(*openCLRenderer)
	if !ok {
		cleanup()
		b.Fatalf("NewOpenCLRenderer returned %T, want *openCLRenderer", renderer)
	}
	return r
}

func reportOpenCLBenchmarkDevice(b *testing.B, r *openCLRenderer) {
	b.Helper()
	b.Logf(
		"OpenCL platform=%q platform_vendor=%q device=%q device_vendor=%q type=%s compute_units=%d",
		r.runtime.Platform.Name,
		r.runtime.Platform.Vendor,
		r.runtime.Device.Name,
		r.runtime.Device.Vendor,
		r.runtime.Device.Type,
		r.runtime.Device.MaxComputeUnits,
	)
}
