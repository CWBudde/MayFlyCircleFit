package renderer

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// CircleAudit describes both the raster visibility and objective usefulness of
// one circle in a completed batch. Circle and OriginalCircle are one-based.
type CircleAudit struct {
	Circle                  int
	OriginalCircle          int
	IntroducedChangedPixels int
	FinalChangedPixels      int
	CostWithout             float64
	MSEContribution         float64
	Valid                   bool
	ValidationError         string
}

// BatchAudit is a post-optimization diagnostic. MSEContribution is positive
// when removing a circle makes the result worse, zero when it has no effect,
// and negative when the image improves without it.
type BatchAudit struct {
	MSE     float64
	Circles []CircleAudit
}

// AuditCircleBatch measures a batch against the renderer's configured base
// canvas and reference. It deliberately runs outside the optimizer hot path:
// each circle is rendered incrementally and once with that circle omitted.
func AuditCircleBatch(r Renderer, params []float64) (BatchAudit, error) {
	if r == nil {
		return BatchAudit{}, errors.New("renderer cannot be nil")
	}

	if len(params) != r.Dim() || len(params)%paramsPerCircle != 0 {
		return BatchAudit{}, fmt.Errorf("parameter count %d does not match renderer dimension %d", len(params), r.Dim())
	}

	circleCount := len(params) / paramsPerCircle

	reference := r.Reference()
	if reference == nil {
		return BatchAudit{}, errors.New("renderer reference cannot be nil")
	}

	fullImage := cloneNRGBA(r.Render(params))
	fullCost := fit.FastMSECost(fullImage, reference)
	audit := BatchAudit{
		MSE:     fullCost,
		Circles: make([]CircleAudit, circleCount),
	}

	referenceBounds := reference.Bounds()
	parameterBounds := fit.NewBounds(circleCount, referenceBounds.Dx(), referenceBounds.Dy())
	vector := fit.ParamVector{
		Data: params, K: circleCount,
		Width: referenceBounds.Dx(), Height: referenceBounds.Dy(),
	}

	// Every circle writes its own slot, and the measurement of one circle reads
	// nothing another circle produces, so walking several runs of the draw order
	// at once changes throughput and nothing else.
	measure := func(stepper auditStepper, circle int) {
		introduced, withoutImage := stepper.step(circle)
		costWithout := fit.FastMSECost(withoutImage, reference)
		validationErr := parameterBounds.ValidateCircle(vector.DecodeCircle(circle))
		audit.Circles[circle] = CircleAudit{
			Circle:                  circle + 1,
			OriginalCircle:          circle + 1,
			IntroducedChangedPixels: introduced,
			FinalChangedPixels:      changedPixelCount(fullImage, withoutImage),
			CostWithout:             costWithout,
			MSEContribution:         costWithout - fullCost,
			Valid:                   validationErr == nil,
			ValidationError:         errorString(validationErr),
		}
	}

	plan, release := planAudit(r, params, circleCount)
	defer release()

	if len(plan.steppers) < 2 {
		stepper := newAuditStepper(r, params, circleCount)
		for circle := range circleCount {
			measure(stepper, circle)
		}

		return audit, nil
	}

	// Chunks are taken from a shared queue rather than dealt out up front,
	// because the work per circle is not uniform: auditing circle c composites
	// the circleCount-c-1 circles drawn after it, so the front of the vector
	// costs several times what the back does. A fixed split would leave the
	// worker holding the front finishing long after the rest; a queue with
	// several chunks per worker lets whoever finishes early take the next run.
	var next atomic.Int64
	walk := func(stepper *accumulatedAuditStepper) {
		for {
			index := int(next.Add(1)) - 1
			if index >= len(plan.chunks) {
				return
			}

			chunk := plan.chunks[index]
			stepper.restart(chunk.start)

			for circle := chunk.start; circle < chunk.end; circle++ {
				measure(stepper, circle)
			}
		}
	}
	var workers sync.WaitGroup
	workers.Add(len(plan.steppers) - 1)

	for _, stepper := range plan.steppers[1:] {
		go func() {
			defer workers.Done()

			walk(stepper)
		}()
	}

	walk(plan.steppers[0])
	workers.Wait()

	return audit, nil
}

