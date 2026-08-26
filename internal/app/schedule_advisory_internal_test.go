package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// advisoryDocument builds a document from the shared fixture, optionally
// raising the base population before the steps are substituted, so a case can
// state only the part it is testing.
func advisoryDocument(t *testing.T, basePopSize int, steps string) *ScheduleDocument {
	t.Helper()

	if basePopSize == 0 {
		return documentWithSteps(t, steps)
	}

	source := strings.Replace(baseDocument, `"popSize": 30`, fmt.Sprintf(`"popSize": %d`, basePopSize), 1)
	source = strings.Replace(source, `"steps": []`, `"steps": `+steps, 1)

	doc, err := ParseSchedule([]byte(source))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	return doc
}

// advisoryDocumentUnder builds the same fixture with the base population raised
// and an engine named on the base stage, so a case can state only the engine it
// is testing. An empty engine leaves the field out of the document entirely,
// which is the shape most documents have and the one that must still warn.
func advisoryDocumentUnder(t *testing.T, optimizer string, basePopSize int, steps string) *ScheduleDocument {
	t.Helper()

	population := fmt.Sprintf(`"popSize": %d`, basePopSize)
	if optimizer != "" {
		population += fmt.Sprintf(`, "optimizer": %q`, optimizer)
	}

	source := strings.Replace(baseDocument, `"popSize": 30`, population, 1)
	source = strings.Replace(source, `"steps": []`, `"steps": `+steps, 1)

	doc, err := ParseSchedule([]byte(source))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	return doc
}

// The two authoring sites the cases below assert against. A base population is
// named at the document's own popSize, while a step that overrides it is named
// at the step that wrote it — see authoringSite for why an inherited population
// never points at the step.
const (
	baseSite  = "base.popSize"
	firstStep = "steps[0].popSize"
)

// wantAdvisory is the part of an advisory a case pins: which authoring site it
// names and how many realized stages it stands for. The prose is asserted once,
// in TestScheduleAdvisoryNamesTheMeasurement, rather than in every row.
type wantAdvisory struct {
	field  string
	stages int
}

func TestScheduleDocumentAdvisories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		basePopSize int
		steps       string
		want        []wantAdvisory
	}{
		{
			name:        "base population raised at one epoch",
			basePopSize: 100,
			steps:       `[]`,
			want:        []wantAdvisory{{field: baseSite, stages: 1}},
		},
		{
			name:        "base population carries onto every extend that overrides nothing",
			basePopSize: 100,
			steps:       `[{"type": "extend", "additionalCircles": 8, "repeat": 3}]`,
			want:        []wantAdvisory{{field: baseSite, stages: 4}},
		},
		{
			name:  "extend override raised at one epoch",
			steps: `[{"type": "extend", "additionalCircles": 8, "popSize": 100}]`,
			want:  []wantAdvisory{{field: firstStep, stages: 1}},
		},
		{
			name:  "polish override raised at one epoch",
			steps: `[{"type": "polish", "popSize": 100, "epochs": 1}]`,
			want:  []wantAdvisory{{field: "steps[0].polishingPopSize", stages: 1}},
		},
		{
			name:  "repeated extend collapses into one advisory",
			steps: `[{"type": "extend", "additionalCircles": 8, "popSize": 100, "repeat": 4}]`,
			want:  []wantAdvisory{{field: firstStep, stages: 4}},
		},
		{
			name: "population raised on some steps only",
			steps: `[{"type": "extend", "additionalCircles": 8, "popSize": 100, "repeat": 2},` +
				`{"type": "extend", "additionalCircles": 8, "repeat": 3}]`,
			want: []wantAdvisory{{field: firstStep, stages: 2}},
		},
		{
			name:        "base and polish raised report in stage order",
			basePopSize: 100,
			steps:       `[{"type": "polish", "popSize": 100, "epochs": 1}]`,
			want: []wantAdvisory{
				{field: baseSite, stages: 1},
				{field: "steps[0].polishingPopSize", stages: 1},
			},
		},
		{
			name:  "population and epochs raised together",
			steps: `[{"type": "extend", "additionalCircles": 8, "popSize": 100, "epochs": 3}]`,
			want:  nil,
		},
		{
			name:  "polish population raised at the default epochs",
			steps: `[{"type": "polish", "popSize": 100}]`,
			want:  nil,
		},
		{
			name:  "default population",
			steps: `[{"type": "extend", "additionalCircles": 8, "repeat": 3}]`,
			want:  nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			doc := advisoryDocument(t, test.basePopSize, test.steps)

			advisories := doc.Advisories()
			if len(advisories) != len(test.want) {
				t.Fatalf("Advisories() = %v, want %d advisories", advisories, len(test.want))
			}

			for i, want := range test.want {
				if advisories[i].Field != want.field {
					t.Fatalf("Advisories()[%d].Field = %q, want %q", i, advisories[i].Field, want.field)
				}

				if advisories[i].Stages != want.stages {
					t.Fatalf("Advisories()[%d].Stages = %d, want %d", i, advisories[i].Stages, want.stages)
				}

				if advisories[i].Message == "" {
					t.Fatalf("Advisories()[%d].Message is empty", i)
				}
			}
		})
	}
}

