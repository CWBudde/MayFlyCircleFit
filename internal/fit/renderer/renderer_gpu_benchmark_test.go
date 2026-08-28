//go:build gpu

package renderer

import (
	"image"
	"os"
	"strconv"
	"sync"
	"testing"
)

// backendMatrixSizes and backendMatrixCircles are the Task 11.9 sweep. Every
// image size is crossed with every circle count so a single run yields the
// complete CPU/OpenCL comparison and the crossover between them.
var (
	backendMatrixSizes   = []int{64, 256, 512, 1024}
	backendMatrixCircles = []int{1, 10, 50, 100}

	// benchmarkBackends is the comparison every benchmark in this file runs.
	benchmarkBackends = []Backend{BackendCPU, BackendOpenCL}
)

// BenchmarkRendererBackendMatrix compares the CPU and OpenCL renderers across
// the full circle-count and image-size matrix.
//
// The "cost" arm measures a bare objective evaluation, which is what an
// optimizer actually runs and the only figure a crossover claim may rest on.
// "cost_then_render" adds the image materialization, which an optimizer pays
// only when it keeps a result; on the OpenCL path that is the lazy readback of
// the device-resident output, so the difference between the two arms isolates
// the transfer.
func BenchmarkRendererBackendMatrix(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	for _, size := range backendMatrixSizes {
		ref := benchmarkPipelineReference(size, size)

		b.Run(strconv.Itoa(size), func(b *testing.B) {
			for _, circles := range backendMatrixCircles {
				params := benchmarkParams(circles, size, size, 20260816)

				b.Run("K="+strconv.Itoa(circles), func(b *testing.B) {
					for _, arm := range []struct {
						name   string
						render bool
					}{
						{name: "cost", render: false},
						{name: "cost_then_render", render: true},
					} {
						b.Run(arm.name, func(b *testing.B) {
							for _, backend := range benchmarkBackends {
								b.Run(string(backend), func(b *testing.B) {
									benchmarkRendererEvaluation(b, backend, ref, params, arm.render)
								})
							}
						})
					}
				})
			}
		})
	}
}

func BenchmarkRendererCost(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	for _, size := range []int{256, 512} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := image.NewNRGBA(image.Rect(0, 0, size, size))
			for i := range ref.Pix {
				ref.Pix[i] = 255
			}

			const circles = 64
			params := benchmarkParams(circles, size, size, 20260816)

			for _, backend := range benchmarkBackends {
				b.Run(string(backend), func(b *testing.B) {
					benchmarkRendererEvaluation(b, backend, ref, params, false)
				})
			}
		})
	}
}

func BenchmarkRendererCostThenRender(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	for _, size := range []int{256, 512} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := image.NewNRGBA(image.Rect(0, 0, size, size))
			for i := range ref.Pix {
				ref.Pix[i] = 255
			}

			const circles = 64
			params := benchmarkParams(circles, size, size, 20260816)

			for _, backend := range benchmarkBackends {
				b.Run(string(backend), func(b *testing.B) {
					benchmarkRendererEvaluation(b, backend, ref, params, true)
				})
			}
		})
	}
}

func benchmarkRendererEvaluation(
	b *testing.B, backend Backend, ref *image.NRGBA, params []float64, render bool,
) {
	b.Helper()

	r, cleanup, err := NewRendererForBackend(string(backend), ref, len(params)/paramsPerCircle)
	if err != nil {
		if requireOpenCLBenchmarks() {
			b.Fatalf("required %s backend unavailable: %v", backend, err)
		}
		b.Skipf("%s backend unavailable: %v", backend, err)
	}
	defer cleanup()

	reportBenchmarkDeviceOnce(b, r)
	requireLiveDevice(b, r, backend, "before")

	working := append([]float64(nil), params...)
	baseX := working[0]
	_ = r.Cost(working)
	if render {
		_ = r.Render(working)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Force a new hash and a real evaluation without changing the workload
		// shape. Consecutive values always differ, including across the wrap, so
		// the OpenCL renderer's single-slot parameter cache never answers one of
		// these calls from its previous result.
		working[0] = baseX + float64(i%32+1)*0.03125
		_ = r.Cost(working)
		if render {
			_ = r.Render(working)
		}
	}

	b.StopTimer()

	// A degraded OpenCL renderer silently answers from the CPU fallback, which
	// would publish CPU timings under a GPU label.
	requireLiveDevice(b, r, backend, "after")
}

// requireLiveDevice fails when an OpenCL renderer has fallen back to its CPU
// fallback. It is a no-op for backends that cannot degrade.
func requireLiveDevice(b *testing.B, r Renderer, backend Backend, when string) {
	b.Helper()

	degradable, ok := r.(interface{ Degraded() bool })
	if !ok || !degradable.Degraded() {
		return
	}

	if requireOpenCLBenchmarks() {
		b.Fatalf("%s backend degraded to the CPU fallback %s the measured loop", backend, when)
	}

	b.Skipf("%s backend degraded to the CPU fallback %s the measured loop", backend, when)
}

func requireOpenCLBenchmarks() bool {
	return os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1"
}

// benchmarkDeviceOnce keeps the device banner out of every sub-benchmark's
// output, where it would sit between the result lines benchstat reads.
var benchmarkDeviceOnce sync.Once

func reportBenchmarkDeviceOnce(b *testing.B, r Renderer) {
	b.Helper()

	adapter, ok := r.(openCLAdapter)
	if !ok {
		return
	}

	benchmarkDeviceOnce.Do(func() {
		runtime := adapter.Runtime()
		b.Logf(
			"OpenCL platform=%q platform_vendor=%q device=%q device_vendor=%q type=%s compute_units=%d",
			runtime.Platform.Name,
			runtime.Platform.Vendor,
			runtime.Device.Name,
			runtime.Device.Vendor,
			runtime.Device.Type,
			runtime.Device.MaxComputeUnits,
		)
	})
}
