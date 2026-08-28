//go:build gpu

package opencl

import (
	"image"
	"image/color"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// silenceOpenCLBenchmarkLogs keeps renderer construction's INFO lines out of the
// benchmark output. Every session logs one, and interleaved they split a result
// line from its name, which leaves the output unparseable by benchstat.
func silenceOpenCLBenchmarkLogs(b *testing.B) {
	b.Helper()

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.DiscardHandler))
	b.Cleanup(func() { slog.SetDefault(previous) })
}

func BenchmarkOpenCLParameterPackAndUpload(b *testing.B) {
	silenceOpenCLBenchmarkLogs(b)

	for _, circles := range []int{1, 10, 50, 100} {
		b.Run("K="+strconv.Itoa(circles), func(b *testing.B) {
			ref := patternedReference(image.Rect(0, 0, 1, 1))
			r, release := newOpenCLBenchmarkRenderer(b, ref, circles)
			defer release()

			params := benchmarkParams(circles, 1, 1, 20260816)
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
	silenceOpenCLBenchmarkLogs(b)

	for _, size := range []int{64, 256, 512, 1024} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := patternedReference(image.Rect(0, 0, size, size))
			r, release := newOpenCLBenchmarkRenderer(b, ref, 1)
			defer release()

			params := benchmarkParams(1, size, size, 20260816)
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

// BenchmarkOpenCLSessionCreation measures the device setup a session pays for
// today. Renderer.NewSession rebuilds the whole stack per session: a fresh
// gpu.InitOpenCL, clCreateProgramWithSource, clBuildProgram, the device info
// queries, and a re-upload of the reference image. Task 11.13 tranche 1 makes
// sessions share one engine, so this is the "before" figure it is measured
// against.
//
// The two arms separate the fixed setup term from what a session adds on top:
// "new" times a full New construction, "session" times a NewSession over a base
// renderer built outside the timed loop. Sweeping the image size separates the
// terms a second way, because the reference upload scales with the canvas while
// the program build does not.
func BenchmarkOpenCLSessionCreation(b *testing.B) {
	silenceOpenCLBenchmarkLogs(b)

	// Matches BenchmarkOptimizePipelineBackends. It stays small deliberately:
	// the circle count only sizes the parameter scratch, so a large one would
	// add noise without touching the setup cost this benchmark is about.
	const circles = 12

	for _, size := range []int{64, 512} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ref := patternedReference(image.Rect(0, 0, size, size))

			b.Run("new", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					// Both the construction and its cleanup are timed. Releasing
					// the context, program, kernels and buffers is part of the
					// per-session lifecycle tranche 1 removes, so it belongs in
					// the measurement; and every iteration has to release, or the
					// benchmark leaks an OpenCL context per iteration until the
					// driver refuses to create another.
					r, cleanup, err := New(ref, circles, newStubFallback)
					if err != nil {
						if os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1" {
							b.Fatalf("required OpenCL backend unavailable: %v", err)
						}

						b.Skipf("OpenCL backend unavailable: %v", err)
					}

					if r.Degraded() {
						cleanup()
						b.Skip("OpenCL backend degraded to the fallback renderer")
					}

					cleanup()
				}
			})

			b.Run("session", func(b *testing.B) {
				base, release := newOpenCLBenchmarkRenderer(b, ref, circles)
				defer release()

				reportOpenCLBenchmarkDevice(b, base)
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					// Timed together for the same reason as the "new" arm above.
					_, cleanup, err := base.NewSession(circles)
					if err != nil {
						b.Fatalf("create session: %v", err)
					}

					cleanup()
				}
			})
		})
	}
}

func newOpenCLBenchmarkRenderer(b *testing.B, ref *image.NRGBA, circles int) (*Renderer, func()) {
	b.Helper()
	r, cleanup, err := New(ref, circles, newStubFallback)
	if err != nil {
		if os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1" {
			b.Fatalf("required OpenCL backend unavailable: %v", err)
		}
		b.Skipf("OpenCL backend unavailable: %v", err)
	}
	if r.Degraded() {
		cleanup()
		b.Skip("OpenCL backend degraded to the fallback renderer")
	}
	return r, cleanup
}

func reportOpenCLBenchmarkDevice(b *testing.B, r *Renderer) {
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

// stubFallback stands in for the CPU renderer. These benchmarks measure the
// device path only, and newOpenCLBenchmarkRenderer skips as soon as the
// renderer degrades, so the fallback is never exercised. The real CPU renderer
// cannot be used here: it lives in the renderer package, which imports this one.
type stubFallback struct {
	reference *image.NRGBA
}

func newStubFallback(reference *image.NRGBA, _ int) Fallback {
	return stubFallback{reference: reference}
}

func (f stubFallback) Render(_ []float64) *image.NRGBA {
	return image.NewNRGBA(f.reference.Bounds())
}

func (f stubFallback) Cost(_ []float64) float64 { return math.Inf(1) }

func patternedReference(bounds image.Rectangle) *image.NRGBA {
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*31 + y*7) & 0xff),
				G: uint8((x*13 + y*29) & 0xff),
				B: uint8((x*3 + y*37) & 0xff),
				A: uint8((x*17 + y*11) & 0xff),
			})
		}
	}
	return ref
}

// benchmarkParams mirrors the renderer package helper of the same name. It is
// duplicated because this package cannot import the renderer package, so keep
// the two bodies and the seeds in step.
func benchmarkParams(k, width, height int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
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
