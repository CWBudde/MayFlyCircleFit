package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/google/uuid"
)

// scheduleFixture is one server over one data root, rebuildable so a test can
// stop a server and start another one over the same files -- which is the only
// honest way to test what a restart sees.
type scheduleFixture struct {
	root      string
	imagePath string
	server    *Server
	maxJobs   int
}

func newScheduleFixture(t *testing.T, maxJobs int) *scheduleFixture {
	t.Helper()
	root := t.TempDir()
	imagePath := filepath.Join(root, "reference.png")
	createSimpleTestImage(t, imagePath)
	fixture := &scheduleFixture{root: root, imagePath: imagePath, maxJobs: maxJobs}
	fixture.restart(t)
	t.Cleanup(func() { fixture.stop(t) })

	return fixture
}

// restart replaces the running server with a new one over the same data root.
func (f *scheduleFixture) restart(t *testing.T) {
	t.Helper()

	if f.server != nil {
		f.stop(t)
	}

	persistence, err := store.NewFSStore(filepath.Join(f.root, "artifacts"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	f.server = NewServerWithOptions(":0", persistence, ServerOptions{
		InputRoots:        rootList(f.root),
		MaxConcurrentJobs: f.maxJobs,
		QueueSize:         8,
	})
}

func (f *scheduleFixture) stop(t *testing.T) {
	t.Helper()

	if f.server == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := f.server.Shutdown(ctx)
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	f.server = nil
}

// scheduleDocument builds a two-stage campaign: a batch base plus one extend.
// The budgets are the whole knob a test has for stage duration.
func scheduleDocument(imagePath string, iters, popSize int) string {
	return fmt.Sprintf(`{
  "name": "test campaign",
  "seed": 42,
  "base": {"refPath": %q, "mode": "batch", "circles": 2, "batchSize": 1, "iters": %d, "popSize": %d},
  "steps": [{"type": "extend", "additionalCircles": 1}]
}`, imagePath, iters, popSize)
}

func (f *scheduleFixture) createSchedule(t *testing.T, document string) string {
	t.Helper()
	return f.createScheduleWithStages(t, document, 2)
}

func (f *scheduleFixture) createScheduleWithStages(t *testing.T, document string, wantStages int) string {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(document))
	f.server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("create schedule status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	var response scheduleSummary

	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	if err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	if response.State != store.ScheduleStateRunning {
		t.Fatalf("created schedule state = %q, want running", response.State)
	}

	if response.TotalStages != wantStages {
		t.Fatalf("created schedule totalStages = %d, want %d", response.TotalStages, wantStages)
	}

	return response.ScheduleID
}

func (f *scheduleFixture) post(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))

	return recorder
}

func (f *scheduleFixture) stages(t *testing.T, scheduleID string) []store.ScheduleStageRecord {
	t.Helper()

	scheduleStore, err := f.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	stages, err := scheduleStore.LoadScheduleStages(scheduleID)
	if err != nil {
		t.Fatalf("load stages: %v", err)
	}

	return stages
}

func (f *scheduleFixture) schedule(t *testing.T, scheduleID string) *store.ScheduleRecord {
	t.Helper()

	scheduleStore, err := f.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		t.Fatalf("load schedule: %v", err)
	}

	return record
}

func (f *scheduleFixture) waitForScheduleState(t *testing.T, scheduleID string, want store.ScheduleState, timeout time.Duration) *store.ScheduleRecord {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last store.ScheduleState

	for time.Now().Before(deadline) {
		record := f.schedule(t, scheduleID)
		if record.State == want {
			return record
		}

		last = record.State
		if record.State == store.ScheduleStateFailed && want != store.ScheduleStateFailed {
			t.Fatalf("schedule failed while waiting for %s: %s", want, record.Error)
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("schedule did not reach %s within %s (last %s)", want, timeout, last)

	return nil
}

// waitForRunningStage blocks until a stage record is running and its job has
// actually started, which is the state a mid-stage kill must interrupt.
func (f *scheduleFixture) waitForRunningStage(t *testing.T, scheduleID string, timeout time.Duration) store.ScheduleStageRecord {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, stage := range f.stages(t, scheduleID) {
			if stage.State != store.ScheduleStateRunning || stage.JobID == "" {
				continue
			}

			if job, ok := f.server.jobManager.GetJob(stage.JobID); ok && job.State == StateRunning {
				return stage
			}
		}

		time.Sleep(2 * time.Millisecond)
	}

	t.Fatalf("no stage started within %s", timeout)

	return store.ScheduleStageRecord{}
}

//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleRunsEveryPlannedStageExactlyOnce(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 5, 20))
	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

	stages := fixture.stages(t, scheduleID)
	if len(stages) != 2 {
		t.Fatalf("recorded %d stages, want 2", len(stages))
	}

	seen := map[string]int{}

	for index, stage := range stages {
		if stage.Index != index {
			t.Fatalf("stage %d recorded under index %d", index, stage.Index)
		}

		if stage.State != store.ScheduleStateCompleted {
			t.Fatalf("stage %d state = %q, want completed: %s", index, stage.State, stage.Error)
		}

		if stage.JobID == "" {
			t.Fatalf("stage %d has no job", index)
		}

		seen[stage.JobID]++
	}

	if len(seen) != 2 {
		t.Fatalf("stages share job identifiers: %v", seen)
	}

	if stages[1].ParentJobID != stages[0].JobID {
		t.Fatalf("stage 1 parent = %q, want %q", stages[1].ParentJobID, stages[0].JobID)
	}

	if stages[0].Circles != 2 || stages[1].Circles != 3 {
		t.Fatalf("stage circles = %d, %d; want 2, 3", stages[0].Circles, stages[1].Circles)
	}

	// The jobs know which stage they are, and the lineage is on the checkpoint,
	// so the chain is readable from the job tree alone after a restart.
	for index, stage := range stages {
		job, ok := fixture.server.jobManager.GetJob(stage.JobID)
		if !ok {
			t.Fatalf("stage %d job %s is not registered", index, stage.JobID)
		}

		if job.ScheduleID != scheduleID || job.StageIndex == nil || *job.StageIndex != index {
			t.Fatalf("stage %d job carries schedule %q stage %v", index, job.ScheduleID, job.StageIndex)
		}
	}

	checkpoint, err := fixture.server.store.LoadCheckpoint(stages[1].JobID)
	if err != nil {
		t.Fatalf("load stage 1 checkpoint: %v", err)
	}

	if checkpoint.ExtendedFrom != stages[0].JobID {
		t.Fatalf("checkpoint extendedFrom = %q, want %q", checkpoint.ExtendedFrom, stages[0].JobID)
	}

	if checkpoint.ScheduleID != scheduleID || checkpoint.StageIndex == nil || *checkpoint.StageIndex != 1 {
		t.Fatalf("checkpoint does not place the job in its schedule: %+v", checkpoint.ScheduleID)
	}
}