const (
	// minAuditChunkCircles is the shortest run worth taking from the queue. Every
	// chunk rebuilds the prefix its first circle sits on, so a run of one circle
	// can cost more to set up than to walk.
	minAuditChunkCircles = 4
	// auditChunksPerWorker decides how finely the draw order is cut. More chunks
	// balance the uneven per-circle work better and cost one more prefix rebuild
	// each: rebuilds are linear in the circle count where the walk is quadratic,
	// so a handful per worker is cheap insurance against a bad split.
	auditChunksPerWorker = 4
)

// auditChunk is a contiguous run of draw slots one stepper walks in one go.
type auditChunk struct {
	start, end int
}

// auditPlan is a set of independent walkers over one parameter vector together
// with the runs of circles they share out between them.
type auditPlan struct {
	steppers []*accumulatedAuditStepper
	chunks   []auditChunk
}

// planAudit opens one stepper per worker, each with its own session and its own
// canvases, and cuts the draw order into runs for them to take. A stepper that
// picks up a run starting at s rebuilds the prefix holding circles [0, s),
// which is linear in the circle count against the quadratic cost of the walk
// itself.
//
// It returns an empty plan -- leaving the caller on the historical serial path,
// canvases and all -- for a backend that cannot hand out independent in-place
// compositors, and for a vector too short to be worth splitting.
func planAudit(r Renderer, params []float64, circleCount int) (auditPlan, func()) {
	if _, inPlace := r.(inPlaceCompositor); !inPlace {
		return auditPlan{}, noopCleanup
	}

	workers := min(renderWorkers(r), circleCount/(minAuditChunkCircles*auditChunksPerWorker))

	sessions, release := concurrentSessions(r, circleCount, workers)
	if len(sessions) < 2 {
		release()
		return auditPlan{}, noopCleanup
	}

	plan := auditPlan{steppers: make([]*accumulatedAuditStepper, 0, len(sessions))}
	for _, session := range sessions {
		compositor, ok := session.(inPlaceCompositor)
		if !ok {
			release()
			return auditPlan{}, noopCleanup
		}

		stepper := newAccumulatedAuditStepper(compositor, params, circleCount, 0)
		if stepper == nil {
			release()
			return auditPlan{}, noopCleanup
		}

		plan.steppers = append(plan.steppers, stepper)
	}

	plan.chunks = auditChunks(circleCount, len(plan.steppers)*auditChunksPerWorker)

	return plan, release
}

// auditChunks cuts [0, circleCount) into at most count contiguous runs of equal
// length. Balancing is left to the queue the runs are taken from, so the cut
// itself only has to be fine enough to give it something to balance.
func auditChunks(circleCount, count int) []auditChunk {
	count = max(min(count, circleCount), 1)

	chunks := make([]auditChunk, 0, count)
	for index := range count {
		start := index * circleCount / count

		end := (index + 1) * circleCount / count
		if start < end {
			chunks = append(chunks, auditChunk{start: start, end: end})
		}
	}

	return chunks
}

// inPlaceCompositor is implemented by renderers that can draw a run of circles
// onto a caller-owned canvas without resetting it. It lets the audit keep the
// already-composited prefix instead of replaying the whole draw order for every
// circle.
type inPlaceCompositor interface {
	compositeParams(img *image.NRGBA, params []float64, count int)
	initialCanvas() *image.NRGBA
}

// auditStepper walks the draw order one circle at a time. step composites the
// circle onto the retained prefix and returns both the pixels it introduced and
// the image of the complete vector without it. The returned image stays valid
// only until the next call.
type auditStepper interface {
	step(circle int) (int, *image.NRGBA)
}

