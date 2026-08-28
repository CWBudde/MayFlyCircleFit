package server

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/google/uuid"
)

// JobState represents the current state of a job.
type JobState string

const (
	StatePending   JobState = "pending"
	StateRunning   JobState = "running"
	StatePaused    JobState = "paused"
	StateCompleted JobState = "completed"
	StateFailed    JobState = "failed"
	StateCancelled JobState = "cancelled"
)

// JobConfig is an alias to avoid duplication with store.JobConfig.
type JobConfig = store.JobConfig

// Job represents an optimization job.
type Job struct {
	ID      string      `json:"id"`
	Project app.Project `json:"project"`
	State   JobState    `json:"state"`
	Config  JobConfig   `json:"config"`
	// RequestedCircles is the target captured in Config.Circles, while
	// ActualCircles is the number currently materialized in BestParams. They are
	// explicit on the resource because a refill-limited batch may legitimately
	// finish short of its target.
	RequestedCircles int       `json:"requestedCircles"`
	ActualCircles    int       `json:"actualCircles"`
	BestParams       []float64 `json:"bestParams,omitempty"`
	BestCost         float64   `json:"bestCost"`
	BestRevision     uint64    `json:"-"`
	CandidateCost    *float64  `json:"candidateCost,omitempty"`
	InitialCost      float64   `json:"initialCost"`
	Iterations       int       `json:"iterations"`
	Evaluations      int       `json:"evaluations"`
	// EvaluationWidth is how many cost evaluations this job's renderer actually
	// ran concurrently, recorded when the renderer was built. It is deliberately
	// not Config.EvaluationWorkers, which is only what was requested: a backend
	// without independent sessions declines the request and runs serially, and a
	// CPU request above GOMAXPROCS is clamped. Reporting the request would claim
	// a concurrency the job never had, and the value exists precisely so two
	// runs can be compared.
	//
	// Zero means serial or not known -- a job restored from a checkpoint has not
	// built a renderer in this process -- and readers must show nothing rather
	// than guess, because the clamp depends on the machine that ran the job.
	EvaluationWidth int `json:"evaluationWidth,omitempty"`
	// EffectiveBackend is the backend this job's renderer was actually built
	// on, recorded at the same point as EvaluationWidth. It differs from
	// Config.Backend when BackendFallback resolved an unavailable backend to
	// the CPU, and it exists for the same reason FastCompositing does: two runs
	// that differ in it are not cost-comparable, so a completed job has to be
	// able to say which one it was.
	//
	// BackendDegraded records that the backend started and then gave up
	// mid-run, which is a different event: the run spent part of its budget on
	// the device before falling back, so its best-so-far spans two arithmetics.
	// The OpenCL renderer degrades permanently once it has degraded at all, so
	// this only ever goes from false to true.
	//
	// Both are empty for a job restored from a checkpoint, which built no
	// renderer in this process. Neither is persisted -- like EvaluationWidth,
	// they describe this process's run, not the configuration that produced it.
	EffectiveBackend app.Backend `json:"effectiveBackend,omitempty"`
	BackendDegraded  bool        `json:"backendDegraded,omitempty"`
	// InheritedEvaluations is what Evaluations already stood at when this job
	// started running. A continuation is seeded from its parent's checkpoint so
	// the campaign total stays readable, but its wall clock starts at this
	// stage; throughput has to divide only the work this stage did. It is not
	// persisted: a job restored from a checkpoint has no clock of its own left
	// to report throughput against.
	InheritedEvaluations int `json:"-"`
	// ExtendedFrom and PolishedFrom name the completed job this one continued
	// from, and at most one is ever set. They are persisted onto the job's
	// checkpoint, so the chain a campaign builds is readable from the job tree
	// after a restart instead of only from the HTTP response that created it.
	ExtendedFrom string `json:"extendedFrom,omitempty"`
	PolishedFrom string `json:"polishedFrom,omitempty"`
	// ScheduleID and StageIndex place the job in a declarative schedule.
	// StageIndex is a pointer, and matches the checkpoint field it is persisted
	// to, because stage zero is a real stage: with a plain int and omitempty the
	// base stage of a campaign would serialize a schedule with no index at all.
	ScheduleID    string         `json:"scheduleId,omitempty"`
	StageIndex    *int           `json:"stageIndex,omitempty"`
	Termination   string         `json:"termination,omitempty"`
	StartTime     time.Time      `json:"startTime"`
	EndTime       *time.Time     `json:"endTime,omitempty"`
	Error         string         `json:"error,omitempty"`
	PSNR          *float64       `json:"-"`
	PSNRInfinite  bool           `json:"-"`
	SSIM          *float64       `json:"-"`
	MetricHistory []MetricSample `json:"-"`
}

