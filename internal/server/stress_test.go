package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerConcurrentJobLifecycleStress(t *testing.T) {
	root := t.TempDir()
	referencePath := filepath.Join(root, "reference.png")
	createSimpleTestImage(t, referencePath)

	const jobCount = 4
	server := NewServerWithOptions(":0", nil, ServerOptions{
		InputRoots:        []string{root},
		MaxConcurrentJobs: 2,
		QueueSize:         jobCount,
	})

	var shutdownOnce sync.Once
	var shutdownErr error
	shutdown := func() {
		shutdownOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			shutdownErr = server.Shutdown(ctx)
		})
	}

	t.Cleanup(func() {
		shutdown()

		if shutdownErr != nil {
			t.Errorf("shutdown did not join all workers: %v", shutdownErr)
		}
	})

	jobs := createStressJobs(t, server, referencePath, jobCount)

	type streamClient struct {
		jobID    string
		cancel   context.CancelFunc
		done     chan struct{}
		response *httptest.ResponseRecorder
	}

	streams := make([]streamClient, 0, len(jobs))
	for _, job := range jobs {
		ctx, cancel := context.WithCancel(context.Background())
		request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/stream", nil).WithContext(ctx)
		response := httptest.NewRecorder()
		done := make(chan struct{})

		streams = append(streams, streamClient{jobID: job.ID, cancel: cancel, done: done, response: response})
		go func() {
			defer close(done)

			server.Handler().ServeHTTP(response, request)
		}()
	}

	t.Cleanup(func() {
		for _, stream := range streams {
			stream.cancel()
		}

		for _, stream := range streams {
			select {
			case <-stream.done:
			case <-time.After(2 * time.Second):
				t.Errorf("SSE handler for %s did not stop", stream.jobID)
			}
		}
	})

	waitForStressCondition(t, 3*time.Second, "all SSE clients to subscribe", func() bool {
		server.jobManager.broadcaster.mu.RLock()
		defer server.jobManager.broadcaster.mu.RUnlock()

		for _, job := range jobs {
			if len(server.jobManager.broadcaster.clients[job.ID]) != 1 {
				return false
			}
		}

		return true
	})

	statusCtx, stopStatus := context.WithCancel(context.Background())
	var statusWG sync.WaitGroup
	var statusReads atomic.Int64

	statusErrors := make(chan error, len(jobs))
	for _, job := range jobs {
		jobID := job.ID

		statusWG.Add(1)
		go func() {
			defer statusWG.Done()

			for {
				select {
				case <-statusCtx.Done():
					return
				default:
				}

				request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/status", nil)
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)

				if response.Code != http.StatusOK {
					statusErrors <- fmt.Errorf("job %s status = %d", jobID, response.Code)
					return
				}

				var status jobStatusResponse
				err := json.NewDecoder(response.Body).Decode(&status)
				if err != nil {
					statusErrors <- fmt.Errorf("decode job %s status: %w", jobID, err)
					return
				}

				if status.ID != jobID {
					statusErrors <- fmt.Errorf("status ID = %s, want %s", status.ID, jobID)
					return
				}

				statusReads.Add(1)
				time.Sleep(time.Millisecond)
			}
		}()
	}

	t.Cleanup(func() {
		stopStatus()
		statusWG.Wait()
	})

	waitForStressCondition(t, 5*time.Second, "two jobs to publish optimizer progress", func() bool {
		progressed := 0

		server.jobManager.broadcaster.mu.RLock()
		defer server.jobManager.broadcaster.mu.RUnlock()

		for _, job := range jobs {
			event, ok := server.jobManager.broadcaster.lastEvent[job.ID]
			if ok && event.State == StateRunning && event.Iterations > 0 {
				progressed++
			}
		}

		return progressed >= server.options.MaxConcurrentJobs
	})

	cancelErrors := make(chan error, len(jobs))
	var cancelWG sync.WaitGroup

	for _, job := range jobs {
		jobID := job.ID

		cancelWG.Add(1)
		go func() {
			defer cancelWG.Done()

			request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/cancel", nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusAccepted {
				cancelErrors <- fmt.Errorf("cancel job %s = %d: %s", jobID, response.Code, response.Body.String())
			}
		}()
	}

	cancelWG.Wait()
	close(cancelErrors)

	for err := range cancelErrors {
		t.Error(err)
	}

	if t.Failed() {
		return
	}

	for _, job := range jobs {
		waitForJobState(t, server.jobManager, job.ID, StateCancelled)
	}

	stopStatus()
	statusWG.Wait()
	close(statusErrors)

	for err := range statusErrors {
		t.Error(err)
	}

	if reads := statusReads.Load(); reads < jobCount {
		t.Errorf("concurrent status reads = %d, want at least %d", reads, jobCount)
	}

	for _, stream := range streams {
		stream.cancel()
	}

	progressStreams := 0

	for _, stream := range streams {
		select {
		case <-stream.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("SSE handler for %s did not stop", stream.jobID)
		}

		if streamHasProgressEvent(t, stream.response.Body.String(), stream.jobID) {
			progressStreams++
		}
	}

	if progressStreams < server.options.MaxConcurrentJobs {
		t.Fatalf("SSE streams with progress = %d, want at least %d", progressStreams, server.options.MaxConcurrentJobs)
	}

	deleteErrors := make(chan error, len(jobs))
	var deleteWG sync.WaitGroup

	for _, job := range jobs {
		jobID := job.ID

		deleteWG.Add(1)
		go func() {
			defer deleteWG.Done()

			request := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+jobID, nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				deleteErrors <- fmt.Errorf("delete job %s = %d: %s", jobID, response.Code, response.Body.String())
			}
		}()
	}

	deleteWG.Wait()
	close(deleteErrors)

	for err := range deleteErrors {
		t.Error(err)
	}

	if remaining := server.jobManager.ListJobs(); len(remaining) != 0 {
		t.Fatalf("jobs remain after cleanup: %d", len(remaining))
	}

	server.jobManager.broadcaster.mu.RLock()
	clientJobs := len(server.jobManager.broadcaster.clients)
	cachedEvents := len(server.jobManager.broadcaster.lastEvent)
	server.jobManager.broadcaster.mu.RUnlock()

	if clientJobs != 0 || cachedEvents != 0 {
		t.Fatalf("SSE resources remain after cleanup: clients=%d events=%d", clientJobs, cachedEvents)
	}

	shutdown()

	if shutdownErr != nil {
		t.Fatalf("shutdown did not join all workers: %v", shutdownErr)
	}
}

