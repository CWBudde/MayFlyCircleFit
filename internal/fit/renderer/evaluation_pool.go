package renderer

import (
	"sync/atomic"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

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

// EvaluationWidth reports how many cost evaluations the pipeline will actually
// run concurrently for base, which is one for any backend that cannot hand out
// independent sessions. Callers use it to report the width they really got
// rather than the width they asked for.
func EvaluationWidth(base Renderer) int {
	return evaluationWorkers(base)
}

// ParallelEvaluationOption returns the optimizer option matching what base can
// actually deliver, and reports whether parallel evaluation was enabled.
//
// It is the single place that decides this, because the decision is only safe
// when made from the renderer's own reported width. Configuring the optimizer
// from a requested worker count instead would let a backend without independent
// sessions -- OpenCL today -- run the optimizer's parallel path against a
// one-slot pool: every evaluation goroutine would queue on that slot for no
// throughput at all, while the run still paid the altered search trajectory
// that parallel evaluation implies. Callers must therefore configure the
// renderer first, then derive the option from the renderer.
//
// A false second result with enabled set means the request could not be
// honored, which is worth a warning rather than silence.
func ParallelEvaluationOption(base Renderer, enabled bool) (opt.MayflyOption, bool) {
	noop := func(*opt.MayflyAdapter) {}
	if !enabled {
		return noop, false
	}
	width := evaluationWorkers(base)
	if width < 2 {
		return noop, false
	}
	return opt.WithParallelEvaluation(width), true
}

// threadedRenderer reports the rendering width a backend was configured for.
// It is the budget for any fan-out a renderer's caller performs, so work
// spread over independent sessions stays inside the same --threads contract a
// single render honors.
type threadedRenderer interface {
	Threads() int
}

// renderWorkers reports how many independent renders base's configuration
// allows to run at once. It is never below one.
func renderWorkers(base Renderer) int {
	threaded, ok := base.(threadedRenderer)
	if !ok {
		return 1
	}
	return max(1, threaded.Threads())
}

// concurrentSessions opens workers independent single-threaded sessions over
// base, for diagnostic work that renders many variants of one vector -- the
// batch audit and residual-region selection, neither of which is in the
// optimizer hot path but both of which are quadratic in the circle count.
//
// Each session gets one rendering thread: the caller runs one session per
// goroutine, so sharding a single render's rows on top of that would only
// oversubscribe the machine. Sessions are single-threaded for the same reason
// the evaluation pool's slots are.
//
// It returns nil, and leaves the caller on its serial path, for any backend
// that cannot hand out independent sessions or does not advertise concurrent
// evaluation. The second condition is what keeps OpenCL out: it can create
// sessions, but several of them working at once has never been validated, and
// that is exactly the marker it withholds.
func concurrentSessions(base Renderer, circleCount, workers int) ([]Renderer, func()) {
	if workers < 2 {
		return nil, noopCleanup
	}
	factory, poolable := base.(rendererSessionFactory)
	if _, concurrent := base.(parallelEvaluationRenderer); !poolable || !concurrent {
		return nil, noopCleanup
	}
	var cleanups []func()
	release := func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
	sessions := make([]Renderer, 0, workers)
	for range workers {
		session, cleanup, err := factory.newSession(circleCount)
		if err != nil || session == nil {
			release()
			return nil, noopCleanup
		}
		cleanups = append(cleanups, cleanup)
		if cpu, ok := session.(*CPURenderer); ok {
			cpu.SetThreads(1)
		}
		sessions = append(sessions, session)
	}
	return sessions, release
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

// ConcurrentEvaluator exposes the pipeline's re-entrant cost evaluation to
// callers that drive an optimizer themselves instead of going through
// OptimizeJoint. A renderer's Cost writes its own reusable canvas and dirty
// span set, so calling it from several goroutines corrupts results silently.
// The evaluator leases one independent session per in-flight evaluation, which
// is exactly what OptimizeJointContext does.
//
// It is required wherever an optimizer is configured with
// opt.WithParallelEvaluation. With a renderer that reports fewer than two
// evaluation workers, or a backend that cannot create sessions, the evaluator
// wraps the caller's renderer in a single slot: correct, and identical to the
// historical serial path.
type ConcurrentEvaluator struct {
	pool *evaluationPool
}

// NewConcurrentEvaluator builds an evaluator over base sized by base's
// configured evaluation width. Close must be called when the run finishes.
func NewConcurrentEvaluator(base Renderer, circleCount int) *ConcurrentEvaluator {
	pool := newEvaluationPool(base, nil, evaluationWorkers(base), func() (Renderer, func(), error) {
		factory, ok := base.(rendererSessionFactory)
		if !ok {
			return nil, nil, ErrStagedOptimizationUnsupported
		}
		return factory.newSession(circleCount)
	})
	return &ConcurrentEvaluator{pool: pool}
}

// Cost evaluates params on a leased session. It is safe for concurrent use.
func (e *ConcurrentEvaluator) Cost(params []float64) float64 {
	slot := e.pool.acquire()
	defer e.pool.release(slot)
	return slot.session.Cost(params)
}

// Width reports how many evaluations run concurrently before callers queue.
func (e *ConcurrentEvaluator) Width() int {
	return e.pool.width()
}

// Evaluations reports how many evaluations the evaluator has served.
func (e *ConcurrentEvaluator) Evaluations() int {
	return e.pool.count()
}

// Close releases the sessions the evaluator created. It must not run while an
// evaluation is in flight.
func (e *ConcurrentEvaluator) Close() {
	e.pool.close()
}