func newAuditStepper(r Renderer, params []float64, circleCount int) auditStepper {
	if compositor, ok := r.(inPlaceCompositor); ok {
		if stepper := newAccumulatedAuditStepper(compositor, params, circleCount, 0); stepper != nil {
			return stepper
		}
	}

	progressive := append([]float64(nil), params...)
	for offset := 0; offset < len(progressive); offset += paramsPerCircle {
		progressive[offset+6] = 0
	}

	return &replayAuditStepper{
		r:           r,
		params:      params,
		progressive: progressive,
		without:     append([]float64(nil), params...),
		previous:    cloneNRGBA(r.Render(progressive)),
	}
}

// replayAuditStepper re-renders the complete parameter vector for every step. It
// is the portable fallback for backends that cannot composite in place.
type replayAuditStepper struct {
	r           Renderer
	params      []float64
	progressive []float64
	without     []float64
	previous    *image.NRGBA
}

func (s *replayAuditStepper) step(circle int) (int, *image.NRGBA) {
	offset := circle * paramsPerCircle
	s.progressive[offset+6] = s.params[offset+6]
	rendered := s.r.Render(s.progressive)
	introduced := changedPixelCount(s.previous, rendered)
	copy(s.previous.Pix, rendered.Pix)

	copy(s.without, s.params)
	s.without[offset+6] = 0

	return introduced, s.r.Render(s.without)
}

// accumulatedAuditStepper retains the canvas holding circles [0, circle) and
// composites forward from it. Draw order is sequential, so that prefix plus the
// suffix after a circle is exactly the image rendered without that circle.
type accumulatedAuditStepper struct {
	compositor inPlaceCompositor
	params     []float64
	count      int
	// initial is the untouched base canvas every prefix is rebuilt from, which
	// is what lets one stepper be repositioned instead of reallocated.
	initial *image.NRGBA
	prefix  *image.NRGBA
	next    *image.NRGBA
	without *image.NRGBA
}

// newAccumulatedAuditStepper opens a stepper positioned at start, with the
// prefix already holding circles [0, start). It returns nil for a compositor
// with no base canvas to start from.
func newAccumulatedAuditStepper(compositor inPlaceCompositor, params []float64, circleCount, start int) *accumulatedAuditStepper {
	initial := compositor.initialCanvas()
	if initial == nil {
		return nil
	}

	stepper := &accumulatedAuditStepper{
		compositor: compositor,
		params:     params,
		count:      circleCount,
		initial:    initial,
		prefix:     cloneNRGBA(initial),
		next:       cloneNRGBA(initial),
		without:    cloneNRGBA(initial),
	}
	stepper.restart(start)

	return stepper
}

// restart repositions the stepper at start by rebuilding the prefix holding
// circles [0, start). It is what lets several steppers cover disjoint runs of
// one vector concurrently, and one stepper cover several runs in turn.
func (s *accumulatedAuditStepper) restart(start int) {
	copy(s.prefix.Pix, s.initial.Pix)

	if start > 0 {
		s.compositor.compositeParams(s.prefix, s.params, start)
	}
}

func (s *accumulatedAuditStepper) step(circle int) (int, *image.NRGBA) {
	offset := circle * paramsPerCircle

	copy(s.without.Pix, s.prefix.Pix)
	s.compositor.compositeParams(s.without, s.params[offset+paramsPerCircle:], s.count-circle-1)

	copy(s.next.Pix, s.prefix.Pix)
	s.compositor.compositeParams(s.next, s.params[offset:], 1)
	introduced := changedPixelCount(s.prefix, s.next)
	s.prefix, s.next = s.next, s.prefix

	return introduced, s.without
}

// CirclePruneOptions controls iterative batch pruning. A circle is removed if
// it changes fewer than MinChangedPixels in the final image or contributes no
// more than MinMSEContribution to MSE. Zero-value options therefore remove
// zero-pixel and non-positive-contribution circles.
type CirclePruneOptions struct {
	MinChangedPixels   int
	MinMSEContribution float64
	MaxRemoved         int
}

