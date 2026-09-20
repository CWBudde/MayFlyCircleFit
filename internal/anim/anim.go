// Package anim replays a finished circle arrangement as a sequence of frames.
//
// It is a port of the four animation exporters in the Pascal predecessor of
// this project, CircledPictureDrawing's MainUnit.pas (SaveAnimationStatic,
// SaveAnimation, SaveAnimationAdvanced and SaveAnimationBlow, dispatched from
// MiSaveAnimationClick). The property that makes the port small is that the
// original animates nothing during the search: every one of those procedures
// is a post-hoc replay of the finished FCircles array, invoked from a menu once
// the run is over. Nothing here needs optimizer history, which is fortunate,
// because this project keeps none -- checkpoints are overwritten in place and
// store.TraceEntry.Params is never populated.
//
// Planning is separated from rendering on purpose. Plan is a pure function from
// a circle list to a list of frames, so the growth curves, the frame counts and
// the commit points are all testable without rendering a pixel; Render then
// executes a plan against the real renderer, which keeps frames byte-identical
// to the ones a run writes as best.png.
package anim

import (
	"errors"
	"fmt"
	"math"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
)

// Style names one of the original's four animation modes. The Go names are
// descriptive; each constant's comment gives the Pascal name it was chosen in
// the dialog by, so a reader can find the procedure it came from.
type Style string

const (
	// StyleStatic is the original's "Static": one frame per circle, no tweening.
	// Frame N is simply the first N circles.
	StyleStatic Style = "static"
	// StyleGrow is the original's "Animated": each circle in turn grows from
	// radius one to its final radius on an ease-out curve, fading in as it goes.
	StyleGrow Style = "grow"
	// StyleCascade is the original's "Advanced": up to MaxActive circles grow
	// concurrently in a sliding window, the head retiring as it reaches full
	// size.
	StyleCascade Style = "cascade"
	// StyleInflate is the original's "Blow All": every circle expands together
	// from nothing to the finished image.
	StyleInflate Style = "inflate"
)

// Growth constants, transcribed from MainUnit.pas.
const (
	// growthGain and growthFloor are the two terms that follow the ease-out
	// step in both growing styles: Radius := Radius * 1.1 and then Radius :=
	// Radius + 1. The floor is what lets a radius of one escape, since the
	// ease-out term alone is proportional to the remaining distance.
	growthGain  = 1.1
	growthFloor = 1.0
	// growCoefficient and cascadeCoefficient are the ease-out rates of
	// SaveAnimation (0.3333, MainUnit.pas:2335) and SaveAnimationAdvanced (0.2,
	// MainUnit.pas:2436). The original writes 0.3333 rather than a third, so
	// this does too.
	growCoefficient    = 0.3333
	cascadeCoefficient = 0.2
	// cascadeDecay and cascadeRadiusBudget gate how fast SaveAnimationAdvanced
	// admits new circles: a running radius total decays by 0.7 each frame and no
	// new circle joins while it is at or above 64.
	cascadeDecay        = 0.7
	cascadeRadiusBudget = 64.0
	// inflateFrameFactor derives SaveAnimationBlow's frame count from the
	// largest radius: Iterations := FixedFloor(0.25 * MaxRadius).
	inflateFrameFactor = 0.25
	// vignetteOutroFrames is the length of the original's closing sequence.
	vignetteOutroFrames = 41
	// cascadeSlots is the concurrency SaveAnimationAdvanced hardcodes.
	cascadeSlots = 32
	// pascalHalfLife is the dialog's default for the frame-dropping control.
	pascalHalfLife = 500
)

// Errors reported by Plan. They are values rather than strings built at the
// return so callers can test for them.
var (
	ErrUnknownStyle = errors.New("unknown animation style")
	ErrNoCircles    = errors.New("no circles to animate")
	ErrScale        = errors.New("scale must be positive and finite")
	ErrMargin       = errors.New("margin must be zero or a positive finite fraction")
	ErrMaxActive    = errors.New("max active must be at least one")
	ErrNegative     = errors.New("value cannot be negative")
	ErrCanvasSize   = errors.New("scaled canvas is not a usable size")
	ErrReverse      = errors.New("reverse only applies to the inflate style")
	ErrCircle       = errors.New("circle cannot be animated")
	ErrPlanSlots    = errors.New("frame draws more circles than the plan was sized for")
	ErrNoCanvas     = errors.New("no background canvas")
)

// Styles lists every style in dialog order, for flag help and validation.
func Styles() []Style {
	return []Style{StyleStatic, StyleGrow, StyleCascade, StyleInflate}
}

