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
	"image"
	"log/slog"
	"sync"
	"unsafe"

	"github.com/cwbudde/circlefit/internal/fit/gpu"
)

// errNoContext and errNoWorkgroupSize are the two engine failures that are not
// an OpenCL status code, so they cannot be phrased through clError.
var (
	errNoContext       = errors.New("failed to access OpenCL context/queue")
	errNoWorkgroupSize = errors.New("OpenCL device has no usable reduction workgroup size")
)

// engine owns the OpenCL state that depends only on the device and the
// reference image: the runtime, the compiled program, the packed reference
// buffer, and the reduction workgroup size read off the device. None of it
// depends on a circle count, which is what distinguishes it from the per-
// Renderer kernels and buffers.
//
// It is reference counted rather than owned outright because a Renderer and the
// sessions derived from it are separate values with separate lifetimes: the
// engine has to outlive whichever of them is torn down first. Today the count
// never exceeds one holder per engine -- NewSession still builds its own -- so
// the counting is inert; it exists so that sharing an engine between sessions
// becomes a change to who calls retain, not a rewrite of teardown.
type engine struct {
	runtime *gpu.Runtime

	context C.cl_context
	queue   C.cl_command_queue
	device  C.cl_device_id

	program         C.cl_program
	referenceBuffer C.cl_mem
	localSize       int

	// builds counts clBuildProgram calls against this engine. Compiling the
	// kernel source is the dominant part of per-session setup, so the count is
	// the direct evidence that a shared engine compiles once rather than once
	// per session.
	builds uint64

	// mu guards refs only. The OpenCL objects themselves are immutable once
	// newEngine returns and are freed exactly when refs reaches zero.
	mu   sync.Mutex
	refs int
}

// newEngine brings up a device, compiles the kernel source, and uploads the
// reference image. The returned engine carries one reference, which belongs to
// the caller.
func newEngine(reference *image.NRGBA) (*engine, error) {
	// The device bring-up failure is returned verbatim rather than wrapped:
	// internal/server/worker.go strips the ErrBackendUnavailable prefix the
	// renderer package adds and shows the remainder as the reason a backend
	// could not start, so an extra layer here would change that message.
	runtime, err := gpu.InitOpenCL()
	if err != nil {
		return nil, err //nolint:wrapcheck // see above: the caller normalises this error.
	}

	eng := &engine{
		runtime: runtime,
		context: C.cl_context(runtime.ContextPtr()),
		queue:   C.cl_command_queue(runtime.QueuePtr()),
		device:  C.cl_device_id(runtime.DevicePtr()),
		refs:    1,
	}

	if eng.context == nil || eng.queue == nil {
		eng.release()
		return nil, errNoContext
	}

	err = eng.build(reference)
	if err != nil {
		eng.release()
		return nil, err
	}

	slog.Info("OpenCL backend initialised",
		"device", eng.runtime.Device.Name,
		"vendor", eng.runtime.Device.Vendor,
		"compute_units", eng.runtime.Device.MaxComputeUnits,
		"reduction_local_size", eng.localSize,
	)

	return eng, nil
}

// build compiles the program, measures the reduction workgroup size, and
// uploads the reference image, in that order. The order is load-bearing: every
// Renderer sizes its partial-sum buffers from localSize, so the size has to be
// known before any of them is constructed.
func (e *engine) build(reference *image.NRGBA) error {
	err := e.buildProgram()
	if err != nil {
		return err
	}

	err = e.probeLocalSize()
	if err != nil {
		return err
	}

	return e.uploadReference(reference)
}

// buildProgram compiles the kernel source for this device once.
//
// The cgo pointer check around a `&x` argument expands to a constant `0 == 0`
// guard, which gocritic reads as a duplicated comparison; the report is against
// generated code rather than anything written here.
//
//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
func (e *engine) buildProgram() error {
	source := C.CString(openclKernelSource)
	defer C.free(unsafe.Pointer(source))

	var status C.cl_int

	e.program = C.clCreateProgramWithSource(e.context, 1, &source, nil, &status)
	if status != C.CL_SUCCESS {
		return clError("clCreateProgramWithSource", status)
	}

	e.builds++

	status = C.clBuildProgram(e.program, 1, &e.device, nil, nil, nil)
	if status != C.CL_SUCCESS {
		e.dumpBuildLog()
		return clError("clBuildProgram", status)
	}

	return nil
}

// probeLocalSize picks the reduction workgroup size the whole device will use.
//
// The query it needs, CL_KERNEL_WORK_GROUP_SIZE, is a property of a program and
// a device rather than of a particular kernel instance, so any kernel pair
// compiled from this program answers it identically. The engine therefore
// creates one throwaway pair, asks it, and releases it again, instead of
// keeping kernels it has no other use for.
func (e *engine) probeLocalSize() error {
	renderKernel, reduceKernel, err := e.newKernels()
	if err != nil {
		return err
	}

	defer func() {
		C.clReleaseKernel(renderKernel)
		C.clReleaseKernel(reduceKernel)
	}()

	localSize, err := e.selectLocalSize(renderKernel, reduceKernel)
	if err != nil {
		return err
	}

	e.localSize = localSize

	return nil
}

