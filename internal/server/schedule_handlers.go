package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/google/uuid"
)

// The schedule endpoints follow the job endpoints exactly: unknown fields are
// refused, errors use the shared writeAPIError shape, and the boundary is the
// trusted-local one documented in docs/behavior-invariants.md.
//
// Schedules live in the server's default store and their stages therefore run
// in the default project. Per-project campaigns are deliberately out of scope
// here; the store is keyed by schedule identifier and does not know about
// projects.

// scheduleSummary is a campaign without its stages, which is what a listing
// needs. The realized stage count comes from expanding the document rather than
// from a stored total, so it cannot disagree with the plan.
type scheduleSummary struct {
	ScheduleID   string              `json:"scheduleId"`
	Name         string              `json:"name,omitempty"`
	State        store.ScheduleState `json:"state"`
	CampaignSeed int64               `json:"campaignSeed"`
	TotalStages  int                 `json:"totalStages"`
	CreatedAt    time.Time           `json:"createdAt"`
	UpdatedAt    time.Time           `json:"updatedAt"`
	Error        string              `json:"error,omitempty"`
}

// scheduleDetail adds the stage records, which are the single source of truth
// for how far the campaign has got.
type scheduleDetail struct {
	scheduleSummary
	Document app.ScheduleDocument        `json:"document"`
	Stages   []store.ScheduleStageRecord `json:"stages"`
}

func summarizeSchedule(record *store.ScheduleRecord) scheduleSummary {
	total := 0
	if plan, err := record.Document.Expand(); err == nil {
		total = len(plan)
	}
	return scheduleSummary{
		ScheduleID:   record.ScheduleID,
		Name:         record.Document.Name,
		State:        record.State,
		CampaignSeed: record.CampaignSeed,
		TotalStages:  total,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
		Error:        record.Error,
	}
}

// handleSchedules handles /api/v1/schedules
func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateSchedule(w, r)
	case http.MethodGet:
		s.handleListSchedules(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

// handleSchedulesWithID handles /api/v1/schedules/:id/*
func (s *Server) handleSchedulesWithID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/schedules/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_schedule_id", "schedule ID required")
		return
	}
	scheduleID := parts[0]
	parsed, err := uuid.Parse(scheduleID)
	if err != nil || parsed == uuid.Nil || parsed.String() != scheduleID {
		writeAPIError(w, http.StatusBadRequest, "invalid_schedule_id", "schedule ID must be a canonical UUID")
		return
	}

	switch {
	case len(parts) == 1:
		s.handleGetSchedule(w, r, scheduleID)
	case len(parts) == 2 && (parts[1] == "cancel" || parts[1] == "pause" || parts[1] == "resume"):
		s.handleScheduleAction(w, r, scheduleID, parts[1])
	default:
		writeAPIError(w, http.StatusNotFound, "not_found", "resource not found")
	}
}

// handleCreateSchedule handles POST /api/v1/schedules. The body is the schedule
// document itself, which is the format ParseSchedule validates and the store
// persists, so a campaign is written exactly as it was authored.
//
// A created schedule starts running, in the same way a created job does.
func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the size limit")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unable to read request body")
		return
	}
	document, err := app.ParseSchedule(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return
	}
	// Refuse an unreachable reference now rather than as a stage failure hours
	// in. The path itself stays as authored: it is resolved again when each
	// stage runs, so a schedule does not bake in this process's view of disk.
	probe := document.Base
	if failure := s.resolveConfigPaths(&probe, "schedule"); failure != nil {
		writeContinuationError(w, failure)
		return
	}

	record := store.NewScheduleRecord(uuid.New().String(), *document)
	record.State = store.ScheduleStateRunning
	if err := scheduleStore.SaveSchedule(record); err != nil {
		slog.Error("Failed to persist schedule", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to persist the schedule")
		return
	}
	if err := s.startScheduleDriver(record.ScheduleID); err != nil {
		slog.Error("Failed to start schedule executor", "schedule_id", record.ScheduleID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to start the schedule")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(summarizeSchedule(record))
}

// handleListSchedules handles GET /api/v1/schedules
func (s *Server) handleListSchedules(w http.ResponseWriter, _ *http.Request) {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}
	records, err := scheduleStore.ListSchedules()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to list schedules")
		return
	}
	summaries := make([]scheduleSummary, 0, len(records))
	for i := range records {
		summaries = append(summaries, summarizeSchedule(&records[i]))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summaries)
}

// handleGetSchedule handles GET /api/v1/schedules/:id
func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request, scheduleID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}
	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		writeScheduleLoadError(w, err)
		return
	}
	stages, err := scheduleStore.LoadScheduleStages(scheduleID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to load schedule stages")
		return
	}
	if stages == nil {
		stages = []store.ScheduleStageRecord{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(scheduleDetail{
		scheduleSummary: summarizeSchedule(record),
		Document:        record.Document,
		Stages:          stages,
	})
}

// handleScheduleAction handles the cancel, pause, and resume verbs. All three
// act on the campaign as a whole.
//
// Pause takes effect at the next stage boundary: a stage is the atomic unit of
// a campaign, and interrupting one would throw away its work without recording
// anything a later resume could use. Cancel does interrupt, because a cancelled
// campaign is not going to want that work.
func (s *Server) handleScheduleAction(w http.ResponseWriter, r *http.Request, scheduleID, action string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}
	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		writeScheduleLoadError(w, err)
		return
	}

	next, failure := scheduleTransition(record.State, action)
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}
	record.State = next
	if next == store.ScheduleStateRunning {
		record.Error = ""
	}
	if err := scheduleStore.SaveSchedule(record); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to update the schedule")
		return
	}

	switch action {
	case "cancel":
		// The state is durable before the stage is touched, so a crash in
		// between leaves a cancelled schedule that starts no executor rather
		// than a running one whose stage was killed behind its back.
		s.cancelScheduleStage(scheduleID)
	case "resume":
		if err := s.startScheduleDriver(scheduleID); err != nil && !errors.Is(err, errScheduleDriverRunning) {
			writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to start the schedule")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(summarizeSchedule(record))
}

// scheduleTransition reports the state an action moves a schedule to, or the
// answer for an action the current state does not allow.
func scheduleTransition(current store.ScheduleState, action string) (store.ScheduleState, *continuationError) {
	switch action {
	case "cancel":
		switch current {
		case store.ScheduleStatePending, store.ScheduleStateRunning, store.ScheduleStatePaused:
			return store.ScheduleStateCancelled, nil
		}
		return "", continuationFailure(http.StatusConflict, "invalid_state", "schedule is "+string(current))
	case "pause":
		if current == store.ScheduleStateRunning {
			return store.ScheduleStatePaused, nil
		}
		return "", continuationFailure(http.StatusConflict, "invalid_state", "only a running schedule can be paused")
	case "resume":
		switch current {
		case store.ScheduleStatePaused, store.ScheduleStatePending:
			return store.ScheduleStateRunning, nil
		}
		return "", continuationFailure(http.StatusConflict, "invalid_state", "only a paused schedule can be resumed")
	}
	return "", continuationFailure(http.StatusNotFound, "not_found", "resource not found")
}

func writeScheduleLoadError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "schedule not found")
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to load the schedule")
}
