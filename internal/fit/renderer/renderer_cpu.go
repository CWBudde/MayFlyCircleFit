package renderer

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"runtime"
	"sync"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// CPURenderer implements software rendering of circles
type CPURenderer struct {
	reference    *image.NRGBA
	k            int
	bounds       *fit.Bounds
	costFunc     fit.CostFunc
	width        int
	height       int
	threads      int
	opaqueCanvas bool
	// forceFloatGeometry is reserved for oracle tests and benchmarks. Normal
	// renderers use Q16.16 when the decoded circle is in its safe range.
	forceFloatGeometry bool
	// forceFloat32Geometry is reserved for reduced-precision SIMD tests and
	// benchmarks. On AVX2 hosts it exercises the vectorized span-edge search.
	forceFloat32Geometry bool
	// enableRowSymmetry is reserved for integration benchmarks and parity
	// tests. Mirrored Q16.16 rows are exact for integer and half-integer
	// centers. The measured prototype remains opt-in because ordinary optimizer
	// centers are almost never eligible and row sharding removes the gain.
	enableRowSymmetry bool
	// parallelEvaluationWorkers is the number of concurrent cost evaluations
	// the optimization pipeline may run against independent sessions of this
	// renderer. Values below two keep the historical single-session behavior.
	// It never affects this renderer's own rendering, only how many sessions
	// the pipeline creates.
	parallelEvaluationWorkers int
	// fastCompositing selects the reduced-precision float32 SIMD span
	// compositor. It is opt-in because that kernel regroups the blend
	// arithmetic and is only accurate to +/-1 per channel, so it is not
	// byte-identical to the default float64 path and changes rendered output.
	fastCompositing bool
	// initialSSD is the exact, unnormalized RGB SSD between initialBg and the
	// reference. It is prepared once for future incremental-cost evaluation.
	initialSSD      uint64
	initialSSDValid bool
	// fastCostSelected distinguishes the built-in FastMSECost from arbitrary
	// CostFunc values, which cannot be compared directly in Go.
	fastCostSelected    bool
	incrementalCostMode incrementalCostMode
	// stagedIncremental enables measured automatic dispatch for sessions that
	// render new sequential/batch circles over a retained canvas.
	stagedIncremental bool
	dirtySpans        dirtySpanSet
	// Buffer pooling to reduce allocations
	canvas    *image.NRGBA // Reusable render buffer
	initialBg []byte       // Precomputed initial background (white or custom canvas)
}

// NewCPURenderer creates a CPU-based renderer with a white background
func NewCPURenderer(reference *image.NRGBA, k int) *CPURenderer {
	if k < 0 {
		k = 0
	}
	bounds := reference.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// Allocate reusable canvas buffer
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))

	// Precompute white background (NRGBA: 255,255,255,255 repeated)
	pixelCount := width * height * 4 // 4 bytes per pixel (RGBA)
	whiteBg := make([]byte, pixelCount)
	for i := 0; i < pixelCount; i++ {
		whiteBg[i] = 255
	}
	initialSSD, initialSSDValid := exactInitialCanvasSSD(whiteBg, width, height, reference)

	return &CPURenderer{
		reference:         reference,
		k:                 k,
		bounds:            fit.NewBounds(k, width, height),
		costFunc:          fit.FastMSECost,
		width:             width,
		height:            height,
		threads:           effectiveThreadCount(runtime.GOMAXPROCS(0), height),
		opaqueCanvas:      true,
		initialSSD:        initialSSD,
		initialSSDValid:   initialSSDValid,
		fastCostSelected:  true,
		stagedIncremental: deltaSSDVectorized(),
		canvas:            canvas,
		initialBg:         whiteBg,
	}
}