// MetricSample is a live-history point used by the detail page. The sampling
// cadence and job iteration limit bound this in-memory history.
type MetricSample struct {
	OptimizerDiagnostics *opt.SearchDiagnostics `json:"optimizerDiagnostics,omitempty"`
	Iteration            int                    `json:"iteration"`
	Cost                 float64                `json:"cost"`
	Evaluations          int                    `json:"evaluations"`
	PSNR                 *float64               `json:"psnr"`
	PSNRInfinite         bool                   `json:"psnrInfinite,omitempty"`
	SSIM                 *float64               `json:"ssim,omitempty"`
	CPS                  float64                `json:"cps"`
	Timestamp            time.Time              `json:"timestamp"`
}

// JobSummary is the detached projection used by collection endpoints. A job
// list needs lifecycle and configuration fields, not the parameter vector or
// metric history; copying those per request makes listing O(total optimizer
// history) in both allocations and retained heap.
type JobSummary struct {
	ID               string           `json:"id"`
	Project          app.Project      `json:"project"`
	State            JobState         `json:"state"`
	Config           JobSummaryConfig `json:"config"`
	RequestedCircles int              `json:"requestedCircles"`
	ActualCircles    int              `json:"actualCircles"`
	BestCost         float64          `json:"bestCost"`
	InitialCost      float64          `json:"initialCost"`
	Iterations       int              `json:"iterations"`
	Evaluations      int              `json:"evaluations"`
	EvaluationWidth  int              `json:"evaluationWidth,omitempty"`
	EffectiveBackend app.Backend      `json:"effectiveBackend,omitempty"`
	BackendDegraded  bool             `json:"backendDegraded,omitempty"`
	CandidateCost    *float64         `json:"candidateCost,omitempty"`
	ExtendedFrom     string           `json:"extendedFrom,omitempty"`
	PolishedFrom     string           `json:"polishedFrom,omitempty"`
	ScheduleID       string           `json:"scheduleId,omitempty"`
	StageIndex       *int             `json:"stageIndex,omitempty"`
	Termination      string           `json:"termination,omitempty"`
	StartTime        time.Time        `json:"startTime"`
	EndTime          *time.Time       `json:"endTime,omitempty"`
	Error            string           `json:"error,omitempty"`
}

type JobSummaryConfig struct {
	RefPath string   `json:"refPath"`
	Mode    app.Mode `json:"mode"`
	Circles int      `json:"circles"`
}

var ErrInvalidTransition = errors.New("invalid job state transition")

// errDuplicateJobID reports that restoreJob was handed an ID the manager
// already holds. Job IDs are UUIDs and the manager is keyed by ID alone, so the
// realistic cause is the same checkpoint directory existing under two projects
// — a copied or restored-from-backup `<data-root>/projects/<slug>/jobs/<uuid>`.
// It is a sentinel so restoreProjectJobs can name both projects in the log
// instead of reporting a generic registration failure.
var errDuplicateJobID = errors.New("job already exists")

// duplicateJobError names the project that already owns the ID so the caller
// can report which side of the collision was kept.
type duplicateJobError struct {
	jobID string
	owner app.Project
}

