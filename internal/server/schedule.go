package server

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/google/uuid"
)

// scheduleStagePollInterval is how often a driver looks at its stage's job. The
// driver holds no worker slot, so this costs a map lookup per tick and nothing
// else; it is short so a cancellation is observed promptly.
const scheduleStagePollInterval = 20 * time.Millisecond

// scheduleEnqueueTimeout bounds how long a stage waits for room in the job
// queue. A full queue is transient — it means manual jobs are using the host —
// so a campaign waits rather than dying, but not forever.
const scheduleEnqueueTimeout = 30 * time.Second

// errScheduleDriverRunning reports that a schedule already has a driver in this
// process. It is the in-process half of the single-executor guarantee.
var errScheduleDriverRunning = errors.New("schedule is already running")

// A schedule executes as one goroutine per running schedule, and that goroutine
// is deliberately not a job worker. It plans, records, and waits; the stages it
// creates are ordinary jobs that go through s.enqueueJob and the same
// MaxConcurrentJobs worker pool a hand-issued POST /api/v1/jobs uses. A
// schedule therefore cannot oversubscribe the host, and cannot starve itself by
// occupying the only worker slot while waiting for a stage to run in it.
//
// Progress has exactly one representation: the stage records in the store. The
// driver holds no cursor. Every iteration reloads the records and derives the
// next stage from them, so there is nothing that can disagree with the record
// of what actually ran — the drift that put a previous orchestrator four hours
// and 352 circles out of date with itself.
//
// The schedule record carries the operator's intent (run, pause, cancel) and
// the campaign's terminal outcome. It never carries progress.

// startScheduleDriver launches the executor for a schedule, refusing a second
// driver for the same schedule.
func (s *Server) startScheduleDriver(scheduleID string) error {
	s.schedulesMu.Lock()
	if _, running := s.scheduleDrivers[scheduleID]; running {
		s.schedulesMu.Unlock()
		return errScheduleDriverRunning
	}
	s.scheduleDrivers[scheduleID] = struct{}{}
	s.schedulesMu.Unlock()

	s.scheduleWG.Add(1)
	go func() {
		defer s.scheduleWG.Done()
		for {
			exit := s.driveSchedule(scheduleID)
			// Deregistering is the handoff point, so the durable intent is read
			// once more while holding the lock startScheduleDriver takes. A
			// resume that arrived while this driver was stopping saw the
			// registration, reported success, and started nothing; without this
			// re-read it would leave a schedule persisted as `running` with no
			// executor until an operator paused and resumed it again.
			//
			// Only a driver that stopped on the schedule's own state hands over.
			// One that stopped because it could not read the store would spin
			// here, so it releases the schedule and lets the next start adopt it.
			s.schedulesMu.Lock()
			if exit == scheduleDriverStopped && s.scheduleWantsDriver(scheduleID) {
				s.schedulesMu.Unlock()
				continue
			}
			delete(s.scheduleDrivers, scheduleID)
			s.schedulesMu.Unlock()
			return
		}
	}()
	return nil
}

// scheduleWantsDriver reports whether the durable record still asks for an
// executor. Callers hold schedulesMu, so the answer cannot be overtaken by a
// resume between this check and the deregistration that follows it.
func (s *Server) scheduleWantsDriver(scheduleID string) bool {
	if s.ctx.Err() != nil {
		return false
	}
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		return false
	}
	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		return false
	}
	return record.State == store.ScheduleStateRunning
}

// restoreSchedules adopts every schedule that was running when the process
// last stopped. A schedule left running is resumed, which is what makes a
// crash mid-stage recoverable; a paused or terminal schedule is left alone.
func (s *Server) restoreSchedules() {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		return
	}
	records, err := scheduleStore.ListSchedules()
	if err != nil {
		slog.Warn("Unable to list schedules for restore", "error", err)
		return
	}
	for _, record := range records {
		if record.State != store.ScheduleStateRunning {
			continue
		}
		if err := s.startScheduleDriver(record.ScheduleID); err != nil {
			slog.Error("Unable to resume schedule", "schedule_id", record.ScheduleID, "error", err)
			continue
		}
		slog.Info("Resumed schedule", "schedule_id", record.ScheduleID)
	}
}

// scheduleDriverExit says why a driver returned, which is what decides whether
// the schedule can be handed straight back to it.
type scheduleDriverExit int

