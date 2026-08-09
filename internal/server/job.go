package server

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

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
	ID          string     `json:"id"`
	State       JobState   `json:"state"`
	Config      JobConfig  `json:"config"`
	BestParams  []float64  `json:"bestParams,omitempty"`
	BestCost    float64    `json:"bestCost"`
	InitialCost float64    `json:"initialCost"`
	Iterations  int        `json:"iterations"`
	Evaluations int        `json:"evaluations"`
	Termination string     `json:"termination,omitempty"`
	StartTime   time.Time  `json:"startTime"`
	EndTime     *time.Time `json:"endTime,omitempty"`
	Error       string     `json:"error,omitempty"`
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

// CreateJob creates a new job with the given configuration
func (jm *JobManager) CreateJob(config JobConfig) *Job {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job := &Job{
		ID:        uuid.New().String(),
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
	return jobs
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
	if len(bestParams) > 0 && (len(job.BestParams) == 0 || bestCost <= job.BestCost) {
		job.BestParams = append([]float64(nil), bestParams...)
		job.BestCost = bestCost
	}
	return nil
}

// CompleteJob records the measured final result and transitions to completed.
func (jm *JobManager) CompleteJob(id string, iterations, evaluations int, bestParams []float64, bestCost, initialCost float64, termination string) error {
	return jm.transition(id, StateCompleted, func(job *Job) {
		job.Iterations = iterations
		job.Evaluations = evaluations
		job.BestParams = append([]float64(nil), bestParams...)
		job.BestCost = bestCost
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
	defer jm.mu.Unlock()
	job, ok := jm.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}
	if !canTransition(job.State, next) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, job.State, next)
	}
	if update != nil {
		update(job)
	}
	job.State = next
	if next == StateCompleted || next == StateFailed || next == StateCancelled {
		end := time.Now()
		job.EndTime = &end
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
	if job.EndTime != nil {
		endTime := *job.EndTime
		cloned.EndTime = &endTime
	}
	return &cloned
}
