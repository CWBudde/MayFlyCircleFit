package main

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/opt"
)

// coldRestartStrategy is the restartStrategy value an arm carries when it uses
// the engine-agnostic cold-restart wrapper rather than one of CMA-ES's own
// shared-budget schedules.
const coldRestartStrategy = "none"

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
	restartsPath := filepath.Join(root, "restarts.csv")

	jobDir := filepath.Join(root, "projects", "measurement", "jobs", "persisted")

	err := os.MkdirAll(jobDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, manifestPath, "arm,block,seed,jobId\ncmaes-ipop,1,1001,persisted\nmayfly-single,2,1002,missing\n")
	writeFixture(t, filepath.Join(jobDir, "checkpoint-info.json"), `{
  "termination":"unknown","optimizerVersion":"test-version","iteration":2,"evaluations":20
}`)
	writeFixture(t, filepath.Join(jobDir, "checkpoint.json"), `{"optimizerVersion":"test-version","restarts":[
  {"stage":0,"restart":0,"regime":"large","population":1024,"iterations":1126,
   "evaluations":1153024,"bestCost":812.5,"termination":"tol_fun"},
  {"stage":0,"restart":1,"regime":"large","population":2048,"iterations":510,
   "evaluations":1044480,"bestCost":803.25,"termination":"maximum_evaluations"}
]}`)

	trace := `{"optimizerDiagnostics":{"sigma":0.3,"conditionNumber":1.2},` +
		`"iteration":1,"cost":12,"evaluations":10,"timestamp":"2026-08-25T10:00:00Z"}` + "\n" +
		`{"optimizerDiagnostics":{"sigma":0.2,"conditionNumber":2.4},` +
		`"iteration":2,"cost":9,"evaluations":20,"timestamp":"2026-08-25T10:00:03Z"}` + "\n"
	writeFixture(t, filepath.Join(jobDir, "trace.jsonl"), trace)

	err = collectPreliminary(settings{
		dataRoot: root, project: "measurement", manifestPath: manifestPath,
		resultsPath: resultsPath, trajectory: trajectoryPath,
		restartsPath: restartsPath, budget: 100,
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

	// A campaign that cannot complete is exactly the one whose restart schedules
	// need reading, so the preliminary path writes them too. They come off the
	// checkpoint in collectJob, so they are available here without the server.
	restarts := readCSVFixture(t, restartsPath)
	if len(restarts) != 3 {
		t.Fatalf("unexpected preliminary restarts: %#v", restarts)
	}

	// writeRestarts itself is covered directly elsewhere; what matters here is
	// that the preliminary path reaches it and carries the manifest identity.
	for index, want := range map[int]string{0: "cmaes-ipop", 1: "1", 2: "1001", 4: "1", 6: "2048"} {
		if restarts[2][index] != want {
			t.Fatalf("restarts[%d] = %q, want %q", index, restarts[2][index], want)
		}
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

	for _, name := range registeredDesigns() {
		plan, err := campaignDesign(name, mustDesignBudget(t, name))
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
	//
	// The restart ladder shares the stagnation campaign's seeds for the same
	// reason and one more: its sep-ipop and sep-ipop-w60 arms repeat that
	// campaign's configuration exactly, so the cells have to reproduce bit for
	// bit, and the shared range puts seed 111018 -- the block that set the best
	// recorded eight-circle cost -- inside the ladder's own design.
	shared := map[[2]string]bool{
		{designPhase21, designLambda}: true, {designLambda, designPhase21}: true,
		{designStag, designLadder}: true, {designLadder, designStag}: true,
	}

	for _, name := range registeredDesigns() {
		plan, err := campaignDesign(name, mustDesignBudget(t, name))
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

	// Task 3 asks whether warm epochs beat equivalent cold attempts, so the
	// two split arms have to be tested against each other. Reading either one
	// against sep-ipop would compare it with a third mechanism and leave the
	// question unanswered after twelve blocks of paid-for runs.
	want := []plannedContrast{
		{control: "mayfly-r16", candidate: "sep-ipop", primary: true},
		{control: "sep-r5", candidate: "sep-e5"},
	}
	if !slices.Equal(plan.contrasts, want) {
		t.Errorf("budget-split registers %+v, want %+v", plan.contrasts, want)
	}

	// Every registered contrast has to be rendered by one of the two report
	// tables, which key off the baseline and the secondary control. A contrast
	// naming neither would be corrected for and then printed nowhere.
	for _, current := range plan.contrasts {
		if current.control != plan.baseline && current.control != plan.secondaryControl {
			t.Errorf("contrast against %q is reported by neither table", current.control)
		}
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

	// The two Mayfly controls have Phase 21's fixed shape, so only the CMA-ES
	// arms grow with the budget. A non-positive one would give them negative
	// generation counts and a larger one would fund them past anything the
	// controls reach, while the report still called the engine comparison
	// evaluation-matched.
	for _, budget := range []int{0, -defaultPop * 5, defaultBudget + defaultPop*5} {
		_, budgetErr := budgetSplitArms(budget)
		if budgetErr == nil {
			t.Errorf("budgetSplitArms accepted budget %d", budget)
		}
	}

	// The full budget still builds, so the guards reject only what they must.
	_, defaultErr := budgetSplitArms(defaultBudget)
	if defaultErr != nil {
		t.Errorf("budgetSplitArms rejected the default budget: %v", defaultErr)
	}
}

const (
	provenanceStateCompleted  = "completed"
	provenanceBackendCPU      = "cpu"
	provenanceBackendOpenCL   = "opencl"
	provenanceBackendFellBack = "opencl(degraded)"
)

// TestReadResultsAcceptsBothColumnWidths pins the compatibility rule that lets
// the backend provenance column be added without touching a recorded campaign.
// Every result CSV committed under docs/ was written at the legacy width, and
// rewriting one to add a column would mean asserting a provenance nobody
// recorded at the time -- so the reader accepts both, and a legacy row reports
// an empty backend rather than a guessed cpu.
func TestReadResultsAcceptsBothColumnWidths(t *testing.T) {
	t.Parallel()

	legacy := []string{
		"arm", "block", "seed", "jobId", "state", "termination", "optimizerVersion",
		"bestCost", "scoredEvaluations", "finalEvaluations", "iterations", "elapsedSeconds",
	}

	cases := []struct {
		name        string
		header      []string
		row         []string
		wantBackend string
	}{
		{
			name:   "legacy",
			header: legacy,
			row: []string{
				"a", "1", "42", "job-1", provenanceStateCompleted, provenanceStateCompleted,
				"v0.1.0", "1.5", "10", "10", "5", "1.0",
			},
			wantBackend: "",
		},
		{
			name:   "with backend",
			header: append(append([]string(nil), legacy...), "backend"),
			row: []string{
				"a", "1", "42", "job-1", provenanceStateCompleted, provenanceStateCompleted,
				"v0.1.0", "1.5", "10", "10", "5", "1.0", provenanceBackendFellBack,
			},
			wantBackend: provenanceBackendFellBack,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "results.csv")
			writeCSVFixture(t, path, [][]string{testCase.header, testCase.row})

			rows, err := readResults(path, 1)
			if err != nil {
				t.Fatalf("readResults: %v", err)
			}

			if len(rows) != 1 {
				t.Fatalf("readResults returned %d rows, want 1", len(rows))
			}

			if rows[0].Backend != testCase.wantBackend {
				t.Errorf("Backend = %q, want %q", rows[0].Backend, testCase.wantBackend)
			}

			if rows[0].Score != 1.5 {
				t.Errorf("Score = %v, want 1.5", rows[0].Score)
			}
		})
	}
}

// TestBackendProvenanceDistinguishesUnknownFromCPU pins the distinction the
// column exists to make, and the checkpoint fallback that keeps it from being
// vacuous. A run nothing recorded is not a CPU run, and reporting cpu there
// would launder a gap into a measurement -- but -action preliminary
// synthesizes its jobStatus from checkpoint-info.json, which carries no
// provenance at all, so without the checkpoint fallback every preliminary row
// would take that unknown branch even for a job that recorded its backend
// perfectly well.
func TestBackendProvenanceDistinguishesUnknownFromCPU(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status jobStatus
		saved  checkpoint
		want   string
	}{
		{name: "no record", want: "unknown"},
		{
			name:   provenanceBackendCPU,
			status: jobStatus{EffectiveBackend: provenanceBackendCPU},
			want:   provenanceBackendCPU,
		},
		{
			name:   provenanceBackendOpenCL,
			status: jobStatus{EffectiveBackend: provenanceBackendOpenCL},
			want:   provenanceBackendOpenCL,
		},
		{
			name:   "opencl that fell back",
			status: jobStatus{EffectiveBackend: provenanceBackendOpenCL, BackendDegraded: true},
			want:   provenanceBackendFellBack,
		},
		{
			name:  "preliminary reads the checkpoint",
			saved: checkpoint{EffectiveBackend: provenanceBackendOpenCL},
			want:  provenanceBackendOpenCL,
		},
		{
			name:  "preliminary keeps the degraded suffix",
			saved: checkpoint{EffectiveBackend: provenanceBackendOpenCL, BackendDegraded: true},
			want:  provenanceBackendFellBack,
		},
		{
			// The live status wins where both have one, so a job that fell back
			// in this process is not overwritten by an earlier run's record.
			name:   "status wins over the checkpoint",
			status: jobStatus{EffectiveBackend: provenanceBackendOpenCL, BackendDegraded: true},
			saved:  checkpoint{EffectiveBackend: provenanceBackendCPU},
			want:   provenanceBackendFellBack,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := backendProvenance(testCase.status, testCase.saved)
			if got != testCase.want {
				t.Errorf("backendProvenance() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func writeCSVFixture(t *testing.T, path string, records [][]string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	writer := csv.NewWriter(file)

	err = writer.WriteAll(records)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRestartLadderIsEvaluationMatchedAndRegistersTwoContrasts pins the shape
// of the ladder the campaign was submitted under. The lesson the budget-split
// report had to record is that a registered design must be frozen at the
// commit its campaign is submitted from; this test is what makes a later edit
// to that design fail loudly rather than silently rewrite what was registered.
func TestRestartLadderIsEvaluationMatchedAndRegistersTwoContrasts(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designLadder, defaultBudget)
	if err != nil {
		t.Fatalf("design %s: %v", designLadder, err)
	}

	// The ladder asks its question on the fixture the record was set on, so it
	// must not carry a fixture override the way budget-split does.
	reference, circles := plan.fixture("example/MayFly-512.png")
	if reference != "example/MayFly-512.png" || circles != defaultCircles {
		t.Errorf("ladder runs %s at %d circles, want the default fixture at %d", reference, circles, defaultCircles)
	}

	if plan.blocks != campaignBlocks {
		t.Errorf("ladder registers %d blocks, want %d", plan.blocks, campaignBlocks)
	}

	// hansenStagnationWindow reads searchDimensions, which describes the
	// default eight-circle fixture. The check above is what makes that legal.
	want := map[string]struct {
		lambda, restarts, stagnation int
		strategy                     string
	}{
		"sep-r2-l1024":  {1024, 2, 0, coldRestartStrategy},
		"sep-r8-l256":   {256, 8, 0, coldRestartStrategy},
		"sep-r32-l64":   {64, 32, 0, coldRestartStrategy},
		"sep-r64-l32":   {32, 64, 0, coldRestartStrategy},
		"sep-ipop":      {1024, 1, 0, "ipop"},
		"sep-ipop-w60":  {1024, 1, 60, "ipop"},
		"sep-bipop-w60": {1024, 1, 60, "bipop"},
	}
	if len(plan.arms) != len(want) {
		t.Fatalf("ladder registers %d arms, want %d", len(plan.arms), len(want))
	}

	for _, current := range plan.arms {
		expected, registered := want[current.name]
		if !registered {
			t.Errorf("ladder registers unexpected arm %s", current.name)

			continue
		}

		if current.popSize != expected.lambda || current.optimizerRestarts != expected.restarts {
			t.Errorf("arm %s is lambda %d x %d restarts, want %d x %d",
				current.name, current.popSize, current.optimizerRestarts, expected.lambda, expected.restarts)
		}

		if current.stopStagnationIters != expected.stagnation || current.restartStrategy != expected.strategy {
			t.Errorf("arm %s is %s with window %d, want %s with window %d",
				current.name, current.restartStrategy, current.stopStagnationIters,
				expected.strategy, expected.stagnation)
		}

		// Every rung has to spend the cap exactly, or a rung difference would
		// measure the budget rather than the shape.
		spent := current.iters * current.popSize *
			max(current.optimizerEpochs, 1) * max(current.optimizerRestarts, 1)
		if spent != defaultBudget {
			t.Errorf("arm %s spends %d evaluations, want %d", current.name, spent, defaultBudget)
		}

		// A rung the server would refuse costs the whole campaign: the manifest
		// is written O_EXCL, so a rejected arm cannot simply be resubmitted.
		if current.popSize < app.MinPopulation || current.popSize > app.MaxPopulation {
			t.Errorf("arm %s uses lambda %d, outside app's %d..%d",
				current.name, current.popSize, app.MinPopulation, app.MaxPopulation)
		}

		if current.optimizerRestarts > app.MaxOptimizerRestarts {
			t.Errorf("arm %s asks for %d cold restarts, above app.MaxOptimizerRestarts %d",
				current.name, current.optimizerRestarts, app.MaxOptimizerRestarts)
		}
	}

	// Two contrasts, so Holm corrects over two questions rather than the
	// twenty-one that seven arms would otherwise produce. sep-r32-l64 is named
	// here rather than chosen from the ladder once the costs are in.
	wantContrasts := []plannedContrast{
		{control: "sep-ipop", candidate: "sep-r32-l64", primary: true},
		{control: "sep-ipop-w60", candidate: "sep-bipop-w60"},
	}
	if !slices.Equal(plan.contrasts, wantContrasts) {
		t.Errorf("ladder registers %+v, want %+v", plan.contrasts, wantContrasts)
	}

	for _, current := range plan.contrasts {
		if current.control != plan.baseline && current.control != plan.secondaryControl {
			t.Errorf("contrast against %q is reported by neither table", current.control)
		}
	}
}

// TestRestartLadderReplicatesTheStagnationCampaignArms is the ladder's validity
// check expressed as a test. sep-ipop and sep-ipop-w60 have to be
// configuration-identical to the stagnation campaign's arms of the same names
// and run on the same seeds, so that campaign's twelve cells must reproduce bit
// for bit. If they do not, the ladder's comparison against the recorded 752.52
// is not licensed and the campaign says nothing about the record.
func TestRestartLadderReplicatesTheStagnationCampaignArms(t *testing.T) {
	t.Parallel()

	ladder, err := campaignDesign(designLadder, defaultBudget)
	if err != nil {
		t.Fatalf("design %s: %v", designLadder, err)
	}

	stagnation, err := campaignDesign(designStag, defaultBudget)
	if err != nil {
		t.Fatalf("design %s: %v", designStag, err)
	}

	if ladder.seedBase != stagnation.seedBase {
		t.Fatalf("ladder seeds from %d, stagnation from %d; the arms would not be comparable",
			ladder.seedBase, stagnation.seedBase)
	}

	byName := func(plan design, name string) (arm, bool) {
		for _, current := range plan.arms {
			if current.name == name {
				return current, true
			}
		}

		return arm{}, false
	}

	for _, name := range []string{"sep-ipop", "sep-ipop-w60"} {
		mine, ok := byName(ladder, name)
		if !ok {
			t.Errorf("ladder does not register %s", name)

			continue
		}

		theirs, ok := byName(stagnation, name)
		if !ok {
			t.Errorf("stagnation does not register %s", name)

			continue
		}

		if mine != theirs {
			t.Errorf("ladder %s is %+v, stagnation ran %+v; the cells would not reproduce", name, mine, theirs)
		}
	}
}

// TestRestartLadderBipopArmCarriesAWindow guards the one configuration mistake
// that would make the secondary contrast measure nothing. go-cma-es gives the
// first large run a budget equal to the whole schedule and only reaches the
// small regime after a large run finishes, so a bipop arm with no stagnation
// criterion is IPOP under another name and the contrast would compare two
// spellings of the same search.
func TestRestartLadderBipopArmCarriesAWindow(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designLadder, defaultBudget)
	if err != nil {
		t.Fatalf("design %s: %v", designLadder, err)
	}

	for _, current := range plan.arms {
		if current.restartStrategy == "bipop" && current.stopStagnationIters == 0 {
			t.Errorf("arm %s runs bipop with no stagnation window and degenerates to a single large run", current.name)
		}

		// The ladder rungs must not carry one. Their restart count is fixed, so
		// stopping a dead run early cannot buy another one -- it would only
		// leave the budget unspent and break the evaluation match.
		if current.restartStrategy == coldRestartStrategy && current.stopStagnationIters != 0 {
			t.Errorf("ladder rung %s carries a stagnation window it cannot spend", current.name)
		}
	}
}

func TestRestartLadderRefusesABudgetItsRungsDoNotDivide(t *testing.T) {
	t.Parallel()

	// One evaluation short of the cap divides neither the ladder product nor
	// the population, so the rungs could not be evaluation-matched.
	_, err := restartLadderArms(defaultBudget - 1)
	if err == nil {
		t.Error("restartLadderArms accepted a budget its rungs do not divide")
	}

	_, err = restartLadderArms(0)
	if err == nil {
		t.Error("restartLadderArms accepted a zero budget")
	}
}

// registeredDesigns is every design campaignDesign knows. The enumerating tests
// share it so a new design cannot be added to the switch and quietly skip the
// closure and seed-collision guards.
func registeredDesigns() []string {
	return []string{
		designPhase21, designLambda, designPilot, designStag,
		designSplit, designLadder, designHunt,
	}
}

// mustDesignBudget resolves a design's own registered budget the way main does,
// so an enumerating test does not have to know which designs inherit the fixed
// MayFly-matched cap and which register their own.
func mustDesignBudget(t *testing.T, name string) int {
	t.Helper()

	budget, err := designBudget(name, 0)
	if err != nil {
		t.Fatalf("design %s budget: %v", name, err)
	}

	return budget
}

func TestDesignBudgetIsTheDesignsAndAFlagCanOnlyAssertIt(t *testing.T) {
	t.Parallel()

	// -budget is both the arm sizer and the trace scoring cap, so a campaign
	// submitted at one value and collected at another would score every job
	// against a cap it never ran under. The design owns the number.
	if budget := mustDesignBudget(t, designHunt); budget != huntBudget {
		t.Errorf("deep hunt budget = %d, want %d", budget, huntBudget)
	}

	if budget := mustDesignBudget(t, designLadder); budget != defaultBudget {
		t.Errorf("restart ladder budget = %d, want %d", budget, defaultBudget)
	}

	_, contradictsHunt := designBudget(designHunt, defaultBudget)
	if contradictsHunt == nil {
		t.Error("designBudget accepted a -budget that contradicts the deep hunt's own")
	}

	_, contradictsLadder := designBudget(designLadder, huntBudget)
	if contradictsLadder == nil {
		t.Error("designBudget accepted a -budget that contradicts the ladder's own")
	}

	// The ceiling every earlier design inherits still has to refuse a bigger
	// budget, or its arms would stop being evaluation-matched.
	_, aboveCeiling := campaignDesign(designLadder, huntBudget)
	if aboveCeiling == nil {
		t.Error("campaignDesign built the restart ladder above the fixed campaign budget")
	}
}

func TestDeepHuntRegistersItsSingleFactorAndCompoundArms(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designHunt, huntBudget)
	if err != nil {
		t.Fatalf("deep hunt: %v", err)
	}

	if !plan.descriptive {
		t.Error("the deep hunt reports an order statistic and must be descriptive")
	}

	if len(plan.contrasts) != 0 {
		t.Errorf("the deep hunt registers %d contrasts, want none", len(plan.contrasts))
	}

	if plan.record != recordCost {
		t.Errorf("deep hunt record = %v, want %v", plan.record, recordCost)
	}

	if plan.blocks != huntBlocks {
		t.Errorf("deep hunt blocks = %d, want %d", plan.blocks, huntBlocks)
	}

	// Pinned rather than left to -ref: the design reports a cost against
	// recordCost and warm-starts from coordinates bounded by that canvas.
	if plan.reference != recordReference || plan.circles != defaultCircles {
		t.Errorf("deep hunt fixture = %q at %d circles, want the pinned %q at %d",
			plan.reference, plan.circles, recordReference, defaultCircles)
	}

	type shape struct {
		covariance   string
		strategy     string
		lambda       int
		iters        int
		epochs       int
		initialSigma float64
		passiveCMA   bool
		warmStart    bool
	}

	want := map[string]shape{
		"sep-ipop":         {"separable", "ipop", 1024, 12288, 1, 0, false, false},
		"blk-ipop":         {"block", "ipop", 1024, 12288, 1, 0, false, false},
		"blk-l4096":        {"block", coldRestartStrategy, 4096, 3072, 1, 0, false, false},
		"sep-l4096":        {"separable", coldRestartStrategy, 4096, 3072, 1, 0, false, false},
		"sep-ipop-s015":    {"separable", "ipop", 1024, 12288, 1, 0.15, false, false},
		"sep-ipop-s050":    {"separable", "ipop", 1024, 12288, 1, 0.50, false, false},
		"sep-ipop-passive": {"separable", "ipop", 1024, 12288, 1, 0, true, false},
		"sep-e8":           {"separable", coldRestartStrategy, 1024, 1536, 8, 0, false, false},
		"sep-warm-e8":      {"separable", coldRestartStrategy, 1024, 1536, 8, 0.05, false, true},
	}

	if len(plan.arms) != len(want) {
		t.Fatalf("deep hunt has %d arms, want %d", len(plan.arms), len(want))
	}

	for _, current := range plan.arms {
		expected, registered := want[current.name]
		if !registered {
			t.Errorf("unregistered arm %s", current.name)

			continue
		}

		got := shape{
			current.covariance, current.restartStrategy, current.popSize, current.iters,
			current.optimizerEpochs, current.initialSigma, current.passiveCMA, current.warmStart,
		}
		if got != expected {
			t.Errorf("arm %s = %+v, want %+v", current.name, got, expected)
		}

		if current.optimizer != "cmaes" {
			t.Errorf("arm %s runs %s; the deep hunt is a CMA-ES design", current.name, current.optimizer)
		}

		// Evaluation-matched by construction. A descriptive design would
		// survive an unmatched one, but an arm quietly running on more
		// evaluations than its neighbours would make the minimum unreadable.
		spend := current.iters * current.popSize * current.optimizerEpochs * current.optimizerRestarts
		if spend != huntBudget {
			t.Errorf("arm %s spends %d evaluations, want %d", current.name, spend, huntBudget)
		}

		if current.popSize < app.MinPopulation || current.popSize > app.MaxPopulation {
			t.Errorf("arm %s wants lambda %d, outside app's %d..%d",
				current.name, current.popSize, app.MinPopulation, app.MaxPopulation)
		}

		if current.optimizerEpochs > app.MaxOptimizerEpochs {
			t.Errorf("arm %s wants %d epochs, above app's %d",
				current.name, current.optimizerEpochs, app.MaxOptimizerEpochs)
		}

		if current.iters > app.MaxIterations {
			t.Errorf("arm %s wants %d iterations, above app's %d",
				current.name, current.iters, app.MaxIterations)
		}

		// app requires optimizerRestarts to be exactly 1 under ipop or bipop,
		// so a stray cold-restart count would be refused at submit time.
		if current.optimizerRestarts != 1 {
			t.Errorf("arm %s wants %d cold restarts; the hunt splits with epochs",
				current.name, current.optimizerRestarts)
		}
	}
}

func TestDeepHuntRefusesABudgetItsArmsDoNotDivide(t *testing.T) {
	t.Parallel()

	// The largest population is 4096, so a budget divisible by 1024 but not by
	// 4096 leaves that arm unmatched rather than merely awkward.
	_, unmatched := deepHuntArms(huntBudget + 1024)
	if unmatched == nil {
		t.Error("deepHuntArms accepted a budget lambda 4096 does not divide")
	}

	_, empty := deepHuntArms(0)
	if empty == nil {
		t.Error("deepHuntArms accepted a zero budget")
	}
}

func TestWarmStartCirclesAreInsideTheBoundsAppEnforces(t *testing.T) {
	t.Parallel()

	// app refuses an out-of-bounds initialCircles rather than clamping it, so a
	// silent edit to recordCircles would fail eleven jobs at submit time rather
	// than degrade quietly. Two of these sit exactly on a bound.
	const side = 512

	bounds := fit.NewBounds(defaultCircles, side, side)

	circles := recordCircles()
	if len(circles) != defaultCircles {
		t.Fatalf("recordCircles has %d circles, want %d", len(circles), defaultCircles)
	}

	for index, circle := range circles {
		params := []float64{circle.x, circle.y, circle.r, circle.red, circle.green, circle.blue, circle.opacity}
		for offset, value := range params {
			position := index*app.ParametersPerCircle + offset
			if value < bounds.Lower[position] || value > bounds.Upper[position] {
				t.Errorf("circle %d parameter %d = %v, outside [%v, %v]",
					index, offset, value, bounds.Lower[position], bounds.Upper[position])
			}
		}
	}
}

func TestDeepHuntPayloadCarriesOnlyTheKnobsAnArmTurns(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designHunt, huntBudget)
	if err != nil {
		t.Fatalf("deep hunt: %v", err)
	}

	byName := make(map[string]arm, len(plan.arms))
	for _, current := range plan.arms {
		byName[current.name] = current
	}

	config := settings{project: defaultProject, reference: "example/MayFly-512.png", workers: 8}

	control := jobPayload(config, plan, byName["sep-ipop"], 114001)
	for _, field := range []string{"initialSigma", "activeCMA", "initialCircles"} {
		if _, present := control[field]; present {
			t.Errorf("the control arm sent %s; it has to reach the server untouched", field)
		}
	}

	if control["covarianceMode"] != "separable" {
		t.Errorf("control covarianceMode = %v, want separable", control["covarianceMode"])
	}

	if block := jobPayload(config, plan, byName["blk-ipop"], 114001); block["covarianceMode"] != "block" {
		t.Errorf("blk-ipop covarianceMode = %v, want block", block["covarianceMode"])
	}

	if sigma := jobPayload(config, plan, byName["sep-ipop-s015"], 114001); sigma["initialSigma"] != 0.15 {
		t.Errorf("sep-ipop-s015 initialSigma = %v, want 0.15", sigma["initialSigma"])
	}

	passive := jobPayload(config, plan, byName["sep-ipop-passive"], 114001)
	if active, present := passive["activeCMA"]; !present || active != false {
		t.Errorf("sep-ipop-passive activeCMA = %v (present %v), want false", active, present)
	}

	warm := jobPayload(config, plan, byName["sep-warm-e8"], 114001)

	specs, ok := warm["initialCircles"].([]map[string]any)
	if !ok {
		t.Fatalf("sep-warm-e8 initialCircles = %T, want []map[string]any", warm["initialCircles"])
	}

	// app requires exactly one spec per circle and a batch wide enough to cover
	// them all, which this design's batchSize already is.
	if len(specs) != defaultCircles {
		t.Errorf("sep-warm-e8 sent %d circles, want %d", len(specs), defaultCircles)
	}

	if warm["batchSize"] != defaultCircles {
		t.Errorf("sep-warm-e8 batchSize = %v, want %d", warm["batchSize"], defaultCircles)
	}

	if colour := specs[0]["color"]; colour != "#181700" {
		t.Errorf("first circle colour = %v, want #181700", colour)
	}
}

// TestRecordDesignsPinTheirFixture covers the review finding that a design
// reporting a cost against recordCost must not let -ref redirect it. Both the
// ladder and the hunt report that record, and the hunt additionally seeds
// initialCircles from coordinates bounded by a 512x512 canvas.
func TestRecordDesignsPinTheirFixture(t *testing.T) {
	t.Parallel()

	for _, name := range []string{designLadder, designHunt} {
		budget := defaultBudget
		if name == designHunt {
			budget = huntBudget
		}

		plan, err := campaignDesign(name, budget)
		if err != nil {
			t.Fatalf("design %s: %v", name, err)
		}

		if plan.record <= 0 {
			t.Fatalf("design %s reports no record, so this test is asserting the wrong thing", name)
		}

		// The fallback is deliberately an image the design must not accept: a
		// pinned design ignores it, an unpinned one would return it.
		reference, circles := plan.fixture("example/Ref-512.png")
		if reference != recordReference {
			t.Errorf("design %s took fixture %q from -ref, want the pinned %q",
				name, reference, recordReference)
		}

		if circles != defaultCircles {
			t.Errorf("design %s fits %d circles, want the %d the record was recorded at",
				name, circles, defaultCircles)
		}
	}
}

// TestEvaluationCapCountsEpochsAndRestarts covers the review finding that the
// descriptive report's "% of cap" column read iters*lambda only, so an arm that
// split its budget into epochs reported eight times its cap.
func TestEvaluationCapCountsEpochsAndRestarts(t *testing.T) {
	t.Parallel()

	plan, err := campaignDesign(designHunt, huntBudget)
	if err != nil {
		t.Fatalf("design %s: %v", designHunt, err)
	}

	if got := plan.evaluationCap(); got != huntBudget {
		t.Errorf("evaluationCap = %d, want the budget %d every arm was sized against", got, huntBudget)
	}

	// The arm that exposed it: eight epochs, so the old product understated the
	// cap eightfold and the column would have printed 800%.
	var split arm

	for _, current := range plan.arms {
		if current.name == "sep-e8" {
			split = current
		}
	}

	if split.optimizerEpochs <= 1 {
		t.Fatalf("sep-e8 has %d epochs, so it no longer covers the finding", split.optimizerEpochs)
	}

	if spent := split.iters * split.popSize * split.optimizerEpochs; spent != plan.evaluationCap() {
		t.Errorf("sep-e8 spends %d against a cap of %d", spent, plan.evaluationCap())
	}
}
