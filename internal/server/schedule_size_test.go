package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// Task 16.7: a campaign the schedule format allows must be readable from the
// terminal. The CLI refuses a response larger than app.MaxCLIResponseBytes —
// an unbounded decode of a body that grows with the stage count is worth
// refusing — so the endpoints the CLI reads have to stay under it at the
// largest campaign a document may expand to.
//
// The bodies are measured, not estimated: each test builds the wire value the
// handler encodes and asserts on len(json). Both are synthesized rather than
// run, so the assertion is the byte count and not a live campaign.

// sizeTestStages builds count realized stage records shaped like the 1016-stage
// campaign measured on 2026-08-18: every stage completed, every stage naming a
// job, and costs that need a full float64 to print.
func sizeTestStages(t *testing.T, count int) []store.ScheduleStageRecord {
	t.Helper()

	config := app.DefaultConfig()
	config.RefPath = "assets/reference.png"

	config.Seed = 42
	err := config.ApplyDefaults()
	if err != nil {
		t.Fatalf("apply config defaults: %v", err)
	}

	started := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)

	stages := make([]store.ScheduleStageRecord, 0, count)
	for index := range count {
		stageConfig := config
		stageConfig.Circles = 8 + index%(app.MaxCircles-8)
		completed := started.Add(time.Duration(index+1) * 97 * time.Second)
		kind := app.ScheduleStageExtend

		switch {
		case index == 0:
			kind = app.ScheduleStageBase
		case index%64 == 0:
			kind = app.ScheduleStagePolish
		}

		stages = append(stages, store.ScheduleStageRecord{
			SchemaVersion: store.ScheduleRecordSchemaVersion,
			ScheduleID:    testSizeScheduleID,
			Index:         index,
			Kind:          kind,
			Circles:       stageConfig.Circles,
			Config:        stageConfig,
			State:         store.ScheduleStateCompleted,
			JobID:         syntheticUUID(index),
			BestCost:      812.5 - float64(index)/3.0,
			Iterations:    200,
			Evaluations:   6000,
			StartedAt:     &started,
			CompletedAt:   &completed,
			UpdatedAt:     completed,
		})
		started = completed
	}

	return stages
}

const testSizeScheduleID = "d8755b85-e689-490a-acf3-d576922822f9"

func syntheticUUID(index int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
}

// TestScheduleDetailStaysUnderTheCLIResponseCap is the Task 16.7 acceptance
// check for `schedule status`: the listing of a campaign at MaxScheduleStages
// has to fit in a response the CLI will decode. The full stage records are
// measured alongside it, because a check that only asserts the new number
// would still pass if the projection quietly went back to carrying configs.
func TestScheduleDetailStaysUnderTheCLIResponseCap(t *testing.T) {
	stages := sizeTestStages(t, app.MaxScheduleStages)

	document, err := app.ParseSchedule([]byte(maxSizedCampaignDocument))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	record, err := store.NewScheduleRecord(testSizeScheduleID, *document)
	if err != nil {
		t.Fatalf("NewScheduleRecord() error = %v", err)
	}

	record.State = store.ScheduleStateRunning
	// The name is free text the response carries twice — once in the summary,
	// once inside the document — so the worst legal case is measured, not the
	// tidy one the fixture would otherwise have.
	record.Document.Name = strings.Repeat("a", app.MaxScheduleNameLen)
	if err := record.Document.Validate(); err != nil {
		t.Fatalf("a name at the limit is not a valid document: %v", err)
	}

	detail := scheduleDetail{
		scheduleSummary: summarizeSchedule(record),
		Document:        record.Document,
		Stages:          summarizeScheduleStages(stages),
	}

	body, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal schedule detail: %v", err)
	}

	full, err := json.Marshal(stages)
	if err != nil {
		t.Fatalf("marshal stage records: %v", err)
	}

	t.Logf("%d stages: listing %d bytes (%d per stage), full records %d bytes (%d per stage)",
		len(stages), len(body), len(body)/len(stages), len(full), len(full)/len(stages))

	if len(body) > app.MaxCLIResponseBytes {
		t.Fatalf("schedule detail is %d bytes for %d stages, over the %d the CLI decodes",
			len(body), len(stages), app.MaxCLIResponseBytes)
	}
	// The document travels in full, because the finish projection is computed
	// from the plan it expands to, so the listing only stays under the cap while
	// the document is bounded too. The measurement is redone with the document
	// at its own limit, which is the case a fixture cannot show: this fixture's
	// document is small, and a campaign is free to author a larger one.
	encodedDocument, err := json.Marshal(record.Document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}

	worstCase := len(body) - len(encodedDocument) + app.MaxScheduleDocumentBytes
	if worstCase > app.MaxCLIResponseBytes {
		t.Fatalf("with a document at its %d byte limit the response is %d bytes, over the %d the CLI decodes",
			app.MaxScheduleDocumentBytes, worstCase, app.MaxCLIResponseBytes)
	}

	// The control: the records this projects from do not fit, which is the
	// defect the task reports. Without it the assertion above could pass for a
	// campaign that was simply small.
	if len(full) <= app.MaxCLIResponseBytes {
		t.Fatalf("the full stage records are %d bytes, so the fixture is too small to show the problem", len(full))
	}
	// The one field of the configuration a reader still needs is the seed, and
	// it is carried once for the campaign rather than once per stage.
	if detail.CampaignSeed == 0 {
		t.Error("the listing dropped the campaign seed, which is what replays the campaign")
	}
}