const (
	// scheduleDriverStopped means the driver returned on the schedule's own
	// durable state — settled, paused, cancelled, or the server going down.
	scheduleDriverStopped scheduleDriverExit = iota
	// scheduleDriverFailed means the driver could not read what it needed. The
	// schedule may still say `running`, so retrying at once would spin.
	scheduleDriverFailed
)

// driveSchedule runs stages until the campaign finishes, is paused, is
// cancelled, or the server shuts down.
func (s *Server) driveSchedule(scheduleID string) scheduleDriverExit {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		slog.Error("Schedule executor has no schedule store", "schedule_id", scheduleID, "error", err)
		return scheduleDriverFailed
	}
	for {
		// Shutdown leaves the in-flight stage's record in `running`. That is the
		// adoptable state, not a leak: the next start finds it and continues the
		// same stage instead of planning a new one.
		if s.ctx.Err() != nil {
			return scheduleDriverStopped
		}
		record, err := scheduleStore.LoadSchedule(scheduleID)
		if err != nil {
			slog.Error("Unable to load schedule", "schedule_id", scheduleID, "error", err)
			return scheduleDriverFailed
		}
		if record.State != store.ScheduleStateRunning {
			slog.Info("Schedule executor stopping", "schedule_id", scheduleID, "state", string(record.State))
			return scheduleDriverStopped
		}
		plan, err := record.Document.Expand()
		if err != nil {
			s.settleSchedule(scheduleID, store.ScheduleStateFailed, fmt.Sprintf("expand schedule: %v", err))
			return scheduleDriverStopped
		}
		recorded, err := scheduleStore.LoadScheduleStages(scheduleID)
		if err != nil {
			slog.Error("Unable to load schedule stages", "schedule_id", scheduleID, "error", err)
			return scheduleDriverFailed
		}
		index, existing, blocked := nextScheduleStage(plan, recorded)
		if blocked != "" {
			s.settleSchedule(scheduleID, store.ScheduleStateFailed, blocked)
			return scheduleDriverStopped
		}
		if index < 0 {
			s.settleSchedule(scheduleID, store.ScheduleStateCompleted, "")
			return scheduleDriverStopped
		}
		// Policy is consulted only for a stage nothing has been recorded for
		// yet. Once a stage has a record it has already been decided, and
		// re-deciding it would let a restart argue with the campaign's own
		// history.
		// A barrier is checked before policy and before the stage is recorded,
		// so a paused campaign leaves no half-decided stage behind: the plan is
		// untouched and the next resume starts exactly here.
		if existing == nil && plan[index].PauseBefore && index > record.ReleasedThroughStage {
			s.settleSchedule(scheduleID, store.ScheduleStatePaused,
				fmt.Sprintf("paused at the barrier before stage %d (%s, %d circles); resume to continue",
					index, plan[index].Kind, plan[index].Circles))
			return scheduleDriverStopped
		}
		if existing == nil {
			verdict := app.EvaluateScheduleStage(plan, index, scheduleStageOutcomes(recorded))
			if !verdict.Run {
				if err := s.recordSkippedStage(scheduleStore, record.ScheduleID, plan[index], verdict.Reason); err != nil {
					s.settleSchedule(scheduleID, store.ScheduleStateFailed, err.Error())
					return scheduleDriverStopped
				}
				slog.Info("Schedule stage skipped by policy", "schedule_id", scheduleID,
					"stage", index, "kind", string(plan[index].Kind), "reason", verdict.Reason)
				continue
			}
		}
		outcome, err := s.runScheduleStage(scheduleStore, record, plan, recorded, index, existing)
		if err != nil {
			slog.Error("Schedule stage did not run", "schedule_id", scheduleID, "stage", index, "error", err)
			s.settleSchedule(scheduleID, store.ScheduleStateFailed, err.Error())
			return scheduleDriverStopped
		}
		switch outcome {
		case store.ScheduleStateCompleted:
			// Loop: the records now say this stage is done.
		case store.ScheduleStateFailed:
			s.settleSchedule(scheduleID, store.ScheduleStateFailed, fmt.Sprintf("stage %d failed", index))
			return scheduleDriverStopped
		default:
			// Cancelled, or the server is going down mid-stage. Either way the
			// records already say what happened; do not overwrite an operator's
			// pause or cancel with a verdict of our own.
			return scheduleDriverStopped
		}
	}
}

