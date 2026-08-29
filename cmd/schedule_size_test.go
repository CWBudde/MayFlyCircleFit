package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/server"
	"github.com/cwbudde/circlefit/internal/store"
)

// Task 16.7: a campaign of the size the schedule format allows has to be
// printable. `schedule status` used to refuse one at roughly 865 stages,
// because the stage listing carried every stage's whole JobConfig and the CLI
// declines to decode a response over app.MaxCLIResponseBytes.
//
// These tests drive the command, so the cap is enforced by the same reader the
// operator hits. internal/server measures the bytes the handler produces for
// the same stage count; here the assertion is that the command reads and
// prints it.

// stageLimitCampaignDocument expands to exactly app.MaxScheduleStages. It is
// the same shape internal/server measures its response against — one stage per
// circle up to the circle limit, then polishing — and is spelled out here
// rather than shared, because a fixture the two packages passed between them
// would let one of them change what the other believes it is measuring.
const stageLimitCampaignDocument = `{
  "schemaVersion": 1,
  "name": "stage-limit campaign",
  "seed": 42,
  "base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "batchSize": 8, "iters": 200, "popSize": 30},
  "steps": [
    {"type": "extend", "repeat": 992, "additionalCircles": 1},
    {"type": "polish", "repeat": 1024},
    {"type": "polish", "repeat": 1024},
    {"type": "polish", "repeat": 1024},
    {"type": "polish", "repeat": 31}
  ]
}`

// stageLimitDetail synthesizes the listing for a campaign at the stage limit:
// every planned stage recorded and completed except the last, which is still
// pending so the projection has something to project.
func stageLimitDetail(t *testing.T) scheduleDetailResponse {
	t.Helper()

	document, err := app.ParseSchedule([]byte(stageLimitCampaignDocument))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	plan, err := document.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if len(plan) != app.MaxScheduleStages {
		t.Fatalf("the fixture expands to %d stages, want %d", len(plan), app.MaxScheduleStages)
	}

	detail := scheduleDetailResponse{Document: *document}
	detail.ScheduleID = testScheduleID
	detail.Name = document.Name
	detail.State = string(store.ScheduleStateRunning)
	detail.CampaignSeed = document.Seed
	detail.TotalStages = len(plan)
	detail.CreatedAt = time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	detail.UpdatedAt = detail.CreatedAt

	for _, stage := range plan {
		elapsed := (97 * time.Second).Nanoseconds()

		summary := scheduleStageSummaryResponse{
			Index:        stage.Index,
			Kind:         stage.Kind,
			State:        store.ScheduleStateCompleted,
			Circles:      stage.Circles,
			BestCost:     812.5 - float64(stage.Index)/3.0,
			ElapsedNanos: &elapsed,
			JobID:        fmt.Sprintf("00000000-0000-4000-8000-%012d", stage.Index),
		}
		if stage.Index == len(plan)-1 {
			summary = scheduleStageSummaryResponse{
				Index: stage.Index, Kind: stage.Kind,
				State: store.ScheduleStatePending, Circles: stage.Circles,
			}
		}

		detail.Stages = append(detail.Stages, summary)
	}

	return detail
}