func (e *duplicateJobError) Error() string {
	return fmt.Sprintf("job already exists: %s (owned by project %q)", e.jobID, e.owner)
}

func (e *duplicateJobError) Unwrap() error { return errDuplicateJobID }

// JobManager manages the lifecycle of jobs.
type JobManager struct {
	mu          sync.RWMutex
	jobs        map[string]*Job
	broadcaster *EventBroadcaster
	uiEvents    *UIEventHub
	// onJobSetChanged invalidates read models derived from persisted jobs. It is
	// installed once by Server before jobs are restored or workers start.
	onJobSetChanged func()
}

// NewJobManager creates a new JobManager.
func NewJobManager() *JobManager {
	uiEvents := NewUIEventHub()

	return &JobManager{
		jobs:        make(map[string]*Job),
		broadcaster: NewEventBroadcaster(uiEvents),
		uiEvents:    uiEvents,
	}
}

// CreateJob creates a new job in the given project. The project slug is the
// in-memory mirror of where the job's artifacts live on disk; the directory
// itself stays authoritative, so nothing about it is written into the
// checkpoint.
func (jm *JobManager) CreateJob(project app.Project, config JobConfig) *Job {
	job, err := jm.CreateJobWithID("", project, config)
	if err != nil {
		// Unreachable: a minted UUID is canonical and the manager cannot already
		// hold it. Panicking here would change a long-standing signature's
		// contract, so the impossible branch keeps the original behavior.
		panic(err)
	}

	return job
}

// CreateJobWithID creates a job under an identifier the caller already holds.
// An empty id mints one, which is what CreateJob does.
//
// Supplying the identifier exists for the schedule executor. A stage must name
// its job in the durable stage record before that job can exist, because a
// running job that no record names is exactly the orphan fork Phase 16 is
// designed against. That is only possible if the identifier is chosen before
// the job is.
func (jm *JobManager) CreateJobWithID(id string, project app.Project, config JobConfig) (*Job, error) {
	if id == "" {
		id = uuid.New().String()
	} else if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil || parsed.String() != id {
		return nil, errors.New("job ID must be a canonical non-zero UUID")
	}

	jm.mu.Lock()
	if existing, exists := jm.jobs[id]; exists {
		jm.mu.Unlock()
		return nil, &duplicateJobError{jobID: id, owner: app.NormalizeProject(existing.Project)}
	}

	// The caller keeps its own copy of the configuration, so the authored
	// arrangement is cloned rather than aliased: a slice shared with a caller is
	// live job state that can be written without the manager lock.
	config.InitialCircles = cloneCircleSpecs(config.InitialCircles)
	job := &Job{
		ID:               id,
		Project:          app.NormalizeProject(project),
		State:            StatePending,
		Config:           config,
		RequestedCircles: config.Circles,
		StartTime:        time.Now(),
	}

	jm.jobs[job.ID] = job
	snapshot := cloneJob(job)
	jm.mu.Unlock()

	if jm.onJobSetChanged != nil {
		jm.onJobSetChanged()
	}

	jm.broadcaster.Broadcast(jobProgressSnapshot(snapshot))

	return snapshot, nil
}

// GetJob retrieves a job by ID.
func (jm *JobManager) GetJob(id string) (*Job, bool) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, exists := jm.jobs[id]
	if !exists {
		return nil, false
	}

	return cloneJob(job), true
}

// ListJobs returns all jobs.
func (jm *JobManager) ListJobs() []*Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]*Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, cloneJob(job))
	}

	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].StartTime.Equal(jobs[j].StartTime) {
			return jobs[i].ID < jobs[j].ID
		}

		return jobs[i].StartTime.After(jobs[j].StartTime)
	})

	return jobs
}

