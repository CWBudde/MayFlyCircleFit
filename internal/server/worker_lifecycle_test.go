package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

func TestServerSupervisesCancellationAndBoundedQueue(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "reference.png")
	createSimpleTestImage(t, imagePath)

	server := NewServerWithOptions(":0", nil, ServerOptions{
		InputRoots: rootList(root), MaxConcurrentJobs: 1, QueueSize: 1,
	})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_ = server.Shutdown(ctx)
	})

	config := JobConfig{RefPath: imagePath, Mode: "joint", Circles: 8, Iters: 10_000, PopSize: 30, Seed: 42}
	first := server.jobManager.CreateJob(app.DefaultProject, config)
	second := server.jobManager.CreateJob(app.DefaultProject, config)

	third := server.jobManager.CreateJob(app.DefaultProject, config)

	err := server.enqueueJob(first.ID)
	if err != nil {
		t.Fatal(err)
	}

	waitForJobState(t, server.jobManager, first.ID, StateRunning)

	err = server.enqueueJob(second.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.enqueueJob(third.ID)
	if !errors.Is(err, ErrJobQueueFull) {
		t.Fatalf("third enqueue error = %v, want ErrJobQueueFull", err)
	}

	started := time.Now()

	err = server.requestCancellation(first.ID)
	if err != nil {
		t.Fatal(err)
	}

	waitForJobState(t, server.jobManager, first.ID, StateCancelled)

	if latency := time.Since(started); latency > 2*time.Second {
		t.Fatalf("cancellation latency %s exceeds 2s", latency)
	}

	_ = server.requestCancellation(second.ID)
}

func TestCancelledJobCanBeDeleted(t *testing.T) {
	server := NewServer(":0", nil)

	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: "unused.png"})

	err := server.requestCancellation(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.DeleteJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := server.jobManager.GetJob(job.ID); ok {
		t.Fatal("deleted job remains in manager")
	}
}

func TestLongJobPublishesProgressTraceAndCheckpoint(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "reference.png")
	createSimpleTestImage(t, imagePath)

	persistence, err := store.NewFSStore(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":0", persistence, ServerOptions{InputRoots: []string{root}})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_ = server.Shutdown(ctx)
	})

	config := JobConfig{
		RefPath: imagePath, Mode: "joint", Circles: 8, Iters: 10_000,
		PopSize: 30, Seed: 42, EffectiveSeed: 42,
		EnableTrace: true, CheckpointInterval: 1,
	}

	job := server.jobManager.CreateJob(app.DefaultProject, config)
	if err := server.enqueueJob(job.ID); err != nil {
		t.Fatal(err)
	}

	waitForJobState(t, server.jobManager, job.ID, StateRunning)

	deadline := time.Now().Add(5 * time.Second)

	var checkpoint *store.Checkpoint
	for time.Now().Before(deadline) {
		checkpoint, err = persistence.LoadCheckpoint(job.ID)
		if err == nil {
			break
		}

		time.Sleep(25 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("intermediate checkpoint not observed: %v", err)
	}

	if checkpoint.Iterations <= 0 || checkpoint.Evaluations <= 0 || len(checkpoint.BestParams) == 0 {
		t.Fatalf("checkpoint has no live progress: %+v", checkpoint)
	}

	if err := server.requestCancellation(job.ID); err != nil {
		t.Fatal(err)
	}

	waitForJobState(t, server.jobManager, job.ID, StateCancelled)

	reader, err := persistence.NewTraceReader(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	entries, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) < 2 {
		t.Fatalf("trace entries = %d, want live progress", len(entries))
	}
}

func waitForJobState(t *testing.T, manager *JobManager, jobID string, want JobState) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := manager.GetJob(jobID)
		if !ok {
			t.Fatalf("job %s disappeared", jobID)
		}

		if job.State == want {
			return
		}

		if job.State == StateFailed && want != StateFailed {
			t.Fatalf("job failed while waiting for %s: %s", want, job.Error)
		}

		time.Sleep(5 * time.Millisecond)
	}

	job, _ := manager.GetJob(jobID)
	t.Fatalf("job state = %s, want %s", job.State, want)
}

func rootList(root string) []string { return []string{root} }
