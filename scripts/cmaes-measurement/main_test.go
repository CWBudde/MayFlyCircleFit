package main

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/circlefit/internal/opt"
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

	for _, name := range []string{designPhase21, designLambda, designPilot, designStag, designSplit} {
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

		if plan.secondaryControl != "" && !names[plan.secondaryControl] {
			t.Errorf("design %s secondary control %q is not one of its arms", name, plan.secondaryControl)
		}

		if plan.blocks < 1 {
			t.Errorf("design %s registers %d blocks", name, plan.blocks)
		}

		if plan.seedBase < 1 {
			t.Errorf("design %s registers seed base %d", name, plan.seedBase)
		}

		// Every contrast must name arms the design runs, or buildContrasts
		// would take a paired difference against nothing.
		primaries := 0

		for _, current := range plan.contrasts {
			if !names[current.control] || !names[current.candidate] {
				t.Errorf("design %s contrast %s vs %s names an arm it does not run",
					name, current.candidate, current.control)
			}

			if current.primary {
				primaries++
			}
		}

		if primaries > 1 {
			t.Errorf("design %s registers %d primary contrasts, want at most one", name, primaries)
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

// TestWriteRestartsRecordsOneRowPerRun covers the artifact that makes a
// restart arm measurable. The result CSV holds one row per job and its
// termination column is the schedule's budget-exhausted reason, so this file
// is the only place a run's own reason survives.
func TestWriteRestartsRecordsOneRowPerRun(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "restarts.csv")
	results := []resultRow{
		{
			manifestRow: manifestRow{Arm: "cmaes-ipop", Block: 1, Seed: 111001},
			Restarts: []opt.RestartRun{
				{
					Termination: "tol_fun", Regime: "large", Stage: 0, Restart: 0,
					Population: 1024, Iterations: 106, Evaluations: 848, BestCost: 900,
				},
				{
					Termination: "maximum_evaluations", Regime: "large", Stage: 0, Restart: 1,
					Population: 2048, Iterations: 11, Evaluations: 352, BestCost: 950,
				},
			},
		},
		// A non-restart arm contributes no rows at all, which is what keeps
		// the file a record of restart schedules rather than of jobs.
		{manifestRow: manifestRow{Arm: "cmaes-single", Block: 1, Seed: 111001}},
	}

	err := writeRestarts(settings{restartsPath: path}, results)
	if err != nil {
		t.Fatalf("writeRestarts() error = %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 3 {
		t.Fatalf("rows = %d, want a header and two runs", len(rows))
	}

	if rows[1][0] != "cmaes-ipop" || rows[1][4] != "0" || rows[1][10] != "tol_fun" {
		t.Errorf("first run = %v, want the initial run's own reason", rows[1])
	}

	if rows[2][4] != "1" || rows[2][6] != "2048" || rows[2][10] != "maximum_evaluations" {
		t.Errorf("second run = %v, want the doubled population that spent the budget", rows[2])
	}
}

func TestRegisteredDesignsUseDisjointSeedPrefixes(t *testing.T) {
	t.Parallel()

	// Two campaigns that share a seed prefix are not independent, and the
	// overlap would be invisible in the result CSVs. Reserve the ranges here so
	// a new design has to pick a free one.
	used := make(map[int64]string)

	// phase21 and lambda deliberately share seeds: three lambda arms repeat
	// Phase 21 so cross-campaign drift is measured. The exception is recorded
	// as an unordered pair, so it does not depend on the order this test
	// happens to visit the designs in.
	shared := map[[2]string]bool{{designPhase21, designLambda}: true, {designLambda, designPhase21}: true}

	for _, name := range []string{designPhase21, designLambda, designPilot, designStag, designSplit} {
		plan, err := campaignDesign(name, defaultBudget)
		if err != nil {
			t.Fatalf("design %s: %v", name, err)
		}

		for block := 1; block <= plan.blocks; block++ {
			seed := plan.seedBase + int64(block)
			if owner, taken := used[seed]; taken && !shared[[2]string{owner, name}] {
				t.Errorf("designs %s and %s both use seed %d", owner, name, seed)
			}

			used[seed] = name
		}
	}
}

func TestStagnationPilotArmsPairEveryWindowWithABaseline(t *testing.T) {
	t.Parallel()

	arms, err := stagnationPilotArms(defaultBudget)
	if err != nil {
		t.Fatalf("stagnationPilotArms: %v", err)
	}

	byName := make(map[string]arm, len(arms))
	for _, current := range arms {
		if _, duplicate := byName[current.name]; duplicate {
			t.Fatalf("duplicate arm %s", current.name)
		}

		byName[current.name] = current

		// Every arm is evaluation-matched by construction, criterion or not,
		// so a window that ends runs early cannot also change the budget.
		if current.iters*current.popSize != defaultBudget {
			t.Errorf("arm %s spends %d evaluations, want %d",
				current.name, current.iters*current.popSize, defaultBudget)
		}

		if current.covariance != "separable" || current.restartStrategy != "ipop" {
			t.Errorf("arm %s is %s/%s, want separable/ipop",
				current.name, current.covariance, current.restartStrategy)
		}
	}

	// Each population level needs its own no-criterion baseline, or reclaimed
	// budget could only be read against another campaign's seeds.
	for _, lambda := range []int{20, 1024} {
		baseline := stagnationArmName(lambda, 0)
		if _, ok := byName[baseline]; !ok {
			t.Errorf("missing no-criterion baseline %s", baseline)
		}

		if window := byName[baseline].stopStagnationIters; window != 0 {
			t.Errorf("baseline %s configures a %d-iteration window", baseline, window)
		}

		anchor := hansenStagnationWindow(lambda)
		for _, want := range []int{anchor / 2, anchor, anchor * 4} {
			name := stagnationArmName(lambda, want)
			if byName[name].stopStagnationIters != want {
				t.Errorf("arm %s configures window %d, want %d",
					name, byName[name].stopStagnationIters, want)
			}
		}
	}

	// The absolute-threshold cell stays a single exploratory arm: a cost-unit
	// threshold cannot transfer to a reference image of a different scale, so
	// it must not be the shape the registered campaign inherits.
	probes := 0

	for _, current := range arms {
		if current.stopMinImprovement > 0 {
			probes++

			if current.stopStagnationIters == 0 {
				t.Errorf("arm %s sets stopMinImprovement without a window; app.JobConfig rejects that",
					current.name)
			}
		}
	}

	if probes != 1 {
		t.Errorf("pilot carries %d min-improvement arms, want exactly 1", probes)
	}
}

func TestHansenStagnationWindow(t *testing.T) {
	t.Parallel()

	// 120 + 30*n/lambda at n = 56, the dimensionality of an eight-circle batch.
	for _, testCase := range []struct {
		lambda int
		want   int
	}{
		{lambda: 20, want: 204},
		{lambda: 64, want: 146},
		{lambda: 1024, want: 121},
	} {
		if got := hansenStagnationWindow(testCase.lambda); got != testCase.want {
			t.Errorf("hansenStagnationWindow(%d) = %d, want %d", testCase.lambda, got, testCase.want)
		}
	}
}

func TestAssertDesignShapeRejectsAContradictingFlag(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designPilot, defaultBudget)
	if err != nil {
		t.Fatalf("campaignDesign: %v", err)
	}

	err = assertDesignShape(settings{}, plan)
	if err != nil {
		t.Errorf("unset flags should adopt the design: %v", err)
	}

	err = assertDesignShape(settings{blocks: plan.blocks, seedBase: plan.seedBase}, plan)
	if err != nil {
		t.Errorf("matching flags should be accepted: %v", err)
	}

	err = assertDesignShape(settings{blocks: plan.blocks + 1}, plan)
	if err == nil {
		t.Error("a contradicting -blocks was accepted")
	}

	err = assertDesignShape(settings{seedBase: plan.seedBase + 1}, plan)
	if err == nil {
		t.Error("a contradicting -seed-base was accepted")
	}
}

func TestDerivedContrastsMatchTheLambdaScreenFamily(t *testing.T) {
	t.Parallel()

	// The lambda report quotes "thirteen paired contrasts" and its committed
	// CSV must keep reproducing that family, so the derivation is pinned here
	// rather than left to whatever the arm count happens to be.
	plan, err := campaignDesign("lambda", defaultBudget)
	if err != nil {
		t.Fatalf("campaignDesign: %v", err)
	}

	if len(plan.contrasts) != 13 {
		t.Errorf("lambda design registers %d contrasts, want 13", len(plan.contrasts))
	}
}

// testDataRoot is any data root: the artifact defaults only join it with the
// design's manifest name.
const testDataRoot = "./data/cmaes-phase11"

func TestDesignArtifactDefaultsAreDistinctPerDesign(t *testing.T) {
	t.Parallel()

	// Collecting a second campaign with only -design must not write over the
	// first one's committed record.
	phase21 := withDesignArtifacts(settings{design: designPhase21, dataRoot: testDataRoot})
	pilot := withDesignArtifacts(settings{design: designPilot, dataRoot: testDataRoot})

	if phase21.resultsPath != "docs/cmaes-measurement.csv" || phase21.trajectory != "docs/cmaes-trajectories.csv" {
		t.Errorf("phase21 artifacts = %q, %q, want the committed campaign's paths",
			phase21.resultsPath, phase21.trajectory)
	}

	for _, pair := range [][2]string{
		{phase21.resultsPath, pilot.resultsPath},
		{phase21.trajectory, pilot.trajectory},
		{phase21.restartsPath, pilot.restartsPath},
		{phase21.manifestPath, pilot.manifestPath},
	} {
		if pair[0] == pair[1] {
			t.Errorf("phase21 and the pilot share artifact path %q", pair[0])
		}
	}
}

func TestExplicitArtifactPathsSurviveTheDesignDefaults(t *testing.T) {
	t.Parallel()

	config := withDesignArtifacts(settings{
		design: designPilot, dataRoot: testDataRoot,
		resultsPath: "docs/elsewhere.csv",
	})

	if config.resultsPath != "docs/elsewhere.csv" {
		t.Errorf("results = %q, want the caller's path", config.resultsPath)
	}
}

func TestEvaluationCapFollowsTheSubmittedBudget(t *testing.T) {
	t.Parallel()

	// The descriptive report divides by this, so a campaign run at half the
	// budget has to report its spend against half the cap.
	half := defaultBudget / 2

	plan, err := campaignDesign(designPilot, half)
	if err != nil {
		t.Fatalf("campaignDesign: %v", err)
	}

	if got := plan.evaluationCap(); got != half {
		t.Errorf("evaluationCap = %d, want %d", got, half)
	}
}

func TestStagnationCampaignRegistersOnePairPerPopulation(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designStag, defaultBudget)
	if err != nil {
		t.Fatalf("design %s: %v", designStag, err)
	}

	// The campaign pays multiplicity for exactly the two questions it asks.
	// Deriving the family from the arms instead would cost four contrasts for
	// the same two answers, which is what the lambda screen paid for.
	if len(plan.contrasts) != 2 {
		t.Fatalf("stagnation registers %d contrasts, want 2", len(plan.contrasts))
	}

	// Half the Hansen anchor at both levels: the window the pilot's
	// pre-registered rule selected, not one chosen from the pilot's costs.
	want := map[string]int{
		"sep-ipop-l20": 0, "sep-ipop-l20-w102": 102,
		"sep-ipop": 0, "sep-ipop-w60": 60,
	}
	if len(plan.arms) != len(want) {
		t.Fatalf("stagnation runs %d arms, want %d", len(plan.arms), len(want))
	}

	for _, current := range plan.arms {
		window, registered := want[current.name]
		if !registered {
			t.Errorf("stagnation runs unregistered arm %s", current.name)

			continue
		}

		if current.stopStagnationIters != window {
			t.Errorf("arm %s uses a window of %d, want %d",
				current.name, current.stopStagnationIters, window)
		}

		// stopMinImprovement is an absolute cost threshold and cannot transfer
		// to a reference image of a different scale, so no shipped arm may set
		// one. The pilot's exploratory cell is the only place it belongs.
		if current.stopMinImprovement != 0 {
			t.Errorf("arm %s sets stopMinImprovement %g in a campaign meant to select a default",
				current.name, current.stopMinImprovement)
		}
	}

	// Half the Hansen anchor at both levels: the window the pilot's
	// pre-registered rule selected, not one chosen from the pilot's costs.
	// Those two numbers are half the Hansen anchor, derived rather than typed.
	for lambda, window := range map[int]int{20: 102, defaultPop: 60} {
		if hansenStagnationWindow(lambda)/2 != window {
			t.Errorf("half the Hansen anchor at lambda %d is %d, want %d",
				lambda, hansenStagnationWindow(lambda)/2, window)
		}
	}

	if _, ok := plan.primaryContrast(); !ok {
		t.Error("the stagnation campaign registers no primary contrast")
	}
}

