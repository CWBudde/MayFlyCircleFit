package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUIEventHubOrdersEvents(t *testing.T) {
	hub := NewUIEventHub()

	events, subscription, sequence := hub.Subscribe()
	defer hub.Unsubscribe(subscription)

	if sequence != 0 {
		t.Fatalf("initial sequence = %d, want 0", sequence)
	}

	first := hub.PublishJobDeleted("one")

	second := hub.PublishCampaignChanged("schedule", "two")
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("published sequences = %d, %d, want 1, 2", first.Sequence, second.Sequence)
	}

	if got := (<-events).Sequence; got != 1 {
		t.Fatalf("first subscriber sequence = %d, want 1", got)
	}

	if got := (<-events).Sequence; got != 2 {
		t.Fatalf("second subscriber sequence = %d, want 2", got)
	}

	_, secondSubscription, snapshot := hub.Subscribe()
	defer hub.Unsubscribe(secondSubscription)

	if snapshot != 2 {
		t.Fatalf("subscription snapshot = %d, want 2", snapshot)
	}
}

func TestUIEventHubDisconnectsSlowSubscriber(t *testing.T) {
	hub := NewUIEventHub()
	events, subscription, _ := hub.Subscribe()

	for range uiEventSubscriberBuffer + 1 {
		hub.PublishJobDeleted("job")
	}

	for range uiEventSubscriberBuffer {
		if _, ok := <-events; !ok {
			t.Fatal("slow subscriber closed before buffered events were readable")
		}
	}

	if _, ok := <-events; ok {
		t.Fatal("slow subscriber remained connected after its buffer overflowed")
	}

	// Unsubscribe must remain safe after Publish has already removed the client.
	hub.Unsubscribe(subscription)
}

func TestJobLifecyclePublishesUIEventsInOrder(t *testing.T) {
	manager := NewJobManager()

	events, subscription, _ := manager.uiEvents.Subscribe()
	defer manager.uiEvents.Unsubscribe(subscription)

	job := manager.CreateJob("", JobConfig{})
	err := manager.StartJob(job.ID)
	if err != nil {
		t.Fatalf("start job: %v", err)
	}

	err := manager.MarkJobCompleted(job.ID)
	if err != nil {
		t.Fatalf("complete job: %v", err)
	}

	err := manager.DeleteJob(job.ID)
	if err != nil {
		t.Fatalf("delete job: %v", err)
	}

	for index, want := range []struct {
		typeName string
		state    JobState
	}{
		{typeName: uiEventJobUpsert, state: StatePending},
		{typeName: uiEventJobUpsert, state: StateRunning},
		{typeName: uiEventJobUpsert, state: StateCompleted},
		{typeName: uiEventJobDeleted},
	} {
		event := <-events
		if event.Sequence != uint64(index+1) || event.Type != want.typeName || event.JobID != job.ID {
			t.Fatalf("event %d = %+v", index, event)
		}

		if want.state != "" && (event.Progress == nil || event.Progress.State != want.state) {
			t.Fatalf("event %d progress = %+v, want state %s", index, event.Progress, want.state)
		}
	}
}

func TestUIEventsEndpointWritesOrderedEnvelope(t *testing.T) {
	server := &Server{uiEvents: NewUIEventHub()}
	server.uiEvents.PublishJobDeleted("before-connect")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	response := &safeResponseRecorder{}
	done := make(chan struct{})

	go func() {
		server.handleUIEvents(response, request)
		close(done)
	}()

	waitForUIEventCount(t, response, 1)

	published := server.uiEvents.PublishCampaignChanged("chain", "campaign-1")

	waitForUIEventCount(t, response, 2)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event handler did not stop after request cancellation")
	}

	events := decodeUIEvents(t, response.String())
	if events[0].Type != uiEventSync || events[0].Sequence != 1 {
		t.Fatalf("initial event = %+v, want sync at sequence 1", events[0])
	}

	if events[1].Type != uiEventCampaignChanged || events[1].Sequence != published.Sequence || events[1].CampaignID != "campaign-1" {
		t.Fatalf("published event = %+v", events[1])
	}

	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	if !strings.Contains(response.String(), "id: "+strconv.FormatUint(published.Sequence, 10)+"\n") {
		t.Fatalf("response does not contain SSE id for sequence %d: %q", published.Sequence, response.String())
	}
}

func TestJobMetricsEndpointReturnsBoundedTail(t *testing.T) {
	server := NewServer("localhost:0", nil)
	shutdownTestServer(t, server)

	job := server.jobManager.CreateJob("", JobConfig{})
	for iteration := 1; iteration <= 3; iteration++ {
		err := server.jobManager.RecordMetrics(job.ID, MetricSample{Iteration: iteration, Cost: float64(iteration)})
		if err != nil {
			t.Fatalf("record metric %d: %v", iteration, err)
		}
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/metrics?limit=2", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, body=%q", response.Code, response.Body.String())
	}

	var samples []MetricSample
	err := json.NewDecoder(response.Body).Decode(&samples)
	if err != nil {
		t.Fatalf("decode metrics: %v", err)
	}

	if len(samples) != 2 || samples[0].Iteration != 2 || samples[1].Iteration != 3 {
		t.Fatalf("metrics = %+v, want iterations 2 and 3", samples)
	}

	for _, test := range []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "zero limit", method: http.MethodGet, path: "/api/v1/jobs/" + job.ID + "/metrics?limit=0", status: http.StatusBadRequest},
		{name: "over maximum", method: http.MethodGet, path: "/api/v1/jobs/" + job.ID + "/metrics?limit=5001", status: http.StatusBadRequest},
		{name: "wrong method", method: http.MethodPost, path: "/api/v1/jobs/" + job.ID + "/metrics", status: http.StatusMethodNotAllowed},
		{name: "missing job", method: http.MethodGet, path: "/api/v1/jobs/00000000-0000-4000-8000-000000000001/metrics", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			server.Handler().ServeHTTP(result, httptest.NewRequest(test.method, test.path, nil))

			if result.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%q", result.Code, test.status, result.Body.String())
			}
		})
	}
}

func waitForUIEventCount(t *testing.T, response *safeResponseRecorder, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(decodeUIEvents(t, response.String())) >= want {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("UI event count did not reach %d; body=%q", want, response.String())
}

func decodeUIEvents(t *testing.T, body string) []UIEvent {
	t.Helper()
	var events []UIEvent

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var event UIEvent
		err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event)
		if err != nil {
			t.Fatalf("decode UI event: %v", err)
		}

		events = append(events, event)
	}

	err := scanner.Err()
	if err != nil {
		t.Fatalf("scan UI events: %v", err)
	}

	return events
}
