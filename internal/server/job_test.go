package server

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/google/uuid"
)

func TestJobManagerBestRevisionAdvancesOnlyForStrictImprovements(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})
	err := jm.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateProgress(job.ID, 1, 10, []float64{1}, 3)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateProgress(job.ID, 2, 20, []float64{2}, 3)
	if err != nil {
		t.Fatal(err)
	}

	unchanged, _ := jm.GetJob(job.ID)
	if unchanged.BestRevision != 1 || !reflect.DeepEqual(unchanged.BestParams, []float64{1}) {
		t.Fatalf("equal-cost update changed best result: revision %d params %v", unchanged.BestRevision, unchanged.BestParams)
	}

	err := jm.UpdateProgress(job.ID, 3, 30, []float64{3}, 2)
	if err != nil {
		t.Fatal(err)
	}

	improved, _ := jm.GetJob(job.ID)
	if improved.BestRevision != 2 || improved.BestCost != 2 || !reflect.DeepEqual(improved.BestParams, []float64{3}) {
		t.Fatalf("improved result = revision %d cost %v params %v", improved.BestRevision, improved.BestCost, improved.BestParams)
	}
}

func TestJobManagerCandidateProgressIsProvisional(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})
	err := jm.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateProgress(job.ID, 10, 100, []float64{1, 2}, 100)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateCandidateProgress(job.ID, 11, 110, 95.25)
	if err != nil {
		t.Fatal(err)
	}

	progress, _ := jm.GetJob(job.ID)
	if progress.CandidateCost == nil || *progress.CandidateCost != 95.25 {
		t.Fatalf("candidate cost = %v, want 95.25", progress.CandidateCost)
	}

	if progress.BestCost != 100 || progress.BestRevision != 1 || !reflect.DeepEqual(progress.BestParams, []float64{1, 2}) {
		t.Fatalf("candidate mutated audited best: cost=%v revision=%d params=%v", progress.BestCost, progress.BestRevision, progress.BestParams)
	}

	if progress.Iterations != 11 || progress.Evaluations != 110 {
		t.Fatalf("candidate counters = %d/%d, want 11/110", progress.Iterations, progress.Evaluations)
	}

	err := jm.ClearCandidateProgress(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	cleared, _ := jm.GetJob(job.ID)
	if cleared.CandidateCost != nil || cleared.BestCost != 100 {
		t.Fatalf("cleared candidate = %v, audited cost = %v", cleared.CandidateCost, cleared.BestCost)
	}
}

func TestJobManagerCandidateProgressKeepsBestCandidateAndClearsAtTerminalState(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})
	err := jm.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateProgress(job.ID, 1, 10, []float64{1}, 100)
	if err != nil {
		t.Fatal(err)
	}

	for iteration, cost := range []float64{98, 99, 97} {
		err := jm.UpdateCandidateProgress(job.ID, iteration+2, (iteration+2)*10, cost)
		if err != nil {
			t.Fatal(err)
		}
	}

	progress, _ := jm.GetJob(job.ID)
	if progress.CandidateCost == nil || *progress.CandidateCost != 97 {
		t.Fatalf("candidate cost = %v, want best provisional cost 97", progress.CandidateCost)
	}

	err := jm.CancelJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	cancelled, _ := jm.GetJob(job.ID)
	if cancelled.CandidateCost != nil {
		t.Fatalf("terminal job retained candidate cost %v", *cancelled.CandidateCost)
	}
}

func TestJobManager_CreateJob(t *testing.T) {
	jm := NewJobManager()

	config := JobConfig{
		RefPath: "test.png",
		Mode:    "joint",
		Circles: 10,
		Iters:   100,
		PopSize: 30,
		Seed:    42,
	}

	job := jm.CreateJob(app.DefaultProject, config)

	if job.ID == "" {
		t.Error("Job ID should not be empty")
	}

	if job.State != StatePending {
		t.Errorf("Initial state should be pending, got %s", job.State)
	}

	if job.Config.RefPath != "test.png" {
		t.Errorf("Config not set correctly")
	}
}

