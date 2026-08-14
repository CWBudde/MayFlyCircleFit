package renderer

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"sort"

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
		return BatchAudit{}, fmt.Errorf("renderer cannot be nil")
	}
	if len(params) != r.Dim() || len(params)%paramsPerCircle != 0 {
		return BatchAudit{}, fmt.Errorf("parameter count %d does not match renderer dimension %d", len(params), r.Dim())
	}

	circleCount := len(params) / paramsPerCircle
	reference := r.Reference()
	if reference == nil {
		return BatchAudit{}, fmt.Errorf("renderer reference cannot be nil")
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

	progressive := append([]float64(nil), params...)
	for offset := 0; offset < len(progressive); offset += paramsPerCircle {
		progressive[offset+6] = 0
	}
	previous := cloneNRGBA(r.Render(progressive))

	for circle := 0; circle < circleCount; circle++ {
		offset := circle * paramsPerCircle
		progressive[offset+6] = params[offset+6]
		rendered := r.Render(progressive)
		introduced := changedPixelCount(previous, rendered)
		copy(previous.Pix, rendered.Pix)

		without := append([]float64(nil), params...)
		without[offset+6] = 0
		withoutImage := r.Render(without)
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

	return audit, nil
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
		return CirclePruneResult{}, fmt.Errorf("renderer cannot be nil")
	}
	if len(params)%paramsPerCircle != 0 {
		return CirclePruneResult{}, fmt.Errorf("parameter count %d is not divisible by %d", len(params), paramsPerCircle)
	}
	if options.MinChangedPixels < 0 {
		return CirclePruneResult{}, fmt.Errorf("minimum changed pixels cannot be negative")
	}
	if math.IsNaN(options.MinMSEContribution) || math.IsInf(options.MinMSEContribution, 0) {
		return CirclePruneResult{}, fmt.Errorf("minimum MSE contribution must be finite")
	}
	if options.MaxRemoved < 0 {
		return CirclePruneResult{}, fmt.Errorf("maximum removed circles cannot be negative")
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
		return nil, fmt.Errorf("canvas and reference cannot be nil")
	}
	if count < 0 {
		return nil, fmt.Errorf("circle count cannot be negative")
	}
	if canvas.Bounds().Dx() != reference.Bounds().Dx() || canvas.Bounds().Dy() != reference.Bounds().Dy() {
		return nil, fmt.Errorf("canvas dimensions must match reference image")
	}
	if count == 0 {
		return []fit.Circle{}, nil
	}
	width, height := canvas.Bounds().Dx(), canvas.Bounds().Dy()
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("cannot seed circles on an empty canvas")
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
			return nil, fmt.Errorf("residual seed region does not intersect the canvas")
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
		return nil, fmt.Errorf("opacity must be finite and within (0, 1]")
	}
	separation := options.MinSeparation
	if separation == 0 {
		separation = 2 * radius
	}
	if math.IsNaN(separation) || math.IsInf(separation, 0) || separation < 0 {
		return nil, fmt.Errorf("minimum separation must be finite and non-negative")
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
