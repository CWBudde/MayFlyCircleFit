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

// wantAdvisory is the part of an advisory a case pins: which authoring site it
// names and how many realized stages it stands for. The prose is asserted once,
// in TestScheduleAdvisoryNamesTheMeasurement, rather than in every row.
type wantAdvisory struct {
	field  string
	stages int
}

func TestScheduleDocumentAdvisories(t *testing.T) {
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
			want:        []wantAdvisory{{field: "base.popSize", stages: 1}},
		},
		{
			name:        "base population carries onto every extend that overrides nothing",
			basePopSize: 100,
			steps:       `[{"type": "extend", "additionalCircles": 8, "repeat": 3}]`,
			want:        []wantAdvisory{{field: "base.popSize", stages: 4}},
		},
		{
			name:  "extend override raised at one epoch",
			steps: `[{"type": "extend", "additionalCircles": 8, "popSize": 100}]`,
			want:  []wantAdvisory{{field: "steps[0].popSize", stages: 1}},
		},
		{
			name:  "polish override raised at one epoch",
			steps: `[{"type": "polish", "popSize": 100, "epochs": 1}]`,
			want:  []wantAdvisory{{field: "steps[0].polishingPopSize", stages: 1}},
		},
		{
			name:  "repeated extend collapses into one advisory",
			steps: `[{"type": "extend", "additionalCircles": 8, "popSize": 100, "repeat": 4}]`,
			want:  []wantAdvisory{{field: "steps[0].popSize", stages: 4}},
		},
		{
			name: "population raised on some steps only",
			steps: `[{"type": "extend", "additionalCircles": 8, "popSize": 100, "repeat": 2},` +
				`{"type": "extend", "additionalCircles": 8, "repeat": 3}]`,
			want: []wantAdvisory{{field: "steps[0].popSize", stages: 2}},
		},
		{
			name:        "base and polish raised report in stage order",
			basePopSize: 100,
			steps:       `[{"type": "polish", "popSize": 100, "epochs": 1}]`,
			want: []wantAdvisory{
				{field: "base.popSize", stages: 1},
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
	doc := advisoryDocument(t, 100,
		`[{"type": "extend", "additionalCircles": 8, "popSize": 200, "repeat": 2},`+
			`{"type": "polish", "popSize": 100, "epochs": 1}]`)

	first := doc.Advisories()
	if len(first) != 3 {
		t.Fatalf("Advisories() = %v, want three advisories", first)
	}

	wantFields := []string{"base.popSize", "steps[0].popSize", "steps[1].polishingPopSize"}
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

// TestScheduleAdvisoriesIgnoreADocumentThatDoesNotExpand keeps the query out of
// the error-reporting business: the caller already has the expansion error.
func TestScheduleAdvisoriesIgnoreADocumentThatDoesNotExpand(t *testing.T) {
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

	if _, err := doc.Expand(); err == nil {
		t.Fatalf("Expand() error = nil, want the fixture not to expand")
	}

	if advisories := doc.Advisories(); advisories != nil {
		t.Fatalf("Advisories() = %v, want none for a document that does not expand", advisories)
	}
}