// TestScheduleResumesTheSameStageAfterRestart is the crash-safety acceptance
// check. A server is stopped while a stage is in flight and a second server is
// started over the same data root; the campaign must continue the very same
// stage, under the very same job identifier, and must neither start a second
// job for it nor move on to the next one.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleResumesTheSameStageAfterRestart(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	// A budget large enough that the base stage is reliably still running when
	// the server is stopped.
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 4000, 20))

	interrupted := fixture.waitForRunningStage(t, scheduleID, 30*time.Second)
	if interrupted.Index != 0 {
		t.Fatalf("expected the base stage to be in flight, got stage %d", interrupted.Index)
	}

	fixture.stop(t)

	// The stage record survives as `running`, naming the job it started. That
	// is the adoptable state; a record that named nothing would be the orphan.
	fixture.restart(t)

	stopped := fixture.stages(t, scheduleID)
	if len(stopped) != 1 {
		t.Fatalf("after the restart %d stages are recorded, want 1", len(stopped))
	}

	if stopped[0].State != store.ScheduleStateRunning {
		t.Fatalf("interrupted stage state = %q, want running", stopped[0].State)
	}

	if stopped[0].JobID != interrupted.JobID {
		t.Fatalf("interrupted stage job = %q, want %q", stopped[0].JobID, interrupted.JobID)
	}

	// The restarted server adopts that stage rather than planning a new one.
	adopted := fixture.waitForRunningStage(t, scheduleID, 30*time.Second)
	if adopted.Index != 0 {
		t.Fatalf("restart resumed stage %d, want stage 0", adopted.Index)
	}

	if adopted.JobID != interrupted.JobID {
		t.Fatalf("restart started job %q for stage 0, want the recorded %q", adopted.JobID, interrupted.JobID)
	}

	if current := fixture.stages(t, scheduleID); len(current) != 1 {
		t.Fatalf("restart recorded %d stages before stage 0 finished, want 1", len(current))
	}
	// Exactly one job exists for the stage: the adopted one. A fork would show
	// up here as a second job carrying the same stage index.
	stageJobs := 0

	for _, job := range fixture.server.jobManager.ListJobs() {
		if job.ScheduleID == scheduleID {
			stageJobs++

			if job.ID != interrupted.JobID {
				t.Fatalf("job %s is a second job for schedule %s", job.ID, scheduleID)
			}
		}
	}

	if stageJobs != 1 {
		t.Fatalf("schedule owns %d jobs after the restart, want 1", stageJobs)
	}

	// Cancelling settles the campaign so the test does not have to sit through
	// the remaining budget.
	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/cancel"); recorder.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	waitForJobState(t, fixture.server.jobManager, interrupted.JobID, StateCancelled)
}

// TestScheduleAdoptsAStageWhoseJobNeverStarted covers the exact interruption
// point the ordering is designed for: the stage record was written as running,
// naming its job, and the process died before that job could be created. The
// next start must adopt the recorded stage, not plan a second one.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleAdoptsAStageWhoseJobNeverStarted(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 4000, 20))
	interrupted := fixture.waitForRunningStage(t, scheduleID, 30*time.Second)
	fixture.stop(t)

	// Erase every trace of the job, leaving only the record that names it.
	persistence, err := store.NewFSStore(filepath.Join(fixture.root, "artifacts"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	err = persistence.DeleteCheckpoint(interrupted.JobID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete checkpoint: %v", err)
	}
	// There is nothing left for the restart to restore, which has to be checked
	// here rather than through the job manager afterwards: the adopted stage runs
	// under the very same identifier, so a driver that is quick off the mark puts
	// a fresh job back under it before the assertion could look.
	_, err = persistence.LoadCheckpoint(interrupted.JobID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("checkpoint for %s survived the delete: %v", interrupted.JobID, err)
	}

	fixture.restart(t)

	adopted := fixture.waitForRunningStage(t, scheduleID, 30*time.Second)
	if adopted.Index != 0 || adopted.JobID != interrupted.JobID {
		t.Fatalf("adopted stage %d job %q, want stage 0 job %q", adopted.Index, adopted.JobID, interrupted.JobID)
	}

	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/cancel"); recorder.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d", recorder.Code)
	}
}