// ListJobSummaries returns the list-page/API projection without cloning
// BestParams or MetricHistory.
func (jm *JobManager) ListJobSummaries() []JobSummary {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]JobSummary, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		summary := JobSummary{
			ID: job.ID, Project: app.NormalizeProject(job.Project), State: job.State,
			Config:           JobSummaryConfig{RefPath: job.Config.RefPath, Mode: job.Config.Mode, Circles: job.Config.Circles},
			RequestedCircles: job.RequestedCircles, ActualCircles: job.ActualCircles,
			BestCost: job.BestCost, InitialCost: job.InitialCost,
			Iterations: job.Iterations, Evaluations: job.Evaluations,
			EvaluationWidth: job.EvaluationWidth, EffectiveBackend: job.EffectiveBackend,
			BackendDegraded: job.BackendDegraded, ExtendedFrom: job.ExtendedFrom,
			PolishedFrom: job.PolishedFrom, ScheduleID: job.ScheduleID,
			Termination: job.Termination, StartTime: job.StartTime, Error: job.Error,
		}

		summary.CandidateCost = cloneFloat(job.CandidateCost)
		if job.StageIndex != nil {
			index := *job.StageIndex
			summary.StageIndex = &index
		}

		if job.EndTime != nil {
			end := *job.EndTime
			summary.EndTime = &end
		}

		jobs = append(jobs, summary)
	}

	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].StartTime.Equal(jobs[j].StartTime) {
			return jobs[i].ID < jobs[j].ID
		}

		return jobs[i].StartTime.After(jobs[j].StartTime)
	})

	return jobs
}

// restoreJob adds a terminal job reconstructed from persisted state.
func (jm *JobManager) restoreJob(job *Job) error {
	if job == nil || job.ID == "" {
		return errors.New("invalid restored job")
	}

	if job.State != StateCompleted && job.State != StateFailed && job.State != StateCancelled {
		return fmt.Errorf("cannot restore non-terminal job state %q", job.State)
	}

	if job.StartTime.IsZero() || job.EndTime == nil {
		return errors.New("restored job is missing lifecycle timestamps")
	}

	jm.mu.Lock()
	if existing, exists := jm.jobs[job.ID]; exists {
		jm.mu.Unlock()
		return &duplicateJobError{jobID: job.ID, owner: app.NormalizeProject(existing.Project)}
	}

	jm.jobs[job.ID] = cloneJob(job)
	jm.mu.Unlock()

	if jm.onJobSetChanged != nil {
		jm.onJobSetChanged()
	}

	return nil
}

// UpdateJob atomically updates a job using the provided function.
func (jm *JobManager) UpdateJob(id string, updateFn func(*Job)) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, exists := jm.jobs[id]
	if !exists {
		return fmt.Errorf("job not found: %s", id)
	}

	updateFn(job)

	return nil
}

// StartJob transitions a pending job to running.
func (jm *JobManager) StartJob(id string) error {
	return jm.transition(id, StateRunning, func(job *Job) {
		job.StartTime = time.Now()
		job.InheritedEvaluations = job.Evaluations
	})
}

// UpdateProgress publishes one immutable optimizer snapshot.
func (jm *JobManager) UpdateProgress(id string, iterations, evaluations int, bestParams []float64, bestCost float64) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, ok := jm.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	if job.State != StateRunning {
		return fmt.Errorf("%w: cannot update %s job", ErrInvalidTransition, job.State)
	}

	if iterations < job.Iterations || evaluations < job.Evaluations || math.IsNaN(bestCost) {
		return errors.New("invalid progress snapshot")
	}

	job.Iterations = iterations
	job.Evaluations = evaluations
	updateBestResult(job, bestParams, bestCost)

	return nil
}