func TestJobManagerLegalTransitions(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})
	err := jm.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateProgress(job.ID, 1, 20, []float64{1, 2}, 4)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.CompleteJob(job.ID, 2, 40, []float64{2, 3}, 3, 10, "completed")
	if err != nil {
		t.Fatal(err)
	}

	err := jm.CancelJob(job.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal transition error = %v", err)
	}

	err := jm.DeleteJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := jm.GetJob(job.ID); ok {
		t.Fatal("deleted job remains visible")
	}
}

func TestJobManagerRejectsRegressingProgress(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})
	err := jm.StartJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateProgress(job.ID, 2, 40, []float64{1}, 3)
	if err != nil {
		t.Fatal(err)
	}

	err := jm.UpdateProgress(job.ID, 1, 20, []float64{2}, 2)
	if err == nil {
		t.Fatal("regressing progress was accepted")
	}
}

func TestJobManager_GetJob(t *testing.T) {
	jm := NewJobManager()

	config := JobConfig{RefPath: "test.png", Mode: "joint"}
	job := jm.CreateJob(app.DefaultProject, config)

	retrieved, exists := jm.GetJob(job.ID)
	if !exists {
		t.Error("Job should exist")
	}

	if retrieved.ID != job.ID {
		t.Error("Retrieved wrong job")
	}

	_, exists = jm.GetJob("nonexistent")
	if exists {
		t.Error("Should not find nonexistent job")
	}
}

func TestJobManager_ReturnsDetachedSnapshots(t *testing.T) {
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})

	params := []float64{1, 2, 3}
	end := time.Now()

	err := jm.UpdateJob(job.ID, func(live *Job) {
		live.BestParams = params
		live.EndTime = &end
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found")
	}

	snapshot.BestParams[0] = 99
	*snapshot.EndTime = snapshot.EndTime.Add(time.Hour)
	snapshot.State = StateFailed

	unchanged, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found")
	}

	if unchanged.BestParams[0] != 1 {
		t.Fatalf("mutable parameter slice escaped: %v", unchanged.BestParams)
	}

	if !unchanged.EndTime.Equal(end) {
		t.Fatalf("mutable end time escaped: got %v, want %v", unchanged.EndTime, end)
	}

	if unchanged.State == StateFailed {
		t.Fatal("mutable job snapshot escaped")
	}
}

func TestJobManagerRecordsCompleteDetachedMetricHistory(t *testing.T) {
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})
	psnr, ssim := 30.0, 0.8

	const sampleCount = 105
	for i := range sampleCount {
		err := jm.RecordMetrics(job.ID, MetricSample{
			Iteration: i, Cost: float64(sampleCount - i), PSNR: &psnr, SSIM: &ssim, Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	snapshot, ok := jm.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found")
	}

	if len(snapshot.MetricHistory) != sampleCount || snapshot.MetricHistory[0].Iteration != 0 {
		t.Fatalf("complete history = %#v", snapshot.MetricHistory)
	}

	*snapshot.PSNR = 99
	*snapshot.SSIM = 0
	snapshot.MetricHistory[0].Cost = -1
	snapshot.MetricHistory[0].PSNR = nil

	unchanged, _ := jm.GetJob(job.ID)
	if *unchanged.PSNR != psnr || *unchanged.SSIM != ssim || unchanged.MetricHistory[0].Cost < 0 || unchanged.MetricHistory[0].PSNR == nil {
		t.Fatal("mutable metric state escaped from the job manager")
	}
}

func TestJobManager_ListJobs(t *testing.T) {
	jm := NewJobManager()

	if len(jm.ListJobs()) != 0 {
		t.Error("Should start with no jobs")
	}

	jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test1.png"})
	jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test2.png"})

	jobs := jm.ListJobs()
	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}
}