func (e *engine) uploadReference(reference *image.NRGBA) error {
	refBytes := packReferenceNRGBA(reference)
	if len(refBytes) == 0 {
		refBytes = make([]byte, 4)
	}

	var status C.cl_int

	bytePixels := C.size_t(len(refBytes))

	e.referenceBuffer = C.clCreateBuffer(
		e.context,
		C.CL_MEM_READ_ONLY|C.CL_MEM_COPY_HOST_PTR|C.CL_MEM_HOST_NO_ACCESS,
		bytePixels,
		unsafe.Pointer(&refBytes[0]),
		&status,
	)
	if status != C.CL_SUCCESS {
		return clError("clCreateBuffer(reference)", status)
	}

	return nil
}

// newKernels creates a render/reduce kernel pair from the built program, in
// that order. The pair belongs to the caller, which must release both.
func (e *engine) newKernels() (C.cl_kernel, C.cl_kernel, error) {
	var status C.cl_int

	renderKernelName := C.CString("render_cost")
	defer C.free(unsafe.Pointer(renderKernelName))

	renderKernel := C.clCreateKernel(e.program, renderKernelName, &status)
	if status != C.CL_SUCCESS {
		return nil, nil, clError("clCreateKernel(render_cost)", status)
	}

	reduceKernelName := C.CString("reduce_sum")
	defer C.free(unsafe.Pointer(reduceKernelName))

	reduceKernel := C.clCreateKernel(e.program, reduceKernelName, &status)
	if status != C.CL_SUCCESS {
		// The caller never sees a half-built pair, so the first kernel has to be
		// given back here or it leaks for the life of the process.
		C.clReleaseKernel(renderKernel)

		return nil, nil, clError("clCreateKernel(reduce_sum)", status)
	}

	return renderKernel, reduceKernel, nil
}

// selectLocalSize reads the largest reduction workgroup size the given kernel
// pair and the device can both sustain.
//
// The cgo pointer check around a `&x` argument expands to a constant `0 == 0`
// guard, which gocritic reads as a duplicated comparison; the report is against
// generated code rather than anything written here.
//
//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
func (e *engine) selectLocalSize(renderKernel, reduceKernel C.cl_kernel) (int, error) {
	var renderLimit C.size_t

	status := C.clGetKernelWorkGroupInfo(
		renderKernel,
		e.device,
		C.CL_KERNEL_WORK_GROUP_SIZE,
		C.size_t(unsafe.Sizeof(renderLimit)),
		unsafe.Pointer(&renderLimit),
		nil,
	)
	if status != C.CL_SUCCESS {
		return 0, clError("clGetKernelWorkGroupInfo(render_cost)", status)
	}

	var reduceLimit C.size_t

	status = C.clGetKernelWorkGroupInfo(
		reduceKernel,
		e.device,
		C.CL_KERNEL_WORK_GROUP_SIZE,
		C.size_t(unsafe.Sizeof(reduceLimit)),
		unsafe.Pointer(&reduceLimit),
		nil,
	)
	if status != C.CL_SUCCESS {
		return 0, clError("clGetKernelWorkGroupInfo(reduce_sum)", status)
	}

	var localMemory C.cl_ulong

	status = C.clGetDeviceInfo(
		e.device,
		C.CL_DEVICE_LOCAL_MEM_SIZE,
		C.size_t(unsafe.Sizeof(localMemory)),
		unsafe.Pointer(&localMemory),
		nil,
	)
	if status != C.CL_SUCCESS {
		return 0, clError("clGetDeviceInfo(local memory)", status)
	}

	limit := min(256, int(renderLimit), int(reduceLimit), int(localMemory)/int(unsafe.Sizeof(float32(0))))

	localSize := largestPowerOfTwo(limit)
	if localSize == 0 {
		return 0, errNoWorkgroupSize
	}

	return localSize, nil
}

// retain records one more holder of the engine.
func (e *engine) retain() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.refs++
}

// release drops one holder and frees the device state once the last one is
// gone. It is safe on a nil engine and on an engine that has already reached
// zero, so a teardown path may run twice without freeing twice.
func (e *engine) release() {
	if e == nil {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.refs <= 0 {
		return
	}

	e.refs--
	if e.refs > 0 {
		return
	}

	e.free()
}

// free releases the device state in the reverse of the order it was acquired.
// The caller holds e.mu.
func (e *engine) free() {
	if e.referenceBuffer != nil {
		C.clReleaseMemObject(e.referenceBuffer)
		e.referenceBuffer = nil
	}

	if e.program != nil {
		C.clReleaseProgram(e.program)
		e.program = nil
	}

	if e.runtime != nil {
		e.runtime.Close()
		e.runtime = nil
	}

	// The unwrapped handles are copies of what Runtime.Close has just released,
	// and Renderers hold copies of their own. Clearing them here keeps the
	// engine itself from handing out a dangling pointer after teardown.
	e.context = nil
	e.queue = nil
	e.device = nil
}

func (e *engine) dumpBuildLog() {
	if e.program == nil || e.device == nil {
		return
	}

	var logSize C.size_t

	status := C.clGetProgramBuildInfo(e.program, e.device, C.CL_PROGRAM_BUILD_LOG, 0, nil, &logSize)
	if status != C.CL_SUCCESS {
		slog.Error("OpenCL: failed to fetch build log size", "err", status)
		return
	}

	if logSize == 0 {
		return
	}

	buf := make([]byte, int(logSize))

	status = C.clGetProgramBuildInfo(
		e.program,
		e.device,
		C.CL_PROGRAM_BUILD_LOG,
		logSize,
		unsafe.Pointer(&buf[0]),
		nil,
	)
	if status != C.CL_SUCCESS {
		slog.Error("OpenCL: failed to fetch build log", "err", status)
		return
	}

	slog.Error("OpenCL build log", "log", string(buf))
}
