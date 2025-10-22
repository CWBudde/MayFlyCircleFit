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
	JobID      string    `json:"jobId"`
	State      JobState  `json:"state"`
	Iterations int       `json:"iterations"`
	BestCost   float64   `json:"bestCost"`
	CPS        float64   `json:"cps"`
	Timestamp  time.Time `json:"timestamp"`
}

// EventBroadcaster manages SSE connections for a job
type EventBroadcaster struct {
	mu        sync.RWMutex
	clients   map[string]map[chan ProgressEvent]bool // jobID -> set of client channels
	lastEvent map[string]ProgressEvent               // jobID -> last event for new clients
}

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

	// Send last event if available (for reconnecting clients)
	if lastEvent, ok := eb.lastEvent[jobID]; ok {
		select {
		case ch <- lastEvent:
		default:
			// Channel full, skip
		}
	}

	slog.Debug("SSE client subscribed", "jobID", jobID, "total_clients", len(eb.clients[jobID]))
	return ch
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

// Broadcast sends an event to all subscribed clients for a job
func (eb *EventBroadcaster) Broadcast(event ProgressEvent) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	// Store last event
	eb.lastEvent[event.JobID] = event

	clients, ok := eb.clients[event.JobID]
	if !ok || len(clients) == 0 {
		return
	}

	slog.Debug("Broadcasting event", "jobID", event.JobID, "clients", len(clients), "iterations", event.Iterations)

	for ch := range clients {
		select {
		case ch <- event:
			// Event sent successfully
		default:
			// Channel full, skip this client (prevents blocking)
			slog.Warn("SSE channel full, skipping event", "jobID", event.JobID)
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
	// Check if job exists
	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to events
	eventChan := s.jobManager.broadcaster.Subscribe(jobID)
	defer s.jobManager.broadcaster.Unsubscribe(jobID, eventChan)

	// Send initial event with current job state
	initialEvent := ProgressEvent{
		JobID:      job.ID,
		State:      job.State,
		Iterations: job.Iterations,
		BestCost:   job.BestCost,
		CPS:        0,
		Timestamp:  time.Now(),
	}

	if err := writeSSEEvent(w, initialEvent); err != nil {
		slog.Error("Failed to write initial SSE event", "error", err)
		return
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

		case <-pingTicker.C:
			// Send ping to keep connection alive
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
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