// TestScheduleStatusPrintsACampaignAtTheStageLimit is the Task 16.7 acceptance
// check: the stage table and the projection are printed for a campaign at
// MaxScheduleStages, and the response the command read is measured.
func TestScheduleStatusPrintsACampaignAtTheStageLimit(t *testing.T) {
	detail := stageLimitDetail(t)

	payload, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}

	t.Logf("%d stages in %d bytes (%d per stage), cap %d",
		len(detail.Stages), len(payload), len(payload)/len(detail.Stages), maxCLIResponseBytes)

	if len(payload) > maxCLIResponseBytes {
		t.Fatalf("the listing is %d bytes, over the %d the CLI decodes", len(payload), maxCLIResponseBytes)
	}

	_, stub := newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	})

	var output bytes.Buffer

	err = runScheduleStatus(testCommand(context.Background(), &output), []string{testScheduleID})
	if err != nil {
		t.Fatalf("runScheduleStatus() error = %v", err)
	}

	if path := <-stub.paths; path != "/api/v1/schedules/"+testScheduleID {
		t.Fatalf("requested %q, want the schedule detail path", path)
	}

	body := output.String()
	for _, marker := range []string{
		fmt.Sprintf("Stages: %d recorded of %d planned", app.MaxScheduleStages, app.MaxScheduleStages),
		"Projection (from measured stage wall clock only)",
		"Remaining: 1m37s",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("schedule status output missing %q:\n%s", marker, lastLines(body, 12))
		}
	}
	// The first and the last row of the table, so the whole campaign is present
	// and not a truncated prefix of it.
	if !hasRow(body, "0", "base", "completed") {
		t.Errorf("schedule status printed no row for the base stage:\n%s", lastLines(body, 12))
	}

	if !hasRow(body, strconv.Itoa(app.MaxScheduleStages-1), "polish", "pending") {
		t.Errorf("schedule status printed no row for the last stage:\n%s", lastLines(body, 12))
	}
}

// TestScheduleImportPrintsAChainAtTheStageLimit is the same check for
// `schedule import`, whose chain view is on the same curve.
func TestScheduleImportPrintsAChainAtTheStageLimit(t *testing.T) {
	detail := chainDetailResponse{
		LeafJobID: fmt.Sprintf("00000000-0000-4000-8000-%012d", app.MaxScheduleStages-1),
		RootJobID: fmt.Sprintf("00000000-0000-4000-8000-%012d", 0),
	}
	for index := range app.MaxScheduleStages {
		kind := "extend"
		if index == 0 {
			kind = "base"
		}

		detail.Stages = append(detail.Stages, chainStageResponse{
			Index:      index,
			Kind:       kind,
			JobID:      fmt.Sprintf("00000000-0000-4000-8000-%012d", index),
			Circles:    8 + index%(app.MaxCircles-8),
			BestCost:   812.5 - float64(index)/3.0,
			Iterations: 200,
		})
	}

	payload, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal chain: %v", err)
	}

	t.Logf("%d stages in %d bytes (%d per stage), cap %d",
		len(detail.Stages), len(payload), len(payload)/len(detail.Stages), maxCLIResponseBytes)

	if len(payload) > maxCLIResponseBytes {
		t.Fatalf("the chain is %d bytes, over the %d the CLI decodes", len(payload), maxCLIResponseBytes)
	}

	_, _ = newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	})

	var output bytes.Buffer

	err = runScheduleImport(testCommand(context.Background(), &output), []string{detail.LeafJobID})
	if err != nil {
		t.Fatalf("runScheduleImport() error = %v", err)
	}

	body := output.String()
	if !strings.Contains(body, fmt.Sprintf("Stages: %d", app.MaxScheduleStages)) {
		t.Errorf("schedule import did not report the stage count:\n%s", lastLines(body, 12))
	}

	if !hasRow(body, "0", "base") {
		t.Errorf("schedule import printed no row for the base stage:\n%s", lastLines(body, 12))
	}

	if !hasRow(body, strconv.Itoa(app.MaxScheduleStages-1), "extend") {
		t.Errorf("schedule import printed no row for the last stage:\n%s", lastLines(body, 12))
	}
}

