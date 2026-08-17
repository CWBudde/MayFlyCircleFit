package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
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
	if err := f.server.Shutdown(ctx); err != nil {
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
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(document))
	f.server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create schedule status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var response scheduleSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if response.State != store.ScheduleStateRunning {
		t.Fatalf("created schedule state = %q, want running", response.State)
	}
	if response.TotalStages != 2 {
		t.Fatalf("created schedule totalStages = %d, want 2", response.TotalStages)
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
	if err := persistence.DeleteCheckpoint(interrupted.JobID); err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete checkpoint: %v", err)
	}
	// There is nothing left for the restart to restore, which has to be checked
	// here rather than through the job manager afterwards: the adopted stage runs
	// under the very same identifier, so a driver that is quick off the mark puts
	// a fresh job back under it before the assertion could look.
	if _, err := persistence.LoadCheckpoint(interrupted.JobID); !errors.Is(err, store.ErrNotFound) {
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
	if err := persistence.SaveScheduleStage(scheduleID, &rewound); err != nil {
		t.Fatalf("rewind stage 0: %v", err)
	}
	record, err := persistence.LoadSchedule(scheduleID)
	if err != nil {
		t.Fatalf("load schedule: %v", err)
	}
	record.State = store.ScheduleStateRunning
	if err := persistence.SaveSchedule(record); err != nil {
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
	if err := scheduleStore.SaveSchedule(record); err != nil {
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
	if err := scheduleStore.SaveSchedule(record); err != nil {
		t.Fatalf("save schedule: %v", err)
	}
	if fixture.server.scheduleWantsDriver(record.ScheduleID) {
		t.Fatal("a paused schedule asked for a driver")
	}
	if fixture.server.scheduleWantsDriver(uuid.New().String()) {
		t.Fatal("an unknown schedule asked for a driver")
	}

	record.State = store.ScheduleStateRunning
	if err := scheduleStore.SaveSchedule(record); err != nil {
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
	if err := json.Unmarshal(recorder.Body.Bytes(), &manualJob); err != nil {
		t.Fatalf("decode manual job: %v", err)
	}

	// Sample continuously until the whole campaign and the manual job are done.
	// One worker means one running job, whichever of the two owns it.
	deadline := time.Now().Add(90 * time.Second)
	scheduleDone, manualDone := false, false
	for time.Now().Before(deadline) && !(scheduleDone && manualDone) {
		if running := fixture.server.jobManager.GetRunningJobs(); len(running) > fixture.maxJobs {
			ids := make([]string, 0, len(running))
			for _, job := range running {
				ids = append(ids, job.ID)
			}
			t.Fatalf("%d jobs running at once with --max-jobs %d: %v", len(running), fixture.maxJobs, ids)
		}
		if !scheduleDone && fixture.schedule(t, scheduleID).State == store.ScheduleStateCompleted {
			scheduleDone = true
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
		if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
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
		if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
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
