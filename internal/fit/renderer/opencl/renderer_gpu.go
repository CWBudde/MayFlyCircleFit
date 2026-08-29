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
	"errors"
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

// ErrInvalidSessionInput reports arguments NewSession or NewSessionWithCanvas
// cannot accept. It is exported because it is the one error class a caller has
// to tell apart from a device failure: a rejected canvas is the caller's
// mistake and no fallback fixes it, while an unavailable device may warrant
// one. The adapter in the renderer package classifies on it.
var ErrInvalidSessionInput = errors.New("invalid session input")

// The individual reasons stay unexported -- callers outside this package match
// the class, not the case -- and they are sentinels rather than inline
// errors.New because the linter refuses dynamic errors.
var (
	errNilCanvas        = fmt.Errorf("%w: canvas cannot be nil", ErrInvalidSessionInput)
	errNegativeCircles  = fmt.Errorf("%w: circle count cannot be negative", ErrInvalidSessionInput)
	errCanvasDimensions = fmt.Errorf("%w: canvas dimensions must match reference image", ErrInvalidSessionInput)
	errTranslucentBase  = fmt.Errorf("%w: base canvas must be fully opaque", ErrInvalidSessionInput)
)

// Fallback is the CPU renderer the GPU path degrades to. Callers inject it so
// this package does not depend on the renderer package.
type Fallback interface {
	Render(params []float64) *image.NRGBA
	Cost(params []float64) float64
}

// NewFallback builds a fallback renderer for a given reference, base canvas and
// circle count. Sessions call it again so each one gets a fallback sized to its
// own circle count, matching the pre-split behavior.
//
// A nil canvas means the renderer starts from white. The parameter is not
// optional decoration: Cost and Render have no error return, so a device lost
// inside an accumulated staged session degrades silently, and a fallback that
// started from white instead of the retained canvas would publish costs for a
// completely different image.
type NewFallback func(reference, canvas *image.NRGBA, circles int) Fallback

