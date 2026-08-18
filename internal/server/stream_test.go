package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

type safeResponseRecorder struct {
	header http.Header
	buf    bytes.Buffer
	mu     sync.Mutex
	code   int
}

func (w *safeResponseRecorder) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *safeResponseRecorder) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.code = statusCode
}

func (w *safeResponseRecorder) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(data)
}

func (w *safeResponseRecorder) Flush() {
	// no-op
}

func (w *safeResponseRecorder) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *safeResponseRecorder) Code() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.code
}

func TestEventBroadcasterConcurrentLifecycle(t *testing.T) {
	eb := NewEventBroadcaster()
	const jobID = "2b35aa54-6343-4d6e-86c1-915bb5543430"

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()
			ch := eb.Subscribe(jobID)
			eb.Broadcast(ProgressEvent{
				JobID:      jobID,
				State:      StateRunning,
				Iterations: iteration,
				Timestamp:  time.Now(),
			})
			eb.Unsubscribe(jobID, ch)
		}(i)
	}
	wg.Wait()

	eb.CleanupJob(jobID)
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	if _, ok := eb.clients[jobID]; ok {
		t.Fatal("client cache was not removed")
	}
	if _, ok := eb.lastEvent[jobID]; ok {
		t.Fatal("last-event cache was not removed")
	}
}

func TestEventBroadcasterAllSubscribers(t *testing.T) {
	eb := NewEventBroadcaster()
	all := eb.SubscribeAll()
	job := eb.Subscribe("job")
	defer eb.UnsubscribeAll(all)
	defer eb.Unsubscribe("job", job)

	eb.Broadcast(ProgressEvent{JobID: "job", State: StateRunning, Timestamp: time.Now()})

	select {
	case event, ok := <-job:
		if !ok {
			t.Fatal("expected job subscriber to receive broadcast event")
		}
		if event.JobID != "job" {
			t.Fatalf("job subscriber event = %q, want %q", event.JobID, "job")
		}
	case <-time.After(time.Second):
		t.Fatal("job subscriber timed out")
	}

	select {
	case event, ok := <-all:
		if !ok {
			t.Fatal("expected wildcard subscriber to receive broadcast event")
		}
		if event.JobID != "job" {
			t.Fatalf("wildcard event = %q, want %q", event.JobID, "job")
		}
	case <-time.After(time.Second):
		t.Fatal("wildcard subscriber timed out")
	}
}

func TestEventBroadcasterCleanupKeepsWildcardSubscribers(t *testing.T) {
	eb := NewEventBroadcaster()
	all := eb.SubscribeAll()
	defer eb.UnsubscribeAll(all)
	job := eb.Subscribe("job")

	eb.Broadcast(ProgressEvent{JobID: "job", State: StateRunning, Timestamp: time.Now()})
	event, ok := <-job
	if !ok {
		t.Fatal("expected job channel to be open")
	}
	if event.JobID != "job" {
		t.Fatalf("unexpected job event %q", event.JobID)
	}

	eb.CleanupJob("job")
	eb.mu.RLock()
	if _, ok := eb.clients["job"]; ok {
		t.Fatal("job clients should be removed on cleanup")
	}
	if _, ok := eb.lastEvent["job"]; ok {
		t.Fatal("job event cache should be removed on cleanup")
	}
	if _, ok := eb.clients[wildcardJobID]; !ok {
		t.Fatal("wildcard clients should stay after job cleanup")
	}
	eb.mu.RUnlock()

	select {
	case _, ok := <-job:
		if ok {
			t.Fatal("expected job channel to close on cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("expected job channel to close after cleanup")
	}

	eb.Broadcast(ProgressEvent{JobID: "job", State: StateRunning, Timestamp: time.Now()})
	select {
	case _, ok := <-all:
		if !ok {
			t.Fatal("wildcard channel should remain open after job cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("wildcard subscriber should receive broadcast after job cleanup")
	}
}

// TestEventBroadcasterConcurrentWildcardLifecycle race-tests the fan-out itself.
// The other wildcard tests are sequential, so running them under -race says
// nothing about Broadcast reaching two subscriber sets while clients churn.
func TestEventBroadcasterConcurrentWildcardLifecycle(t *testing.T) {
	eb := NewEventBroadcaster()
	const jobID = "6f2c0d21-3f4a-4f0e-9c2d-2a1f5c8b7e04"

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(2)
		go func(iteration int) {
			defer wg.Done()
			ch := eb.SubscribeAll()
			eb.Broadcast(ProgressEvent{
				JobID:      jobID,
				State:      StateRunning,
				Iterations: iteration,
				Timestamp:  time.Now(),
			})
			eb.UnsubscribeAll(ch)
		}(i)
		go func(iteration int) {
			defer wg.Done()
			ch := eb.Subscribe(jobID)
			eb.Broadcast(ProgressEvent{
				JobID:      jobID,
				State:      StateRunning,
				Iterations: iteration,
				Timestamp:  time.Now(),
			})
			eb.Unsubscribe(jobID, ch)
		}(i)
	}
	wg.Wait()

	eb.mu.RLock()
	defer eb.mu.RUnlock()
	if _, ok := eb.clients[wildcardJobID]; ok {
		t.Fatal("wildcard clients leaked after every global subscriber unsubscribed")
	}
	if _, ok := eb.clients[jobID]; ok {
		t.Fatal("job clients leaked after every subscriber unsubscribed")
	}
}

func TestEventBroadcasterCleanupMakesUnsubscribeIdempotent(t *testing.T) {
	eb := NewEventBroadcaster()
	const jobID = "b65ef8ca-150c-4f74-ae59-661140af049f"
	ch := eb.Subscribe(jobID)

	eb.CleanupJob(jobID)
	eb.Unsubscribe(jobID, ch)
}

func TestJobStreamPublishesTerminalTransitionsAndCloses(t *testing.T) {
	tests := []struct {
		name       string
		wantState  JobState
		transition func(*JobManager, string) error
	}{
		{name: "cancelled", wantState: StateCancelled, transition: func(manager *JobManager, id string) error {
			return manager.CancelJob(id)
		}},
		{name: "failed", wantState: StateFailed, transition: func(manager *JobManager, id string) error {
			return manager.FailJob(id, "safe failure")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(":8080", nil)
			job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{})
			if err := server.jobManager.UpdateJob(job.ID, func(current *Job) { current.Evaluations = 42 }); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/stream", nil)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				server.handleJobStream(response, request, job.ID)
				close(done)
			}()

			waitForSubscriber(t, server.jobManager.broadcaster, job.ID)
			if err := test.transition(server.jobManager, job.ID); err != nil {
				t.Fatal(err)
			}

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("SSE handler did not close after job became %s", test.wantState)
			}

			events := decodeSSEEvents(t, response.Body.String())
			if len(events) != 2 {
				t.Fatalf("event count = %d, want initial and terminal events; body=%q", len(events), response.Body.String())
			}
			if events[0].State != StatePending || events[1].State != test.wantState {
				t.Fatalf("states = [%s, %s], want [pending, %s]", events[0].State, events[1].State, test.wantState)
			}
			if events[0].Evaluations != 42 || events[1].Evaluations != 42 {
				t.Fatalf("evaluations = [%d, %d], want [42, 42]", events[0].Evaluations, events[1].Evaluations)
			}

			server.jobManager.broadcaster.mu.RLock()
			clientCount := len(server.jobManager.broadcaster.clients[job.ID])
			server.jobManager.broadcaster.mu.RUnlock()
			if clientCount != 0 {
				t.Fatalf("subscriber count = %d, want 0", clientCount)
			}
		})
	}
}

