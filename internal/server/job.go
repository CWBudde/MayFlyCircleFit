package server

import (
	"fmt"
	"sync"
	"time"

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

// JobConfig holds configuration for an optimization job
type JobConfig struct {
	RefPath string `json:"refPath"`
	Mode    string `json:"mode"`    // joint, sequential, batch
	Circles int    `json:"circles"`
	Iters   int    `json:"iters"`
	PopSize int    `json:"popSize"`
	Seed    int64  `json:"seed"`
}

// Job represents an optimization job
type Job struct {
	ID         string      `json:"id"`
	State      JobState    `json:"state"`
	Config     JobConfig   `json:"config"`
	BestParams []float64   `json:"bestParams,omitempty"`
	BestCost   float64     `json:"bestCost"`
	InitialCost float64    `json:"initialCost"`
	Iterations int         `json:"iterations"`
	StartTime  time.Time   `json:"startTime"`
	EndTime    *time.Time  `json:"endTime,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// JobManager manages the lifecycle of jobs
type JobManager struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

// NewJobManager creates a new JobManager
func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]*Job),
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
