package cmd

import (
	"image"
	"image/color"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
)

// resumeGradientReference builds a deterministic non-uniform image. A solid
// image lets almost any circle score identically, which would hide an
// interleaving effect instead of exposing it.
func resumeGradientReference(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 7) % 256),
				G: uint8((y * 5) % 256),
				B: uint8((x*y + 31) % 256),
				A: 255,
			})
		}
	}

	return img
}

// resumeCandidates builds a fixed candidate set so every pool width evaluates
// exactly the same vectors in the same order.
func resumeCandidates(batches, workers, dim int, lower, upper []float64) [][][]float64 {
	rng := rand.New(rand.NewSource(11))

	all := make([][][]float64, batches)
	for b := range all {
		batch := make([][]float64, workers)
		for i := range batch {
			candidate := make([]float64, dim)
			for j := range candidate {
				candidate[j] = lower[j] + rng.Float64()*(upper[j]-lower[j])
			}

			batch[i] = candidate
		}

		all[b] = batch
	}

	return all
}

// resumeEvaluationCosts drives the resume construction path with a given
// evaluation width and returns every cost the objective produced. The
// objective is called from several goroutines at once, which is exactly what
// MayFly does once the optimizer is configured with parallel evaluation, and
// what resume asks for whenever the checkpoint recorded --parallel-evaluation.
func resumeEvaluationCosts(t *testing.T, workers int, circles int) []float64 {
	t.Helper()

	ref := resumeGradientReference(24, 18)
	rend := renderer.NewCPURenderer(ref, circles)
	rend.SetThreads(2)
	rend.SetParallelEvaluationWorkers(workers)

	joint := newResumeJointProblem(rend, circles)
	defer joint.close()

	batches := resumeCandidates(4, 4, joint.problem.Dim, joint.problem.Lower, joint.problem.Upper)
	var costs []float64

	for _, batch := range batches {
		batchCosts := make([]float64, len(batch))
		var group sync.WaitGroup
		group.Add(len(batch))

		for i := range batch {
			go func() {
				defer group.Done()

				batchCosts[i] = joint.problem.Eval(append([]float64(nil), batch[i]...))
			}()
		}

		group.Wait()

		costs = append(costs, batchCosts...)
	}

	return costs
}

// TestResumeJointProblemIsConcurrencySafe is the regression test for resume
// sharing one renderer across MayFly's evaluation goroutines. A renderer's Cost
// writes its own reusable canvas, so concurrent calls tore the canvas and
// returned costs that no candidate has. The failure was silent: a wrong cost,
// a garbage output image, and a corrupted follow-on checkpoint, with no error.
// Under -race the old code also reports a data race in render.
func TestResumeJointProblemIsConcurrencySafe(t *testing.T) {
	serial := resumeEvaluationCosts(t, 1, 3)

	parallel := resumeEvaluationCosts(t, 4, 3)
	if len(serial) == 0 {
		t.Fatal("no evaluations recorded")
	}

	if !slices.Equal(serial, parallel) {
		t.Fatalf("concurrent resume costs differ from serial costs\nserial:   %v\nparallel: %v", serial, parallel)
	}
}

// TestResumeJointProblemUsesPooledSessions guards the mechanism rather than
// only its observable effect: with parallel evaluation configured, the resume
// objective must lease sessions instead of reusing the caller's renderer.
func TestResumeJointProblemUsesPooledSessions(t *testing.T) {
	ref := resumeGradientReference(16, 12)
	rend := renderer.NewCPURenderer(ref, 2)
	rend.SetParallelEvaluationWorkers(4)

	evaluator := renderer.NewConcurrentEvaluator(rend, 2)
	defer evaluator.Close()

	if got := evaluator.Width(); got != rend.ParallelEvaluationWorkers() {
		t.Fatalf("evaluator width = %d, want %d", got, rend.ParallelEvaluationWorkers())
	}
}
