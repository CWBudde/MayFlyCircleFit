package app

import (
	"fmt"
	"strconv"
	"strings"
)

// The plan summary answers the one question worth answering before a campaign
// is started: how much optimizer work has just been ordered. It is derived from
// the realized stage list alone, so it needs no store, no server, and no job —
// which is what lets a dry run answer it without reaching anything.

// SchedulePlanSummary counts a realized plan and its nominal optimizer work.
//
// The conditional figures are kept apart from the unconditional ones rather
// than folded in. A conditional stage is planned but not promised: whether it
// runs is decided at run time from what the earlier stages measured, and a
// single total that quietly includes it would overstate the committed work by
// exactly the part nobody can predict yet.
type SchedulePlanSummary struct {
	Stages   int
	Base     int
	Extends  int
	Polishes int

	// Conditional is how many of the planned stages carry a `when` object.
	Conditional int

	// TotalIterations is the whole nominal planned count, conditional stages
	// included. See PlannedIterations for what it does and does not bound.
	TotalIterations int

	// ConditionalIterations is the part of TotalIterations that belongs to
	// conditional stages, so a reader can subtract it and see the floor.
	ConditionalIterations int
}

// FirmIterations is the nominal count that runs whatever the campaign measures.
func (s SchedulePlanSummary) FirmIterations() int {
	return s.TotalIterations - s.ConditionalIterations
}

// SummarizeSchedulePlan counts a realized plan. It reads the stage list only,
// and never the outcomes: a summary is what was ordered, not what happened.
func SummarizeSchedulePlan(plan []ScheduleStage) SchedulePlanSummary {
	summary := SchedulePlanSummary{Stages: len(plan)}
	for _, stage := range plan {
		iterations := stage.PlannedIterations()
		summary.TotalIterations += iterations

		switch stage.Kind {
		case ScheduleStageBase:
			summary.Base++
		case ScheduleStageExtend:
			summary.Extends++
		case ScheduleStagePolish:
			summary.Polishes++
		}

		if stage.When != nil {
			summary.Conditional++
			summary.ConditionalIterations += iterations
		}
	}

	return summary
}

// PlannedIterations is the nominal optimizer iteration count of one realized
// stage: the planned stages times their epochs times their iterations.
//
// It is not a prediction of the iterations actually spent: early stopping,
// convergence detection, and a polish sweep that stops improving all cut the
// real figure. Nothing raises it. The bounded residual-refill stages a batch
// run may attempt (renderer.MaxExtraBatchStages) used to, by a whole stage
// each, which is what made the arms of two campaigns incomparable; a refill is
// now drawn from the planned budget rather than added to it, so it is counted
// here already. Callers presenting this number must still say what it is — the
// nominal plan and an upper bound, not a prediction.
func (s ScheduleStage) PlannedIterations() int {
	config := s.Config

	total := 0
	if !config.PolishingOnly {
		total = s.plannedOptimizerStages() * config.Iters *
			max(config.OptimizerEpochs, 1) * max(config.OptimizerRestarts, 1)
	}

	if config.PolishingEnabled {
		total += config.PolishingMaxSweeps * config.PolishingEpochs * config.PolishingIters
	}

	return total
}

// plannedOptimizerStages is how many optimizer runs the stage schedules.
//
// An extend stage differs from a fresh run of the same size: the circles it
// inherits are a frozen prefix that is baked once, never re-optimized, so only
// the appended circles are divided into batches.
func (s ScheduleStage) plannedOptimizerStages() int {
	config := s.Config

	circles := config.Circles
	if s.Kind == ScheduleStageExtend {
		circles = s.AdditionalCircles
	}

	if circles < 1 {
		return 0
	}

	switch config.Mode {
	case ModeSequential:
		return circles
	case ModeBatch:
		batchSize := max(config.BatchSize, 1)
		return (circles + batchSize - 1) / batchSize
	default:
		return 1
	}
}

// Describe states a condition the way an operator would read it, so a plan can
// print why a stage is conditional instead of only that it is.
func (c *ScheduleCondition) Describe() string {
	if c == nil {
		return ""
	}

	clauses := make([]string, 0, 2)
	if len(c.Circles) > 0 {
		counts := make([]string, len(c.Circles))
		for i, circles := range c.Circles {
			counts[i] = strconv.Itoa(circles)
		}

		clauses = append(clauses, "only at "+strings.Join(counts, "/")+" circles")
	}

	if c.MinGain != nil && c.AbortAfterBarren != nil {
		clauses = append(clauses, fmt.Sprintf("abandoned after %d consecutive stages gaining less than %g",
			*c.AbortAfterBarren, *c.MinGain))
	}

	if len(clauses) == 0 {
		return "decided at run time"
	}

	return strings.Join(clauses, "; ")
}
