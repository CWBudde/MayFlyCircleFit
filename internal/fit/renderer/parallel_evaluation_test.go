package renderer

import (
	"image"
	"image/color"
	"math/rand"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

// gradientReference builds a deterministic non-uniform image. A solid image
// lets almost any circle score identically, which would hide an evaluation
// order effect instead of exposing it.
func gradientReference(width, height int) *image.NRGBA {
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

// concurrentOptimizer calls the objective from several goroutines at once,
// which is exactly what Mayfly's parallel evaluation pool does. It reproduces
// the interleaving hazard without depending on the optimizer's internals.
type concurrentOptimizer struct {
	workers int
	batches int
	dim     int
	costs   []float64
}

func (o *concurrentOptimizer) Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64) {
	rng := rand.New(rand.NewSource(7))
	best := append([]float64(nil), lower...)
	bestCost := eval(best)
	for range o.batches {
		candidates := make([][]float64, o.workers)
		for i := range candidates {
			candidate := make([]float64, dim)
			for j := range candidate {
				candidate[j] = lower[j] + rng.Float64()*(upper[j]-lower[j])
			}
			candidates[i] = candidate
		}
		costs := make([]float64, o.workers)
		var group sync.WaitGroup
		group.Add(o.workers)
		for i := range candidates {
			go func() {
				defer group.Done()
				costs[i] = eval(candidates[i])
			}()
		}
		group.Wait()
		o.costs = append(o.costs, costs...)
		for i, cost := range costs {
			if cost < bestCost {
				bestCost = cost
				best = candidates[i]
			}
		}
	}
	o.dim = dim
	return best, bestCost
}

// evaluationCostsFor runs the same fixed candidates through the pipeline with a
// given pool width and returns every cost the objective produced.
func evaluationCostsFor(t *testing.T, workers int, run func(base *CPURenderer, optimizer opt.Optimizer) error) []float64 {
	t.Helper()
	ref := gradientReference(24, 18)
	base := NewCPURenderer(ref, 3)
	base.SetThreads(2)
	base.SetParallelEvaluationWorkers(workers)
	// The optimizer's concurrency is fixed so both pool widths see exactly the
	// same candidates in the same order; only the pool width varies.
	optimizer := &concurrentOptimizer{workers: 4, batches: 4}
	if err := run(base, optimizer); err != nil {
		t.Fatalf("optimization error = %v", err)
	}
	return optimizer.costs
}

// TestParallelEvaluationIsPixelExact is the safety claim behind the feature:
// concurrent cost evaluation must return exactly the costs a serial evaluation
// of the same candidates returns. A shared canvas would interleave and produce
// different numbers here. Run it under -race to also catch the data race.
func TestParallelEvaluationIsPixelExact(t *testing.T) {
	modes := map[string]func(base *CPURenderer, optimizer opt.Optimizer) error{
		"joint": func(base *CPURenderer, optimizer opt.Optimizer) error {
			_, err := OptimizeJoint(base, optimizer, 3, DisabledConvergenceConfig())
			return err
		},
		"sequential": func(base *CPURenderer, optimizer opt.Optimizer) error {
			_, err := OptimizeSequential(base, optimizer, 3, DisabledConvergenceConfig(), nil)
			return err
		},
		"batch": func(base *CPURenderer, optimizer opt.Optimizer) error {
			_, err := OptimizeBatch(base, optimizer, 4, 2, DisabledConvergenceConfig())
			return err
		},
	}
	for name, run := range modes {
		t.Run(name, func(t *testing.T) {
			serial := evaluationCostsFor(t, 1, run)
			parallel := evaluationCostsFor(t, 4, run)
			if len(serial) == 0 {
				t.Fatal("no evaluations recorded")
			}
			if !slices.Equal(serial, parallel) {
				t.Fatalf("parallel costs differ from serial costs\nserial:   %v\nparallel: %v", serial, parallel)
			}
		})
	}
}

// TestEvaluationPoolReportsEveryEvaluation guards the atomic counter that
// replaced the shared increment.
func TestEvaluationPoolReportsEveryEvaluation(t *testing.T) {
	ref := gradientReference(16, 16)
	base := NewCPURenderer(ref, 2)
	base.SetParallelEvaluationWorkers(4)
	pool := newEvaluationPool(base, nil, evaluationWorkers(base), func() (Renderer, func(), error) {
		return base.newSession(2)
	})
	defer pool.close()
	if pool.width() != 4 {
		t.Fatalf("pool width = %d, want 4", pool.width())
	}
	const evaluations = 200
	var group sync.WaitGroup
	group.Add(evaluations)
	for range evaluations {
		go func() {
			defer group.Done()
			slot := pool.acquire()
			defer pool.release(slot)
			slot.session.Cost(transparentParams(2))
		}()
	}
	group.Wait()
	if pool.count() != evaluations {
		t.Fatalf("pool count = %d, want %d", pool.count(), evaluations)
	}
}

// TestEvaluationPoolFallsBackToPrimary keeps a backend that cannot create
// sessions working instead of failing the run.
func TestEvaluationPoolFallsBackToPrimary(t *testing.T) {
	base := NewCPURenderer(gradientReference(8, 8), 1)
	pool := newEvaluationPool(base, nil, 8, func() (Renderer, func(), error) {
		return nil, nil, ErrStagedOptimizationUnsupported
	})
	defer pool.close()
	if pool.width() != 1 {
		t.Fatalf("pool width = %d, want 1", pool.width())
	}
	slot := pool.acquire()
	if slot.session != Renderer(base) {
		t.Fatal("fallback slot does not use the primary session")
	}
	pool.release(slot)
}

// TestPooledSessionsRenderSingleThreaded documents the deliberate choice to
// disable the row-band fan-out inside a pooled session: with many evaluations
// in flight it only oversubscribes the machine.
func TestPooledSessionsRenderSingleThreaded(t *testing.T) {
	base := NewCPURenderer(gradientReference(64, 64), 2)
	base.SetThreads(4)
	base.SetParallelEvaluationWorkers(3)
	pool := newEvaluationPool(base, nil, evaluationWorkers(base), func() (Renderer, func(), error) {
		return base.newSession(2)
	})
	defer pool.close()
	for range pool.width() {
		slot := pool.acquire()
		defer pool.release(slot)
		cpu, ok := slot.session.(*CPURenderer)
		if !ok {
			t.Fatalf("pooled session type = %T, want *CPURenderer", slot.session)
		}
		if cpu.Threads() != 1 {
			t.Fatalf("pooled session threads = %d, want 1", cpu.Threads())
		}
	}
	if base.Threads() != 4 {
		t.Fatalf("base renderer threads = %d, want the caller's 4", base.Threads())
	}
}

// TestParallelEvaluationWorkersCapped guards the memory bound. Each evaluation
// worker above one costs a full extra session with its own canvas and
// background copy, roughly 2*W*H*4 bytes. Configuration only rejects
// Threads < 1, so --threads 10000 is a valid request that reaches this setter
// from the CLI and from the server job API alike; at 1920x1080 an unclamped
// pool would try to allocate on the order of 166 GB.
func TestParallelEvaluationWorkersCapped(t *testing.T) {
	base := NewCPURenderer(gradientReference(16, 12), 2)
	base.SetParallelEvaluationWorkers(10000)
	maxWorkers := runtime.GOMAXPROCS(0)
	if got := base.ParallelEvaluationWorkers(); got > maxWorkers {
		t.Fatalf("ParallelEvaluationWorkers() = %d, want at most GOMAXPROCS %d", got, maxWorkers)
	}
	if got := base.ParallelEvaluationWorkers(); got != maxWorkers {
		t.Fatalf("ParallelEvaluationWorkers() = %d, want the GOMAXPROCS cap %d", got, maxWorkers)
	}
}

// TestParallelEvaluationWorkersIgnoresImageHeight documents why the cap does
// not reuse effectiveThreadCount: that helper also clamps to the image height,
// which is right for row sharding and wrong for evaluation width, because
// concurrent evaluations are whole independent renders.
func TestParallelEvaluationWorkersIgnoresImageHeight(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 3 {
		t.Skip("needs at least three processors to distinguish the caps")
	}
	base := NewCPURenderer(gradientReference(8, 2), 2)
	base.SetParallelEvaluationWorkers(3)
	if got := base.ParallelEvaluationWorkers(); got != 3 {
		t.Fatalf("ParallelEvaluationWorkers() = %d, want 3 despite the two-row image", got)
	}
}
