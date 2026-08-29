package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDashboardPageRenders(t *testing.T) {
	t.Parallel()

	page := DashboardPageData{
		Campaigns: []CampaignSummary{
			{
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
			GPU:       GPUFacts{State: "available"},
			Version:   "dev",
			Commit:    "abc123",
			BuildDate: "2026-08-13",
			GoVersion: "go1.24",
		},
	}

	var output bytes.Buffer

	err := DashboardPage(page).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render dashboard page: %v", err)
	}

	body := output.String()
	for _, marker := range []string{
		`Dashboard`,
		`Running jobs`,
		`Campaigns`,
		`/jobs`,
		`/schedules`,
		`data-island="dashboard"`,
		`data-island-label="dashboard"`,
		`/api/v1/events`,
		`>CPS</th>`,
		`id="dashboard-page"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered dashboard page missing %q", marker)
		}
	}
}

// TestDashboardPageSeedsTheIsland pins the JSON the island reads before its
// first fetch. The names matter as much as the values: the island parses this
// script and later parses /api/v1/dashboard into the same shape, so a tag
// renamed on only one side would leave the page correct until it refreshed.
func TestDashboardPageSeedsTheIsland(t *testing.T) {
	t.Parallel()

	page := DashboardPageData{
		RunningJobs: []DashboardRunningJob{{ID: "job-1", Project: "default", State: "running", Iterations: 7, MaxIters: 10, CPS: 1.5}},
		Aggregates:  DashboardAggregates{Running: 1, Pending: 2, Completed: 3, CPS: 1.5},
		HostFacts:   HostFacts{GOARCH: "amd64", SIMD: "avx2", GPU: GPUFacts{State: "available"}},
	}

	var output bytes.Buffer

	err := DashboardPage(page).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render dashboard page: %v", err)
	}

	seed := extractDashboardSeed(t, output.String())

	var decoded struct {
		RunningJobs []struct {
			ID  string  `json:"id"`
			CPS float64 `json:"cps"`
		} `json:"runningJobs"`
		Aggregates struct {
			Running int     `json:"running"`
			CPS     float64 `json:"runningCps"`
		} `json:"aggregates"`
		HostFacts struct {
			GOARCH string `json:"goarch"`
			GPU    struct {
				State string `json:"state"`
			} `json:"gpu"`
		} `json:"hostFacts"`
	}

	err = json.Unmarshal([]byte(seed), &decoded)
	if err != nil {
		t.Fatalf("decode dashboard seed: %v", err)
	}

	if len(decoded.RunningJobs) != 1 || decoded.RunningJobs[0].ID != "job-1" || decoded.RunningJobs[0].CPS != 1.5 {
		t.Errorf("seed running jobs = %+v, want one job-1 row at 1.5 cps", decoded.RunningJobs)
	}

	if decoded.Aggregates.Running != 1 || decoded.Aggregates.CPS != 1.5 {
		t.Errorf("seed aggregates = %+v, want running 1 at 1.5 cps", decoded.Aggregates)
	}

	if decoded.HostFacts.GOARCH != "amd64" || decoded.HostFacts.GPU.State != "available" {
		t.Errorf("seed host facts = %+v, want amd64 with an available GPU", decoded.HostFacts)
	}
}

func extractDashboardSeed(t *testing.T, body string) string {
	t.Helper()
	const open = `id="dashboard-page"`

	start := strings.Index(body, open)
	if start < 0 {
		t.Fatal("rendered dashboard page has no seed script")
	}

	start = strings.Index(body[start:], ">")
	if start < 0 {
		t.Fatal("dashboard seed script tag is unterminated")
	}

	rest := body[strings.Index(body, open)+start+1:]

	before, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("dashboard seed script is unterminated")
	}

	return before
}

func TestDashboardCampaignURL(t *testing.T) {
	t.Parallel()

	if got, want := dashboardCampaignURL(CampaignSummary{Source: CampaignFromChain, ID: "abc"}), "/chains/abc"; got != want {
		t.Fatalf("dashboardCampaignURL() = %q, want %q", got, want)
	}

	if got, want := dashboardCampaignURL(CampaignSummary{Source: CampaignFromSchedule, ID: "def"}), "/schedules/def"; got != want {
		t.Fatalf("dashboardCampaignURL() = %q, want %q", got, want)
	}
}

func dashboardPageFixture() DashboardPageData {
	return DashboardPageData{
		Campaigns: []CampaignSummary{
			{
				ID:             "11111111-1111-1111-1111-111111111111",
				Name:           "Chain campaign",
				State:          "running",
				Source:         CampaignFromChain,
				RecordedStages: 2,
				PlannedStages:  3,
				Circles:        32,
				BestCost:       18.25,
				HasBestCost:    true,
				UpdatedAt:      time.Date(2026, 8, 13, 12, 34, 56, 0, time.UTC),
			},
			{
				ID:             "22222222-2222-2222-2222-222222222222",
				Name:           "Scheduled campaign",
				State:          "pending",
				Source:         CampaignFromSchedule,
				LeafJobID:      "33333333-3333-3333-3333-333333333333",
				RecordedStages: 1,
				Circles:        16,
				CampaignSeries: []CampaignSeriesPoint{{Kind: "base", Circles: 16, BestCost: 99.9, HasBestCost: true}},
				UpdatedAt:      time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC),
			},
		},
		RunningJobs: []DashboardRunningJob{
			{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Project: "default", State: "running", Iterations: 55, MaxIters: 120, BestCost: 9.9, InitialCost: 11.1, CPS: 4.4},
			{ID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", Project: "default", State: "completed", Iterations: 10, MaxIters: 10, BestCost: 1.1, InitialCost: 2.2, CPS: 0.2},
		},
		Aggregates: DashboardAggregates{
			Running:   1,
			Pending:   1,
			Completed: 0,
			CPS:       4.4,
		},
		HostFacts: HostFacts{
			GOOS:   "linux",
			GOARCH: "amd64",
			SIMD:   "avx2",
			GPU:    GPUFacts{State: "available"},
		},
	}
}

func renderDashboardPage(t *testing.T, page DashboardPageData) string {
	t.Helper()

	var output bytes.Buffer

	err := DashboardPage(page).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render dashboard page: %v", err)
	}

	return output.String()
}

func TestDashboardPageRendersCampaignCardsWithLinksAndActions(t *testing.T) {
	t.Parallel()

	body := renderDashboardPage(t, dashboardPageFixture())

	markers := []string{
		"/chains/11111111-1111-1111-1111-111111111111",
		"/schedules/22222222-2222-2222-2222-222222222222",
		"/api/v1/events",
		`Running jobs`,
		`Campaigns`,
		`Open campaigns`,
		`Open jobs page`,
		`Stream updates`,
		BundleURL(),
		`Dashboard`,
		`Monitor jobs, campaigns, and host state from one view.`,
	}
	for _, marker := range markers {
		if !strings.Contains(body, marker) {
			t.Errorf("dashboard page missing %q", marker)
		}
	}
}

func TestDashboardPageDisplaysRunningJobsAndAggregates(t *testing.T) {
	t.Parallel()

	body := renderDashboardPage(t, dashboardPageFixture())
	for _, marker := range []string{
		`<strong>Running:</strong> 1`,
		`<strong>Pending:</strong> 1`,
		`<strong>Completed:</strong> 0`,
		`<strong>Circles/sec:</strong> 4.40`,
		`Host`,
		`<strong>SIMD:</strong> avx2`,
		`<strong>GPU:</strong> available`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("dashboard page missing %q", marker)
		}
	}
}

func TestFormatJobImprovement(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			if got := formatJobImprovement(test.initial, test.best); got != test.want {
				t.Fatalf("formatJobImprovement(%v, %v) = %q, want %q", test.initial, test.best, got, test.want)
			}
		})
	}
}

// TestDashboardRunningJobsTableIsAccessible pins the table's accessibility
// scaffolding. The seven columns are wider than a phone, so the scroller is a
// named region with a tab stop rather than a bare overflow div a keyboard
// cannot reach, and every header declares the column it heads.
func TestDashboardRunningJobsTableIsAccessible(t *testing.T) {
	t.Parallel()

	body := renderDashboardPage(t, dashboardPageFixture())

	for _, marker := range []string{
		`class="table-scroll"`,
		`role="region"`,
		`aria-label="Running jobs"`,
		`tabindex="0"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("running jobs scroller missing %q", marker)
		}
	}

	for _, column := range []string{"Job", "State", "Circles", "Iter", "Best cost", "Gain", "CPS"} {
		if !strings.Contains(body, `scope="col"`) || !strings.Contains(body, ">"+column+"</th>") {
			t.Errorf("running jobs table has no scoped header for %q", column)
		}
	}

	if got, want := strings.Count(body, `<th scope="col"`), 7; got != want {
		t.Errorf("scoped headers = %d, want %d", got, want)
	}
}

