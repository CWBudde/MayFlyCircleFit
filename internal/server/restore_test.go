package server

import (
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/store"
)

func TestServerRestoresPersistedJobsAndHistory(t *testing.T) {
	persistence, err := store.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jobID := "12345678-1234-4234-8234-123456789abc"
	start := time.Date(2026, time.August, 14, 1, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	checkpoint := store.NewCheckpoint(jobID, make([]float64, 7), 5, 10, 100, store.JobConfig{
		RefPath: "test.png", Mode: "joint", Circles: 1, Iters: 100, PopSize: 20,
	})
	checkpoint.Evaluations = 2000
	checkpoint.Termination = "completed"
	checkpoint.Timestamp = end
	if err := persistence.SaveCheckpoint(jobID, checkpoint); err != nil {
		t.Fatal(err)
	}

	psnrStart, psnrEnd := 20.0, 23.0
	writer, err := persistence.NewTraceWriter(jobID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []store.TraceEntry{
		{Iteration: 0, Cost: 10, PSNR: &psnrStart, Timestamp: start},
		{Iteration: 100, Cost: 5, PSNR: &psnrEnd, Timestamp: end},
	} {
		if err := writer.Write(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	server := NewServer(":0", persistence)
	t.Cleanup(server.cancel)
	jobs := server.jobManager.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("restored jobs = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.ID != jobID || job.State != StateCompleted || job.Iterations != 100 || job.Evaluations != 2000 {
		t.Fatalf("unexpected restored job: %#v", job)
	}
	if !job.StartTime.Equal(start) || job.EndTime == nil || !job.EndTime.Equal(end) {
		t.Fatalf("restored lifecycle = %v to %v, want %v to %v", job.StartTime, job.EndTime, start, end)
	}
	if len(job.MetricHistory) != 2 || job.PSNR == nil || *job.PSNR != psnrEnd {
		t.Fatalf("restored metric history = %#v, PSNR = %v", job.MetricHistory, job.PSNR)
	}
}

func TestJobFromCheckpointTreatsRefillLimitAsCompleted(t *testing.T) {
	checkpoint := store.NewCheckpoint("12345678-1234-4234-8234-123456789abc", make([]float64, 7), 5, 10, 100, store.JobConfig{
		RefPath: "test.png", Mode: "batch", Circles: 1, Iters: 100, PopSize: 20, BatchSize: 1,
	})
	checkpoint.Termination = "refill_limit"
	job := jobFromCheckpoint(checkpoint)
	if job.State != StateCompleted || job.Termination != "refill_limit" {
		t.Fatalf("restored refill-limited job = %#v", job)
	}
}
