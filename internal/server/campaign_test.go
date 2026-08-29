package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/cwbudde/circlefit/internal/ui"
)

// The 96-circle chain that PLAN.md named as the acceptance fixture no longer
// exists: it lived only on a compute box whose directory was deleted, and the
// data is unrecoverable. The chain below is therefore synthesized, and is
// modelled on the shape that campaign had — a batch base, repeated extends, and
// a polish that holds the circle count still while moving the cost.
const (
	chainBaseJob    = "11111111-1111-4111-8111-111111111111"
	chainExtendJob  = "22222222-2222-4222-8222-222222222222"
	chainPolishJob  = "33333333-3333-4333-8333-333333333333"
	chainSecondJob  = "44444444-4444-4444-8444-444444444444"
	chainOrphanJob  = "55555555-5555-4555-8555-555555555555"
	chainScheduleID = "66666666-6666-4666-8666-666666666666"
)

type synthesizedStage struct {
	jobID    string
	parent   string
	polished bool
	circles  int
	cost     float64
}

type checkpointListCountingStore struct {
	store.Store

	mu    sync.Mutex
	lists int
}

func (counting *checkpointListCountingStore) ListCheckpoints() ([]store.CheckpointInfo, error) {
	counting.mu.Lock()
	counting.lists++
	counting.mu.Unlock()

	return counting.Store.ListCheckpoints()
}

func (counting *checkpointListCountingStore) listCount() int {
	counting.mu.Lock()
	defer counting.mu.Unlock()

	return counting.lists
}

// synthesizeChain writes a chain of checkpoints whose only connection is the
// lineage each one records, which is exactly the situation for a campaign
// driven by hand through the extend and polish endpoints.
func synthesizeChain(t *testing.T, persistence store.Store, imagePath string, stages []synthesizedStage) {
	t.Helper()

	timestamp := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	for i, stage := range stages {
		config := store.JobConfig{
			RefPath: imagePath,
			Mode:    app.ModeBatch,
			Circles: stage.circles,
			Iters:   100,
			PopSize: 30,
			Seed:    42,
		}
		checkpoint := store.NewCheckpoint(stage.jobID, make([]float64, stage.circles*7), stage.cost, 900, 100*(i+1), config)
		checkpoint.ActualCircles = stage.circles
		checkpoint.Termination = "completed"
		checkpoint.Timestamp = timestamp.Add(time.Duration(i) * time.Hour)

		if stage.parent != "" {
			if stage.polished {
				checkpoint.PolishedFrom = stage.parent
			} else {
				checkpoint.ExtendedFrom = stage.parent
			}
		}

		err := persistence.SaveCheckpoint(stage.jobID, checkpoint)
		if err != nil {
			t.Fatalf("save checkpoint %s: %v", stage.jobID, err)
		}
	}
}

func defaultSynthesizedChain() []synthesizedStage {
	return []synthesizedStage{
		{jobID: chainBaseJob, circles: 8, cost: 812.5},
		{jobID: chainExtendJob, parent: chainBaseJob, circles: 16, cost: 640.25},
		{jobID: chainPolishJob, parent: chainExtendJob, polished: true, circles: 16, cost: 631.75},
		{jobID: chainSecondJob, parent: chainPolishJob, circles: 24, cost: 572.357},
	}
}

// TestImportedChainRendersAsOneCampaign is the replacement acceptance check.
// The original fixture is gone, so this asserts the same property against a
// synthesized chain: four unrelated job records read back as one run, in order,
// with each stage's kind recovered from its lineage.
func TestImportedChainRendersAsOneCampaign(t *testing.T) {
	fixture := newScheduleFixture(t, 1)
	synthesizeChain(t, fixture.server.store, fixture.imagePath, defaultSynthesizedChain())

	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/chains/"+chainSecondJob, nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("chain page status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	body := recorder.Body.String()
	for _, marker := range []string{
		"Imported chain",
		"Reconstructed from checkpoint lineage · 4 stages",
		"812.500",
		"640.250",
		"631.750",
		"572.357",
		"Cost against circle count",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("chain campaign page missing %q", marker)
		}
	}
	// One run means one ordered table, so the stage costs must appear in run
	// order rather than in whatever order the store happened to list them.
	if !inOrder(body, "812.500", "640.250", "631.750", "572.357") {
		t.Error("chain campaign page does not present the stages in run order")
	}
}

