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
	ID            string         `json:"id"`
	Project       string         `json:"project"`
	State         JobState       `json:"state"`
	Config        JobConfig      `json:"config"`
	BestParams    []float64      `json:"bestParams,omitempty"`
	BestCost      float64        `json:"bestCost"`
	BestRevision  uint64         `json:"-"`
	CandidateCost *float64       `json:"candidateCost,omitempty"`
	InitialCost   float64        `json:"initialCost"`
	Iterations    int            `json:"iterations"`
	Evaluations   int            `json:"evaluations"`
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
func (jm *JobManager) CreateJob(project string, config JobConfig) *Job {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	if project == "" {
		project = app.DefaultProject
	}
	job := &Job{
		ID:        uuid.New().String(),
		Project:   project,
		State:     StatePending,
		Config:    config,
		StartTime: time.Now(),
	}

	jm.jobs[job.ID] = job
	return cloneJob(job)
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
	if _, exists := jm.jobs[job.ID]; exists {
		return fmt.Errorf("job already exists: %s", job.ID)
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