// scheduleStageOutcomes projects the persisted stage records onto the narrow
// view policy is allowed to read. It is the only bridge between the store's
// lifecycle states and the application's outcome states, and it exists so the
// decision itself stays a pure function over plain values.
func scheduleStageOutcomes(recorded []store.ScheduleStageRecord) []app.ScheduleStageOutcome {
	outcomes := make([]app.ScheduleStageOutcome, 0, len(recorded))
	for _, stage := range recorded {
		state := app.ScheduleOutcomePending
		switch stage.State {
		case store.ScheduleStateCompleted:
			state = app.ScheduleOutcomeCompleted
		case store.ScheduleStateSkipped:
			state = app.ScheduleOutcomeSkipped
		}
		// Only a completed stage carries a cost it actually measured. A pending,
		// skipped or failed record's zero is the absence of a number, and a
		// non-finite cost is a job that never produced one; a completed stage's
		// zero is a perfect fit and must reach policy as the measurement it is.
		measured := state == app.ScheduleOutcomeCompleted &&
			!math.IsNaN(stage.BestCost) && !math.IsInf(stage.BestCost, 0)
		outcomes = append(outcomes, app.ScheduleStageOutcome{
			Index:        stage.Index,
			Kind:         stage.Kind,
			State:        state,
			BestCost:     stage.BestCost,
			CostMeasured: measured,
		})
	}
	return outcomes
}

// recordSkippedStage writes the declined stage before the campaign moves past
// it, so the records — the only progress there is — account for every planned
// stage rather than leaving a hole the next reader has to explain.
func (s *Server) recordSkippedStage(scheduleStore store.ScheduleStore, scheduleID string, stage app.ScheduleStage, reason string) error {
	stageRecord := store.NewScheduleStageRecord(scheduleID, stage)
	stageRecord.State = store.ScheduleStateSkipped
	stageRecord.Reason = reason
	if err := scheduleStore.SaveScheduleStage(scheduleID, stageRecord); err != nil {
		return fmt.Errorf("record skipped stage %d: %w", stage.Index, err)
	}
	s.publishScheduleChanged(scheduleID)
	return nil
}

// nextScheduleStage derives the cursor from the records alone: the first
// planned stage that is not recorded as completed. It returns the existing
// record for that stage when there is one, which is the stage to adopt rather
// than restart from nothing.
//
// A recorded stage that failed or was cancelled blocks the campaign with a
// reason, because continuing past it would silently skip work the plan asked
// for.
func nextScheduleStage(plan []app.ScheduleStage, recorded []store.ScheduleStageRecord) (int, *store.ScheduleStageRecord, string) {
	byIndex := make(map[int]*store.ScheduleStageRecord, len(recorded))
	for i := range recorded {
		byIndex[recorded[i].Index] = &recorded[i]
	}
	for index := range plan {
		record, ok := byIndex[index]
		if !ok {
			return index, nil, ""
		}
		switch record.State {
		case store.ScheduleStateCompleted:
			continue
		case store.ScheduleStateSkipped:
			// Policy already declined this stage and said so on disk. The
			// decision is not revisited, which is what keeps the loop from
			// re-deciding the same stage forever.
			continue
		case store.ScheduleStateFailed, store.ScheduleStateCancelled:
			return -1, nil, fmt.Sprintf("stage %d is %s", index, record.State)
		default:
			return index, record, ""
		}
	}
	return -1, nil, ""
}

