package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// The schedule command is a client, so its tests are contract tests: they stand
// a server up that answers exactly what internal/server answers and check that
// the command speaks to the right path and renders what came back.

const testScheduleID = "66666666-6666-4666-8666-666666666666"

// scheduleServerStub records the paths it was asked for and answers the
// schedule surface.
type scheduleServerStub struct {
	paths   chan string
	bodies  chan string
	handler http.HandlerFunc
}

func newScheduleStub(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *scheduleServerStub) {
	t.Helper()
	stub := &scheduleServerStub{
		paths:   make(chan string, 8),
		bodies:  make(chan string, 8),
		handler: handler,
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		stub.paths <- request.URL.Path
		body, _ := io.ReadAll(request.Body)
		stub.bodies <- string(body)
		writer.Header().Set("Content-Type", "application/json")
		stub.handler(writer, request)
	}))
	t.Cleanup(server.Close)
	previous := scheduleServerURL
	scheduleServerURL = server.URL
	t.Cleanup(func() { scheduleServerURL = previous })
	return server, stub
}

func scheduleDetailFixture() map[string]any {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	completed := started.Add(90 * time.Second)
	return map[string]any{
		"scheduleId":   testScheduleID,
		"name":         "synthesized campaign",
		"state":        "running",
		"campaignSeed": 42,
		"totalStages":  3,
		"createdAt":    started,
		"updatedAt":    completed,
		"document": map[string]any{
			"schemaVersion": 1,
			"seed":          42,
			"base":          map[string]any{"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "iters": 100, "popSize": 30, "seed": 42},
		},
		"stages": []map[string]any{
			{
				"index": 0, "kind": "base", "circles": 8, "state": "completed",
				"jobId": "11111111-1111-4111-8111-111111111111", "bestCost": 812.5,
				"elapsedNanos": completed.Sub(started).Nanoseconds(),
			},
			{
				"index": 1, "kind": "polish", "circles": 8, "state": "skipped",
				"reason": "polishing stopped paying",
			},
		},
	}
}

func TestScheduleStatusRendersTheStageTable(t *testing.T) {
	_, stub := newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(scheduleDetailFixture())
	})
	var output bytes.Buffer
	if err := runScheduleStatus(testCommand(context.Background(), &output), []string{testScheduleID}); err != nil {
		t.Fatalf("runScheduleStatus() error = %v", err)
	}
	if path := <-stub.paths; path != "/api/v1/schedules/"+testScheduleID {
		t.Fatalf("requested %q, want the schedule detail path", path)
	}
	body := output.String()
	for _, marker := range []string{
		"synthesized campaign",
		"Stages: 2 recorded of 3 planned",
		"812.500",
		"1m30s",
		"skipped",
		"polishing stopped paying",
		"Accepted polishing sweeps are not persisted",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("schedule status output missing %q:\n%s", marker, body)
		}
	}
}

func TestScheduleCreateValidatesBeforePosting(t *testing.T) {
	_, stub := newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"scheduleId": testScheduleID, "state": "running", "campaignSeed": 42,
			"totalStages": 3, "createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
		})
	})
	document := `{"seed": 42, "base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "iters": 100, "popSize": 30},
 "steps": [{"type": "extend", "additionalCircles": 8}]}`
	path := filepath.Join(t.TempDir(), "campaign.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
	var output bytes.Buffer
	if err := runScheduleCreate(testCommand(context.Background(), &output), []string{path}); err != nil {
		t.Fatalf("runScheduleCreate() error = %v", err)
	}
	if requested := <-stub.paths; requested != "/api/v1/schedules" {
		t.Fatalf("requested %q, want the schedule collection", requested)
	}
	// The document is posted as authored, not re-serialized from the parse.
	if posted := <-stub.bodies; posted != document {
		t.Fatalf("posted %q, want the document as authored", posted)
	}
	if !strings.Contains(output.String(), "Schedule "+testScheduleID+" created (running)") {
		t.Fatalf("create output = %q", output.String())
	}
}

