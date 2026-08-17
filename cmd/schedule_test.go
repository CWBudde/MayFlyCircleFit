package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
				"schemaVersion": 1, "scheduleId": testScheduleID, "index": 0, "kind": "base",
				"stepIndex": -1, "repetition": 0, "circles": 8, "state": "completed",
				"jobId": "11111111-1111-4111-8111-111111111111", "bestCost": 812.5,
				"startedAt": started, "completedAt": completed, "updatedAt": completed,
				"config": map[string]any{"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "iters": 100, "popSize": 30, "seed": 42},
			},
			{
				"schemaVersion": 1, "scheduleId": testScheduleID, "index": 1, "kind": "polish",
				"stepIndex": 0, "repetition": 1, "circles": 8, "state": "skipped",
				"reason": "polishing stopped paying", "updatedAt": completed,
				"config": map[string]any{"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "iters": 100, "popSize": 30, "seed": 42},
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
					"circles": 8, "bestCost": 812.5, "iterations": 100, "evaluations": 3000,
					"termination": "completed", "timestamp": time.Now().UTC()},
				{"index": 1, "kind": "extend", "jobId": leaf, "parentJobId": "11111111-1111-4111-8111-111111111111",
					"circles": 16, "bestCost": 640.25, "iterations": 200, "evaluations": 6000,
					"termination": "completed", "timestamp": time.Now().UTC()},
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
// would name a seed that replays nothing; the resolved value is read back from
// the stage that ran, and before that the output says so.
func TestScheduleSeedIsNeverReportedAsZero(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	detail := scheduleDetailResponse{}
	detail.ScheduleID = testScheduleID
	detail.State = "running"
	detail.TotalStages = 2
	detail.CreatedAt, detail.UpdatedAt = started, started

	var unstarted bytes.Buffer
	printScheduleDetail(&unstarted, detail)
	if !strings.Contains(unstarted.String(), "Seed: unresolved") {
		t.Fatalf("unstarted campaign output = %q, want an unresolved seed", unstarted.String())
	}
	if strings.Contains(unstarted.String(), "Seed: 0") {
		t.Fatalf("the zero sentinel was printed as a seed:\n%s", unstarted.String())
	}

	detail.Stages = []store.ScheduleStageRecord{{
		ScheduleID: testScheduleID,
		Index:      0,
		Kind:       app.ScheduleStageBase,
		StepIndex:  -1,
		Circles:    8,
		State:      store.ScheduleStateCompleted,
		Config:     store.JobConfig{RefPath: "assets/ref.png", EffectiveSeed: 987654321},
	}}
	var running bytes.Buffer
	printScheduleDetail(&running, detail)
	if !strings.Contains(running.String(), "Seed: 987654321") {
		t.Fatalf("running campaign output = %q, want the seed the stage recorded", running.String())
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
