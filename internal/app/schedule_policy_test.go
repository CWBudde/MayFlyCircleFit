package app

import (
	"strconv"
	"strings"
	"testing"
)

// referenceCampaign is the second run's policy stated once: base 8, +8 to 512,
// polish at 32/64/96/128/192/256, and polishing abandoned after two consecutive
// polish stages that gained less than 1.0 cost units. Nothing here is code —
// the abort rule and the circle guard are both authored values, which is the
// whole point of Task 16.3.
const referenceCampaignSteps = `[
    {"type": "extend", "repeat": 3, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [32, 64, 96, 128, 192, 256], "minGain": 1.0, "abortAfterBarren": 2}},
    {"type": "extend", "repeat": 4, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [32, 64, 96, 128, 192, 256], "minGain": 1.0, "abortAfterBarren": 2}},
    {"type": "extend", "repeat": 4, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [32, 64, 96, 128, 192, 256], "minGain": 1.0, "abortAfterBarren": 2}},
    {"type": "extend", "repeat": 4, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [32, 64, 96, 128, 192, 256], "minGain": 1.0, "abortAfterBarren": 2}},
    {"type": "extend", "repeat": 8, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [32, 64, 96, 128, 192, 256], "minGain": 1.0, "abortAfterBarren": 2}},
    {"type": "extend", "repeat": 8, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [32, 64, 96, 128, 192, 256], "minGain": 1.0, "abortAfterBarren": 2}},
    {"type": "extend", "repeat": 32, "additionalCircles": 8}
  ]`

// realizeSchedule walks a plan exactly the way the executor does — evaluate the
// next planned stage against the outcomes recorded so far, run it or record it
// skipped, repeat — and returns the realized sequence. The gains are supplied
// by the test rather than measured, which is what makes the policy testable
// without an optimizer.
func realizeSchedule(plan []ScheduleStage, cost float64, gain func(ScheduleStage) float64) ([]ScheduleStage, []bool) {
	outcomes := make([]ScheduleStageOutcome, 0, len(plan))
	run := make([]ScheduleStage, 0, len(plan))

	ran := make([]bool, len(plan))
	for index, stage := range plan {
		verdict := EvaluateScheduleStage(plan, index, outcomes)
		if !verdict.Run {
			outcomes = append(outcomes, ScheduleStageOutcome{
				Index: index, Kind: stage.Kind, State: ScheduleOutcomeSkipped,
			})

			continue
		}

		cost -= gain(stage)
		outcomes = append(outcomes, ScheduleStageOutcome{
			Index: index, Kind: stage.Kind, State: ScheduleOutcomeCompleted,
			BestCost: cost, CostMeasured: true,
		})
		run = append(run, stage)
		ran[index] = true
	}

	return run, ran
}

// polishPoints lists the circle counts at which a polish actually ran.
func polishPoints(stages []ScheduleStage) []int {
	points := []int{}

	for _, stage := range stages {
		if stage.Kind == ScheduleStagePolish {
			points = append(points, stage.Circles)
		}
	}

	return points
}

func formatInts(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(value)
	}

	return strings.Join(parts, ",")
}

// TestReferenceCampaignPolicyRealizesTheExactStageSequence is the Task 16.3
// acceptance check. The same authored document produces different realized
// sequences purely from what the stages measured, and every one of them is
// stated exactly.
func TestReferenceCampaignPolicyRealizesTheExactStageSequence(t *testing.T) {
	doc := documentWithSteps(t, referenceCampaignSteps)

	plan, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	// The plan itself is unconditional and is what a dry run prints: 1 base, 63
	// extends, 6 polishes, whatever happens at run time.
	if len(plan) != 70 {
		t.Fatalf("planned stages = %d, want 70", len(plan))
	}

	cases := []struct {
		name string
		// polishGain is what each successive polish stage removes from the cost.
		polishGain []float64
		wantPolish []int
	}{
		{
			name:       "polish keeps paying",
			polishGain: []float64{5, 5, 5, 5, 5, 5},
			wantPolish: []int{32, 64, 96, 128, 192, 256},
		},
		{
			name:       "two barren polishes abandon polishing",
			polishGain: []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5},
			wantPolish: []int{32, 64},
		},
		{
			name:       "a paying polish resets the streak",
			polishGain: []float64{0.5, 5, 0.5, 0.5, 0.5, 0.5},
			wantPolish: []int{32, 64, 96, 128},
		},
		{
			name:       "a gain exactly at the threshold is not barren",
			polishGain: []float64{1.0, 1.0, 1.0, 1.0, 1.0, 1.0},
			wantPolish: []int{32, 64, 96, 128, 192, 256},
		},
		{
			name:       "one barren polish is tolerated throughout",
			polishGain: []float64{0.5, 5, 0.5, 5, 0.5, 5},
			wantPolish: []int{32, 64, 96, 128, 192, 256},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			polishes := 0
			realized, ran := realizeSchedule(plan, 1000, func(stage ScheduleStage) float64 {
				if stage.Kind != ScheduleStagePolish {
					// Extends carry the campaign; the exact figure only has to
					// be large enough that a polish is judged against it.
					return 10
				}

				gain := testCase.polishGain[polishes]
				polishes++

				return gain
			})

			got := polishPoints(realized)
			if formatInts(got) != formatInts(testCase.wantPolish) {
				t.Fatalf("polishes ran at [%s], want [%s]", formatInts(got), formatInts(testCase.wantPolish))
			}
			// Every extend runs regardless: policy may only decline polishes,
			// so the circle climb is identical in all five cases.
			wantStages := 64 + len(testCase.wantPolish)
			if len(realized) != wantStages {
				t.Fatalf("realized %d stages, want %d", len(realized), wantStages)
			}

			for index, stage := range plan {
				if stage.Kind != ScheduleStagePolish && !ran[index] {
					t.Fatalf("stage %d (%s at %d circles) was skipped", index, stage.Kind, stage.Circles)
				}
			}

			if last := realized[len(realized)-1]; last.Circles != 512 || last.Kind != ScheduleStageExtend {
				t.Fatalf("final stage = %s at %d circles, want extend at 512", last.Kind, last.Circles)
			}
		})
	}
}