// TestScheduleAdoptsAStageThatAlreadyCompleted covers the other end of the
// crash window: the stage's job wrote its terminal checkpoint and the process
// died before the driver could record the outcome. Rerunning that stage would
// delete a finished checkpoint and repeat every iteration of it, so the record
// must be settled from the restored job instead.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleAdoptsAStageThatAlreadyCompleted(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 5, 20))
	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

	stages := fixture.stages(t, scheduleID)
	if len(stages) != 2 {
		t.Fatalf("recorded %d stages, want 2", len(stages))
	}

	completed := stages[0]

	before, err := fixture.server.store.LoadCheckpoint(completed.JobID)
	if err != nil {
		t.Fatalf("load stage 0 checkpoint: %v", err)
	}

	fixture.stop(t)

	// Rewind the records to the instant before the driver wrote the outcome.
	persistence, err := store.NewFSStore(filepath.Join(fixture.root, "artifacts"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	rewound := completed
	rewound.State = store.ScheduleStateRunning
	rewound.CompletedAt = nil
	rewound.BestCost = 0
	rewound.Iterations = 0

	rewound.Evaluations = 0

	err = persistence.SaveScheduleStage(scheduleID, &rewound)
	if err != nil {
		t.Fatalf("rewind stage 0: %v", err)
	}

	record, err := persistence.LoadSchedule(scheduleID)
	if err != nil {
		t.Fatalf("load schedule: %v", err)
	}

	record.State = store.ScheduleStateRunning

	err = persistence.SaveSchedule(record)
	if err != nil {
		t.Fatalf("rewind schedule: %v", err)
	}

	fixture.restart(t)
	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

	after := fixture.stages(t, scheduleID)
	if len(after) != 2 {
		t.Fatalf("after the restart %d stages are recorded, want 2", len(after))
	}

	if after[0].JobID != completed.JobID {
		t.Fatalf("adopted stage 0 job = %q, want %q", after[0].JobID, completed.JobID)
	}

	if after[0].State != store.ScheduleStateCompleted {
		t.Fatalf("adopted stage 0 state = %q, want completed: %s", after[0].State, after[0].Error)
	}

	if after[0].BestCost != completed.BestCost || after[0].Iterations != completed.Iterations {
		t.Fatalf("adopted stage 0 reports cost %v over %d iterations, want %v over %d",
			after[0].BestCost, after[0].Iterations, completed.BestCost, completed.Iterations)
	}
	// The finished checkpoint is the thing a rerun would have thrown away.
	reloaded, err := fixture.server.store.LoadCheckpoint(completed.JobID)
	if err != nil {
		t.Fatalf("stage 0 checkpoint did not survive the restart: %v", err)
	}

	if !reloaded.Timestamp.Equal(before.Timestamp) {
		t.Fatalf("stage 0 was rerun: checkpoint written at %s, was %s", reloaded.Timestamp, before.Timestamp)
	}
}

// TestScheduleStageIsNotStartedAfterAPause covers the window between the
// driver reading the schedule as running and writing the stage record. Pause
// and cancel are durable before they are acted on, so a driver holding a stale
// read must notice and start nothing.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleStageIsNotStartedAfterAPause(t *testing.T) {
	fixture := newScheduleFixture(t, 2)

	scheduleStore, err := fixture.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	document, err := app.ParseSchedule([]byte(scheduleDocument(fixture.imagePath, 5, 20)))
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}

	record, err := store.NewScheduleRecord(uuid.New().String(), *document)
	if err != nil {
		t.Fatalf("build schedule record: %v", err)
	}

	record.State = store.ScheduleStatePaused

	err = scheduleStore.SaveSchedule(record)
	if err != nil {
		t.Fatalf("save schedule: %v", err)
	}

	plan, err := document.Expand()
	if err != nil {
		t.Fatalf("expand schedule: %v", err)
	}

	// What the driver holds is the read it took before the pause landed.
	stale := *record
	stale.State = store.ScheduleStateRunning

	outcome, err := fixture.server.runScheduleStage(scheduleStore, &stale, plan, nil, 0, nil)
	if err != nil {
		t.Fatalf("runScheduleStage() error = %v", err)
	}

	if outcome != store.ScheduleStateRunning {
		t.Fatalf("outcome = %q, want the running signal that stops the driver without a verdict", outcome)
	}

	if stages := fixture.stages(t, record.ScheduleID); len(stages) != 0 {
		t.Fatalf("a paused campaign recorded %d stages, want 0", len(stages))
	}

	for _, job := range fixture.server.jobManager.ListJobs() {
		if job.ScheduleID == record.ScheduleID {
			t.Fatalf("a paused campaign started job %s", job.ID)
		}
	}
}

// TestScheduleWantsDriverFollowsTheDurableState covers the handoff decision a
// driver makes as it deregisters: a resume that raced with the stop saw the
// registration and started nothing, so the exiting driver has to look at the
// record rather than assume it is done.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleWantsDriverFollowsTheDurableState(t *testing.T) {
	fixture := newScheduleFixture(t, 1)

	scheduleStore, err := fixture.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	document, err := app.ParseSchedule([]byte(scheduleDocument(fixture.imagePath, 5, 20)))
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}

	record, err := store.NewScheduleRecord(uuid.New().String(), *document)
	if err != nil {
		t.Fatalf("build schedule record: %v", err)
	}

	record.State = store.ScheduleStatePaused

	err = scheduleStore.SaveSchedule(record)
	if err != nil {
		t.Fatalf("save schedule: %v", err)
	}

	if fixture.server.scheduleWantsDriver(record.ScheduleID) {
		t.Fatal("a paused schedule asked for a driver")
	}

	if fixture.server.scheduleWantsDriver(uuid.New().String()) {
		t.Fatal("an unknown schedule asked for a driver")
	}

	record.State = store.ScheduleStateRunning

	err = scheduleStore.SaveSchedule(record)
	if err != nil {
		t.Fatalf("save schedule: %v", err)
	}

	if !fixture.server.scheduleWantsDriver(record.ScheduleID) {
		t.Fatal("a resumed schedule was left without a driver")
	}

	// A server on its way down hands nothing over; the record stays adoptable.
	fixture.server.cancel()

	if fixture.server.scheduleWantsDriver(record.ScheduleID) {
		t.Fatal("a shutting-down server handed the schedule back to its driver")
	}
}