// TestScheduleCreateRefusesAnInvalidDocumentLocally keeps a typo from costing a
// round trip, and makes the CLI report the field the server would have named.
func TestScheduleCreateRefusesAnInvalidDocumentLocally(t *testing.T) {
	_, stub := newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("an invalid document reached the server")
		writer.WriteHeader(http.StatusBadRequest)
	})
	path := filepath.Join(t.TempDir(), "campaign.json")
	document := `{"seed": 42, "base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "iters": 100, "popSize": 30},
 "steps": [{"type": "sharpen"}]}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
	err := runScheduleCreate(testCommand(context.Background(), &bytes.Buffer{}), []string{path})
	if err == nil {
		t.Fatal("runScheduleCreate() accepted an unknown step type")
	}
	if !strings.Contains(err.Error(), "steps[0].type") {
		t.Fatalf("error = %v, want it to name the offending field", err)
	}
	select {
	case path := <-stub.paths:
		t.Fatalf("the CLI still called %q", path)
	default:
	}
}

func TestScheduleActionsPostToTheirVerb(t *testing.T) {
	for _, action := range []string{"cancel", "pause", "resume"} {
		t.Run(action, func(t *testing.T) {
			_, stub := newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"scheduleId": testScheduleID, "state": "paused", "campaignSeed": 42,
					"totalStages": 3, "createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
				})
			})
			var output bytes.Buffer
			if err := runScheduleAction(context.Background(), &output, testScheduleID, action); err != nil {
				t.Fatalf("runScheduleAction(%s) error = %v", action, err)
			}
			want := "/api/v1/schedules/" + testScheduleID + "/" + action
			if path := <-stub.paths; path != want {
				t.Fatalf("requested %q, want %q", path, want)
			}
		})
	}
}

func TestScheduleImportRendersTheChain(t *testing.T) {
	const leaf = "44444444-4444-4444-8444-444444444444"
	_, stub := newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"leafJobId": leaf,
			"rootJobId": "11111111-1111-4111-8111-111111111111",
			"stages": []map[string]any{
				{"index": 0, "kind": "base", "jobId": "11111111-1111-4111-8111-111111111111",
					"circles": 8, "bestCost": 812.5, "iterations": 100},
				{"index": 1, "kind": "extend", "jobId": leaf,
					"circles": 16, "bestCost": 640.25, "iterations": 200},
			},
		})
	})
	var output bytes.Buffer
	if err := runScheduleImport(testCommand(context.Background(), &output), []string{leaf}); err != nil {
		t.Fatalf("runScheduleImport() error = %v", err)
	}
	if path := <-stub.paths; path != "/api/v1/chains/"+leaf {
		t.Fatalf("requested %q, want the chain path", path)
	}
	body := output.String()
	for _, marker := range []string{"Stages: 2", "base", "extend", "812.500", "640.250"} {
		if !strings.Contains(body, marker) {
			t.Errorf("import output missing %q:\n%s", marker, body)
		}
	}
}

// TestScheduleSeedIsNeverReportedAsZero covers a campaign whose document
// omitted the seed. Zero is the "resolve one for me" sentinel, so printing it
// would name a seed that replays nothing, and the output says so instead.
//
// The seed is read from the campaign, not from a stage: a persisted schedule
// has a resolved seed by construction, and every stage inherits that one seed,
// so a per-stage copy would be the same number on every row of the listing.
func TestScheduleSeedIsNeverReportedAsZero(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	detail := scheduleDetailResponse{}
	detail.ScheduleID = testScheduleID
	detail.State = "running"
	detail.TotalStages = 2
	detail.CreatedAt, detail.UpdatedAt = started, started

	var unstarted bytes.Buffer
	printScheduleDetail(&unstarted, detail, started)
	if !strings.Contains(unstarted.String(), "Seed: unresolved") {
		t.Fatalf("unstarted campaign output = %q, want an unresolved seed", unstarted.String())
	}
	if strings.Contains(unstarted.String(), "Seed: 0") {
		t.Fatalf("the zero sentinel was printed as a seed:\n%s", unstarted.String())
	}

	detail.CampaignSeed = 987654321
	detail.Stages = []scheduleStageSummaryResponse{{
		Index:   0,
		Kind:    app.ScheduleStageBase,
		Circles: 8,
		State:   store.ScheduleStateCompleted,
	}}
	var running bytes.Buffer
	printScheduleDetail(&running, detail, started)
	if !strings.Contains(running.String(), "Seed: 987654321") {
		t.Fatalf("running campaign output = %q, want the campaign seed", running.String())
	}
}

func TestScheduleListReportsAnEmptyServer(t *testing.T) {
	_, _ = newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("[]"))
	})
	var output bytes.Buffer
	if err := runScheduleList(testCommand(context.Background(), &output), nil); err != nil {
		t.Fatalf("runScheduleList() error = %v", err)
	}
	if !strings.Contains(output.String(), "No schedules found") {
		t.Fatalf("list output = %q", output.String())
	}
}

// referenceCampaignDocument reads the 512-circle campaign of Task 16.4's
// acceptance check -- base 8 circles, +8 extends to 512, and a polish at
// 32/64/96/128/192/256 that is abandoned after two consecutive barren sweeps.
//
// It is read from docs/ rather than inlined here on purpose. The documented
// worked example and the document this command is asserted against are the same
// bytes, so the documentation cannot describe a format the command would refuse.
// internal/app.TestDocumentedExamplePlansTheReferenceCampaign parses the same
// file and pins the stage and iteration figures the documentation quotes.
func referenceCampaignDocument(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "docs", "examples", "512-circle-campaign.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the documented example: %v", err)
	}
	return string(data)
}

// writeScheduleDocument drops a document in a temporary directory and hands
// back its path.
func writeScheduleDocument(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "campaign.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
	return path
}

// dryRun runs `schedule create --dry-run` and restores the flag afterwards.
func dryRun(t *testing.T, path string) string {
	t.Helper()
	previous := scheduleDryRun
	scheduleDryRun = true
	t.Cleanup(func() { scheduleDryRun = previous })
	var output bytes.Buffer
	if err := runScheduleCreate(testCommand(context.Background(), &output), []string{path}); err != nil {
		t.Fatalf("runScheduleCreate(--dry-run) error = %v", err)
	}
	return output.String()
}

// TestScheduleDryRunListsTheReferenceCampaign is the Task 16.4 acceptance check
// at the command level. The iteration total is hand-computed in
// internal/app.TestReferenceCampaignPlanMatchesTheHandComputation, which writes
// the arithmetic out; this test checks the command prints that same figure over
// the whole stage list.
func TestScheduleDryRunListsTheReferenceCampaign(t *testing.T) {
	_, stub := newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("a dry run reached the server")
		writer.WriteHeader(http.StatusInternalServerError)
	})
	body := dryRun(t, writeScheduleDocument(t, referenceCampaignDocument(t)))

	for _, marker := range []string{
		"nothing was submitted and no schedule was created",
		"Stages: 70 (1 base, 63 extend, 6 polish; 6 conditional)",
		"Seed: 4242",
		"Planned optimizer iterations (nominal): 32000",
		"unconditional: 12800",
		"conditional:   19200 across 6 stages",
		// The first and last stages of the climb, and a polish in between.
		"0   base    8        200",
		"69  extend  512      200",
		// A conditional stage is shown as conditional, with its condition.
		"conditional: only at 32/64/96/128/192/256 circles; abandoned after 2 consecutive stages gaining less than 1",
		// Per-stage parameters.
		"+8 circles, batch 8, 1 × 200 iters, pop 30",
		// A polish stage reports the polishing population, which is its own
		// default rather than the base stage's popSize.
		fmt.Sprintf("active set 5, %d sweeps × %d × %d iters, pop %d",
			app.DefaultPolishingMaxSweeps, app.DefaultPolishingEpochs, app.DefaultPolishingIters, app.DefaultPolishingPopSize),
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("dry run output missing %q:\n%s", marker, body)
		}
	}
	// Every planned stage is listed, conditional ones included.
	if lines := strings.Count(body, "\n"); lines < 70 {
		t.Errorf("dry run printed %d lines, too few for 70 stages:\n%s", lines, body)
	}
	select {
	case path := <-stub.paths:
		t.Fatalf("the dry run called %q", path)
	default:
	}
}

// TestScheduleDryRunTouchesNoStore is the other half of the task: a dry run may
// not create a schedule directory, a stage file, or a job. The positive control
// writes one schedule through the store afterwards, so the comparison is known
// to be able to see a write.
func TestScheduleDryRunTouchesNoStore(t *testing.T) {
	_, _ = newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("a dry run reached the server")
		writer.WriteHeader(http.StatusInternalServerError)
	})
	root := t.TempDir()
	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	before := treeSnapshot(t, root)

	dryRun(t, writeScheduleDocument(t, referenceCampaignDocument(t)))

	after := treeSnapshot(t, root)
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the dry run changed the data root:\nbefore %v\nafter  %v", before, after)
	}
	for _, entry := range after {
		if strings.Contains(entry, "schedules") {
			t.Fatalf("the dry run left %q under the data root", entry)
		}
	}

	document, err := app.ParseSchedule([]byte(referenceCampaignDocument(t)))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}
	record, err := store.NewScheduleRecord(testScheduleID, *document)
	if err != nil {
		t.Fatalf("NewScheduleRecord() error = %v", err)
	}
	if err := persistence.SaveSchedule(record); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}
	control := treeSnapshot(t, root)
	if strings.Join(control, "\n") == strings.Join(after, "\n") {
		t.Fatal("saving a schedule left the data root unchanged, so the comparison proves nothing")
	}
}

// treeSnapshot lists every path below root, relative and sorted.
func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, relative)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(paths)
	return paths
}

// projectionDetailFixture is a four-stage campaign — base plus three extends —
// with the first stages already completed, which is what a projection reads.
func projectionDetailFixture(completedExtends int, extendElapsed []time.Duration) map[string]any {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	config := map[string]any{"refPath": "assets/ref.png", "mode": "batch", "circles": 8,
		"batchSize": 8, "iters": 200, "popSize": 30, "seed": 42}
	stages := []map[string]any{{
		"index": 0, "kind": "base", "circles": 8, "state": "completed",
		"jobId": "11111111-1111-4111-8111-111111111111", "bestCost": 812.5,
		"elapsedNanos": time.Minute.Nanoseconds(),
	}}
	at := started.Add(time.Minute)
	for index := 1; index <= completedExtends; index++ {
		elapsed := extendElapsed[index-1]
		stages = append(stages, map[string]any{
			"index": index, "kind": "extend", "circles": 8 + 8*index,
			"state": "completed", "jobId": "22222222-2222-4222-8222-222222222222",
			"bestCost": 700.0, "elapsedNanos": elapsed.Nanoseconds(),
		})
		at = at.Add(elapsed)
	}
	return map[string]any{
		"scheduleId": testScheduleID, "name": "projected campaign", "state": "running",
		"campaignSeed": 42, "totalStages": 4, "createdAt": started, "updatedAt": at,
		"document": map[string]any{
			"schemaVersion": 1, "seed": 42, "base": config,
			"steps": []map[string]any{{"type": "extend", "repeat": 3, "additionalCircles": 8}},
		},
		"stages": stages,
	}
}

// TestScheduleStatusProjectsFromMeasuredStages checks the projection is derived
// from the recorded wall clock and from nothing else: two extends at 2 and 4
// minutes make the one remaining extend 3 minutes.
func TestScheduleStatusProjectsFromMeasuredStages(t *testing.T) {
	fixture := projectionDetailFixture(2, []time.Duration{2 * time.Minute, 4 * time.Minute})
	var detail scheduleDetailResponse
	decodeFixture(t, fixture, &detail)

	var output bytes.Buffer
	printScheduleDetail(&output, detail, time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC))
	body := output.String()
	for _, marker := range []string{
		"Projection (from measured stage wall clock only)",
		"Remaining: 3m0s, finishing around 2026-08-01T13:03:00Z",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("status output missing %q:\n%s", marker, body)
		}
	}
	if !strings.Contains(body, "extend") || !strings.Contains(body, "3m0s") {
		t.Errorf("status output missing the extend rate:\n%s", body)
	}
}

// TestScheduleStatusRefusesToProjectFromOneStage is the honesty requirement: a
// single sample is reported as insufficient rather than extrapolated.
func TestScheduleStatusRefusesToProjectFromOneStage(t *testing.T) {
	fixture := projectionDetailFixture(1, []time.Duration{2 * time.Minute})
	var detail scheduleDetailResponse
	decodeFixture(t, fixture, &detail)

	var output bytes.Buffer
	printScheduleDetail(&output, detail, time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC))
	body := output.String()
	for _, marker := range []string{
		"insufficient data: 1 completed extend stage(s), 2 needed",
		"No finish time: not every remaining stage kind has been measured yet.",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("status output missing %q:\n%s", marker, body)
		}
	}
	if strings.Contains(body, "finishing around") {
		t.Errorf("a single sample still produced a finish time:\n%s", body)
	}
}

// TestScheduleStatusGivesNoFinishTimeToATerminalCampaign pins the review
// finding: the projection anchors at the current clock, so a campaign the
// server will never advance must not be handed a future finish time.
func TestScheduleStatusGivesNoFinishTimeToATerminalCampaign(t *testing.T) {
	for _, state := range []string{"failed", "cancelled", "completed"} {
		t.Run(state, func(t *testing.T) {
			fixture := projectionDetailFixture(2, []time.Duration{2 * time.Minute, 4 * time.Minute})
			fixture["state"] = state
			var detail scheduleDetailResponse
			decodeFixture(t, fixture, &detail)

			var output bytes.Buffer
			printScheduleDetail(&output, detail, time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC))
			body := output.String()
			if !strings.Contains(body, "No projection: the campaign is "+state+" and will not advance.") {
				t.Errorf("status output does not report the terminal campaign:\n%s", body)
			}
			if strings.Contains(body, "finishing around") {
				t.Errorf("a %s campaign was still given a finish time:\n%s", state, body)
			}
		})
	}
}

// TestScheduleStatusProjectsAPausedCampaignWithoutATimestamp keeps the rates
// visible for a paused campaign while refusing the one thing that would be a
// guess: when it starts again.
func TestScheduleStatusProjectsAPausedCampaignWithoutATimestamp(t *testing.T) {
	fixture := projectionDetailFixture(2, []time.Duration{2 * time.Minute, 4 * time.Minute})
	fixture["state"] = "paused"
	var detail scheduleDetailResponse
	decodeFixture(t, fixture, &detail)

	var output bytes.Buffer
	printScheduleDetail(&output, detail, time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC))
	body := output.String()
	if !strings.Contains(body, "Remaining once the campaign runs again: 3m0s (no finish time while it is paused)") {
		t.Errorf("status output missing the paused remaining workload:\n%s", body)
	}
	if strings.Contains(body, "finishing around") {
		t.Errorf("a paused campaign was still given a finish time:\n%s", body)
	}
}

// TestScheduleDryRunReportsAnOmittedSeedAsAutomatic pins the other review
// finding: the document carries zero and every expansion draws a different
// throwaway seed, so neither is a truthful thing to print.
func TestScheduleDryRunReportsAnOmittedSeedAsAutomatic(t *testing.T) {
	_, _ = newScheduleStub(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("a dry run reached the server")
		writer.WriteHeader(http.StatusInternalServerError)
	})
	document := strings.Replace(referenceCampaignDocument(t), `"seed": 4242,`, "", 1)
	body := dryRun(t, writeScheduleDocument(t, document))

	if !strings.Contains(body, "Seed: automatic — resolved at submission") {
		t.Errorf("dry run did not report the omitted seed as automatic:\n%s", firstLines(body, 6))
	}
	if strings.Contains(body, "Seed: 0") {
		t.Errorf("dry run presented the omitted seed as seed zero:\n%s", firstLines(body, 6))
	}
}

// firstLines keeps a failure message readable for a 70-stage plan.
func firstLines(body string, count int) string {
	lines := strings.SplitN(body, "\n", count+1)
	if len(lines) > count {
		lines = lines[:count]
	}
	return strings.Join(lines, "\n")
}

// decodeFixture round-trips a fixture through JSON so the test reads exactly
// what the CLI would decode from the server.
func decodeFixture(t *testing.T, fixture map[string]any, target any) {
	t.Helper()
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
}
