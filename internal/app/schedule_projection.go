package app

import (
	"fmt"
	"time"
)

// MinProjectionSamples is how many completed stages of a kind are needed before
// that kind is projected at all.
//
// Two is the smallest number that is not a single sample. One stage carries the
// whole of whatever was happening on the machine while it ran — a cold page
// cache, another job on the same cores — and multiplying that by sixty
// remaining stages produces a confident figure with nothing behind it. Below
// this threshold the projection reports insufficient data instead.
const MinProjectionSamples = 2

// ScheduleStageTiming is one measured stage's wall clock, and the only runtime
// input the projection reads.
//
// It is a plain value for the same reason ScheduleStageOutcome is: the store
// owns the stage records, the application layer owns the arithmetic, and
// keeping the record type out of here is what lets a table drive the tests.
type ScheduleStageTiming struct {
	Index   int
	Kind    ScheduleStageKind
	State   ScheduleOutcomeState
	Elapsed time.Duration

	// Circles is the count the canvas held once the stage completed, and
	// BestCost the cost it reached there. Together with Elapsed they are one
	// point on the cost curve, which is all ProjectScheduleCost reads.
	Circles  int
	BestCost float64

	// CostMeasured says whether BestCost is a number the stage actually
	// produced, and follows ScheduleStageOutcome's convention for the same
	// reason: zero is a legitimate cost — a perfect fit costs exactly nothing —
	// so absence has to be stated rather than inferred from the value. It
	// defaults to false, so a timing assembled without a cost cannot pass an
	// accidental zero off as a measurement.
	CostMeasured bool
}

// ScheduleKindProjection is the estimate for one stage kind.
//
// Kinds are projected separately and never blended. An extend stage is roughly
// flat in circle count, because the circles it inherits are baked into the
// canvas once and never re-evaluated; a polish stage is not, because selection
// walks the whole canvas — a residual-region sweep renders every circle twice
// per selection pass — so its cost climbs with the circle count. One rate
// covering both would be wrong for each of them in opposite directions.
type ScheduleKindProjection struct {
	Kind ScheduleStageKind

	// Samples is how many completed stages of this kind were measured, and
	// Observed the wall clock they took together.
	Samples  int
	Observed time.Duration

	// PerStage is Observed divided by Samples, zero when the kind is not
	// projected.
	PerStage time.Duration

	// RemainingStages counts planned stages of this kind that have neither
	// completed nor been skipped. ConditionalStages is the subset of those that
	// carry a condition and may yet be declined.
	RemainingStages   int
	ConditionalStages int

	// Remaining is PerStage times RemainingStages, and ConditionalRemaining the
	// part of it that depends on conditions nobody can decide yet. Both are zero
	// when the kind is not projected.
	Remaining            time.Duration
	ConditionalRemaining time.Duration

	// Note explains why a kind was not projected, or qualifies an estimate that
	// is known to lean one way.
	Note string
}

// Projected reports whether this kind's estimate rests on enough measurements.
func (p ScheduleKindProjection) Projected() bool {
	return p.Samples >= MinProjectionSamples && p.PerStage > 0
}

// ScheduleProjection is a finish estimate derived entirely from measurement.
//
// Nothing in it comes from a model of the optimizer. If the stages that have
// run say nothing about a kind still to come, the projection says so rather
// than filling the gap with a guess.
type ScheduleProjection struct {
	AsOf time.Time

	// Kinds holds one entry per stage kind present in the plan, base first.
	Kinds []ScheduleKindProjection

	// RemainingStages counts every planned stage that has neither completed nor
	// been skipped. Zero means there is nothing left to project.
	RemainingStages int

	// Complete reports that every kind with stages left could be projected. When
	// it is false the totals below are zero and the per-kind notes say what is
	// missing.
	Complete bool

	// Remaining is the estimate for everything still planned, and Firm the part
	// of it that runs whatever the campaign measures. FinishBy and EarliestFinish
	// are AsOf plus those two.
	Remaining time.Duration
	Firm      time.Duration

	FinishBy       time.Time
	EarliestFinish time.Time

	// Cost is what the same measurements say about the other scarce resource.
	// It hangs here rather than behind a second entry point so a caller cannot
	// build one projection from one set of stages and the other from another,
	// and so the time answer it carries is by construction the Remaining above.
	Cost ScheduleCostProjection
}