// NewCPURendererWithCanvas creates a CPU-based renderer with a custom initial canvas.
// This is useful for continuing optimization from a previous result (e.g., adding circles
// to an existing partial solution).
// The canvas parameter is copied, so the original image is not modified.
func NewCPURendererWithCanvas(reference *image.NRGBA, canvas *image.NRGBA, k int) *CPURenderer {
	if k < 0 {
		k = 0
	}
	bounds := reference.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	// Verify canvas dimensions match reference
	canvasBounds := canvas.Bounds()
	if canvasBounds.Dx() != width || canvasBounds.Dy() != height {
		panic("canvas dimensions must match reference image")
	}

	// Allocate reusable canvas buffer (copy from input canvas)
	canvasCopy := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvasCopy, canvasCopy.Bounds(), canvas, canvas.Bounds().Min, draw.Src)

	// Store initial canvas state for reset between renders
	pixelCount := width * height * 4 // 4 bytes per pixel (RGBA)
	initialBg := make([]byte, pixelCount)
	copy(initialBg, canvasCopy.Pix)
	initialSSD, initialSSDValid := exactInitialCanvasSSD(initialBg, width, height, reference)

	return &CPURenderer{
		reference:         reference,
		k:                 k,
		bounds:            fit.NewBounds(k, width, height),
		costFunc:          fit.FastMSECost,
		width:             width,
		height:            height,
		threads:           effectiveThreadCount(runtime.GOMAXPROCS(0), height),
		opaqueCanvas:      pixelsAreOpaque(initialBg),
		initialSSD:        initialSSD,
		initialSSDValid:   initialSSDValid,
		fastCostSelected:  true,
		stagedIncremental: deltaSSDVectorized(),
		canvas:            canvasCopy,
		initialBg:         initialBg,
	}
}

// Render creates an image from parameter vector
func (r *CPURenderer) Render(params []float64) *image.NRGBA {
	return r.render(params, nil)
}

func (r *CPURenderer) render(params []float64, dirty *dirtySpanSet) *image.NRGBA {
	// Reset canvas to initial background using fast copy (avoids allocation)
	copy(r.canvas.Pix, r.initialBg)
	if dirty != nil {
		dirty.reset(r.height, r.k)
	}
	if len(params) != r.Dim() {
		return r.canvas
	}
	if r.k == 0 || r.height == 0 {
		return r.canvas
	}

	// Each worker owns a disjoint band of rows and composites every circle in
	// the original order. This keeps the output pixel-exact without locks.
	if r.threads <= 1 {
		r.renderRowsTracked(r.canvas, params, 0, r.height, dirty)
		return r.canvas
	}

	var workers sync.WaitGroup
	workers.Add(r.threads - 1)
	for worker := 0; worker < r.threads-1; worker++ {
		minY := worker * r.height / r.threads
		maxY := (worker + 1) * r.height / r.threads
		go func() {
			defer workers.Done()
			r.renderRowsTracked(r.canvas, params, minY, maxY, dirty)
		}()
	}
	r.renderRowsTracked(r.canvas, params, (r.threads-1)*r.height/r.threads, r.height, dirty)
	workers.Wait()

	return r.canvas
}

func (r *CPURenderer) renderRowsTracked(img *image.NRGBA, params []float64, minY, maxY int, dirty *dirtySpanSet) {
	r.compositeRows(img, params, r.k, minY, maxY, dirty)
}

func (r *CPURenderer) compositeRows(img *image.NRGBA, params []float64, count, minY, maxY int, dirty *dirtySpanSet) {
	pv := fit.ParamVector{Data: params, K: count, Width: r.width, Height: r.height}
	for i := 0; i < count; i++ {
		circle := pv.DecodeCircle(i)
		r.renderCircleScanlineRowsTracked(img, circle, minY, maxY, dirty)
	}
}

// compositeParams draws count circles from params onto img in draw order without
// resetting it to the initial background. Callers own img and its prior content;
// this is the primitive that lets an immutable prefix be rendered once and reused
// instead of being replayed on every evaluation.
func (r *CPURenderer) compositeParams(img *image.NRGBA, params []float64, count int) {
	if img == nil || count <= 0 || r.height == 0 {
		return
	}
	if len(params) < count*paramsPerCircle {
		return
	}
	if img.Bounds().Dx() != r.width || img.Bounds().Dy() != r.height {
		return
	}

	if r.threads <= 1 {
		r.compositeRows(img, params, count, 0, r.height, nil)
		return
	}

	var workers sync.WaitGroup
	workers.Add(r.threads - 1)
	for worker := 0; worker < r.threads-1; worker++ {
		minY := worker * r.height / r.threads
		maxY := (worker + 1) * r.height / r.threads
		go func() {
			defer workers.Done()
			r.compositeRows(img, params, count, minY, maxY, nil)
		}()
	}
	r.compositeRows(img, params, count, (r.threads-1)*r.height/r.threads, r.height, nil)
	workers.Wait()
}

