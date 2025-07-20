package cron

import (
	"time"
)

// JobStatus represents the current status of a cron job.
type JobStatus string

const (
	StatusPending  JobStatus = "pending"
	StatusRunning  JobStatus = "running"
	StatusComplete JobStatus = "complete"
	StatusFailed   JobStatus = "failed"
	StatusDisabled JobStatus = "disabled"
)

// Job represents a cron job that can be executed periodically.
type Job struct {
	ID          string        `json:"id" pg:"type=uuid,primary"`
	Name        string        `json:"name" pg:"type=varchar(255),unique"`
	Description string        `json:"description" pg:"type=text,default=''"`
	Interval    time.Duration `json:"interval" pg:"type=bigint"` // stored as nanoseconds in DB.
	Status      JobStatus     `json:"status" pg:"type=varchar(50),default='pending',index=btree"`
	LastRun     *time.Time    `json:"last_run,omitempty" pg:"type=timestamp,default=NULL"`
	NextRun     time.Time     `json:"next_run" pg:"type=timestamp,index=btree"`
	CreatedAt   time.Time     `json:"created_at" pg:"type=timestamp,default=clock_timestamp()"`
	UpdatedAt   time.Time     `json:"updated_at" pg:"type=timestamp,default=clock_timestamp()"`
	ErrorCount  int           `json:"error_count" pg:"type=int,default=0"`
	LastError   string        `json:"last_error,omitempty" pg:"type=text,default=NULL"`

	// Handler is the function to execute (not stored in DB).
	Handler JobHandler `json:"-" pg:"-"`
}

// JobHandler is the function signature for job execution.
type JobHandler func(job *Job) error

// IsReady returns true if the job is ready to run.
func (j *Job) IsReady() bool {
	return j.Status != StatusDisabled &&
		j.Status != StatusRunning &&
		time.Now().After(j.NextRun)
}

// CalculateNextRun calculates the next run time based on interval.
func (j *Job) CalculateNextRun() {
	if j.LastRun == nil {
		j.NextRun = time.Now().Add(j.Interval)
	} else {
		j.NextRun = j.LastRun.Add(j.Interval)
	}
}

// MarkRunning sets the job status to running.
func (j *Job) MarkRunning() {
	j.Status = StatusRunning
	j.UpdatedAt = time.Now()
}

// MarkComplete marks the job as completed successfully.
func (j *Job) MarkComplete() {
	now := time.Now()
	j.Status = StatusComplete
	j.LastRun = &now
	j.UpdatedAt = now
	j.CalculateNextRun()
	j.Status = StatusPending // Ready for next execution.
}

// MarkFailed marks the job as failed with an error.
func (j *Job) MarkFailed(err error) {
	now := time.Now()
	j.Status = StatusFailed
	j.LastRun = &now
	j.UpdatedAt = now
	j.ErrorCount++
	j.LastError = err.Error()
	j.CalculateNextRun()

	// Auto-retry after failure (could be configurable).
	if j.ErrorCount < 5 { // Max 5 retries.
		j.Status = StatusPending
	} else {
		j.Status = StatusDisabled // Disable after too many failures.
	}
}
