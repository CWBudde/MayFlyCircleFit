package app

import (
	"fmt"
	"time"
)

// The finish projection answers "when", and until now that was the only
// question the campaign surface asked. The measured growth campaign showed
// there are two, and that the objective flips with whichever resource is
// scarce: while the circle budget is open, gain per hour decides, and against a
// fixed circle ceiling, gain per circle does. The same settings win one and
// lose the other — iters 500 returned 1.84x the gain per circle of iters 50
// while returning roughly half the gain per hour — so a surface that reports
// only wall clock hands a campaign against a ceiling the wrong answer. This
// file derives the second answer from the measurements the first one already
// reads, and from nothing else.

// costTrailingWindowDivisor splits the measured legs into a trailing window:
// the last legs/costTrailingWindowDivisor of them, rounded up, never fewer than
// one.
//
// Two — the trailing half — is the coarsest window that still discards the
// early campaign, and coarse is what is wanted here. A window of one leg would
// make every forward projection the extrapolation of a single measurement,
// which is the thing MinProjectionSamples exists to refuse; a window of
// everything is the campaign average, which is what the trailing window exists
// to avoid. Half keeps the sample count growing with the campaign while the
// window keeps sliding away from its opening stages.
const costTrailingWindowDivisor = 2

// costDecayNoteThreshold is how far the trailing per-circle rate may fall below
// the whole-campaign rate before the projection says so.
//
// Some decay is expected and unremarkable: a canvas that already fits well has
// less left to win, and a threshold set tight enough to fire on that would fire
// on every campaign and mean nothing. A quarter is the point at which the
// trailing rate stops being a slower version of the campaign average and starts
// being a different regime, so that even the trailing projection reads
// optimistic. The measured campaign crossed it: by its third thousand circles
// the trailing rate sat 28% under its own average, 0.017697 against 0.024647
// cost per circle, which is exactly the state an operator planning a fourth
// thousand needs told about.
const costDecayNoteThreshold = 0.25

// ScheduleCostProjection is what the measured stages say about cost, in the two
// rates a campaign can be short of: cost removed per circle added, and cost
// removed per hour spent.
//
// It is derived the way the finish projection is, from measurement alone, with
// no model of the optimizer behind it. Where it deliberately differs is in
// which measurements it extrapolates: a stage's wall clock does not drift
// systematically, so the finish projection averages every sample of a kind;
// cost gain does drift, downward, so the forward projections here read the
// trailing window only. See RecentGainPerCircle.
type ScheduleCostProjection struct {
	// Samples counts the completed, cost-measured stages the projection read,
	// the same unit ScheduleKindProjection.Samples uses. Consecutive samples
	// pair into legs, so there are Samples-1 of those: the first sample is a
	// starting point, not a gain.
	Samples int

	// Gain is the cost removed across the whole measured span, AddedCircles the
	// circles it took, and Elapsed the wall clock. Elapsed sums the legs only —
	// the stage that produced the first sample is excluded, because the time it
	// spent bought the starting point rather than any part of the gain.
	Gain         float64
	AddedCircles int
	Elapsed      time.Duration

	// GainPerCircle and GainPerHour are Gain over those two denominators, zero
	// when a denominator is zero or the projection is gated. They describe what
	// the campaign has done; they are deliberately not what it is projected
	// forward with.
	GainPerCircle float64
	GainPerHour   float64

	// RecentGainPerCircle and RecentGainPerHour are the same two rates over the
	// trailing window alone, and every forward projection below uses them. The
	// measured campaign is the reason: 1000 -> 2000 circles removed 31.597 cost
	// units (0.031597 per circle) and 2000 -> 3000 removed 17.697 (0.017697),
	// so a rate averaged over both over-predicts the next leg by 1.79x.
	// Extrapolating the campaign average would be a model of the optimizer, and
	// a wrong one.
	//
	// RecentLegs is how many legs the window held, and RecentCircles and
	// RecentElapsed the two denominators those rates were divided by. They are
	// published because a surface that prints a rate has to be able to say what
	// it was measured over, and "the last two legs" tells an operator far less
	// than "the last 1000 circles" or "the last 56m": a leg is an internal unit
	// of this projection, while circles and wall clock are what the campaign was
	// actually spending. Labelling a trailing rate with the whole-campaign
	// AddedCircles or Elapsed would be the alternative, and it would be wrong.
	RecentGainPerCircle float64
	RecentGainPerHour   float64
	RecentLegs          int
	RecentCircles       int
	RecentElapsed       time.Duration

	// LatestCircles and LatestCost say where the campaign stands: the last
	// measured stage's circle count, and the cost it reached. Both projections
	// below start from here rather than from a fitted curve, so a campaign that
	// is reported as being at a cost is reported at one it actually measured.
	LatestCircles int
	LatestCost    float64

	// RemainingCircles is the final planned stage's circle count less
	// LatestCircles, and CostAtPlanEnd where the trailing per-circle rate puts
	// the campaign once they are spent, floored at zero.
	//
	// HasCircleCeiling is false when there is no per-circle rate to spend or no
	// circles left to spend it on, and CostAtPlanEnd is then zero rather than a
	// restatement of LatestCost: a finished plan has a measurement, not an
	// estimate, and dressing the one up as the other is what the presence flag
	// is here to prevent.
	RemainingCircles int
	CostAtPlanEnd    float64
	HasCircleCeiling bool

	// RemainingElapsed is the finish projection's own estimate, passed in
	// rather than recomputed so the two projections cannot disagree about the
	// plan, and CostAtFinish where the trailing per-hour rate puts the campaign
	// once that time is spent, floored at zero. HasTimeBudget carries the same
	// discipline as HasCircleCeiling; in particular an incomplete finish
	// projection leaves RemainingElapsed zero, and the time answer is then
	// absent while the circle answer still stands.
	RemainingElapsed time.Duration
	CostAtFinish     float64
	HasTimeBudget    bool

	// Note explains why the projection is absent, or reports that the trailing
	// rate has fallen far enough below the campaign average that even it reads
	// optimistic. See costDecayNoteThreshold.
	Note string
}

