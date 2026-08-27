package ui

import (
	"bytes"
	"context"
	"math"
	"strconv"
	"strings"
	"testing"
)

func synthesizedCampaign() Campaign {
	accepted := 3

	return Campaign{
		ID:            "66666666-6666-4666-8666-666666666666",
		Name:          "synthesized campaign",
		State:         "running",
		Source:        CampaignFromSchedule,
		CampaignSeed:  42,
		HasSeed:       true,
		PlannedStages: 5,
		Stages: []CampaignStage{
			{
				Index: 0, Kind: "base", State: "completed", Circles: 8, BestCost: 812.5, HasBestCost: true,
				PSNR: 19.03, HasPSNR: true, ElapsedSec: 90, HasElapsed: true, JobID: "11111111-1111-4111-8111-111111111111",
			},
			{
				Index: 1, Kind: "extend", State: "completed", Circles: 16, BestCost: 640.25, HasBestCost: true,
				PSNR: 20.07, HasPSNR: true, ElapsedSec: 240, HasElapsed: true, JobID: "22222222-2222-4222-8222-222222222222",
			},
			{
				Index: 2, Kind: "polish", State: "completed", Circles: 16, BestCost: 631.75, HasBestCost: true,
				PSNR: 20.13, HasPSNR: true, ElapsedSec: 60, HasElapsed: true, AcceptedSweeps: &accepted,
				JobID: "33333333-3333-4333-8333-333333333333",
			},
			{
				Index: 3, Kind: "polish", State: "skipped", Circles: 16,
				Note: "polishing stopped paying after two barren stages",
			},
			{
				Index: 4, Kind: "extend", State: "running", Circles: 24,
				ElapsedAbsent: "The stage has not finished", JobID: "44444444-4444-4444-8444-444444444444",
			},
		},
	}
}

func renderCampaign(t *testing.T, campaign Campaign) string {
	t.Helper()

	var output bytes.Buffer

	err := CampaignPage(campaign).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render campaign: %v", err)
	}

	return output.String()
}

// TestCampaignPageShowsTheStageTable checks the columns the task named, and
// checks that the two it cannot populate say so rather than showing a zero.
func TestCampaignPageShowsTheStageTable(t *testing.T) {
	body := renderCampaign(t, synthesizedCampaign())
	for _, marker := range []string{
		"synthesized campaign",
		"Schedule · 5 of 5 stages recorded · seed 42",
		">Circles<", ">Cost<", ">PSNR<", ">Elapsed<", ">Accepted<",
		"812.500", "640.250", "631.750",
		"19.03 dB", "20.07 dB",
		"1m30s", "4m0s",
		"polishing stopped paying after two barren stages",
		"Skipped",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("campaign page missing %q", marker)
		}
	}
	// A stage that has not finished must show an em dash, never a zero cost:
	// zero is a perfect fit and would invert the reading of the table.
	if strings.Count(body, "0.000") > 0 {
		t.Error("campaign page renders an unmeasured cost as zero")
	}

	if !strings.Contains(body, "The stage has not finished") {
		t.Error("campaign page does not say why a running stage has no elapsed time")
	}

	if !strings.Contains(body, "The polisher does not persist its accepted-sweep count") {
		t.Error("campaign page does not explain the empty accepted-sweep column")
	}
}

// TestCampaignPlotIsSelfContained is the constraint that matters for a locally
// served UI: the chart is markup first, and nothing it loads comes from a
// network the host may not have. The React island added in Task 17.7 only
// upgrades the plot after mount, so the SVG stays the page's own drawing and
// every script the page pulls is embedded in this binary.
func TestCampaignPlotIsSelfContained(t *testing.T) {
	body := renderCampaign(t, synthesizedCampaign())
	if !strings.Contains(body, "<svg") || !strings.Contains(body, "<polyline") {
		t.Fatal("campaign page does not draw an inline SVG plot")
	}
	// Scoped to the attributes that actually fetch something: a bare
	// "https://" also matches an SVG namespace or a link in prose, neither of
	// which loads anything off the host.
	for _, forbidden := range []string{`src="http`, `src="//`, `href="http`, `href="//`, `url(http`, `url(//`} {
		if strings.Contains(bodyWithoutLayout(body), forbidden) {
			t.Errorf("campaign plot pulls in %q, but the UI is served with no external assets", forbidden)
		}
	}

	for _, source := range scriptSources(bodyWithoutLayout(body)) {
		if !strings.HasPrefix(source, StaticPrefix) {
			t.Errorf("campaign page loads %q, which is not an embedded asset under %s", source, StaticPrefix)
		}
	}
}