// TestScheduleAndManualJobShareTheJobLimit is the oversubscription acceptance
// check: a stage goes through the same admission control as a hand-created job,
// so the two together can never exceed MaxConcurrentJobs.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleAndManualJobShareTheJobLimit(t *testing.T) {
	fixture := newScheduleFixture(t, 1)
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 80, 20))
	fixture.waitForRunningStage(t, scheduleID, 30*time.Second)

	manual := createJobRequest{JobConfig: JobConfig{
		RefPath: fixture.imagePath, Mode: "batch", Circles: 2, BatchSize: 1, Iters: 80, PopSize: 20, Seed: 7,
	}}

	body, err := json.Marshal(manual)
	if err != nil {
		t.Fatalf("marshal manual job: %v", err)
	}

	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder,
		httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(string(body))))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("create manual job status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	var manualJob Job

	err = json.Unmarshal(recorder.Body.Bytes(), &manualJob)
	if err != nil {
		t.Fatalf("decode manual job: %v", err)
	}

	// Sample continuously until the whole campaign and the manual job are done.
	// One worker means one running job, whichever of the two owns it.
	deadline := time.Now().Add(90 * time.Second)

	scheduleDone, manualDone := false, false
	for time.Now().Before(deadline) && (!scheduleDone || !manualDone) {
		if running := fixture.server.jobManager.GetRunningJobs(); len(running) > fixture.maxJobs {
			ids := make([]string, 0, len(running))
			for _, job := range running {
				ids = append(ids, job.ID)
			}

			t.Fatalf("%d jobs running at once with --max-jobs %d: %v", len(running), fixture.maxJobs, ids)
		}

		if !scheduleDone {
			// A campaign that settled anywhere but `completed` will never reach
			// it, so say why now instead of sitting out the deadline and
			// reporting nothing but "not done".
			switch current := fixture.schedule(t, scheduleID); current.State {
			case store.ScheduleStateCompleted:
				scheduleDone = true
			case store.ScheduleStateFailed, store.ScheduleStateCancelled:
				t.Fatalf("schedule settled as %q: %s", current.State, current.Error)
			}
		}

		if !manualDone {
			if job, ok := fixture.server.jobManager.GetJob(manualJob.ID); ok && job.State == StateCompleted {
				manualDone = true
			}
		}

		time.Sleep(time.Millisecond)
	}

	if !scheduleDone || !manualDone {
		t.Fatalf("schedule done = %v, manual job done = %v", scheduleDone, manualDone)
	}
	// Both really ran: serialized by the limit, not refused by it.
	for _, stage := range fixture.stages(t, scheduleID) {
		if stage.State != store.ScheduleStateCompleted {
			t.Fatalf("stage %d state = %q, want completed", stage.Index, stage.State)
		}
	}
}

//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestSchedulePauseStopsAtAStageBoundaryAndResumes(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 5, 20))

	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/pause"); recorder.Code != http.StatusAccepted {
		t.Fatalf("pause status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	// Pausing twice is a conflict, not a silent no-op.
	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/pause"); recorder.Code != http.StatusConflict {
		t.Fatalf("second pause status = %d, want 409", recorder.Code)
	}

	// A paused campaign runs no further stages once the in-flight one settles.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		settled := true

		for _, stage := range fixture.stages(t, scheduleID) {
			if stage.State == store.ScheduleStateRunning {
				settled = false
			}
		}

		if settled {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	paused := fixture.stages(t, scheduleID)
	if len(paused) == 2 {
		t.Skip("the campaign finished before the pause was observed; nothing to resume")
	}

	if state := fixture.schedule(t, scheduleID).State; state != store.ScheduleStatePaused {
		t.Fatalf("schedule state = %q, want paused", state)
	}

	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/resume"); recorder.Code != http.StatusAccepted {
		t.Fatalf("resume status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

	if stages := fixture.stages(t, scheduleID); len(stages) != 2 {
		t.Fatalf("resumed campaign recorded %d stages, want 2", len(stages))
	}
}

//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleCancelCancelsTheInFlightStage(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 4000, 20))
	stage := fixture.waitForRunningStage(t, scheduleID, 30*time.Second)

	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/cancel"); recorder.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	waitForJobState(t, fixture.server.jobManager, stage.JobID, StateCancelled)

	if state := fixture.schedule(t, scheduleID).State; state != store.ScheduleStateCancelled {
		t.Fatalf("schedule state = %q, want cancelled", state)
	}
	// A cancelled campaign is terminal: it cannot be resumed back into life.
	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/resume"); recorder.Code != http.StatusConflict {
		t.Fatalf("resume of a cancelled schedule status = %d, want 409", recorder.Code)
	}
}

