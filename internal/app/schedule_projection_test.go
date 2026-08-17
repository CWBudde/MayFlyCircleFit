package app

import (
	"strings"
	"testing"
	"time"
)

// projectionPlan is a small campaign with both kinds and one conditional
// polish: base, extend, extend, polish (conditional), extend.
const projectionSteps = `[
    {"type": "extend", "repeat": 2, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [24], "minGain": 1.0, "abortAfterBarren": 2}},
    {"type": "extend", "additionalCircles": 8}
]`

func projectionPlan(t *testing.T) []ScheduleStage {
	t.Helper()
	plan, err := documentWithSteps(t, projectionSteps).Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(plan) != 5 {
		t.Fatalf("planned stages = %d, want 5", len(plan))
	}
	return plan
}

func completed(index int, kind ScheduleStageKind, elapsed time.Duration) ScheduleStageTiming {
	return ScheduleStageTiming{Index: index, Kind: kind, State: ScheduleOutcomeCompleted, Elapsed: elapsed}
}

func TestProjectScheduleFinishRefusesToGuess(t *testing.T) {
	plan := projectionPlan(t)
	asOf := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		timings []ScheduleStageTiming
		// wantNoteFor names the kind whose note must report insufficient data.
		wantNoteFor ScheduleStageKind
	}{
		{
			name:        "nothing has run",
			timings:     nil,
			wantNoteFor: ScheduleStageBase,
		},
		{
			name:        "one sample of a kind is not a rate",
			timings:     []ScheduleStageTiming{completed(0, ScheduleStageBase, time.Minute), completed(1, ScheduleStageExtend, time.Minute)},
			wantNoteFor: ScheduleStageExtend,
		},
		{
			name: "extends measured but no polish yet",
			timings: []ScheduleStageTiming{
				completed(0, ScheduleStageBase, time.Minute),
				completed(1, ScheduleStageExtend, time.Minute),
				completed(2, ScheduleStageExtend, time.Minute),
			},
			wantNoteFor: ScheduleStagePolish,
		},
		{
			name: "a skipped stage is not a measurement",
			timings: []ScheduleStageTiming{
				completed(0, ScheduleStageBase, time.Minute),
				completed(1, ScheduleStageExtend, time.Minute),
				{Index: 2, Kind: ScheduleStageExtend, State: ScheduleOutcomeSkipped},
			},
			wantNoteFor: ScheduleStageExtend,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			projection := ProjectScheduleFinish(plan, testCase.timings, asOf)
			if projection.Complete {
				t.Fatalf("projection claimed a finish time from %d timings", len(testCase.timings))
			}
			if !projection.FinishBy.IsZero() || projection.Remaining != 0 {
				t.Fatalf("an incomplete projection still reported %v by %v", projection.Remaining, projection.FinishBy)
			}
			for _, kind := range projection.Kinds {
				if kind.Kind != testCase.wantNoteFor {
					continue
				}
				if !strings.Contains(kind.Note, "insufficient data") {
					t.Fatalf("%s note = %q, want it to report insufficient data", kind.Kind, kind.Note)
				}
				return
			}
			t.Fatalf("no entry for %s in the projection", testCase.wantNoteFor)
		})
	}
}