// DefaultOptions is the starting point a caller adjusts. It deliberately does
// not reproduce the Pascal dialog's defaults: scale is 1 rather than 2 and
// there is no margin or outro, because those three exist only to give the
// original's closing vignette a border to darken, and a frame sequence destined
// for a video wants neither. PascalDefaults restores them.
func DefaultOptions() Options {
	return Options{
		Style:     StyleStatic,
		Scale:     1,
		HalfLife:  pascalHalfLife,
		MaxActive: cascadeSlots,
	}
}

// PascalDefaults returns the settings TFmSaveAnimation opens with, for
// reproducing the original's output rather than a clean sequence.
func PascalDefaults(style Style) Options {
	opts := DefaultOptions()
	opts.Style = style
	opts.Scale = 2
	opts.Margin = 0.5
	opts.Outro = vignetteOutroFrames

	return opts
}

// Options configures a plan. The zero value is not usable; start from
// DefaultOptions.
type Options struct {
	Style Style
	// Scale multiplies every coordinate and radius. The original carried it as
	// a percentage in a spin edit and applied it as FixedMul(radius, IntScale);
	// circle parameters here are already in pixels, so it is a plain multiply.
	Scale float64
	// Margin is empty space added on each side, as a fraction of the scaled
	// content size. The original hardcoded the equivalent of 0.5 -- a canvas
	// twice the scaled reference with the picture in the middle quarter -- for
	// the sole benefit of Outro. Zero leaves the frames the size of the scaled
	// reference.
	Margin float64
	// HalfLife drives StyleGrow's frame dropping. The share of frames skipped
	// rises as the run goes on, so the animation accelerates. Zero disables it.
	HalfLife int
	// MaxActive is how many circles StyleCascade grows concurrently. The
	// original hardcoded 32 while persisting an unused dialog control for it.
	MaxActive int
	// Frames overrides StyleInflate's frame count. Zero derives it from the
	// largest radius, as the original does.
	Frames int
	// Outro appends that many closing vignette frames, which darken the margin
	// and draw a brightening border. The original always emitted 41 of them for
	// every style but Static. Without a Margin there is no border to darken and
	// only the inner frame lines appear.
	Outro int
	// Reverse plays StyleInflate backwards, so the image deflates to nothing.
	// It revives the original's CbInverted checkbox, which was wired to a
	// parameter the call site never passed. The original also skipped its outro
	// when inverted, and so does this.
	Reverse bool
}

// Frame is one emitted image, described as what changes rather than as pixels.
//
// Commit is composited into the background before the frame is rendered and
// stays there for every later frame; Active is drawn over that background for
// this frame only. That split is the original's BackDraw and Drawing bitmaps:
// a circle still growing is redrawn from scratch each frame, and only reaching
// its final radius makes it permanent.
type Frame struct {
	Commit   []float64
	Active   []float64
	Vignette bool
}

// Sequence is a planned animation together with the canvas it is planned for.
type Sequence struct {
	Frames []Frame
	// Width and Height are the emitted frame size, which Scale and Margin can
	// make larger than the reference.
	Width, Height int
	// OffsetX and OffsetY locate the scaled content inside that frame. They are
	// zero without a Margin, and are what the vignette darkens around.
	OffsetX, OffsetY int
	// Slots is the largest number of circles any single frame draws, which is
	// what the renderer executing this plan has to be sized for.
	Slots int
}

// Plan converts a finished arrangement into the frames that replay it. Circles
// are in reference-image coordinates, back to front, exactly as they sit in a
// parameter vector.
func Plan(circles []fit.Circle, width, height int, opts Options) (*Sequence, error) {
	err := opts.validate()
	if err != nil {
		return nil, err
	}

	err = checkCircles(circles)
	if err != nil {
		return nil, err
	}

	sequence, err := newSequence(width, height, opts)
	if err != nil {
		return nil, err
	}

	frames, err := planFrames(circles, *sequence, opts)
	if err != nil {
		return nil, err
	}

	sequence.Frames = frames

	if opts.Reverse && opts.Style == StyleInflate {
		reverse(sequence.Frames)
	} else if opts.Outro > 0 {
		sequence.Frames = appendVignette(sequence.Frames, opts.Outro)
	}

	sequence.Slots = slotsFor(sequence.Frames)

	return sequence, nil
}

