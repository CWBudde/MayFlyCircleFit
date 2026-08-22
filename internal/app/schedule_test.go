package app

import (
	"errors"
	"fmt"
	"reflect"
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
			name:     "base writes the derived effective seed",
			document: strings.Replace(baseDocument, `"popSize": 30`, `"popSize": 30, "effectiveSeed": 7`, 1),
			wantErr:  "effectiveSeed",
		},
		{
			name:     "base writes the runtime resume count",
			document: strings.Replace(baseDocument, `"popSize": 30`, `"popSize": 30, "resumeCount": 2`, 1),
			wantErr:  "resumeCount",
		},
		{
			name:     "base is polishing only",
			document: strings.Replace(baseDocument, `"popSize": 30`, `"popSize": 30, "polishingOnly": true`, 1),
			wantErr:  "polishingOnly",
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

func TestParseScheduleRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name     string
		document string
		wantErr  string
	}{
		{name: "not json", document: `not json`, wantErr: "decode schedule"},
		{name: "two objects", document: baseDocument + baseDocument, wantErr: "one JSON object"},
		{name: "no base", document: `{"schemaVersion": 1}`, wantErr: "refPath"},
		{
			name:     "budget override the optimizer refuses",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "extend", "additionalCircles": 8, "popSize": 5}]`, 1),
			wantErr:  "popSize",
		},
		{
			name:     "polish batch size",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "polish", "batchSize": 4}]`, 1),
			wantErr:  "batchSize",
		},
		{
			name:     "extend carrying a polish override",
			document: strings.Replace(baseDocument, `"steps": []`, `"steps": [{"type": "extend", "additionalCircles": 8, "maxSweeps": 2}]`, 1),
			wantErr:  "maxSweeps",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSchedule([]byte(test.document))
			if err == nil {
				t.Fatalf("ParseSchedule() error = nil, want one mentioning %q", test.wantErr)
			}

			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ParseSchedule() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

// TestScheduleBaseCarriesThePolishingPopulation pins the JSON name a document
// writes the polishing population under, and that a written value survives the
// defaults it would otherwise be replaced by.
func TestScheduleBaseCarriesThePolishingPopulation(t *testing.T) {
	source := strings.Replace(baseDocument, `"popSize": 30`, `"popSize": 30,
    "polishingPopSize": 50`, 1)

	doc, err := ParseSchedule([]byte(source))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if got := stages[0].Config.PolishingPopSize; got != 50 {
		t.Fatalf("base polishingPopSize = %d, want 50", got)
	}
}

// TestScheduleStepBudgetOverrides pins that a step's budget lands on the field
// the matching HTTP request would have set, polish included.
func TestScheduleStepBudgetOverrides(t *testing.T) {
	doc := documentWithSteps(t, `[
    {"type": "extend", "additionalCircles": 8, "batchSize": 4, "epochs": 3, "iters": 500, "popSize": 40},
    {"type": "polish", "strategy": "contiguous-window", "epochs": 2, "iters": 900, "stagnationIters": 100, "minImprovement": 0.5, "popSize": 60}
  ]`)

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	extend := stages[1].Config
	if extend.BatchSize != 4 || extend.OptimizerEpochs != 3 || extend.Iters != 500 || extend.PopSize != 40 {
		t.Fatalf("extend overrides = %+v", extend)
	}

	polish := stages[2].Config
	if polish.PolishingStrategy != PolishingContiguousWindow || polish.PolishingEpochs != 2 || polish.PolishingIters != 900 ||
		polish.PolishingStagnationIters != 100 || polish.PolishingMinImprovement != 0.5 || polish.PolishingPopSize != 60 {
		t.Fatalf("polish overrides = %+v", polish)
	}
	// A polish step's popSize is its polishing population, exactly as popSize on
	// a /polish request is. It does not touch the job-wide population, which a
	// polish-only stage never spends.
	if polish.PopSize != 30 {
		t.Fatalf("polish stage popSize = %d, want the base document's 30 untouched", polish.PopSize)
	}
	// Overrides are per step: each stage derives from the base configuration, so
	// the extend step's budget does not leak into the polish that follows it.
	if polish.Iters != 200 || polish.OptimizerEpochs != 1 || polish.BatchSize != 8 {
		t.Fatalf("polish stage inherited the previous step's budget: %+v", polish)
	}
}

func TestScheduleStepRepetitions(t *testing.T) {
	tests := []struct {
		repeat int
		want   int
	}{{0, 1}, {1, 1}, {63, 63}}
	for _, test := range tests {
		if got := (ScheduleStep{Repeat: test.repeat}).Repetitions(); got != test.want {
			t.Fatalf("Repetitions() for repeat %d = %d, want %d", test.repeat, got, test.want)
		}
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
		// DeepEqual rather than ==: a configuration carries a hand-authored
		// circle list, so it is no longer a comparable struct.
		if !reflect.DeepEqual(first[i].Config, second[i].Config) {
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
	// The resolved seed belongs to the document, not to one expansion: a
	// campaign that omitted its seed must still replay after being written out
	// and read back.
	if doc.Seed != stages[0].Config.EffectiveSeed {
		t.Fatalf("document seed = %d, want the resolved stage seed %d", doc.Seed, stages[0].Config.EffectiveSeed)
	}

	again, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if again[0].Config.EffectiveSeed != stages[0].Config.EffectiveSeed {
		t.Fatalf("second expansion used seed %d, want %d", again[0].Config.EffectiveSeed, stages[0].Config.EffectiveSeed)
	}
}

// TestScheduleDocumentValidateWithoutTheParser covers the documents the parser
// never sees: reconstructed in code, or read back from the store.
func TestScheduleDocumentValidateWithoutTheParser(t *testing.T) {
	valid := *documentWithSteps(t, `[{"type": "extend", "additionalCircles": 8}]`)
	err := valid.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}

	tests := []struct {
		name    string
		mutate  func(*ScheduleDocument)
		wantErr string
	}{
		{
			name:    "unsupported embedded schema version",
			mutate:  func(d *ScheduleDocument) { d.SchemaVersion = ScheduleSchemaVersion + 1 },
			wantErr: "schemaVersion",
		},
		{
			name:    "unknown step type",
			mutate:  func(d *ScheduleDocument) { d.Steps = []ScheduleStep{{Type: "shrink"}} },
			wantErr: "type",
		},
		{
			name:    "polishing-only base",
			mutate:  func(d *ScheduleDocument) { d.Base.PolishingOnly = true },
			wantErr: "polishingOnly",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := valid
			document.Steps = append([]ScheduleStep(nil), valid.Steps...)
			test.mutate(&document)

			err := document.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want one mentioning %q", test.wantErr)
			}

			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

// TestScheduleExpandIgnoresAnAuthoredEffectiveSeed guards the campaign seed
// against a document assembled in code, where the parser's refusal of
// base.effectiveSeed cannot help.
func TestScheduleExpandIgnoresAnAuthoredEffectiveSeed(t *testing.T) {
	doc := *documentWithSteps(t, `[{"type": "extend", "additionalCircles": 8}]`)
	doc.Base.EffectiveSeed = 7

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	for i, stage := range stages {
		if stage.Config.EffectiveSeed != doc.Seed {
			t.Fatalf("stage %d ran with seed %d, want the campaign seed %d", i, stage.Config.EffectiveSeed, doc.Seed)
		}
	}
}

// TestScheduleExpandKeepsAuthoredCirclesOnTheBaseStageOnly covers the guard
// that makes initialCircles safe inside a campaign. Every stage is expanded
// from the same base configuration, so without the explicit clear an extend
// stage would carry eight authored circles while asking for twelve — which
// fails validation — and a polish stage would carry an arrangement its parent
// has already moved past.
func TestScheduleExpandKeepsAuthoredCirclesOnTheBaseStageOnly(t *testing.T) {
	source := strings.Replace(baseDocument, `"popSize": 30`, `"popSize": 30,
    "initialCircles": [
      {"x": 1, "y": 2, "r": 3, "color": "#010203"},
      {"x": 4, "y": 5, "r": 6, "color": "#040506"},
      {"x": 7, "y": 8, "r": 9, "color": "#070809"},
      {"x": 10, "y": 11, "r": 12, "color": "#0a0b0c"},
      {"x": 13, "y": 14, "r": 15, "color": "#0d0e0f"},
      {"x": 16, "y": 17, "r": 18, "color": "#101112"},
      {"x": 19, "y": 20, "r": 21, "color": "#131415"},
      {"x": 22, "y": 23, "r": 24, "color": "#161718"}
    ]`, 1)
	source = strings.Replace(source, `"steps": []`,
		`"steps": [{"type": "polish"}, {"type": "extend", "additionalCircles": 4}, {"type": "polish"}]`, 1)

	doc, err := ParseSchedule([]byte(source))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if len(stages) != 4 {
		t.Fatalf("stage count = %d, want 4", len(stages))
	}

	if len(stages[0].Config.InitialCircles) != 8 {
		t.Fatalf("base stage carries %d authored circles, want 8", len(stages[0].Config.InitialCircles))
	}

	for _, stage := range stages[1:] {
		if len(stage.Config.InitialCircles) != 0 {
			t.Fatalf("stage %d (%s) carries %d authored circles, want none",
				stage.Index, stage.Kind, len(stage.Config.InitialCircles))
		}
	}

	if stages[2].Circles != 12 {
		t.Fatalf("extend stage circles = %d, want 12", stages[2].Circles)
	}
}

// TestScheduleExpandCarriesABarrierOntoItsFirstStageOnly pins the two things a
// barrier promises: it marks the stage the campaign stops before, and on a
// repeated step it marks one stage rather than every repetition.
func TestScheduleExpandCarriesABarrierOntoItsFirstStageOnly(t *testing.T) {
	doc := documentWithSteps(t, `[
		{"type": "polish"},
		{"type": "extend", "additionalCircles": 4, "pauseBefore": true, "repeat": 3}
	]`)

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if len(stages) != 5 {
		t.Fatalf("stage count = %d, want 5", len(stages))
	}

	barriers := []int{}

	for _, stage := range stages {
		if stage.PauseBefore {
			barriers = append(barriers, stage.Index)
		}
	}

	if len(barriers) != 1 || barriers[0] != 2 {
		t.Fatalf("barriers at %v, want exactly stage 2", barriers)
	}
	// The barrier stops the campaign; it must not remove the stage or change
	// what the plan grows to, or resuming would produce a different campaign.
	if stages[4].Circles != 20 {
		t.Fatalf("final circles = %d, want 20", stages[4].Circles)
	}
}

// TestScheduleBarrierIsLegalOnEitherKind guards the distinction from `when`,
// which is refused on extend. A barrier skips nothing, so the reason `when` is
// refused there does not apply to it.
func TestScheduleBarrierIsLegalOnEitherKind(t *testing.T) {
	doc := documentWithSteps(t, `[
		{"type": "polish", "pauseBefore": true},
		{"type": "extend", "additionalCircles": 4, "pauseBefore": true}
	]`)

	stages, err := doc.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if !stages[1].PauseBefore || !stages[2].PauseBefore {
		t.Fatalf("barriers = %v/%v, want both", stages[1].PauseBefore, stages[2].PauseBefore)
	}
}

// TestScheduleDocumentIsBounded covers what keeps a campaign inspectable: the
// detail response carries the document in full, so an unbounded document is an
// unbounded response however small the stage listing is. A 600 kB name was
// accepted before this, and put `schedule status` back over the CLI's cap with
// a handful of stages.
func TestScheduleDocumentIsBounded(t *testing.T) {
	base := `"base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "batchSize": 8, "iters": 200, "popSize": 30}`

	t.Run("a name at the limit is accepted", func(t *testing.T) {
		document := fmt.Sprintf(`{"seed": 42, "name": %q, %s}`, strings.Repeat("a", MaxScheduleNameLen), base)
		if _, err := ParseSchedule([]byte(document)); err != nil {
			t.Fatalf("ParseSchedule() error = %v", err)
		}
	})

	t.Run("a longer name is refused", func(t *testing.T) {
		document := fmt.Sprintf(`{"seed": 42, "name": %q, %s}`, strings.Repeat("a", MaxScheduleNameLen+1), base)

		_, err := ParseSchedule([]byte(document))
		if err == nil {
			t.Fatal("ParseSchedule() accepted an unbounded name")
		}

		if !strings.Contains(err.Error(), "name") {
			t.Fatalf("error = %v, want it to name the field", err)
		}
	})

	t.Run("a document over the byte limit is refused", func(t *testing.T) {
		// The padding is a legal value in a legal field, so the refusal is about
		// the size of the document and not about anything malformed in it.
		padding := strings.Repeat(" ", MaxScheduleDocumentBytes)
		document := fmt.Sprintf(`{"seed": 42, %s%s}`, base, padding)

		_, err := ParseSchedule([]byte(document))
		if err == nil {
			t.Fatal("ParseSchedule() accepted a document over the byte limit")
		}

		if !strings.Contains(err.Error(), "bytes") {
			t.Fatalf("error = %v, want it to state the size", err)
		}
	})

	// The bound is only worth having if it leaves room for the stage listing it
	// shares the response with. A stage summary costs about 172 B, measured by
	// server.TestScheduleDetailStaysUnderTheCLIResponseCap.
	t.Run("the bound leaves room for a full stage listing", func(t *testing.T) {
		const measuredBytesPerStage = 172

		listing := MaxScheduleStages * measuredBytesPerStage
		if MaxScheduleDocumentBytes+listing > MaxCLIResponseBytes {
			t.Fatalf("a document at %d bytes plus %d stages at %d bytes is %d, over the %d the CLI decodes",
				MaxScheduleDocumentBytes, MaxScheduleStages, measuredBytesPerStage,
				MaxScheduleDocumentBytes+listing, MaxCLIResponseBytes)
		}
	})
}
