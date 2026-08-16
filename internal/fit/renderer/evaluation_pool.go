package renderer

import "sync/atomic"

// parallelEvaluationRenderer reports how many concurrent cost evaluations a
// renderer's sessions may serve. Backends that do not implement it, including
// OpenCL, keep the single-session path.
type parallelEvaluationRenderer interface {
	ParallelEvaluationWorkers() int
}

// evaluationWorkers reports the concurrent evaluation width configured on base.
// It is never below one, so callers can size a pool with it unconditionally.
func evaluationWorkers(base Renderer) int {
	renderer, ok := base.(parallelEvaluationRenderer)
	if !ok {
		return 1
	}
	if workers := renderer.ParallelEvaluationWorkers(); workers > 1 {
		return workers
	}
	return 1
}

// evaluationSlot pairs one independent renderer session with the scratch vector
// its evaluations assemble. A slot is leased by exactly one goroutine at a
// time, so neither the session's canvas nor the scratch vector is shared.
type evaluationSlot struct {
	session Renderer
	// combined is the full parameter vector a staged evaluation writes its
	// stage parameters into. It is nil when the optimizer already evaluates a
	// complete vector.
	combined []float64
}

// evaluationPool makes cost evaluation re-entrant. Concurrent optimizers lease
// a slot per evaluation, so no two goroutines composite into the same canvas.
// A pool of one slot behaves exactly like the historical serial path.
type evaluationPool struct {
	free chan *evaluationSlot
	// cleanups releases the sessions the pool created itself. The primary
	// session belongs to the caller and is never released here.
	cleanups    []func()
	evaluations atomic.Int64
}

// newEvaluationPool builds a pool of workers independent sessions.
//
// With workers below two the pool wraps the caller's primary session and
// scratch vector, which keeps the single-threaded path allocation-identical to
// before. Otherwise it creates its own sessions through newSession, each with
// its own canvas, its own copy of combined, and a single rendering thread. The
// primary session then stays reserved for the caller's rendering and canvas
// work. If session creation fails the pool degrades to the primary session
// rather than failing the run: fewer slots only costs throughput.
func newEvaluationPool(
	primary Renderer,
	combined []float64,
	workers int,
	newSession func() (Renderer, func(), error),
) *evaluationPool {
	pool := &evaluationPool{}
	var slots []*evaluationSlot
	if workers > 1 && newSession != nil {
		for range workers {
			session, cleanup, err := newSession()
			if err != nil || session == nil {
				break
			}
			if cpu, ok := session.(*CPURenderer); ok {
				cpu.SetThreads(1)
			}
			pool.cleanups = append(pool.cleanups, cleanup)
			slots = append(slots, &evaluationSlot{
				session:  session,
				combined: append([]float64(nil), combined...),
			})
		}
	}
	if len(slots) == 0 {
		pool.close()
		slots = []*evaluationSlot{{session: primary, combined: combined}}
	}
	pool.free = make(chan *evaluationSlot, len(slots))
	for _, slot := range slots {
		pool.free <- slot
	}
	return pool
}

// width reports how many evaluations the pool can serve concurrently.
func (p *evaluationPool) width() int {
	return cap(p.free)
}

// acquire leases a slot, blocking until one is free. More concurrent callers
// than slots is correct and merely serializes the surplus.
func (p *evaluationPool) acquire() *evaluationSlot {
	p.evaluations.Add(1)
	return <-p.free
}

// release returns a leased slot.
func (p *evaluationPool) release(slot *evaluationSlot) {
	p.free <- slot
}

// count reports the evaluations served since the pool was created.
func (p *evaluationPool) count() int {
	return int(p.evaluations.Load())
}

// close releases every session the pool created. It must not run while an
// evaluation is in flight.
func (p *evaluationPool) close() {
	for _, cleanup := range p.cleanups {
		if cleanup != nil {
			cleanup()
		}
	}
	p.cleanups = nil
}