// runScheduleStage records, starts, and awaits one stage, returning the state
// the stage settled in.
//
// The ordering here is the whole crash-safety argument, so it is spelled out:
//
//  1. The job identifier is minted before anything durable is written.
//  2. The stage record is written as `running`, naming that identifier, before
//     the job exists in the manager and therefore before it can be enqueued or
//     started.
//  3. Only then is the job created under that same identifier and queued.
//
// At every point where the process can die, a restart sees a consistent world.
// Before (2) there is no record and no job. Between (2) and (3) there is a
// `running` record naming a job that was never created, which the next start
// adopts by index and re-runs under the same identifier. During (3) there is a
// `running` record naming the job that is actually running, which the next
// start also adopts. There is no window in which a job runs that no record
// names, which is the orphan fork this design exists to prevent.
//
// Adopting is not the same as rerunning. The restored job under the record's
// identifier is inspected first: a completed one settles the stage where it
// stands, and only an interrupted attempt is discarded and run again.
func (s *Server) runScheduleStage(
	scheduleStore store.ScheduleStore,
	record *store.ScheduleRecord,
	plan []app.ScheduleStage,
	recorded []store.ScheduleStageRecord,
	index int,
	existing *store.ScheduleStageRecord,
) (store.ScheduleState, error) {
	stage := plan[index]

	parentJobID := ""
	parentIndex := -1
	if index > 0 {
		previous := lastRunStageRecord(recorded, index)
		if previous == nil || previous.State != store.ScheduleStateCompleted || previous.JobID == "" {
			return "", fmt.Errorf("stage %d has no completed predecessor", index)
		}
		parentJobID = previous.JobID
		parentIndex = previous.Index
	}

	// An adopted stage keeps the identifier its record already names, so the
	// stage and its job stay one-to-one across any number of restarts.
	jobID := uuid.New().String()
	if existing != nil && existing.JobID != "" {
		jobID = existing.JobID
		settled, adopted, err := s.settleAdoptedStage(scheduleStore, record.ScheduleID, index, existing)
		if err != nil {
			return "", err
		}
		if adopted {
			return settled, nil
		}
		s.discardStageAttempt(jobID)
	}

	config, source, err := s.scheduleStageConfig(stage, plan, parentJobID, parentIndex)
	if err != nil {
		return "", err
	}

	// Resolving the configuration reads the store and the filesystem, so an
	// operator's pause or cancel can land while it happens. It is durable before
	// it is acted on, so it is re-read here, as late as it can be: everything
	// above this point is preparation that can simply be dropped, and below it a
	// job exists. Without this, a paused campaign would still start one more
	// stage, and a cancel would find a record naming a job that does not exist
	// yet and cancel nothing.
	if state, err := scheduleStateNow(scheduleStore, record.ScheduleID); err == nil && state != store.ScheduleStateRunning {
		slog.Info("Schedule stage not started; the campaign is no longer running",
			"schedule_id", record.ScheduleID, "stage", index, "state", string(state))
		// The same "stop without a verdict of our own" signal shutdown uses.
		return store.ScheduleStateRunning, nil
	}

	stageRecord := store.NewScheduleStageRecord(record.ScheduleID, stage)
	stageRecord.Config = config
	stageRecord.State = store.ScheduleStateRunning
	stageRecord.JobID = jobID
	stageRecord.ParentJobID = parentJobID
	startedAt := time.Now().UTC()
	stageRecord.StartedAt = &startedAt
	if err := scheduleStore.SaveScheduleStage(record.ScheduleID, stageRecord); err != nil {
		return "", fmt.Errorf("record stage %d: %w", index, err)
	}
	s.publishScheduleChanged(record.ScheduleID)

	if err := s.startScheduleStageJob(jobID, config, source, stage, record.ScheduleID, parentJobID); err != nil {
		stageRecord.State = store.ScheduleStateFailed
		stageRecord.Error = err.Error()
		if saveErr := scheduleStore.SaveScheduleStage(record.ScheduleID, stageRecord); saveErr != nil {
			slog.Error("Unable to record a stage that could not start",
				"schedule_id", record.ScheduleID, "stage", index, "error", saveErr)
		}
		s.publishScheduleChanged(record.ScheduleID)
		return store.ScheduleStateFailed, nil
	}

	// A cancel that landed in the window above found a record naming a job that
	// did not exist yet, so requestCancellation had nothing to cancel. The job
	// exists now, so the durable intent is replayed against it rather than
	// letting the stage run for hours after the campaign was cancelled.
	if state, err := scheduleStateNow(scheduleStore, record.ScheduleID); err == nil && state == store.ScheduleStateCancelled {
		if err := s.requestCancellation(jobID); err != nil {
			slog.Debug("Replayed cancel found the stage job already settling",
				"schedule_id", record.ScheduleID, "job_id", jobID, "error", err)
		}
	}

	job, settled := s.awaitJobTermination(jobID)
	if !settled {
		// The server is shutting down. The record stays `running` on purpose.
		return store.ScheduleStateRunning, nil
	}

	completedAt := time.Now().UTC()
	stageRecord.CompletedAt = &completedAt
	stageRecord.BestCost = job.BestCost
	stageRecord.Iterations = job.Iterations
	stageRecord.Evaluations = int64(job.Evaluations)
	switch job.State {
	case StateCompleted:
		stageRecord.State = store.ScheduleStateCompleted
	case StateCancelled:
		stageRecord.State = store.ScheduleStateCancelled
	default:
		stageRecord.State = store.ScheduleStateFailed
		stageRecord.Error = job.Error
	}
	if err := scheduleStore.SaveScheduleStage(record.ScheduleID, stageRecord); err != nil {
		return "", fmt.Errorf("record stage %d outcome: %w", index, err)
	}
	s.publishScheduleChanged(record.ScheduleID)
	slog.Info("Schedule stage settled", "schedule_id", record.ScheduleID, "stage", index,
		"kind", string(stage.Kind), "job_id", jobID, "state", string(stageRecord.State), "best_cost", job.BestCost)
	return stageRecord.State, nil
}