func TestChainAPIReturnsTheWholeLineage(t *testing.T) {
	fixture := newScheduleFixture(t, 1)
	synthesizeChain(t, fixture.server.store, fixture.imagePath, defaultSynthesizedChain())

	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/chains/"+chainSecondJob, nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("chain API status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	var detail chainDetailWire

	err := json.Unmarshal(recorder.Body.Bytes(), &detail)
	if err != nil {
		t.Fatalf("decode chain: %v", err)
	}

	if detail.RootJobID != chainBaseJob || detail.LeafJobID != chainSecondJob {
		t.Fatalf("chain endpoints = %q..%q, want %q..%q", detail.RootJobID, detail.LeafJobID, chainBaseJob, chainSecondJob)
	}

	wantKinds := []string{"base", "extend", "polish", "extend"}
	wantCircles := []int{8, 16, 16, 24}

	if len(detail.Stages) != len(wantKinds) {
		t.Fatalf("chain stages = %d, want %d", len(detail.Stages), len(wantKinds))
	}

	for i, stage := range detail.Stages {
		if stage.Kind != wantKinds[i] || stage.Circles != wantCircles[i] {
			t.Errorf("stage %d = (%s, %d circles), want (%s, %d circles)", i, stage.Kind, stage.Circles, wantKinds[i], wantCircles[i])
		}
	}
}

func TestChainAPIRejectsUnknownAndMalformedJobs(t *testing.T) {
	fixture := newScheduleFixture(t, 1)

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "not a uuid", path: "/api/v1/chains/campaign", want: http.StatusBadRequest},
		{name: "unknown job", path: "/api/v1/chains/" + chainOrphanJob, want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			fixture.server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
		})
	}
}

// TestChainDiscoveryIgnoresSingleJobsAndSchedules keeps the campaign list
// honest: a lone checkpoint is not a campaign, and a stage a schedule already
// owns is shown by the schedule view rather than twice.
func TestChainDiscoveryIgnoresSingleJobsAndSchedules(t *testing.T) {
	timestamp := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	infos := []store.CheckpointInfo{
		{JobID: chainBaseJob, ActualCircles: 8, BestCost: 812.5, Timestamp: timestamp},
		{JobID: chainExtendJob, ExtendedFrom: chainBaseJob, ActualCircles: 16, BestCost: 640.25, Timestamp: timestamp.Add(time.Hour)},
		{JobID: chainOrphanJob, ActualCircles: 4, BestCost: 900, Timestamp: timestamp},
		{JobID: chainPolishJob, PolishedFrom: chainExtendJob, ScheduleID: chainScheduleID, ActualCircles: 16, BestCost: 600, Timestamp: timestamp.Add(2 * time.Hour)},
	}

	chains := discoverChains(infos)
	if len(chains) != 1 {
		t.Fatalf("discovered %d chains, want 1: %+v", len(chains), chains)
	}

	chain := chains[0]
	if chain.LeafJobID != chainExtendJob || chain.RootJobID != chainBaseJob || chain.Stages != 2 {
		t.Fatalf("chain = %+v, want the two-stage base..extend chain", chain)
	}
}

// TestChainDiscoveryStopsOnACycle guards the walk against a hand-edited or
// corrupt lineage rather than letting the listing spin.
func TestChainDiscoveryStopsOnACycle(t *testing.T) {
	infos := []store.CheckpointInfo{
		{JobID: chainBaseJob, ExtendedFrom: chainExtendJob},
		{JobID: chainExtendJob, ExtendedFrom: chainBaseJob},
	}
	if chains := discoverChains(infos); len(chains) != 0 {
		t.Fatalf("discovered %d chains from a cycle, want 0", len(chains))
	}

	fixture := newScheduleFixture(t, 1)
	synthesizeChain(t, fixture.server.store, fixture.imagePath, []synthesizedStage{
		{jobID: chainBaseJob, circles: 8, cost: 812.5},
	})
	// A checkpoint that names itself is refused by validation, so the cycle a
	// walk can actually meet is a two-node one written by hand.
	persistence := fixture.server.store

	checkpoint, err := persistence.LoadCheckpoint(chainBaseJob)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}

	checkpoint.ExtendedFrom = chainExtendJob

	err = persistence.SaveCheckpoint(chainBaseJob, checkpoint)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	checkpoint.JobID = chainExtendJob

	checkpoint.ExtendedFrom = chainBaseJob

	err = persistence.SaveCheckpoint(chainExtendJob, checkpoint)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	_, err = chainCheckpoints(persistence, chainExtendJob)
	if err == nil {
		t.Fatal("chainCheckpoints() accepted a cyclic lineage")
	}
}