// TestScheduleAdvisoryNamesTheMeasurement pins the prose, because an advisory
// that does not carry its evidence is an opinion.
func TestScheduleAdvisoryNamesTheMeasurement(t *testing.T) {
	t.Parallel()

	doc := advisoryDocument(t, 0,
		`[{"type": "extend", "additionalCircles": 8, "popSize": 100, "repeat": 63}]`)

	advisories := doc.Advisories()
	if len(advisories) != 1 {
		t.Fatalf("Advisories() = %v, want exactly one advisory", advisories)
	}

	advisory := advisories[0]
	if advisory.Stages != 63 {
		t.Fatalf("Advisories()[0].Stages = %d, want 63", advisory.Stages)
	}

	for _, want := range []string{
		"steps[0].popSize 100",
		"optimizerEpochs 1",
		"on 63 stages",
		"0.026",
		"2.2x",
		"1.94",
		"docs/schedule-format.md",
	} {
		if !strings.Contains(advisory.Message, want) {
			t.Fatalf("Advisories()[0].Message = %q, want it to name %q", advisory.Message, want)
		}
	}

	if advisory.String() != advisory.Message {
		t.Fatalf("String() = %q, want the message %q", advisory.String(), advisory.Message)
	}
}

// TestScheduleAdvisorySaysWhoseMeasurementItIs guards the one claim the data
// does not support: the figures were taken on extend stages, so the polishing
// note has to borrow them out loud.
func TestScheduleAdvisorySaysWhoseMeasurementItIs(t *testing.T) {
	t.Parallel()

	doc := advisoryDocument(t, 0, `[{"type": "polish", "popSize": 100, "epochs": 1}]`)

	advisories := doc.Advisories()
	if len(advisories) != 1 {
		t.Fatalf("Advisories() = %v, want exactly one advisory", advisories)
	}

	message := advisories[0].Message
	if !strings.Contains(message, "polishingEpochs 1") {
		t.Fatalf("Advisories()[0].Message = %q, want it to name polishingEpochs", message)
	}

	if !strings.Contains(message, "on extend stages, not on polishing sweeps:") {
		t.Fatalf("Advisories()[0].Message = %q, want it to say the figure was measured on extends", message)
	}
}

// TestScheduleAdvisoriesSingularStageCount keeps the note readable at the size
// most documents hit: one stage.
func TestScheduleAdvisoriesSingularStageCount(t *testing.T) {
	t.Parallel()

	doc := advisoryDocument(t, 100, `[]`)

	advisories := doc.Advisories()
	if len(advisories) != 1 {
		t.Fatalf("Advisories() = %v, want exactly one advisory", advisories)
	}

	if !strings.Contains(advisories[0].Message, "on 1 stage:") {
		t.Fatalf("Advisories()[0].Message = %q, want it to read \"on 1 stage\"", advisories[0].Message)
	}
}

// TestScheduleAdvisoriesAreDeterministic pins the ordering contract: a document
// setting more than one always reports the same one first.
func TestScheduleAdvisoriesAreDeterministic(t *testing.T) {
	t.Parallel()

	doc := advisoryDocument(t, 100,
		`[{"type": "extend", "additionalCircles": 8, "popSize": 200, "repeat": 2},`+
			`{"type": "polish", "popSize": 100, "epochs": 1}]`)

	first := doc.Advisories()
	if len(first) != 3 {
		t.Fatalf("Advisories() = %v, want three advisories", first)
	}

	wantFields := []string{baseSite, firstStep, "steps[1].polishingPopSize"}
	for i, want := range wantFields {
		if first[i].Field != want {
			t.Fatalf("Advisories()[%d].Field = %q, want %q", i, first[i].Field, want)
		}
	}

	second := doc.Advisories()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Advisories() = %v on the second call, want %v", second, first)
	}
}

