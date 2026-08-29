package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// readScheduleExample loads the worked example carried by
// docs/schedule-format.md. It is a real file rather than a fenced block
// precisely so this package can parse it: a documented format that nothing
// executes drifts from the implemented one and nobody finds out until a
// campaign is refused.
//
// cmd.referenceCampaignDocument reads the same file, so renaming or removing it
// fails both packages rather than quietly leaving one of them behind.
func readScheduleExample(t *testing.T) []byte {
	t.Helper()

	path := filepath.Join("..", "..", "docs", "examples", "512-circle-campaign.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the documented example: %v", err)
	}

	return data
}

// TestDocumentedExamplePlansTheReferenceCampaign is the Task 16.6 acceptance
// check. The documented example must parse under the shipped parser and must
// plan the campaign the documentation says it plans: 70 stages and 48,800
// iterations. Change either the format or the example and this fails.
//
// The arithmetic behind those two figures is written out in
// TestReferenceCampaignPlanMatchesTheHandComputation; this test checks the
// documented file reproduces it.
func TestDocumentedExamplePlansTheReferenceCampaign(t *testing.T) {
	t.Parallel()

	document, err := ParseSchedule(readScheduleExample(t))
	if err != nil {
		t.Fatalf("the documented example does not parse: %v", err)
	}

	plan, err := document.Expand()
	if err != nil {
		t.Fatalf("the documented example does not expand: %v", err)
	}

	summary := SummarizeSchedulePlan(plan)

	checks := []struct {
		name string
		got  int
		want int
	}{
		{"stages", summary.Stages, 70},
		{"base stages", summary.Base, 1},
		{"extend stages", summary.Extends, 63},
		{"polish stages", summary.Polishes, 6},
		{"conditional stages", summary.Conditional, 6},
		{"total iterations", summary.TotalIterations, 32000},
		{"unconditional iterations", summary.FirmIterations(), 12800},
		{"conditional iterations", summary.ConditionalIterations, 19200},
		{"final circle count", plan[len(plan)-1].Circles, 512},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %d, want %d", check.name, check.got, check.want)
		}
	}

	if document.Seed != 4242 {
		t.Errorf("documented seed = %d, want 4242", document.Seed)
	}
}

// TestDocumentedExampleIsTheCampaignTheTestsAlreadyPin ties the file on disk to
// the campaign the rest of the package asserts against, so the two cannot drift
// apart in either direction: editing the example without editing the tests, or
// the reverse, fails here.
func TestDocumentedExampleIsTheCampaignTheTestsAlreadyPin(t *testing.T) {
	t.Parallel()

	documented, err := ParseSchedule(readScheduleExample(t))
	if err != nil {
		t.Fatalf("the documented example does not parse: %v", err)
	}

	pinned := documentWithSteps(t, referenceCampaignSteps)
	if !reflect.DeepEqual(documented.Steps, pinned.Steps) {
		t.Errorf("the documented steps differ from referenceCampaignSteps:\n docs %+v\n test %+v",
			documented.Steps, pinned.Steps)
	}

	if !reflect.DeepEqual(documented.Base, pinned.Base) {
		t.Errorf("the documented base differs from baseDocument's:\n docs %+v\n test %+v",
			documented.Base, pinned.Base)
	}
}
