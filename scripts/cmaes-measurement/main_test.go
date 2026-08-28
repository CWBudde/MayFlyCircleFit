package main

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCampaignArmsAreEvaluationMatched(t *testing.T) {
	t.Parallel()

	arms, err := campaignArms(defaultBudget)
	if err != nil {
		t.Fatal(err)
	}

	if len(arms) != 5 {
		t.Fatalf("arms = %d, want 5", len(arms))
	}

	for _, current := range arms[2:] {
		if got := current.iters * defaultPop; got != defaultBudget {
			t.Errorf("%s budget = %d, want %d", current.name, got, defaultBudget)
		}
	}
}

func TestPairedImprovement(t *testing.T) {
	t.Parallel()

	control := []resultRow{
		{manifestRow: manifestRow{Block: 1}, Score: 10},
		{manifestRow: manifestRow{Block: 2}, Score: 12},
		{manifestRow: manifestRow{Block: 3}, Score: 14},
	}
	candidate := []resultRow{
		{manifestRow: manifestRow{Block: 1}, Score: 9},
		{manifestRow: manifestRow{Block: 2}, Score: 10},
		{manifestRow: manifestRow{Block: 3}, Score: 11},
	}

	mean, statistic, wins := pairedImprovement(control, candidate)
	if mean != 2 || wins != 3 || math.Abs(statistic-3.464101615137754) > 1e-12 {
		t.Fatalf("pairedImprovement() = (%v, %v, %d)", mean, statistic, wins)
	}
}

func TestStudentTTwoSided(t *testing.T) {
	t.Parallel()

	cases := []struct {
		statistic float64
		degrees   int
		want      float64
	}{
		{statistic: 0, degrees: 11, want: 1},
		{statistic: 2.201096, degrees: 11, want: 0.05},
		{statistic: 2.42, degrees: 11, want: 0.034007},
		{statistic: 5.04, degrees: 11, want: 0.000378},
		{statistic: -5.04, degrees: 11, want: 0.000378},
		{statistic: 12.7062, degrees: 1, want: 0.05},
	}
	for _, current := range cases {
		got := studentTTwoSided(current.statistic, current.degrees)
		if math.Abs(got-current.want) > 1e-5 {
			t.Errorf("studentTTwoSided(%v, %d) = %v, want %v",
				current.statistic, current.degrees, got, current.want)
		}
	}

	if got := studentTTwoSided(math.Inf(1), 11); got != 0 {
		t.Errorf("studentTTwoSided(+Inf, 11) = %v, want 0", got)
	}
}

func TestStudentTCritical(t *testing.T) {
	t.Parallel()

	cases := []struct {
		alpha   float64
		degrees int
		want    float64
	}{
		{alpha: 0.05, degrees: 11, want: 2.200985},
		{alpha: 0.05 / 7, degrees: 11, want: 3.294859},
	}
	for _, current := range cases {
		got := studentTCritical(current.alpha, current.degrees)
		if math.Abs(got-current.want) > 1e-5 {
			t.Errorf("studentTCritical(%v, %d) = %v, want %v",
				current.alpha, current.degrees, got, current.want)
		}
	}
}

func TestHolmRejectStopsAtTheFirstRetainedContrast(t *testing.T) {
	t.Parallel()

	// The campaign's seven p-values: the three smallest clear their step-down
	// thresholds and every larger one retains, including 0.03385, which would
	// have cleared an uncorrected 0.05.
	contrasts := []contrast{
		{candidate: "mayfly-r16", pValue: 0.03385},
		{candidate: "cmaes-single", pValue: 0.06960},
		{candidate: "cmaes-ipop", pValue: 0.00716},
		{candidate: "sep-cmaes-ipop", pValue: 0.00038},
		{candidate: "cmaes-single-r16", pValue: 0.85909},
		{candidate: "cmaes-ipop-r16", pValue: 0.03546},
		{candidate: "sep-cmaes-ipop-r16", pValue: 0.00049},
	}
	holmReject(contrasts, 0.05)

	want := map[string]bool{
		"mayfly-r16": false, "cmaes-single": false, "cmaes-ipop": true,
		"sep-cmaes-ipop": true, "cmaes-single-r16": false,
		"cmaes-ipop-r16": false, "sep-cmaes-ipop-r16": true,
	}
	for _, current := range contrasts {
		if current.rejected != want[current.candidate] {
			t.Errorf("holmReject() %s rejected = %v, want %v",
				current.candidate, current.rejected, want[current.candidate])
		}
	}
}

func TestCollectPreliminaryUsesPersistedJobsOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.csv")
	resultsPath := filepath.Join(root, "results.csv")
	trajectoryPath := filepath.Join(root, "trajectory.csv")

	jobDir := filepath.Join(root, "projects", "measurement", "jobs", "persisted")

	err := os.MkdirAll(jobDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, manifestPath, "arm,block,seed,jobId\ncmaes-ipop,1,1001,persisted\nmayfly-single,2,1002,missing\n")
	writeFixture(t, filepath.Join(jobDir, "checkpoint-info.json"), `{
  "termination":"unknown","optimizerVersion":"test-version","iteration":2,"evaluations":20
}`)
	writeFixture(t, filepath.Join(jobDir, "checkpoint.json"), `{"optimizerVersion":"test-version"}`)

	trace := `{"optimizerDiagnostics":{"sigma":0.3,"conditionNumber":1.2},` +
		`"iteration":1,"cost":12,"evaluations":10,"timestamp":"2026-08-25T10:00:00Z"}` + "\n" +
		`{"optimizerDiagnostics":{"sigma":0.2,"conditionNumber":2.4},` +
		`"iteration":2,"cost":9,"evaluations":20,"timestamp":"2026-08-25T10:00:03Z"}` + "\n"
	writeFixture(t, filepath.Join(jobDir, "trace.jsonl"), trace)

	err = collectPreliminary(settings{
		dataRoot: root, project: "measurement", manifestPath: manifestPath,
		resultsPath: resultsPath, trajectory: trajectoryPath, budget: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := readCSVFixture(t, resultsPath)
	if len(results) != 2 {
		t.Fatalf("unexpected preliminary results: %#v", results)
	}

	for index, want := range map[int]string{4: "interrupted", 6: "test-version", 7: "9", 11: "3.000000"} {
		if results[1][index] != want {
			t.Fatalf("results[%d] = %q, want %q", index, results[1][index], want)
		}
	}

	trajectory := readCSVFixture(t, trajectoryPath)
	if len(trajectory) != 3 || trajectory[1][6] != "" || trajectory[1][7] != "0.29999999999999999" {
		t.Fatalf("unexpected preliminary trajectory: %#v", trajectory)
	}
}

func writeFixture(t *testing.T, path, contents string) {
	t.Helper()

	err := os.WriteFile(path, []byte(contents), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func readCSVFixture(t *testing.T, path string) [][]string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	return records
}

func TestLambdaScreenArmsAreEvaluationMatched(t *testing.T) {
	t.Parallel()

	arms, err := lambdaScreenArms(defaultBudget)
	if err != nil {
		t.Fatal(err)
	}

	// Two covariance modes crossed with one no-restart shape plus one IPOP
	// shape per lambda level.
	if want := 2 * (1 + len(lambdaLevels())); len(arms) != want {
		t.Fatalf("arms = %d, want %d", len(arms), want)
	}

	seen := make(map[string]bool, len(arms))
	for _, current := range arms {
		if seen[current.name] {
			t.Errorf("duplicate arm name %q", current.name)
		}

		seen[current.name] = true

		if current.optimizer != "cmaes" {
			t.Errorf("%s optimizer = %q, want cmaes", current.name, current.optimizer)
		}
		// The whole screen rests on every arm spending the same number of
		// evaluations; an arm that does not is not comparable to the rest.
		if got := current.iters * current.popSize; got != defaultBudget {
			t.Errorf("%s budget = %d (%d iters x lambda %d), want %d",
				current.name, got, current.iters, current.popSize, defaultBudget)
		}
	}

	// Three cells have to repeat Phase 21 exactly, because the report reads a
	// difference against those rows as cross-campaign drift rather than noise.
	for _, name := range []string{"cmaes-single", "cmaes-ipop", "sep-cmaes-ipop"} {
		if !seen[name] {
			t.Errorf("lambda screen is missing the Phase 21 replication arm %q", name)
		}
	}
	// And this is the cell Phase 21 never ran (Task 23.2).
	if !seen["sep-cmaes-single"] {
		t.Error("lambda screen is missing sep-cmaes-single")
	}
}

func TestLambdaScreenRejectsAnIndivisibleBudget(t *testing.T) {
	t.Parallel()

	// 20 does not divide this budget, so the lambda-20 arms could not be
	// evaluation-matched and the screen must refuse rather than round.
	_, err := lambdaScreenArms(defaultPop * 3)
	if err == nil {
		t.Fatal("lambdaScreenArms accepted a budget no lambda level divides")
	}
}

func TestCampaignDesignsAreNamedAndClosed(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"phase21", "lambda"} {
		plan, err := campaignDesign(name, defaultBudget)
		if err != nil {
			t.Fatalf("design %s: %v", name, err)
		}

		names := make(map[string]bool, len(plan.arms))
		for _, current := range plan.arms {
			names[current.name] = true
		}
		// Both controls have to be arms the design actually runs, or analyze
		// would silently compare against an empty slice.
		if !names[plan.baseline] {
			t.Errorf("design %s baseline %q is not one of its arms", name, plan.baseline)
		}

		if !names[plan.secondaryControl] {
			t.Errorf("design %s secondary control %q is not one of its arms", name, plan.secondaryControl)
		}
	}

	_, unregistered := campaignDesign("bogus", defaultBudget)
	if unregistered == nil {
		t.Fatal("campaignDesign accepted an unregistered design")
	}
}

func TestPhase21ArmsAllUseTheDefaultPopulation(t *testing.T) {
	t.Parallel()

	// The registered Phase 21 design predates per-arm populations; pinning this
	// keeps the refactor from quietly changing what that campaign submits.
	arms, err := campaignArms(defaultBudget)
	if err != nil {
		t.Fatal(err)
	}

	for _, current := range arms {
		if current.popSize != defaultPop {
			t.Errorf("%s popSize = %d, want %d", current.name, current.popSize, defaultPop)
		}
	}
}