// CircleRemoval records an iterative pruning decision. OriginalCircle refers
// to the input draw order even after earlier circles have been removed.
type CircleRemoval struct {
	OriginalCircle     int
	FinalChangedPixels int
	MSEContribution    float64
}

// CirclePruneResult contains a pruned parameter vector in its original draw
// order and a fresh audit of the retained circles.
type CirclePruneResult struct {
	Params  []float64
	Removed []CircleRemoval
	Audit   BatchAudit
}

// PruneCircleBatch repeatedly removes the least useful eligible circle and
// re-audits the remaining batch. Re-auditing matters because overlapping
// circles can become useful after a later or redundant circle is removed.
func PruneCircleBatch(base Renderer, params []float64, options CirclePruneOptions) (CirclePruneResult, error) {
	if base == nil {
		return CirclePruneResult{}, errors.New("renderer cannot be nil")
	}

	if len(params)%paramsPerCircle != 0 {
		return CirclePruneResult{}, fmt.Errorf("parameter count %d is not divisible by %d", len(params), paramsPerCircle)
	}

	if options.MinChangedPixels < 0 {
		return CirclePruneResult{}, errors.New("minimum changed pixels cannot be negative")
	}

	if math.IsNaN(options.MinMSEContribution) || math.IsInf(options.MinMSEContribution, 0) {
		return CirclePruneResult{}, errors.New("minimum MSE contribution must be finite")
	}

	if options.MaxRemoved < 0 {
		return CirclePruneResult{}, errors.New("maximum removed circles cannot be negative")
	}

	minChanged := options.MinChangedPixels
	if minChanged == 0 {
		minChanged = 1
	}

	retained := append([]float64(nil), params...)

	original := make([]int, len(params)/paramsPerCircle)
	for i := range original {
		original[i] = i + 1
	}

	result := CirclePruneResult{}

	for {
		audit, err := auditWithCircleCount(base, retained)
		if err != nil {
			return CirclePruneResult{}, err
		}

		for i := range audit.Circles {
			audit.Circles[i].OriginalCircle = original[i]
		}

		remove := leastUsefulCircle(audit.Circles, minChanged, options.MinMSEContribution)
		if remove < 0 || options.MaxRemoved > 0 && len(result.Removed) >= options.MaxRemoved {
			result.Params = retained
			result.Audit = audit

			return result, nil
		}

		circle := audit.Circles[remove]
		result.Removed = append(result.Removed, CircleRemoval{
			OriginalCircle:     original[remove],
			FinalChangedPixels: circle.FinalChangedPixels,
			MSEContribution:    circle.MSEContribution,
		})
		retained = removeCircleParams(retained, remove)
		original = append(original[:remove], original[remove+1:]...)
	}
}

func auditWithCircleCount(base Renderer, params []float64) (BatchAudit, error) {
	if base.Dim() == len(params) {
		return AuditCircleBatch(base, params)
	}

	factory, ok := base.(rendererSessionFactory)
	if !ok {
		return BatchAudit{}, fmt.Errorf("%w: cannot audit %d circles with %T", ErrStagedOptimizationUnsupported, len(params)/paramsPerCircle, base)
	}

	session, cleanup, err := factory.newSession(len(params) / paramsPerCircle)
	if err != nil {
		return BatchAudit{}, err
	}
	defer cleanup()

	return AuditCircleBatch(session, params)
}

func leastUsefulCircle(circles []CircleAudit, minChanged int, minContribution float64) int {
	remove := -1

	for i, circle := range circles {
		eligible := !circle.Valid || circle.FinalChangedPixels < minChanged || circle.MSEContribution <= minContribution
		if !eligible {
			continue
		}

		if remove < 0 || circle.MSEContribution < circles[remove].MSEContribution ||
			circle.MSEContribution == circles[remove].MSEContribution && circle.FinalChangedPixels < circles[remove].FinalChangedPixels {
			remove = i
		}
	}

	return remove
}

