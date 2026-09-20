package anim

import (
	"math"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
)

// Every style starts by emitting the bare background, which the original wrote
// as Frame0.png in all four procedures.

// planStatic is SaveAnimationStatic (MainUnit.pas:2164). No tweening at all:
// each circle is added and the result saved, so frame N is the first N circles.
func planStatic(circles []fit.Circle, placer placer) []Frame {
	plan := &planner{}
	plan.emit(nil)

	for _, circle := range circles {
		plan.commit(placer.place(circle))
		plan.emit(nil)
	}

	return plan.frames
}

// planGrow is SaveAnimation (MainUnit.pas:2230). One circle at a time expands
// from radius one to its final radius, its opacity tracking how far along it
// is, and only the last step makes it permanent.
func planGrow(circles []fit.Circle, placer placer, halfLife int) []Frame {
	plan := &planner{}
	plan.emit(nil)

	gate := &halfLifeGate{halfLife: float64(halfLife)}

	for i, circle := range circles {
		final := placer.place(circle)
		growCircle(plan, gate, final)

		// The circle becomes permanent whether or not its frame survives the
		// gate: the original draws it outside the gated block and only the save
		// is conditional. The last circle is forced through, because a sequence
		// whose final frame is not the finished image is simply wrong, and the
		// original could end that way.
		plan.commit(final)

		if gate.allows() || i == len(circles)-1 {
			plan.emit(nil)
		}

		gate.advance()
	}

	return plan.frames
}

// growCircle emits the in-between frames of one circle. The circle is drawn at
// its current radius and then the radius advances, so the first frame shows it
// at radius one and the loop stops before the final radius, which planGrow
// commits.
func growCircle(plan *planner, gate *halfLifeGate, final fit.Circle) {
	step := final
	step.R = fit.MinCircleRadius

	for step.R < final.R {
		// Opacity rises with the radius, which is the original's
		// Round(FinalAlpha * (Radius / FinalRadius)). It is left as a float
		// here rather than quantized to the original's byte, so the committed
		// circle is bit-identical to the one a run recorded.
		step.Opacity = final.Opacity * (step.R / final.R)

		if gate.allows() {
			plan.emit(encode(step))
		}

		gate.advance()

		step.R = advanceRadius(step.R, final.R, growCoefficient)
	}
}

// planCascade is SaveAnimationAdvanced (MainUnit.pas:2426). Up to maxActive
// circles grow at once in a sliding window; each frame redraws the whole window
// over the committed background, and the head becomes permanent as it reaches
// full size.
func planCascade(circles []fit.Circle, placer placer, maxActive int) []Frame {
	plan := &planner{}
	plan.emit(nil)

	window := &cascadeWindow{maxActive: maxActive, placer: placer, circles: circles}

	for window.head < len(circles) {
		window.admit()
		plan.emit(window.step(plan))
	}

	return plan.frames
}

// cascadeWindow is the original's SlotList together with the radius budget that
// decides when another circle may join it.
type cascadeWindow struct {
	circles   []fit.Circle
	placer    placer
	radii     []float64
	head      int
	total     float64
	maxActive int
}

// admit decays the running radius total and lets at most one circle in per
// frame. The budget is measured in unscaled radii, as the original measures it,
// so a scaled run admits circles on the same schedule.
func (w *cascadeWindow) admit() {
	w.total *= cascadeDecay

	if len(w.radii) >= w.maxActive {
		return
	}

	if len(w.radii) > 0 && w.total >= cascadeRadiusBudget {
		return
	}

	next := w.head + len(w.radii)
	if next >= len(w.circles) {
		return
	}

	w.radii = append(w.radii, fit.MinCircleRadius)
	w.total += w.circles[next].R
}

// step draws every circle in the window and reports them as one frame's active
// set, committing any that have reached full size.
//
// Retirement is checked only at the head, and the original checks it before
// drawing anything behind it, so a commit carries the head alone. Nothing
// half-grown is ever made permanent.
func (w *cascadeWindow) step(plan *planner) []float64 {
	active := make([]float64, 0, len(w.radii)*app.ParamsPerCircle)

	slot := 0
	for slot < len(w.radii) {
		final := w.placer.place(w.circles[w.head+slot])

		if slot == 0 && w.radii[slot] >= final.R {
			plan.commit(final)

			w.radii = w.radii[1:]
			w.head++

			continue
		}

		drawn := final
		drawn.R = w.radii[slot]
		drawn.Opacity = final.Opacity * (drawn.R / final.R)
		active = append(active, encode(drawn)...)

		if w.radii[slot] < final.R {
			w.radii[slot] = advanceRadius(w.radii[slot], final.R, cascadeCoefficient)
		}

		w.radii[slot] = min(w.radii[slot], final.R)
		slot++
	}

	return active
}

// planInflate is SaveAnimationBlow (MainUnit.pas:2606). Every circle expands
// together, so each frame redraws the whole arrangement at a fraction of its
// size and nothing is ever committed.
func planInflate(circles []fit.Circle, placer placer, frames int) []Frame {
	plan := &planner{}
	plan.emit(nil)

	iterations := frames
	if iterations == 0 {
		iterations = int(math.Floor(inflateFrameFactor * largestRadius(circles)))
	}

	// Two frames is the floor: the ratio divides by iterations-1, and an
	// arrangement of small circles can derive fewer than that.
	iterations = max(iterations, 2)

	for frame := 1; frame < iterations; frame++ {
		ratio := float64(frame) / float64(iterations-1)
		plan.emit(inflateFrame(circles, placer, ratio))
	}

	return plan.frames
}

// inflateFrame scales every radius linearly with the ratio while opacity
// follows its square root, which is what makes the arrangement fade in faster
// than it grows.
func inflateFrame(circles []fit.Circle, placer placer, ratio float64) []float64 {
	active := make([]float64, 0, len(circles)*app.ParamsPerCircle)
	fade := math.Sqrt(ratio)

	for _, circle := range circles {
		drawn := placer.place(circle)
		drawn.R = circle.R * placer.scale * ratio
		drawn.Opacity = circle.Opacity * fade
		active = append(active, encode(drawn)...)
	}

	return active
}

func largestRadius(circles []fit.Circle) float64 {
	largest := 0.0
	for _, circle := range circles {
		largest = max(largest, circle.R)
	}

	return largest
}

// advanceRadius is the ease-out both growing styles share, transcribed from the
// three assignments the original makes inside `with Circle.GeometricShape do`:
//
//	Radius := Radius + (FinalRadius - Radius) * coefficient
//	Radius := Radius * 1.1
//	Radius := Radius + 1
//
// Only the coefficient distinguishes the two styles. The constant floor is what
// guarantees progress: the ease-out term alone shrinks with the remaining
// distance, so a radius of one would otherwise crawl.
func advanceRadius(radius, final, coefficient float64) float64 {
	radius += (final - radius) * coefficient
	radius *= growthGain

	return radius + growthFloor
}

// halfLifeGate is the original's progressive frame dropping. A position
// accumulates 1 - halfLife/(halfLife + index) per candidate frame; while it is
// above one the frame is skipped and one is subtracted. Because the index rises
// for the whole run and never resets per circle, the skipped share grows and
// the animation speeds up as it goes.
type halfLifeGate struct {
	halfLife float64
	position float64
	index    int
}

func (g *halfLifeGate) allows() bool {
	return g.halfLife <= 0 || g.position <= 1
}

func (g *halfLifeGate) advance() {
	g.index++

	if g.halfLife <= 0 {
		return
	}

	if g.position > 1 {
		g.position--
	}

	g.position += 1 - g.halfLife/(g.halfLife+float64(g.index))
}
