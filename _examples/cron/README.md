# Cron Framework for Go

A simple, persistent cron framework for Go that uses PostgreSQL for state management and survives server restarts.

## Features

- **Persistent State**: Jobs and their execution state are stored in PostgreSQL
- **Server Restart Resilience**: Jobs continue running after server restarts
- **Simple Duration-based Scheduling**: Uses Go's `time.Duration` instead of cron syntax
- **Concurrent Execution**: Jobs run in separate goroutines
- **Error Handling**: Failed jobs are automatically retried with exponential backoff
- **Job Management**: Enable/disable jobs dynamically
- **Status Tracking**: Monitor job execution status and history

## Quick Start

### 1. Database Setup

Create a PostgreSQL database and connection string:
```sql
CREATE DATABASE crondb;
```

### 2. Basic Usage

```go
package main

import (
    "log"
    "time"

    "github.com/kataras/pg/_examples/cron"
)

func main() {
    // Configure the cron manager
    config := cron.Config{
        DatabaseURL:  "postgres://user:password@localhost:5432/crondb?sslmode=disable",
        TickInterval: 30 * time.Second, // How often to check for ready jobs
    }

    // Create manager
    manager, err := cron.NewManager(config)
    if err != nil {
        log.Fatal("Failed to create cron manager:", err)
    }
    defer manager.Stop()

    // Register a job handler
    manager.RegisterJob(
        "my-job",
        "My Daily Job",
        "Does something important every day",
        24 * time.Hour, // Run every 24 hours
        myJobHandler,
    )

    // Add the job to database
    job := &cron.Job{
        ID:          "my-job",
        Name:        "My Daily Job", 
        Description: "Does something important every day",
        Interval:    24 * time.Hour,
        Status:      cron.StatusPending,
        NextRun:     time.Now().Add(24 * time.Hour),
    }
    
    if err := manager.AddJob(job); err != nil {
        log.Fatal("Failed to add job:", err)
    }

    // Start the manager
    if err := manager.Start(); err != nil {
        log.Fatal("Failed to start cron manager:", err)
    }

    // Keep the program running
    select {}
}

func myJobHandler(job *cron.Job) error {
    log.Printf("Executing job: %s", job.Name)
    
    // Do your work here
    // Return error if job fails
    
    return nil
}
```

## API Reference

### Job Status

Jobs can have the following statuses:
- `StatusPending`: Ready to run when scheduled
- `StatusRunning`: Currently executing
- `StatusComplete`: Last execution completed successfully
- `StatusFailed`: Last execution failed
- `StatusDisabled`: Job is disabled and won't run

### Manager Methods

#### `NewManager(config Config) (*Manager, error)`
Creates a new cron manager with the given configuration.

#### `RegisterJob(id, name, description string, interval time.Duration, handler JobHandler)`
Registers a job handler function. This must be done before starting the manager.

#### `AddJob(job *Job) error`
Adds or updates a job in the database.

#### `Start() error`
Starts the cron manager. This loads jobs from the database and begins the execution loop.

#### `Stop()`
Gracefully stops the cron manager.

#### `GetJob(id string) (*Job, bool)`
Retrieves a job by ID.

#### `ListJobs() []*Job`
Returns all registered jobs.

#### `DisableJob(id string) error`
Disables a job so it won't run.

#### `EnableJob(id string) error`
Re-enables a disabled job.

### Job Structure

```go
type Job struct {
    ID          string        // Unique identifier
    Name        string        // Human-readable name
    Description string        // Job description
    Interval    time.Duration // How often to run
    Status      JobStatus     // Current status
    LastRun     *time.Time    // When last executed
    NextRun     time.Time     // When to run next
    CreatedAt   time.Time     // Creation timestamp
    UpdatedAt   time.Time     // Last update timestamp
    ErrorCount  int           // Number of consecutive failures
    LastError   string        // Last error message
    Handler     JobHandler    // Execution function (not stored in DB)
}
```

### Job Handler Function

Job handlers should have this signature:
```go
type JobHandler func(job *Job) error
```

The handler receives the job instance and should return an error if the job fails.

## Database Schema

The framework automatically creates this table:

```sql
CREATE TABLE cron_jobs (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    interval BIGINT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    last_run TIMESTAMP,
    next_run TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMP NOT NULL DEFAULT clock_timestamp(),
    error_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT
);

CREATE UNIQUE INDEX idx_cron_jobs_name ON cron_jobs(name);
CREATE INDEX idx_cron_jobs_next_run ON cron_jobs(next_run);
CREATE INDEX idx_cron_jobs_status ON cron_jobs(status);
```

## Error Handling

- Jobs that fail are automatically retried up to 5 times
- After 5 consecutive failures, jobs are automatically disabled
- Failed jobs have their `NextRun` time calculated normally, so they retry on schedule
- Error details are stored in the `LastError` field

## Best Practices

1. **Keep job handlers lightweight**: Long-running jobs should be broken into smaller chunks
2. **Handle errors gracefully**: Always return meaningful errors from job handlers
3. **Use appropriate intervals**: Don't schedule jobs too frequently to avoid database load
4. **Monitor job status**: Regularly check for failed or disabled jobs
5. **Test job handlers**: Ensure your job handlers are idempotent and handle edge cases

## Integration with Existing Applications

The cron framework is designed to be easily integrated into existing Go applications. Simply:

1. Add the cron manager to your application startup
2. Register your job handlers during initialization  
3. Start the manager after your application is ready
4. Stop the manager during graceful shutdown

See the example in `_examples/cron/example/main.go` for a complete implementation.