// TestScheduleCirclesConditionGatesAStage pins the other half of the policy: a
// polish whose circle count is not listed never runs, whatever the costs said.
func TestScheduleCirclesConditionGatesAStage(t *testing.T) {
	doc := documentWithSteps(t, `[
    {"type": "extend", "repeat": 2, "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [16]}},
    {"type": "extend", "additionalCircles": 8},
    {"type": "polish", "when": {"circles": [16]}}
  ]`)

	plan, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	realized, _ := realizeSchedule(plan, 100, func(ScheduleStage) float64 { return 1 })

	// Planned: base 8, +8, +8, polish at 24, +8, polish at 32. Only the listed
	// count would run a polish, and neither polish sits at 16.
	if points := polishPoints(realized); len(points) != 0 {
		t.Fatalf("polishes ran at %v, want none", points)
	}

	if len(realized) != 4 {
		t.Fatalf("realized %d stages, want 4", len(realized))
	}

	verdict := EvaluateScheduleStage(plan, 3, nil)
	if verdict.Run {
		t.Fatalf("stage 3 verdict = run, want skipped")
	}

	if !strings.Contains(verdict.Reason, "24") {
		t.Fatalf("skip reason %q does not name the stage's circle count", verdict.Reason)
	}
}

// TestEvaluateScheduleStageIsPureOverTheOutcomes covers the decision function
// directly, including the cases the campaign test cannot reach.
func TestEvaluateScheduleStageIsPureOverTheOutcomes(t *testing.T) {
	limit := 2
	threshold := 1.0
	condition := &ScheduleCondition{MinGain: &threshold, AbortAfterBarren: &limit}
	plan := []ScheduleStage{
		{Index: 0, Kind: ScheduleStageBase, Circles: 8},
		{Index: 1, Kind: ScheduleStagePolish, Circles: 8, When: condition},
		{Index: 2, Kind: ScheduleStagePolish, Circles: 8, When: condition},
		{Index: 3, Kind: ScheduleStagePolish, Circles: 8, When: condition},
	}
	completed := func(index int, kind ScheduleStageKind, cost float64) ScheduleStageOutcome {
		return ScheduleStageOutcome{
			Index: index, Kind: kind, State: ScheduleOutcomeCompleted,
			BestCost: cost, CostMeasured: true,
		}
	}
	// unmeasured is a stage that settled without a cost anyone can read — the
	// only thing that makes a gain unknown. Zero is not that; see the
	// zero-cost case below.
	unmeasured := func(index int, kind ScheduleStageKind) ScheduleStageOutcome {
		return ScheduleStageOutcome{Index: index, Kind: kind, State: ScheduleOutcomeCompleted}
	}

	cases := []struct {
		name     string
		index    int
		outcomes []ScheduleStageOutcome
		wantRun  bool
	}{
		{
			name:     "no outcomes yet",
			index:    1,
			outcomes: nil,
			wantRun:  true,
		},
		{
			name:  "one barren polish is below the limit",
			index: 2,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 100),
				completed(1, ScheduleStagePolish, 99.5),
			},
			wantRun: true,
		},
		{
			name:  "two barren polishes reach the limit",
			index: 3,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 100),
				completed(1, ScheduleStagePolish, 99.5),
				completed(2, ScheduleStagePolish, 99.1),
			},
			wantRun: false,
		},
		{
			name:  "a later outcome cannot decide an earlier stage",
			index: 1,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 100),
				completed(1, ScheduleStagePolish, 99.5),
				completed(2, ScheduleStagePolish, 99.1),
			},
			wantRun: true,
		},
		{
			name:  "an unmeasured predecessor is not evidence of barrenness",
			index: 3,
			outcomes: []ScheduleStageOutcome{
				unmeasured(0, ScheduleStageBase),
				completed(1, ScheduleStagePolish, 99.5),
				completed(2, ScheduleStagePolish, 99.1),
			},
			wantRun: true,
		},
		{
			// A perfect fit costs exactly zero, which is a measurement and not a
			// hole in the record. Two polishes that each gained nothing on top of
			// it are as barren as a stage can be, so the third must be declined.
			name:  "a completed zero-cost stage feeds the barren streak",
			index: 3,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 0),
				completed(1, ScheduleStagePolish, 0),
				completed(2, ScheduleStagePolish, 0),
			},
			wantRun: false,
		},
		{
			// The same zero seen only once still ends a streak that has not
			// reached the limit, because one barren polish is tolerated.
			name:  "a single zero-gain polish stays below the limit",
			index: 2,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 0),
				completed(1, ScheduleStagePolish, 0),
			},
			wantRun: true,
		},
		{
			name:  "skipped stages neither count nor break the streak",
			index: 3,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 100),
				completed(1, ScheduleStagePolish, 99.5),
				{Index: 2, Kind: ScheduleStagePolish, State: ScheduleOutcomeSkipped},
			},
			wantRun: true,
		},
		{
			name:  "a running stage carries no verdict",
			index: 3,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 100),
				completed(1, ScheduleStagePolish, 99.5),
				{Index: 2, Kind: ScheduleStagePolish, State: ScheduleOutcomePending},
			},
			wantRun: true,
		},
		{
			name:  "an unconditional stage always runs",
			index: 0,
			outcomes: []ScheduleStageOutcome{
				completed(0, ScheduleStageBase, 100),
			},
			wantRun: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			verdict := EvaluateScheduleStage(plan, testCase.index, testCase.outcomes)
			if verdict.Run != testCase.wantRun {
				t.Fatalf("Run = %v (%q), want %v", verdict.Run, verdict.Reason, testCase.wantRun)
			}

			if !verdict.Run && verdict.Reason == "" {
				t.Fatalf("a declined stage must say why")
			}
			// The same inputs must give the same answer, every time it is asked.
			if again := EvaluateScheduleStage(plan, testCase.index, testCase.outcomes); again != verdict {
				t.Fatalf("second evaluation = %+v, first = %+v", again, verdict)
			}
		})
	}
}