func TestJobManagerListSummariesDoesNotCarryOptimizerHistory(t *testing.T) {
	jm := NewJobManager()
	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})
	end := time.Now()
	candidate := 5.0

	err := jm.UpdateJob(job.ID, func(live *Job) {
		live.BestParams = make([]float64, 21_000)
		live.MetricHistory = make([]MetricSample, 10_000)
		live.CandidateCost = &candidate
		live.EndTime = &end
	})
	if err != nil {
		t.Fatal(err)
	}

	summaries := jm.ListJobSummaries()
	if len(summaries) != 1 || summaries[0].ID != job.ID {
		t.Fatalf("summaries = %+v", summaries)
	}

	*summaries[0].CandidateCost = 99
	*summaries[0].EndTime = time.Time{}

	unchanged, _ := jm.GetJob(job.ID)
	if *unchanged.CandidateCost != candidate || unchanged.EndTime.IsZero() {
		t.Fatal("job summary aliases live job state")
	}
}

func TestJobManager_UpdateJob(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})

	err := jm.UpdateJob(job.ID, func(j *Job) {
		j.State = StateRunning
		j.Iterations = 10
		j.BestCost = 123.45
	})
	if err != nil {
		t.Errorf("Update should succeed: %v", err)
	}

	updated, _ := jm.GetJob(job.ID)
	if updated.State != StateRunning {
		t.Error("State should be updated")
	}

	if updated.Iterations != 10 {
		t.Error("Iterations should be updated")
	}

	if updated.BestCost != 123.45 {
		t.Error("BestCost should be updated")
	}

	err = jm.UpdateJob("nonexistent", func(j *Job) {})
	if err == nil {
		t.Error("Update of nonexistent job should fail")
	}
}

func TestJobManager_ThreadSafety(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(app.DefaultProject, JobConfig{RefPath: "test.png"})

	// Simulate concurrent updates
	done := make(chan bool)

	for i := range 10 {
		go func(iteration int) {
			jm.UpdateJob(job.ID, func(j *Job) {
				j.Iterations = iteration

				time.Sleep(1 * time.Millisecond)
			})

			done <- true
		}(i)
	}

	// Wait for all updates
	for range 10 {
		<-done
	}

	// Should not crash - actual value depends on race
	_, exists := jm.GetJob(job.ID)
	if !exists {
		t.Error("Job should still exist after concurrent updates")
	}
}

// TestCreateJobWithIDHoldsTheIdentifierTheCallerChose covers the property the
// schedule executor depends on: a stage can name its job before that job
// exists, and no second job can then take that name.
func TestCreateJobWithIDHoldsTheIdentifierTheCallerChose(t *testing.T) {
	manager := NewJobManager()
	chosen := uuid.NewString()

	job, err := manager.CreateJobWithID(chosen, app.DefaultProject, JobConfig{RefPath: "ref.png"})
	if err != nil {
		t.Fatalf("CreateJobWithID() error = %v", err)
	}

	if job.ID != chosen {
		t.Fatalf("job ID = %q, want %q", job.ID, chosen)
	}

	if _, ok := manager.GetJob(chosen); !ok {
		t.Fatal("job was not registered under the chosen identifier")
	}

	if _, err := manager.CreateJobWithID(chosen, app.DefaultProject, JobConfig{RefPath: "ref.png"}); !errors.Is(err, errDuplicateJobID) {
		t.Fatalf("second create error = %v, want errDuplicateJobID", err)
	}

	minted, err := manager.CreateJobWithID("", app.DefaultProject, JobConfig{RefPath: "ref.png"})
	if err != nil {
		t.Fatalf("CreateJobWithID(\"\") error = %v", err)
	}

	if minted.ID == "" || minted.ID == chosen {
		t.Fatalf("minted ID = %q", minted.ID)
	}

	for _, bad := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"} {
		if _, err := manager.CreateJobWithID(bad, app.DefaultProject, JobConfig{}); err == nil {
			t.Fatalf("CreateJobWithID(%q) was accepted", bad)
		}
	}
}
