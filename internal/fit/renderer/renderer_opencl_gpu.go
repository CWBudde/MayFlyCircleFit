//go:build gpu

package renderer

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
	"unsafe"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/fit/gpu"
)

const openclKernelSource = `
__kernel void render_cost(
    __global const float *params,
    const int circleCount,
    const int width,
    const int height,
    __global const float4 *reference,
    __global float4 *outImage,
    __global float *outError) {

    const int idx = get_global_id(0);
    const int pixelCount = width * height;
    if (idx >= pixelCount) {
        return;
    }

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

        if (opacity < 0.001f || radius <= 0.0f) {
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
    }

    color.xyz = clamp(color.xyz, 0.0f, 1.0f);
    color.w = clamp(color.w, 0.0f, 1.0f);

    outImage[idx] = color;

    const float4 ref = reference[idx];
    const float dr = (color.x - ref.x) * 255.0f;
    const float dg = (color.y - ref.y) * 255.0f;
    const float db = (color.z - ref.z) * 255.0f;
    outError[idx] = dr * dr + dg * dg + db * db;
}
`

type openCLRenderer struct {
	runtime    *gpu.Runtime
	fallback   *CPURenderer
	reference  *image.NRGBA
	bounds     *fit.Bounds
	width      int
	height     int
	pixelCount int

	context C.cl_context
	queue   C.cl_command_queue
	device  C.cl_device_id
	program C.cl_program
	kernel  C.cl_kernel

	paramsBuffer    C.cl_mem
	referenceBuffer C.cl_mem
	outputBuffer    C.cl_mem
	errorBuffer     C.cl_mem

	paramsScratch []float32
	imageScratch  []float32
	errorScratch  []float32

	renderImage *image.NRGBA

	lastHash  uint64
	lastCost  float64
	lastValid bool

	degraded bool
}

// NewOpenCLRenderer creates an OpenCL GPU-based renderer
func NewOpenCLRenderer(reference *image.NRGBA, k int) (Renderer, func(), error) {
	rt, err := gpu.InitOpenCL()
	if err != nil {
		return nil, noopCleanup, fmt.Errorf("%w: %v", ErrBackendUnavailable, err)
	}

	r := &openCLRenderer{
		runtime:       rt,
		fallback:      NewCPURenderer(reference, k),
		reference:     reference,
		bounds:        fit.NewBounds(k, reference.Bounds().Dx(), reference.Bounds().Dy()),
		width:         reference.Bounds().Dx(),
		height:        reference.Bounds().Dy(),
		pixelCount:    reference.Bounds().Dx() * reference.Bounds().Dy(),
		paramsScratch: make([]float32, k*7), // 7 params per circle
		imageScratch:  make([]float32, reference.Bounds().Dx()*reference.Bounds().Dy()*4),
		errorScratch:  make([]float32, reference.Bounds().Dx()*reference.Bounds().Dy()),
		renderImage:   image.NewNRGBA(reference.Bounds()),
	}

	if err := r.init(); err != nil {
		rt.Close()
		return nil, noopCleanup, err
	}

	cleanup := func() {
		r.release()
	}

	return r, cleanup, nil
}

