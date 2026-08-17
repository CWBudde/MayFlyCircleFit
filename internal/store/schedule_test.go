package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

const (
	testScheduleID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testStageJobID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testBaseJobID  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func testScheduleDocument(t *testing.T) app.ScheduleDocument {
	t.Helper()
	doc, err := app.ParseSchedule([]byte(`{
		"schemaVersion": 1,
		"name": "512 circle campaign",
		"seed": 4242,
		"base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "batchSize": 8, "iters": 200, "popSize": 30},
		"steps": [
			{"type": "extend", "repeat": 3, "additionalCircles": 8},
			{"type": "polish", "maxSweeps": 4}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}
	return *doc
}

func testScheduleRecord(t *testing.T) *ScheduleRecord {
	t.Helper()
	doc := testScheduleDocument(t)
	record, err := NewScheduleRecord(testScheduleID, doc)
	if err != nil {
		t.Fatalf("NewScheduleRecord() error = %v", err)
	}
	return record
}

func newScheduleStore(t *testing.T) (*FSStore, string) {
	t.Helper()
	dir := t.TempDir()
	fsStore, err := NewFSStore(dir)
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	return fsStore, dir
}

// TestScheduleSurvivesAStoreRestart is the acceptance check for the phase: the
// campaign and its realized lineage must come back from disk without an
// external ledger.
func TestScheduleSurvivesAStoreRestart(t *testing.T) {
	fsStore, dir := newScheduleStore(t)
	record := testScheduleRecord(t)
	if err := fsStore.SaveSchedule(record); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}

	stages, err := record.Document.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	started := time.Now().UTC().Truncate(time.Second)
	for i, planned := range stages[:2] {
		stage := NewScheduleStageRecord(testScheduleID, planned)
		stage.State = ScheduleStateCompleted
		stage.StartedAt = &started
		stage.BestCost = 200 - float64(i)
		if i == 0 {
			stage.JobID = testBaseJobID
		} else {
			stage.JobID = testStageJobID
			stage.ParentJobID = testBaseJobID
		}
		if err := fsStore.SaveScheduleStage(testScheduleID, stage); err != nil {
			t.Fatalf("SaveScheduleStage(%d) error = %v", i, err)
		}
	}

	// A fresh store over the same directory is what a server restart sees.
	restarted, err := NewFSStore(dir)
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	reloaded, err := restarted.LoadSchedule(testScheduleID)
	if err != nil {
		t.Fatalf("LoadSchedule() error = %v", err)
	}
	if reloaded.Document.Name != "512 circle campaign" || reloaded.Document.Seed != 4242 {
		t.Fatalf("LoadSchedule() = %+v, want the authored document back", reloaded.Document)
	}
	if len(reloaded.Document.Steps) != 2 {
		t.Fatalf("reloaded document has %d steps, want 2", len(reloaded.Document.Steps))
	}
	if reloaded.CampaignSeed != 4242 {
		t.Fatalf("CampaignSeed = %d, want 4242", reloaded.CampaignSeed)
	}

	reloadedStages, err := restarted.LoadScheduleStages(testScheduleID)
	if err != nil {
		t.Fatalf("LoadScheduleStages() error = %v", err)
	}
	if len(reloadedStages) != 2 {
		t.Fatalf("LoadScheduleStages() returned %d stages, want 2", len(reloadedStages))
	}
	if reloadedStages[0].Index != 0 || reloadedStages[1].Index != 1 {
		t.Fatalf("stages came back out of order: %+v", reloadedStages)
	}
	if reloadedStages[0].JobID != testBaseJobID || reloadedStages[1].ParentJobID != testBaseJobID {
		t.Fatalf("stage lineage lost: %+v", reloadedStages)
	}
	if reloadedStages[1].Config.Circles != 16 {
		t.Fatalf("stage 1 Config.Circles = %d, want 16", reloadedStages[1].Config.Circles)
	}
	if reloadedStages[1].State != ScheduleStateCompleted {
		t.Fatalf("stage 1 State = %q, want completed", reloadedStages[1].State)
	}
}

func TestSaveScheduleStageIsIdempotentPerIndex(t *testing.T) {
	fsStore, _ := newScheduleStore(t)
	record := testScheduleRecord(t)
	if err := fsStore.SaveSchedule(record); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}
	stages, err := record.Document.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	stage := NewScheduleStageRecord(testScheduleID, stages[0])
	stage.JobID = testBaseJobID
	stage.State = ScheduleStateRunning
	if err := fsStore.SaveScheduleStage(testScheduleID, stage); err != nil {
		t.Fatalf("SaveScheduleStage() error = %v", err)
	}
	stage.State = ScheduleStateCompleted
	stage.BestCost = 161.99
	if err := fsStore.SaveScheduleStage(testScheduleID, stage); err != nil {
		t.Fatalf("SaveScheduleStage() rewrite error = %v", err)
	}
	reloaded, err := fsStore.LoadScheduleStages(testScheduleID)
	if err != nil {
		t.Fatalf("LoadScheduleStages() error = %v", err)
	}
	if len(reloaded) != 1 {
		t.Fatalf("rewriting a stage produced %d records, want 1", len(reloaded))
	}
	if reloaded[0].State != ScheduleStateCompleted || reloaded[0].BestCost != 161.99 {
		t.Fatalf("stage rewrite lost the update: %+v", reloaded[0])
	}
}

func TestScheduleStoreNotFoundAndListing(t *testing.T) {
	fsStore, _ := newScheduleStore(t)
	if _, err := fsStore.LoadSchedule(testScheduleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadSchedule() error = %v, want ErrNotFound", err)
	}
	if _, err := fsStore.LoadScheduleStages(testScheduleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadScheduleStages() error = %v, want ErrNotFound", err)
	}
	if err := fsStore.DeleteSchedule(testScheduleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteSchedule() error = %v, want ErrNotFound", err)
	}
	schedules, err := fsStore.ListSchedules()
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	if len(schedules) != 0 {
		t.Fatalf("ListSchedules() = %+v, want empty", schedules)
	}

	record := testScheduleRecord(t)
	if err := fsStore.SaveSchedule(record); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}
	schedules, err = fsStore.ListSchedules()
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	if len(schedules) != 1 || schedules[0].ScheduleID != testScheduleID {
		t.Fatalf("ListSchedules() = %+v, want the saved schedule", schedules)
	}
	if err := fsStore.DeleteSchedule(testScheduleID); err != nil {
		t.Fatalf("DeleteSchedule() error = %v", err)
	}
	if _, err := fsStore.LoadSchedule(testScheduleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadSchedule() after delete error = %v, want ErrNotFound", err)
	}
}

func TestScheduleStoreRejectsInvalidInput(t *testing.T) {
	fsStore, _ := newScheduleStore(t)
	valid := testScheduleRecord(t)
	stages, err := valid.Document.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	tests := []struct {
		name    string
		run     func() error
		wantErr string
	}{
		{
			name:    "nil schedule",
			run:     func() error { return fsStore.SaveSchedule(nil) },
			wantErr: "nil",
		},
		{
			name: "schedule id is not a uuid",
			run: func() error {
				record := testScheduleRecord(t)
				record.ScheduleID = "campaign"
				return fsStore.SaveSchedule(record)
			},
			wantErr: "scheduleID",
		},
		{
			name: "traversal schedule id",
			run: func() error {
				_, err := fsStore.LoadSchedule("../../etc")
				return err
			},
			wantErr: "scheduleID",
		},
		{
			name: "stage for an unknown schedule",
			run: func() error {
				return fsStore.SaveScheduleStage(testScheduleID, NewScheduleStageRecord(testScheduleID, stages[0]))
			},
			wantErr: "not found",
		},
		{
			name: "stage id disagrees with the schedule",
			run: func() error {
				if err := fsStore.SaveSchedule(valid); err != nil {
					return err
				}
				stage := NewScheduleStageRecord("dddddddd-dddd-4ddd-8ddd-dddddddddddd", stages[0])
				return fsStore.SaveScheduleStage(testScheduleID, stage)
			},
			wantErr: "does not match",
		},
		{
			name: "negative stage index",
			run: func() error {
				if err := fsStore.SaveSchedule(valid); err != nil {
					return err
				}
				stage := NewScheduleStageRecord(testScheduleID, stages[0])
				stage.Index = -1
				return fsStore.SaveScheduleStage(testScheduleID, stage)
			},
			wantErr: "Index",
		},
		{
			name: "stage job id is not a uuid",
			run: func() error {
				if err := fsStore.SaveSchedule(valid); err != nil {
					return err
				}
				stage := NewScheduleStageRecord(testScheduleID, stages[0])
				stage.JobID = "job-1"
				return fsStore.SaveScheduleStage(testScheduleID, stage)
			},
			wantErr: "JobID",
		},
		{
			name: "embedded document from a newer build",
			run: func() error {
				record := testScheduleRecord(t)
				record.Document.SchemaVersion = app.ScheduleSchemaVersion + 1
				return fsStore.SaveSchedule(record)
			},
			wantErr: "schemaVersion",
		},
		{
			name: "embedded document with an unknown step type",
			run: func() error {
				record := testScheduleRecord(t)
				record.Document.Steps = []app.ScheduleStep{{Type: "shrink"}}
				return fsStore.SaveSchedule(record)
			},
			wantErr: "type",
		},
		{
			name: "campaign seed disagrees with the document",
			run: func() error {
				record := testScheduleRecord(t)
				record.CampaignSeed = 7
				return fsStore.SaveSchedule(record)
			},
			wantErr: "CampaignSeed",
		},
		{
			name: "stage config describes another stage",
			run: func() error {
				if err := fsStore.SaveSchedule(valid); err != nil {
					return err
				}
				stage := NewScheduleStageRecord(testScheduleID, stages[1])
				stage.Circles = stage.Config.Circles + 8
				return fsStore.SaveScheduleStage(testScheduleID, stage)
			},
			wantErr: "Config.Circles",
		},
		{
			name: "unknown stage state",
			run: func() error {
				if err := fsStore.SaveSchedule(valid); err != nil {
					return err
				}
				stage := NewScheduleStageRecord(testScheduleID, stages[0])
				stage.State = "halfway"
				return fsStore.SaveScheduleStage(testScheduleID, stage)
			},
			wantErr: "State",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil {
				t.Fatalf("expected an error mentioning %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

// TestScheduleRecordResolvesAnOmittedSeed keeps a campaign replayable: the
// record must carry a concrete seed even when the document omitted one, or the
// stages replanned after a restart would use a fresh random campaign seed.
func TestScheduleRecordResolvesAnOmittedSeed(t *testing.T) {
	doc, err := app.ParseSchedule([]byte(`{
		"base": {"refPath": "assets/ref.png", "mode": "batch", "circles": 8, "batchSize": 8, "iters": 200, "popSize": 30},
		"steps": [{"type": "extend", "additionalCircles": 8}]
	}`))
	if err != nil {
		t.Fatalf("ParseSchedule() error = %v", err)
	}
	record, err := NewScheduleRecord(testScheduleID, *doc)
	if err != nil {
		t.Fatalf("NewScheduleRecord() error = %v", err)
	}
	if record.CampaignSeed == 0 || record.Document.Seed != record.CampaignSeed {
		t.Fatalf("record seed = %d, document seed = %d, want one resolved seed", record.CampaignSeed, record.Document.Seed)
	}

	fsStore, dir := newScheduleStore(t)
	if err := fsStore.SaveSchedule(record); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}
	restarted, err := NewFSStore(dir)
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	reloaded, err := restarted.LoadSchedule(testScheduleID)
	if err != nil {
		t.Fatalf("LoadSchedule() error = %v", err)
	}
	stages, err := reloaded.Document.Expand()
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if stages[0].Config.EffectiveSeed != record.CampaignSeed {
		t.Fatalf("reloaded expansion used seed %d, want %d", stages[0].Config.EffectiveSeed, record.CampaignSeed)
	}
}

// TestScheduleStoreRefusesASymlinkedRecord keeps the schedule reader inside the
// store, matching the guard the checkpoint artifacts already have.
func TestScheduleStoreRefusesASymlinkedRecord(t *testing.T) {
	fsStore, dir := newScheduleStore(t)
	record := testScheduleRecord(t)
	if err := fsStore.SaveSchedule(record); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(outside, []byte(`{"scheduleId": "`+testScheduleID+`"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	path := filepath.Join(dir, schedulesDirName, testScheduleID, "schedule.json")
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks are unavailable here: %v", err)
	}
	if _, err := fsStore.LoadSchedule(testScheduleID); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("LoadSchedule() error = %v, want a refusal naming the symlink", err)
	}
}

// TestScheduleStoreSatisfiesTheInterface pins the contract 16.2's executor will
// program against.
func TestScheduleStoreSatisfiesTheInterface(t *testing.T) {
	fsStore, _ := newScheduleStore(t)
	var _ ScheduleStore = fsStore
}