func TestAllJobStream_SnapshotAndUpdates(t *testing.T) {
	server := NewServer(":8080", nil)
	first := server.jobManager.CreateJob(app.DefaultProject, JobConfig{})
	second := server.jobManager.CreateJob(app.DefaultProject, JobConfig{})
	if err := server.jobManager.StartJob(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := server.jobManager.StartJob(second.ID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	w := &safeResponseRecorder{}
	done := make(chan struct{})
	go func() {
		server.handleAllJobStream(w, req)
		close(done)
	}()
	waitForSubscriberCount(t, server.jobManager.broadcaster, wildcardJobID, 1)

	events := waitForSSEEvents(t, w, 2)
	running := map[string]bool{first.ID: true, second.ID: true}
	for _, event := range events {
		if !running[event.JobID] {
			t.Fatalf("snapshot contains %s, expected only running jobs", event.JobID)
		}
		if event.State != StateRunning {
			t.Fatalf("snapshot event state = %s, want running", event.State)
		}
		delete(running, event.JobID)
	}
	if len(running) != 0 {
		t.Fatalf("snapshot missed jobs: %v", running)
	}

	server.jobManager.broadcaster.Broadcast(ProgressEvent{JobID: first.ID, State: StateRunning, Iterations: 12, Timestamp: time.Now()})
	events = waitForSSEEvents(t, w, 3)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not terminate")
	}

	if events[2].JobID != first.ID || events[2].Iterations != 12 {
		t.Fatalf("live event = %+v", events[2])
	}
}

func TestEventBroadcasterDoesNotRegressTerminalState(t *testing.T) {
	broadcaster := NewEventBroadcaster()
	terminal := ProgressEvent{JobID: "job", State: StateCancelled, Timestamp: time.Now()}
	broadcaster.Broadcast(terminal)
	broadcaster.Broadcast(ProgressEvent{JobID: "job", State: StateRunning, Iterations: 10, Timestamp: time.Now()})

	broadcaster.mu.RLock()
	got := broadcaster.lastEvent["job"]
	broadcaster.mu.RUnlock()
	if got.State != StateCancelled {
		t.Fatalf("cached state = %s, want cancelled", got.State)
	}
}

func waitForSubscriberCount(t *testing.T, broadcaster *EventBroadcaster, jobID string, expected int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		broadcaster.mu.RLock()
		count := len(broadcaster.clients[jobID])
		broadcaster.mu.RUnlock()
		if count == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	broadcaster.mu.RLock()
	count := len(broadcaster.clients[jobID])
	broadcaster.mu.RUnlock()
	t.Fatalf("SSE clients for %q = %d, want %d", jobID, count, expected)
}

func waitForSubscriber(t *testing.T, broadcaster *EventBroadcaster, jobID string) {
	t.Helper()
	waitForSubscriberCount(t, broadcaster, jobID, 1)
}

// waitForSSEEvents polls the recorder until the handler has written the events
// under test. The handler writes from its own goroutine, so sleeping a fixed
// interval and reading once only asserts that the machine was fast enough.
func waitForSSEEvents(t *testing.T, recorder *safeResponseRecorder, expected int) []ProgressEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var events []ProgressEvent
	for time.Now().Before(deadline) {
		events = decodeSSEEvents(t, recorder.String())
		if len(events) >= expected {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(events) != expected {
		t.Fatalf("SSE event count = %d, want %d", len(events), expected)
	}
	return events
}

func decodeSSEEvents(t *testing.T, body string) []ProgressEvent {
	t.Helper()
	var events []ProgressEvent
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event ProgressEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("decode SSE event: %v", err)
		}
		events = append(events, event)
	}
	return events
}
