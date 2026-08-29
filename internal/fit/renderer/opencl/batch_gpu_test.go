//go:build gpu

package opencl //nolint:testpackage // exercises the unexported batch evaluator on one serial device

import (
	"image"
	"os"
	"strconv"
	"testing"
)

// batchLambdas are the generation sizes the probe reports. 20 and 1024 are the
// two levels the CMA-ES stagnation campaign actually runs (docs/cmaes-lambda-report.md);
// 64 and 256 sit between them so a curve, rather than two points, decides
// whether the fixed per-evaluation floor amortizes.
var batchLambdas = []int{20, 64, 256, 1024}

// TestOpenCLCostBatchMatchesSerial pins the property the whole probe rests on:
// a pipelined generation reports exactly what one-call-at-a-time reports.
//
// Exactly, not within the CPU-versus-OpenCL deviation budget. That budget
// exists because the device accumulates in float32 against a float64 CPU path;
// here both arms are the same float32 device arithmetic over the same inputs,
// so any difference at all is a defect in the batching -- a slot reading
// another candidate's partials, or a reduction that ended on the wrong buffer.
//
// The canvas sizes are chosen for the reduction depth they force rather than
// for realism: 8x8 fits in a single workgroup and never enters the reduction
// loop, while 256x256 needs several passes and therefore exercises the
// ping-pong and the copy-back that fixes each slot's result at a known offset.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLCostBatchMatchesSerial(t *testing.T) {
	for _, size := range []int{8, 256} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			const circles = 8

			renderer, cleanup := newOpenCLBatchTestRenderer(t, size, circles)
			defer cleanup()

			candidates := batchCandidates(7, circles, size, 20260829)

			evaluator, release, err := renderer.newBatchEvaluator(len(candidates))
			if err != nil {
				t.Fatalf("newBatchEvaluator: %v", err)
			}
			defer release()

			batched, err := evaluator.costPipelined(candidates)
			if err != nil {
				t.Fatalf("costPipelined: %v", err)
			}

			serial := evaluator.costSerial(candidates)

			if len(batched) != len(serial) {
				t.Fatalf("costPipelined returned %d costs, costSerial returned %d", len(batched), len(serial))
			}

			for i := range serial {
				if batched[i] != serial[i] {
					t.Errorf("candidate %d: pipelined cost %v, serial cost %v", i, batched[i], serial[i])
				}
			}

			// A batch rebinds the render kernel's parameter and partial-sums
			// arguments. If it failed to put them back, the next ordinary
			// evaluation would read a batch slot's buffer, and it would do it
			// silently -- so assert the ordinary path still agrees after a batch
			// rather than trusting the restore.
			if got := renderer.Cost(candidates[0]); got != serial[0] {
				t.Errorf("Cost after a batch = %v, want %v: static kernel arguments were not restored", got, serial[0])
			}
		})
	}
}

// TestOpenCLCostBatchRejectsAnOversizedGeneration covers the one input error
// the evaluator can be handed at run time, since the width is fixed when its
// device buffers are allocated.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLCostBatchRejectsAnOversizedGeneration(t *testing.T) {
	const circles = 4

	renderer, cleanup := newOpenCLBatchTestRenderer(t, 8, circles)
	defer cleanup()

	evaluator, release, err := renderer.newBatchEvaluator(2)
	if err != nil {
		t.Fatalf("newBatchEvaluator: %v", err)
	}
	defer release()

	_, err = evaluator.costPipelined(batchCandidates(3, circles, 8, 1))
	if err == nil {
		t.Error("a generation wider than the evaluator was accepted")
	}
}

