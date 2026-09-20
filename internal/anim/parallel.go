package anim

import (
	"image"
	"runtime"
	"sync"
)

// Workers resolves a requested worker count the way the rest of the project
// does: a non-positive request means every core, and no request may exceed
// GOMAXPROCS. It is the same contract as the renderer's --threads, so the two
// knobs cannot be read to mean different things.
func Workers(requested int) int {
	limit := runtime.GOMAXPROCS(0)
	if requested < 1 || requested > limit {
		requested = limit
	}

	if requested < 1 {
		return 1
	}

	return requested
}

// SinkPool runs a Sink on several goroutines, so finishing one frame does not
// hold up the render of the next.
//
// It exists because drawing the animation is the cheap part. Rendering is the
// only stage with a sequential dependency -- every frame composites onto the
// canvas the previous one committed, which is what the Pascal BackDraw/Drawing
// split means -- and on a supersampled run it is the smaller half of the work:
// measured over a 3130-frame 4096x4096 cascade, 33 ms of rendering against
// 116 ms of PNG encoding per frame. Everything after the render is independent
// per frame and belongs off the critical path.
//
// Frames therefore finish out of order. That is invisible in the output,
// because each one is its own numbered file, and the caller keeps an ordered
// view of its own: Submit is called from the render loop, in sequence.
//
// The pool does not copy. Render hands its sink the renderer's live buffer,
// which the next frame overwrites, so what is given to Submit has to be an
// image the caller will not touch again.
type SinkPool struct {
	sink    Sink
	frames  chan frameJob
	err     error
	closing sync.Once
	wait    sync.WaitGroup
	mu      sync.Mutex
}

type frameJob struct {
	img   *image.NRGBA
	index int
}

// NewSinkPool starts workers goroutines running sink. A non-positive count
// selects every core. Close must be called, and its error checked, before the
// output is complete.
//
// The queue holds one frame per worker. That bound is the point: an unbounded
// queue would let the renderer run ahead of the encoders and accumulate whole
// frames in memory, which at 4096x4096 is 67 MB each.
func NewSinkPool(workers int, sink Sink) *SinkPool {
	count := Workers(workers)

	pool := &SinkPool{
		sink:   sink,
		frames: make(chan frameJob, count),
	}

	pool.wait.Add(count)

	for range count {
		go pool.run()
	}

	return pool
}

// Submit hands a frame to the pool. Its signature is Sink, so it can be passed
// to Render directly.
//
// It blocks while every worker is busy, which is the backpressure that keeps
// the queue bounded, and it reports the first error any worker has hit so the
// render stops rather than writing another three thousand frames into a
// directory that is already broken.
func (p *SinkPool) Submit(index int, img *image.NRGBA) error {
	err := p.Err()
	if err != nil {
		return err
	}

	p.frames <- frameJob{index: index, img: img}

	return nil
}

// Close stops accepting frames, waits for the ones in flight, and reports the
// first error a worker hit. It is safe to call more than once, so a caller can
// defer it and still check the error on the happy path.
func (p *SinkPool) Close() error {
	p.closing.Do(func() { close(p.frames) })
	p.wait.Wait()

	return p.Err()
}

// Err reports the first error a worker hit, or nil.
func (p *SinkPool) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.err
}

// run drains the queue until it closes. After a failure it keeps draining
// rather than returning: a worker that stopped receiving would leave Submit
// blocked on a full channel with nobody to empty it, and the render would hang
// instead of reporting the error.
func (p *SinkPool) run() {
	defer p.wait.Done()

	for job := range p.frames {
		if p.Err() != nil {
			continue
		}

		err := p.sink(job.index, job.img)
		if err != nil {
			p.fail(err)
		}
	}
}

func (p *SinkPool) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.err == nil {
		p.err = err
	}
}

// minRowsPerWorker stops parallelRows handing out bands too thin to pay for
// the goroutine that runs them. Below it the work is done inline.
const minRowsPerWorker = 16

// parallelRows splits rows into contiguous bands and runs work on each of them
// concurrently, waiting for all of them.
//
// Bands are disjoint and each writes only its own output rows, so no
// synchronisation is needed inside work and the result does not depend on how
// the split fell: the parallel and the serial answers are byte-identical.
func parallelRows(rows, workers int, work func(from, to int)) {
	workers = Workers(workers)

	if useful := (rows + minRowsPerWorker - 1) / minRowsPerWorker; workers > useful {
		workers = useful
	}

	if workers < 2 {
		work(0, rows)

		return
	}

	band := (rows + workers - 1) / workers

	var wait sync.WaitGroup

	for from := 0; from < rows; from += band {
		last := min(from+band, rows)

		wait.Add(1)

		go func() {
			defer wait.Done()

			work(from, last)
		}()
	}

	wait.Wait()
}