func createStressJobs(t *testing.T, server *Server, referencePath string, count int) []*Job {
	t.Helper()

	created := make(chan *Job, count)
	errors := make(chan error, count)

	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()

			body, err := json.Marshal(JobConfig{
				RefPath: referencePath,
				Mode:    "joint",
				Backend: "cpu",
				Circles: 8,
				Iters:   10_000,
				PopSize: 30,
				Seed:    seed,
			})
			if err != nil {
				errors <- err
				return
			}

			request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusCreated {
				errors <- fmt.Errorf("create job = %d: %s", response.Code, response.Body.String())
				return
			}

			var job Job
			if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
				errors <- err
				return
			}

			created <- &job
		}(int64(i + 1))
	}

	wg.Wait()
	close(created)
	close(errors)

	for err := range errors {
		t.Error(err)
	}

	if t.Failed() {
		return nil
	}

	jobs := make([]*Job, 0, count)
	for job := range created {
		jobs = append(jobs, job)
	}

	if len(jobs) != count {
		t.Fatalf("created jobs = %d, want %d", len(jobs), count)
	}

	return jobs
}

func streamHasProgressEvent(t *testing.T, stream, jobID string) bool {
	t.Helper()

	for line := range strings.SplitSeq(stream, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var event ProgressEvent
		err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event)
		if err != nil {
			t.Fatalf("decode SSE event for %s: %v", jobID, err)
		}

		if event.JobID == jobID && event.State == StateRunning && event.Iterations > 0 {
			return true
		}
	}

	return false
}

func waitForStressCondition(t *testing.T, timeout time.Duration, description string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", description)
}