// scheduleStageConfig builds the configuration a stage runs with. The plan owns
// every optimizer field; the parent checkpoint owns only what continuity
// requires — the resume count, the effective seed, and the resolved image
// paths.
//
// The base stage has no parent and returns a nil source. parentIndex is the
// planned index of the stage the parent job realized, which is the stage before
// this one unless policy skipped some in between.
func (s *Server) scheduleStageConfig(stage app.ScheduleStage, plan []app.ScheduleStage, parentJobID string, parentIndex int) (JobConfig, *continuationSource, error) {
	config := stage.Config
	s.applyDefaultBackend(&config)
	if parentJobID == "" {
		if failure := s.resolveConfigPaths(&config, "schedule"); failure != nil {
			return config, nil, failure
		}
		normalized, err := app.Normalize(config)
		if err != nil {
			return config, nil, fmt.Errorf("stage %d configuration: %w", stage.Index, err)
		}
		return normalized, nil, nil
	}

	kind := extendContinuation
	if stage.Kind == app.ScheduleStagePolish {
		kind = polishContinuation
	}
	source, failure := s.continuationSourceFor(parentJobID, kind)
	if failure != nil {
		return config, nil, fmt.Errorf("stage %d cannot continue job %s: %s", stage.Index, parentJobID, failure.message)
	}
	// The chain must still be the chain the plan describes. A parent holding a
	// different circle count than the plan predicted means the campaign diverged
	// from its document, and running on would append to the wrong canvas.
	if expected := plan[parentIndex].Circles; source.config.Circles != expected {
		return config, nil, fmt.Errorf("stage %d expected a %d circle parent, found %d",
			stage.Index, expected, source.config.Circles)
	}
	config.RefPath = source.config.RefPath
	config.CanvasPath = source.config.CanvasPath
	config.ResumeCount = source.config.ResumeCount
	config.EffectiveSeed = source.config.EffectiveSeed
	normalized, err := app.Normalize(config)
	if err != nil {
		return config, nil, fmt.Errorf("stage %d configuration: %w", stage.Index, err)
	}
	return normalized, source, nil
}

// startScheduleStageJob creates the stage's job under the identifier the record
// already names and puts it in the shared queue, waiting for room rather than
// abandoning a campaign because manual jobs are momentarily using the host.
func (s *Server) startScheduleStageJob(jobID string, config JobConfig, source *continuationSource, stage app.ScheduleStage, scheduleID, parentJobID string) error {
	index := stage.Index
	// The lineage is set here rather than left to the stage record, so a job the
	// schedule created carries the same extendedFrom/polishedFrom onto its
	// checkpoint that a hand-issued continuation would.
	lineage := func(job *Job) {
		job.ScheduleID = scheduleID
		// The index is a pointer so stage zero survives a JSON round trip; each
		// job needs its own copy rather than a share of the loop's variable.
		stageIndex := index
		job.StageIndex = &stageIndex
		switch stage.Kind {
		case app.ScheduleStageExtend:
			job.ExtendedFrom = parentJobID
		case app.ScheduleStagePolish:
			job.PolishedFrom = parentJobID
		}
	}
	deadline := time.Now().Add(scheduleEnqueueTimeout)
	for {
		var failure *continuationError
		if source != nil {
			_, failure = s.startContinuation(jobID, app.DefaultProject, config, source, lineage)
		} else {
			failure = s.startBaseStageJob(jobID, config, lineage)
		}
		if failure == nil {
			return nil
		}
		if failure.code != "queue_full" || time.Now().After(deadline) {
			return errors.New(failure.message)
		}
		// startContinuation failed the job it created, so the identifier is free
		// again only after the manager forgets it.
		s.discardStageAttempt(jobID)
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(scheduleStagePollInterval):
		}
	}
}