func TestParseScheduleRejectsMalformedConditions(t *testing.T) {
	tests := []struct {
		name    string
		steps   string
		wantErr string
	}{
		{
			name:    "condition on an extend step",
			steps:   `[{"type": "extend", "additionalCircles": 8, "when": {"circles": [16]}}]`,
			wantErr: "polish step only",
		},
		{
			name:    "gain without a limit",
			steps:   `[{"type": "polish", "when": {"minGain": 1.0}}]`,
			wantErr: "together",
		},
		{
			name:    "limit without a gain",
			steps:   `[{"type": "polish", "when": {"abortAfterBarren": 2}}]`,
			wantErr: "together",
		},
		{
			name:    "negative gain",
			steps:   `[{"type": "polish", "when": {"minGain": -1.0, "abortAfterBarren": 2}}]`,
			wantErr: "negative",
		},
		{
			name:    "zero limit",
			steps:   `[{"type": "polish", "when": {"minGain": 1.0, "abortAfterBarren": 0}}]`,
			wantErr: "abortAfterBarren",
		},
		{
			name:    "circle count out of range",
			steps:   `[{"type": "polish", "when": {"circles": [0]}}]`,
			wantErr: "circles",
		},
		{
			name:    "duplicate circle count",
			steps:   `[{"type": "polish", "when": {"circles": [16, 16]}}]`,
			wantErr: "twice",
		},
		{
			name:    "unknown condition field",
			steps:   `[{"type": "polish", "when": {"circlez": [16]}}]`,
			wantErr: "circlez",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := strings.Replace(baseDocument, `"steps": []`, `"steps": `+test.steps, 1)

			_, err := ParseSchedule([]byte(source))
			if err == nil {
				t.Fatalf("ParseSchedule() accepted %s", test.steps)
			}

			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error %q does not mention %q", err, test.wantErr)
			}
		})
	}
}
