package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/store"
)

// continuationKind names a continuation in the messages its preconditions
// produce. The wording is the wording the extend and polish endpoints already
// answered with, kept verbatim so factoring the checks out of the handlers is
// not observable to a client.
type continuationKind struct {
	// stateReason answers a source job that has not completed.
	stateReason string
	// requirement answers a checkpoint that is not a complete batch result.
	requirement string
}

var (
	extendContinuation = continuationKind{
		stateReason: "only completed jobs can be extended",
		requirement: "extension requires a complete batch checkpoint",
	}
	polishContinuation = continuationKind{
		stateReason: "only completed jobs can be polished",
		requirement: "polishing requires a complete batch checkpoint",
	}
)

// continuationSource is the validated starting point every continuation shares:
// a complete batch checkpoint, or a refill-limited one rebased to its actual
// size, plus the configuration derived from it with the resume count advanced,
// the effective seed carried forward, and every path resolved against the input
// policy.
//
// It exists so the schedule executor continues a stage through exactly the code
// the extend and polish endpoints use. A second, parallel implementation of
// these preconditions is precisely how a scheduled stage would come to run with
// a configuration a hand-issued request would have refused.
type continuationSource struct {
	checkpoint *store.Checkpoint
	// config is the parent's configuration, normalized and resolved. Callers
	// overlay their own overrides on top of it.
	config app.JobConfig
	// evaluations is the parent's evaluation count narrowed to int, which is
	// what the job manager counts in.
	evaluations int
}

// continuationError carries the HTTP answer for a failed precondition. The
// handlers write it; the executor logs it and fails the stage, so both report
// the same reason for the same refusal.
type continuationError struct {
	status  int
	code    string
	message string
}

func (e *continuationError) Error() string { return e.message }

func continuationFailure(status int, code, message string) *continuationError {
	return &continuationError{status: status, code: code, message: message}
}

// continuationConfig derives the configuration a continuation starts from: the
// parent's, normalized, and rebased onto the arrangement the parent actually
// materialized.
//
// A refill-limited stage is a valid checkpoint of what it did place, so a
// continuation inherits that actual size; otherwise another +N request would
// retain the gap and strand the chain again.
func continuationConfig(checkpoint *store.Checkpoint, kind continuationKind) (app.JobConfig, *continuationError) {
	config, err := app.Normalize(checkpoint.Config)
	if err != nil || config.Mode != app.ModeBatch {
		return config, continuationFailure(http.StatusBadRequest, "invalid_checkpoint", kind.requirement)
	}

	actualCircles := checkpoint.ActualCircles
	complete := actualCircles == config.Circles

	continuableShortStage := checkpoint.Termination == string(renderer.TerminationRefillLimit) &&
		actualCircles < config.Circles
	if len(checkpoint.BestParams) != actualCircles*app.ParamsPerCircle || (!complete && !continuableShortStage) {
		return config, continuationFailure(http.StatusBadRequest, "invalid_checkpoint", kind.requirement)
	}

	config.Circles = actualCircles
	config.BatchSize = min(config.BatchSize, actualCircles)
	config.PolishingActiveSetSize = min(config.PolishingActiveSetSize, actualCircles)

	return config, nil
}

// continuableCheckpoint loads the parent's completed checkpoint and decides
// whether this build may continue it, which is the half of the preconditions
// that is about the checkpoint rather than about the request.
//
// The version guard runs here for the same reason an explicit resume runs it. A
// continuation is not a fresh search: it is seeded from the parent's best
// parameters and, for an extend, freezes them as a prefix it never revisits. So
// the cost it starts from was produced by whatever libraries the parent linked,
// and continuing it under a behaviour-changing upgrade would write a checkpoint
// naming the new version as though the whole run were comparable.
func (s *Server) continuableCheckpoint(jobID string, allowMismatch bool) (*store.Checkpoint, *continuationError) {
	jobStore, err := s.storeForJob(jobID)
	if err != nil {
		slog.Error("Failed to resolve project store for continuation", "job_id", jobID, "error", err)
		return nil, continuationFailure(http.StatusInternalServerError, "project_unavailable", "the project store is unavailable")
	}

	checkpoint, err := jobStore.LoadCheckpoint(jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, continuationFailure(http.StatusNotFound, "not_found", "completed checkpoint not found")
		}

		return nil, continuationFailure(http.StatusInternalServerError, "checkpoint_error", "failed to load completed checkpoint")
	}

	err = checkpoint.Validate()
	if err != nil {
		return nil, continuationFailure(http.StatusBadRequest, "invalid_checkpoint", "completed checkpoint is invalid")
	}

	warnings, err := s.guardCheckpointVersions(checkpoint, allowMismatch)
	if err != nil {
		// 409 rather than 400: the request is well formed and the checkpoint is
		// valid; it is this build that cannot continue it comparably. That is
		// the status an explicit resume already answers the same refusal with.
		return nil, continuationFailure(http.StatusConflict, "optimizer_version_mismatch", err.Error())
	}

	for _, warning := range warnings {
		slog.Warn("Optimizer version check", "job_id", jobID, "warning", warning)
	}

	return checkpoint, nil
}

