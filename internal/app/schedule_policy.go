package app

import (
	"fmt"
	"sort"
)

// MaxScheduleConditionCircles bounds the circle list on a condition. It is far
// past the six polish points the 512-circle campaign names and exists only so a
// hand-edited document cannot carry an unbounded list.
const MaxScheduleConditionCircles = 256

// ScheduleCondition is the optional `when` object on a step: the part of a
// campaign that cannot be decided when the document is written, because it
// depends on what the stages before it measured.
//
// Two rules were hardcoded constants in the Python orchestrator this phase
// replaces, and both are policy rather than mechanism:
//
//   - Circles ran a polish only at listed circle counts.
//   - MinGain with AbortAfterBarren abandoned polishing once it stopped paying.
//     Over the 512-circle baseline, polishing spent 80% of the wall clock for
//     10% of the gain and returned exactly 0.00 past roughly 300 circles. The
//     threshold is authored rather than built in precisely because its right
//     value moves: the polish acceptance gate has since been fixed, and a rule
//     compiled in as 1.0 would have been wrong the day after.
//
// A condition is valid on a polish step only. Skipping a polish leaves the
// canvas exactly as the plan predicted, so every later stage keeps its planned
// circle count; skipping an extend would move every circle count after it and
// make the realized run disagree with its own plan.
type ScheduleCondition struct {
	// Circles restricts the step to stages whose canvas holds one of these
	// counts. An empty list places no restriction.
	Circles []int `json:"circles,omitempty"`

	// MinGain is the cost improvement a stage must deliver to count as
	// productive: the previous completed stage's best cost minus this stage's.
	MinGain *float64 `json:"minGain,omitempty"`

	// AbortAfterBarren is how many consecutive stages of this kind may gain
	// less than MinGain before the step is abandoned for the rest of the
	// campaign. The two fields are meaningless apart and must be given together.
	AbortAfterBarren *int `json:"abortAfterBarren,omitempty"`
}

func (c *ScheduleCondition) validate(field func(string) string) error {
	if c == nil {
		return nil
	}

	if len(c.Circles) > MaxScheduleConditionCircles {
		return invalid(field("when.circles"), fmt.Sprintf("must list at most %d circle counts", MaxScheduleConditionCircles))
	}

	seen := make(map[int]bool, len(c.Circles))
	for _, circles := range c.Circles {
		if circles < 1 || circles > MaxCircles {
			return invalid(field("when.circles"), fmt.Sprintf("must hold counts between 1 and %d", MaxCircles))
		}

		if seen[circles] {
			return invalid(field("when.circles"), fmt.Sprintf("lists %d twice", circles))
		}

		seen[circles] = true
	}

	if (c.MinGain == nil) != (c.AbortAfterBarren == nil) {
		return invalid(field("when.minGain"), "and when.abortAfterBarren must be given together; neither means anything alone")
	}

	if c.MinGain != nil && *c.MinGain < 0 {
		return invalid(field("when.minGain"), "cannot be negative")
	}

	if c.AbortAfterBarren != nil && (*c.AbortAfterBarren < 1 || *c.AbortAfterBarren > MaxScheduleStages) {
		return invalid(field("when.abortAfterBarren"), fmt.Sprintf("must be between 1 and %d", MaxScheduleStages))
	}

	return nil
}

// ScheduleOutcomeState is what a realized stage came to. It is deliberately
// narrower than the store's stage states: policy reads outcomes, not lifecycle,
// and the only distinctions that change a decision are "it ran and measured
// something", "policy already declined it", and "neither yet".
type ScheduleOutcomeState string

const (
	ScheduleOutcomePending   ScheduleOutcomeState = "pending"
	ScheduleOutcomeCompleted ScheduleOutcomeState = "completed"
	ScheduleOutcomeSkipped   ScheduleOutcomeState = "skipped"
)