// Cost computes error between params and reference
func (r *CPURenderer) Cost(params []float64) float64 {
	if len(params) != r.Dim() || r.width == 0 || r.height == 0 {
		return math.Inf(1)
	}
	if r.incrementalCostMode != incrementalCostDisabled && r.fastCostSelected && r.initialSSDValid {
		if r.incrementalCostMode == incrementalCostAuto && !r.incrementalCandidateWorthwhile(params) {
			return fit.FastMSECost(r.Render(params), r.reference)
		}
		rendered := r.render(params, &r.dirtySpans)
		if r.incrementalCostMode == incrementalCostForce || incrementalCostWorthwhile(&r.dirtySpans, r.width*r.height) {
			if total, ok := r.incrementalSSDTotal(rendered, &r.dirtySpans); ok {
				return float64(total) / float64(r.width*r.height*3)
			}
		}
		return fit.FastMSECost(rendered, r.reference)
	}
	rendered := r.Render(params)
	return r.costFunc(rendered, r.reference)
}

// newSession creates an independent CPU renderer that preserves the reference,
// base canvas, and selected cost function.
func (r *CPURenderer) newSession(circleCount int) (Renderer, func(), error) {
	if circleCount < 0 {
		return nil, noopCleanup, fmt.Errorf("circle count cannot be negative")
	}

	canvas := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
	initialBg := append([]byte(nil), r.initialBg...)
	return &CPURenderer{
		reference:            r.reference,
		k:                    circleCount,
		bounds:               fit.NewBounds(circleCount, r.width, r.height),
		costFunc:             r.costFunc,
		width:                r.width,
		height:               r.height,
		threads:              r.threads,
		opaqueCanvas:         r.opaqueCanvas,
		forceFloatGeometry:   r.forceFloatGeometry,
		forceFloat32Geometry: r.forceFloat32Geometry,
		enableRowSymmetry:    r.enableRowSymmetry,
		fastCompositing:      r.fastCompositing,
		initialSSD:           r.initialSSD,
		initialSSDValid:      r.initialSSDValid,
		fastCostSelected:     r.fastCostSelected,
		incrementalCostMode:  r.incrementalCostMode,
		stagedIncremental:    r.stagedIncremental,
		canvas:               canvas,
		initialBg:            initialBg,
	}, noopCleanup, nil
}

func exactInitialCanvasSSD(initial []byte, width, height int, reference *image.NRGBA) (uint64, bool) {
	canvas := &image.NRGBA{
		Pix:    initial,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}
	return fit.ExactSSD(canvas, reference)
}

// newSessionWithCanvas creates a staged session that renders only newly
// optimized circles over an already-retained canvas.
func (r *CPURenderer) newSessionWithCanvas(canvas *image.NRGBA, circleCount int) (Renderer, func(), error) {
	if canvas == nil {
		return nil, noopCleanup, fmt.Errorf("canvas cannot be nil")
	}
	if circleCount < 0 {
		return nil, noopCleanup, fmt.Errorf("circle count cannot be negative")
	}
	if canvas.Bounds().Dx() != r.width || canvas.Bounds().Dy() != r.height {
		return nil, noopCleanup, fmt.Errorf("canvas dimensions must match reference image")
	}

	session := NewCPURendererWithCanvas(r.reference, canvas, circleCount)
	session.costFunc = r.costFunc
	session.fastCostSelected = r.fastCostSelected
	session.incrementalCostMode = r.incrementalCostMode
	session.stagedIncremental = r.stagedIncremental
	if session.incrementalCostMode == incrementalCostDisabled && session.fastCostSelected &&
		session.stagedIncremental && session.incrementalStagedSessionEligible() {
		session.incrementalCostMode = incrementalCostAuto
	}
	session.threads = r.threads
	session.forceFloatGeometry = r.forceFloatGeometry
	session.forceFloat32Geometry = r.forceFloat32Geometry
	session.enableRowSymmetry = r.enableRowSymmetry
	session.fastCompositing = r.fastCompositing
	return session, noopCleanup, nil
}

// initialCanvas returns an independent snapshot of the configured base canvas.
func (r *CPURenderer) initialCanvas() *image.NRGBA {
	canvas := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
	copy(canvas.Pix, r.initialBg)
	return canvas
}

