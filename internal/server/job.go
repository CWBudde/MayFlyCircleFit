package server

import (
	"fmt"
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
	StartTime   time.Time  `json:"startTime"`
	EndTime     *time.Time `json:"endTime,omitempty"`
	Error       string     `json:"error,omitempty"`
}

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
	return job
}

// GetJob retrieves a job by ID
func (jm *JobManager) GetJob(id string) (*Job, bool) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, exists := jm.jobs[id]
	return job, exists
}

// ListJobs returns all jobs
func (jm *JobManager) ListJobs() []*Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]*Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, job)
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

// GetRunningJobs returns all jobs currently in the running state
func (jm *JobManager) GetRunningJobs() []*Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	runningJobs := make([]*Job, 0)
	for _, job := range jm.jobs {
		if job.State == StateRunning {
			runningJobs = append(runningJobs, job)
		}
	}
	return runningJobs
}