// TestCampaignViewOfAScheduleShowsEveryStageState covers the columns the stage
// records can populate, including the skipped stage that policy declined.
func TestCampaignViewOfAScheduleShowsEveryStageState(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	completed := started.Add(90 * time.Second)
	record := &store.ScheduleRecord{
		SchemaVersion: store.ScheduleRecordSchemaVersion,
		ScheduleID:    chainScheduleID,
		State:         store.ScheduleStateRunning,
		CampaignSeed:  42,
		Document: app.ScheduleDocument{
			Name: "synthesized campaign",
			Seed: 42,
			Base: app.JobConfig{RefPath: "assets/ref.png", Mode: app.ModeBatch, Circles: 8, Iters: 100, PopSize: 30, Seed: 42},
		},
	}
	stages := []store.ScheduleStageRecord{
		{
			Index: 0, Kind: app.ScheduleStageBase, State: store.ScheduleStateCompleted,
			Circles: 8, BestCost: 812.5, JobID: chainBaseJob,
			StartedAt: &started, CompletedAt: &completed,
		},
		{
			Index: 1, Kind: app.ScheduleStagePolish, State: store.ScheduleStateSkipped,
			Circles: 8, Reason: "polishing stopped paying after two barren stages",
		},
		{
			Index: 2, Kind: app.ScheduleStageExtend, State: store.ScheduleStateRunning,
			Circles: 16, JobID: chainExtendJob, ParentJobID: chainBaseJob, StartedAt: &started,
		},
	}

	campaign := campaignFromSchedule(record, stages)
	if campaign.Name != "synthesized campaign" || !campaign.HasSeed {
		t.Fatalf("campaign header = %+v, want the document name and seed", campaign)
	}

	if len(campaign.Stages) != 3 {
		t.Fatalf("campaign stages = %d, want 3", len(campaign.Stages))
	}

	base := campaign.Stages[0]
	if !base.HasBestCost || base.BestCost != 812.5 {
		t.Errorf("base cost = (%v, %f), want the recorded 812.5", base.HasBestCost, base.BestCost)
	}

	if !base.HasPSNR {
		t.Error("base stage has no PSNR, but a cost is all PSNR needs")
	}

	if !base.HasElapsed || base.ElapsedSec != 90 {
		t.Errorf("base elapsed = (%v, %f), want 90s", base.HasElapsed, base.ElapsedSec)
	}

	if base.AcceptedSweeps != nil {
		t.Error("base stage claims an accepted-sweep count, which nothing persists")
	}

	skipped := campaign.Stages[1]
	if skipped.State != string(store.ScheduleStateSkipped) || skipped.Note == "" {
		t.Errorf("skipped stage = %+v, want the policy reason carried through", skipped)
	}

	if skipped.HasBestCost {
		t.Error("skipped stage reports a cost, but it never ran")
	}

	running := campaign.Stages[2]
	if running.HasBestCost || running.HasElapsed {
		t.Errorf("running stage = %+v, want no cost and no elapsed until it finishes", running)
	}

	if running.ElapsedAbsent == "" {
		t.Error("running stage does not say why its elapsed column is empty")
	}
}

func TestCampaignListPageShowsSchedulesAndChains(t *testing.T) {
	fixture := newScheduleFixture(t, 1)
	synthesizeChain(t, fixture.server.store, fixture.imagePath, defaultSynthesizedChain())

	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/schedules", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("campaign list status = %d", recorder.Code)
	}

	body := recorder.Body.String()
	for _, marker := range []string{
		"Imported chains",
		"/chains/" + chainSecondJob,
		"from " + chainBaseJob[:8],
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("campaign list missing %q", marker)
		}
	}
}

