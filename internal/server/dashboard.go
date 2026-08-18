package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

const (
	dashboardCampaignLimit      = 12
	dashboardChainScanTTL       = 2 * time.Second
	dashboardMetricHistoryLimit = 100
)

type dashboardResponse struct {
	Campaigns   []ui.CampaignSummary  `json:"campaigns"`
	RunningJobs []dashboardRunningJob `json:"runningJobs"`
	Aggregates  dashboardAggregates   `json:"aggregates"`
	HostFacts   HostFacts             `json:"hostFacts"`
}

type dashboardRunningJob struct {
	ID              string            `json:"id"`
	Project         app.Project       `json:"project"`
	State           string            `json:"state"`
	Iterations      int               `json:"iterations"`
	MaxIters        int               `json:"maxIters"`
	BestCost        float64           `json:"bestCost"`
	InitialCost     float64           `json:"initialCost"`
	CPS             float64           `json:"cps"`
	EvaluationWidth int               `json:"evaluationWidth,omitempty"`
	ElapsedSec      float64           `json:"elapsedSec"`
	MetricHistory   []ui.MetricSample `json:"metricHistory"`
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

	runningJobs, aggregates := s.dashboardRows()
	response := dashboardResponse{
		Campaigns:   s.dashboardCampaigns(),
		RunningJobs: runningJobs,
		Aggregates:  aggregates,
		HostFacts:   HostFactsFromMetadata(s.metadata),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode dashboard response", "error", err)
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

func (s *Server) dashboardRows() ([]dashboardRunningJob, dashboardAggregates) {
	runningRows := make([]dashboardRunningJob, 0)
	counts := s.jobManager.CountStates()
	aggregates := dashboardAggregates{
		Running:   counts[StateRunning],
		Pending:   counts[StatePending],
		Completed: counts[StateCompleted],
	}

	for _, job := range s.jobManager.GetRunningJobs() {
		row := dashboardRunningJobFrom(job)
		aggregates.CPS += row.CPS
		runningRows = append(runningRows, row)
	}
	return runningRows, aggregates
}

func dashboardRunningJobFrom(job *Job) dashboardRunningJob {
	history := make([]ui.MetricSample, 0, len(job.MetricHistory))
	for _, sample := range job.MetricHistory {
		history = append(history, ui.MetricSample{
			Iteration:    sample.Iteration,
			Cost:         sample.Cost,
			PSNR:         cloneFloat(sample.PSNR),
			PSNRInfinite: sample.PSNRInfinite,
			SSIM:         cloneFloat(sample.SSIM),
		})
	}
	if len(history) > dashboardMetricHistoryLimit {
		history = history[len(history)-dashboardMetricHistoryLimit:]
	}

	elapsed := jobElapsed(job)
	return dashboardRunningJob{
		ID:              job.ID,
		Project:         app.NormalizeProject(job.Project),
		State:           string(job.State),
		Iterations:      job.Iterations,
		MaxIters:        plannedOptimizerIterations(job.Config),
		BestCost:        job.BestCost,
		InitialCost:     job.InitialCost,
		CPS:             circlesPerSecond(job, elapsed),
		EvaluationWidth: job.EvaluationWidth,
		ElapsedSec:      elapsed.Seconds(),
		MetricHistory:   history,
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
	s.dashboardMu.Lock()
	defer s.dashboardMu.Unlock()

	if !s.dashboardChainScanned.IsZero() && time.Since(s.dashboardChainScanned) < dashboardChainScanTTL {
		cached := make([]discoveredChain, len(s.dashboardChainScan))
		copy(cached, s.dashboardChainScan)
		return cached
	}

	chains := s.discoverAllChains()
	s.dashboardChainScan = make([]discoveredChain, len(chains))
	copy(s.dashboardChainScan, chains)
	s.dashboardChainScanned = time.Now()
	return chains
}
