package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ProgressEvent represents a progress update event
type ProgressEvent struct {
	JobID                 string    `json:"jobId"`
	State                 JobState  `json:"state"`
	Iterations            int       `json:"iterations"`
	Evaluations           int       `json:"evaluations"`
	BestCost              float64   `json:"bestCost"`
	BestRevision          uint64    `json:"bestRevision"`
	CandidateCost         *float64  `json:"candidateCost,omitempty"`
	CandidatePSNR         *float64  `json:"candidatePsnr,omitempty"`
	CandidatePSNRInfinite bool      `json:"candidatePsnrInfinite,omitempty"`
	PSNR                  *float64  `json:"psnr"`
	PSNRInfinite          bool      `json:"psnrInfinite,omitempty"`
	SSIM                  *float64  `json:"ssim,omitempty"`
	CPS                   float64   `json:"cps"`
	Timestamp             time.Time `json:"timestamp"`
}

func (e ProgressEvent) terminal() bool {
	return e.State == StateCompleted || e.State == StateFailed || e.State == StateCancelled
}

// EventBroadcaster manages SSE connections for a job
type EventBroadcaster struct {
	mu        sync.RWMutex
	clients   map[string]map[chan ProgressEvent]bool // jobID -> set of client channels
	lastEvent map[string]ProgressEvent               // jobID -> last event for ordering and diagnostics
}

const wildcardJobID = "*"

// NewEventBroadcaster creates a new event broadcaster
func NewEventBroadcaster() *EventBroadcaster {
	return &EventBroadcaster{
		clients:   make(map[string]map[chan ProgressEvent]bool),
		lastEvent: make(map[string]ProgressEvent),
	}
}

// Subscribe adds a client to receive events for a job
func (eb *EventBroadcaster) Subscribe(jobID string) chan ProgressEvent {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan ProgressEvent, 10) // Buffered to prevent blocking

	if eb.clients[jobID] == nil {
		eb.clients[jobID] = make(map[chan ProgressEvent]bool)
	}
	eb.clients[jobID][ch] = true

	slog.Debug("SSE client subscribed", "jobID", jobID, "total_clients", len(eb.clients[jobID]))
	return ch
}

// SubscribeAll adds a client that receives all jobs' progress events.
func (eb *EventBroadcaster) SubscribeAll() chan ProgressEvent {
	return eb.Subscribe(wildcardJobID)
}

// Unsubscribe removes a client from receiving events
func (eb *EventBroadcaster) Unsubscribe(jobID string, ch chan ProgressEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if clients, ok := eb.clients[jobID]; ok {
		delete(clients, ch)
		close(ch)

		if len(clients) == 0 {
			delete(eb.clients, jobID)
		}
	}

	slog.Debug("SSE client unsubscribed", "jobID", jobID)
}

// UnsubscribeAll removes a client from receiving all jobs' progress events.
func (eb *EventBroadcaster) UnsubscribeAll(ch chan ProgressEvent) {
	eb.Unsubscribe(wildcardJobID, ch)
}

// Broadcast sends an event to all subscribed clients for a job
func (eb *EventBroadcaster) Broadcast(event ProgressEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	// A progress callback can race with cancellation after updating the job but
	// before publishing its event. Do not overwrite a terminal state with that
	// stale running update.
	if previous, ok := eb.lastEvent[event.JobID]; ok && previous.terminal() && !event.terminal() {
		return
	}

	// Store last event
	eb.lastEvent[event.JobID] = event

	// Wildcard clients are a separate set under their own key, so a job with no
	// subscribers of its own still has to be fanned out to the global stream.
	jobClients := eb.clients[event.JobID]
	wildcardClients := eb.clients[wildcardJobID]
	subscriberCount := len(jobClients) + len(wildcardClients)
	if subscriberCount == 0 {
		return
	}

	slog.Debug("Broadcasting event", "jobID", event.JobID, "clients", subscriberCount, "iterations", event.Iterations)

	for ch := range jobClients {
		select {
		case ch <- event:
			// Event sent successfully
		default:
			// Channel full, skip this client (prevents blocking)
			slog.Warn("SSE channel full, skipping event", "jobID", event.JobID)
		}
	}
	for ch := range wildcardClients {
		select {
		case ch <- event:
			// Event sent successfully
		default:
			// Channel full, skip this client (prevents blocking)
			slog.Warn("SSE channel full, skipping wildcard event", "jobID", event.JobID)
		}
	}
}

// CleanupJob removes all clients and cached events for a job
func (eb *EventBroadcaster) CleanupJob(jobID string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if clients, ok := eb.clients[jobID]; ok {
		for ch := range clients {
			close(ch)
		}
		delete(eb.clients, jobID)
	}

	delete(eb.lastEvent, jobID)
	slog.Debug("Cleaned up SSE resources", "jobID", jobID)
}