// startBaseStageJob creates and enqueues the campaign's first job, which
// continues nothing and so cannot go through startContinuation.
func (s *Server) startBaseStageJob(jobID string, config JobConfig, lineage func(*Job)) *continuationError {
	job, err := s.jobManager.CreateJobWithID(jobID, app.DefaultProject, config)
	if err != nil {
		return continuationFailure(http.StatusInternalServerError, "job_error", fmt.Sprintf("failed to create base stage job: %v", err))
	}
	if err := s.jobManager.UpdateJob(job.ID, lineage); err != nil {
		return continuationFailure(http.StatusInternalServerError, "job_error", "failed to initialize base stage job")
	}
	if initialized, ok := s.jobManager.GetJob(job.ID); ok {
		s.jobManager.broadcaster.Broadcast(jobProgressSnapshot(initialized))
		s.publishScheduleChanged(initialized.ScheduleID)
	}
	if err := s.enqueueJob(job.ID); err != nil {
		_ = s.jobManager.FailJob(job.ID, "server job queue is full")
		return continuationFailure(http.StatusTooManyRequests, "queue_full", "server job queue is full")
	}
	return nil
}

// scheduleStateNow reads the campaign's durable state, which is where the
// operator's intent lives.
func scheduleStateNow(scheduleStore store.ScheduleStore, scheduleID string) (store.ScheduleState, error) {
	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		return "", err
	}
	return record.State, nil
}

// settleAdoptedStage finishes a stage whose job had already run to completion
// when the process stopped, instead of rerunning it.
//
// Jobs are restored from their checkpoints before schedules are adopted, so the
// crash window between "the job wrote its terminal checkpoint" and "the driver
// recorded the stage outcome" shows up here as a completed job under exactly
// the identifier the `running` record names. Discarding that attempt would
// delete a finished checkpoint and repeat the whole stage, which is the
// duplicate work the one-stage-one-job design exists to rule out.
//
// Only a completed job is adopted. A failed or cancelled attempt is a partial
// result that nothing downstream can continue from, so it is discarded and rerun
// as before.
func (s *Server) settleAdoptedStage(
	scheduleStore store.ScheduleStore,
	scheduleID string,
	index int,
	existing *store.ScheduleStageRecord,
) (store.ScheduleState, bool, error) {
	job, ok := s.jobManager.GetJob(existing.JobID)
	if !ok || job.State != StateCompleted {
		return "", false, nil
	}
	// The completed job has to be this stage's job and no other. A checkpoint
	// carrying a different schedule or index under the same identifier is not
	// something to settle a campaign from.
	if job.ScheduleID != scheduleID || job.StageIndex == nil || *job.StageIndex != index {
		return "", false, nil
	}

	settled := *existing
	settled.State = store.ScheduleStateCompleted
	settled.Error = ""
	settled.BestCost = job.BestCost
	settled.Iterations = job.Iterations
	settled.Evaluations = int64(job.Evaluations)
	completedAt := time.Now().UTC()
	if job.EndTime != nil {
		completedAt = job.EndTime.UTC()
	}
	settled.CompletedAt = &completedAt
	if err := scheduleStore.SaveScheduleStage(scheduleID, &settled); err != nil {
		// Reporting the failure keeps the completed checkpoint: the alternative
		// path from here discards it, and a store that cannot be written is no
		// reason to throw away finished work.
		return "", false, fmt.Errorf("record adopted stage %d: %w", index, err)
	}
	s.publishScheduleChanged(scheduleID)
	slog.Info("Adopted a stage that had already completed", "schedule_id", scheduleID, "stage", index,
		"job_id", existing.JobID, "best_cost", job.BestCost)
	return store.ScheduleStateCompleted, true, nil
}

