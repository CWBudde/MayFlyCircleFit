package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	uiEventJobUpsert       = "job.upsert"
	uiEventJobDeleted      = "job.deleted"
	uiEventCampaignChanged = "campaign.changed"
	uiEventSync            = "sync"

	uiEventSubscriberBuffer = 64
	uiEventSyncInterval     = 15 * time.Second
)

// UIEvent is the ordered browser-facing notification envelope. It deliberately
// carries only cheap, live fields. REST resources remain authoritative and are
// refetched for configuration, history, campaign stages, and action metadata.
type UIEvent struct {
	Sequence   uint64         `json:"sequence"`
	Type       string         `json:"type"`
	JobID      string         `json:"jobId,omitempty"`
	CampaignID string         `json:"campaignId,omitempty"`
	Source     string         `json:"source,omitempty"`
	Progress   *ProgressEvent `json:"progress,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
}

// UIEventHub assigns one process-wide order to UI notifications. A slow
// subscriber is disconnected instead of silently losing an event: reconnecting
// clients refetch their authoritative resource before applying more events.
type UIEventHub struct {
	mu       sync.Mutex
	sequence uint64
	clients  map[chan UIEvent]struct{}
}

func NewUIEventHub() *UIEventHub {
	return &UIEventHub{clients: make(map[chan UIEvent]struct{})}
}

func (hub *UIEventHub) Subscribe() (<-chan UIEvent, chan UIEvent, uint64) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	ch := make(chan UIEvent, uiEventSubscriberBuffer)
	hub.clients[ch] = struct{}{}

	return ch, ch, hub.sequence
}

func (hub *UIEventHub) Unsubscribe(ch chan UIEvent) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	if _, ok := hub.clients[ch]; !ok {
		return
	}

	delete(hub.clients, ch)
	close(ch)
}

func (hub *UIEventHub) Publish(event UIEvent) UIEvent {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	hub.sequence++

	event.Sequence = hub.sequence
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	for ch := range hub.clients {
		select {
		case ch <- event:
		default:
			// Closing the channel makes the SSE handler return. EventSource then
			// reconnects, and its onopen path refetches the authoritative state.
			delete(hub.clients, ch)
			close(ch)
			slog.Warn("UI event subscriber fell behind; forcing resynchronization")
		}
	}

	return event
}

func (hub *UIEventHub) PublishJob(progress ProgressEvent) UIEvent {
	copy := progress

	return hub.Publish(UIEvent{
		Type:      uiEventJobUpsert,
		JobID:     progress.JobID,
		Progress:  &copy,
		Timestamp: progress.Timestamp,
	})
}

func (hub *UIEventHub) PublishJobDeleted(jobID string) UIEvent {
	return hub.Publish(UIEvent{Type: uiEventJobDeleted, JobID: jobID})
}

func (hub *UIEventHub) PublishCampaignChanged(source, campaignID string) UIEvent {
	return hub.Publish(UIEvent{
		Type:       uiEventCampaignChanged,
		Source:     source,
		CampaignID: campaignID,
	})
}

// handleUIEvents exposes the ordered notification stream used by React pages.
// Existing /stream routes retain their original payloads for compatibility.
func (s *Server) handleUIEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "sse_not_supported", "server-sent events are not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	events, subscription, sequence := s.uiEvents.Subscribe()
	defer s.uiEvents.Unsubscribe(subscription)

	// The heartbeat reports the last sequence written on this connection, not
	// the hub's high-water mark: a sync carrying a sequence the client has not
	// received yet would make it discard the real event as stale.
	lastWritten := sequence

	err := writeUIEvent(w, UIEvent{
		Sequence:  lastWritten,
		Type:      uiEventSync,
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		return
	}

	flusher.Flush()

	ctx, releaseStream := s.streamContext(r)
	defer releaseStream()

	ticker := time.NewTicker(uiEventSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}

			err := writeUIEvent(w, event)
			if err != nil {
				return
			}

			lastWritten = event.Sequence

			flusher.Flush()
		case <-ticker.C:
			err := writeUIEvent(w, UIEvent{
				Sequence:  lastWritten,
				Type:      uiEventSync,
				Timestamp: time.Now().UTC(),
			})
			if err != nil {
				return
			}

			flusher.Flush()
		}
	}
}

func writeUIEvent(w http.ResponseWriter, event UIEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal UI event: %w", err)
	}

	if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", strconv.FormatUint(event.Sequence, 10), data); err != nil {
		return fmt.Errorf("write UI event: %w", err)
	}

	return nil
}