// Dim returns the dimensionality of the parameter space
func (r *CPURenderer) Dim() int {
	return r.k * 7 // paramsPerCircle
}

// Bounds returns lower and upper bounds for parameters
func (r *CPURenderer) Bounds() (lower, upper []float64) {
	return r.bounds.Lower, r.bounds.Upper
}

// Reference returns the reference image
func (r *CPURenderer) Reference() *image.NRGBA {
	return r.reference
}

// SetCostFunc sets the cost function used for evaluation
func (r *CPURenderer) SetCostFunc(costFunc fit.CostFunc) {
	r.costFunc = costFunc
	r.fastCostSelected = false
}

// UseFastCost restores the runtime-dispatched SIMD cost implementation after a
// custom cost function has been selected. New CPU renderers use this by default.
func (r *CPURenderer) UseFastCost() {
	r.costFunc = fit.FastMSECost
	r.fastCostSelected = true
}

// SetThreads configures CPU rendering parallelism. Non-positive values select
// GOMAXPROCS. Values above GOMAXPROCS or the image height are capped to avoid
// oversubscription and empty row shards.
// Call SetThreads before starting an optimization; changing renderer settings
// concurrently with Render is unsupported.
func (r *CPURenderer) SetThreads(threads int) {
	r.threads = effectiveThreadCount(threads, r.height)
}

// Threads returns the effective number of rendering workers.
func (r *CPURenderer) Threads() int {
	return r.threads
}

// SetParallelEvaluationWorkers configures how many concurrent cost evaluations
// the optimization pipeline may run. The pipeline then creates that many
// independent sessions, each with its own canvas, and gives every session a
// single rendering thread: with many evaluations in flight the row-band
// fan-out inside one render is pure overhead. Call it before starting an
// optimization.
//
// Non-positive values select GOMAXPROCS, matching SetThreads. The two setters
// must agree on what zero means: they are fed from adjacent configuration
// fields, and a setter that read zero as "one" would silently disable
// evaluation parallelism for any caller that had not filled the field in.
//
// The value is capped at GOMAXPROCS, which is the documented contract of the
// --threads flag. The cap is not merely advisory: every worker above one costs
// a full extra session with its own canvas and background copy (about
// 2*W*H*4 bytes), so an unclamped --threads 10000 would try to allocate
// hundreds of gigabytes at HD resolution. The cap deliberately does not reuse
// effectiveThreadCount, which additionally clamps to the image height: that is
// right for row sharding but wrong here, because evaluation concurrency is
// unrelated to how many rows a single render can split into.
func (r *CPURenderer) SetParallelEvaluationWorkers(workers int) {
	r.parallelEvaluationWorkers = effectiveEvaluationWorkers(workers)
}

// ParallelEvaluationWorkers reports the configured concurrent evaluation width.
func (r *CPURenderer) ParallelEvaluationWorkers() int {
	if r.parallelEvaluationWorkers < 1 {
		return 1
	}
	return r.parallelEvaluationWorkers
}

// ConfigureCPUParallelism applies both parallelism settings a job configuration
// carries. They are independent knobs: threads shards the rows of one render,
// while evaluationWorkers runs whole independent renders side by side, and the
// two compete for the same cores.
//
// Evaluation width is left alone unless parallelEvaluation is set, so the
// setting is inert until it is opted into. Every entry point that builds a CPU
// renderer from a configuration goes through here, so the two settings cannot
// drift apart between the CLI, resume, and the server.
func ConfigureCPUParallelism(cpu *CPURenderer, threads, evaluationWorkers int, parallelEvaluation bool) {
	cpu.SetThreads(threads)
	if parallelEvaluation {
		cpu.SetParallelEvaluationWorkers(evaluationWorkers)
	}
}

// effectiveEvaluationWorkers clamps a requested evaluation width into
// [1, GOMAXPROCS], resolving non-positive requests to GOMAXPROCS exactly as
// effectiveThreadCount does. Unlike effectiveThreadCount it ignores the image
// height, because concurrent evaluations are whole independent renders rather
// than row shards of one render.
func effectiveEvaluationWorkers(workers int) int {
	maxWorkers := runtime.GOMAXPROCS(0)
	if workers < 1 || workers > maxWorkers {
		workers = maxWorkers
	}
	if workers < 1 {
		return 1
	}
	return workers
}