func removeCircleParams(params []float64, circle int) []float64 {
	offset := circle * paramsPerCircle
	result := make([]float64, 0, len(params)-paramsPerCircle)
	result = append(result, params[:offset]...)
	result = append(result, params[offset+paramsPerCircle:]...)

	return result
}

func changedPixelCount(a, b *image.NRGBA) int {
	if a == nil || b == nil || !a.Bounds().Eq(b.Bounds()) {
		return 0
	}

	changed := 0

	bounds := a.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			aOffset := a.PixOffset(x, y)

			bOffset := b.PixOffset(x, y)
			if !bytes.Equal(a.Pix[aOffset:aOffset+4], b.Pix[bOffset:bOffset+4]) {
				changed++
			}
		}
	}

	return changed
}

// ResidualSeedOptions controls deterministic replacement-circle seeding.
// Radius, Opacity, and MinSeparation use useful image-relative defaults when
// zero. Explicit non-zero values are validated rather than silently repaired.
type ResidualSeedOptions struct {
	Radius        float64
	Opacity       float64
	MinSeparation float64
	// Region restricts candidate centers to an image subregion. An empty region
	// uses the complete canvas.
	Region image.Rectangle
}

type residualPixel struct {
	x, y   int
	energy uint32
}

// SeedCirclesFromResidual places replacement circles at separated high-error
// pixels. Their colors compensate for the configured opacity so compositing
// moves the current pixel toward the reference pixel.
func SeedCirclesFromResidual(canvas, reference *image.NRGBA, count int, options ResidualSeedOptions) ([]fit.Circle, error) {
	if canvas == nil || reference == nil {
		return nil, errors.New("canvas and reference cannot be nil")
	}

	if count < 0 {
		return nil, errors.New("circle count cannot be negative")
	}

	if canvas.Bounds().Dx() != reference.Bounds().Dx() || canvas.Bounds().Dy() != reference.Bounds().Dy() {
		return nil, errors.New("canvas dimensions must match reference image")
	}

	if count == 0 {
		return []fit.Circle{}, nil
	}

	width, height := canvas.Bounds().Dx(), canvas.Bounds().Dy()
	if width == 0 || height == 0 {
		return nil, errors.New("cannot seed circles on an empty canvas")
	}

	if count > width*height {
		return nil, fmt.Errorf("circle count %d exceeds the %d distinct canvas pixels", count, width*height)
	}

	region := options.Region
	if region.Empty() {
		region = canvas.Bounds()
	} else {
		region = region.Intersect(canvas.Bounds())
		if region.Empty() {
			return nil, errors.New("residual seed region does not intersect the canvas")
		}
	}

	if count > region.Dx()*region.Dy() {
		return nil, fmt.Errorf("circle count %d exceeds the %d distinct region pixels", count, region.Dx()*region.Dy())
	}

	radius := options.Radius
	if radius == 0 {
		radius = math.Max(fit.MinCircleRadius, float64(min(width, height))/20)
	}

	maxRadius := float64(max(width, height))
	if math.IsNaN(radius) || math.IsInf(radius, 0) || radius < fit.MinCircleRadius || radius > maxRadius {
		return nil, fmt.Errorf("radius must be finite and within [%g, %g]", fit.MinCircleRadius, maxRadius)
	}

	opacity := options.Opacity
	if opacity == 0 {
		opacity = 0.5
	}

	if math.IsNaN(opacity) || math.IsInf(opacity, 0) || opacity <= 0 || opacity > 1 {
		return nil, errors.New("opacity must be finite and within (0, 1]")
	}

	separation := options.MinSeparation
	if separation == 0 {
		separation = 2 * radius
	}

	if math.IsNaN(separation) || math.IsInf(separation, 0) || separation < 0 {
		return nil, errors.New("minimum separation must be finite and non-negative")
	}

	pixels := make([]residualPixel, 0, region.Dx()*region.Dy())
	canvasBounds := canvas.Bounds()
	referenceBounds := reference.Bounds()

	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			canvasOffset := canvas.PixOffset(x, y)
			referenceOffset := reference.PixOffset(referenceBounds.Min.X+x-canvasBounds.Min.X, referenceBounds.Min.Y+y-canvasBounds.Min.Y)
			dr := int(canvas.Pix[canvasOffset]) - int(reference.Pix[referenceOffset])
			dg := int(canvas.Pix[canvasOffset+1]) - int(reference.Pix[referenceOffset+1])
			db := int(canvas.Pix[canvasOffset+2]) - int(reference.Pix[referenceOffset+2])
			pixels = append(pixels, residualPixel{x: x - canvasBounds.Min.X, y: y - canvasBounds.Min.Y, energy: uint32(dr*dr + dg*dg + db*db)})
		}
	}

	sort.SliceStable(pixels, func(i, j int) bool { return pixels[i].energy > pixels[j].energy })

	selected := make([]residualPixel, 0, count)
	for _, pixel := range pixels {
		if len(selected) == count {
			break
		}

		if separatedFromAll(pixel, selected, separation) {
			selected = append(selected, pixel)
		}
	}
	// A large requested separation may not fit all replacements. Fill the
	// remainder by residual rank while avoiding duplicate centers.
	for _, pixel := range pixels {
		if len(selected) == count {
			break
		}

		if !containsResidualPixel(selected, pixel) {
			selected = append(selected, pixel)
		}
	}

	circles := make([]fit.Circle, len(selected))
	for i, pixel := range selected {
		canvasOffset := canvas.PixOffset(canvasBounds.Min.X+pixel.x, canvasBounds.Min.Y+pixel.y)
		referenceOffset := reference.PixOffset(referenceBounds.Min.X+pixel.x, referenceBounds.Min.Y+pixel.y)
		circles[i] = fit.Circle{
			X:       float64(pixel.x),
			Y:       float64(pixel.y),
			R:       radius,
			CR:      correctiveChannel(canvas.Pix[canvasOffset], reference.Pix[referenceOffset], opacity),
			CG:      correctiveChannel(canvas.Pix[canvasOffset+1], reference.Pix[referenceOffset+1], opacity),
			CB:      correctiveChannel(canvas.Pix[canvasOffset+2], reference.Pix[referenceOffset+2], opacity),
			Opacity: opacity,
		}
	}

	return circles, nil
}