// Projected reports whether this estimate rests on enough measurement to carry
// rates at all.
func (p ScheduleCostProjection) Projected() bool {
	return p.Samples >= MinProjectionSamples
}

// ProjectScheduleCost estimates where a campaign's cost lands, both against the
// plan's circle ceiling and against the finish projection's clock.
//
// It is a pure function of (plan, timings, remaining): it reads no clock,
// accumulates nothing, and returns the same value for the same inputs, in
// whatever order the timings arrive. Timings outside the plan are ignored, as
// they are in ProjectScheduleFinish, and a stage whose CostMeasured is false
// contributes nothing — a stage that reached cost zero is a measurement, one
// that reported no cost is not, and the flag is the only thing that tells them
// apart.
//
// remaining is the caller's ScheduleProjection.Remaining. Zero is the honest
// value when the finish projection could not be completed, and it switches the
// time answer off instead of inventing one.
//
// MinProjectionSamples is reused as the gate, in its own unit: two completed
// stages are the fewest that measure any gain at all, since one stage measures
// only where the campaign started. Below that the rates stay zero and Note says
// so, exactly as finishKindProjection does for a kind it cannot project. What
// guards the estimate above the gate is the trailing window and the decay note,
// not a larger threshold — a campaign that has run two stages still has to be
// told something, and the alternative is to say nothing until it has run three.
func ProjectScheduleCost(
	plan []ScheduleStage, timings []ScheduleStageTiming, remaining time.Duration,
) ScheduleCostProjection {
	measured := costMilestones(plan, timings)

	projection := ScheduleCostProjection{Samples: len(measured), RemainingElapsed: remaining}
	if len(measured) > 0 {
		latest := measured[len(measured)-1]
		projection.LatestCircles = latest.circles
		projection.LatestCost = latest.bestCost
	}

	if len(plan) > 0 {
		// A plan whose end is already behind the campaign leaves nothing to
		// project against, so the shortfall is clamped rather than reported as
		// negative work.
		projection.RemainingCircles = max(plan[len(plan)-1].Circles-projection.LatestCircles, 0)
	}

	if !projection.Projected() {
		projection.Note = fmt.Sprintf(
			"insufficient data: %d completed cost-measured stage(s), %d needed before cost is projected",
			projection.Samples, MinProjectionSamples)

		return projection
	}

	first, latest := measured[0], measured[len(measured)-1]
	projection.Gain = first.bestCost - latest.bestCost
	projection.AddedCircles = latest.circles - first.circles
	projection.Elapsed = legElapsed(measured)
	projection.GainPerCircle = costPerCircle(projection.Gain, projection.AddedCircles)
	projection.GainPerHour = costPerHour(projection.Gain, projection.Elapsed)

	legs := len(measured) - 1
	projection.RecentLegs = (legs + costTrailingWindowDivisor - 1) / costTrailingWindowDivisor

	window := measured[len(measured)-1-projection.RecentLegs:]
	recentGain := window[0].bestCost - latest.bestCost
	recentCircles := latest.circles - window[0].circles
	recentElapsed := legElapsed(window)
	projection.RecentCircles = recentCircles
	projection.RecentElapsed = recentElapsed
	projection.RecentGainPerCircle = costPerCircle(recentGain, recentCircles)
	projection.RecentGainPerHour = costPerHour(recentGain, recentElapsed)

	if recentCircles > 0 && projection.RemainingCircles > 0 {
		projection.HasCircleCeiling = true
		projection.CostAtPlanEnd = floorCost(
			projection.LatestCost - projection.RecentGainPerCircle*float64(projection.RemainingCircles))
	}

	if recentElapsed > 0 && remaining > 0 {
		projection.HasTimeBudget = true
		projection.CostAtFinish = floorCost(projection.LatestCost - projection.RecentGainPerHour*remaining.Hours())
	}

	projection.Note = costDecayNote(projection)

	return projection
}

