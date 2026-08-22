package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// TestContinuationJobsRecordTheirParent covers the defect this task fixes: the
// parent used to exist only in the HTTP response, so a chain of extends left no
// trace anywhere on disk.
func TestContinuationJobsRecordTheirParent(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		parentOf   func(*Job) (string, string)
	}{
		{
			name:       "extend",
			path:       "/extend",
			body:       `{"additionalCircles":2}`,
			wantStatus: http.StatusCreated,
			parentOf:   func(job *Job) (string, string) { return "extendedFrom", job.ExtendedFrom },
		},
		{
			name:       "polish",
			path:       "/polish",
			body:       `{"activeSetSize":1}`,
			wantStatus: http.StatusCreated,
			parentOf:   func(job *Job) (string, string) { return "polishedFrom", job.PolishedFrom },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, sourceID, _ := newExtendableBatchJob(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+sourceID+test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")

			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, req)

			if response.Code != test.wantStatus {
				t.Fatalf("%s status = %d body=%s", test.name, response.Code, response.Body.String())
			}

			var payload struct {
				JobID string `json:"jobId"`
			}
			err := json.NewDecoder(response.Body).Decode(&payload)
			if err != nil {
				t.Fatal(err)
			}

			continuation, ok := server.jobManager.GetJob(payload.JobID)
			if !ok {
				t.Fatal("continuation job not found")
			}

			field, parent := test.parentOf(continuation)
			if parent != sourceID {
				t.Fatalf("continuation %s = %q, want %q", field, parent, sourceID)
			}

			if continuation.ExtendedFrom != "" && continuation.PolishedFrom != "" {
				t.Fatalf("continuation claims two parents: %+v", continuation)
			}
		})
	}
}

// TestJobLineageSurvivesTheCheckpoint asserts the round trip through the store:
// a lineage written on a checkpoint comes back on the restored job.
func TestJobLineageSurvivesTheCheckpoint(t *testing.T) {
	const (
		jobID    = "11111111-1111-4111-8111-111111111111"
		parentID = "22222222-2222-4222-8222-222222222222"
		schedule = "33333333-3333-4333-8333-333333333333"
	)

	config, err := app.Normalize(JobConfig{
		RefPath: "assets/ref.png", Mode: app.ModeBatch, Circles: 1, BatchSize: 1,
		Iters: 10, PopSize: 20, Threads: 1, Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}

	baseStage := 0
	job := &Job{
		ID: jobID, Project: app.DefaultProject, State: StateCompleted, Config: config,
		ExtendedFrom: parentID, ScheduleID: schedule, StageIndex: &baseStage,
	}
	checkpoint := store.NewCheckpoint(jobID, []float64{1, 1, 1, 1, 0, 0, 1}, 5, 10, 3, config)
	checkpoint.Timestamp = time.Now()
	applyJobLineage(checkpoint, job)

	if checkpoint.ExtendedFrom != parentID || checkpoint.ScheduleID != schedule {
		t.Fatalf("checkpoint lineage = %+v", checkpoint)
	}

	if checkpoint.StageIndex == nil || *checkpoint.StageIndex != 0 {
		t.Fatalf("checkpoint StageIndex = %v, want 0", checkpoint.StageIndex)
	}

	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	restored := jobFromCheckpoint(checkpoint, app.DefaultProject)
	if restored.ExtendedFrom != parentID || restored.ScheduleID != schedule {
		t.Fatalf("restored job lineage = %+v", restored)
	}
	// Stage zero must survive the round trip rather than look like no stage.
	if restored.StageIndex == nil || *restored.StageIndex != 0 {
		t.Fatalf("restored StageIndex = %v, want 0", restored.StageIndex)
	}
}

// TestJobWithoutLineageWritesNone keeps a hand-started job free of lineage
// noise, which is also what a pre-existing checkpoint looks like.
func TestJobWithoutLineageWritesNone(t *testing.T) {
	config, err := app.Normalize(JobConfig{
		RefPath: "assets/ref.png", Mode: app.ModeBatch, Circles: 1, BatchSize: 1,
		Iters: 10, PopSize: 20, Threads: 1, Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}

	job := &Job{ID: "11111111-1111-4111-8111-111111111111", Config: config}
	checkpoint := store.NewCheckpoint(job.ID, []float64{1, 1, 1, 1, 0, 0, 1}, 5, 10, 3, config)
	applyJobLineage(checkpoint, job)

	if _, ok := checkpoint.ContinuedFrom(); ok {
		t.Fatalf("checkpoint invented a parent: %+v", checkpoint)
	}

	if checkpoint.ScheduleID != "" || checkpoint.StageIndex != nil {
		t.Fatalf("checkpoint invented a schedule: %+v", checkpoint)
	}
}