// TestChainStageStateMatchesRestore pins the imported-stage state mapping to
// the one jobFromCheckpoint uses. The two read the same field for the same
// purpose, and a chain that says "completed" where a restored job says
// "cancelled" is the contradiction this guards against.
func TestChainStageStateMatchesRestore(t *testing.T) {
	terminations := []string{
		"completed", "target_cost", "stagnation", "convergence", "stage_convergence", "refill_limit",
		"failed", "cancelled",
		"", store.TerminationUnknown, store.TerminationLegacy,
		"something_a_future_version_writes",
	}
	for _, termination := range terminations {
		t.Run("termination="+termination, func(t *testing.T) {
			checkpoint := &store.Checkpoint{JobID: chainBaseJob, Termination: termination}

			want := string(jobFromCheckpoint(checkpoint, app.DefaultProject).State)
			if got := chainStageState(termination); got != want {
				t.Fatalf("chainStageState(%q) = %q, want %q", termination, got, want)
			}
		})
	}
}

// TestChainListingReportsTheLeafTermination keeps the campaign card and the
// campaign detail page telling the same story about how a chain ended.
func TestChainListingReportsTheLeafTermination(t *testing.T) {
	timestamp := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		termination string
		want        string
	}{
		{termination: "completed", want: "completed"},
		{termination: "failed", want: "failed"},
		{termination: "cancelled", want: "cancelled"},
		{termination: store.TerminationUnknown, want: "cancelled"},
		{termination: store.TerminationLegacy, want: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.termination, func(t *testing.T) {
			infos := []store.CheckpointInfo{
				{JobID: chainBaseJob, Termination: "completed", ActualCircles: 8, Timestamp: timestamp},
				{
					JobID: chainExtendJob, ExtendedFrom: chainBaseJob, Termination: test.termination,
					ActualCircles: 16, Timestamp: timestamp.Add(time.Hour),
				},
			}

			summaries := chainCampaignSummaries(discoverChains(infos))
			if len(summaries) != 1 {
				t.Fatalf("summarized %d chains, want 1", len(summaries))
			}

			if summaries[0].State != test.want {
				t.Fatalf("summary state = %q, want %q", summaries[0].State, test.want)
			}
		})
	}
}

// TestCampaignSeedFallsBackToARecordedStage covers a document that omitted the
// seed: the record keeps the zero sentinel, but the stage that ran recorded the
// seed it resolved, and that is the reproducible value the view must show.
func TestCampaignSeedFallsBackToARecordedStage(t *testing.T) {
	record := &store.ScheduleRecord{
		SchemaVersion: store.ScheduleRecordSchemaVersion,
		ScheduleID:    chainScheduleID,
		State:         store.ScheduleStateRunning,
		Document: app.ScheduleDocument{
			Base: app.JobConfig{RefPath: "assets/ref.png", Mode: app.ModeBatch, Circles: 8, Iters: 100, PopSize: 30},
		},
	}
	if campaign := campaignFromSchedule(record, nil); campaign.HasSeed {
		t.Fatalf("an unstarted campaign must not claim a seed, got %d", campaign.CampaignSeed)
	}

	stages := []store.ScheduleStageRecord{{
		Index: 0, Kind: app.ScheduleStageBase, State: store.ScheduleStateCompleted,
		Circles: 8, JobID: chainBaseJob,
		Config: store.JobConfig{RefPath: "assets/ref.png", EffectiveSeed: 987654321},
	}}

	campaign := campaignFromSchedule(record, stages)
	if !campaign.HasSeed || campaign.CampaignSeed != 987654321 {
		t.Fatalf("campaign seed = %d (has=%v), want the seed the stage recorded",
			campaign.CampaignSeed, campaign.HasSeed)
	}
}

// TestChainDiscoveryCoversEveryProject guards the campaign listing against
// silently omitting a chain that /chains/:jobID renders perfectly well: the
// detail route resolves a job through its own project store, so discovery has
// to look in the same places.
func TestChainDiscoveryCoversEveryProject(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	imagePath := filepath.Join(root, "reference.png")
	createSimpleTestImage(t, imagePath)

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{
		DataRoot:   root,
		InputRoots: rootList(root),
	})

	projectStore, err := server.projects.GetOrCreate(app.Project("wallpaper"))
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	synthesizeChain(t, persistence, imagePath, []synthesizedStage{
		{jobID: chainBaseJob, circles: 8, cost: 812.5},
		{jobID: chainExtendJob, parent: chainBaseJob, circles: 16, cost: 640.25},
	})
	synthesizeChain(t, projectStore, imagePath, []synthesizedStage{
		{jobID: chainPolishJob, circles: 8, cost: 700},
		{jobID: chainSecondJob, parent: chainPolishJob, circles: 16, cost: 500},
	})

	found := make(map[string]bool)
	for _, chain := range server.discoverAllChains() {
		found[chain.LeafJobID] = true
	}

	if !found[chainExtendJob] {
		t.Error("the default project's chain is missing from the campaign listing")
	}

	if !found[chainSecondJob] {
		t.Error("a named project's chain is missing from the campaign listing")
	}
}

