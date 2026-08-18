package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
	"github.com/google/uuid"
)

func TestHandleDashboardMethodNotAllowed(t *testing.T) {
	fixture := newScheduleFixture(t, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard", nil)
	rec := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleDashboardBuildsCampaignsJobsAndHostFacts(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	scheduleStore, err := fixture.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	base := time.Now().UTC().Add(-time.Hour)

	for i := 0; i < dashboardCampaignLimit+4; i++ {
		state := store.ScheduleStateCompleted
		if i == dashboardCampaignLimit+3 {
			state = store.ScheduleStateRunning
		} else if i == dashboardCampaignLimit+2 {
			state = store.ScheduleStatePending
		}
		saveCampaignScheduleForDashboard(
			t,
			scheduleStore,
			fixture.imagePath,
			state,
			base.Add(time.Duration(i)*time.Minute),
			"campaign-"+uuid.NewString(),
			uuid.NewString(),
		)
	}

	fixture.server.jobManager.CreateJob(app.DefaultProject, app.JobConfig{
		RefPath: fixture.imagePath, Mode: app.ModeBatch, Circles: 8, Iters: 100, PopSize: 30, Seed: 42,
	})
	running := fixture.server.jobManager.CreateJob(app.DefaultProject, app.JobConfig{
		RefPath: fixture.imagePath, Mode: app.ModeBatch, Circles: 8, Iters: 100, PopSize: 30, Seed: 99,
	})
	completed := fixture.server.jobManager.CreateJob(app.DefaultProject, app.JobConfig{
		RefPath: fixture.imagePath, Mode: app.ModeBatch, Circles: 8, Iters: 100, PopSize: 30, Seed: 123,
	})

	// Force stable states for aggregate assertions and the running row.
	if err := fixture.server.jobManager.UpdateJob(running.ID, func(job *Job) {
		job.State = StateRunning
		job.StartTime = time.Now().Add(-2 * time.Second)
		job.Iterations = 55
		job.BestCost = 42.5
		job.InitialCost = 100
		job.Evaluations = 120
		job.BestParams = make([]float64, 56)
		for i := range 200 {
			job.MetricHistory = append(job.MetricHistory, MetricSample{
				Iteration: i,
				Cost:      100 - float64(i)*0.5,
				Timestamp: time.Now().Add(-time.Second * time.Duration(200-i)),
			})
		}
	}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}
	if err := fixture.server.jobManager.UpdateJob(completed.ID, func(job *Job) {
		done := time.Now()
		job.State = StateCompleted
		job.BestCost = 9.9
		job.InitialCost = 12
		job.EndTime = &done
	}); err != nil {
		t.Fatalf("seed completed job: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	rec := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var response dashboardResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}

	if len(response.Campaigns) != dashboardCampaignLimit {
		t.Fatalf("campaign count = %d, want %d", len(response.Campaigns), dashboardCampaignLimit)
	}
	if response.Campaigns[0].State != string(store.ScheduleStateRunning) {
		t.Fatalf("first campaign state = %q, want running", response.Campaigns[0].State)
	}
	if response.Campaigns[1].State != string(store.ScheduleStatePending) {
		t.Fatalf("second campaign state = %q, want pending", response.Campaigns[1].State)
	}
	for i := 2; i < len(response.Campaigns); i++ {
		if response.Campaigns[i].State == string(store.ScheduleStateRunning) || response.Campaigns[i].State == string(store.ScheduleStatePending) {
			t.Fatalf("campaign %d state = %q, want non-active", i, response.Campaigns[i].State)
		}
	}

	if response.Aggregates.Running != 1 || response.Aggregates.Pending != 1 || response.Aggregates.Completed != 1 {
		t.Fatalf("aggregates = %+v, want running=1 pending=1 completed=1", response.Aggregates)
	}
	if response.Aggregates.CPS <= 0 {
		t.Fatalf("aggregates cps = %f, want > 0", response.Aggregates.CPS)
	}
	if len(response.RunningJobs) != 1 {
		t.Fatalf("running jobs = %d, want 1", len(response.RunningJobs))
	}
	if response.RunningJobs[0].ID != running.ID {
		t.Fatalf("running job id = %q, want %q", response.RunningJobs[0].ID, running.ID)
	}
	if response.RunningJobs[0].Iterations != 55 {
		t.Fatalf("running iterations = %d, want 55", response.RunningJobs[0].Iterations)
	}
	if response.RunningJobs[0].Project != app.DefaultProject {
		t.Fatalf("running project = %q, want %q", response.RunningJobs[0].Project, app.DefaultProject)
	}
	if response.RunningJobs[0].MaxIters <= 0 {
		t.Fatalf("running max iters = %d, want > 0", response.RunningJobs[0].MaxIters)
	}
	if len(response.RunningJobs[0].MetricHistory) != dashboardMetricHistoryLimit {
		t.Fatalf("running metric history = %d, want %d", len(response.RunningJobs[0].MetricHistory), dashboardMetricHistoryLimit)
	}

	if response.HostFacts.Version == "" {
		t.Fatalf("host facts version is missing")
	}

	runningCampaign := response.Campaigns[0]
	if len(runningCampaign.CampaignSeries) != 1 {
		t.Fatalf("campaign series = %d points, want 1", len(runningCampaign.CampaignSeries))
	}
	point := runningCampaign.CampaignSeries[0]
	if point.Kind != string(app.ScheduleStageBase) || point.Circles != 4 || point.BestCost != 10 {
		t.Fatalf("campaign series point = %+v, want a base stage of 4 circles at cost 10", point)
	}
	if point.HasBestCost {
		t.Fatalf("a running stage reported a best cost")
	}
	if runningCampaign.LeafJobID != "" {
		t.Fatalf("running campaign leaf job = %q, want empty until a stage completes", runningCampaign.LeafJobID)
	}
	completedCampaign := response.Campaigns[2]
	if !completedCampaign.CampaignSeries[0].HasBestCost {
		t.Fatalf("a completed stage reported no best cost")
	}
	if completedCampaign.LeafJobID == "" {
		t.Fatalf("completed campaign has no leaf job for its thumbnail")
	}
}

// TestDashboardChainCampaignCarriesRunOrderSeries pins the series a chain
// contributes: the mini chart plots cost against circles in run order, so the
// base stage has to come first even though discovery walks from the leaf up.
func TestDashboardChainCampaignCarriesRunOrderSeries(t *testing.T) {
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
	synthesizeChain(t, persistence, imagePath, defaultSynthesizedChain())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var response dashboardResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if len(response.Campaigns) != 1 {
		t.Fatalf("campaigns = %d, want the one synthesized chain", len(response.Campaigns))
	}

	campaign := response.Campaigns[0]
	if campaign.LeafJobID != chainSecondJob {
		t.Fatalf("leaf job = %q, want %q", campaign.LeafJobID, chainSecondJob)
	}
	wantKinds := []string{"base", "extend", "polish", "extend"}
	wantCircles := []int{8, 16, 16, 24}
	wantCosts := []float64{812.5, 640.25, 631.75, 572.357}
	if len(campaign.CampaignSeries) != len(wantKinds) {
		t.Fatalf("series = %d points, want %d", len(campaign.CampaignSeries), len(wantKinds))
	}
	for i, point := range campaign.CampaignSeries {
		if point.Index != i || point.Kind != wantKinds[i] || point.Circles != wantCircles[i] || point.BestCost != wantCosts[i] {
			t.Fatalf("series point %d = %+v, want index %d kind %q circles %d cost %g",
				i, point, i, wantKinds[i], wantCircles[i], wantCosts[i])
		}
		if !point.HasBestCost {
			t.Fatalf("chain series point %d reported no best cost", i)
		}
	}
}

func TestSortDashboardCampaigns(t *testing.T) {
	campaigns := []ui.CampaignSummary{
		{ID: "1", State: string(store.ScheduleStateCompleted), UpdatedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
		{ID: "2", State: string(store.ScheduleStateRunning), UpdatedAt: time.Date(2026, 8, 1, 12, 3, 0, 0, time.UTC)},
		{ID: "3", State: string(store.ScheduleStatePending), UpdatedAt: time.Date(2026, 8, 1, 12, 2, 0, 0, time.UTC)},
	}

	sortDashboardCampaigns(campaigns)

	if campaigns[0].ID != "2" || campaigns[1].ID != "3" || campaigns[2].ID != "1" {
		t.Fatalf("sorted campaign order = %v, want 2,3,1", []string{campaigns[0].ID, campaigns[1].ID, campaigns[2].ID})
	}
}

func TestDashboardRunningJobFromBoundsMetricHistory(t *testing.T) {
	fixture := newScheduleFixture(t, 1)
	job := fixture.server.jobManager.CreateJob(app.DefaultProject, app.JobConfig{
		RefPath: fixture.imagePath, Mode: app.ModeBatch, Circles: 8, Iters: 100, PopSize: 30, Seed: 42,
	})
	if err := fixture.server.jobManager.UpdateJob(job.ID, func(job *Job) {
		job.State = StateRunning
		job.StartTime = time.Now().Add(-time.Second)
		job.Evaluations = 10
		job.BestCost = 12.5
		job.BestParams = make([]float64, 56)
		for i := 0; i < dashboardMetricHistoryLimit+10; i++ {
			job.MetricHistory = append(job.MetricHistory, MetricSample{
				Iteration: i,
				Cost:      float64(i),
				Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
			})
		}
	}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}

	jobState, ok := fixture.server.jobManager.GetJob(job.ID)
	if !ok {
		t.Fatalf("missing seeded job")
	}
	row := dashboardRunningJobFrom(jobState)
	if row.Project != app.DefaultProject {
		t.Fatalf("row project = %q, want %q", row.Project, app.DefaultProject)
	}
	if len(row.MetricHistory) != dashboardMetricHistoryLimit {
		t.Fatalf("metric history = %d, want %d", len(row.MetricHistory), dashboardMetricHistoryLimit)
	}
	if row.CPS <= 0 {
		t.Fatalf("cps = %f, want >0", row.CPS)
	}
	if row.ElapsedSec <= 0 {
		t.Fatalf("elapsed seconds = %f, want >0", row.ElapsedSec)
	}
}

func saveCampaignScheduleForDashboard(
	t *testing.T,
	scheduleStore store.ScheduleStore,
	imagePath string,
	state store.ScheduleState,
	updated time.Time,
	name string,
	stageJobID string,
) string {
	t.Helper()
	scheduleID := uuid.NewString()
	baseConfig := app.DefaultConfig()
	baseConfig.RefPath = imagePath
	baseConfig.Mode = app.ModeBatch
	baseConfig.Circles = 4
	baseConfig.Iters = 10
	baseConfig.PopSize = 30
	baseConfig.BatchSize = 4
	baseConfig.PolishingActiveSetSize = 4
	baseConfig.Seed = 42
	baseConfig.Backend = "cpu"
	baseConfig.Variant = app.VariantStandard

	stageConfig := baseConfig

	record, err := store.NewScheduleRecord(scheduleID, app.ScheduleDocument{
		Name: name,
		Base: baseConfig,
	})
	if err != nil {
		t.Fatalf("build schedule record: %v", err)
	}
	record.State = state
	record.UpdatedAt = updated
	if err := scheduleStore.SaveSchedule(record); err != nil {
		t.Fatalf("save schedule %s: %v", scheduleID, err)
	}
	stage := store.NewScheduleStageRecord(scheduleID, app.ScheduleStage{
		Index:             0,
		Kind:              app.ScheduleStageBase,
		StepIndex:         0,
		Repetition:        0,
		Circles:           4,
		AdditionalCircles: 0,
		Config:            stageConfig,
	})
	stage.State = state
	stage.UpdatedAt = updated
	stage.JobID = stageJobID
	stage.BestCost = 10
	stage.Iterations = 1
	stage.Evaluations = 1
	if err := scheduleStore.SaveScheduleStage(scheduleID, stage); err != nil {
		t.Fatalf("save schedule stage %s: %v", scheduleID, err)
	}
	return scheduleID
}