//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleEndpointsFollowTheJobConventions(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	handler := fixture.server.Handler()

	t.Run("unknown field is refused", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		body := `{"base": {"refPath": "x.png", "mode": "batch", "circles": 2, "iters": 5, "popSize": 6}, "nope": 1}`
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body)))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", recorder.Code)
		}
	})

	t.Run("silently defaulted field names the effective one", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		body := fmt.Sprintf(`{"base": {"refPath": %q, "mode": "batch", "circles": 2, "iters": 5, "popSize": 6, "convergenceEnabled": false}}`, fixture.imagePath)
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body)))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", recorder.Code)
		}

		if !strings.Contains(recorder.Body.String(), "disableConvergence") {
			t.Fatalf("error does not name the effective field: %s", recorder.Body.String())
		}
	})

	t.Run("reference outside the input roots is refused", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		body := scheduleDocument(filepath.Join(t.TempDir(), "elsewhere.png"), 5, 20)
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body)))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", recorder.Code)
		}
	})

	t.Run("unknown schedule is not found", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/schedules/2f1c8e2a-0000-4000-8000-000000000000", nil))

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})

	t.Run("non-UUID identifier is refused", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/schedules/not-a-uuid", nil))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", recorder.Code)
		}
	})

	t.Run("wrong method is refused", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/schedules", nil))

		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", recorder.Code)
		}
	})

	t.Run("detail and listing report the campaign", func(t *testing.T) {
		scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 5, 20))
		fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/schedules/"+scheduleID, nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("detail status = %d", recorder.Code)
		}

		var detail scheduleDetail

		err := json.Unmarshal(recorder.Body.Bytes(), &detail)
		if err != nil {
			t.Fatalf("decode detail: %v", err)
		}

		if len(detail.Stages) != 2 || detail.TotalStages != 2 {
			t.Fatalf("detail reports %d of %d stages, want 2 of 2", len(detail.Stages), detail.TotalStages)
		}

		if detail.Document.Name != "test campaign" {
			t.Fatalf("detail document name = %q", detail.Document.Name)
		}

		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("list status = %d", recorder.Code)
		}

		var listed []scheduleSummary

		err = json.Unmarshal(recorder.Body.Bytes(), &listed)
		if err != nil {
			t.Fatalf("decode listing: %v", err)
		}

		found := false

		for _, summary := range listed {
			if summary.ScheduleID == scheduleID {
				found = true
			}
		}

		if !found {
			t.Fatalf("listing omits schedule %s", scheduleID)
		}
	})
}

//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestSchedulesAreUnavailableWithoutACheckpointStore(t *testing.T) {
	server := NewServerWithOptions(":0", nil, ServerOptions{})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_ = server.Shutdown(ctx)
	})

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}