// TestProjectionIsUnchangedByTheSummaryProjection is the Task 16.7 requirement
// that the estimate must not move because its input got smaller. It runs
// against a real server over a real store, so the summaries are the ones the
// endpoint produces rather than ones this test wrote, and compares the
// projection derived from them with the projection derived from the stage
// records on disk, field by field.
func TestProjectionIsUnchangedByTheSummaryProjection(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	document, err := app.ParseSchedule([]byte(projectionCampaignDocument))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	record, err := store.NewScheduleRecord(testScheduleID, *document)
	if err != nil {
		t.Fatalf("NewScheduleRecord() error = %v", err)
	}
	// A paused campaign keeps its remaining stages — so there is something to
	// project — without the restored server driving one of them.
	record.State = store.ScheduleStatePaused

	err = persistence.SaveSchedule(record)
	if err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}

	plan, err := record.Document.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	records := recordedStages(t, persistence, plan)

	srv := server.NewServerWithOptions(":0", persistence, server.ServerOptions{DataRoot: root})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := srv.Shutdown(ctx)
		if err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	previous := scheduleServerURL
	scheduleServerURL = httpServer.URL

	t.Cleanup(func() { scheduleServerURL = previous })

	// The command itself first: it has to decode what this server sends, and it
	// refuses a field it does not know about.
	var output bytes.Buffer

	err = runScheduleStatus(testCommand(context.Background(), &output), []string{testScheduleID})
	if err != nil {
		t.Fatalf("runScheduleStatus() error = %v", err)
	}

	if !strings.Contains(output.String(), "Remaining once the campaign runs again") {
		t.Fatalf("status output has no projection:\n%s", output.String())
	}

	body, err := requestCLI(context.Background(), http.MethodGet,
		scheduleBaseURL()+"/schedules/"+testScheduleID)
	if err != nil {
		t.Fatalf("read the schedule: %v", err)
	}

	var detail scheduleDetailResponse

	err = decodeCLIResponse(body, &detail)
	if err != nil {
		t.Fatalf("decode the schedule: %v", err)
	}

	asOf := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	fromSummaries := app.ProjectScheduleFinish(plan, stageTimings(detail.Stages), asOf)
	fromRecords := app.ProjectScheduleFinish(plan, timingsFromRecords(records), asOf)

	if fromSummaries.RemainingStages != fromRecords.RemainingStages ||
		fromSummaries.Complete != fromRecords.Complete ||
		fromSummaries.Remaining != fromRecords.Remaining ||
		fromSummaries.Firm != fromRecords.Firm ||
		!fromSummaries.FinishBy.Equal(fromRecords.FinishBy) ||
		!fromSummaries.EarliestFinish.Equal(fromRecords.EarliestFinish) {
		t.Fatalf("projection from the listing = %+v, from the records = %+v", fromSummaries, fromRecords)
	}
	// The cost projection is comparable, so the whole of it is compared rather
	// than the handful of fields a reviewer thought to list.
	if fromSummaries.Cost != fromRecords.Cost {
		t.Fatalf("cost projection from the listing = %+v, from the records = %+v",
			fromSummaries.Cost, fromRecords.Cost)
	}

	if !fromRecords.Cost.Projected() {
		t.Fatalf("the fixture projects no cost, so the cost comparison proves nothing: %+v",
			fromRecords.Cost)
	}

	if len(fromSummaries.Kinds) != len(fromRecords.Kinds) {
		t.Fatalf("projected %d kinds from the listing, %d from the records",
			len(fromSummaries.Kinds), len(fromRecords.Kinds))
	}

	for i, kind := range fromSummaries.Kinds {
		if kind != fromRecords.Kinds[i] {
			t.Errorf("kind %d from the listing = %+v, from the records = %+v",
				i, kind, fromRecords.Kinds[i])
		}
	}
	// A projection of nothing would satisfy every comparison above.
	if !fromRecords.Complete || fromRecords.Remaining == 0 {
		t.Fatalf("the fixture projects nothing, so the comparison proves nothing: %+v", fromRecords)
	}
}

// projectionCampaignDocument is a short campaign: a base stage, four extends,
// and a polish, which is enough for both kinds to reach MinProjectionSamples
// while leaving stages of each kind still to run.
const projectionCampaignDocument = `{
  "schemaVersion": 1,
  "name": "projection parity",
  "seed": 42,
  "base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "batchSize": 8, "iters": 200, "popSize": 30},
  "steps": [
    {"type": "extend", "repeat": 4, "additionalCircles": 8},
    {"type": "polish", "repeat": 3}
  ]
}`

