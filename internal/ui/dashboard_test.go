package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestDashboardPageRenders(t *testing.T) {
	page := DashboardPageData{
		Campaigns: []CampaignSummary{{
			ID:             "11111111-1111-1111-1111-111111111111",
			Name:           "Test campaign",
			State:          "running",
			Source:         CampaignFromSchedule,
			RecordedStages: 2,
			PlannedStages:  3,
			Circles:        128,
			BestCost:       12.5,
			HasBestCost:    true,
			UpdatedAt:      time.Date(2026, 8, 13, 12, 34, 56, 0, time.UTC),
		},
			{
				ID:      "22222222-2222-2222-2222-222222222222",
				Source:  CampaignFromChain,
				State:   "completed",
				Circles: 64,
			},
		},
		RunningJobs: []DashboardRunningJob{{
			ID:          "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			Project:     "default",
			State:       "running",
			Iterations:  1234,
			MaxIters:    2000,
			BestCost:    0.1234,
			InitialCost: 3.4,
			CPS:         5.67,
		}},
		Aggregates: DashboardAggregates{
			Running:   1,
			Pending:   2,
			Completed: 3,
			CPS:       5.67,
		},
		HostFacts: HostFacts{
			GOOS:      "linux",
			GOARCH:    "amd64",
			SIMD:      "avx2",
			GPUState:  "available",
			Version:   "dev",
			Commit:    "abc123",
			BuildDate: "2026-08-13",
			GoVersion: "go1.24",
		},
	}

	var output bytes.Buffer
	if err := DashboardPage(page).Render(context.Background(), &output); err != nil {
		t.Fatalf("render dashboard page: %v", err)
	}
	body := output.String()
	for _, marker := range []string{
		`Dashboard`,
		`Running jobs`,
		`Campaigns`,
		`/jobs`,
		`/schedules`,
		`data-island="placeholder"`,
		`data-island-label="dashboard"`,
		`/api/v1/stream`,
		`>CPS</th>`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered dashboard page missing %q", marker)
		}
	}
}

func TestDashboardCampaignURL(t *testing.T) {
	if got, want := dashboardCampaignURL(CampaignSummary{Source: CampaignFromChain, ID: "abc"}), "/chains/abc"; got != want {
		t.Fatalf("dashboardCampaignURL() = %q, want %q", got, want)
	}
	if got, want := dashboardCampaignURL(CampaignSummary{Source: CampaignFromSchedule, ID: "def"}), "/schedules/def"; got != want {
		t.Fatalf("dashboardCampaignURL() = %q, want %q", got, want)
	}
}

func TestFormatJobImprovement(t *testing.T) {
	for _, test := range []struct {
		name          string
		initial, best float64
		want          string
	}{
		{"improving", 10, 5, "↓ 50.0%"},
		{"no_initial", 0, 5, "—"},
		{"worse", 5, 10, "—"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := formatJobImprovement(test.initial, test.best); got != test.want {
				t.Fatalf("formatJobImprovement(%v, %v) = %q, want %q", test.initial, test.best, got, test.want)
			}
		})
	}
}
