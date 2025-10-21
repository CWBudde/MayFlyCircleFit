package server

import (
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