// TestCampaignPageMountsOneIsland guards the defect that left the campaign cost
// plot dead for as long as it existed: a data-island the mount registry does not
// know, nested inside an island that does. mountIslands creates a root for each
// match, so the outer island's first commit clears the inner one's container
// away. The campaign page renders exactly one mount point; the cost plot is the
// server-rendered SVG, which CampaignDetailIsland replaces along with the rest
// of the page.
func TestCampaignPageMountsOneIsland(t *testing.T) {
	body := renderCampaign(t, synthesizedCampaign())

	island := bodyWithoutLayout(body)
	if got := strings.Count(island, "data-island="); got != 1 {
		t.Errorf("campaign page renders %d island mount points, want exactly 1", got)
	}

	if !strings.Contains(island, "<svg") {
		t.Error("campaign page renders no server-side cost plot, so the page is blank without the bundle")
	}

	if !strings.Contains(body, BundleURL()) {
		t.Error("campaign page renders an island but never loads the bundle that mounts it")
	}
}

// scriptSources collects the src of every script tag in the markup. A script
// without one is inline — the JSON seed — and loads nothing.
func scriptSources(markup string) []string {
	var sources []string

	for _, fragment := range strings.Split(markup, "<script")[1:] {
		tag, _, ok := strings.Cut(fragment, ">")
		if !ok {
			continue
		}

		_, after, ok := strings.Cut(tag, `src="`)
		if !ok {
			continue
		}

		source, _, ok := strings.Cut(after, `"`)
		if ok {
			sources = append(sources, source)
		}
	}

	return sources
}

// bodyWithoutLayout drops the shared layout so the assertion is about the
// campaign markup rather than about the navigation the layout already owns.
func bodyWithoutLayout(body string) string {
	// Match the opening tag rather than the literal "<main>": the layout gives
	// it an id and a tabindex so the skip link can move focus into it, and a
	// helper that silently returns the whole document when it stops matching
	// would hand every caller the navigation's own external links.
	start := strings.Index(body, "<main")

	end := strings.Index(body, "</main>")
	if start < 0 || end < 0 {
		return body
	}

	return body[start:end]
}

// TestCampaignPlotPlacesEveryMeasuredStage checks the geometry rather than the
// markup: the plot exists to answer "is this schedule better than the last
// one", which needs the points in the right place, not merely present.
func TestCampaignPlotPlacesEveryMeasuredStage(t *testing.T) {
	plot := buildCampaignPlot(synthesizedCampaign().Stages)
	if plot.Empty {
		t.Fatal("plot is empty for a campaign with three measured stages")
	}

	if len(plot.Points) != 3 {
		t.Fatalf("plot has %d points, want the three stages that recorded a cost", len(plot.Points))
	}

	if !plot.Points[2].Polish {
		t.Error("the polish stage is not drawn with the polish marker")
	}
	// The cost axis runs the ordinary way up, so a campaign that is working
	// reads as a descending curve.
	if parseCoord(t, plot.Points[0].CY) >= parseCoord(t, plot.Points[2].CY) {
		t.Error("the plot does not put a higher cost higher up")
	}
	// Circle count grows left to right, and the polish stage does not move it.
	if parseCoord(t, plot.Points[0].CX) >= parseCoord(t, plot.Points[1].CX) {
		t.Error("the plot does not order the stages by circle count")
	}

	if plot.Points[1].CX != plot.Points[2].CX {
		t.Error("the polish stage moved along the circle axis, but it appends no circles")
	}

	if strings.Count(plot.Polyline, ",") != 3 {
		t.Errorf("polyline = %q, want one coordinate pair per measured stage", plot.Polyline)
	}
}

// TestCampaignPlotSurvivesDegenerateInput covers the two campaigns that would
// otherwise divide by zero: one stage, and a cost that never moved.
func TestCampaignPlotSurvivesDegenerateInput(t *testing.T) {
	tests := []struct {
		name   string
		stages []CampaignStage
	}{
		{name: "single stage", stages: []CampaignStage{{Kind: "base", Circles: 8, BestCost: 812.5, HasBestCost: true}}},
		{name: "flat cost", stages: []CampaignStage{
			{Index: 0, Kind: "base", Circles: 8, BestCost: 100, HasBestCost: true},
			{Index: 1, Kind: "polish", Circles: 8, BestCost: 100, HasBestCost: true},
		}},
		{name: "zero cost", stages: []CampaignStage{
			{Index: 0, Kind: "base", Circles: 8, BestCost: 0, HasBestCost: true},
			{Index: 1, Kind: "polish", Circles: 8, BestCost: 0, HasBestCost: true},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plot := buildCampaignPlot(test.stages)
			if plot.Empty {
				t.Fatal("plot is empty for a campaign that measured a cost")
			}

			for _, point := range plot.Points {
				if math.IsNaN(parseCoord(t, point.CX)) || math.IsNaN(parseCoord(t, point.CY)) {
					t.Fatalf("point %+v has a non-finite coordinate", point)
				}
			}
		})
	}
}

func TestCampaignPlotIgnoresUnmeasuredAndNonFiniteStages(t *testing.T) {
	plot := buildCampaignPlot([]CampaignStage{
		{Index: 0, Kind: "base", Circles: 8, State: "running"},
		{Index: 1, Kind: "extend", Circles: 16, BestCost: math.Inf(1), HasBestCost: true},
		{Index: 2, Kind: "extend", Circles: 24, BestCost: math.NaN(), HasBestCost: true},
	})
	if !plot.Empty {
		t.Fatalf("plot drew %d points from stages that measured nothing usable", len(plot.Points))
	}
}

