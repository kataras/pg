package cron

import (
	"fmt"
	"log"
	"time"
)

// JobBuilder helps build jobs with a fluent interface.
type JobBuilder struct {
	job *Job
}

// NewJobBuilder creates a new job builder.
func NewJobBuilder(name, description string) *JobBuilder {
	return &JobBuilder{
		job: &Job{
			// ID:        id,
			Name:        name,
			Description: description,
			Status:      StatusPending,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}
}

// WithID sets the job id.
func (b *JobBuilder) WithID(id string) *JobBuilder {
	b.job.ID = id
	return b
}

// WithDescription sets the job description.
func (b *JobBuilder) WithDescription(description string) *JobBuilder {
	b.job.Description = description
	return b
}

// WithInterval sets the job interval.
func (b *JobBuilder) WithInterval(interval time.Duration) *JobBuilder {
	b.job.Interval = interval
	return b
}

// WithNextRun sets the next run time (default is now + interval).
func (b *JobBuilder) WithNextRun(nextRun time.Time) *JobBuilder {
	b.job.NextRun = nextRun
	return b
}

// WithHandler sets the job handler.
func (b *JobBuilder) WithHandler(handler JobHandler) *JobBuilder {
	b.job.Handler = handler
	return b
}

// Build returns the constructed job.
func (b *JobBuilder) Build() *Job {
	if b.job.NextRun.IsZero() {
		b.job.NextRun = time.Now().Add(b.job.Interval)
	}
	return b.job
}

// Common job intervals for convenience.
var (
	EveryMinute    = 1 * time.Minute
	Every5Minutes  = 5 * time.Minute
	Every10Minutes = 10 * time.Minute
	Every15Minutes = 15 * time.Minute
	Every30Minutes = 30 * time.Minute
	EveryHour      = 1 * time.Hour
	Every2Hours    = 2 * time.Hour
	Every6Hours    = 6 * time.Hour
	Every12Hours   = 12 * time.Hour
	EveryDay       = 24 * time.Hour
	EveryWeek      = 7 * 24 * time.Hour
)

// SimpleJob creates a simple job with minimal configuration.
func SimpleJob(name, description string, interval time.Duration, handler JobHandler) *Job {
	return NewJobBuilder(name, description).
		WithInterval(interval).
		WithHandler(handler).
		Build()
}

// DelayedJob creates a job that starts after a delay.
func DelayedJob(name, description string, delay, interval time.Duration, handler JobHandler) *Job {
	return NewJobBuilder(name, description).
		WithInterval(interval).
		WithNextRun(time.Now().Add(delay)).
		WithHandler(handler).
		Build()
}

// OneTimeJob creates a job that runs only once.
func OneTimeJob(name, description string, runAt time.Time, handler JobHandler) *Job {
	// For one-time jobs, we set a very large interval so they don't repeat.
	return NewJobBuilder(name, description).
		WithInterval(100 * 365 * 24 * time.Hour). // 100 years.
		WithNextRun(runAt).
		WithHandler(handler).
		Build()
}

// LoggingJobWrapper wraps a job handler with logging.
func LoggingJobWrapper(name string, handler JobHandler) JobHandler {
	return func(job *Job) error {
		start := time.Now()
		log.Printf("[CRON] Starting job: %s", name)

		err := handler(job)

		duration := time.Since(start)
		if err != nil {
			log.Printf("[CRON] Job %s failed after %v: %v", name, duration, err)
		} else {
			log.Printf("[CRON] Job %s completed successfully in %v", name, duration)
		}

		return err
	}
}

// RetryJobWrapper wraps a job handler with custom retry logic.
func RetryJobWrapper(maxRetries int, handler JobHandler) JobHandler {
	return func(job *Job) error {
		var lastErr error

		for i := 0; i < maxRetries; i++ {
			if err := handler(job); err != nil {
				lastErr = err
				if i < maxRetries-1 {
					// Wait before retrying (exponential backoff).
					delay := time.Duration(i+1) * time.Second
					time.Sleep(delay)
				}
			} else {
				return nil // Success
			}
		}

		return fmt.Errorf("job failed after %d retries, last error: %w", maxRetries, lastErr)
	}
}

// TimeoutJobWrapper wraps a job handler with a timeout.
func TimeoutJobWrapper(timeout time.Duration, handler JobHandler) JobHandler {
	return func(job *Job) error {
		done := make(chan error, 1)

		go func() {
			done <- handler(job)
		}()

		select {
		case err := <-done:
			return err
		case <-time.After(timeout):
			return fmt.Errorf("job timed out after %v", timeout)
		}
	}
}

// ChainJobHandlers chains multiple job handlers to run sequentially
func ChainJobHandlers(handlers ...JobHandler) JobHandler {
	return func(job *Job) error {
		for i, handler := range handlers {
			if err := handler(job); err != nil {
				return fmt.Errorf("handler %d failed: %w", i+1, err)
			}
		}
		return nil
	}
}

// ConditionalJobHandler runs a handler only if a condition is met
func ConditionalJobHandler(condition func() bool, handler JobHandler) JobHandler {
	return func(job *Job) error {
		if !condition() {
			log.Printf("[CRON] Skipping job %s: condition not met", job.Name)
			return nil
		}
		return handler(job)
	}
}

// JobStats provides statistics about job execution.
type JobStats struct {
	TotalJobs     int
	PendingJobs   int
	RunningJobs   int
	CompletedJobs int
	FailedJobs    int
	DisabledJobs  int
	AvgErrorCount float64
}

// GetJobStats returns statistics about all jobs.
func (m *Manager) GetJobStats() JobStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := JobStats{}
	totalErrors := 0

	for _, job := range m.jobs {
		stats.TotalJobs++
		totalErrors += job.ErrorCount

		switch job.Status {
		case StatusPending:
			stats.PendingJobs++
		case StatusRunning:
			stats.RunningJobs++
		case StatusComplete:
			stats.CompletedJobs++
		case StatusFailed:
			stats.FailedJobs++
		case StatusDisabled:
			stats.DisabledJobs++
		}
	}

	if stats.TotalJobs > 0 {
		stats.AvgErrorCount = float64(totalErrors) / float64(stats.TotalJobs)
	}

	return stats
}

// GetJobsByStatus returns jobs filtered by status.
func (m *Manager) GetJobsByStatus(status JobStatus) []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var jobs []*Job
	for _, job := range m.jobs {
		if job.Status == status {
			jobs = append(jobs, job)
		}
	}

	return jobs
}

// GetOverdueJobs returns jobs that should have run but haven't.
func (m *Manager) GetOverdueJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var jobs []*Job

	for _, job := range m.jobs {
		if job.Status == StatusPending && now.After(job.NextRun.Add(m.tickInterval)) {
			jobs = append(jobs, job)
		}
	}

	return jobs
}
