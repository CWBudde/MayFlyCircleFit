package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

func lineageConfig() JobConfig {
	return JobConfig{
		RefPath: "assets/ref.png",
		Mode:    app.ModeBatch,
		Circles: 2,
		Iters:   100,
		PopSize: 30,
		Seed:    7,
	}
}

func lineageCheckpoint(t *testing.T, jobID string) *Checkpoint {
	t.Helper()
	checkpoint := NewCheckpoint(jobID, make([]float64, 14), 1.5, 4.0, 10, lineageConfig())
	checkpoint.Timestamp = time.Now()
	return checkpoint
}

// TestCheckpointLineageRoundTrip is the point of the field: the chain must be
// reconstructible from the job tree alone, with no external ledger.
func TestCheckpointLineageRoundTrip(t *testing.T) {
	const (
		jobID    = "11111111-1111-4111-8111-111111111111"
		parentID = "22222222-2222-4222-8222-222222222222"
		schedule = "33333333-3333-4333-8333-333333333333"
	)
	stage := 4
	checkpoint := lineageCheckpoint(t, jobID)
	checkpoint.ExtendedFrom = parentID
	checkpoint.ScheduleID = schedule
	checkpoint.StageIndex = &stage

	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"extendedFrom"`) {
		t.Fatalf("marshalled checkpoint does not carry the lineage: %s", data)
	}

	var reloaded Checkpoint
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if reloaded.ExtendedFrom != parentID {
		t.Fatalf("ExtendedFrom = %q, want %q", reloaded.ExtendedFrom, parentID)
	}
	if reloaded.PolishedFrom != "" {
		t.Fatalf("PolishedFrom = %q, want empty", reloaded.PolishedFrom)
	}
	if reloaded.ScheduleID != schedule {
		t.Fatalf("ScheduleID = %q, want %q", reloaded.ScheduleID, schedule)
	}
	if reloaded.StageIndex == nil || *reloaded.StageIndex != stage {
		t.Fatalf("StageIndex = %v, want %d", reloaded.StageIndex, stage)
	}
	if err := reloaded.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	info := reloaded.ToInfo()
	if info.ExtendedFrom != parentID || info.ScheduleID != schedule {
		t.Fatalf("ToInfo() lost the lineage: %+v", info)
	}
	if parent, ok := reloaded.ContinuedFrom(); !ok || parent != parentID {
		t.Fatalf("ContinuedFrom() = (%q, %v), want (%q, true)", parent, ok, parentID)
	}
}

// TestCheckpointWithoutLineageStillLoads guards the migration promise: a
// checkpoint written before the field existed must load unchanged.
func TestCheckpointWithoutLineageStillLoads(t *testing.T) {
	for _, version := range []string{`"schemaVersion": 1,`, `"schemaVersion": 2,`, ``} {
		payload := `{
			` + version + `
			"jobId": "11111111-1111-4111-8111-111111111111",
			"bestParams": [0,0,0,0,0,0,0],
			"bestCost": 1.0,
			"initialCost": 2.0,
			"requestedCircles": 1,
			"actualCircles": 1,
			"iterations": 5,
			"timestamp": "2026-01-01T00:00:00Z",
			"config": {"refPath": "assets/ref.png", "mode": "batch", "circles": 1, "iters": 10, "popSize": 30, "seed": 3}
		}`
		var checkpoint Checkpoint
		if err := json.Unmarshal([]byte(payload), &checkpoint); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", version, err)
		}
		if checkpoint.ExtendedFrom != "" || checkpoint.PolishedFrom != "" || checkpoint.StageIndex != nil {
			t.Fatalf("legacy checkpoint invented a lineage: %+v", checkpoint)
		}
		if _, ok := checkpoint.ContinuedFrom(); ok {
			t.Fatal("ContinuedFrom() reported a parent for a legacy checkpoint")
		}
		if err := checkpoint.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	}
}

func TestCheckpointLineageValidation(t *testing.T) {
	const (
		jobID  = "11111111-1111-4111-8111-111111111111"
		parent = "22222222-2222-4222-8222-222222222222"
	)
	stage := 0
	negative := -1
	tests := []struct {
		name    string
		mutate  func(*Checkpoint)
		wantErr string
	}{
		{
			name:    "both parents",
			mutate:  func(c *Checkpoint) { c.ExtendedFrom = parent; c.PolishedFrom = parent },
			wantErr: "PolishedFrom",
		},
		{
			name:    "parent is not a job id",
			mutate:  func(c *Checkpoint) { c.PolishedFrom = "not-a-uuid" },
			wantErr: "PolishedFrom",
		},
		{
			name:    "self parent",
			mutate:  func(c *Checkpoint) { c.ExtendedFrom = jobID },
			wantErr: "ExtendedFrom",
		},
		{
			name:    "schedule id is not a uuid",
			mutate:  func(c *Checkpoint) { c.ScheduleID = "campaign" },
			wantErr: "ScheduleID",
		},
		{
			name:    "stage index without a schedule",
			mutate:  func(c *Checkpoint) { c.StageIndex = &stage },
			wantErr: "StageIndex",
		},
		{
			name: "negative stage index",
			mutate: func(c *Checkpoint) {
				c.ScheduleID = "33333333-3333-4333-8333-333333333333"
				c.StageIndex = &negative
			},
			wantErr: "StageIndex",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkpoint := lineageCheckpoint(t, jobID)
			test.mutate(checkpoint)
			err := checkpoint.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want one naming %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want it to name %q", err, test.wantErr)
			}
		})
	}
}

func TestSaveAndLoadCheckpointPreservesLineage(t *testing.T) {
	const (
		jobID  = "11111111-1111-4111-8111-111111111111"
		parent = "22222222-2222-4222-8222-222222222222"
	)
	fsStore, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	checkpoint := lineageCheckpoint(t, jobID)
	checkpoint.PolishedFrom = parent
	if err := fsStore.SaveCheckpoint(jobID, checkpoint); err != nil {
		t.Fatalf("SaveCheckpoint() error = %v", err)
	}
	loaded, err := fsStore.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if loaded.PolishedFrom != parent {
		t.Fatalf("PolishedFrom = %q, want %q", loaded.PolishedFrom, parent)
	}
	infos, err := fsStore.ListCheckpoints()
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	if len(infos) != 1 || infos[0].PolishedFrom != parent {
		t.Fatalf("ListCheckpoints() = %+v, want the lineage preserved", infos)
	}
}