// ScheduleStageOutcome is one measured stage, and the only runtime input policy
// is allowed to read. Keeping it a plain value is what makes the decision a
// pure function that a table can drive, and what keeps the store's record type
// out of the application layer.
type ScheduleStageOutcome struct {
	Index int
	Kind  ScheduleStageKind
	State ScheduleOutcomeState

	// BestCost is the cost the canvas held when the stage settled, and is
	// meaningful only when CostMeasured is set. Zero is a legitimate value — a
	// perfect fit costs exactly nothing — so it must never be read as "no
	// measurement".
	BestCost float64

	// CostMeasured says whether BestCost is a number the stage actually
	// produced. It defaults to false so an outcome built without one cannot
	// pass an accidental zero off as a measurement.
	CostMeasured bool
}

// ScheduleStageVerdict is the decision for one planned stage.
type ScheduleStageVerdict struct {
	// Run reports whether the stage should be started at all.
	Run bool

	// Reason explains a declined stage in the operator's terms. It is recorded
	// with the skipped stage so the campaign's own records say why a planned
	// stage never ran.
	Reason string
}

// EvaluateScheduleStage decides whether one planned stage runs, given the
// outcomes of the stages before it.
//
// It is a pure function of (plan, index, outcomes) on purpose. Nothing is
// accumulated across calls and no counter is stored anywhere: the barren streak
// is recomputed from the recorded outcomes every time it is asked for, so it
// cannot drift from the record of what actually ran. Feed it the same records
// and it returns the same verdict, which is also what makes an already-decided
// stage safe to re-evaluate after a restart.
//
// Outcomes at or after index are ignored, so a caller may hand over everything
// it has without slicing.
func EvaluateScheduleStage(plan []ScheduleStage, index int, outcomes []ScheduleStageOutcome) ScheduleStageVerdict {
	if index < 0 || index >= len(plan) {
		return ScheduleStageVerdict{Run: true}
	}

	stage := plan[index]

	when := stage.When
	if when == nil {
		return ScheduleStageVerdict{Run: true}
	}

	if len(when.Circles) > 0 && !containsInt(when.Circles, stage.Circles) {
		return ScheduleStageVerdict{Reason: fmt.Sprintf(
			"%s is scheduled only at %v circles, and this stage sits at %d",
			stage.Kind, when.Circles, stage.Circles)}
	}

	if when.AbortAfterBarren != nil && when.MinGain != nil {
		limit := *when.AbortAfterBarren
		if streak := barrenStreak(outcomes, stage.Kind, index, *when.MinGain); streak >= limit {
			return ScheduleStageVerdict{Reason: fmt.Sprintf(
				"the last %d %s stages each gained less than %g cost units",
				streak, stage.Kind, *when.MinGain)}
		}
	}

	return ScheduleStageVerdict{Run: true}
}

// barrenStreak counts how many of the most recent completed stages of a kind
// gained less than minGain, stopping at the first one that paid for itself.
//
// A stage's gain is measured against the completed stage before it, whatever
// kind that was, because that is the cost the canvas actually held when the
// stage started. A gain that cannot be measured — no completed predecessor, or
// either side missing a cost — ends the streak rather than extending it: an
// unknown is not evidence that polishing has stopped paying. Absence is read
// from CostMeasured and never from the number: a stage that reached a perfect
// fit records a best cost of exactly zero, and its zero gain is the strongest
// evidence there is that further polishing is barren.
func barrenStreak(outcomes []ScheduleStageOutcome, kind ScheduleStageKind, before int, minGain float64) int {
	completed := make([]ScheduleStageOutcome, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Index < before && outcome.State == ScheduleOutcomeCompleted {
			completed = append(completed, outcome)
		}
	}

	sort.Slice(completed, func(i, j int) bool { return completed[i].Index < completed[j].Index })

	streak := 0

	for i := len(completed) - 1; i >= 0; i-- {
		if completed[i].Kind != kind {
			continue
		}

		if i == 0 || !completed[i].CostMeasured || !completed[i-1].CostMeasured {
			break
		}

		if completed[i-1].BestCost-completed[i].BestCost >= minGain {
			break
		}

		streak++
	}

	return streak
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