// handleJobStream handles SSE connections for job progress
func (s *Server) handleJobStream(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	// Check if job exists. The snapshot itself is taken again after
	// subscribing, so only the existence answer matters here.
	if _, exists := s.jobManager.GetJob(jobID); !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "sse_not_supported", "server-sent events are not supported")
		return
	}

	// Subscribe before taking the initial snapshot so a state transition cannot
	// fall into the gap between the snapshot and channel registration.
	eventChan := s.jobManager.broadcaster.Subscribe(jobID)
	defer s.jobManager.broadcaster.Unsubscribe(jobID, eventChan)
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		return
	}

	// Send initial event with current job state
	initialEvent := ProgressEvent{
		JobID:         job.ID,
		State:         job.State,
		Iterations:    job.Iterations,
		Evaluations:   job.Evaluations,
		BestCost:      job.BestCost,
		BestRevision:  job.BestRevision,
		CandidateCost: cloneFloat(job.CandidateCost),
		PSNR:          cloneFloat(job.PSNR),
		PSNRInfinite:  job.PSNRInfinite,
		SSIM:          cloneFloat(job.SSIM),
		CPS:           0,
		Timestamp:     time.Now(),
	}
	initialEvent.CandidatePSNR, initialEvent.CandidatePSNRInfinite = serializableCandidatePSNR(job.CandidateCost)

	if err := writeSSEEvent(w, initialEvent); err != nil {
		slog.Error("Failed to write initial SSE event", "error", err)
		return
	}
	flusher.Flush()
	if initialEvent.terminal() {
		return
	}

	// Set up ping ticker to keep connection alive
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// Listen for events and client disconnect
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			slog.Debug("SSE client disconnected", "jobID", jobID)
			return

		case event, ok := <-eventChan:
			if !ok {
				// Channel closed
				return
			}

			if err := writeSSEEvent(w, event); err != nil {
				slog.Error("Failed to write SSE event", "error", err)
				return
			}
			flusher.Flush()
			if event.terminal() {
				return
			}

		case <-pingTicker.C:
			// Send ping to keep connection alive
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// handleAllJobStream streams live updates for all jobs.
//
// It immediately emits the snapshot of all currently running jobs, then streams
// matching events until the client disconnects.
//
// Unlike handleJobStream it does not return on a terminal event: a terminal
// state ends one job, not the dashboard's view of every other one, so the
// stream outlives the jobs it reports on and only the client closes it.
func (s *Server) handleAllJobStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "sse_not_supported", "server-sent events are not supported")
		return
	}

	eventChan := s.jobManager.broadcaster.SubscribeAll()
	defer s.jobManager.broadcaster.UnsubscribeAll(eventChan)

	// Send one snapshot of all running jobs.
	//
	// Subscribing first means an event can already be queued by the time the
	// snapshot reads the manager, and the manager will by then have advanced
	// past it. Writing the snapshot and then draining that backlog unchanged
	// would walk the client backwards -- iteration 20 followed by iteration 10 --
	// so each job carries a floor until one of its events catches up. The
	// manager rejects a non-monotonic progress update, so the floor cannot
	// outrun the job, and terminal events bypass it so a cancellation is never
	// the event that gets dropped.
	iterationFloor := make(map[string]int)
	for _, job := range s.jobManager.GetRunningJobs() {
		snapshot := jobProgressSnapshot(job)
		iterationFloor[snapshot.JobID] = snapshot.Iterations
		if err := writeSSEEvent(w, snapshot); err != nil {
			slog.Error("Failed to write initial SSE event", "error", err)
			return
		}
	}
	flusher.Flush()

	// Set up ping ticker to keep connection alive
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// Listen for events and client disconnect
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			slog.Debug("SSE global client disconnected")
			return

		case event, ok := <-eventChan:
			if !ok {
				// Channel closed
				return
			}

			if floor, guarded := iterationFloor[event.JobID]; guarded {
				if !event.terminal() && event.Iterations < floor {
					continue
				}
				// The stream is ordered from here on: Broadcast is serialized
				// and this channel is FIFO, so only the snapshot gap needed a
				// guard.
				delete(iterationFloor, event.JobID)
			}

			if err := writeSSEEvent(w, event); err != nil {
				slog.Error("Failed to write SSE event", "error", err)
				return
			}
			flusher.Flush()

		case <-pingTicker.C:
			// Send ping to keep connection alive
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// jobProgressSnapshot converts a live job into a SSE-compatible progress event.
func jobProgressSnapshot(job *Job) ProgressEvent {
	cps := circlesPerSecond(job, time.Since(job.StartTime))

	event := ProgressEvent{
		JobID:         job.ID,
		State:         job.State,
		Iterations:    job.Iterations,
		Evaluations:   job.Evaluations,
		BestCost:      job.BestCost,
		BestRevision:  job.BestRevision,
		CandidateCost: cloneFloat(job.CandidateCost),
		PSNR:          cloneFloat(job.PSNR),
		PSNRInfinite:  job.PSNRInfinite,
		SSIM:          cloneFloat(job.SSIM),
		CPS:           cps,
		Timestamp:     time.Now(),
	}
	event.CandidatePSNR, event.CandidatePSNRInfinite = serializableCandidatePSNR(job.CandidateCost)
	return event
}

// writeSSEEvent writes an event in SSE format
func writeSSEEvent(w http.ResponseWriter, event ProgressEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// SSE format: "data: {json}\n\n"
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}