// BenchmarkOpenCLGenerationEvaluation is the measurement PLAN.md Task 11 asks
// for before a batched objective interface is designed. It compares evaluating
// a generation one blocking round trip at a time against evaluating it with a
// single host synchronization, at the canvas and circle count a campaign stage
// actually runs.
//
// Read it with docs/gpu-backends.md's warning in mind: do not pass -count, take
// medians over separate passes, and expect a wide spread on a throttling
// laptop. `just bench-gpu` runs one pass.
func BenchmarkOpenCLGenerationEvaluation(b *testing.B) {
	const (
		size    = 512
		circles = 8
	)

	ref := patternedReference(image.Rect(0, 0, size, size))

	for _, lambda := range batchLambdas {
		candidates := batchCandidates(lambda, circles, size, 20260829)

		b.Run("lambda="+strconv.Itoa(lambda), func(b *testing.B) {
			renderer, cleanup := newOpenCLBatchBenchmarkRenderer(b, ref, circles)
			defer cleanup()

			evaluator, release, err := renderer.newBatchEvaluator(lambda)
			if err != nil {
				b.Fatalf("newBatchEvaluator: %v", err)
			}
			defer release()

			// Both arms report per-evaluation cost rather than per-generation,
			// so the two lambda levels are directly comparable and a flat column
			// is the visible form of "nothing amortizes".
			b.Run("serial", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					evaluator.costSerial(candidates)
				}

				b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N*lambda), "us/eval")
			})

			b.Run("pipelined", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					_, err := evaluator.costPipelined(candidates)
					if err != nil {
						b.Fatalf("costPipelined: %v", err)
					}
				}

				b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N*lambda), "us/eval")
			})
		})
	}
}

// BenchmarkOpenCLEvaluationFloorBySize answers the question the pipelined arm
// raised by losing: if removing the per-candidate host synchronization makes a
// generation slower, how much of an evaluation is removable overhead at all?
//
// It sweeps the canvas at a fixed circle count on the ordinary serial path. The
// smallest canvas does almost no per-pixel work, so its per-evaluation figure is
// very nearly the launch-and-synchronize floor by itself; the difference between
// that and the 512x512 figure is per-pixel work that a batched kernel would
// still have to do once per candidate. Together they bound what any batching
// scheme can win, which is what PLAN.md Task 11 needs before an objective
// interface is designed for it.
func BenchmarkOpenCLEvaluationFloorBySize(b *testing.B) {
	const (
		circles = 8
		lambda  = 64
	)

	for _, size := range []int{8, 64, 128, 256, 512} {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			ref := patternedReference(image.Rect(0, 0, size, size))

			renderer, cleanup := newOpenCLBatchBenchmarkRenderer(b, ref, circles)
			defer cleanup()

			evaluator, release, err := renderer.newBatchEvaluator(lambda)
			if err != nil {
				b.Fatalf("newBatchEvaluator: %v", err)
			}
			defer release()

			candidates := batchCandidates(lambda, circles, size, 20260829)

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				evaluator.costSerial(candidates)
			}

			b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N*lambda), "us/eval")
		})
	}
}

// batchCandidates builds count distinct parameter vectors. They differ by seed
// so the cost cache in ensure cannot serve the serial arm from a hit and make
// it look free.
func batchCandidates(count, circles, size int, seed int64) [][]float64 {
	candidates := make([][]float64, count)
	for i := range candidates {
		candidates[i] = benchmarkParams(circles, size, size, seed+int64(i))
	}

	return candidates
}

func newOpenCLBatchTestRenderer(t *testing.T, size, circles int) (*Renderer, func()) {
	t.Helper()

	ref := patternedReference(image.Rect(0, 0, size, size))

	renderer, cleanup, err := New(ref, circles, newStubFallback)
	if err != nil {
		if os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1" {
			t.Fatalf("required OpenCL backend unavailable: %v", err)
		}

		t.Skipf("OpenCL backend unavailable: %v", err)
	}

	if renderer.Degraded() {
		cleanup()
		t.Skip("OpenCL backend degraded to the fallback renderer")
	}

	return renderer, cleanup
}

func newOpenCLBatchBenchmarkRenderer(b *testing.B, ref *image.NRGBA, circles int) (*Renderer, func()) {
	b.Helper()

	renderer, cleanup, err := New(ref, circles, newStubFallback)
	if err != nil {
		if os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1" {
			b.Fatalf("required OpenCL backend unavailable: %v", err)
		}

		b.Skipf("OpenCL backend unavailable: %v", err)
	}

	if renderer.Degraded() {
		cleanup()
		b.Skip("OpenCL backend degraded to the fallback renderer")
	}

	return renderer, cleanup
}
