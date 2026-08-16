package server

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/google/uuid"
)

// JobState represents the current state of a job
type JobState string

const (
	StatePending   JobState = "pending"
	StateRunning   JobState = "running"
	StateCompleted JobState = "completed"
	StateFailed    JobState = "failed"
	StateCancelled JobState = "cancelled"
)

// JobConfig is an alias to avoid duplication with store.JobConfig
type JobConfig = store.JobConfig

// Job represents an optimization job
type Job struct {
	ID            string      `json:"id"`
	Project       app.Project `json:"project"`
	State         JobState    `json:"state"`
	Config        JobConfig   `json:"config"`
	BestParams    []float64   `json:"bestParams,omitempty"`
	BestCost      float64     `json:"bestCost"`
	BestRevision  uint64      `json:"-"`
	CandidateCost *float64    `json:"candidateCost,omitempty"`
	InitialCost   float64     `json:"initialCost"`
	Iterations    int         `json:"iterations"`
	Evaluations   int         `json:"evaluations"`
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
	Iteration    int       `json:"iteration"`
	Cost         float64   `json:"cost"`
	PSNR         *float64  `json:"psnr"`
	PSNRInfinite bool      `json:"psnrInfinite,omitempty"`
	SSIM         *float64  `json:"ssim,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
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

// JobManager manages the lifecycle of jobs
type JobManager struct {
	mu          sync.RWMutex
	jobs        map[string]*Job
	broadcaster *EventBroadcaster
}

// NewJobManager creates a new JobManager
func NewJobManager() *JobManager {
	return &JobManager{
		jobs:        make(map[string]*Job),
		broadcaster: NewEventBroadcaster(),
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
		return nil, fmt.Errorf("job ID must be a canonical non-zero UUID")
	}

	jm.mu.Lock()
	defer jm.mu.Unlock()
	if existing, exists := jm.jobs[id]; exists {
		return nil, &duplicateJobError{jobID: id, owner: app.NormalizeProject(existing.Project)}
	}

	job := &Job{
		ID:        id,
		Project:   app.NormalizeProject(project),
		State:     StatePending,
		Config:    config,
		StartTime: time.Now(),
	}

	jm.jobs[job.ID] = job
	return cloneJob(job), nil
}

// GetJob retrieves a job by ID
func (jm *JobManager) GetJob(id string) (*Job, bool) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, exists := jm.jobs[id]
	if !exists {
		return nil, false
	}
	return cloneJob(job), true
}

// ListJobs returns all jobs
func (jm *JobManager) ListJobs() []*Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]*Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, cloneJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].StartTime.After(jobs[j].StartTime)
	})
	return jobs
}

// restoreJob adds a terminal job reconstructed from persisted state.
func (jm *JobManager) restoreJob(job *Job) error {
	if job == nil || job.ID == "" {
		return fmt.Errorf("invalid restored job")
	}
	if job.State != StateCompleted && job.State != StateFailed && job.State != StateCancelled {
		return fmt.Errorf("cannot restore non-terminal job state %q", job.State)
	}
	if job.StartTime.IsZero() || job.EndTime == nil {
		return fmt.Errorf("restored job is missing lifecycle timestamps")
	}

	jm.mu.Lock()
	defer jm.mu.Unlock()
	if existing, exists := jm.jobs[job.ID]; exists {
		return &duplicateJobError{jobID: job.ID, owner: app.NormalizeProject(existing.Project)}
	}
	jm.jobs[job.ID] = cloneJob(job)
	return nil
}

// UpdateJob atomically updates a job using the provided function
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
		return fmt.Errorf("invalid progress snapshot")
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
		return fmt.Errorf("invalid candidate progress snapshot")
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
	job.BestCost = bestCost
	job.BestRevision++
	return true
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

// CompleteJob records the measured final result and transitions to completed.
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

// DeleteJob removes a terminal job. Active jobs must be cancelled first.
func (jm *JobManager) DeleteJob(id string) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	job, ok := jm.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}
	if job.State == StatePending || job.State == StateRunning {
		return fmt.Errorf("%w: cannot delete %s job", ErrInvalidTransition, job.State)
	}
	delete(jm.jobs, id)
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
	event := ProgressEvent{
		JobID:         job.ID,
		State:         job.State,
		Iterations:    job.Iterations,
		Evaluations:   job.Evaluations,
		BestCost:      job.BestCost,
		BestRevision:  job.BestRevision,
		CandidateCost: cloneFloat(job.CandidateCost),
		PSNR:          cloneFloat(job.PSNR),
		PSNRInfinite:  job.PSNRInfinite,
		SSIM:          cloneFloat(job.SSIM),
		Timestamp:     time.Now(),
	}
	jm.mu.Unlock()

	// Successful completion is published by the worker with its measured CPS.
	// Failure and cancellation can originate outside the worker, so publish
	// those transitions here to guarantee that live streams terminate.
	if next == StateFailed || next == StateCancelled {
		jm.broadcaster.Broadcast(event)
	}
	return nil
}

func canTransition(current, next JobState) bool {
	switch current {
	case StatePending:
		return next == StateRunning || next == StateFailed || next == StateCancelled
	case StateRunning:
		return next == StateCompleted || next == StateFailed || next == StateCancelled
	default:
		return false
	}
}

// GetRunningJobs returns all jobs currently in the running state
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

// cloneJob returns a fully detached snapshot. Callers may safely retain or
// serialize it while the manager continues updating the live job.
func cloneJob(job *Job) *Job {
	if job == nil {
		return nil
	}

	cloned := *job
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

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