// TestScheduleAdvisoriesOnBaseAndExtendAreMayflyOnly keeps the note where its
// evidence is. popSize is shared by all three engines: it reaches CMA-ES as
// lambda and Dragonfly as NPop, and a population above the default is an
// ordinary setting there rather than the measured waste the note describes.
func TestScheduleAdvisoriesOnBaseAndExtendAreMayflyOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		optimizer string
		want      []wantAdvisory
	}{
		{
			name:      "omitted engine still warns",
			optimizer: "",
			want:      []wantAdvisory{{field: baseSite, stages: 2}},
		},
		{
			name:      "explicit mayfly warns",
			optimizer: "mayfly",
			want:      []wantAdvisory{{field: baseSite, stages: 2}},
		},
		{
			name:      "cmaes is silent",
			optimizer: "cmaes",
			want:      nil,
		},
		{
			name:      "dragonfly is silent",
			optimizer: "dragonfly",
			want:      nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			doc := advisoryDocumentUnder(t, test.optimizer, 100,
				`[{"type": "extend", "additionalCircles": 8}]`)

			advisories := doc.Advisories()
			if len(advisories) != len(test.want) {
				t.Fatalf("Advisories() = %v, want %d advisories", advisories, len(test.want))
			}

			for i, want := range test.want {
				if advisories[i].Field != want.field || advisories[i].Stages != want.stages {
					t.Fatalf("Advisories()[%d] = %+v, want %v", i, advisories[i], want)
				}
			}
		})
	}
}

// TestScheduleAdvisoriesOnAnExtendOverrideAreMayflyOnly covers the other
// authoring site: a population written on the step rather than inherited from
// the base is gated on the same engine.
func TestScheduleAdvisoriesOnAnExtendOverrideAreMayflyOnly(t *testing.T) {
	t.Parallel()

	steps := `[{"type": "extend", "additionalCircles": 8, "popSize": 100}]`

	mayfly := advisoryDocumentUnder(t, "mayfly", 30, steps)
	if advisories := mayfly.Advisories(); len(advisories) != 1 ||
		advisories[0].Field != firstStep {
		t.Fatalf("Advisories() = %v, want one note on steps[0].popSize", advisories)
	}

	for _, optimizer := range []string{"cmaes", "dragonfly"} {
		doc := advisoryDocumentUnder(t, optimizer, 30, steps)
		if advisories := doc.Advisories(); advisories != nil {
			t.Fatalf("Advisories() = %v under %q, want none", advisories, optimizer)
		}
	}
}

// TestPolishBudgetIsAdvisedWhateverEngineIsNamed pins the asymmetry the gate
// deliberately leaves in place: polishing runs its own MayFly population
// regardless of the document's engine, so the polish branch carries no gate.
//
// It calls wastefulBudget rather than Advisories because no document can reach
// that combination — a schedule naming another engine and asking for a polish
// step is refused at parse time, which TestScheduleRefusesPolishingUnderCMAES
// covers. The branch still has to stay ungated, because that refusal lives in
// another file and a later reader must not "fix" the asymmetry here.
func TestPolishBudgetIsAdvisedWhateverEngineIsNamed(t *testing.T) {
	t.Parallel()

	defaults := DefaultConfig()

	var doc ScheduleDocument

	for _, optimizer := range []Optimizer{"", OptimizerMayfly, OptimizerCMAES, OptimizerDragonfly} {
		stage := ScheduleStage{
			Kind:      ScheduleStagePolish,
			StepIndex: -1,
			Config: JobConfig{
				Optimizer:        optimizer,
				PolishingPopSize: defaults.PolishingPopSize + 70,
				PolishingEpochs:  1,
			},
		}

		budget, wasteful := doc.wastefulBudget(stage, defaults)
		if !wasteful {
			t.Fatalf("wastefulBudget() reported no waste under optimizer %q", optimizer)
		}

		if budget.field != "base.polishingPopSize" || !budget.polishing {
			t.Fatalf("wastefulBudget() = %+v under optimizer %q, want a polishing note", budget, optimizer)
		}
	}
}

// TestScheduleAdvisoriesIgnoreADocumentThatDoesNotExpand keeps the query out of
// the error-reporting business: the caller already has the expansion error.
func TestScheduleAdvisoriesIgnoreADocumentThatDoesNotExpand(t *testing.T) {
	t.Parallel()

	doc := ScheduleDocument{
		SchemaVersion: ScheduleSchemaVersion,
		Seed:          4242,
		Base: JobConfig{
			RefPath: "assets/ref.png",
			Mode:    ModeBatch,
			Circles: MaxCircles + 1,
			PopSize: 100,
		},
	}

	_, err := doc.Expand()
	if err == nil {
		t.Fatalf("Expand() error = nil, want the fixture not to expand")
	}

	if advisories := doc.Advisories(); advisories != nil {
		t.Fatalf("Advisories() = %v, want none for a document that does not expand", advisories)
	}
}