func TestStagnationDesignsRejectABudgetShorterThanTheirWindow(t *testing.T) {
	t.Parallel()

	// 5120 stays divisible by both populations, so the evaluation-matching
	// check passes it, but it leaves lambda 1024 five iterations against a
	// 60-generation window. app.JobConfig.Validate refuses that, and it would
	// refuse it at submit -- after the earlier arms are queued and the
	// manifest is already written. The design has to reject it first.
	const tooSmall = 5120

	for _, name := range []string{designPilot, designStag} {
		_, err := campaignDesign(name, tooSmall)
		if err == nil {
			t.Errorf("design %s accepted budget %d, which cannot reach its stagnation window",
				name, tooSmall)
		}
	}

	// The full budget still builds, so the guard rejects only what it must.
	for _, name := range []string{designPilot, designStag} {
		_, err := campaignDesign(name, defaultBudget)
		if err != nil {
			t.Errorf("design %s at the default budget: %v", name, err)
		}
	}
}

func TestBudgetSplitArmsAreEvaluationMatchedAndFixtureIsExplicit(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designSplit, defaultBudget)
	if err != nil {
		t.Fatalf("design %s: %v", designSplit, err)
	}

	// A campaign run on a different image is not poolable with one run on the
	// shared fixture, so the design has to name both, and the circle count has
	// to differ from the eight every earlier campaign fitted (Task 10).
	reference, circles := plan.fixture("example/MayFly-512.png")
	if reference == "example/MayFly-512.png" {
		t.Error("budget-split reuses the fixture every earlier campaign measured")
	}

	if circles == defaultCircles {
		t.Errorf("budget-split fits %d circles, the same shape as every earlier campaign", circles)
	}

	// Splitting a budget into epochs or cold restarts multiplies the generation
	// count. If the product missed the cap, a split arm would be compared
	// against an unsplit one that had spent more, and the contrast would
	// measure the budget rather than the split.
	for _, current := range plan.arms {
		if current.optimizer != "cmaes" {
			continue
		}

		spent := current.iters * current.popSize *
			max(current.optimizerEpochs, 1) * max(current.optimizerRestarts, 1)
		if spent != defaultBudget {
			t.Errorf("arm %s spends %d evaluations, want %d", current.name, spent, defaultBudget)
		}
	}

	if len(plan.contrasts) != 2 {
		t.Fatalf("budget-split registers %d contrasts, want 2", len(plan.contrasts))
	}
}

func TestBudgetSplitRefusesABudgetItsSplitsDoNotDivide(t *testing.T) {
	t.Parallel()

	// 1024 generations do not divide into five equal parts, so the split arms
	// could not be evaluation-matched and the design must refuse rather than
	// round a fifth of the budget away.
	_, err := budgetSplitArms(defaultPop * 1024)
	if err == nil {
		t.Fatal("budgetSplitArms accepted a budget its splits do not divide")
	}
}
