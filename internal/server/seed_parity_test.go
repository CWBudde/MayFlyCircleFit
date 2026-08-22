package server

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

// The tests below extend TestDashboardPageSeedMatchesEndpointShape to the rest
// of the server-rendered seeds. Every page hands its island a JSON blob and the
// island then refetches the matching endpoint; if the two shapes drift, the
// first refetch replaces a populated view with an empty one. Each test builds
// both sides with equal contents by hand and compares them as wire names, so a
// tag renamed on one side alone fails here instead of in the browser.

// assertSeedSubset reports every seed leaf the endpoint does not carry under
// the same name and value. rename maps the seed paths the island deliberately
// translates onto the endpoint path they are read from; a path listed there is
// checked against its translation instead of against its own name.
func assertSeedSubset(t *testing.T, what string, seedKeys, endpointKeys map[string]any, rename map[string]string) {
	t.Helper()
	for path, want := range seedKeys {
		lookup := path
		if translated, ok := rename[path]; ok {
			lookup = translated
		}
		got, ok := endpointKeys[lookup]
		if !ok {
			t.Errorf("the %s seed carries %q but the endpoint has no %q", what, path, lookup)
			continue
		}
		if got != want {
			t.Errorf("%s: %q = %v in the endpoint payload, want %v as in the page seed at %q", what, lookup, got, want, path)
		}
	}
}

// assertEndpointHas names the endpoint-only wire names an island reads, which a
// seed that omits them cannot pin.
func assertEndpointHas(t *testing.T, what string, endpointKeys map[string]any, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, ok := endpointKeys[path]; !ok {
			t.Errorf("the %s endpoint payload has no %q, which the island reads", what, path)
		}
	}
}

// TestJobListSeedMatchesEndpointShape pins the job list contract. The seed is
// the flattened ui.JobListItem the templ page renders; the endpoint serves the
// nested JobSummary. The island reconciles the two in fromRaw
// (web/src/JobList.tsx), so the three configuration fields are checked against
// the nested names fromRaw actually reads rather than against their own. Every
// other field, and the page envelope, must agree name for name.
func TestJobListSeedMatchesEndpointShape(t *testing.T) {
	start := time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Second)

	seed := ui.JobListPage{
		Jobs: []ui.JobListItem{{
			ID:          "11111111-1111-1111-1111-111111111111",
			State:       string(StateCompleted),
			RefPath:     "assets/reference.png",
			Mode:        string(app.ModeBatch),
			Circles:     64,
			Iterations:  25,
			BestCost:    12.5,
			InitialCost: 40.5,
			StartTime:   start,
			EndTime:     &end,
			Error:       "boom",
		}},
		NextCursor: "cursor-token",
		Total:      3,
	}
	payload := jobListPage{
		Jobs: []JobSummary{{
			ID:      "11111111-1111-1111-1111-111111111111",
			Project: app.DefaultProject,
			State:   StateCompleted,
			Config: JobSummaryConfig{
				RefPath: "assets/reference.png",
				Mode:    app.ModeBatch,
				Circles: 64,
			},
			Iterations:  25,
			BestCost:    12.5,
			InitialCost: 40.5,
			StartTime:   start,
			EndTime:     &end,
			Error:       "boom",
		}},
		NextCursor: "cursor-token",
		Total:      3,
	}

	assertSeedSubset(t, "job list", jsonLeafKeys(t, seed), jsonLeafKeys(t, payload), map[string]string{
		"jobs.0.refPath": "jobs.0.config.refPath",
		"jobs.0.mode":    "jobs.0.config.mode",
		"jobs.0.circles": "jobs.0.config.circles",
	})

	// The paginating island reads these from the endpoint alone: the envelope
	// keys drive the sentinel, and the nested config keys are what fromRaw
	// flattens back into the seed's shape.
	assertEndpointHas(t, "job list", jsonLeafKeys(t, payload),
		"jobs.0.config.refPath", "jobs.0.config.mode", "jobs.0.config.circles",
		"nextCursor", "total",
	)
}