func TestChainListingsShareInvalidationBasedDiscoveryCache(t *testing.T) {
	root := t.TempDir()

	fsStore, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	synthesizeChain(t, fsStore, "reference.png", []synthesizedStage{{jobID: chainBaseJob, circles: 8, cost: 10}})
	counting := &checkpointListCountingStore{Store: fsStore}
	server := NewServer("localhost:0", counting)
	baseline := counting.listCount() // restore performs its own listing

	for _, path := range []string{"/api/v1/dashboard", "/api/v1/chains", "/api/v1/dashboard"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body=%q", path, response.Code, response.Body.String())
		}
	}

	if got := counting.listCount(); got != baseline+1 {
		t.Fatalf("shared listings performed %d checkpoint scans after baseline %d, want one", got, baseline)
	}

	job := server.jobManager.CreateJob(app.DefaultProject, store.JobConfig{
		RefPath: "reference.png", Mode: app.ModeBatch, Circles: 1, Iters: 1, PopSize: 1,
	})
	server.cachedAllChains()

	if got := counting.listCount(); got != baseline+2 {
		t.Fatalf("job creation did not invalidate chain cache: scans=%d", got)
	}

	err = server.jobManager.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.CompleteJob(job.ID, 1, 1, make([]float64, 7), 1, 2, "completed")
	if err != nil {
		t.Fatal(err)
	}

	server.cachedAllChains()

	if got := counting.listCount(); got != baseline+3 {
		t.Fatalf("job completion did not invalidate chain cache: scans=%d", got)
	}
}

func TestCampaignViewAPIUsesSourceNeutralReadModel(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	imagePath := filepath.Join(root, "reference.png")
	createSimpleTestImage(t, imagePath)

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{
		DataRoot: root, InputRoots: rootList(root),
	})
	shutdownTestServer(t, server)
	synthesizeChain(t, persistence, imagePath, defaultSynthesizedChain())

	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil))

	if listResponse.Code != http.StatusOK {
		t.Fatalf("campaign list status = %d, body=%q", listResponse.Code, listResponse.Body.String())
	}

	var list campaignViewList

	err = json.NewDecoder(listResponse.Body).Decode(&list)
	if err != nil {
		t.Fatalf("decode campaign list: %v", err)
	}

	if len(list.Schedules) != 0 || len(list.Chains) != 1 || list.Chains[0].Source != "chain" {
		t.Fatalf("campaign list = %+v", list)
	}

	detailResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailResponse, httptest.NewRequest(http.MethodGet, "/api/v1/campaigns/chain/"+chainSecondJob, nil))

	if detailResponse.Code != http.StatusOK {
		t.Fatalf("campaign detail status = %d, body=%q", detailResponse.Code, detailResponse.Body.String())
	}

	var campaign ui.Campaign

	err = json.NewDecoder(detailResponse.Body).Decode(&campaign)
	if err != nil {
		t.Fatalf("decode campaign detail: %v", err)
	}

	if campaign.ID != chainSecondJob || campaign.Source != "chain" || len(campaign.Stages) != len(defaultSynthesizedChain()) {
		t.Fatalf("campaign detail = %+v", campaign)
	}
}

func inOrder(text string, markers ...string) bool {
	cursor := 0
	for _, marker := range markers {
		index := strings.Index(text[cursor:], marker)
		if index < 0 {
			return false
		}

		cursor += index + len(marker)
	}

	return true
}