// SetFastCompositing selects the reduced-precision float32 SIMD span
// compositor. Rendered output then differs from the exact float64 path by up to
// one unit per channel, so callers opt in explicitly.
func (r *CPURenderer) SetFastCompositing(enabled bool) {
	r.fastCompositing = enabled
}

// FastCompositing reports whether the reduced-precision span compositor is
// selected.
func (r *CPURenderer) FastCompositing() bool {
	return r.fastCompositing
}

// FastCompositingBackend names the kernel the fast compositor would use.
func FastCompositingBackend() string {
	return fastCompositeBackend
}

func effectiveThreadCount(threads, height int) int {
	if threads < 1 {
		threads = runtime.GOMAXPROCS(0)
	}
	if maxThreads := runtime.GOMAXPROCS(0); threads > maxThreads {
		threads = maxThreads
	}
	if height > 0 && threads > height {
		threads = height
	}
	if threads < 1 {
		return 1
	}
	return threads
}

// renderCircle composites a circle onto the image using premultiplied alpha
func (r *CPURenderer) renderCircle(img *image.NRGBA, c fit.Circle) {
	// Early-reject: circle is fully transparent
	if c.Opacity == 0 {
		return
	}

	// Compute AABB (Axis-Aligned Bounding Box)
	minXf := c.X - c.R
	maxXf := c.X + c.R
	minYf := c.Y - c.R
	maxYf := c.Y + c.R

	// Early-reject: circle completely outside image bounds
	if maxXf < 0 || minXf >= float64(r.width) || maxYf < 0 || minYf >= float64(r.height) {
		return
	}

	// Clamp AABB to image bounds (use integer arithmetic)
	minX := int(minXf)
	if minX < 0 {
		minX = 0
	}
	maxX := int(maxXf + 1) // +1 for ceiling
	if maxX > r.width {
		maxX = r.width
	}
	minY := int(minYf)
	if minY < 0 {
		minY = 0
	}
	maxY := int(maxYf + 1) // +1 for ceiling
	if maxY > r.height {
		maxY = r.height
	}

	r2 := c.R * c.R

	// Scan bounding box (note: using < instead of <= due to ceiling adjustment)
	for y := minY; y < maxY; y++ {
		for x := minX; x < maxX; x++ {
			// Check if inside circle
			dx := float64(x) - c.X
			dy := float64(y) - c.Y
			if dx*dx+dy*dy > r2 {
				continue
			}

			// Composite with premultiplied alpha
			compositePixel(img, x, y, c.CR, c.CG, c.CB, c.Opacity)
		}
	}
}

// renderCircleScanline uses scanline algorithm to avoid per-pixel distance checks
func (r *CPURenderer) renderCircleScanline(img *image.NRGBA, c fit.Circle) {
	r.renderCircleScanlineRows(img, c, 0, r.height)
}

// renderCircleScanlineRows composites the portion of c within [rowStart,
// rowEnd). Callers may safely process disjoint row ranges concurrently.
func (r *CPURenderer) renderCircleScanlineRows(img *image.NRGBA, c fit.Circle, rowStart, rowEnd int) {
	r.renderCircleScanlineRowsTracked(img, c, rowStart, rowEnd, nil)
}

