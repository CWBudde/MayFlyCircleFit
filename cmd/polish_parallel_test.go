//nolint:testpackage // builds the unexported CLI polishing factory directly
package cmd

import (
	"image"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/opt"
)

// polishParallelConfig is the smallest configuration newPolishOptimizer reads,
// with the evaluation width the caller asked for.
func polishParallelConfig(parallel bool, workers int) app.JobConfig {
	return app.JobConfig{
		Variant:            app.VariantStandard,
		PolishingIters:     10,
		PolishingPopSize:   20,
		Seed:               7,
		ParallelEvaluation: parallel,
		EvaluationWorkers:  workers,
	}
}

// polishParallelRenderer is a CPU renderer configured to hand out the requested
// number of independent sessions, which is what makes the width grantable.
func polishParallelRenderer(t *testing.T, workers int) renderer.Renderer {
	t.Helper()

	rend := renderer.NewCPURenderer(image.NewNRGBA(image.Rect(0, 0, 16, 16)), 4)
	rend.SetParallelEvaluationWorkers(workers)

	return rend
}

// TestCLIMayflyPolisherHonorsParallelEvaluation closes the divergence between
// the two halves of the polishing decision.
//
// internal/server/worker.go's newPolishOptimizer has always carried the width
// across, so without this --parallel-evaluation widened the CLI's base stage
// and left its sweep serial -- the same stored configuration then ran two
// different searches depending on which binary picked it up, and the width is
// not trajectory-neutral.
func TestCLIMayflyPolisherHonorsParallelEvaluation(t *testing.T) {
	t.Parallel()

	const workers = 4

	polisher, err := newPolishOptimizer(polishParallelConfig(true, workers), polishParallelRenderer(t, workers))
	if err != nil {
		t.Fatalf("newPolishOptimizer() error = %v", err)
	}

	if got := opt.ParallelEvaluationWidth(polisher); got != workers {
		t.Errorf("ParallelEvaluationWidth() = %d, want %d", got, workers)
	}
}

// TestCLIMayflyPolisherStaysSerialByDefault is what keeps the change above from
// moving any recorded figure: a run that did not ask for parallel evaluation
// gets the serial sweep it always got, bit for bit.
func TestCLIMayflyPolisherStaysSerialByDefault(t *testing.T) {
	t.Parallel()

	polisher, err := newPolishOptimizer(polishParallelConfig(false, 4), polishParallelRenderer(t, 4))
	if err != nil {
		t.Fatalf("newPolishOptimizer() error = %v", err)
	}

	if got := opt.ParallelEvaluationWidth(polisher); got != 1 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 1", got)
	}
}