// costMilestone is one measured point on the cost curve: what the canvas held
// once a stage completed, and what that stage spent reaching it.
type costMilestone struct {
	circles  int
	bestCost float64
	elapsed  time.Duration
}

// costMilestones collects the completed, cost-measured stages in plan order.
//
// The order comes from the plan and not from the timings slice, because a
// caller is free to hand the timings over in any order and a leg is only a gain
// if it pairs the stages the campaign actually ran in sequence.
func costMilestones(plan []ScheduleStage, timings []ScheduleStageTiming) []costMilestone {
	byIndex := make(map[int]ScheduleStageTiming, len(timings))

	for _, timing := range timings {
		if timing.Index < 0 || timing.Index >= len(plan) {
			continue
		}
		// Only a completed stage measured anything, and only a cost-measured
		// one measured a cost: a skipped stage moved nothing and a failed one
		// stopped somewhere nobody recorded.
		if timing.State != ScheduleOutcomeCompleted || !timing.CostMeasured {
			continue
		}

		byIndex[timing.Index] = timing
	}

	milestones := make([]costMilestone, 0, len(byIndex))

	for _, stage := range plan {
		timing, ok := byIndex[stage.Index]
		if !ok {
			continue
		}

		milestones = append(milestones, costMilestone{
			circles:  timing.Circles,
			bestCost: timing.BestCost,
			elapsed:  timing.Elapsed,
		})
	}

	return milestones
}

// legElapsed sums the wall clock of every leg in a run of milestones, which is
// every milestone but the first: the opening one is a starting point, and the
// time that produced it bought no part of the gain measured after it.
func legElapsed(milestones []costMilestone) time.Duration {
	var elapsed time.Duration
	for _, milestone := range milestones[1:] {
		elapsed += milestone.elapsed
	}

	return elapsed
}

// costPerCircle and costPerHour divide a gain by a denominator that may not
// have been measured. A polish-only span adds no circles and a span of
// instantaneous stages spends no time; both are legitimate, and both make the
// corresponding rate absent rather than infinite.
func costPerCircle(gain float64, circles int) float64 {
	if circles <= 0 {
		return 0
	}

	return gain / float64(circles)
}

func costPerHour(gain float64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}

	return gain / elapsed.Hours()
}

// floorCost clamps a projected cost at zero. A rate carried far enough forward
// eventually predicts a negative cost, which is not a fit anyone can render; it
// is the rate running off the end of the measurement that produced it.
func floorCost(cost float64) float64 {
	if cost < 0 {
		return 0
	}

	return cost
}

// costDecayNote qualifies a projection whose trailing per-circle rate has
// fallen well below the campaign average, naming both rates so the reader can
// see the size of the gap rather than take the verdict on trust.
func costDecayNote(projection ScheduleCostProjection) string {
	if projection.GainPerCircle <= 0 {
		return ""
	}

	if projection.RecentGainPerCircle >= projection.GainPerCircle*(1-costDecayNoteThreshold) {
		return ""
	}

	return fmt.Sprintf(
		"the per-circle return is decaying: %.6f cost/circle over the last %d leg(s) "+
			"against %.6f over the whole campaign, so even this projection is optimistic",
		projection.RecentGainPerCircle, projection.RecentLegs, projection.GainPerCircle)
}