// TestCampaignListSeedMatchesEndpointShape proves the claim the source makes at
// internal/server/campaign_handlers.go:59-62: the browser read model is the
// templ seed, not the CLI's compact listing. The seed struct is declared inline
// in internal/ui/schedule.templ, so it is repeated here; the comparison runs
// both ways because "identical" is the documented contract, not "subset".
func TestCampaignListSeedMatchesEndpointShape(t *testing.T) {
	updated := time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC)
	summary := func(id string, source ui.CampaignSource) ui.CampaignSummary {
		return ui.CampaignSummary{
			ID: id, Name: "ladder", State: "completed", Source: source,
			RecordedStages: 2, PlannedStages: 3,
			CampaignSeries: []ui.CampaignSeriesPoint{
				{Index: 0, Kind: "base", Circles: 32, BestCost: 20.5, HasBestCost: true},
			},
			LeafJobID: "22222222-2222-2222-2222-222222222222",
			Circles:   64, BestCost: 12.5, HasBestCost: true,
			UpdatedAt: updated,
		}
	}
	schedules := []ui.CampaignSummary{summary("33333333-3333-3333-3333-333333333333", ui.CampaignFromSchedule)}
	chains := []ui.CampaignSummary{summary("44444444-4444-4444-4444-444444444444", ui.CampaignFromChain)}

	// The literal the templ page embeds, repeated field for field.
	seed := struct {
		Schedules []ui.CampaignSummary `json:"schedules"`
		Chains    []ui.CampaignSummary `json:"chains"`
	}{schedules, chains}
	payload := campaignViewList{Schedules: schedules, Chains: chains}

	seedKeys := jsonLeafKeys(t, seed)
	endpointKeys := jsonLeafKeys(t, payload)
	assertSeedSubset(t, "campaign list", seedKeys, endpointKeys, nil)
	assertSeedSubset(t, "campaign list (reversed)", endpointKeys, seedKeys, nil)

	// The wire names the campaign list island reads (web/src/Campaigns.tsx).
	assertEndpointHas(t, "campaign list", endpointKeys,
		"schedules.0.id", "schedules.0.name", "schedules.0.state", "schedules.0.source",
		"schedules.0.recordedStages", "schedules.0.plannedStages", "schedules.0.leafJobId",
		"schedules.0.circles", "schedules.0.bestCost", "schedules.0.hasBestCost", "schedules.0.updatedAt",
		"schedules.0.campaignSeries.0.index", "schedules.0.campaignSeries.0.kind",
		"schedules.0.campaignSeries.0.circles", "schedules.0.campaignSeries.0.bestCost",
		"schedules.0.campaignSeries.0.hasBestCost",
		"chains.0.id",
	)
}

// campaignDetailFixture is the campaign both sides of the detail contract carry.
func campaignDetailFixture() ui.Campaign {
	sweeps := 4
	return ui.Campaign{
		ID:     "55555555-5555-5555-5555-555555555555",
		Name:   "ladder",
		State:  "completed",
		Source: ui.CampaignFromSchedule,

		CampaignSeed:  42,
		HasSeed:       true,
		PlannedStages: 3,
		Stages: []ui.CampaignStage{
			{
				Index: 0, Kind: "base", State: "completed", Circles: 32,
				BestCost: 20.5, HasBestCost: true,
				PSNR: 31.25, PSNRInfinite: false, HasPSNR: true,
				Iterations: 25, Evaluations: 12345,
				ElapsedSec: 1.5, HasElapsed: true, ElapsedAbsent: "",
				JobID:       "66666666-6666-6666-6666-666666666666",
				ParentJobID: "",
				Note:        "",
			},
			{
				Index: 1, Kind: "polish", State: "completed", Circles: 64,
				BestCost: 12.5, HasBestCost: true,
				AcceptedSweeps: &sweeps,
				JobID:          "77777777-7777-7777-7777-777777777777",
				ParentJobID:    "66666666-6666-6666-6666-666666666666",
			},
		},
		Error: "",
	}
}

// TestCampaignDetailSeedMatchesEndpointShape covers the campaign detail
// pairing. Both sides are the same Go type today — internal/ui/schedule.templ
// seeds ui.Campaign and handleCampaignViewDetail encodes ui.Campaign — so the
// shapes cannot currently diverge. The test still compares them, because the
// day one side gains a projection type the divergence has to fail here, and it
// pins the wire names the detail island reads.
func TestCampaignDetailSeedMatchesEndpointShape(t *testing.T) {
	campaign := campaignDetailFixture()

	seedKeys := jsonLeafKeys(t, campaign)
	endpointKeys := jsonLeafKeys(t, campaign)
	assertSeedSubset(t, "campaign detail", seedKeys, endpointKeys, nil)
	assertSeedSubset(t, "campaign detail (reversed)", endpointKeys, seedKeys, nil)

	assertEndpointHas(t, "campaign detail", endpointKeys,
		"id", "name", "state", "source", "campaignSeed", "hasSeed", "plannedStages", "error",
		"stages.0.index", "stages.0.kind", "stages.0.state", "stages.0.circles",
		"stages.0.bestCost", "stages.0.hasBestCost",
		"stages.0.psnr", "stages.0.psnrInfinite", "stages.0.hasPsnr",
		"stages.0.iterations", "stages.0.evaluations",
		"stages.0.elapsedSec", "stages.0.hasElapsed", "stages.0.elapsedAbsent",
		"stages.0.jobId", "stages.0.parentJobId", "stages.0.note",
		"stages.1.acceptedSweeps",
	)
}