func (r *openCLRenderer) init() error {
	r.context = C.cl_context(r.runtime.ContextPtr())
	r.queue = C.cl_command_queue(r.runtime.QueuePtr())
	r.device = C.cl_device_id(r.runtime.DevicePtr())

	if r.context == nil || r.queue == nil {
		return fmt.Errorf("%w: failed to access OpenCL context/queue", ErrBackendUnavailable)
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

	kernelName := C.CString("render_cost")
	defer C.free(unsafe.Pointer(kernelName))
	r.kernel = C.clCreateKernel(r.program, kernelName, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateKernel", status)
	}

	bytePixels := C.size_t(r.pixelCount * 4 * int(unsafe.Sizeof(float32(0))))
	byteErrors := C.size_t(r.pixelCount * int(unsafe.Sizeof(float32(0))))
	byteParams := C.size_t(len(r.paramsScratch) * int(unsafe.Sizeof(float32(0))))

	r.outputBuffer = C.clCreateBuffer(r.context, C.CL_MEM_READ_WRITE, bytePixels, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(output)", status)
	}

	r.errorBuffer = C.clCreateBuffer(r.context, C.CL_MEM_READ_WRITE, byteErrors, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(error)", status)
	}

	r.paramsBuffer = C.clCreateBuffer(r.context, C.CL_MEM_READ_ONLY, byteParams, nil, &status)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(params)", status)
	}

	refFloats := make([]float32, r.pixelCount*4)
	for i := 0; i < r.pixelCount; i++ {
		offset := i * 4
		refFloats[offset+0] = float32(r.reference.Pix[offset+0]) / 255.0
		refFloats[offset+1] = float32(r.reference.Pix[offset+1]) / 255.0
		refFloats[offset+2] = float32(r.reference.Pix[offset+2]) / 255.0
		refFloats[offset+3] = float32(r.reference.Pix[offset+3]) / 255.0
	}

	r.referenceBuffer = C.clCreateBuffer(r.context, C.CL_MEM_READ_ONLY|C.CL_MEM_COPY_HOST_PTR, bytePixels, unsafe.Pointer(&refFloats[0]), &status)
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
	)

	return nil
}

func (r *openCLRenderer) setStaticKernelArgs() error {
	var status C.cl_int

	width := C.cl_int(r.width)
	height := C.cl_int(r.height)

	status = C.clSetKernelArg(r.kernel, 2, C.size_t(unsafe.Sizeof(width)), unsafe.Pointer(&width))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(width)", status)
	}

	status = C.clSetKernelArg(r.kernel, 3, C.size_t(unsafe.Sizeof(height)), unsafe.Pointer(&height))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(height)", status)
	}

	status = C.clSetKernelArg(r.kernel, 4, C.size_t(unsafe.Sizeof(r.referenceBuffer)), unsafe.Pointer(&r.referenceBuffer))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(reference)", status)
	}

	status = C.clSetKernelArg(r.kernel, 5, C.size_t(unsafe.Sizeof(r.outputBuffer)), unsafe.Pointer(&r.outputBuffer))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(output)", status)
	}

	status = C.clSetKernelArg(r.kernel, 6, C.size_t(unsafe.Sizeof(r.errorBuffer)), unsafe.Pointer(&r.errorBuffer))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(error)", status)
	}

	return nil
}

func (r *openCLRenderer) clError(prefix string, status C.cl_int) error {
	return fmt.Errorf("%s: %s (%d)", prefix, C.GoString(C.mayfly_gpu_renderer_error_string(status)), int(status))
}