// ProjectScheduleFinish estimates the remaining wall clock of a campaign from
// the stages that have already run.
//
// It is a pure function of (plan, timings, asOf), accumulates nothing, and
// stores nothing: hand it the same records and it returns the same estimate.
// Timings for stages outside the plan are ignored, and a stage the timings say
// nothing about is treated as still to come.
//
// A running stage counts as fully remaining. The alternative — subtracting the
// part of it already elapsed — would need a per-stage progress model, which is
// the a-priori reasoning this projection exists to avoid.
//
// It also fills Cost from the same timings, so one call answers both of the
// questions a campaign can be asking. See ProjectScheduleCost.
func ProjectScheduleFinish(plan []ScheduleStage, timings []ScheduleStageTiming, asOf time.Time) ScheduleProjection {
	observed := make(map[ScheduleStageKind]*ScheduleKindProjection)
	kindOf := func(kind ScheduleStageKind) *ScheduleKindProjection {
		if entry, ok := observed[kind]; ok {
			return entry
		}

		entry := &ScheduleKindProjection{Kind: kind}
		observed[kind] = entry

		return entry
	}

	states := make(map[int]ScheduleOutcomeState, len(timings))
	for _, timing := range timings {
		if timing.Index < 0 || timing.Index >= len(plan) {
			continue
		}

		states[timing.Index] = timing.State
		// Only a completed stage measured anything. A skipped one took no time
		// and a failed one stopped early, so neither says what a stage costs.
		if timing.State != ScheduleOutcomeCompleted || timing.Elapsed <= 0 {
			continue
		}

		entry := kindOf(timing.Kind)
		entry.Samples++
		entry.Observed += timing.Elapsed
	}

	for _, stage := range plan {
		entry := kindOf(stage.Kind)
		switch states[stage.Index] {
		case ScheduleOutcomeCompleted, ScheduleOutcomeSkipped:
			continue
		}

		entry.RemainingStages++
		if stage.When != nil {
			entry.ConditionalStages++
		}
	}

	projection := ScheduleProjection{AsOf: asOf, Complete: true}

	for _, kind := range []ScheduleStageKind{ScheduleStageBase, ScheduleStageExtend, ScheduleStagePolish} {
		entry, ok := observed[kind]
		if !ok {
			continue
		}

		finishKindProjection(entry)

		if entry.RemainingStages > 0 && !entry.Projected() {
			projection.Complete = false
		}

		projection.RemainingStages += entry.RemainingStages
		projection.Kinds = append(projection.Kinds, *entry)
	}

	if projection.Complete {
		for _, entry := range projection.Kinds {
			projection.Remaining += entry.Remaining
			projection.Firm += entry.Remaining - entry.ConditionalRemaining
		}

		projection.FinishBy = asOf.Add(projection.Remaining)
		projection.EarliestFinish = asOf.Add(projection.Firm)
	}

	// The cost projection is computed last because it takes Remaining as its
	// time budget. An incomplete finish projection leaves that zero, which is
	// the honest input: the campaign then gets its circle answer and no time
	// answer, rather than a time answer resting on a finish nobody could
	// estimate.
	projection.Cost = ProjectScheduleCost(plan, timings, projection.Remaining)

	return projection
}

// finishKindProjection turns one kind's measurements into its estimate, or
// states why it has none.
func finishKindProjection(entry *ScheduleKindProjection) {
	if entry.RemainingStages == 0 {
		return
	}

	if entry.Samples < MinProjectionSamples {
		entry.Note = fmt.Sprintf(
			"insufficient data: %d completed %s stage(s), %d needed before %s stages are projected",
			entry.Samples, entry.Kind, MinProjectionSamples, entry.Kind)

		return
	}

	entry.PerStage = entry.Observed / time.Duration(entry.Samples)
	entry.Remaining = entry.PerStage * time.Duration(entry.RemainingStages)

	entry.ConditionalRemaining = entry.PerStage * time.Duration(entry.ConditionalStages)
	if entry.Kind == ScheduleStagePolish {
		entry.Note = "a polish stage costs more as the canvas grows, so this is a lower bound"
	}
}
