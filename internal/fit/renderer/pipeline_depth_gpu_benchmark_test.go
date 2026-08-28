//go:build gpu

package renderer //nolint:testpackage // uses the unexported newSessionWithCanvas hook

import (
	"image"
	"strconv"
	"testing"
)

// stagedDepths are retained-circle counts a campaign passes through.
// docs/schedule-format.md describes campaigns run to 1000-3000 circles with
// additionalCircles 1, so a late stage carries a very deep retained prefix.
var stagedDepths = []int{8, 32, 128, 512}

// BenchmarkStagedEvaluationAtDepth isolates the one quantity that decides
// whether the OpenCL staged pipeline needs an accumulated canvas: the cost of a
// single objective evaluation at a given retained depth.
//
// A stage appends one circle to D retained ones. The CPU renderer implements
// accumulatedSessionFactory, so its session rasterizes one circle over a
// retained canvas and its per-evaluation cost is independent of D. The OpenCL
// renderer does not, so its session rebuilds all D+1 circles from white on
// every evaluation and its cost grows with D.
//
// The three arms separate backend from technique. "cpu_replay" is the CPU
// paying the same replay the GPU pays, which is what makes "cpu_accumulated"
// attributable to the accumulated canvas rather than to the CPU being faster.
//
// The pipeline benchmarks cannot see this. They run eight evaluations per
// stage, where per-stage setup dominates, while a real stage runs hundreds.
func BenchmarkStagedEvaluationAtDepth(b *testing.B) {
	silencePipelineBenchmarkLogs(b)

	for _, size := range []int{128, 512} {
		ref := benchmarkPipelineReference(size, size)

		b.Run(strconv.Itoa(size), func(b *testing.B) {
			for _, depth := range stagedDepths {
				b.Run("D="+strconv.Itoa(depth), func(b *testing.B) {
					retained := benchmarkParams(depth, size, size, 20260828)
					appended := benchmarkParams(depth+1, size, size, 20260828)
					canvas := retainedCanvas(b, ref, retained, depth)

					b.Run("cpu_accumulated", func(b *testing.B) {
						base := NewCPURenderer(ref, depth)

						session, cleanup, err := base.newSessionWithCanvas(canvas, 1)
						if err != nil {
							b.Fatal(err)
						}

						b.Cleanup(cleanup)

						benchmarkDepthCost(b, BackendCPU, session, appended[depth*paramsPerCircle:])
					})

					b.Run("cpu_replay", func(b *testing.B) {
						benchmarkDepthCost(b, BackendCPU, NewCPURenderer(ref, depth+1), appended)
					})

					b.Run("opencl_replay", func(b *testing.B) {
						r, cleanup, err := NewRendererForBackend(string(BackendOpenCL), ref, depth+1)
						if err != nil {
							// A reproduction run asks for OpenCL explicitly. Skipping
							// there would let go test report success with every OpenCL
							// cell missing, and publish a profile with a hole in it.
							if requireOpenCLBenchmarks() {
								b.Fatalf("required opencl backend unavailable: %v", err)
							}

							b.Skipf("opencl backend unavailable: %v", err)
						}

						// Registered rather than deferred. A deferred release runs
						// before the benchmark function returns, which is inside the
						// measured region: the fixed tens-of-milliseconds OpenCL
						// teardown would be divided by b.N and reported as
						// per-evaluation cost. That is the defect this branch corrects
						// in BenchmarkOpenCLSessionCreation. b.Cleanup also runs after
						// a Fatalf, so the device is released on the failure paths too.
						b.Cleanup(cleanup)

						requireBenchmarkGPUDevice(b, r)
						benchmarkDepthCost(b, BackendOpenCL, r, appended)
					})
				})
			}
		})
	}
}

// retainedCanvas rasterizes the retained prefix once, which is what a staged
// pipeline hands to the next session.
func retainedCanvas(b *testing.B, ref *image.NRGBA, params []float64, depth int) *image.NRGBA {
	b.Helper()

	return cloneNRGBA(NewCPURenderer(ref, depth).Render(params))
}

func benchmarkDepthCost(b *testing.B, backend Backend, r Renderer, params []float64) {
	b.Helper()

	working := append([]float64(nil), params...)
	baseX := working[0]
	_ = r.Cost(working)

	requireLiveDevice(b, r, backend, "before")

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		// Defeat the single-slot parameter cache without changing the shape of
		// the workload, exactly as benchmarkRendererEvaluation does.
		working[0] = baseX + float64(i%32+1)*0.03125
		_ = r.Cost(working)
	}

	// Stop before checking, so the check is not itself measured -- and check at
	// all because Cost has no error return: a device lost mid-loop degrades the
	// renderer permanently and silently to its CPU fallback, and the remaining
	// iterations would be published as an OpenCL time.
	b.StopTimer()
	requireLiveDevice(b, r, backend, "after")
}
