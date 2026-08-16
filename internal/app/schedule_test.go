package app

import (
	"errors"
	"strings"
	"testing"
)

// baseDocument is the smallest document that parses: a batch base stage and no
// steps. Tests copy it and edit the one field under test.
const baseDocument = `{
  "schemaVersion": 1,
  "name": "smoke",
  "seed": 4242,
  "base": {
    "refPath": "assets/ref.png",
    "mode": "batch",
    "circles": 8,
    "batchSize": 8,
    "iters": 200,
    "popSize": 30
  },
  "steps": []
}`

func documentWithSteps(t *testing.T, steps string) *ScheduleDocument {
	t.Helper()
	source := strings.Replace(baseDocument, `"steps": []`, `"steps": `+steps, 1)
	doc, err := ParseSchedule([]byte(source))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}
	return doc
}

func TestParseScheduleRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name     string
		document string
		field    string
	}{
		{
			name:     "unknown top level field",
			document: strings.Replace(baseDocument, `"name": "smoke",`, `"name": "smoke", "cadence": "weekly",`, 1),
			field:    "cadence",
		},
		{
			name:     "unknown base field",
			document: strings.Replace(baseDocument, `"circles": 8,`, `"circles": 8, "circlez": 9,`, 1),
			field:    "circlez",
		},
		{
			name:     "unknown step field",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "extend", "additionalCircles": 8, "wobble": 3}]`, 1),
			field:    "wobble",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSchedule([]byte(test.document))
			if err == nil {
				t.Fatalf("ParseSchedule() error = nil, want rejection of %q", test.field)
			}
			if !strings.Contains(err.Error(), test.field) {
				t.Fatalf("ParseSchedule() error = %v, want it to name %q", err, test.field)
			}
		})
	}
}

// TestParseScheduleRejectsSilentlyOverriddenFields is the documented trap: a
// field that ApplyDefaults puts back must be an error naming the field that
// actually works, never a silent drop.
func TestParseScheduleRejectsSilentlyOverriddenFields(t *testing.T) {
	const anchor = `"refPath": "assets/ref.png",`
	tests := []struct {
		name      string
		old       string
		new       string
		field     string
		mentions  string
		wantError bool
	}{
		{
			name:      "convergenceEnabled false",
			old:       anchor,
			new:       anchor + ` "convergenceEnabled": false,`,
			field:     "base.convergenceEnabled",
			mentions:  "disableConvergence",
			wantError: true,
		},
		{
			name:      "enableTrace false",
			old:       anchor,
			new:       anchor + ` "enableTrace": false,`,
			field:     "base.enableTrace",
			mentions:  "disableTrace",
			wantError: true,
		},
		{
			name:      "explicit zero circles",
			old:       `"circles": 8,`,
			new:       `"circles": 0,`,
			field:     "base.circles",
			wantError: true,
		},
		{
			name:      "disableConvergence true is honoured",
			old:       anchor,
			new:       anchor + ` "disableConvergence": true,`,
			wantError: false,
		},
		{
			name:      "explicit true convergenceEnabled matches the default",
			old:       anchor,
			new:       anchor + ` "convergenceEnabled": true,`,
			wantError: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := strings.Replace(baseDocument, test.old, test.new, 1)
			_, err := ParseSchedule([]byte(source))
			if !test.wantError {
				if err != nil {
					t.Fatalf("ParseSchedule() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseSchedule() error = nil, want rejection of %s", test.field)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("ParseSchedule() error = %T, want *ValidationError", err)
			}
			if validation.Field != test.field {
				t.Fatalf("ValidationError.Field = %q, want %q", validation.Field, test.field)
			}
			if test.mentions != "" && !strings.Contains(validation.Reason, test.mentions) {
				t.Fatalf("ValidationError.Reason = %q, want it to name %q", validation.Reason, test.mentions)
			}
		})
	}
}

func TestParseScheduleValidation(t *testing.T) {
	tests := []struct {
		name     string
		document string
		wantErr  string
	}{
		{
			name:     "unsupported schema version",
			document: strings.Replace(baseDocument, `"schemaVersion": 1`, `"schemaVersion": 2`, 1),
			wantErr:  "schemaVersion",
		},
		{
			name:     "unknown step type",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "shrink"}]`, 1),
			wantErr:  "type",
		},
		{
			name:     "extend without additionalCircles",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "extend"}]`, 1),
			wantErr:  "additionalCircles",
		},
		{
			name:     "polish carrying an extend field",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "polish", "additionalCircles": 4}]`, 1),
			wantErr:  "additionalCircles",
		},
		{
			name:     "negative repeat",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "extend", "additionalCircles": 8, "repeat": -1}]`, 1),
			wantErr:  "repeat",
		},
		{
			name:     "seed disagrees with the base seed",
			document: strings.Replace(baseDocument, `"popSize": 30`, `"popSize": 30, "seed": 7`, 1),
			wantErr:  "seed",
		},
		{
			name:     "steps require a batch base",
			document: strings.Replace(strings.Replace(baseDocument, `"mode": "batch"`, `"mode": "joint"`, 1), `"steps": []`, `"steps": [{"type": "extend", "additionalCircles": 8}]`, 1),
			wantErr:  "mode",
		},
		{
			name:     "extension beyond the circle limit",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "extend", "additionalCircles": 8, "repeat": 500}]`, 1),
			wantErr:  "circle limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSchedule([]byte(test.document))
			if err == nil {
				t.Fatalf("ParseSchedule() error = nil, want an error mentioning %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ParseSchedule() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

func TestScheduleExpandRepeatsGeneratorSteps(t *testing.T) {
	doc := documentWithSteps(t, `[
    {"type": "extend", "repeat": 3, "additionalCircles": 8},
    {"type": "polish", "maxSweeps": 4, "activeSetSize": 8}
  ]`)

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	want := []struct {
		kind    ScheduleStageKind
		circles int
	}{
		{ScheduleStageBase, 8},
		{ScheduleStageExtend, 16},
		{ScheduleStageExtend, 24},
		{ScheduleStageExtend, 32},
		{ScheduleStagePolish, 32},
	}
	if len(stages) != len(want) {
		t.Fatalf("Expand() produced %d stages, want %d", len(stages), len(want))
	}
	for i, expected := range want {
		stage := stages[i]
		if stage.Index != i {
			t.Fatalf("stage %d has Index %d", i, stage.Index)
		}
		if stage.Kind != expected.kind {
			t.Fatalf("stage %d Kind = %q, want %q", i, stage.Kind, expected.kind)
		}
		if stage.Circles != expected.circles {
			t.Fatalf("stage %d Circles = %d, want %d", i, stage.Circles, expected.circles)
		}
		if stage.Config.Circles != expected.circles {
			t.Fatalf("stage %d Config.Circles = %d, want %d", i, stage.Config.Circles, expected.circles)
		}
		if stage.Config.EffectiveSeed != 4242 {
			t.Fatalf("stage %d EffectiveSeed = %d, want the campaign seed 4242", i, stage.Config.EffectiveSeed)
		}
	}
	polish := stages[len(stages)-1]
	if !polish.Config.PolishingEnabled || !polish.Config.PolishingOnly {
		t.Fatalf("polish stage config = %+v, want polishing enabled and polishing-only", polish.Config)
	}
	if polish.Config.PolishingMaxSweeps != 4 || polish.Config.PolishingActiveSetSize != 8 {
		t.Fatalf("polish overrides not applied: %+v", polish.Config)
	}
	if stages[1].Config.PolishingEnabled {
		t.Fatalf("extend stage must not enable polishing: %+v", stages[1].Config)
	}
	if stages[1].Config.BatchSize != 8 {
		t.Fatalf("extend stage BatchSize = %d, want the appended-circle count 8", stages[1].Config.BatchSize)
	}
}

// TestScheduleExpandRealizesTheReferenceCampaign pins the campaign the phase
// was written for: base 8, +8 to 512, polish at 32/64/96/128/192/256.
func TestScheduleExpandRealizesTheReferenceCampaign(t *testing.T) {
	doc := documentWithSteps(t, `[
    {"type": "extend", "repeat": 3, "additionalCircles": 8},
    {"type": "polish"},
    {"type": "extend", "repeat": 4, "additionalCircles": 8},
    {"type": "polish"},
    {"type": "extend", "repeat": 4, "additionalCircles": 8},
    {"type": "polish"},
    {"type": "extend", "repeat": 4, "additionalCircles": 8},
    {"type": "polish"},
    {"type": "extend", "repeat": 8, "additionalCircles": 8},
    {"type": "polish"},
    {"type": "extend", "repeat": 8, "additionalCircles": 8},
    {"type": "polish"},
    {"type": "extend", "repeat": 32, "additionalCircles": 8}
  ]`)

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	extends, polishes := 0, 0
	var polishCircles []int
	for _, stage := range stages {
		switch stage.Kind {
		case ScheduleStageExtend:
			extends++
		case ScheduleStagePolish:
			polishes++
			polishCircles = append(polishCircles, stage.Circles)
		case ScheduleStageBase:
		}
	}
	if extends != 63 {
		t.Fatalf("extend stages = %d, want 63", extends)
	}
	if polishes != 6 {
		t.Fatalf("polish stages = %d, want 6", polishes)
	}
	wantPolishAt := []int{32, 64, 96, 128, 192, 256}
	for i, circles := range wantPolishAt {
		if polishCircles[i] != circles {
			t.Fatalf("polish %d ran at %d circles, want %d", i, polishCircles[i], circles)
		}
	}
	last := stages[len(stages)-1]
	if last.Circles != 512 {
		t.Fatalf("final stage circles = %d, want 512", last.Circles)
	}
}

func TestScheduleExpandIsDeterministicForAFixedSeed(t *testing.T) {
	doc := documentWithSteps(t, `[{"type": "extend", "repeat": 2, "additionalCircles": 8}]`)
	first, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	second, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("stage counts differ: %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Config != second[i].Config {
			t.Fatalf("stage %d config is not stable across expansions", i)
		}
	}
}

func TestScheduleCampaignSeedResolvesWhenOmitted(t *testing.T) {
	source := strings.Replace(baseDocument, `"seed": 4242,`, "", 1)
	doc, err := ParseSchedule([]byte(source))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}
	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if stages[0].Config.EffectiveSeed == 0 {
		t.Fatal("Expand() left the effective seed unresolved")
	}
}