func (r *CPURenderer) renderCircleScanlineRowsTracked(
	img *image.NRGBA,
	c fit.Circle,
	rowStart, rowEnd int,
	dirty *dirtySpanSet,
) {
	// Early-reject: circle is fully transparent
	if c.Opacity == 0 {
		return
	}

	// Compute vertical bounds
	minYf := c.Y - c.R
	maxYf := c.Y + c.R

	// Early-reject: circle completely outside image bounds
	if maxYf < 0 || minYf >= float64(r.height) {
		return
	}

	// Clamp to image bounds
	minY := int(minYf)
	if minY < 0 {
		minY = 0
	}
	if minY < rowStart {
		minY = rowStart
	}
	maxY := int(maxYf + 1) // +1 for ceiling
	if maxY > r.height {
		maxY = r.height
	}
	if maxY > rowEnd {
		maxY = rowEnd
	}

	r2 := c.R * c.R
	var fixedGeometry fixedCircleQ16
	useFixedGeometry := false
	if !r.forceFloatGeometry && !r.forceFloat32Geometry {
		fixedGeometry, useFixedGeometry = newFixedCircleQ16(c)
	}
	var center32, y32, radiusSquared32 float32
	if r.forceFloat32Geometry {
		center32 = float32(c.X)
		y32 = float32(c.Y)
		radius32 := float32(c.R)
		radiusSquared32 = radius32 * radius32
	}

	// Scanline algorithm: for each row, compute horizontal span
	if useFixedGeometry {
		r.renderFixedCircleRowsTracked(img, c, fixedGeometry, minY, maxY, dirty)
		return
	}
	for y := minY; y < maxY; y++ {
		if r.forceFloat32Geometry {
			dy := float32(y) - y32
			remaining := radiusSquared32 - dy*dy
			if remaining < 0 {
				continue
			}
			xStart, xEnd := circleSpanFloat32Selected(center32, remaining, r.width)
			if xStart < 0 {
				xStart = 0
			}
			if dirty != nil {
				dirty.add(y, xStart, xEnd)
			}
			r.compositeCircleSpan(img, c, y, xStart, xEnd)
			continue
		}

		// Calculate distance from row to circle center
		dy := float64(y) - c.Y
		dy2 := dy * dy

		// Check if row intersects circle
		if dy2 > r2 {
			continue // Row entirely outside circle
		}

		// Find the horizontal extent with the float64 oracle. This path also
		// handles geometry outside the safe signed Q16.16 coordinate range.
		xStart, xEnd := circleSpanFloat64(c.X, r2-dy2, r.width)
		if xStart < 0 {
			xStart = 0
		}
		if dirty != nil {
			dirty.add(y, xStart, xEnd)
		}
		r.compositeCircleSpan(img, c, y, xStart, xEnd)
	}
}

func (r *CPURenderer) renderFixedCircleRowsTracked(
	img *image.NRGBA,
	c fit.Circle,
	geometry fixedCircleQ16,
	minY, maxY int,
	dirty *dirtySpanSet,
) {
	rowSum, symmetric := geometry.symmetricRowSum()
	symmetric = symmetric && r.enableRowSymmetry
	if !symmetric {
		for y := minY; y < maxY; y++ {
			xStart, xEnd, intersects := geometry.span(y, r.width)
			if !intersects {
				continue
			}
			if dirty != nil {
				dirty.add(y, xStart, xEnd)
			}
			r.compositeCircleSpan(img, c, y, xStart, xEnd)
		}
		return
	}

	// Walk inward from both shard edges. If the edge rows are a symmetric
	// pair, one span search covers both and the loop advances twice. Otherwise
	// the edge farther from its partner is unpaired inside this shard and must
	// be rendered normally. This handles clipped and asymmetric worker bands
	// without walking the already-rendered half merely to skip it.
	for topY, bottomY := minY, maxY-1; topY <= bottomY; {
		mirrorY := rowSum - topY
		switch {
		case mirrorY > bottomY:
			r.renderFixedCircleRowTracked(img, c, geometry, topY, dirty)
			topY++
		case mirrorY < bottomY:
			r.renderFixedCircleRowTracked(img, c, geometry, bottomY, dirty)
			bottomY--
		default:
			xStart, xEnd, intersects := geometry.span(topY, r.width)
			if intersects {
				if dirty != nil {
					dirty.add(topY, xStart, xEnd)
				}
				if bottomY != topY {
					r.compositeCircleSpanPair(img, c, topY, bottomY, xStart, xEnd)
				} else {
					r.compositeCircleSpan(img, c, topY, xStart, xEnd)
				}
				if bottomY != topY {
					if dirty != nil {
						dirty.add(bottomY, xStart, xEnd)
					}
				}
			}
			topY++
			bottomY--
		}
	}
}

func (r *CPURenderer) renderFixedCircleRowTracked(
	img *image.NRGBA,
	c fit.Circle,
	geometry fixedCircleQ16,
	y int,
	dirty *dirtySpanSet,
) {
	xStart, xEnd, intersects := geometry.span(y, r.width)
	if !intersects {
		return
	}
	if dirty != nil {
		dirty.add(y, xStart, xEnd)
	}
	r.compositeCircleSpan(img, c, y, xStart, xEnd)
}

