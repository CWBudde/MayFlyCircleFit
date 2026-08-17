package app

import (
	"strings"
	"testing"
)

func TestPlannedIterationsCountsTheStageBudget(t *testing.T) {
	tests := []struct {
		name  string
		steps string
		// want is the budget of the last stage the document realizes.
		want int
	}{
		{
			// The base stage alone: 8 circles in one batch of 8, one epoch of
			// 200 iterations.
			name:  "base batch stage",
			steps: `[]`,
			want:  1 * 1 * 200,
		},
		{
			// An extend optimizes only the circles it appends; the ones it
			// inherits are a frozen prefix. 8 appended in batches of 8 is one
			// optimizer run, whatever the canvas already holds.
			name:  "extend counts only the appended circles",
			steps: `[{"type": "extend", "repeat": 8, "additionalCircles": 8}]`,
			want:  1 * 1 * 200,
		},
		{
			// A narrower batch splits the same append into more runs.
			name:  "extend with a narrower batch",
			steps: `[{"type": "extend", "additionalCircles": 8, "batchSize": 3}]`,
			want:  3 * 1 * 200, // ceil(8/3) = 3 optimizer runs
		},
		{
			name:  "extend budget overrides apply",
			steps: `[{"type": "extend", "additionalCircles": 8, "epochs": 4, "iters": 500}]`,
			want:  1 * 4 * 500,
		},
		{
			// A polish stage runs no batch stage at all, only sweeps.
			name:  "polish uses the polishing budget",
			steps: `[{"type": "polish"}]`,
			want:  3 * 2 * 1000,
		},
		{
			name:  "polish budget overrides apply",
			steps: `[{"type": "polish", "maxSweeps": 5, "epochs": 1, "iters": 400, "stagnationIters": 400}]`,
			want:  5 * 1 * 400,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			plan, err := documentWithSteps(t, testCase.steps).Expand()
			if err != nil {
				t.Fatalf("Expand() error = %v", err)
			}
			last := plan[len(plan)-1]
			if got := last.PlannedIterations(); got != testCase.want {
				t.Fatalf("PlannedIterations() = %d, want %d", got, testCase.want)
			}
		})
	}
}

// TestConditionDescribeStatesBothClauses keeps a conditional stage from being
// printed as merely "conditional": a plan has to say on what.
func TestConditionDescribeStatesBothClauses(t *testing.T) {
	plan, err := documentWithSteps(t, referenceCampaignSteps).Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	var described string
	for _, stage := range plan {
		if stage.When != nil {
			described = stage.When.Describe()
			break
		}
	}
	for _, want := range []string{
		"only at 32/64/96/128/192/256 circles",
		"abandoned after 2 consecutive stages gaining less than 1",
	} {
		if !strings.Contains(described, want) {
			t.Errorf("Describe() = %q, want it to contain %q", described, want)
		}
	}
	var unconditional *ScheduleCondition
	if got := unconditional.Describe(); got != "" {
		t.Errorf("a nil condition described itself as %q", got)
	}
}

// TestReferenceCampaignPlanMatchesTheHandComputation is the Task 16.4
// acceptance check. The arithmetic is written out rather than computed, so a
// reader can check the figure without running anything.
//
// The campaign is base 8 circles, +8 extends to 512, and a conditional polish
// at 32/64/96/128/192/256, over the standard test base: batch mode, batch size
// 8, 200 iterations, one optimizer epoch, and the shipped polishing defaults of
// 3 sweeps × 2 epochs × 1000 iterations.
//
//	stages   1 base + 63 extends + 6 polishes                     =    70
//
//	base     ceil(8 circles / 8 per batch) = 1 optimizer run
//	         1 run × 1 epoch × 200 iters                          =   200
//
//	extend   each appends 8 circles, the prefix is frozen, so
//	         ceil(8 / 8) = 1 optimizer run
//	         1 run × 1 epoch × 200 iters                          =   200
//	         × 63 extends                                         = 12600
//
//	polish   no batch stage; 3 sweeps × 2 epochs × 1000 iters     =  6000
//	         × 6 polishes                                         = 36000
//
//	total    200 + 12600 + 36000                                  = 48800
//	  of which conditional (the 6 polishes)                       = 36000
//	  of which unconditional (base + extends)                     = 12800
//
// 63 extends because the canvas climbs from 8 to 512 in steps of 8, and
// (512 - 8) / 8 = 63.
func TestReferenceCampaignPlanMatchesTheHandComputation(t *testing.T) {
	plan, err := documentWithSteps(t, referenceCampaignSteps).Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	summary := SummarizeSchedulePlan(plan)

	const (
		wantStages      = 1 + 63 + 6
		wantBase        = 1 * 1 * 200
		wantExtends     = 63 * (1 * 1 * 200)
		wantPolishes    = 6 * (3 * 2 * 1000)
		wantTotal       = wantBase + wantExtends + wantPolishes
		wantConditional = wantPolishes
	)
	if wantTotal != 48800 {
		t.Fatalf("the hand computation itself is inconsistent: %d", wantTotal)
	}

	checks := []struct {
		name string
		got  int
		want int
	}{
		{"stages", summary.Stages, wantStages},
		{"base stages", summary.Base, 1},
		{"extend stages", summary.Extends, 63},
		{"polish stages", summary.Polishes, 6},
		{"conditional stages", summary.Conditional, 6},
		{"total iterations", summary.TotalIterations, wantTotal},
		{"conditional iterations", summary.ConditionalIterations, wantConditional},
		{"unconditional iterations", summary.FirmIterations(), wantBase + wantExtends},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %d, want %d", check.name, check.got, check.want)
		}
	}

	// The plan reaches exactly 512 circles, which is what makes the 63 in the
	// hand computation the right multiplier.
	if last := plan[len(plan)-1]; last.Circles != 512 {
		t.Errorf("final stage sits at %d circles, want 512", last.Circles)
	}
	// Every conditional stage is a polish. An extend cannot be conditional,
	// because skipping one would move the circle count of every later stage.
	for _, stage := range plan {
		if stage.When != nil && stage.Kind != ScheduleStagePolish {
			t.Errorf("stage %d of kind %s carries a condition", stage.Index, stage.Kind)
		}
	}
}
