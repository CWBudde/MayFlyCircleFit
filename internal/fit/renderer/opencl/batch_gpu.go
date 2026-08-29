//go:build gpu

package opencl

/*
#cgo LDFLAGS: -lOpenCL
#define CL_TARGET_OPENCL_VERSION 120
#define CL_USE_DEPRECATED_OPENCL_1_2_APIS
#include <CL/cl.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// The batch evaluator's own failures. They are sentinels rather than inline
// errors.New for the same reason as the session-input ones above: the linter
// refuses dynamic errors, and a caller matching on a class is better served by
// one it can name.
var (
	errBatchStaging   = errors.New("batch evaluator: host staging allocation failed")
	errBatchTooWide   = errors.New("batch evaluator: generation exceeds evaluator width")
	errBatchDimension = errors.New("batch evaluator: candidate does not match renderer dimension")
)

// A generation of candidates is evaluated one at a time today: Renderer.ensure
// holds the engine's queue mutex across a blocking parameter write, a
// render_cost dispatch, a host-driven reduction loop and a blocking four-byte
// read, and it does that once per candidate. The queue is in-order, so the
// device cannot start candidate i+1 until i has completed -- but the host round
// trip at the end of each chain is time the device spends idle waiting to be
// fed, and it is paid lambda times per generation.
//
// The measured evidence that this is worth attacking is in
// docs/gpu-performance-report.md: a least-squares fit over four circle counts
// splits a 512x512 evaluation into a 63.1 us fixed floor and 3.224 us per
// circle, so at the eight circles a campaign stage runs, 71% of the evaluation
// is floor. Independently, an end-to-end campaign-shaped run measured the same
// per-evaluation cost at lambda=20 and lambda=1024 (0.191 and 0.194 ms), which
// is what "nothing amortizes across a generation" looks like from outside.
//
// batchEvaluator exists to measure what removing the per-candidate host stall
// is worth, and nothing more. It is deliberately unexported and reached only
// from benchmarks and the parity test: PLAN.md Task 11 asks for a batched
// objective interface, and the interface is worth designing only once the
// ceiling is known. See docs/gpu-backends.md for why a GPU number needs
// separate passes rather than -count.
type batchEvaluator struct {
	renderer *Renderer

	slots []batchSlot

	// staging and costs are C memory rather than Go slices because the
	// pipelined path issues non-blocking transfers. OpenCL keeps the host
	// pointer until the command completes, and cgo forbids C retaining a Go
	// pointer past the call that received it -- a rule the blocking transfers
	// in ensure and materializeImage satisfy by construction and this one does
	// not.
	staging *C.float
	costs   *C.float

	width      int
	dimension  int
	stagingLen int

	// retained records that a drain failed and commands may still hold staging
	// and costs. close honours it by leaking them; see drainQueuedTransfers.
	retained bool
}

// batchSlot is the per-candidate device state a pipelined generation needs.
// The kernels and the output image stay shared: the queue is in-order, so no
// two dispatches overlap, and a cost-only batch never reads the image back.
type batchSlot struct {
	params   C.cl_mem
	partialA C.cl_mem
	partialB C.cl_mem
}

// newBatchEvaluator allocates the per-candidate buffers for a generation of at
// most width candidates. The returned cleanup releases them and is safe to call
// once; the evaluator borrows the renderer and frees nothing that belongs to it.
func (r *Renderer) newBatchEvaluator(width int) (*batchEvaluator, func(), error) {
	if width < 1 {
		return nil, noopCleanup, fmt.Errorf("%w: batch width must be positive", ErrInvalidSessionInput)
	}
	if r.engine == nil {
		return nil, noopCleanup, errNoContext
	}

	dimension := r.Dim()
	partialCount := max(1, ceilDiv(r.pixelCount, r.localSize))

	evaluator := &batchEvaluator{
		renderer:   r,
		slots:      make([]batchSlot, 0, width),
		width:      width,
		dimension:  dimension,
		stagingLen: width * max(1, dimension),
	}

	cleanup := func() { evaluator.close() }

	evaluator.staging = (*C.float)(C.calloc(C.size_t(evaluator.stagingLen), C.sizeof_float))
	evaluator.costs = (*C.float)(C.calloc(C.size_t(width), C.sizeof_float))
	if evaluator.staging == nil || evaluator.costs == nil {
		cleanup()
		return nil, noopCleanup, fmt.Errorf("%w: %d candidates", errBatchStaging, width)
	}

	byteParams := C.size_t(max(1, dimension) * int(unsafe.Sizeof(float32(0))))
	bytePartials := C.size_t(partialCount * int(unsafe.Sizeof(float32(0))))

	for range width {
		slot, err := r.newBatchSlot(byteParams, bytePartials)
		if err != nil {
			cleanup()
			return nil, noopCleanup, err
		}

		evaluator.slots = append(evaluator.slots, slot)
	}

	return evaluator, cleanup, nil
}

func (r *Renderer) newBatchSlot(byteParams, bytePartials C.size_t) (batchSlot, error) {
	var status C.cl_int

	var slot batchSlot

	slot.params = C.clCreateBuffer(r.context, C.CL_MEM_READ_ONLY|C.CL_MEM_HOST_WRITE_ONLY, byteParams, nil, &status)
	if status != C.CL_SUCCESS {
		return batchSlot{}, r.clError("clCreateBuffer(batch params)", status)
	}

	slot.partialA = C.clCreateBuffer(r.context, C.CL_MEM_READ_WRITE, bytePartials, nil, &status)
	if status != C.CL_SUCCESS {
		C.clReleaseMemObject(slot.params)
		return batchSlot{}, r.clError("clCreateBuffer(batch partialA)", status)
	}

	slot.partialB = C.clCreateBuffer(r.context, C.CL_MEM_READ_WRITE, bytePartials, nil, &status)
	if status != C.CL_SUCCESS {
		C.clReleaseMemObject(slot.partialA)
		C.clReleaseMemObject(slot.params)
		return batchSlot{}, r.clError("clCreateBuffer(batch partialB)", status)
	}

	return slot, nil
}

func (b *batchEvaluator) close() {
	for _, slot := range b.slots {
		if slot.partialB != nil {
			C.clReleaseMemObject(slot.partialB)
		}
		if slot.partialA != nil {
			C.clReleaseMemObject(slot.partialA)
		}
		if slot.params != nil {
			C.clReleaseMemObject(slot.params)
		}
	}

	b.slots = nil

	// The mem objects are safe to release with commands still in flight --
	// OpenCL keeps them alive until those complete -- but the host pointers are
	// not, and a failed drain is the one case where nothing guarantees they are
	// free. Leak them there rather than hand the device freed memory.
	if b.staging != nil {
		if !b.retained {
			C.free(unsafe.Pointer(b.staging))
		}

		b.staging = nil
	}

	if b.costs != nil {
		if !b.retained {
			C.free(unsafe.Pointer(b.costs))
		}

		b.costs = nil
	}
}

// drainQueuedTransfers waits for whatever enqueueGeneration managed to issue
// before its caller is allowed to release the host buffers. The pipelined
// writes and cost readbacks are non-blocking, so OpenCL retains b.staging and
// b.costs until each command completes; returning an enqueue error straight to
// a caller that then runs the evaluator's cleanup would free them out from
// under the device, during the very device failure being reported.
//
// A drain that itself fails leaves commands that may never complete, and there
// is then no later moment at which freeing becomes safe. The buffers are
// deliberately leaked in that case and the renderer stops being trusted: a few
// kilobytes held for the life of the process is the cheaper of the two
// outcomes.
func (b *batchEvaluator) drainQueuedTransfers(cause error) error {
	status := C.clFinish(b.renderer.queue)
	if status == C.CL_SUCCESS {
		return cause
	}

	b.retained = true
	b.renderer.degraded.Store(true)

	return errors.Join(cause, b.renderer.clError("clFinish(batch drain)", status))
}

// costSerial evaluates a generation the way the optimizer does today: one
// Renderer.Cost call per candidate, each with its own blocking round trip. It
// is the benchmark's baseline arm and deliberately routes through the ordinary
// path rather than reimplementing it, so the comparison cannot flatter itself.
func (b *batchEvaluator) costSerial(paramSets [][]float64) []float64 {
	costs := make([]float64, len(paramSets))
	for i, params := range paramSets {
		costs[i] = b.renderer.Cost(params)
	}

	return costs
}

// costPipelined evaluates a whole generation with one host synchronization
// instead of one per candidate. Every candidate still gets its own dispatch and
// its own reduction chain -- the kernels are unchanged -- but the writes and the
// cost readbacks are non-blocking, so the queue stays fed and the host waits
// once, at the end.
//
// It reports costs identical to costSerial rather than merely close: both arms
// run the same float32 device arithmetic over the same inputs, so a difference
// is a defect in the batching and not a precision budget. The parity test pins
// that.
func (b *batchEvaluator) costPipelined(paramSets [][]float64) ([]float64, error) {
	renderer := b.renderer

	if len(paramSets) > b.width {
		return nil, fmt.Errorf("%w: %d candidates, width %d", errBatchTooWide, len(paramSets), b.width)
	}
	if renderer.engine == nil {
		return nil, errNoContext
	}
	if len(paramSets) == 0 {
		return nil, nil
	}

	for _, params := range paramSets {
		if len(params) != b.dimension {
			return nil, fmt.Errorf("%w: %d parameters, dimension %d", errBatchDimension, len(params), b.dimension)
		}
	}

	// The whole generation is one command sequence on the shared queue, for the
	// same reason a single evaluation is: another renderer's chain interleaved
	// with this one would read another candidate's partials.
	renderer.engine.queueMu.Lock()
	defer renderer.engine.queueMu.Unlock()

	// The batch rebinds the params and partial-sums arguments away from the
	// renderer's own buffers, so the cached device result no longer describes
	// what those buffers hold.
	renderer.deviceValid = false
	renderer.imageValid = false

	defer func() {
		err := renderer.restoreStaticBatchArgs()
		if err != nil {
			// The next ensure rebinds arguments 0, 1 and 7 itself and would
			// still read this renderer's stale partial buffer, so the safe
			// answer is to stop trusting the device rather than to continue.
			renderer.degraded.Store(true)
		}
	}()

	err := b.enqueueGeneration(paramSets)
	if err != nil {
		// Some of the generation's non-blocking transfers may already have been
		// accepted, and they hold b.staging and b.costs until they complete.
		return nil, b.drainQueuedTransfers(err)
	}

	status := C.clFinish(renderer.queue)
	if status != C.CL_SUCCESS {
		return nil, renderer.clError("clFinish(batch)", status)
	}

	costs := make([]float64, len(paramSets))
	host := unsafe.Slice((*float32)(unsafe.Pointer(b.costs)), b.width)

	for i := range paramSets {
		costs[i] = float64(host[i]) / float64(renderer.pixelCount*3)
	}

	renderer.evaluations += uint64(len(paramSets))

	return costs, nil
}

// enqueueGeneration issues every candidate's chain without waiting for any of
// them. Nothing here reads a result; costPipelined's single clFinish is what
// makes the values valid.
func (b *batchEvaluator) enqueueGeneration(paramSets [][]float64) error {
	renderer := b.renderer

	staging := unsafe.Slice((*float32)(unsafe.Pointer(b.staging)), b.stagingLen)
	byteParams := C.size_t(b.dimension * int(unsafe.Sizeof(float32(0))))
	circleCount := C.cl_int(b.dimension / paramsPerCircle)

	for i, params := range paramSets {
		slot := b.slots[i]
		base := i * b.dimension

		for j, param := range params {
			staging[base+j] = float32(param)
		}

		status := C.clEnqueueWriteBuffer(
			renderer.queue, slot.params, C.CL_FALSE, 0, byteParams,
			unsafe.Add(unsafe.Pointer(b.staging), base*int(unsafe.Sizeof(float32(0)))), 0, nil, nil,
		)
		if status != C.CL_SUCCESS {
			return renderer.clError("clEnqueueWriteBuffer(batch params)", status)
		}

		err := b.enqueueCandidate(slot, circleCount)
		if err != nil {
			return err
		}

		status = C.clEnqueueReadBuffer(
			renderer.queue, slot.partialA, C.CL_FALSE, 0, C.size_t(unsafe.Sizeof(C.float(0))),
			unsafe.Add(unsafe.Pointer(b.costs), i*int(unsafe.Sizeof(C.float(0)))), 0, nil, nil,
		)
		if status != C.CL_SUCCESS {
			return renderer.clError("clEnqueueReadBuffer(batch cost)", status)
		}
	}

	return nil
}

// enqueueCandidate dispatches render_cost and the reduction chain for one
// candidate, leaving the reduced cost in slot.partialA. The reduction ping-pongs
// an even number of times by construction below: whichever buffer the loop ends
// on is copied back into partialA so the caller's readback offset is fixed.
func (b *batchEvaluator) enqueueCandidate(slot batchSlot, circleCount C.cl_int) error {
	renderer := b.renderer

	local := C.size_t(renderer.localSize)
	localBytes := C.size_t(renderer.localSize * int(unsafe.Sizeof(float32(0))))

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status := C.clSetKernelArg(
		renderer.renderKernel, 0, C.size_t(unsafe.Sizeof(slot.params)), unsafe.Pointer(&slot.params),
	)
	if status != C.CL_SUCCESS {
		return renderer.clError("clSetKernelArg(batch params)", status)
	}

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status = C.clSetKernelArg(
		renderer.renderKernel, 1, C.size_t(unsafe.Sizeof(circleCount)), unsafe.Pointer(&circleCount),
	)
	if status != C.CL_SUCCESS {
		return renderer.clError("clSetKernelArg(batch circleCount)", status)
	}

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status = C.clSetKernelArg(
		renderer.renderKernel, 6, C.size_t(unsafe.Sizeof(slot.partialA)), unsafe.Pointer(&slot.partialA),
	)
	if status != C.CL_SUCCESS {
		return renderer.clError("clSetKernelArg(batch partialSums)", status)
	}

	status = C.clSetKernelArg(renderer.renderKernel, 7, localBytes, nil)
	if status != C.CL_SUCCESS {
		return renderer.clError("clSetKernelArg(batch render scratch)", status)
	}

	partialCount := ceilDiv(renderer.pixelCount, renderer.localSize)
	global := C.size_t(partialCount * renderer.localSize)

	status = C.clEnqueueNDRangeKernel(renderer.queue, renderer.renderKernel, 1, nil, &global, &local, 0, nil, nil)
	if status != C.CL_SUCCESS {
		return renderer.clError("clEnqueueNDRangeKernel(batch render_cost)", status)
	}

	return b.enqueueReduction(slot, partialCount, local, localBytes)
}

// enqueueReduction collapses partialCount partial sums to one float, mirroring
// the loop in ensure but over this slot's buffers. It copies the result back
// into partialA when the ping-pong ends on partialB, so every slot's cost sits
// at the same offset in the same buffer whatever the reduction depth was.
func (b *batchEvaluator) enqueueReduction(slot batchSlot, partialCount int, local, localBytes C.size_t) error {
	renderer := b.renderer

	input, output := slot.partialA, slot.partialB

	for partialCount > 1 {
		nextCount := ceilDiv(partialCount, renderer.localSize*2)
		global := C.size_t(nextCount * renderer.localSize)
		count := C.cl_int(partialCount)

		err := renderer.setReduceArgs(input, output, count, localBytes)
		if err != nil {
			return err
		}

		status := C.clEnqueueNDRangeKernel(renderer.queue, renderer.reduceKernel, 1, nil, &global, &local, 0, nil, nil)
		if status != C.CL_SUCCESS {
			return renderer.clError("clEnqueueNDRangeKernel(batch reduce_sum)", status)
		}

		partialCount = nextCount
		input, output = output, input
	}

	if input == slot.partialA {
		return nil
	}

	status := C.clEnqueueCopyBuffer(
		renderer.queue, input, slot.partialA, 0, 0, C.size_t(unsafe.Sizeof(C.float(0))), 0, nil, nil,
	)
	if status != C.CL_SUCCESS {
		return renderer.clError("clEnqueueCopyBuffer(batch reduced cost)", status)
	}

	return nil
}

// setReduceArgs binds one reduction step. It is a method on Renderer because
// ensure needs the same four arguments in the same order.
func (r *Renderer) setReduceArgs(input, output C.cl_mem, count C.cl_int, localBytes C.size_t) error {
	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status := C.clSetKernelArg(r.reduceKernel, 0, C.size_t(unsafe.Sizeof(input)), unsafe.Pointer(&input))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(reduce input)", status)
	}

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status = C.clSetKernelArg(r.reduceKernel, 1, C.size_t(unsafe.Sizeof(output)), unsafe.Pointer(&output))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(reduce output)", status)
	}

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status = C.clSetKernelArg(r.reduceKernel, 2, C.size_t(unsafe.Sizeof(count)), unsafe.Pointer(&count))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(reduce count)", status)
	}

	status = C.clSetKernelArg(r.reduceKernel, 3, localBytes, nil)
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(reduce scratch)", status)
	}

	return nil
}

// restoreStaticBatchArgs rebinds the two arguments a batch moved off the
// renderer's own buffers. Argument 6 is the one that matters: ensure sets 0, 1
// and 7 on every evaluation but relies on the static binding for the partial
// sums, so leaving a slot's buffer bound would make the next ordinary Cost read
// a batch candidate's partials.
func (r *Renderer) restoreStaticBatchArgs() error {
	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status := C.clSetKernelArg(
		r.renderKernel, 0, C.size_t(unsafe.Sizeof(r.paramsBuffer)), unsafe.Pointer(&r.paramsBuffer),
	)
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(restore params)", status)
	}

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status = C.clSetKernelArg(
		r.renderKernel, 6, C.size_t(unsafe.Sizeof(r.partialBufferA)), unsafe.Pointer(&r.partialBufferA),
	)
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(restore partialSums)", status)
	}

	return nil
}