// TestScheduleSkipsAStageItsPolicyDeclines is the executor half of Task 16.3.
// The polish is planned, so a dry run would print it, but its circle condition
// never matches; the campaign must record the skip and continue the chain from
// the last stage that actually ran.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleSkipsAStageItsPolicyDeclines(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	document := fmt.Sprintf(`{
  "name": "conditional campaign",
  "seed": 42,
  "base": {"refPath": %q, "mode": "batch", "circles": 2, "batchSize": 1, "iters": 5, "popSize": 20},
  "steps": [
    {"type": "extend", "additionalCircles": 1},
    {"type": "polish", "when": {"circles": [99]}},
    {"type": "extend", "additionalCircles": 1}
  ]
}`, fixture.imagePath)
	scheduleID := fixture.createScheduleWithStages(t, document, 4)
	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

	stages := fixture.stages(t, scheduleID)
	if len(stages) != 4 {
		t.Fatalf("recorded %d stages, want 4", len(stages))
	}

	wantStates := []store.ScheduleState{
		store.ScheduleStateCompleted,
		store.ScheduleStateCompleted,
		store.ScheduleStateSkipped,
		store.ScheduleStateCompleted,
	}
	for index, want := range wantStates {
		if stages[index].State != want {
			t.Fatalf("stage %d state = %q, want %q", index, stages[index].State, want)
		}
	}

	skipped := stages[2]
	if skipped.JobID != "" {
		t.Fatalf("skipped stage names job %q", skipped.JobID)
	}

	if !strings.Contains(skipped.Reason, "99") {
		t.Fatalf("skipped stage reason = %q, want the condition it failed", skipped.Reason)
	}
	// The chain steps over the skipped stage rather than breaking on it.
	if stages[3].ParentJobID != stages[1].JobID {
		t.Fatalf("stage 3 parent = %q, want stage 1's job %q", stages[3].ParentJobID, stages[1].JobID)
	}

	if stages[3].Circles != 4 {
		t.Fatalf("stage 3 circles = %d, want 4", stages[3].Circles)
	}
}

//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestNextScheduleStageDerivesTheCursorFromTheRecords(t *testing.T) {
	cases := []struct {
		name     string
		states   []store.ScheduleState
		wantNext int
		wantHeld bool
		blocked  bool
	}{
		{name: "nothing recorded", states: nil, wantNext: 0},
		{name: "first completed", states: []store.ScheduleState{store.ScheduleStateCompleted}, wantNext: 1},
		{name: "first still running", states: []store.ScheduleState{store.ScheduleStateRunning}, wantNext: 0, wantHeld: true},
		{name: "all completed", states: []store.ScheduleState{store.ScheduleStateCompleted, store.ScheduleStateCompleted, store.ScheduleStateCompleted}, wantNext: -1},
		{name: "a failed stage blocks", states: []store.ScheduleState{store.ScheduleStateFailed}, wantNext: -1, blocked: true},
		{
			name:     "a skipped stage is settled, not pending",
			states:   []store.ScheduleState{store.ScheduleStateCompleted, store.ScheduleStateSkipped},
			wantNext: 2,
		},
		{
			name:     "every stage settled, some by policy",
			states:   []store.ScheduleState{store.ScheduleStateSkipped, store.ScheduleStateCompleted, store.ScheduleStateSkipped},
			wantNext: -1,
		},
	}
	planned := make([]app.ScheduleStage, 3)

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorded := make([]store.ScheduleStageRecord, 0, len(testCase.states))
			for index, state := range testCase.states {
				recorded = append(recorded, store.ScheduleStageRecord{Index: index, State: state, JobID: ""})
			}

			next, held, blocked := nextScheduleStage(planned, recorded)
			if next != testCase.wantNext {
				t.Fatalf("next = %d, want %d", next, testCase.wantNext)
			}

			if (held != nil) != testCase.wantHeld {
				t.Fatalf("held record = %v, want held %v", held, testCase.wantHeld)
			}

			if (blocked != "") != testCase.blocked {
				t.Fatalf("blocked = %q, want blocked %v", blocked, testCase.blocked)
			}
		})
	}
}

// TestScheduleStageOutcomesMarkOnlyCompletedCostsAsMeasured pins the bridge
// between the store's records and the policy's view of them. A completed stage
// that reached a perfect fit records a best cost of exactly zero, and policy
// has to see that as a measurement; a record that never settled carries no cost
// at all, and its zero must stay an absence.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleStageOutcomesMarkOnlyCompletedCostsAsMeasured(t *testing.T) {
	recorded := []store.ScheduleStageRecord{
		{Index: 0, Kind: app.ScheduleStageBase, State: store.ScheduleStateCompleted, BestCost: 161.99},
		{Index: 1, Kind: app.ScheduleStagePolish, State: store.ScheduleStateCompleted, BestCost: 0},
		{Index: 2, Kind: app.ScheduleStagePolish, State: store.ScheduleStateSkipped},
		{Index: 3, Kind: app.ScheduleStagePolish, State: store.ScheduleStateRunning},
		{Index: 4, Kind: app.ScheduleStagePolish, State: store.ScheduleStateCompleted, BestCost: math.Inf(1)},
		{Index: 5, Kind: app.ScheduleStagePolish, State: store.ScheduleStateCompleted, BestCost: math.NaN()},
	}
	wantMeasured := []bool{true, true, false, false, false, false}
	wantState := []app.ScheduleOutcomeState{
		app.ScheduleOutcomeCompleted, app.ScheduleOutcomeCompleted, app.ScheduleOutcomeSkipped,
		app.ScheduleOutcomePending, app.ScheduleOutcomeCompleted, app.ScheduleOutcomeCompleted,
	}

	outcomes := scheduleStageOutcomes(recorded)
	if len(outcomes) != len(recorded) {
		t.Fatalf("projected %d outcomes, want %d", len(outcomes), len(recorded))
	}

	for index, outcome := range outcomes {
		if outcome.State != wantState[index] {
			t.Errorf("stage %d state = %q, want %q", index, outcome.State, wantState[index])
		}

		if outcome.CostMeasured != wantMeasured[index] {
			t.Errorf("stage %d CostMeasured = %v, want %v", index, outcome.CostMeasured, wantMeasured[index])
		}
	}
}

// TestScheduleBarrierPausesBeforeTheStageAndResumeReleasesIt is the whole
// contract of a barrier: the campaign stops where the document said, the
// barred stage is not recorded at all (so nothing has to be undone), the reason
// is durable enough to poll, and one resume carries it past — rather than
// walking straight back into the same barrier.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleBarrierPausesBeforeTheStageAndResumeReleasesIt(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	document := fmt.Sprintf(`{
  "name": "barrier campaign",
  "seed": 42,
  "base": {"refPath": %q, "mode": "batch", "circles": 2, "batchSize": 1, "iters": 5, "popSize": 20},
  "steps": [{"type": "extend", "additionalCircles": 1, "pauseBefore": true}]
}`, fixture.imagePath)
	scheduleID := fixture.createScheduleWithStages(t, document, 2)

	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStatePaused, 30*time.Second)

	record := fixture.schedule(t, scheduleID)
	if record.Error == "" {
		t.Fatal("a barrier pause recorded no reason; there is nothing for an operator to poll")
	}
	// The barred stage must be untouched, not recorded and rolled back.
	if stages := fixture.stages(t, scheduleID); len(stages) != 1 {
		t.Fatalf("recorded %d stages at the barrier, want only the base", len(stages))
	}

	if recorder := fixture.post(t, "/api/v1/schedules/"+scheduleID+"/resume"); recorder.Code != http.StatusAccepted {
		t.Fatalf("resume status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

	if stages := fixture.stages(t, scheduleID); len(stages) != 2 {
		t.Fatalf("resumed campaign recorded %d stages, want 2", len(stages))
	}
}

// advisoryDocument raises the population above the default while leaving the
// epochs alone, which is the pair Task 16.9 measured as wasted: an epoch
// reseeds from the best candidate so far, so a large population with one epoch
// has nowhere to spend itself.
func advisoryDocument(imagePath string, epochs int) string {
	return fmt.Sprintf(`{
  "name": "advisory campaign",
  "seed": 42,
  "base": {"refPath": %q, "mode": "batch", "circles": 2, "batchSize": 1,
           "iters": 5, "popSize": 100, "optimizerEpochs": %d},
  "steps": [{"type": "extend", "additionalCircles": 1}]
}`, imagePath, epochs)
}

// TestScheduleAdvisoriesReachBothResponses pins the advisory on the HTTP
// surface. It is not persisted anywhere -- it is recomputed from the stored
// document on every read -- so the create response and a detail fetched later
// have to agree, and a document that raises both halves of the budget pair has
// to fall silent.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleAdvisoriesReachBothResponses(t *testing.T) {
	fixture := newScheduleFixture(t, 2)

	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/v1/schedules",
		strings.NewReader(advisoryDocument(fixture.imagePath, 1))))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	var created scheduleSummary

	err := json.Unmarshal(recorder.Body.Bytes(), &created)
	if err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	if len(created.Warnings) != 1 {
		t.Fatalf("create response carried %d warnings, want 1: %v", len(created.Warnings), created.Warnings)
	}

	if !strings.Contains(created.Warnings[0], "base.popSize") ||
		!strings.Contains(created.Warnings[0], "optimizerEpochs") {
		t.Fatalf("warning does not name the field pair it is about: %q", created.Warnings[0])
	}

	detail := fixture.detail(t, created.ScheduleID)
	if len(detail.Warnings) != 1 || detail.Warnings[0] != created.Warnings[0] {
		t.Fatalf("detail warnings = %v, want the create response's %v", detail.Warnings, created.Warnings)
	}

	// The same population with the epochs to spend it is the advice being
	// followed, and following advice must silence it.
	quiet := fixture.detail(t, fixture.createSchedule(t, advisoryDocument(fixture.imagePath, 3)))
	if len(quiet.Warnings) != 0 {
		t.Fatalf("a document raising both halves of the pair still warned: %v", quiet.Warnings)
	}
}

// detail fetches the campaign detail response through the handler.
func (f *scheduleFixture) detail(t *testing.T, scheduleID string) scheduleDetail {
	t.Helper()

	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/schedules/"+scheduleID, nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	var detail scheduleDetail

	err := json.Unmarshal(recorder.Body.Bytes(), &detail)
	if err != nil {
		t.Fatalf("decode detail response: %v", err)
	}

	return detail
}

// growthCampaign writes a campaign that has measurably progressed without
// running one: a three-stage plan to 3000 circles, with the first two stages
// recorded as completed at the costs the measured campaign reached. It is
// written straight to the store rather than posted, because a posted schedule
// starts a driver and this test is about arithmetic over records, not about
// stages that run.
func (f *scheduleFixture) growthCampaign(t *testing.T, state store.ScheduleState) string {
	t.Helper()

	document := fmt.Sprintf(`{
  "name": "growth campaign",
  "seed": 42,
  "base": {"refPath": %q, "mode": "batch", "circles": 1000, "batchSize": 1, "iters": 5, "popSize": 20},
  "steps": [{"type": "extend", "additionalCircles": 1000, "repeat": 2}]
}`, f.imagePath)

	parsed, err := app.ParseSchedule([]byte(document))
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}

	record, err := store.NewScheduleRecord(uuid.New().String(), *parsed)
	if err != nil {
		t.Fatalf("build schedule record: %v", err)
	}

	record.State = state

	scheduleStore, err := f.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	err = scheduleStore.SaveSchedule(record)
	if err != nil {
		t.Fatalf("save schedule: %v", err)
	}

	plan, err := record.Document.Expand()
	if err != nil {
		t.Fatalf("expand document: %v", err)
	}

	if len(plan) != 3 {
		t.Fatalf("plan expands to %d stages, want 3", len(plan))
	}

	// The costs are the measured campaign's own milestones at 1000 and 2000
	// circles, so the leg between them is the one the estimator was built on.
	started := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	for index, cost := range []float64{96.199, 64.602} {
		stage := store.NewScheduleStageRecord(record.ScheduleID, plan[index])
		stage.State = store.ScheduleStateCompleted
		stage.BestCost = cost

		begin := started.Add(time.Duration(index) * time.Hour)
		end := begin.Add(30 * time.Minute)
		stage.StartedAt, stage.CompletedAt = &begin, &end

		err = scheduleStore.SaveScheduleStage(record.ScheduleID, stage)
		if err != nil {
			t.Fatalf("save stage %d: %v", index, err)
		}
	}

	return record.ScheduleID
}

// TestScheduleDetailProjectsWhatIsLeft covers the second question the campaign
// surface now answers: not when the plan finishes, but where the fit lands
// once it does. The figures are checked against the leg the records describe
// rather than against the whole campaign average, because that is the rate the
// projection is required to extrapolate.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleDetailProjectsWhatIsLeft(t *testing.T) {
	fixture := newScheduleFixture(t, 1)
	scheduleID := fixture.growthCampaign(t, store.ScheduleStateRunning)

	detail := fixture.detail(t, scheduleID)
	if detail.Projection == nil {
		t.Fatal("a running campaign with two measured stages and one to go carries no projection")
	}

	cost := detail.Projection.Cost
	if !cost.Projected || cost.Samples != 2 {
		t.Fatalf("cost projection = (projected %v, %d samples), want projected over 2",
			cost.Projected, cost.Samples)
	}

	if cost.LatestCircles != 2000 || cost.LatestCost != 64.602 {
		t.Fatalf("campaign stands at (%d circles, cost %f), want (2000, 64.602)",
			cost.LatestCircles, cost.LatestCost)
	}

	if cost.RemainingCircles != 1000 || !cost.HasCircleCeiling {
		t.Fatalf("circle ceiling = (%d remaining, has %v), want (1000, true)",
			cost.RemainingCircles, cost.HasCircleCeiling)
	}

	// 96.199 - 64.602 over the 1000 circles between them is 0.031597 per
	// circle, which over the last 1000 leaves 33.005.
	if math.Abs(cost.RecentGainPerCircle-0.031597) > 1e-9 {
		t.Errorf("trailing rate = %f cost/circle, want 0.031597", cost.RecentGainPerCircle)
	}

	if math.Abs(cost.CostAtPlanEnd-33.005) > 1e-6 {
		t.Errorf("cost at the plan's end = %f, want 33.005", cost.CostAtPlanEnd)
	}

	// PSNR is derived server-side so the browser never re-implements it.
	if cost.PlanEndPSNR == nil || cost.PlanEndPSNRInfinite {
		t.Errorf("plan-end PSNR = (%v, infinite %v), want a finite figure",
			cost.PlanEndPSNR, cost.PlanEndPSNRInfinite)
	}

	if detail.Projection.RemainingStages != 1 {
		t.Errorf("remaining stages = %d, want 1", detail.Projection.RemainingStages)
	}
}

// TestScheduleDetailOmitsTheProjectionForACampaignThatIsOver is the other half
// of the rule the CLI applies: the estimate anchors at a clock, and a campaign
// that will never advance again makes that anchor a false claim.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleDetailOmitsTheProjectionForACampaignThatIsOver(t *testing.T) {
	fixture := newScheduleFixture(t, 1)

	for _, state := range []store.ScheduleState{
		store.ScheduleStateCompleted, store.ScheduleStateFailed, store.ScheduleStateCancelled,
	} {
		t.Run(string(state), func(t *testing.T) {
			scheduleID := fixture.growthCampaign(t, state)
			if detail := fixture.detail(t, scheduleID); detail.Projection != nil {
				t.Fatalf("a %s campaign carries a projection: %+v", state, detail.Projection)
			}
		})
	}
}

// TestSettledStageRecordsTheCirclesItMaterialized covers the settlement copy
// the projections depend on. A stage record's Circles is the *planned* count —
// it comes from the expanded plan and Validate pins it to Config.Circles, so it
// can never be rewritten — which means a stage that built fewer circles than it
// asked for has nowhere to say so unless settlement records the job's own tally.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestSettledStageRecordsTheCirclesItMaterialized(t *testing.T) {
	fixture := newScheduleFixture(t, 2)
	scheduleID := fixture.createSchedule(t, scheduleDocument(fixture.imagePath, 5, 20))
	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, 60*time.Second)

	stages := fixture.stages(t, scheduleID)
	if len(stages) != 2 {
		t.Fatalf("recorded %d stages, want 2", len(stages))
	}

	for _, stage := range stages {
		if stage.State != store.ScheduleStateCompleted {
			t.Fatalf("stage %d state = %q, want completed: %s", stage.Index, stage.State, stage.Error)
		}
		// The checkpoint is where the job's tally lands, so it is the
		// independent copy of the figure the record is supposed to carry.
		checkpoint, err := fixture.server.store.LoadCheckpoint(stage.JobID)
		if err != nil {
			t.Fatalf("load stage %d checkpoint: %v", stage.Index, err)
		}

		if stage.ActualCircles != checkpoint.ActualCircles {
			t.Errorf("stage %d recorded %d materialized circles, its job built %d",
				stage.Index, stage.ActualCircles, checkpoint.ActualCircles)
		}
		// A settled stage that left the field at zero would read back as the
		// planned count through MaterializedCircles, which is exactly the
		// silent fallback this copy exists to make unnecessary.
		if stage.ActualCircles == 0 {
			t.Errorf("stage %d settled without recording what it built", stage.Index)
		}

		if stage.MaterializedCircles() != checkpoint.ActualCircles {
			t.Errorf("stage %d reports %d circles on the canvas, the checkpoint holds %d",
				stage.Index, stage.MaterializedCircles(), checkpoint.ActualCircles)
		}
	}
}

// TestScheduleProjectionChargesOnlyTheCirclesAStageBuilt is the refill-limit
// case. A batch stage that terminates at its refill limit materializes fewer
// circles than the plan requested, and both halves of the cost projection —
// the per-circle rate's denominator and the distance left to the plan's
// ceiling — have to be measured against what exists rather than what was asked
// for, or the campaign is charged for circles that were never drawn.
//
//nolint:paralleltest // boots a worker-backed server; parallel campaigns would skew its wall-clock waits.
func TestScheduleProjectionChargesOnlyTheCirclesAStageBuilt(t *testing.T) {
	fixture := newScheduleFixture(t, 1)
	scheduleID := fixture.growthCampaign(t, store.ScheduleStateRunning)

	scheduleStore, err := fixture.server.scheduleStore()
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}

	stages := fixture.stages(t, scheduleID)
	if len(stages) != 2 || stages[1].Circles != 2000 {
		t.Fatalf("fixture recorded %d stages, the last at %d circles; want 2 ending at 2000",
			len(stages), stages[len(stages)-1].Circles)
	}

	// The second stage asked for 2000 circles and refilled its way to 1999.
	limited := stages[1]
	limited.ActualCircles = limited.Circles - 1

	err = scheduleStore.SaveScheduleStage(scheduleID, &limited)
	if err != nil {
		t.Fatalf("record the refill-limited stage: %v", err)
	}

	detail := fixture.detail(t, scheduleID)
	if detail.Projection == nil {
		t.Fatal("a running campaign with two measured stages and one to go carries no projection")
	}
	// The listing carries the realized count too, so the CLI projects from the
	// same figure rather than re-deriving one the server did not send.
	if detail.Stages[1].ActualCircles != 1999 {
		t.Errorf("stage 1 summary reports %d materialized circles, want 1999",
			detail.Stages[1].ActualCircles)
	}

	cost := detail.Projection.Cost
	if cost.LatestCircles != 1999 {
		t.Errorf("campaign stands at %d circles, want the 1999 it built", cost.LatestCircles)
	}

	if cost.RemainingCircles != 1001 {
		t.Errorf("remaining circles = %d, want 3000 - 1999", cost.RemainingCircles)
	}

	// 96.199 - 64.602 over the 999 circles that actually appeared, not over
	// the 1000 the stage requested.
	wantRate := (96.199 - 64.602) / 999
	if math.Abs(cost.RecentGainPerCircle-wantRate) > 1e-9 {
		t.Errorf("trailing rate = %f cost/circle, want %f", cost.RecentGainPerCircle, wantRate)
	}

	wantEnd := 64.602 - wantRate*1001
	if math.Abs(cost.CostAtPlanEnd-wantEnd) > 1e-6 {
		t.Errorf("cost at the plan's end = %f, want %f", cost.CostAtPlanEnd, wantEnd)
	}
}
