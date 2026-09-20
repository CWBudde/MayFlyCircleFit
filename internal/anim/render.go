package anim

import (
	"fmt"
	"image"
	"image/draw"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
)

// Sink receives each rendered frame in order.
//
// The image it is handed is the renderer's own buffer and is overwritten by the
// next frame, so a sink that keeps it -- or hands it to another goroutine --
// must copy first.
//
// The work a sink does is on the critical path: Render is one sequential loop,
// because every frame composites onto the canvas the previous one committed.
// Wrap it in a SinkPool to move that work off the loop, which is what turns an
// animation export from a single-threaded job into a parallel one.
type Sink func(index int, img *image.NRGBA) error

// Render executes a plan, compositing through the project's own renderer so a
// frame is byte-identical to the image the same circles produce as best.png.
//
// The loop itself cannot be parallelised: a frame draws over the background the
// frame before it committed, which is the Pascal BackDraw/Drawing split and the
// whole reason the port is faithful. What can be parallelised is everything
// after the composite, and a caller does that by handing Render a SinkPool.
//
// background is the canvas the arrangement was fitted over, already at the
// sequence's frame size: white for an ordinary run, the base image for a run
// given a custom canvas.
func Render(sequence *Sequence, background *image.NRGBA, sink Sink) error {
	err := checkCanvas(sequence, background)
	if err != nil {
		return err
	}

	state := &execution{
		slots:      max(sequence.Slots, 1),
		background: clone(background),
		sequence:   sequence,
	}
	state.rebuild()

	for index, frame := range sequence.Frames {
		img, renderErr := state.frame(frame)
		if renderErr != nil {
			return renderErr
		}

		err = sink(index, img)
		if err != nil {
			return err
		}

		state.last = img
	}

	return nil
}

type execution struct {
	sequence   *Sequence
	background *image.NRGBA
	renderer   *renderer.CPURenderer
	vignette   *image.NRGBA
	last       *image.NRGBA
	slots      int
}

// rebuild points the renderer at the current background. A renderer owns an
// immutable initial canvas, so making a commit permanent means constructing a
// new one; that happens once per commit, never once per frame.
func (e *execution) rebuild() {
	e.renderer = renderer.NewCPURendererWithCanvas(e.background, e.background, e.slots)
}

func (e *execution) frame(frame Frame) (*image.NRGBA, error) {
	if frame.Vignette {
		return e.vignetteFrame(), nil
	}

	if len(frame.Commit) > 0 {
		committed, err := e.render(frame.Commit)
		if err != nil {
			return nil, err
		}

		e.background = clone(committed)
		e.rebuild()
	}

	if len(frame.Active) == 0 {
		return e.background, nil
	}

	return e.render(frame.Active)
}

func (e *execution) render(params []float64) (*image.NRGBA, error) {
	padded, err := pad(params, e.slots)
	if err != nil {
		return nil, err
	}

	return e.renderer.Render(padded), nil
}

// vignetteFrame applies one step of the closing sequence. The steps accumulate
// on their own image rather than on the background, which is what makes the
// margin darken and the border brighten over the run of them.
func (e *execution) vignetteFrame() *image.NRGBA {
	if e.vignette == nil {
		e.vignette = clone(e.last)
	}

	applyVignetteStep(e.vignette, e.sequence.OffsetX, e.sequence.OffsetY)

	return e.vignette
}

// pad extends a frame's circles to the renderer's fixed width with fully
// transparent ones. Render refuses a vector that is not exactly its dimension,
// and the renderer rejects a zero-opacity circle before any per-circle setup,
// so the padding costs one comparison each and paints nothing.
func pad(params []float64, slots int) ([]float64, error) {
	width := slots * app.ParamsPerCircle
	if len(params) > width {
		return nil, fmt.Errorf("%w: %d circles in a plan sized for %d",
			ErrPlanSlots, len(params)/app.ParamsPerCircle, slots)
	}

	if len(params) == width {
		return params, nil
	}

	padded := make([]float64, width)
	copy(padded, params)

	return padded, nil
}

func checkCanvas(sequence *Sequence, background *image.NRGBA) error {
	if background == nil {
		return ErrNoCanvas
	}

	bounds := background.Bounds()
	if bounds.Dx() != sequence.Width || bounds.Dy() != sequence.Height {
		return fmt.Errorf("%w: canvas is %dx%d but the sequence needs %dx%d",
			ErrCanvasSize, bounds.Dx(), bounds.Dy(), sequence.Width, sequence.Height)
	}

	return nil
}

func clone(img *image.NRGBA) *image.NRGBA {
	copied := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(copied, copied.Bounds(), img, img.Bounds().Min, draw.Src)

	return copied
}