func TestCampaignPageWithoutStages(t *testing.T) {
	body := renderCampaign(t, Campaign{ID: "66666666-6666-4666-8666-666666666666", State: "pending", Source: CampaignFromSchedule})
	if !strings.Contains(body, "No stage has been recorded yet.") {
		t.Error("an empty campaign does not say so")
	}

	if !strings.Contains(body, "No stage has recorded a cost yet.") {
		t.Error("an empty campaign still claims to have a plot")
	}
}

func parseCoord(t *testing.T, value string) float64 {
	t.Helper()

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("coordinate %q is not a plain number: %v", value, err)
	}

	return parsed
}

// TestCampaignPageShowsTheLatestCompletedStageImages covers the branch Task
// 17.8 added: the campaign page borrows the job detail page's viewer to show
// the newest stage that actually produced artifacts. The stage it picks matters
// as much as the viewer being there — pointing the viewer at a running stage
// would render three broken images.
func TestCampaignPageShowsTheLatestCompletedStageImages(t *testing.T) {
	body := renderCampaign(t, synthesizedCampaign())
	if !strings.Contains(body, `class="card image-viewer campaign-images"`) {
		t.Fatal("campaign page does not render the shared image viewer")
	}
	// Stage 4 is running and stage 3 was skipped, so stage 2 is the newest one
	// with artifacts to show.
	const polishJob = "33333333-3333-4333-8333-333333333333"
	if !strings.Contains(body, "/api/v1/jobs/"+polishJob+"/best.png") {
		t.Errorf("viewer is not pointed at the latest completed stage %s", polishJob)
	}

	for _, unfinished := range []string{"44444444-4444-4444-8444-444444444444"} {
		if strings.Contains(body, "/api/v1/jobs/"+unfinished+"/best.png") {
			t.Errorf("viewer is pointed at stage job %s, which has produced no artifacts", unfinished)
		}
	}

	if !strings.Contains(body, `data-view-mode="side-by-side"`) {
		t.Error("campaign viewer does not open on the side-by-side comparison")
	}

	// This viewer sits inside the campaign-detail island root, which mounting
	// replaces wholesale. It is the no-JavaScript fallback here; the live viewer
	// is the same React component, reached through Campaigns.tsx. A mount point
	// in this subtree would be a second React root over a doomed node.
	if strings.Contains(body, `data-island="image-viewer"`) {
		t.Error("campaign page advertises an image-viewer mount point inside the campaign-detail island")
	}
}

// TestCampaignPageWithoutCompletedStagesSaysSo is the other half of the branch:
// a campaign whose stages have all yet to finish must say there is nothing to
// show rather than render a viewer whose images 404.
func TestCampaignPageWithoutCompletedStagesSaysSo(t *testing.T) {
	body := renderCampaign(t, Campaign{
		ID:     "66666666-6666-4666-8666-666666666666",
		State:  "running",
		Source: CampaignFromSchedule,
		Stages: []CampaignStage{
			{Index: 0, Kind: "base", State: "running", Circles: 8, JobID: "11111111-1111-4111-8111-111111111111"},
		},
	})
	if strings.Contains(body, `class="card image-viewer`) {
		t.Error("campaign page renders an image viewer before any stage has finished")
	}

	if !strings.Contains(body, "No completed stage has produced image artifacts yet.") {
		t.Error("campaign page does not explain why it shows no images")
	}
}

// TestCampaignRatesPrintAMeasuredZero pins the presence rule the two rate rows
// follow: a trailing window that added circles and spent wall clock but removed
// no cost measured a rate of zero, which is a finding about the campaign rather
// than a number the page failed to get. The CLI's own block gates on the same
// denominators and prints 0.000000, so a dash here would put the two surfaces
// in disagreement about a measurement they both hold.
func TestCampaignRatesPrintAMeasuredZero(t *testing.T) {
	t.Parallel()

	flat := CampaignProjection{
		Projected:     true,
		RecentCircles: 1000, RecentElapsedSec: 3360,
		RecentGainPerCircle: 0, RecentGainPerHour: 0,
	}

	if got := campaignPerCircleRate(flat); got != "0.000000 cost/circle over the last 1000 circles" {
		t.Errorf("campaignPerCircleRate() = %q, want the measured zero spelled out", got)
	}

	if got := campaignPerHourRate(flat); got != "0.00 cost/hour over the last 56m0s" {
		t.Errorf("campaignPerHourRate() = %q, want the measured zero spelled out", got)
	}
	// The dash is reserved for the window that has no denominator to divide by.
	unmeasured := CampaignProjection{Projected: true}
	if got := campaignPerCircleRate(unmeasured); got != "—" {
		t.Errorf("campaignPerCircleRate() = %q, want a dash without circles to divide by", got)
	}

	if got := campaignPerHourRate(unmeasured); got != "—" {
		t.Errorf("campaignPerHourRate() = %q, want a dash without wall clock to divide by", got)
	}
}