func (r *openCLRenderer) dumpBuildLog() {
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

func (r *openCLRenderer) Render(params []float64) *image.NRGBA {
	if r.degraded {
		return r.fallback.Render(params)
	}

	if err := r.ensure(params); err != nil {
		slog.Warn("OpenCL renderer degraded to CPU", "reason", err)
		r.degraded = true
		return r.fallback.Render(params)
	}

	pix := r.renderImage.Pix
	for i := 0; i < r.pixelCount; i++ {
		offset := i * 4
		pix[offset+0] = clampToByte(r.imageScratch[offset+0] * 255.0)
		pix[offset+1] = clampToByte(r.imageScratch[offset+1] * 255.0)
		pix[offset+2] = clampToByte(r.imageScratch[offset+2] * 255.0)
		pix[offset+3] = clampToByte(r.imageScratch[offset+3] * 255.0)
	}

	return r.renderImage
}

func clampToByte(v float32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func (r *openCLRenderer) Cost(params []float64) float64 {
	if r.degraded {
		return r.fallback.Cost(params)
	}

	if err := r.ensure(params); err != nil {
		slog.Warn("OpenCL renderer degraded to CPU", "reason", err)
		r.degraded = true
		return r.fallback.Cost(params)
	}

	return r.lastCost
}

func (r *openCLRenderer) ensure(params []float64) error {
	hash := hashParams(params)
	if r.lastValid && r.lastHash == hash {
		return nil
	}

	circleCount := len(params) / paramsPerCircle
	if circleCount == 0 {
		// Reset image to white and zero cost.
		for i := 0; i < len(r.imageScratch); i += 4 {
			r.imageScratch[i+0] = 1.0
			r.imageScratch[i+1] = 1.0
			r.imageScratch[i+2] = 1.0
			r.imageScratch[i+3] = 1.0
		}
		for i := range r.errorScratch {
			r.errorScratch[i] = 0
		}
		r.lastCost = 0
		r.lastHash = hash
		r.lastValid = true
		return nil
	}

	if circleCount*paramsPerCircle > len(r.paramsScratch) {
		return fmt.Errorf("parameter count %d exceeds renderer capacity %d", circleCount, len(r.paramsScratch)/paramsPerCircle)
	}

	for i := 0; i < circleCount*paramsPerCircle; i++ {
		r.paramsScratch[i] = float32(params[i])
	}

	var status C.cl_int
	if len(r.paramsScratch) > 0 {
		byteParams := C.size_t(circleCount * paramsPerCircle * int(unsafe.Sizeof(float32(0))))
		status = C.clEnqueueWriteBuffer(r.queue, r.paramsBuffer, C.CL_TRUE, 0, byteParams, unsafe.Pointer(&r.paramsScratch[0]), 0, nil, nil)
		if status != C.CL_SUCCESS {
			return r.clError("clEnqueueWriteBuffer(params)", status)
		}
	}

	cc := C.cl_int(circleCount)
	status = C.clSetKernelArg(r.kernel, 0, C.size_t(unsafe.Sizeof(r.paramsBuffer)), unsafe.Pointer(&r.paramsBuffer))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(params)", status)
	}

	status = C.clSetKernelArg(r.kernel, 1, C.size_t(unsafe.Sizeof(cc)), unsafe.Pointer(&cc))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(circleCount)", status)
	}

	global := C.size_t(r.pixelCount)
	status = C.clEnqueueNDRangeKernel(r.queue, r.kernel, 1, nil, &global, nil, 0, nil, nil)
	if status != C.CL_SUCCESS {
		return r.clError("clEnqueueNDRangeKernel", status)
	}

	status = C.clFinish(r.queue)
	if status != C.CL_SUCCESS {
		return r.clError("clFinish", status)
	}

	if len(r.imageScratch) > 0 {
		bytePixels := C.size_t(len(r.imageScratch) * int(unsafe.Sizeof(float32(0))))
		status = C.clEnqueueReadBuffer(r.queue, r.outputBuffer, C.CL_TRUE, 0, bytePixels, unsafe.Pointer(&r.imageScratch[0]), 0, nil, nil)
		if status != C.CL_SUCCESS {
			return r.clError("clEnqueueReadBuffer(output)", status)
		}
	}

	if len(r.errorScratch) > 0 {
		byteErrors := C.size_t(len(r.errorScratch) * int(unsafe.Sizeof(float32(0))))
		status = C.clEnqueueReadBuffer(r.queue, r.errorBuffer, C.CL_TRUE, 0, byteErrors, unsafe.Pointer(&r.errorScratch[0]), 0, nil, nil)
		if status != C.CL_SUCCESS {
			return r.clError("clEnqueueReadBuffer(error)", status)
		}
	}

	var sum float64
	for _, v := range r.errorScratch {
		sum += float64(v)
	}

	r.lastCost = sum / float64(r.pixelCount*3)
	r.lastHash = hash
	r.lastValid = true

	return nil
}

func (r *openCLRenderer) Dim() int {
	return r.bounds.K * paramsPerCircle
}

func (r *openCLRenderer) Bounds() (lower, upper []float64) {
	return r.bounds.Lower, r.bounds.Upper
}

func (r *openCLRenderer) Reference() *image.NRGBA {
	return r.reference
}

func (r *openCLRenderer) release() {
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
	if r.errorBuffer != nil {
		C.clReleaseMemObject(r.errorBuffer)
		r.errorBuffer = nil
	}
	if r.kernel != nil {
		C.clReleaseKernel(r.kernel)
		r.kernel = nil
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