// jobControlsDataAttrs renders the job detail page and returns the data-*
// attributes of the job-controls island root, keyed by attribute name.
func jobControlsDataAttrs(t *testing.T, job ui.JobDetail) map[string]string {
	t.Helper()
	var rendered bytes.Buffer
	if err := ui.JobDetailPage(job).Render(context.Background(), &rendered); err != nil {
		t.Fatalf("render job detail page: %v", err)
	}
	body := rendered.String()
	start := strings.Index(body, `data-island="job-controls"`)
	if start < 0 {
		t.Fatalf("the job detail page has no job-controls island root")
	}
	// Walk back to the opening angle bracket, then forward to the tag's end.
	open := strings.LastIndex(body[:start], "<")
	end := strings.Index(body[start:], ">")
	if open < 0 || end < 0 {
		t.Fatalf("the job-controls island root is not a well-formed tag")
	}
	tag := body[open : start+end]

	attrs := make(map[string]string)
	for _, match := range regexp.MustCompile(`data-([a-z0-9-]+)="([^"]*)"`).FindAllStringSubmatch(tag, -1) {
		attrs[match[1]] = match[2]
	}
	return attrs
}

// TestJobDetailDataSeedMatchesStatusEndpoint covers the fifth pairing. The job
// detail page seeds its island through data-* attributes rather than a
// JSONScript, so jsonLeafKeys cannot compare the two sides directly: every
// attribute is a formatted string and the names are kebab-case, not JSON tags.
// What is testable, and what actually breaks the island, is the mapping the
// island performs in initialStatus (web/src/JobControls.tsx): each attribute it
// reads must exist in the /status payload under the name it refetches, holding
// the same value. Each row below is one of those reads.
func TestJobDetailDataSeedMatchesStatusEndpoint(t *testing.T) {
	start := time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC)
	job := ui.JobDetail{
		ID:           "88888888-8888-8888-8888-888888888888",
		State:        string(StateCompleted),
		Circles:      64,
		Iterations:   25,
		Evaluations:  12345,
		MaxIters:     100,
		BestCost:     12.5,
		InitialCost:  40.5,
		BestRevision: 7,
		CPS:          2.5,
		CanPolish:    true,
		StartTime:    start,
	}
	payload := jobStatusResponse{
		ID:            job.ID,
		Project:       app.DefaultProject,
		State:         JobState(job.State),
		BestCost:      job.BestCost,
		BestRevision:  job.BestRevision,
		InitialCost:   job.InitialCost,
		Iterations:    job.Iterations,
		Evaluations:   job.Evaluations,
		MaxIterations: job.MaxIters,
		Actions:       &jobActions{Polish: job.CanPolish},
		CPS:           job.CPS,
		StartTime:     start,
	}

	attrs := jobControlsDataAttrs(t, job)
	endpointKeys := jsonLeafKeys(t, payload)

	const (
		asString = "string"
		asNumber = "number"
		asBool   = "bool"
	)
	for _, testCase := range []struct {
		attr string
		path string
		kind string
	}{
		{attr: "job-id", path: "id", kind: asString},
		{attr: "job-state", path: "state", kind: asString},
		{attr: "best-revision", path: "bestRevision", kind: asNumber},
		{attr: "max-iterations", path: "maxIterations", kind: asNumber},
		{attr: "iterations", path: "iterations", kind: asNumber},
		{attr: "evaluations", path: "evaluations", kind: asNumber},
		{attr: "best-cost", path: "bestCost", kind: asNumber},
		{attr: "initial-cost", path: "initialCost", kind: asNumber},
		{attr: "cps", path: "cps", kind: asNumber},
		{attr: "can-polish", path: "actions.polish", kind: asBool},
	} {
		t.Run(testCase.attr, func(t *testing.T) {
			raw, ok := attrs[testCase.attr]
			if !ok {
				t.Fatalf("the job detail page no longer seeds data-%s, which the island reads", testCase.attr)
			}
			value, ok := endpointKeys[testCase.path]
			if !ok {
				t.Fatalf("the status payload has no %q, which the island refetches for data-%s", testCase.path, testCase.attr)
			}
			switch testCase.kind {
			case asString:
				if value != raw {
					t.Errorf("%q = %v in the status payload, want %q as in data-%s", testCase.path, value, raw, testCase.attr)
				}
			case asNumber:
				parsed, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					t.Fatalf("data-%s = %q is not a number: %v", testCase.attr, raw, err)
				}
				number, ok := value.(float64)
				if !ok {
					t.Fatalf("%q is %T in the status payload, want a number as in data-%s", testCase.path, value, testCase.attr)
				}
				if number != parsed {
					t.Errorf("%q = %v in the status payload, want %v as in data-%s", testCase.path, number, parsed, testCase.attr)
				}
			case asBool:
				parsed, err := strconv.ParseBool(raw)
				if err != nil {
					t.Fatalf("data-%s = %q is not a boolean: %v", testCase.attr, raw, err)
				}
				if value != parsed {
					t.Errorf("%q = %v in the status payload, want %v as in data-%s", testCase.path, value, parsed, testCase.attr)
				}
			}
		})
	}
}