func planFrames(circles []fit.Circle, seq Sequence, opts Options) ([]Frame, error) {
	placer := placer{scale: opts.Scale, offsetX: float64(seq.OffsetX), offsetY: float64(seq.OffsetY)}

	switch opts.Style {
	case StyleStatic:
		return planStatic(circles, placer), nil
	case StyleGrow:
		return planGrow(circles, placer, opts.HalfLife), nil
	case StyleCascade:
		return planCascade(circles, placer, opts.MaxActive), nil
	case StyleInflate:
		return planInflate(circles, placer, opts.Frames), nil
	}

	return nil, fmt.Errorf("%w: %q", ErrUnknownStyle, opts.Style)
}

// newSequence resolves the frame geometry. The original computed a canvas of
// 2 * scale * reference and placed the drawing at a quarter of it, which is
// this with Margin 0.5.
func newSequence(width, height int, opts Options) (*Sequence, error) {
	contentWidth := float64(width) * opts.Scale
	contentHeight := float64(height) * opts.Scale
	offsetX := int(math.Round(contentWidth * opts.Margin))
	offsetY := int(math.Round(contentHeight * opts.Margin))

	sequence := &Sequence{
		Width:   int(math.Round(contentWidth)) + 2*offsetX,
		Height:  int(math.Round(contentHeight)) + 2*offsetY,
		OffsetX: offsetX,
		OffsetY: offsetY,
	}

	err := app.ValidateImageDimensions(sequence.Width, sequence.Height)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCanvasSize, err)
	}

	return sequence, nil
}

// checkCircles rejects what the growth curves cannot express. Every style
// divides an opacity by a final radius, so a radius that is zero or not finite
// would silently produce transparent or absent circles rather than an error.
func checkCircles(circles []fit.Circle) error {
	if len(circles) == 0 {
		return ErrNoCircles
	}

	for i, circle := range circles {
		if circle.R <= 0 || math.IsInf(circle.R, 0) || math.IsNaN(circle.R) {
			return fmt.Errorf("%w: circle %d has radius %v", ErrCircle, i, circle.R)
		}
	}

	return nil
}

func (o Options) validate() error {
	err := o.validateGeometry()
	if err != nil {
		return err
	}

	return o.validateStyleSettings()
}

func (o Options) validateGeometry() error {
	if o.Scale <= 0 || math.IsInf(o.Scale, 0) || math.IsNaN(o.Scale) {
		return ErrScale
	}

	if o.Margin < 0 || math.IsInf(o.Margin, 0) || math.IsNaN(o.Margin) {
		return ErrMargin
	}

	for name, value := range map[string]int{"halfLife": o.HalfLife, "frames": o.Frames, "outro": o.Outro} {
		if value < 0 {
			return fmt.Errorf("%s: %w", name, ErrNegative)
		}
	}

	return nil
}

func (o Options) validateStyleSettings() error {
	if o.Style == StyleCascade && o.MaxActive < 1 {
		return ErrMaxActive
	}

	if o.Reverse && o.Style != StyleInflate {
		return ErrReverse
	}

	return nil
}

// placer maps a circle from reference coordinates into frame coordinates.
type placer struct {
	scale            float64
	offsetX, offsetY float64
}

func (p placer) place(circle fit.Circle) fit.Circle {
	circle.X = circle.X*p.scale + p.offsetX
	circle.Y = circle.Y*p.scale + p.offsetY
	circle.R *= p.scale

	return circle
}

// planner accumulates frames while carrying commits that no frame showed.
//
// A commit can outlive its frame: StyleGrow always makes a circle permanent
// once it reaches full size, but the frame that would have shown it is subject
// to the half-life gate and may be dropped. The circle is still in the image,
// so the commit rides along to whichever frame is emitted next.
type planner struct {
	frames  []Frame
	pending []float64
}

func (p *planner) commit(circle fit.Circle) {
	p.pending = append(p.pending, encode(circle)...)
}

func (p *planner) emit(active []float64) {
	p.frames = append(p.frames, Frame{Commit: p.pending, Active: active})
	p.pending = nil
}

func encode(circle fit.Circle) []float64 {
	return []float64{circle.X, circle.Y, circle.R, circle.CR, circle.CG, circle.CB, circle.Opacity}
}

func slotsFor(frames []Frame) int {
	slots := 0
	for _, frame := range frames {
		slots = max(slots, len(frame.Commit)/app.ParamsPerCircle, len(frame.Active)/app.ParamsPerCircle)
	}

	return slots
}

func reverse(frames []Frame) {
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
}

func appendVignette(frames []Frame, count int) []Frame {
	for range count {
		frames = append(frames, Frame{Vignette: true})
	}

	return frames
}