// growthCampaignRecord is the campaign the estimator was built from, as far as
// the view is concerned: a plan to 3000 circles, measured at 1000 and 2000, and
// a population raised without the epochs to spend it.
func growthCampaignRecord(t *testing.T) (*store.ScheduleRecord, []store.ScheduleStageRecord) {
	t.Helper()

	document, err := app.ParseSchedule([]byte(`{
  "name": "growth campaign",
  "seed": 42,
  "base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 1000, "batchSize": 1, "iters": 100, "popSize": 100},
  "steps": [{"type": "extend", "additionalCircles": 1000, "repeat": 2}]
}`))
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}

	record := &store.ScheduleRecord{
		SchemaVersion: store.ScheduleRecordSchemaVersion,
		ScheduleID:    chainScheduleID,
		State:         store.ScheduleStateRunning,
		CampaignSeed:  42,
		Document:      *document,
	}

	started := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	firstEnd := started.Add(30 * time.Minute)
	secondStart := started.Add(time.Hour)
	secondEnd := secondStart.Add(30 * time.Minute)

	stages := []store.ScheduleStageRecord{
		{
			Index: 0, Kind: app.ScheduleStageBase, State: store.ScheduleStateCompleted,
			Circles: 1000, BestCost: 96.199, JobID: chainBaseJob,
			StartedAt: &started, CompletedAt: &firstEnd,
		},
		{
			Index: 1, Kind: app.ScheduleStageExtend, State: store.ScheduleStateCompleted,
			Circles: 2000, BestCost: 64.602, JobID: chainExtendJob, ParentJobID: chainBaseJob,
			StartedAt: &secondStart, CompletedAt: &secondEnd,
		},
	}

	return record, stages
}

// TestCampaignFromScheduleCarriesAdvisoriesAndProjection covers the two things
// a planned campaign knows that an imported chain cannot: what its document
// says about its own budget, and where the stages it has left are heading.
func TestCampaignFromScheduleCarriesAdvisoriesAndProjection(t *testing.T) {
	t.Parallel()

	record, stages := growthCampaignRecord(t)

	campaign := campaignFromSchedule(record, stages)
	if len(campaign.Warnings) != 1 {
		t.Fatalf("campaign carried %d warnings, want the one its document earns: %v",
			len(campaign.Warnings), campaign.Warnings)
	}

	if !strings.Contains(campaign.Warnings[0], "base.popSize") {
		t.Errorf("warning does not name the field it is about: %q", campaign.Warnings[0])
	}

	if campaign.Projection == nil {
		t.Fatal("a running campaign with a stage left carries no projection")
	}

	projection := *campaign.Projection
	if !projection.Projected || projection.Samples != 2 {
		t.Fatalf("projection = (projected %v, %d samples), want projected over 2",
			projection.Projected, projection.Samples)
	}

	if projection.LatestCircles != 2000 || projection.RemainingCircles != 1000 {
		t.Errorf("projection stands at %d circles with %d to go, want 2000 and 1000",
			projection.LatestCircles, projection.RemainingCircles)
	}

	if !projection.HasCircleCeiling || math.Abs(projection.CostAtPlanEnd-33.005) > 1e-6 {
		t.Errorf("cost at the plan's end = (%v, %f), want a projected 33.005",
			projection.HasCircleCeiling, projection.CostAtPlanEnd)
	}

	// The card shows a PSNR beside the cost, and derives it here rather than in
	// the browser, for the same reason every stage row does.
	if !projection.HasPlanEndPSNR || projection.PlanEndPSNRInfinite {
		t.Errorf("plan-end PSNR = (%v, infinite %v), want a finite figure",
			projection.HasPlanEndPSNR, projection.PlanEndPSNRInfinite)
	}
}

// TestCampaignFromChainHasNothingToAdviseOrProject pins the deliberate gap: a
// lineage walked backwards out of checkpoints was never planned, so there is no
// authoring site an advisory could name and no remaining stage to project
// towards. Empty here is the honest answer, not a missing feature.
func TestCampaignFromChainHasNothingToAdviseOrProject(t *testing.T) {
	t.Parallel()

	chain := []*store.Checkpoint{
		{JobID: chainBaseJob, ActualCircles: 1000, BestCost: 96.199, Termination: "completed"},
		{
			JobID: chainExtendJob, ExtendedFrom: chainBaseJob, ActualCircles: 2000,
			BestCost: 64.602, Termination: "completed",
		},
	}

	campaign := campaignFromChain(chainExtendJob, chain)
	if len(campaign.Warnings) != 0 {
		t.Errorf("an imported chain has no document, but carried warnings: %v", campaign.Warnings)
	}

	if campaign.Projection != nil {
		t.Errorf("an imported chain has no plan, but carried a projection: %+v", campaign.Projection)
	}
}
