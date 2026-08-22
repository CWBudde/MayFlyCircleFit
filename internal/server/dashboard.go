package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

const (
	dashboardCampaignLimit      = 12
	dashboardMetricHistoryLimit = 100
)

type dashboardResponse struct {
	Campaigns   []ui.CampaignSummary  `json:"campaigns"`
	RunningJobs []dashboardRunningJob `json:"runningJobs"`
	Aggregates  dashboardAggregates   `json:"aggregates"`
	HostFacts   HostFacts             `json:"hostFacts"`
}

type dashboardRunningJob struct {
	ID               string            `json:"id"`
	Project          app.Project       `json:"project"`
	State            string            `json:"state"`
	Iterations       int               `json:"iterations"`
	MaxIters         int               `json:"maxIters"`
	Circles          int               `json:"circles"`
	RequestedCircles int               `json:"requestedCircles"`
	BestCost         float64           `json:"bestCost"`
	InitialCost      float64           `json:"initialCost"`
	CPS              float64           `json:"cps"`
	EvaluationWidth  int               `json:"evaluationWidth,omitempty"`
	ElapsedSec       float64           `json:"elapsedSec"`
	MetricHistory    []ui.MetricSample `json:"metricHistory"`
}

type dashboardAggregates struct {
	Running   int     `json:"running"`
	Pending   int     `json:"pending"`
	Completed int     `json:"completed"`
	CPS       float64 `json:"runningCps"`
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	response := s.dashboardPayload()

	w.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		slog.Error("Failed to encode dashboard response", "error", err)
	}
}

// dashboardPayload builds the whole read model, metric history included. The
// island seeds its sparklines from that history, so the JSON endpoint owes it.
func (s *Server) dashboardPayload() dashboardResponse {
	return s.buildDashboardPayload(true)
}

// dashboardPagePayload leaves the metric history out. The server-rendered page
// draws no sparkline, so converting up to dashboardMetricHistoryLimit samples
// per running job would be work thrown away on every page load.
func (s *Server) dashboardPagePayload() dashboardResponse {
	return s.buildDashboardPayload(false)
}

func (s *Server) buildDashboardPayload(includeHistory bool) dashboardResponse {
	runningJobs, aggregates := s.dashboardRows(includeHistory)

	return dashboardResponse{
		Campaigns:   s.dashboardCampaigns(),
		RunningJobs: runningJobs,
		Aggregates:  aggregates,
		HostFacts:   HostFactsFromMetadata(s.metadata),
	}
}

// dashboardCampaigns merges the planned schedules with the chains reconstructed
// from checkpoint lineage, then keeps the active ones and the most recently
// updated. A read model that cannot be built is reported as empty rather than
// as an error: the rest of the dashboard is still worth serving.
func (s *Server) dashboardCampaigns() []ui.CampaignSummary {
	summaries := make([]ui.CampaignSummary, 0)

	scheduleStore, err := s.scheduleStore()
	if err != nil {
		slog.Warn("Unable to open the schedule store for the dashboard", "error", err)
	} else if records, listErr := scheduleStore.ListSchedules(); listErr != nil {
		slog.Warn("Unable to list schedules for the dashboard", "error", listErr)
	} else {
		for i := range records {
			stages, loadErr := scheduleStore.LoadScheduleStages(records[i].ScheduleID)
			if loadErr != nil {
				slog.Warn("Unable to load schedule stages for the dashboard",
					"schedule_id", records[i].ScheduleID, "error", loadErr)
			}

			summaries = append(summaries, summarizeCampaign(&records[i], stages))
		}
	}

	summaries = append(summaries, chainCampaignSummaries(s.dashboardChains())...)
	sortDashboardCampaigns(summaries)

	if len(summaries) > dashboardCampaignLimit {
		summaries = summaries[:dashboardCampaignLimit]
	}

	return summaries
}

func (s *Server) dashboardRows(includeHistory bool) ([]dashboardRunningJob, dashboardAggregates) {
	counts, running := s.jobManager.StateCountsWithRunning()
	aggregates := dashboardAggregates{
		Running:   counts[StateRunning],
		Pending:   counts[StatePending],
		Completed: counts[StateCompleted],
	}

	runningRows := make([]dashboardRunningJob, 0, len(running))
	for _, job := range running {
		row := dashboardRunningJobFrom(job, includeHistory)
		aggregates.CPS += row.CPS
		runningRows = append(runningRows, row)
	}

	return runningRows, aggregates
}

func dashboardRunningJobFrom(job *Job, includeHistory bool) dashboardRunningJob {
	// The sparkline seed is bounded, so only the tail is worth converting: a
	// long-running job's history is refetched whole on every dashboard load.
	// A caller that draws no sparkline pays for none of it.
	var history []ui.MetricSample

	if includeHistory {
		samples := job.MetricHistory
		if len(samples) > dashboardMetricHistoryLimit {
			samples = samples[len(samples)-dashboardMetricHistoryLimit:]
		}

		history = make([]ui.MetricSample, 0, len(samples))
		for _, sample := range samples {
			history = append(history, ui.MetricSample{
				Iteration:    sample.Iteration,
				Evaluations:  sample.Evaluations,
				Cost:         sample.Cost,
				CPS:          sample.CPS,
				PSNR:         cloneFloat(sample.PSNR),
				PSNRInfinite: sample.PSNRInfinite,
				SSIM:         cloneFloat(sample.SSIM),
				Timestamp:    sample.Timestamp,
			})
		}
	}

	elapsed := jobElapsed(job)

	return dashboardRunningJob{
		ID:               job.ID,
		Project:          app.NormalizeProject(job.Project),
		State:            string(job.State),
		Iterations:       job.Iterations,
		MaxIters:         plannedOptimizerIterations(job.Config),
		Circles:          job.ActualCircles,
		RequestedCircles: job.RequestedCircles,
		BestCost:         job.BestCost,
		InitialCost:      job.InitialCost,
		CPS:              circlesPerSecond(job, elapsed),
		EvaluationWidth:  job.EvaluationWidth,
		ElapsedSec:       elapsed.Seconds(),
		MetricHistory:    history,
	}
}

func sortDashboardCampaigns(campaigns []ui.CampaignSummary) {
	sort.Slice(campaigns, func(i, j int) bool {
		iActive := isDashboardActiveCampaignState(campaigns[i].State)

		jActive := isDashboardActiveCampaignState(campaigns[j].State)
		if iActive != jActive {
			return iActive
		}

		if !campaigns[i].UpdatedAt.Equal(campaigns[j].UpdatedAt) {
			return campaigns[i].UpdatedAt.After(campaigns[j].UpdatedAt)
		}

		return campaigns[i].ID < campaigns[j].ID
	})
}

func isDashboardActiveCampaignState(state string) bool {
	return state == string(StateRunning) || state == string(StatePending)
}

func (s *Server) dashboardChains() []discoveredChain {
	return s.cachedAllChains()
}