// refillLimitedStageIndex is the stage recordedStages settles short of its
// planned circle count. It is an extend stage in the middle of the campaign, so
// it lands inside the trailing window the cost projection extrapolates from and
// a path that read the planned count instead would compute a different rate.
const refillLimitedStageIndex = 3

// recordedStages writes every stage but the last as completed, so both kinds
// clear MinProjectionSamples and one stage is still to come.
func recordedStages(t *testing.T, persistence store.ScheduleStore, plan []app.ScheduleStage) []store.ScheduleStageRecord {
	t.Helper()

	elapsed := map[app.ScheduleStageKind]time.Duration{
		app.ScheduleStageBase:   3 * time.Minute,
		app.ScheduleStageExtend: 2 * time.Minute,
		app.ScheduleStagePolish: 7 * time.Minute,
	}
	at := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)

	recorded := make([]store.ScheduleStageRecord, 0, len(plan))
	for _, stage := range plan[:len(plan)-1] {
		record := store.NewScheduleStageRecord(testScheduleID, stage)
		record.State = store.ScheduleStateCompleted
		record.JobID = fmt.Sprintf("00000000-0000-4000-8000-%012d", stage.Index)
		record.BestCost = 812.5 - float64(stage.Index)
		// One stage stops a circle short of its request, which is what a batch
		// stage that hit its refill limit does. Both projection paths have to
		// read that count rather than the plan's, so the parity comparison
		// below only holds if both of them do.
		if stage.Index == refillLimitedStageIndex {
			record.ActualCircles = stage.Circles - 1
		}

		started, completed := at, at.Add(elapsed[stage.Kind])
		record.StartedAt, record.CompletedAt = &started, &completed
		at = completed

		err := persistence.SaveScheduleStage(testScheduleID, record)
		if err != nil {
			t.Fatalf("SaveScheduleStage(%d) error = %v", stage.Index, err)
		}

		recorded = append(recorded, *record)
	}

	return recorded
}

// timingsFromRecords is the reduction the CLI performed before the listing
// became a projection: it reads the two timestamps off the full stage record.
// It stays here as the reference the summary is compared against.
//
// It fills the cost fields on the same rule stageTimings does, and has to: the
// two are compared field for field, and a reference that left them empty would
// let the comparison pass by agreeing that nothing was projected. For the same
// reason it reads the materialized circle count: one stage of the fixture
// settled short of its request, and a reference that charged the planned count
// would disagree with the listing rather than check it.
func timingsFromRecords(stages []store.ScheduleStageRecord) []app.ScheduleStageTiming {
	timings := make([]app.ScheduleStageTiming, 0, len(stages))
	for _, stage := range stages {
		timing := app.ScheduleStageTiming{
			Index:        stage.Index,
			Kind:         stage.Kind,
			State:        scheduleOutcomeState(stage.State),
			Circles:      stage.MaterializedCircles(),
			BestCost:     stage.BestCost,
			CostMeasured: stage.State == store.ScheduleStateCompleted,
		}
		if stage.StartedAt != nil && stage.CompletedAt != nil {
			timing.Elapsed = stage.CompletedAt.Sub(*stage.StartedAt)
		}

		timings = append(timings, timing)
	}

	return timings
}

// hasRow reports whether a table row starts with the given columns. The tables
// are tabwriter output, so the padding between columns depends on how wide the
// widest cell in the campaign is; the columns themselves do not.
func hasRow(body string, columns ...string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < len(columns) {
			continue
		}

		match := true

		for i, column := range columns {
			if fields[i] != column {
				match = false
				break
			}
		}

		if match {
			return true
		}
	}

	return false
}

// lastLines trims a long table down to what a failure message can carry.
func lastLines(body string, count int) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) <= count {
		return body
	}

	return "...\n" + strings.Join(lines[len(lines)-count:], "\n")
}