// SeedParamsFromResidual is the flat-vector form used by optimizer candidates.
func SeedParamsFromResidual(canvas, reference *image.NRGBA, count int, options ResidualSeedOptions) ([]float64, error) {
	circles, err := SeedCirclesFromResidual(canvas, reference, count, options)
	if err != nil {
		return nil, err
	}

	params := make([]float64, len(circles)*paramsPerCircle)

	vector := fit.ParamVector{Data: params, K: len(circles), Width: canvas.Bounds().Dx(), Height: canvas.Bounds().Dy()}
	for i, circle := range circles {
		vector.EncodeCircle(i, circle)
	}

	return params, nil
}

func separatedFromAll(candidate residualPixel, selected []residualPixel, minimum float64) bool {
	minimumSquared := minimum * minimum

	for _, existing := range selected {
		dx := float64(candidate.x - existing.x)

		dy := float64(candidate.y - existing.y)
		if dx*dx+dy*dy < minimumSquared {
			return false
		}
	}

	return true
}

func containsResidualPixel(selected []residualPixel, candidate residualPixel) bool {
	for _, existing := range selected {
		if existing.x == candidate.x && existing.y == candidate.y {
			return true
		}
	}

	return false
}

func correctiveChannel(canvas, reference uint8, opacity float64) float64 {
	canvasValue := float64(canvas) / 255
	referenceValue := float64(reference) / 255

	return math.Max(0, math.Min(1, (referenceValue-(1-opacity)*canvasValue)/opacity))
}
