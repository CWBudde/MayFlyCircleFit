package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
	"github.com/google/uuid"
)

// The campaign pages are the HTML side of the schedule surface. They render
// exactly what /api/v1/schedules and /api/v1/chains return, so the browser and
// the CLI cannot show different campaigns.

// handleCampaignList handles GET /schedules
func (s *Server) handleCampaignList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	scheduleStore, err := s.scheduleStore()
	if err != nil {
		s.renderCampaignPage(w, r, ui.CampaignListPage(nil, nil, err.Error()))
		return
	}
	records, err := scheduleStore.ListSchedules()
	if err != nil {
		slog.Error("Unable to list schedules for the campaign page", "error", err)
		s.renderCampaignPage(w, r, ui.CampaignListPage(nil, nil, "failed to list schedules"))
		return
	}
	schedules := make([]ui.CampaignSummary, 0, len(records))
	for i := range records {
		stages, err := scheduleStore.LoadScheduleStages(records[i].ScheduleID)
		if err != nil {
			slog.Warn("Unable to load schedule stages for the campaign list",
				"schedule_id", records[i].ScheduleID, "error", err)
		}
		schedules = append(schedules, summarizeCampaign(&records[i], stages))
	}

	// Chain discovery reads the default project's checkpoints, which is where
	// schedules live too. A per-project campaign listing would need the store to
	// know about projects, which it deliberately does not.
	var chains []ui.CampaignSummary
	if infos, err := s.store.ListCheckpoints(); err != nil {
		slog.Warn("Unable to list checkpoints for chain discovery", "error", err)
	} else {
		chains = chainCampaignSummaries(discoverChains(infos))
	}
	s.renderCampaignPage(w, r, ui.CampaignListPage(schedules, chains, ""))
}

// handleCampaignDetail handles GET /schedules/:id
func (s *Server) handleCampaignDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	scheduleID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/schedules/"), "/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !isCanonicalUUID(scheduleID) {
		w.WriteHeader(http.StatusNotFound)
		s.renderCampaignPage(w, r, ui.CampaignNotFound(scheduleID, "A schedule is named by a canonical UUID."))
		return
	}
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.renderCampaignPage(w, r, ui.CampaignNotFound(scheduleID, err.Error()))
		return
	}
	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		w.WriteHeader(campaignLoadStatus(err))
		s.renderCampaignPage(w, r, ui.CampaignNotFound(scheduleID, "No schedule with that identifier."))
		return
	}
	stages, err := scheduleStore.LoadScheduleStages(scheduleID)
	if err != nil {
		slog.Error("Unable to load schedule stages", "schedule_id", scheduleID, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		s.renderCampaignPage(w, r, ui.CampaignNotFound(scheduleID, "The stage records could not be read."))
		return
	}
	s.renderCampaignPage(w, r, ui.CampaignPage(campaignFromSchedule(record, stages)))
}

// handleChainDetail handles GET /chains/:jobID, the imported campaign view for
// a chain of extend and polish jobs that no schedule planned.
func (s *Server) handleChainDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jobID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/chains/"), "/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	chain, err := s.loadChain(jobID)
	if err != nil {
		w.WriteHeader(campaignLoadStatus(err))
		s.renderCampaignPage(w, r, ui.CampaignNotFound(jobID, chainErrorDetail(err)))
		return
	}
	s.renderCampaignPage(w, r, ui.CampaignPage(campaignFromChain(jobID, chain)))
}

// handleChains handles GET /api/v1/chains
func (s *Server) handleChains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if s.store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "checkpoints_unavailable", "checkpoint feature not enabled")
		return
	}
	infos, err := s.store.ListCheckpoints()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "chain_error", "failed to list checkpoints")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chainSummaryWires(discoverChains(infos)))
}

// handleChainsWithID handles GET /api/v1/chains/:jobID
func (s *Server) handleChainsWithID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	jobID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/chains/"), "/")
	chain, err := s.loadChain(jobID)
	switch {
	case errors.Is(err, errInvalidChainJobID):
		writeAPIError(w, http.StatusBadRequest, "invalid_job_id", "job ID must be a canonical UUID")
		return
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "no checkpoint chain for that job")
		return
	case err != nil:
		writeAPIError(w, http.StatusInternalServerError, "chain_error", "failed to read the checkpoint chain")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chainDetailFrom(jobID, chain))
}

var errInvalidChainJobID = errors.New("job ID must be a canonical UUID")

// loadChain resolves the store for a job and walks its lineage. The store comes
// from the job's own project, so a chain never reaches into another project's
// checkpoints.
func (s *Server) loadChain(jobID string) ([]*store.Checkpoint, error) {
	if !isCanonicalUUID(jobID) {
		return nil, errInvalidChainJobID
	}
	checkpoints, err := s.storeForJob(jobID)
	if err != nil {
		return nil, err
	}
	if checkpoints == nil {
		return nil, errors.New("checkpoint feature not enabled")
	}
	return chainCheckpoints(checkpoints, jobID)
}

func chainErrorDetail(err error) string {
	switch {
	case errors.Is(err, errInvalidChainJobID):
		return "A chain is named by the canonical UUID of its last job."
	case errors.Is(err, store.ErrNotFound):
		return "No checkpoint for that job, so there is no chain to reconstruct."
	default:
		return "The checkpoint chain could not be read."
	}
}

func campaignLoadStatus(err error) int {
	switch {
	case errors.Is(err, errInvalidChainJobID):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func isCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

// renderCampaignPage keeps the render-and-report shape in one place; every
// campaign page fails the same way. The status code is already written by the
// caller when the page is an error page, so a failure here can only be logged.
func (s *Server) renderCampaignPage(w http.ResponseWriter, r *http.Request, component templ.Component) {
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("Failed to render campaign page", "path", r.URL.Path, "error", err)
	}
}