// continuationSourceFor validates that jobID can be continued and returns the
// checkpoint and configuration a continuation starts from.
//
// allowMismatch carries the caller's override for the optimizer version guard,
// which every continuation runs for the same reason an explicit resume does. A
// continuation is not a fresh search: it is seeded from the parent's best
// parameters and, for an extend, freezes them as a prefix it never revisits. So
// the recorded cost it starts from was produced by whatever libraries the
// parent linked, and continuing it under a behaviour-changing upgrade writes a
// checkpoint that names the new version as though the whole run were
// comparable. Guarding here rather than in each handler is what makes a
// scheduled stage refuse exactly what a hand-issued request would.
func (s *Server) continuationSourceFor(
	jobID string, kind continuationKind, allowMismatch bool,
) (*continuationSource, *continuationError) {
	source, ok := s.jobManager.GetJob(jobID)
	if !ok {
		return nil, continuationFailure(http.StatusNotFound, "not_found", "job not found")
	}

	if source.State != StateCompleted {
		return nil, continuationFailure(http.StatusConflict, "invalid_state", kind.stateReason)
	}

	checkpoint, failure := s.continuableCheckpoint(jobID, allowMismatch)
	if failure != nil {
		return nil, failure
	}

	config, failure := continuationConfig(checkpoint, kind)
	if failure != nil {
		return nil, failure
	}

	evaluations := int(checkpoint.Evaluations)
	if int64(evaluations) != checkpoint.Evaluations {
		return nil, continuationFailure(http.StatusBadRequest, "invalid_checkpoint", "checkpoint evaluation count is out of range")
	}

	failure = s.resolveConfigPaths(&config, "checkpoint")
	if failure != nil {
		return nil, failure
	}

	config.EffectiveSeed = checkpoint.EffectiveSeed
	config.ResumeCount = checkpoint.ResumeCount + 1

	return &continuationSource{checkpoint: checkpoint, config: config, evaluations: evaluations}, nil
}

// resolveConfigPaths rewrites a configuration's image paths to the policy's
// resolved form, refusing anything outside the configured input roots.
func (s *Server) resolveConfigPaths(config *app.JobConfig, origin string) *continuationError {
	if s.inputErr != nil {
		return continuationFailure(http.StatusInternalServerError, "server_config", "server input roots are unavailable")
	}

	code := "invalid_" + origin

	resolved, err := s.input.resolveImage(config.RefPath)
	if err != nil {
		return continuationFailure(http.StatusBadRequest, code, origin+" reference is outside configured input roots")
	}

	config.RefPath = resolved
	if config.CanvasPath == "" {
		return nil
	}

	resolved, err = s.input.resolveImage(config.CanvasPath)
	if err != nil {
		return continuationFailure(http.StatusBadRequest, code, origin+" canvas is outside configured input roots")
	}

	config.CanvasPath = resolved

	return nil
}

// startContinuation creates, seeds from the parent result, and enqueues a
// continuation job.
//
// jobID may be empty to mint one, or an identifier the caller already holds.
// The second form exists for the schedule executor: a stage must name its job
// in the durable stage record before that job can exist, because a job no
// record names is exactly the orphan fork this phase prevents.
func (s *Server) startContinuation(jobID string, project app.Project, config JobConfig, src *continuationSource, lineage func(*Job)) (*Job, *continuationError) {
	job, err := s.jobManager.CreateJobWithID(jobID, project, config)
	if err != nil {
		slog.Error("Failed to create continuation job", "job_id", jobID, "error", err)
		return nil, continuationFailure(http.StatusInternalServerError, "job_error", "failed to initialize continuation job")
	}

	err = s.jobManager.UpdateJob(job.ID, func(live *Job) {
		updateBestResult(live, src.checkpoint.BestParams, src.checkpoint.BestCost)
		live.InitialCost = src.checkpoint.InitialCost
		live.Iterations = src.checkpoint.Iteration

		live.Evaluations = src.evaluations
		// The parent's secondary library travels with the parameters it
		// produced, so the continuation's own checkpoint can keep naming it.
		live.InheritedPolishingVersion = src.checkpoint.PolishingOptimizerVersion
		if lineage != nil {
			lineage(live)
		}
	})
	if err != nil {
		return nil, continuationFailure(http.StatusInternalServerError, "job_error", "failed to initialize continuation job")
	}

	if initialized, ok := s.jobManager.GetJob(job.ID); ok {
		s.jobManager.broadcaster.Broadcast(jobProgressSnapshot(initialized))

		if initialized.ScheduleID != "" {
			s.publishScheduleChanged(initialized.ScheduleID)
		} else if initialized.ExtendedFrom != "" || initialized.PolishedFrom != "" {
			s.publishChainsChanged()
		}
	}

	err = s.enqueueJob(job.ID)
	if err != nil {
		_ = s.jobManager.FailJob(job.ID, "server job queue is full")
		return nil, continuationFailure(http.StatusTooManyRequests, "queue_full", "server job queue is full")
	}

	return job, nil
}

// writeContinuationError answers a failed precondition in the shared error
// shape.
func writeContinuationError(w http.ResponseWriter, failure *continuationError) {
	writeAPIError(w, failure.status, failure.code, failure.message)
}

// requireCheckpointStore reports the store a continuation needs, or the answer
// for a server running without checkpointing.
func (s *Server) requireCheckpointStore() *continuationError {
	if s.store == nil {
		return continuationFailure(http.StatusServiceUnavailable, "checkpoint_unavailable", "checkpoint feature not enabled")
	}

	return nil
}

// scheduleStore reports the schedule persistence API, which is optional in the
// same way ArtifactStore is: a checkpoint-only store keeps working, it just
// cannot hold schedules.
func (s *Server) scheduleStore() (store.ScheduleStore, error) {
	if s.store == nil {
		return nil, errors.New("checkpoint feature not enabled")
	}

	scheduleStore, ok := s.store.(store.ScheduleStore)
	if !ok {
		return nil, errors.New("the configured store does not persist schedules")
	}

	return scheduleStore, nil
}