const openclKernelSource = `
__kernel void render_cost(
    __global const float *params,
    const int circleCount,
    const int width,
    const int height,
    __global const uchar4 *reference,
    __global uchar4 *outImage,
    __global float *partialSums,
    __local float *scratch,
    __global const uchar4 *baseCanvas,
    const int hasBase) {

    const int idx = get_global_id(0);
    const int localID = get_local_id(0);
    const int localSize = get_local_size(0);
    const int pixelCount = width * height;
    float error = 0.0f;

    if (idx < pixelCount) {
        const int x = idx % width;
        const int y = idx / width;

        // A staged session starts from the canvas its retained circles already
        // produced instead of from white. hasBase is uniform across the whole
        // dispatch, so a renderer without a base canvas issues no load at all
        // and joint -- the one pipeline the GPU wins -- pays nothing for this.
        //
        // The loop below composites premultiplied, while NRGBA stores straight
        // alpha, so the base is premultiplied on the way in. Every canvas this
        // path can receive is opaque, where that multiply is the identity.
        float4 color = (float4)(1.0f, 1.0f, 1.0f, 1.0f);
        if (hasBase) {
            const float4 retained = convert_float4(baseCanvas[idx]) / 255.0f;
            color = (float4)(retained.xyz * retained.w, retained.w);
        }

        for (int i = 0; i < circleCount; ++i) {
            const int base = i * 7;
            const float cx = params[base + 0];
            const float cy = params[base + 1];
            const float radius = params[base + 2];
            const float cr = params[base + 3];
            const float cg = params[base + 4];
            const float cb = params[base + 5];
            const float opacity = params[base + 6];

            // The CPU renderer skips a circle only when it is exactly
            // transparent, and a negative radius leaves it an empty row range.
            if (opacity == 0.0f || radius < 0.0f) {
                continue;
            }

            const float dx = (float)x - cx;
            const float dy = (float)y - cy;
            const float radiusSquared = radius * radius;
            const float dy2 = dy * dy;

            // Rows are selected before columns, exactly as the CPU scanline
            // loop does: a row the disc does not reach paints nothing.
            if (dy2 > radiusSquared) {
                continue;
            }

            // Every row the disc *does* reach paints its nearest sample, even
            // where the disc test rejects it. The CPU span search starts at
            // int(centerX+0.5) and walks outward without ever testing that
            // pixel, so it is painted unconditionally; a plain dx*dx+dy*dy
            // test drops both tangent rows of a small circle and moves the
            // cost by a factor of two. See docs/renderer-correctness.md.
            if (dx * dx + dy2 > radiusSquared && x != (int)(cx + 0.5f)) {
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
	// engine owns everything derived from the device and the reference image:
	// the runtime, the compiled program, and the reference buffer. This Renderer
	// holds one reference to it and gives that reference back in close.
	engine *engine

	// runtime, context, queue, device and localSize are borrowed copies of the
	// engine's values. They are cached here because they are read on nearly
	// every line of the dispatch path, and this Renderer never releases any of
	// them: engine.release frees them when the last holder is gone.
	runtime   *gpu.Runtime
	context   C.cl_context
	queue     C.cl_command_queue
	device    C.cl_device_id
	localSize int

	fallback    Fallback
	newFallback NewFallback

	reference  *image.NRGBA
	bounds     *fit.Bounds
	width      int
	height     int
	pixelCount int

	renderKernel C.cl_kernel
	reduceKernel C.cl_kernel

	paramsBuffer   C.cl_mem
	outputBuffer   C.cl_mem
	partialBufferA C.cl_mem
	partialBufferB C.cl_mem

	// baseCanvas is the retained canvas a staged session composites onto, and
	// baseBuffer is its packed device copy. Both are nil for a renderer that
	// starts from white, which is every renderer outside an accumulated staged
	// session. The canvas is fixed for the session's whole life, so it is
	// uploaded once in init and never touched again -- which is why the cost
	// cache can go on hashing the parameters alone.
	baseCanvas *image.NRGBA
	baseBuffer C.cl_mem

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
	// through a separate session per stage, so a per-renderer flag would leave a
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

// newRenderer brings up a device of its own and builds a renderer on it. Only
// a base renderer takes this path; a session shares its parent's engine through
// newRendererOnEngine.
func newRenderer(
	reference *image.NRGBA, circleCount int, newFallback NewFallback, degraded *atomic.Bool,
) (*Renderer, func(), error) {
	eng, err := newEngine(reference)
	if err != nil {
		return nil, noopCleanup, err
	}

	// newEngine hands back the constructing reference, which belongs to this
	// call and not to the Renderer. Dropping it on every exit path -- while the
	// Renderer takes one of its own inside newRendererOnEngine -- means the two
	// are undone independently: a failed build releases the Renderer's
	// reference and the deferred release frees the engine, while a successful
	// one leaves exactly the Renderer holding it.
	defer eng.release()

	return newRendererOnEngine(eng, reference, nil, circleCount, newFallback, degraded)
}

// newRendererOnEngine builds a renderer over an engine that already exists and
// over an existing degradation record. It takes its own reference to the
// engine, which cleanup gives back, so the engine outlives whichever of its
// holders is torn down first.
func newRendererOnEngine(
	eng *engine,
	reference *image.NRGBA,
	baseCanvas *image.NRGBA,
	circleCount int,
	newFallback NewFallback,
	degraded *atomic.Bool,
) (*Renderer, func(), error) {
	if degraded.Load() {
		return newDegradedRenderer(reference, baseCanvas, circleCount, newFallback, degraded)
	}

	eng.retain()

	r := &Renderer{
		engine:        eng,
		runtime:       eng.runtime,
		context:       eng.context,
		queue:         eng.queue,
		device:        eng.device,
		localSize:     eng.localSize,
		fallback:      newFallback(reference, baseCanvas, circleCount),
		newFallback:   newFallback,
		reference:     reference,
		baseCanvas:    baseCanvas,
		bounds:        fit.NewBounds(circleCount, reference.Bounds().Dx(), reference.Bounds().Dy()),
		width:         reference.Bounds().Dx(),
		height:        reference.Bounds().Dy(),
		pixelCount:    reference.Bounds().Dx() * reference.Bounds().Dy(),
		paramsScratch: make([]float32, circleCount*paramsPerCircle),
		renderImage:   image.NewNRGBA(image.Rect(0, 0, reference.Bounds().Dx(), reference.Bounds().Dy())),
		degraded:      degraded,
	}

	if err := r.init(); err != nil {
		r.close()
		return nil, noopCleanup, err
	}

	cleanup := func() {
		r.close()
	}

	return r, cleanup, nil
}

// init creates the per-Renderer half of the OpenCL state: a kernel pair of its
// own and the buffers sized from this Renderer's circle count and from the
// engine's reduction workgroup size.
func (r *Renderer) init() error {
	renderKernel, reduceKernel, err := r.engine.newKernels()
	if err != nil {
		return err
	}

	r.renderKernel = renderKernel
	r.reduceKernel = reduceKernel

	var status C.cl_int

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

	err = r.uploadBaseCanvas()
	if err != nil {
		return err
	}

	return r.setStaticKernelArgs()
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

	// The reference buffer belongs to the engine; this is a borrowed handle, and
	// clSetKernelArg copies the value the pointer refers to.
	reference := r.engine.referenceBuffer
	referenceArg := unsafe.Pointer(&reference)

	status = C.clSetKernelArg(r.renderKernel, 4, C.size_t(unsafe.Sizeof(reference)), referenceArg)
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

	// Argument 7 is the local scratch, sized per dispatch in ensure. Arguments
	// 8 and 9 are the base canvas and the flag that says whether to read it. A
	// renderer without one still has to bind a valid buffer, because OpenCL
	// requires every argument set before an enqueue; the engine's four-byte
	// placeholder is never dereferenced, because hasBase is zero for exactly
	// the renderers that bind it.
	baseCanvas := r.engine.emptyCanvasBuffer
	hasBase := C.cl_int(0)

	if r.baseBuffer != nil {
		baseCanvas = r.baseBuffer
		hasBase = 1
	}

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status = C.clSetKernelArg(r.renderKernel, 8, C.size_t(unsafe.Sizeof(baseCanvas)), unsafe.Pointer(&baseCanvas))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(baseCanvas)", status)
	}

	//nolint:gocritic // dupSubExpr fires on cgo's generated pointer-check guard.
	status = C.clSetKernelArg(r.renderKernel, 9, C.size_t(unsafe.Sizeof(hasBase)), unsafe.Pointer(&hasBase))
	if status != C.CL_SUCCESS {
		return r.clError("clSetKernelArg(hasBase)", status)
	}

	return nil
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

// clError formats an OpenCL status code. The engine reports the same failures,
// and the C helper this calls is defined in this file's cgo preamble -- a
// static function in one file's preamble is invisible to another's -- so the
// implementation is a package-level function and Renderer.clError forwards to
// it.
func clError(prefix string, status C.cl_int) error {
	return fmt.Errorf("%s: %s (%d)", prefix, C.GoString(C.mayfly_gpu_renderer_error_string(status)), int(status))
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

// NewSession creates an OpenCL renderer over the same reference and the same
// device engine, allocating only the kernel pair and the buffers its own circle
// count needs. Sequential and batch optimization use these sessions with an
// increasing circle count. Sequential and batch use NewSessionWithCanvas
// instead, which starts a stage from the retained canvas rather than replaying
// the circles behind it.
//
// It shares rather than rebuilds because the rebuild was the staged path's
// whole cost: a session used to re-enumerate every platform and device, create
// a context and queue, and compile the kernel source again, none of which
// depends on the circle count that changed.
func (r *Renderer) NewSession(circleCount int) (*Renderer, func(), error) {
	if circleCount < 0 {
		return nil, noopCleanup, errNegativeCircles
	}

	return newRendererOnEngine(r.engine, r.reference, nil, circleCount, r.newFallback, r.degraded)
}

// NewSessionWithCanvas creates a session that composites circleCount new
// circles onto an already-retained canvas instead of replaying the circles that
// produced it. The canvas is uploaded once and never touched again, so the
// session's per-evaluation work depends on circleCount alone rather than on the
// depth of the prefix behind it.
//
// The canvas must be opaque, and that is enforced rather than handled. The
// kernel composites premultiplied and writes an opaque image back, and the CPU
// renderer takes a different compositing path for a canvas that is not opaque,
// so the two agree only on opaque canvases. Every canvas the pipelines can hand
// in comes from Render, which writes alpha 255 unconditionally, so a
// translucent one is a bug -- and an error is better than silently wrong
// pixels.
func (r *Renderer) NewSessionWithCanvas(canvas *image.NRGBA, circleCount int) (*Renderer, func(), error) {
	if canvas == nil {
		return nil, noopCleanup, errNilCanvas
	}

	if circleCount < 0 {
		return nil, noopCleanup, errNegativeCircles
	}

	if canvas.Bounds().Dx() != r.width || canvas.Bounds().Dy() != r.height {
		return nil, noopCleanup, errCanvasDimensions
	}

	if !canvasIsOpaque(canvas) {
		return nil, noopCleanup, errTranslucentBase
	}

	return newRendererOnEngine(r.engine, r.reference, canvas, circleCount, r.newFallback, r.degraded)
}

// InitialCanvas returns an independent snapshot of the canvas this renderer
// starts every render from: the retained canvas of an accumulated session, or
// opaque white for every other renderer.
//
// It returns nil for a zero-pixel image, which is what makes the staged
// accumulator fall back to replaying instead of carrying an empty canvas.
func (r *Renderer) InitialCanvas() *image.NRGBA {
	if r.pixelCount == 0 {
		return nil
	}

	canvas := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
	if r.baseCanvas != nil {
		copy(canvas.Pix, packReferenceNRGBA(r.baseCanvas))

		return canvas
	}

	for i := range canvas.Pix {
		canvas.Pix[i] = 0xFF
	}

	return canvas
}

// canvasIsOpaque reports whether every pixel has alpha 255. It walks the image
// through PixOffset rather than Pix directly, because a sub-image's rows are
// not contiguous.
func canvasIsOpaque(canvas *image.NRGBA) bool {
	bounds := canvas.Bounds()
	for y := range bounds.Dy() {
		row := canvas.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		for x := range bounds.Dx() {
			if canvas.Pix[row+x*4+3] != 0xFF {
				return false
			}
		}
	}

	return true
}

func (r *Renderer) ensure(params []float64) error {
	hash := hashParams(params)
	if r.deviceValid && r.deviceHash == hash {
		return nil
	}

	if len(params) != r.Dim() {
		return fmt.Errorf("parameter count %d does not match renderer dimension %d", len(params), r.Dim())
	}

	// A renderer built without an engine has no device to reach. Cost and Render
	// route to the fallback before they get here, because such a renderer is
	// only ever built when the degradation record is already set and degradation
	// is permanent -- so this is unreachable, and it returns an error rather
	// than dereferencing nil if that ever stops being true.
	if r.engine == nil {
		return errNoContext
	}

	// Everything from here on is a chain of commands on a queue this renderer
	// shares with its siblings, and the chain has to reach the blocking read at
	// the end without another renderer's chain interleaved. The cache hit above
	// is deliberately outside the lock: it touches no device.
	r.engine.queueMu.Lock()
	defer r.engine.queueMu.Unlock()

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

		err := r.setReduceArgs(input, output, count, localBytes)
		if err != nil {
			return err
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

	// Unreachable for the same reason as the guard in ensure, and cheap for the
	// same reason.
	if r.engine == nil {
		return errNoContext
	}

	// The readback is one command on the shared queue; the guard above reads
	// only this renderer's cache state.
	r.engine.queueMu.Lock()
	defer r.engine.queueMu.Unlock()

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

// uploadBaseCanvas packs the retained canvas and copies it to the device once.
// A renderer that starts from white has none, and binds the engine's
// placeholder instead.
func (r *Renderer) uploadBaseCanvas() error {
	if r.baseCanvas == nil || r.pixelCount == 0 {
		return nil
	}

	packed := packReferenceNRGBA(r.baseCanvas)
	if len(packed) == 0 {
		return nil
	}

	var status C.cl_int

	r.baseBuffer = C.clCreateBuffer(
		r.context,
		C.CL_MEM_READ_ONLY|C.CL_MEM_COPY_HOST_PTR|C.CL_MEM_HOST_NO_ACCESS,
		C.size_t(len(packed)),
		unsafe.Pointer(&packed[0]),
		&status,
	)
	if status != C.CL_SUCCESS {
		return r.clError("clCreateBuffer(baseCanvas)", status)
	}

	return nil
}

func (*Renderer) clError(prefix string, status C.cl_int) error {
	return clError(prefix, status)
}

// close gives back everything this Renderer holds: its own kernels and buffers
// first, then its reference to the engine. That order matters, because
// releaseOwn waits on the engine's queue and the engine may free it.
//
// Calling it twice is a no-op. releaseOwn clears every handle it used and
// engine.release ignores an engine that has already reached zero, which is what
// keeps a doubled teardown from releasing another holder's device state.
func (r *Renderer) close() {
	r.releaseOwn()
	r.engine.release()
	r.engine = nil
}

// releaseOwn frees only what this Renderer created. The program, the reference
// buffer and the runtime belong to the engine and are deliberately untouched
// here.
func (r *Renderer) releaseOwn() {
	// Both kernel dispatches are enqueued without events, so nothing orders
	// them against these releases except the in-order queue. Today teardown
	// gets away with that only because clReleaseCommandQueue, inside
	// Runtime.Close, waits for the queue to drain -- and once an engine is
	// shared, that implicit barrier no longer runs when a single session goes
	// away. The blocking wait is therefore a requirement of sharing the queue,
	// not a defensive extra.
	if r.engine != nil && r.queue != nil {
		// Teardown participates in the same serialization: the wait must not
		// start while a sibling is still issuing its chain, or it would drain a
		// half-issued sequence and return before that sibling's remaining
		// commands were even enqueued.
		r.engine.queueMu.Lock()
		C.clFinish(r.queue)
		r.engine.queueMu.Unlock()
	}

	if r.paramsBuffer != nil {
		C.clReleaseMemObject(r.paramsBuffer)
		r.paramsBuffer = nil
	}

	if r.baseBuffer != nil {
		C.clReleaseMemObject(r.baseBuffer)
		r.baseBuffer = nil
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

	// The borrowed handles are cleared last. They are copies of state the engine
	// may free the moment this Renderer gives its reference back, so leaving
	// them set would let a second teardown wait on a command queue that no
	// longer exists -- the one operation above that reads a handle it does not
	// own.
	r.runtime = nil
	r.context = nil
	r.queue = nil
	r.device = nil
}

// newDegradedRenderer builds a session that never touches the device, for the
// case where the degradation record already says there is nothing to touch.
//
// Degradation is permanent, so such a session would route every Cost and Render
// to its CPU fallback no matter what it allocated. Allocating anyway is not
// merely wasted: on a real device loss the kernel and buffer creation in init
// is what fails, and a failure there makes NewSession return an error and the
// staged pipeline abort -- the run stops instead of finishing on the CPU, which
// is the opposite of what the shared record exists to achieve. The invariant
// that a lost device costs one timeout per run rather than one per stage is
// only true if a session created after the loss does no device work at all.
//
// It holds no engine reference, because it uses nothing the engine owns.
func newDegradedRenderer(
	reference, baseCanvas *image.NRGBA, circleCount int, newFallback NewFallback, degraded *atomic.Bool,
) (*Renderer, func(), error) {
	width := reference.Bounds().Dx()
	height := reference.Bounds().Dy()

	r := &Renderer{
		fallback:      newFallback(reference, baseCanvas, circleCount),
		newFallback:   newFallback,
		reference:     reference,
		baseCanvas:    baseCanvas,
		bounds:        fit.NewBounds(circleCount, width, height),
		width:         width,
		height:        height,
		pixelCount:    width * height,
		paramsScratch: make([]float32, circleCount*paramsPerCircle),
		renderImage:   image.NewNRGBA(image.Rect(0, 0, width, height)),
		degraded:      degraded,
	}

	return r, func() { r.close() }, nil
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
