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

	scheduleStore, err := s.scheduleStore()
	summaries := make([]ui.CampaignSummary, 0)
	if err == nil {
		records, listErr := scheduleStore.ListSchedules()
		if listErr != nil {
			slog.Warn("Failed to list schedules for dashboard", "error", listErr)
		} else {
			for i := range records {
				stages, loadErr := scheduleStore.LoadScheduleStages(records[i].ScheduleID)
				if loadErr != nil {
					slog.Warn("Unable to load schedule stages for dashboard", "schedule_id", records[i].ScheduleID, "error", loadErr)
					stages = nil
				}
				summaries = append(summaries, summarizeCampaign(&records[i], stages))
			}
		}
	}
	chains := chainCampaignSummaries(s.dashboardChains())
	summaries = append(summaries, chains...)
	sortDashboardCampaigns(summaries)
	if len(summaries) > dashboardCampaignLimit {
		summaries = summaries[:dashboardCampaignLimit]
	}

	runningJobs, aggregates := s.dashboardRows()
	response := dashboardResponse{
		Campaigns:   summaries,
		RunningJobs: runningJobs,
		Aggregates:  aggregates,
		HostFacts:   HostFactsFromMetadata(s.metadata),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode dashboard response", "error", err)
	}
}

func (s *Server) dashboardRows() ([]dashboardRunningJob, dashboardAggregates) {
	runningRows := make([]dashboardRunningJob, 0)
	aggregates := dashboardAggregates{}
	allJobs := s.jobManager.ListJobs()
	for _, job := range allJobs {
		switch job.State {
		case StateRunning:
			aggregates.Running++
		case StatePending:
			aggregates.Pending++
		case StateCompleted:
			aggregates.Completed++
		}
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

	elapsed := time.Since(job.StartTime).Seconds()
	iterations := plannedOptimizerIterations(job.Config)
	return dashboardRunningJob{
		ID:              job.ID,
		Project:         app.NormalizeProject(job.Project),
		State:           string(job.State),
		Iterations:      job.Iterations,
		MaxIters:        iterations,
		BestCost:        job.BestCost,
		InitialCost:     job.InitialCost,
		CPS:             jobProgressCPS(job),
		EvaluationWidth: job.EvaluationWidth,
		ElapsedSec:      elapsed,
		MetricHistory:   history,
	}
}

func jobProgressCPS(job *Job) float64 {
	elapsed := time.Since(job.StartTime).Seconds()
	if elapsed <= 0 {
		return 0
	}
	totalCircles := job.Evaluations * max(1, len(job.BestParams)/7)
	return float64(totalCircles) / elapsed
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
