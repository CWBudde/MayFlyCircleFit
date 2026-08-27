package renderer

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cwbudde/circlefit/internal/fit"
)

// CircleVisibility reports the number of canvas pixels changed when one circle
// is introduced in draw order. A zero count means that the circle is invisible
// at that point: for example, it may be transparent, outside the canvas, or
// indistinguishable from the canvas beneath it.
type CircleVisibility struct {
	Circle          int
	ChangedPixels   int
	Valid           bool
	ValidationError string
}

// AnalyzeCircleVisibility replays params incrementally and reports whether each
// circle changes the configured base canvas. It is intended for result
// diagnostics, not the optimizer's hot evaluation path: it performs one full
// render per circle.
func AnalyzeCircleVisibility(r Renderer, params []float64) ([]CircleVisibility, error) {
	if r == nil {
		return nil, errors.New("renderer cannot be nil")
	}

	if len(params) != r.Dim() || len(params)%paramsPerCircle != 0 {
		return nil, fmt.Errorf("parameter count %d does not match renderer dimension %d", len(params), r.Dim())
	}

	circleCount := len(params) / paramsPerCircle
	referenceBounds := r.Reference().Bounds()
	parameterBounds := fit.NewBounds(circleCount, referenceBounds.Dx(), referenceBounds.Dy())
	vector := fit.ParamVector{
		Data: params, K: circleCount,
		Width: referenceBounds.Dx(), Height: referenceBounds.Dy(),
	}

	working := append([]float64(nil), params...)
	for offset := 0; offset < len(working); offset += paramsPerCircle {
		working[offset+6] = 0
	}

	base := r.Render(working)
	previous := append([]byte(nil), base.Pix...)

	visibility := make([]CircleVisibility, circleCount)
	for circle := range circleCount {
		offset := circle * paramsPerCircle
		working[offset+6] = params[offset+6]
		rendered := r.Render(working)

		changed := 0

		bounds := rendered.Bounds()
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				pixelOffset := rendered.PixOffset(x, y)
				if !bytes.Equal(previous[pixelOffset:pixelOffset+4], rendered.Pix[pixelOffset:pixelOffset+4]) {
					changed++
				}
			}
		}

		validationErr := parameterBounds.ValidateCircle(vector.DecodeCircle(circle))
		visibility[circle] = CircleVisibility{
			Circle:          circle + 1,
			ChangedPixels:   changed,
			Valid:           validationErr == nil,
			ValidationError: errorString(validationErr),
		}

		copy(previous, rendered.Pix)
	}

	return visibility, nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
