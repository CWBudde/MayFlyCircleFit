package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
	"github.com/google/uuid"
)

type campaignViewList struct {
	Schedules []ui.CampaignSummary `json:"schedules"`
	Chains    []ui.CampaignSummary `json:"chains"`
}

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

	chains := chainCampaignSummaries(s.discoverAllChains())
	s.renderCampaignPage(w, r, ui.CampaignListPage(schedules, chains, ""))
}

// handleCampaignViewList is the browser read model for the campaign listing.
// It intentionally matches the templ seed instead of making React reconstruct
// richer cards from the CLI's compact schedule listing.
func (s *Server) handleCampaignViewList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "campaigns_unavailable", err.Error())
		return
	}
	records, err := scheduleStore.ListSchedules()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "campaign_error", "failed to list schedules")
		return
	}
	schedules := make([]ui.CampaignSummary, 0, len(records))
	for i := range records {
		stages, loadErr := scheduleStore.LoadScheduleStages(records[i].ScheduleID)
		if loadErr != nil {
			slog.Warn("Unable to load schedule stages for campaign API", "schedule_id", records[i].ScheduleID, "error", loadErr)
		}
		schedules = append(schedules, summarizeCampaign(&records[i], stages))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(campaignViewList{
		Schedules: schedules,
		Chains:    chainCampaignSummaries(s.discoverAllChains()),
	})
}

// handleCampaignViewDetail returns the same source-neutral campaign projection
// the HTML detail page embeds. Paths are /campaigns/schedule/:id and
// /campaigns/chain/:id.
func (s *Server) handleCampaignViewDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/campaigns/"), "/"), "/")
	if len(parts) != 2 || !isCanonicalUUID(parts[1]) {
		writeAPIError(w, http.StatusBadRequest, "invalid_campaign", "campaign source and canonical ID required")
		return
	}
	var campaign ui.Campaign
	switch parts[0] {
	case "schedule":
		scheduleStore, err := s.scheduleStore()
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, "campaigns_unavailable", err.Error())
			return
		}
		record, err := scheduleStore.LoadSchedule(parts[1])
		if err != nil {
			writeScheduleLoadError(w, err)
			return
		}
		stages, err := scheduleStore.LoadScheduleStages(parts[1])
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "campaign_error", "failed to load campaign stages")
			return
		}
		campaign = campaignFromSchedule(record, stages)
	case "chain":
		chain, err := s.loadChain(parts[1])
		if err != nil {
			writeAPIError(w, campaignLoadStatus(err), "campaign_error", chainErrorDetail(err))
			return
		}
		campaign = campaignFromChain(parts[1], chain)
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_campaign", "campaign source must be schedule or chain")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(campaign)
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chainSummaryWires(s.discoverAllChains()))
}

// discoverAllChains lists every hand-driven chain the server can reconstruct,
// across every project it knows about. /chains/:jobID already resolves a job
// through its own project store, so listing only the default project would make
// the campaign page omit chains whose detail page renders perfectly well.
//
// Each store is discovered on its own rather than by pooling the listings:
// lineage never crosses a project boundary, and a pooled parent map could link
// a child to a same-named checkpoint in another project. The merged result is
// ordered again because each store only sorted its own chains.
func (s *Server) discoverAllChains() []discoveredChain {
	chains := make([]discoveredChain, 0)
	for _, slug := range s.chainProjects() {
		checkpoints, err := s.storeForSlug(slug)
		if err != nil || checkpoints == nil {
			continue
		}
		infos, err := checkpoints.ListCheckpoints()
		if err != nil {
			slog.Warn("Unable to list checkpoints for chain discovery",
				"project", string(slug), "error", err)
			continue
		}
		chains = append(chains, discoverChains(infos)...)
	}
	sortDiscoveredChains(chains)
	return chains
}

// chainProjects names the projects to scan. The registry already holds the
// default project, so the fallback only covers a server built without one.
func (s *Server) chainProjects() []app.Project {
	if s.projects == nil {
		return []app.Project{app.DefaultProject}
	}
	return s.projects.Slugs()
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