// maxSizedCampaignDocument expands to exactly MaxScheduleStages: a base stage,
// one +1 extend per circle up to MaxCircles, and polish stages for the rest
// (split across steps only because a single step repeats at most 1024 times).
// It is the shape the limit was sized for — one stage per circle plus polishing
// between them — rather than an arbitrary large number.
const maxSizedCampaignDocument = `{
  "schemaVersion": 1,
  "name": "stage-limit campaign",
  "seed": 42,
  "base": {"refPath": "assets/reference.png", "mode": "batch", "circles": 8, "batchSize": 8, "iters": 200, "popSize": 30},
  "steps": [
    {"type": "extend", "repeat": 992, "additionalCircles": 1},
    {"type": "polish", "repeat": 1024},
    {"type": "polish", "repeat": 1024},
    {"type": "polish", "repeat": 1024},
    {"type": "polish", "repeat": 31}
  ]
}`

func TestMaxSizedCampaignDocumentExpandsToTheStageLimit(t *testing.T) {
	document, err := app.ParseSchedule([]byte(maxSizedCampaignDocument))
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
}

// TestChainDetailStaysUnderTheCLIResponseCap is the same check for
// `schedule import`. A chain is not bounded by MaxScheduleStages at all, so it
// is measured at the same stage count for comparability.
func TestChainDetailStaysUnderTheCLIResponseCap(t *testing.T) {
	chain := make([]*store.Checkpoint, 0, app.MaxScheduleStages)

	timestamp := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	for index := range app.MaxScheduleStages {
		checkpoint := &store.Checkpoint{
			SchemaVersion:    store.CheckpointSchemaVersion,
			JobID:            syntheticUUID(index),
			RequestedCircles: 1 + index%app.MaxCircles,
			ActualCircles:    1 + index%app.MaxCircles,
			BestCost:         812.5 - float64(index)/3.0,
			Iterations:       200,
			Evaluations:      6000,
			Termination:      "converged",
			Timestamp:        timestamp.Add(time.Duration(index) * time.Minute),
		}
		if index > 0 {
			checkpoint.ExtendedFrom = syntheticUUID(index - 1)
		}

		chain = append(chain, checkpoint)
	}

	body, err := json.Marshal(chainDetailFrom(chain[len(chain)-1].JobID, chain))
	if err != nil {
		t.Fatalf("marshal chain detail: %v", err)
	}

	t.Logf("%d stages: chain %d bytes (%d per stage)", len(chain), len(body), len(body)/len(chain))

	if len(body) > app.MaxCLIResponseBytes {
		t.Fatalf("chain detail is %d bytes for %d stages, over the %d the CLI decodes",
			len(body), len(chain), app.MaxCLIResponseBytes)
	}
}

// TestScheduleStageEndpointReturnsTheWholeRecord is the other half of the
// projection: the configuration a stage ran with stays retrievable, one stage
// at a time, because replaying a single stage is what it is recorded for.
func TestScheduleStageEndpointReturnsTheWholeRecord(t *testing.T) {
	fixture := newScheduleFixture(t, 1)

	scheduleStore, err := fixture.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	document, err := app.ParseSchedule([]byte(maxSizedCampaignDocument))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}

	record, err := store.NewScheduleRecord(testSizeScheduleID, *document)
	if err != nil {
		t.Fatalf("NewScheduleRecord() error = %v", err)
	}

	record.State = store.ScheduleStateCompleted
	if err := scheduleStore.SaveSchedule(record); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}

	stage := sizeTestStages(t, 2)[1]
	if err := scheduleStore.SaveScheduleStage(testSizeScheduleID, &stage); err != nil {
		t.Fatalf("SaveScheduleStage() error = %v", err)
	}

	handler := fixture.server.Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/schedules/%s/stages/1", testSizeScheduleID), nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("stage status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	var got store.ScheduleStageRecord
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode stage: %v", err)
	}

	if got.Index != stage.Index || got.JobID != stage.JobID {
		t.Fatalf("stage = (%d, %q), want (%d, %q)", got.Index, got.JobID, stage.Index, stage.JobID)
	}

	if got.Config.Circles != stage.Config.Circles || got.Config.EffectiveSeed != stage.Config.EffectiveSeed {
		t.Fatalf("stage config = %+v, want the recorded configuration", got.Config)
	}

	// The listing said which stages exist; asking for one that does not is a
	// missing stage rather than a missing campaign or an empty record.
	for _, test := range []struct {
		name string
		path string
		want int
	}{
		{name: "unrecorded stage", path: "/stages/7", want: http.StatusNotFound},
		{name: "not a number", path: "/stages/latest", want: http.StatusBadRequest},
		{name: "negative", path: "/stages/-1", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
				"/api/v1/schedules/"+testSizeScheduleID+test.path, nil))

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}