func (r *CPURenderer) compositeCircleSpan(img *image.NRGBA, c fit.Circle, y, xStart, xEnd int) {
	// Opaque canvases remain opaque under source-over compositing, so their
	// spans can use the runtime-dispatched SIMD implementation.
	if r.opaqueCanvas {
		if r.fastCompositing {
			compositeOpaqueSpanFast(img.Pix, y*img.Stride+xStart*4, xEnd-xStart, c.CR, c.CG, c.CB, c.Opacity)
			return
		}
		compositeOpaqueSpan(img.Pix, y*img.Stride+xStart*4, xEnd-xStart, c.CR, c.CG, c.CB, c.Opacity)
		return
	}
	for x := xStart; x < xEnd; x++ {
		compositePixel(img, x, y, c.CR, c.CG, c.CB, c.Opacity)
	}
}

func (r *CPURenderer) compositeCircleSpanPair(img *image.NRGBA, c fit.Circle, firstY, secondY, xStart, xEnd int) {
	if r.opaqueCanvas {
		if r.fastCompositing {
			// The float32 kernel has no paired variant; two vector spans keep the
			// same crossover behaviour as the single-span path.
			r.compositeCircleSpan(img, c, firstY, xStart, xEnd)
			r.compositeCircleSpan(img, c, secondY, xStart, xEnd)
			return
		}
		compositeOpaqueSpanPair(
			img.Pix,
			firstY*img.Stride+xStart*4,
			secondY*img.Stride+xStart*4,
			xEnd-xStart,
			c.CR,
			c.CG,
			c.CB,
			c.Opacity,
		)
		return
	}
	r.compositeCircleSpan(img, c, firstY, xStart, xEnd)
	r.compositeCircleSpan(img, c, secondY, xStart, xEnd)
}

// renderCircleHybrid uses bounding box for small circles and scanline for large ones
// This combines the best of both approaches: avoid search overhead for small circles,
// gain algorithmic advantage for large circles.
//
// BENCHMARK NOTE: Current benchmarks show scanline is faster for ALL circle sizes.
// Direct call to scanline for best performance.
func (r *CPURenderer) renderCircleHybrid(img *image.NRGBA, c fit.Circle) {
	// Benchmarking shows scanline is consistently faster across all circle sizes
	// No conditional needed - always use scanline
	r.renderCircleScanline(img, c)
}

// Optimization constants
const inv255 = 1.0 / 255.0 // Reciprocal for fast division

// compositePixel blends a color onto the image at (x,y) using premultiplied alpha
func compositePixel(img *image.NRGBA, x, y int, r, g, b, alpha float64) {
	// Inline PixOffset calculation (faster than function call)
	i := y*img.Stride + x*4

	// Current background color (non-premultiplied) - use reciprocal multiplication
	bgR := float64(img.Pix[i+0]) * inv255
	bgG := float64(img.Pix[i+1]) * inv255
	bgB := float64(img.Pix[i+2]) * inv255

	// Foreground premultiplied
	fgR := r * alpha
	fgG := g * alpha
	fgB := b * alpha

	// An opaque destination remains opaque under source-over compositing. This
	// is the common path for the default white canvas and avoids alpha
	// normalization, the output-alpha reciprocal, and the alpha store.
	if img.Pix[i+3] == 255 {
		bgBlend := 1 - alpha
		img.Pix[i+0] = uint8((fgR+bgR*bgBlend)*255 + 0.5)
		img.Pix[i+1] = uint8((fgG+bgG*bgBlend)*255 + 0.5)
		img.Pix[i+2] = uint8((fgB+bgB*bgBlend)*255 + 0.5)
		return
	}

	bgA := float64(img.Pix[i+3]) * inv255
	fgA := alpha

	// Porter-Duff "over" operator
	outA := fgA + bgA*(1-fgA)
	if outA == 0 {
		return // Transparent
	}

	// Hoist division: compute reciprocal once, multiply three times
	invOutA := 1.0 / outA

	// Precompute common subexpression
	bgBlend := bgA * (1 - fgA)

	outR := (fgR + bgR*bgBlend) * invOutA
	outG := (fgG + bgG*bgBlend) * invOutA
	outB := (fgB + bgB*bgBlend) * invOutA

	// Write back as 8-bit (use int conversion with +0.5 for rounding, faster than math.Round)
	img.Pix[i+0] = uint8(outR*255 + 0.5)
	img.Pix[i+1] = uint8(outG*255 + 0.5)
	img.Pix[i+2] = uint8(outB*255 + 0.5)
	img.Pix[i+3] = uint8(outA*255 + 0.5)
}
