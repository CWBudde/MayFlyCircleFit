package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

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

func waitForSubscriber(t *testing.T, broadcaster *EventBroadcaster, jobID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		broadcaster.mu.RLock()
		count := len(broadcaster.clients[jobID])
		broadcaster.mu.RUnlock()
		if count == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("SSE client did not subscribe")
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