// UpdateCandidateProgress records a provisional full-image cost produced by a
// transactional polishing sweep. It deliberately does not update BestParams,
// BestCost, or BestRevision: those remain the audited, checkpoint-safe result.
func (jm *JobManager) UpdateCandidateProgress(id string, iterations, evaluations int, candidateCost float64) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, ok := jm.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	if job.State != StateRunning {
		return fmt.Errorf("%w: cannot update %s job", ErrInvalidTransition, job.State)
	}

	if iterations < job.Iterations || evaluations < job.Evaluations || math.IsNaN(candidateCost) || math.IsInf(candidateCost, 0) {
		return errors.New("invalid candidate progress snapshot")
	}

	job.Iterations = iterations

	job.Evaluations = evaluations
	if candidateCost < job.BestCost && (job.CandidateCost == nil || candidateCost < *job.CandidateCost) {
		job.CandidateCost = cloneFloat(&candidateCost)
	}

	return nil
}

// ClearCandidateProgress removes an in-flight result after its sweep has been
// accepted or rejected by the full-image usefulness audit.
func (jm *JobManager) ClearCandidateProgress(id string) error {
	return jm.UpdateJob(id, func(job *Job) { job.CandidateCost = nil })
}

// updateBestResult records only strict improvements. BestRevision is the
// stable identity used by live clients and image response validators.
func updateBestResult(job *Job, bestParams []float64, bestCost float64) bool {
	if len(bestParams) == 0 || (len(job.BestParams) > 0 && bestCost >= job.BestCost) {
		return false
	}

	job.BestParams = append([]float64(nil), bestParams...)
	job.ActualCircles = len(bestParams) / app.ParamsPerCircle
	job.BestCost = bestCost
	job.BestRevision++

	return true
}

// getJobState reports the current state of a job, or the empty state when the
// job is unknown.
func (jm *JobManager) getJobState(id string) JobState {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[id]
	if !ok {
		return ""
	}

	return job.State
}

func (jm *JobManager) bestSnapshot(id string) (float64, uint64, *float64, bool) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[id]
	if !ok {
		return 0, 0, nil, false
	}

	return job.BestCost, job.BestRevision, cloneFloat(job.CandidateCost), true
}

// RecordMetrics stores the latest quality metrics and one UI-history sample.
// Callers provide immutable pointer values; the manager clones them.
func (jm *JobManager) RecordMetrics(id string, sample MetricSample) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, ok := jm.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	sample.PSNR = cloneFloat(sample.PSNR)
	sample.SSIM = cloneFloat(sample.SSIM)
	job.PSNR = cloneFloat(sample.PSNR)

	job.PSNRInfinite = sample.PSNRInfinite
	if sample.SSIM != nil {
		job.SSIM = cloneFloat(sample.SSIM)
	}

	job.MetricHistory = append(job.MetricHistory, sample)

	return nil
}

// RecordFinalResult stores a job's measured outcome while it is still running.
//
// It is the first half of completion. The second half, MarkJobCompleted, is
// deliberately deferred until the result is durable: `completed` is the state
// every continuation reads as "there is a checkpoint to extend or polish from",
// so publishing it before the checkpoint exists hands out a promise the store
// cannot yet keep.
func (jm *JobManager) RecordFinalResult(id string, iterations, evaluations int, bestParams []float64, bestCost, initialCost float64, termination string) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, exists := jm.jobs[id]
	if !exists {
		return fmt.Errorf("job not found: %s", id)
	}

	if job.State != StateRunning {
		return fmt.Errorf("%w: cannot record a final result for a %s job", ErrInvalidTransition, job.State)
	}

	job.Iterations = iterations
	job.Evaluations = evaluations
	updateBestResult(job, bestParams, bestCost)
	job.InitialCost = initialCost
	job.Termination = termination

	return nil
}

// MarkJobCompleted transitions a running job whose result is already recorded
// and persisted.
func (jm *JobManager) MarkJobCompleted(id string) error {
	return jm.transition(id, StateCompleted, nil)
}

// CompleteJob records the measured final result and transitions to completed in
// one step. Production runs split the two around the checkpoint write; this
// remains for callers that have nothing to persist.
func (jm *JobManager) CompleteJob(id string, iterations, evaluations int, bestParams []float64, bestCost, initialCost float64, termination string) error {
	return jm.transition(id, StateCompleted, func(job *Job) {
		job.Iterations = iterations
		job.Evaluations = evaluations
		updateBestResult(job, bestParams, bestCost)
		job.InitialCost = initialCost
		job.Termination = termination
	})
}

