package server

import (
	"errors"
	"testing"
	"time"
)

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

	job := jm.CreateJob(config)

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
	job := jm.CreateJob(JobConfig{RefPath: "test.png"})
	if err := jm.StartJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := jm.UpdateProgress(job.ID, 1, 20, []float64{1, 2}, 4); err != nil {
		t.Fatal(err)
	}
	if err := jm.CompleteJob(job.ID, 2, 40, []float64{2, 3}, 3, 10, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := jm.CancelJob(job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal transition error = %v", err)
	}
	if err := jm.DeleteJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := jm.GetJob(job.ID); ok {
		t.Fatal("deleted job remains visible")
	}
}

func TestJobManagerRejectsRegressingProgress(t *testing.T) {
	jm := NewJobManager()
	job := jm.CreateJob(JobConfig{RefPath: "test.png"})
	if err := jm.StartJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := jm.UpdateProgress(job.ID, 2, 40, []float64{1}, 3); err != nil {
		t.Fatal(err)
	}
	if err := jm.UpdateProgress(job.ID, 1, 20, []float64{2}, 2); err == nil {
		t.Fatal("regressing progress was accepted")
	}
}

func TestJobManager_GetJob(t *testing.T) {
	jm := NewJobManager()

	config := JobConfig{RefPath: "test.png", Mode: "joint"}
	job := jm.CreateJob(config)

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
	job := jm.CreateJob(JobConfig{RefPath: "test.png"})

	params := []float64{1, 2, 3}
	end := time.Now()
	if err := jm.UpdateJob(job.ID, func(live *Job) {
		live.BestParams = params
		live.EndTime = &end
	}); err != nil {
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
	job := jm.CreateJob(JobConfig{RefPath: "test.png"})
	psnr, ssim := 30.0, 0.8
	const sampleCount = 105
	for i := 0; i < sampleCount; i++ {
		if err := jm.RecordMetrics(job.ID, MetricSample{
			Iteration: i, Cost: float64(sampleCount - i), PSNR: &psnr, SSIM: &ssim, Timestamp: time.Now(),
		}); err != nil {
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

	jm.CreateJob(JobConfig{RefPath: "test1.png"})
	jm.CreateJob(JobConfig{RefPath: "test2.png"})

	jobs := jm.ListJobs()
	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}
}

func TestJobManager_UpdateJob(t *testing.T) {
	jm := NewJobManager()

	job := jm.CreateJob(JobConfig{RefPath: "test.png"})

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

	job := jm.CreateJob(JobConfig{RefPath: "test.png"})

	// Simulate concurrent updates
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(iteration int) {
			jm.UpdateJob(job.ID, func(j *Job) {
				j.Iterations = iteration
				time.Sleep(1 * time.Millisecond)
			})
			done <- true
		}(i)
	}

	// Wait for all updates
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not crash - actual value depends on race
	_, exists := jm.GetJob(job.ID)
	if !exists {
		t.Error("Job should still exist after concurrent updates")
	}
}