// TestProjectScheduleFinishDerivesEachKindSeparately is the measurement rule
// stated as a test: the extend rate and the polish rate are computed from their
// own stages, and a blended rate would give a different answer.
func TestProjectScheduleFinishDerivesEachKindSeparately(t *testing.T) {
	plan := projectionPlan(t)
	asOf := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	// Two extends at 1 and 3 minutes, two polishes at 10 and 20 minutes. Stage 2
	// is an extend and stage 3 the conditional polish; the extra polish sample
	// is supplied at an index the plan does not hold, so it is ignored — which
	// is itself the assertion that out-of-plan timings cannot move the estimate.
	timings := []ScheduleStageTiming{
		completed(0, ScheduleStageBase, 30*time.Second),
		completed(1, ScheduleStageExtend, time.Minute),
		completed(2, ScheduleStageExtend, 3*time.Minute),
		completed(99, ScheduleStagePolish, time.Hour),
	}
	projection := ProjectScheduleFinish(plan, timings, asOf)
	if projection.Complete {
		t.Fatal("the polish kind has no in-plan samples, yet the projection was complete")
	}

	// Give the polish kind two real samples by planning a second polish.
	plan = append(plan, ScheduleStage{
		Index: 5, Kind: ScheduleStagePolish, Circles: 32, Config: plan[3].Config,
	})
	timings = append(timings[:3],
		completed(3, ScheduleStagePolish, 10*time.Minute),
		completed(5, ScheduleStagePolish, 20*time.Minute),
	)
	// Stage 4, the last extend, is all that remains.
	projection = ProjectScheduleFinish(plan, timings, asOf)
	if !projection.Complete {
		t.Fatalf("projection incomplete with both kinds measured: %+v", projection.Kinds)
	}
	// Extends: (1m + 3m) / 2 = 2m each, one left.
	want := 2 * time.Minute
	if projection.Remaining != want {
		t.Fatalf("remaining = %v, want %v", projection.Remaining, want)
	}
	if projection.FinishBy != asOf.Add(want) {
		t.Fatalf("finish = %v, want %v", projection.FinishBy, asOf.Add(want))
	}
	// A blended rate over all four completed stages would have been
	// (0.5 + 1 + 3 + 10 + 20) / 5 minutes, nowhere near the extend rate.
	for _, kind := range projection.Kinds {
		if kind.Kind == ScheduleStageExtend && kind.PerStage != 2*time.Minute {
			t.Fatalf("extend rate = %v, want 2m derived from extends alone", kind.PerStage)
		}
	}
}

// TestProjectScheduleFinishSeparatesConditionalWork keeps a conditional stage
// from being promised: it is counted, and counted apart.
func TestProjectScheduleFinishSeparatesConditionalWork(t *testing.T) {
	plan := projectionPlan(t)
	asOf := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	timings := []ScheduleStageTiming{
		completed(0, ScheduleStageBase, 30*time.Second),
		completed(1, ScheduleStageExtend, time.Minute),
		completed(2, ScheduleStageExtend, 3*time.Minute),
		// A polish elsewhere in the campaign measured twice, so the kind has a
		// rate even though the plan's own polish has not run.
		{Index: 3, Kind: ScheduleStagePolish, State: ScheduleOutcomePending},
	}
	// Two polish samples are needed; plan a second polish and measure both.
	plan = append(plan,
		ScheduleStage{Index: 5, Kind: ScheduleStagePolish, Circles: 32, Config: plan[3].Config, When: plan[3].When},
		ScheduleStage{Index: 6, Kind: ScheduleStagePolish, Circles: 40, Config: plan[3].Config, When: plan[3].When},
	)
	timings = append(timings,
		completed(5, ScheduleStagePolish, 4*time.Minute),
		completed(6, ScheduleStagePolish, 6*time.Minute),
	)

	projection := ProjectScheduleFinish(plan, timings, asOf)
	if !projection.Complete {
		t.Fatalf("projection incomplete: %+v", projection.Kinds)
	}
	// Remaining: extend stage 4 at 2m, plus the conditional polish stage 3 at 5m.
	if want := 7 * time.Minute; projection.Remaining != want {
		t.Fatalf("remaining = %v, want %v", projection.Remaining, want)
	}
	if want := 2 * time.Minute; projection.Firm != want {
		t.Fatalf("firm remaining = %v, want %v with the conditional polish removed", projection.Firm, want)
	}
	if projection.EarliestFinish != asOf.Add(2*time.Minute) {
		t.Fatalf("earliest finish = %v, want %v", projection.EarliestFinish, asOf.Add(2*time.Minute))
	}
	// The polish estimate is qualified: those samples were measured on a smaller
	// canvas than the stage they are projecting.
	for _, kind := range projection.Kinds {
		if kind.Kind == ScheduleStagePolish && !strings.Contains(kind.Note, "lower bound") {
			t.Fatalf("polish note = %q, want it to qualify the estimate as a lower bound", kind.Note)
		}
	}
}
