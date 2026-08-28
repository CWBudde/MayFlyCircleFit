//go:build gpu

package opencl

/*
#cgo LDFLAGS: -lOpenCL
#define CL_TARGET_OPENCL_VERSION 120
#define CL_USE_DEPRECATED_OPENCL_1_2_APIS
#include <CL/cl.h>
#include <stdlib.h>

static const char* mayfly_gpu_renderer_error_string(cl_int status) {
	switch (status) {
	case CL_SUCCESS: return "CL_SUCCESS";
	case CL_DEVICE_NOT_FOUND: return "CL_DEVICE_NOT_FOUND";
	case CL_DEVICE_NOT_AVAILABLE: return "CL_DEVICE_NOT_AVAILABLE";
	case CL_COMPILER_NOT_AVAILABLE: return "CL_COMPILER_NOT_AVAILABLE";
	case CL_MEM_OBJECT_ALLOCATION_FAILURE: return "CL_MEM_OBJECT_ALLOCATION_FAILURE";
	case CL_OUT_OF_RESOURCES: return "CL_OUT_OF_RESOURCES";
	case CL_OUT_OF_HOST_MEMORY: return "CL_OUT_OF_HOST_MEMORY";
	case CL_BUILD_PROGRAM_FAILURE: return "CL_BUILD_PROGRAM_FAILURE";
	case CL_MAP_FAILURE: return "CL_MAP_FAILURE";
	case CL_INVALID_VALUE: return "CL_INVALID_VALUE";
	case CL_INVALID_DEVICE: return "CL_INVALID_DEVICE";
	case CL_INVALID_CONTEXT: return "CL_INVALID_CONTEXT";
	case CL_INVALID_MEM_OBJECT: return "CL_INVALID_MEM_OBJECT";
	case CL_INVALID_IMAGE_SIZE: return "CL_INVALID_IMAGE_SIZE";
	case CL_INVALID_OPERATION: return "CL_INVALID_OPERATION";
	case CL_INVALID_KERNEL_NAME: return "CL_INVALID_KERNEL_NAME";
	case CL_INVALID_KERNEL: return "CL_INVALID_KERNEL";
	case CL_INVALID_ARG_INDEX: return "CL_INVALID_ARG_INDEX";
	case CL_INVALID_ARG_VALUE: return "CL_INVALID_ARG_VALUE";
	case CL_INVALID_ARG_SIZE: return "CL_INVALID_ARG_SIZE";
	case CL_INVALID_KERNEL_ARGS: return "CL_INVALID_KERNEL_ARGS";
	case CL_INVALID_WORK_GROUP_SIZE: return "CL_INVALID_WORK_GROUP_SIZE";
	case CL_INVALID_WORK_DIMENSION: return "CL_INVALID_WORK_DIMENSION";
	default: return "CL_UNKNOWN_ERROR";
	}
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"image"
	"log/slog"
	"math"
	"sync/atomic"
	"unsafe"

	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/fit/gpu"
)

// paramsPerCircle mirrors the renderer parameter layout. It is duplicated here
// rather than imported so this package stays a leaf: the renderer package
// imports it, never the other way around.
const paramsPerCircle = 7

// noopCleanup is returned alongside errors so callers can defer unconditionally.
var noopCleanup = func() {}

// Fallback is the CPU renderer the GPU path degrades to. Callers inject it so
// this package does not depend on the renderer package.
type Fallback interface {
	Render(params []float64) *image.NRGBA
	Cost(params []float64) float64
}

// NewFallback builds a fallback renderer for a given reference and circle
// count. Sessions call it again so each one gets a fallback sized to its own
// circle count, matching the pre-split behavior.
type NewFallback func(reference *image.NRGBA, circles int) Fallback

const openclKernelSource = `
__kernel void render_cost(
    __global const float *params,
    const int circleCount,
    const int width,
    const int height,
    __global const uchar4 *reference,
    __global uchar4 *outImage,
    __global float *partialSums,
    __local float *scratch) {

    const int idx = get_global_id(0);
    const int localID = get_local_id(0);
    const int localSize = get_local_size(0);
    const int pixelCount = width * height;
    float error = 0.0f;

    if (idx < pixelCount) {
        const int x = idx % width;
        const int y = idx / width;

        float4 color = (float4)(1.0f, 1.0f, 1.0f, 1.0f);

        for (int i = 0; i < circleCount; ++i) {
            const int base = i * 7;
            const float cx = params[base + 0];
            const float cy = params[base + 1];
            const float radius = params[base + 2];
            const float cr = params[base + 3];
            const float cg = params[base + 4];
            const float cb = params[base + 5];
            const float opacity = params[base + 6];

            if (opacity < 0.001f || radius < 0.0f) {
                continue;
            }

            const float dx = (float)x - cx;
            const float dy = (float)y - cy;
            if (dx * dx + dy * dy > radius * radius) {
                continue;
            }

            const float4 fg = (float4)(cr, cg, cb, 1.0f) * opacity;
            const float invOpacity = 1.0f - fg.w;

            color.xyz = fg.xyz + color.xyz * invOpacity;
            color.w = fg.w + color.w * invOpacity;
            // Match the CPU renderer's NRGBA storage semantics between layers.
            color.xyz = floor(clamp(color.xyz, 0.0f, 1.0f) * 255.0f + 0.5f) / 255.0f;
            color.w = floor(clamp(color.w, 0.0f, 1.0f) * 255.0f + 0.5f) / 255.0f;
        }

        color.xyz = clamp(color.xyz, 0.0f, 1.0f);
        color.w = clamp(color.w, 0.0f, 1.0f);

        const float3 renderedBytes = floor(color.xyz * 255.0f + 0.5f);
        outImage[idx] = convert_uchar4_sat((float4)(renderedBytes, 255.0f));

        const float3 referenceBytes = convert_float3(reference[idx].xyz);
        const float dr = renderedBytes.x - referenceBytes.x;
        const float dg = renderedBytes.y - referenceBytes.y;
        const float db = renderedBytes.z - referenceBytes.z;
        error = dr * dr + dg * dg + db * db;
    }

    scratch[localID] = error;
    barrier(CLK_LOCAL_MEM_FENCE);

    for (int offset = localSize / 2; offset > 0; offset /= 2) {
        if (localID < offset) {
            scratch[localID] += scratch[localID + offset];
        }
        barrier(CLK_LOCAL_MEM_FENCE);
    }

    if (localID == 0) {
        partialSums[get_group_id(0)] = scratch[0];
    }
}

__kernel void reduce_sum(
    __global const float *input,
    __global float *output,
    const int count,
    __local float *scratch) {

    const int localID = get_local_id(0);
    const int localSize = get_local_size(0);
    const int base = get_group_id(0) * localSize * 2 + localID;

    float sum = 0.0f;
    if (base < count) {
        sum = input[base];
    }
    if (base + localSize < count) {
        sum += input[base + localSize];
    }

    scratch[localID] = sum;
    barrier(CLK_LOCAL_MEM_FENCE);

    for (int offset = localSize / 2; offset > 0; offset /= 2) {
        if (localID < offset) {
            scratch[localID] += scratch[localID + offset];
        }
        barrier(CLK_LOCAL_MEM_FENCE);
    }

    if (localID == 0) {
        output[get_group_id(0)] = scratch[0];
    }
}
`

type Renderer struct {
	runtime     *gpu.Runtime
	fallback    Fallback
	newFallback NewFallback

	reference  *image.NRGBA
	bounds     *fit.Bounds
	width      int
	height     int
	pixelCount int

	context      C.cl_context
	queue        C.cl_command_queue
	device       C.cl_device_id
	program      C.cl_program
	renderKernel C.cl_kernel
	reduceKernel C.cl_kernel
	localSize    int

	paramsBuffer    C.cl_mem
	referenceBuffer C.cl_mem
	outputBuffer    C.cl_mem
	partialBufferA  C.cl_mem
	partialBufferB  C.cl_mem

	paramsScratch []float32

	renderImage *image.NRGBA

	deviceHash  uint64
	lastCost    float64
	deviceValid bool
	imageHash   uint64
	imageValid  bool
	evaluations uint64

	// degraded is shared with every session derived from this renderer, and
	// with the renderer a session was derived from. Degradation is a fact about
	// the device, not about one Renderer value: the staged pipelines evaluate
	// through independent sessions, so a per-renderer flag would leave a
	// sequential or batch run reporting a clean device while every circle after
	// the failure was costed on the CPU. Sharing it also stops a lost device
	// being rediscovered once per stage.
	//
	// It is atomic because it is now reachable from more than one Renderer.
	// OpenCL still evaluates serially -- it withholds the concurrent-evaluation
	// marker -- so this removes a hazard rather than enabling concurrency.
	degraded *atomic.Bool
}

// New creates an OpenCL GPU-based renderer. The fallback renderer serves every
// request the device cannot, so callers must supply a working CPU renderer
// factory.
func New(reference *image.NRGBA, k int, newFallback NewFallback) (*Renderer, func(), error) {
	return newRenderer(reference, k, newFallback, &atomic.Bool{})
}

// newRenderer builds a renderer over an existing degradation record. A base
// renderer starts a fresh one; a session inherits its parent's.
func newRenderer(
	reference *image.NRGBA, circleCount int, newFallback NewFallback, degraded *atomic.Bool,
) (*Renderer, func(), error) {
	rt, err := gpu.InitOpenCL()
	if err != nil {
		return nil, noopCleanup, err
	}

	r := &Renderer{
		runtime:       rt,
		fallback:      newFallback(reference, circleCount),
		newFallback:   newFallback,
		reference:     reference,
		bounds:        fit.NewBounds(circleCount, reference.Bounds().Dx(), reference.Bounds().Dy()),
		width:         reference.Bounds().Dx(),
		height:        reference.Bounds().Dy(),
		pixelCount:    reference.Bounds().Dx() * reference.Bounds().Dy(),
		paramsScratch: make([]float32, circleCount*paramsPerCircle),
		renderImage:   image.NewNRGBA(image.Rect(0, 0, reference.Bounds().Dx(), reference.Bounds().Dy())),
		degraded:      degraded,
	}

	if err := r.init(); err != nil {
		r.release()
		return nil, noopCleanup, err
	}

	cleanup := func() {
		r.release()
	}

	return r, cleanup, nil
}

func (r *Renderer) init() error {
	r.context = C.cl_context(r.runtime.ContextPtr())
	r.queue = C.cl_command_queue(r.runtime.QueuePtr())
	r.device = C.cl_device_id(r.runtime.DevicePtr())

	if r.context == nil || r.queue == nil {
		return fmt.Errorf("failed to access OpenCL context/queue")
	}

	source := C.CString(openclKernelSource)
	defer C.free(unsafe.Pointer(source))

	var status C.cl_int
	r.program = C.clCreateProgramWithSource(r.context, 1, &source, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateProgramWithSource", status)
	}

	status = C.clBuildProgram(r.program, 1, &r.device, nil, nil, nil)
	if status != C.CL_SUCCESS {
		r.dumpBuildLog()
		return r.clError("clBuildProgram", status)
	}

	renderKernelName := C.CString("render_cost")
	defer C.free(unsafe.Pointer(renderKernelName))
	r.renderKernel = C.clCreateKernel(r.program, renderKernelName, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateKernel(render_cost)", status)
	}

	reduceKernelName := C.CString("reduce_sum")
	defer C.free(unsafe.Pointer(reduceKernelName))
	r.reduceKernel = C.clCreateKernel(r.program, reduceKernelName, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateKernel(reduce_sum)", status)
	}

	localSize, err := r.selectLocalSize()
	if err != nil {
		return err
	}
	r.localSize = localSize

	bufferPixels := max(1, r.pixelCount)
	bufferParams := max(1, len(r.paramsScratch))
	partialCount := max(1, ceilDiv(r.pixelCount, r.localSize))
	bytePixels := C.size_t(bufferPixels * 4)
	bytePartials := C.size_t(partialCount * int(unsafe.Sizeof(float32(0))))
	byteParams := C.size_t(bufferParams * int(unsafe.Sizeof(float32(0))))

	r.outputBuffer = C.clCreateBuffer(r.context, C.CL_MEM_WRITE_ONLY|C.CL_MEM_HOST_READ_ONLY, bytePixels, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(output)", status)
	}

	r.partialBufferA = C.clCreateBuffer(r.context, C.CL_MEM_READ_WRITE, bytePartials, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(partialA)", status)
	}

	r.partialBufferB = C.clCreateBuffer(r.context, C.CL_MEM_READ_WRITE, bytePartials, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(partialB)", status)
	}

	r.paramsBuffer = C.clCreateBuffer(r.context, C.CL_MEM_READ_ONLY|C.CL_MEM_HOST_WRITE_ONLY, byteParams, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(params)", status)
	}

	refBytes := packReferenceNRGBA(r.reference)
	if len(refBytes) == 0 {
		refBytes = make([]byte, 4)
	}

	r.referenceBuffer = C.clCreateBuffer(r.context, C.CL_MEM_READ_ONLY|C.CL_MEM_COPY_HOST_PTR|C.CL_MEM_HOST_NO_ACCESS, bytePixels, unsafe.Pointer(&refBytes[0]), &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(reference)", status)
	}

	if err := r.setStaticKernelArgs(); err != nil {
		return err
	}

	slog.Info("OpenCL backend initialised",
		"device", r.runtime.Device.Name,
		"vendor", r.runtime.Device.Vendor,
		"compute_units", r.runtime.Device.MaxComputeUnits,
		"reduction_local_size", r.localSize,
	)

	return nil
}

func (r *Renderer) setStaticKernelArgs() error {
	var status C.cl_int

	width := C.cl_int(r.width)
	height := C.cl_int(r.height)

	status = C.clSetKernelArg(r.renderKernel, 2, C.size_t(unsafe.Sizeof(width)), unsafe.Pointer(&width))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(width)", status)
	}

	status = C.clSetKernelArg(r.renderKernel, 3, C.size_t(unsafe.Sizeof(height)), unsafe.Pointer(&height))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(height)", status)
	}

	status = C.clSetKernelArg(r.renderKernel, 4, C.size_t(unsafe.Sizeof(r.referenceBuffer)), unsafe.Pointer(&r.referenceBuffer))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(reference)", status)
	}

	status = C.clSetKernelArg(r.renderKernel, 5, C.size_t(unsafe.Sizeof(r.outputBuffer)), unsafe.Pointer(&r.outputBuffer))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(output)", status)
	}

	status = C.clSetKernelArg(r.renderKernel, 6, C.size_t(unsafe.Sizeof(r.partialBufferA)), unsafe.Pointer(&r.partialBufferA))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(partialSums)", status)
	}

	return nil
}

func (r *Renderer) selectLocalSize() (int, error) {
	var renderLimit C.size_t
	status := C.clGetKernelWorkGroupInfo(
		r.renderKernel,
		r.device,
		C.CL_KERNEL_WORK_GROUP_SIZE,
		C.size_t(unsafe.Sizeof(renderLimit)),
		unsafe.Pointer(&renderLimit),
		nil,
	)
	if status != C.CL_SUCCESS {
		return 0, r.clError("clGetKernelWorkGroupInfo(render_cost)", status)
	}

	var reduceLimit C.size_t
	status = C.clGetKernelWorkGroupInfo(
		r.reduceKernel,
		r.device,
		C.CL_KERNEL_WORK_GROUP_SIZE,
		C.size_t(unsafe.Sizeof(reduceLimit)),
		unsafe.Pointer(&reduceLimit),
		nil,
	)
	if status != C.CL_SUCCESS {
		return 0, r.clError("clGetKernelWorkGroupInfo(reduce_sum)", status)
	}

	var localMemory C.cl_ulong
	status = C.clGetDeviceInfo(
		r.device,
		C.CL_DEVICE_LOCAL_MEM_SIZE,
		C.size_t(unsafe.Sizeof(localMemory)),
		unsafe.Pointer(&localMemory),
		nil,
	)
	if status != C.CL_SUCCESS {
		return 0, r.clError("clGetDeviceInfo(local memory)", status)
	}

	limit := min(256, int(renderLimit), int(reduceLimit), int(localMemory)/int(unsafe.Sizeof(float32(0))))
	localSize := largestPowerOfTwo(limit)
	if localSize == 0 {
		return 0, fmt.Errorf("OpenCL device has no usable reduction workgroup size")
	}

	return localSize, nil
}

func largestPowerOfTwo(limit int) int {
	result := 1
	for result <= limit/2 {
		result *= 2
	}
	if limit < 1 {
		return 0
	}
	return result
}

func ceilDiv(value, divisor int) int {
	if value <= 0 {
		return 0
	}
	return 1 + (value-1)/divisor
}

func (r *Renderer) clError(prefix string, status C.cl_int) error {
	return fmt.Errorf("%s: %s (%d)", prefix, C.GoString(C.mayfly_gpu_renderer_error_string(status)), int(status))
}

func (r *Renderer) dumpBuildLog() {
	if r.program == nil || r.device == nil {
		return
	}

	var logSize C.size_t
	if status := C.clGetProgramBuildInfo(r.program, r.device, C.CL_PROGRAM_BUILD_LOG, 0, nil, &logSize); status != C.CL_SUCCESS {
		slog.Error("OpenCL: failed to fetch build log size", "err", status)
		return
	}
	if logSize == 0 {
		return
	}

	buf := make([]byte, int(logSize))
	if status := C.clGetProgramBuildInfo(r.program, r.device, C.CL_PROGRAM_BUILD_LOG, logSize, unsafe.Pointer(&buf[0]), nil); status != C.CL_SUCCESS {
		slog.Error("OpenCL: failed to fetch build log", "err", status)
		return
	}

	slog.Error("OpenCL build log", "log", string(buf))
}

func (r *Renderer) Render(params []float64) *image.NRGBA {
	if len(params) != r.Dim() || r.pixelCount == 0 {
		return r.fallback.Render(params)
	}
	if r.degraded.Load() {
		return r.fallback.Render(params)
	}

	if err := r.ensure(params); err != nil {
		slog.Warn("OpenCL renderer degraded to CPU", "reason", err)
		r.degraded.Store(true)
		return r.fallback.Render(params)
	}

	if err := r.materializeImage(r.deviceHash); err != nil {
		slog.Warn("OpenCL renderer degraded to CPU", "reason", err)
		r.degraded.Store(true)
		return r.fallback.Render(params)
	}

	return r.renderImage
}

func (r *Renderer) Cost(params []float64) float64 {
	if len(params) != r.Dim() || r.pixelCount == 0 {
		return r.fallback.Cost(params)
	}
	if r.degraded.Load() {
		return r.fallback.Cost(params)
	}

	if err := r.ensure(params); err != nil {
		slog.Warn("OpenCL renderer degraded to CPU", "reason", err)
		r.degraded.Store(true)
		return r.fallback.Cost(params)
	}

	return r.lastCost
}

// NewSession creates an independent OpenCL renderer over the same reference.
// Sequential and batch optimization use these sessions with an increasing
// circle count, replaying retained circles because OpenCL does not yet support
// an accumulated base canvas.
func (r *Renderer) NewSession(circleCount int) (*Renderer, func(), error) {
	if circleCount < 0 {
		return nil, noopCleanup, fmt.Errorf("circle count cannot be negative")
	}
	return newRenderer(r.reference, circleCount, r.newFallback, r.degraded)
}

func (r *Renderer) ensure(params []float64) error {
	hash := hashParams(params)
	if r.deviceValid && r.deviceHash == hash {
		return nil
	}

	if len(params) != r.Dim() {
		return fmt.Errorf("parameter count %d does not match renderer dimension %d", len(params), r.Dim())
	}

	circleCount := len(params) / paramsPerCircle
	if circleCount*paramsPerCircle > len(r.paramsScratch) {
		return fmt.Errorf("parameter count %d exceeds renderer capacity %d", circleCount, len(r.paramsScratch)/paramsPerCircle)
	}
	r.deviceValid = false
	r.imageValid = false

	if err := r.uploadParams(params); err != nil {
		return err
	}

	var status C.cl_int
	cc := C.cl_int(circleCount)
	status = C.clSetKernelArg(r.renderKernel, 0, C.size_t(unsafe.Sizeof(r.paramsBuffer)), unsafe.Pointer(&r.paramsBuffer))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(params)", status)
	}

	status = C.clSetKernelArg(r.renderKernel, 1, C.size_t(unsafe.Sizeof(cc)), unsafe.Pointer(&cc))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(circleCount)", status)
	}

	local := C.size_t(r.localSize)
	localBytes := C.size_t(r.localSize * int(unsafe.Sizeof(float32(0))))
	status = C.clSetKernelArg(r.renderKernel, 7, localBytes, nil)
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(render scratch)", status)
	}

	partialCount := ceilDiv(r.pixelCount, r.localSize)
	global := C.size_t(partialCount * r.localSize)
	status = C.clEnqueueNDRangeKernel(r.queue, r.renderKernel, 1, nil, &global, &local, 0, nil, nil)
	if status != C.CL_SUCCESS {
		return r.clError("clEnqueueNDRangeKernel(render_cost)", status)
	}

	input := r.partialBufferA
	output := r.partialBufferB
	for partialCount > 1 {
		nextCount := ceilDiv(partialCount, r.localSize*2)
		global = C.size_t(nextCount * r.localSize)
		count := C.cl_int(partialCount)

		status = C.clSetKernelArg(r.reduceKernel, 0, C.size_t(unsafe.Sizeof(input)), unsafe.Pointer(&input))
		if status != C.CL_SUCCESS {
			return r.clError("clSetKernelArg(reduce input)", status)
		}
		status = C.clSetKernelArg(r.reduceKernel, 1, C.size_t(unsafe.Sizeof(output)), unsafe.Pointer(&output))
		if status != C.CL_SUCCESS {
			return r.clError("clSetKernelArg(reduce output)", status)
		}
		status = C.clSetKernelArg(r.reduceKernel, 2, C.size_t(unsafe.Sizeof(count)), unsafe.Pointer(&count))
		if status != C.CL_SUCCESS {
			return r.clError("clSetKernelArg(reduce count)", status)
		}
		status = C.clSetKernelArg(r.reduceKernel, 3, localBytes, nil)
		if status != C.CL_SUCCESS {
			return r.clError("clSetKernelArg(reduce scratch)", status)
		}
		status = C.clEnqueueNDRangeKernel(r.queue, r.reduceKernel, 1, nil, &global, &local, 0, nil, nil)
		if status != C.CL_SUCCESS {
			return r.clError("clEnqueueNDRangeKernel(reduce_sum)", status)
		}

		partialCount = nextCount
		input, output = output, input
	}

	var finalSum float32
	status = C.clEnqueueReadBuffer(
		r.queue,
		input,
		C.CL_TRUE,
		0,
		C.size_t(unsafe.Sizeof(finalSum)),
		unsafe.Pointer(&finalSum),
		0,
		nil,
		nil,
	)
	if status != C.CL_SUCCESS {
		return r.clError("clEnqueueReadBuffer(reduced cost)", status)
	}

	r.lastCost = float64(finalSum) / float64(r.pixelCount*3)
	r.deviceHash = hash
	r.deviceValid = true
	r.evaluations++

	return nil
}

// uploadParams converts the optimizer's float64 vector into the persistent
// float32 staging slice and transfers only those parameters to the device.
func (r *Renderer) uploadParams(params []float64) error {
	if len(params) > len(r.paramsScratch) {
		return fmt.Errorf("parameter count %d exceeds renderer capacity %d", len(params), len(r.paramsScratch))
	}
	if len(params) == 0 {
		return nil
	}

	for i, param := range params {
		r.paramsScratch[i] = float32(param)
	}

	byteParams := C.size_t(len(params) * int(unsafe.Sizeof(float32(0))))
	status := C.clEnqueueWriteBuffer(r.queue, r.paramsBuffer, C.CL_TRUE, 0, byteParams, unsafe.Pointer(&r.paramsScratch[0]), 0, nil, nil)
	if status != C.CL_SUCCESS {
		return r.clError("clEnqueueWriteBuffer(params)", status)
	}
	return nil
}

func (r *Renderer) materializeImage(hash uint64) error {
	if r.imageValid && r.imageHash == hash {
		return nil
	}
	if !r.deviceValid || r.deviceHash != hash {
		return fmt.Errorf("OpenCL output is not available for requested parameters")
	}

	bytePixels := C.size_t(r.pixelCount * 4)
	status := C.clEnqueueReadBuffer(
		r.queue,
		r.outputBuffer,
		C.CL_TRUE,
		0,
		bytePixels,
		unsafe.Pointer(&r.renderImage.Pix[0]),
		0,
		nil,
		nil,
	)
	if status != C.CL_SUCCESS {
		return r.clError("clEnqueueReadBuffer(output)", status)
	}

	r.imageHash = hash
	r.imageValid = true
	return nil
}

func packReferenceNRGBA(reference *image.NRGBA) []byte {
	if reference == nil || reference.Bounds().Empty() {
		return nil
	}

	bounds := reference.Bounds()
	width := bounds.Dx()
	packed := make([]byte, width*bounds.Dy()*4)
	rowBytes := width * 4
	for y := 0; y < bounds.Dy(); y++ {
		source := reference.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		copy(packed[y*rowBytes:(y+1)*rowBytes], reference.Pix[source:source+rowBytes])
	}
	return packed
}

func (r *Renderer) Dim() int {
	return r.bounds.K * paramsPerCircle
}

func (r *Renderer) Bounds() (lower, upper []float64) {
	return r.bounds.Lower, r.bounds.Upper
}

func (r *Renderer) Reference() *image.NRGBA {
	return r.reference
}

func (r *Renderer) release() {
	if r.paramsBuffer != nil {
		C.clReleaseMemObject(r.paramsBuffer)
		r.paramsBuffer = nil
	}
	if r.referenceBuffer != nil {
		C.clReleaseMemObject(r.referenceBuffer)
		r.referenceBuffer = nil
	}
	if r.outputBuffer != nil {
		C.clReleaseMemObject(r.outputBuffer)
		r.outputBuffer = nil
	}
	if r.partialBufferA != nil {
		C.clReleaseMemObject(r.partialBufferA)
		r.partialBufferA = nil
	}
	if r.partialBufferB != nil {
		C.clReleaseMemObject(r.partialBufferB)
		r.partialBufferB = nil
	}
	if r.renderKernel != nil {
		C.clReleaseKernel(r.renderKernel)
		r.renderKernel = nil
	}
	if r.reduceKernel != nil {
		C.clReleaseKernel(r.reduceKernel)
		r.reduceKernel = nil
	}
	if r.program != nil {
		C.clReleaseProgram(r.program)
		r.program = nil
	}
	if r.runtime != nil {
		r.runtime.Close()
		r.runtime = nil
	}
}

func hashParams(params []float64) uint64 {
	hasher := fnv.New64a()
	buf := make([]byte, 8)
	for _, v := range params {
		binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
		_, _ = hasher.Write(buf)
	}
	return hasher.Sum64()
}