// TestDashboardUsesAccessibleSuccessText guards the contrast fix: the gain
// column is text, and --success-color as text on the light surface is 2.54:1.
func TestDashboardUsesAccessibleSuccessText(t *testing.T) {
	t.Parallel()

	body := renderDashboardPage(t, dashboardPageFixture())

	if !strings.Contains(body, "var(--success-text-strong)") {
		t.Error("dashboard page does not use --success-text-strong for the gain column")
	}

	if strings.Contains(body, "color: var(--success-color)") {
		t.Error("dashboard page still uses --success-color as a text color")
	}
}

// TestDashboardRowsWrapOnNarrowViewports keeps the shared wrap vocabulary in
// place: the summary grid has to collapse below its 220px track, and the
// section and campaign headers have to wrap instead of colliding.
func TestDashboardRowsWrapOnNarrowViewports(t *testing.T) {
	t.Parallel()

	body := renderDashboardPage(t, dashboardPageFixture())

	for _, marker := range []string{
		`minmax(min(220px, 100%), 1fr)`,
		`class="row-between"`,
		`class="row-between row-between-top"`,
		`class="row-end"`,
		`class="meta-row"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("dashboard page missing %q", marker)
		}
	}

	// The layout's own stylesheet legitimately declares space-between, so the
	// negative assertion only covers the markup below it.
	_, markup, ok := strings.Cut(body, "</style>")
	if !ok {
		t.Fatal("rendered dashboard page has no layout stylesheet")
	}

	if strings.Contains(markup, "justify-content: space-between") {
		t.Error("dashboard page still lays out a row with an inline space-between instead of .row-between")
	}
}