// discardStageAttempt forgets an interrupted attempt at a stage so the stage's
// identifier can be reused.
//
// Reuse is what keeps a stage and its job one-to-one, which in turn is what
// makes "did this stage run twice?" a question the records can answer. The
// discarded checkpoint is deliberate: it is a partial result, and neither the
// extend nor the polish path can continue from anything but a completed batch
// checkpoint, so nothing downstream could have used it.
func (s *Server) discardStageAttempt(jobID string) {
	jobStore, err := s.storeForJob(jobID)
	if err != nil {
		jobStore = s.store
	}
	if err := s.jobManager.DeleteJob(jobID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		slog.Debug("No restored job to discard for an adopted stage", "job_id", jobID, "error", err)
	}
	s.jobManager.broadcaster.CleanupJob(jobID)
	if jobStore != nil {
		if err := jobStore.DeleteCheckpoint(jobID); err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Warn("Unable to discard an interrupted stage's checkpoint", "job_id", jobID, "error", err)
		}
	}
}

// awaitJobTermination blocks until a job reaches a terminal state, reporting
// false when the server shuts down first.
func (s *Server) awaitJobTermination(jobID string) (*Job, bool) {
	ticker := time.NewTicker(scheduleStagePollInterval)
	defer ticker.Stop()
	for {
		job, ok := s.jobManager.GetJob(jobID)
		if !ok {
			return nil, false
		}
		switch job.State {
		case StateCancelled:
			// Shutdown cancels every running job. That is the server stopping,
			// not a verdict on the stage, so it must leave the record `running`
			// and adoptable rather than recording a cancellation the operator
			// never asked for.
			if s.ctx.Err() != nil {
				return nil, false
			}
			return job, true
		case StateCompleted, StateFailed:
			return job, true
		}
		select {
		case <-s.ctx.Done():
			return nil, false
		case <-ticker.C:
		}
	}
}

// settleSchedule moves a running schedule to a state it stops in — completed,
// failed, or paused at a barrier — and records why. It is a no-op on a schedule
// that is no longer running, so an operator's own pause or cancel is never
// overwritten by a verdict the driver reached at the same moment.
func (s *Server) settleSchedule(scheduleID string, state store.ScheduleState, reason string) {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		return
	}
	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		slog.Error("Unable to load schedule to settle it", "schedule_id", scheduleID, "error", err)
		return
	}
	if record.State != store.ScheduleStateRunning {
		return
	}
	record.State = state
	record.Error = reason
	if err := scheduleStore.SaveSchedule(record); err != nil {
		slog.Error("Unable to record schedule outcome", "schedule_id", scheduleID, "state", string(state), "error", err)
		return
	}
	s.publishScheduleChanged(scheduleID)
	slog.Info("Schedule settled", "schedule_id", scheduleID, "state", string(state), "reason", reason)
}

func (s *Server) publishScheduleChanged(scheduleID string) {
	if s.uiEvents != nil && scheduleID != "" {
		s.uiEvents.PublishCampaignChanged("schedule", scheduleID)
	}
}

func (s *Server) publishChainsChanged() {
	if s.uiEvents != nil {
		s.uiEvents.PublishCampaignChanged("chain", "")
	}
}

// cancelScheduleStage cancels whichever stage of a schedule is in flight. It is
// best effort by nature: the stage may settle on its own between the read and
// the cancel, which is not an error.
func (s *Server) cancelScheduleStage(scheduleID string) {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		return
	}
	stages, err := scheduleStore.LoadScheduleStages(scheduleID)
	if err != nil {
		slog.Warn("Unable to load stages to cancel a schedule", "schedule_id", scheduleID, "error", err)
		return
	}
	for _, stage := range stages {
		if stage.State != store.ScheduleStateRunning || stage.JobID == "" {
			continue
		}
		if err := s.requestCancellation(stage.JobID); err != nil {
			slog.Debug("In-flight stage was already settled", "schedule_id", scheduleID, "job_id", stage.JobID, "error", err)
		}
	}
}

// lastRunStageRecord returns the record of the newest stage before index that
// policy did not skip. A skipped stage produced no checkpoint, so the chain
// continues from the last stage that actually ran; because only polish stages
// may be skipped, and a polish leaves the circle count alone, that parent still
// holds exactly the canvas the plan predicted.
func lastRunStageRecord(records []store.ScheduleStageRecord, index int) *store.ScheduleStageRecord {
	var found *store.ScheduleStageRecord
	for i := range records {
		if records[i].Index >= index || records[i].State == store.ScheduleStateSkipped {
			continue
		}
		if found == nil || records[i].Index > found.Index {
			found = &records[i]
		}
	}
	return found
}