// FailJob records a safe diagnostic and transitions a pending/running job.
func (jm *JobManager) FailJob(id, message string) error {
	return jm.transition(id, StateFailed, func(job *Job) { job.Error = message })
}

// CancelJob transitions a pending/running job to cancelled.
func (jm *JobManager) CancelJob(id string) error {
	return jm.transition(id, StateCancelled, nil)
}

// PauseJob transitions a running job to paused.
func (jm *JobManager) PauseJob(id string) error {
	return jm.transition(id, StatePaused, nil)
}

// claimPause transitions a running job to paused and returns the snapshot the
// caller must checkpoint. Claiming the state and taking the snapshot under one
// lock is what makes the pause safe: a job that published its final result
// first is no longer running, so the claim is refused and no stale snapshot can
// replace the checkpoint a completed job guarantees to its continuations.
func (jm *JobManager) claimPause(id string) (*Job, error) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, ok := jm.jobs[id]
	if !ok {
		return nil, store.ErrNotFound
	}

	if job.State != StateRunning {
		return nil, fmt.Errorf("%w: job is %s", ErrInvalidTransition, job.State)
	}

	job.State = StatePaused

	return cloneJob(job), nil
}

// ResumeJob transitions a paused job to running.
func (jm *JobManager) ResumeJob(id string) error {
	return jm.transition(id, StateRunning, nil)
}

// DeleteJob removes a terminal job. Active jobs must be cancelled first.
func (jm *JobManager) DeleteJob(id string) error {
	jm.mu.Lock()

	job, ok := jm.jobs[id]
	if !ok {
		jm.mu.Unlock()
		return fmt.Errorf("job not found: %s", id)
	}

	if job.State == StatePending || job.State == StateRunning {
		jm.mu.Unlock()
		return fmt.Errorf("%w: cannot delete %s job", ErrInvalidTransition, job.State)
	}

	delete(jm.jobs, id)
	jm.mu.Unlock()

	if jm.onJobSetChanged != nil {
		jm.onJobSetChanged()
	}

	jm.uiEvents.PublishJobDeleted(id)

	return nil
}

func (jm *JobManager) transition(id string, next JobState, update func(*Job)) error {
	jm.mu.Lock()

	job, ok := jm.jobs[id]
	if !ok {
		jm.mu.Unlock()
		return fmt.Errorf("job not found: %s", id)
	}

	if !canTransition(job.State, next) {
		jm.mu.Unlock()
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, job.State, next)
	}

	if update != nil {
		update(job)
	}

	job.State = next
	if next == StateCompleted || next == StateFailed || next == StateCancelled {
		job.CandidateCost = nil
		end := time.Now()
		job.EndTime = &end
	}

	snapshot := cloneJob(job)
	jm.mu.Unlock()

	if (next == StateCompleted || next == StateFailed || next == StateCancelled) && jm.onJobSetChanged != nil {
		jm.onJobSetChanged()
	}

	jm.broadcaster.Broadcast(jobProgressSnapshot(snapshot))

	if next == StateCompleted || next == StateFailed || next == StateCancelled {
		if snapshot.ScheduleID != "" {
			jm.uiEvents.PublishCampaignChanged("schedule", snapshot.ScheduleID)
		} else if snapshot.ExtendedFrom != "" || snapshot.PolishedFrom != "" {
			jm.uiEvents.PublishCampaignChanged("chain", "")
		}
	}

	return nil
}

func canTransition(current, next JobState) bool {
	switch current {
	case StatePending:
		// A pending job cannot be paused: the worker loop only picks up pending
		// work, and resuming reads a checkpoint a job that never ran has not
		// written, so pausing here would strand the job in a state it cannot
		// leave.
		return next == StateRunning || next == StateFailed || next == StateCancelled
	case StateRunning:
		return next == StateCompleted || next == StateFailed || next == StateCancelled || next == StatePaused
	case StatePaused:
		// Completion is deliberately absent. A worker that finishes after the
		// pause was claimed must not publish the job as completed, because the
		// checkpoint on disk is the pause snapshot the operator asked for.
		return next == StateRunning || next == StateFailed || next == StateCancelled
	default:
		return false
	}
}

// GetRunningJobs returns all jobs currently in the running state.
func (jm *JobManager) GetRunningJobs() []*Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	runningJobs := make([]*Job, 0)

	for _, job := range jm.jobs {
		if job.State == StateRunning {
			runningJobs = append(runningJobs, cloneJob(job))
		}
	}

	return runningJobs
}

// jobElapsed is the wall clock a job has been alive: to its end if it reached
// one, otherwise to now.
func jobElapsed(job *Job) time.Duration {
	if job.EndTime != nil {
		return job.EndTime.Sub(job.StartTime)
	}

	return time.Since(job.StartTime)
}

// circlesPerSecond is the throughput every view reports: each evaluation
// rasterized the whole vector, so the circle count is what the optimizer
// actually drew, over the elapsed wall clock. Only the evaluations this job ran
// itself count, because only its own wall clock is being measured.
func circlesPerSecond(job *Job, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}

	evaluations := max(0, job.Evaluations-job.InheritedEvaluations)
	totalCircles := evaluations * max(1, len(job.BestParams)/7)

	return float64(totalCircles) / elapsed.Seconds()
}

// StateCountsWithRunning tallies the jobs by state and clones the running ones,
// both under one read lock. The dashboard prints the counts beside the rows, so
// two snapshots can contradict each other: a job that finishes between them
// leaves a running count with no row explaining it, and a terminal event that
// lands before the page opens its stream never corrects it. Only the running
// jobs are cloned, because a ListJobs snapshot would copy every job's
// parameters and metric history just to produce three integers.
func (jm *JobManager) StateCountsWithRunning() (map[JobState]int, []*Job) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	counts := make(map[JobState]int, len(jm.jobs))
	running := make([]*Job, 0)

	for _, job := range jm.jobs {
		counts[job.State]++
		if job.State == StateRunning {
			running = append(running, cloneJob(job))
		}
	}

	return counts, running
}

// cloneJob returns a fully detached snapshot. Callers may safely retain or
// serialize it while the manager continues updating the live job.
func cloneJob(job *Job) *Job {
	if job == nil {
		return nil
	}

	cloned := *job
	// Config is copied by value, which leaves its one slice field shared. A
	// reader mutating the circles it got from GetJob or ListJobs would otherwise
	// be writing the live job, outside the lock and possibly while the worker is
	// seeding from it.
	cloned.Config.InitialCircles = cloneCircleSpecs(job.Config.InitialCircles)
	cloned.BestParams = append([]float64(nil), job.BestParams...)
	cloned.CandidateCost = cloneFloat(job.CandidateCost)
	cloned.PSNR = cloneFloat(job.PSNR)
	cloned.SSIM = cloneFloat(job.SSIM)

	cloned.MetricHistory = make([]MetricSample, len(job.MetricHistory))
	for i, sample := range job.MetricHistory {
		cloned.MetricHistory[i] = sample
		cloned.MetricHistory[i].PSNR = cloneFloat(sample.PSNR)
		cloned.MetricHistory[i].SSIM = cloneFloat(sample.SSIM)
	}

	if job.EndTime != nil {
		endTime := *job.EndTime
		cloned.EndTime = &endTime
	}

	return &cloned
}

// cloneCircleSpecs copies an authored arrangement, preserving nil so a job that
// was never seeded keeps saying so in JSON.
func cloneCircleSpecs(specs app.CircleSpecs) app.CircleSpecs {
	if specs == nil {
		return nil
	}

	return append(app.CircleSpecs(nil), specs...)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}

	cloned := *value

	return &cloned
}
