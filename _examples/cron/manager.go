package cron

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kataras/pg"
)

// Manager handles cron job scheduling and execution with PostgreSQL persistence.
type Manager struct {
	db             *pg.DB
	jobsRepository *pg.Repository[*Job]
	jobs           map[string]*Job
	handlers       map[string]JobHandler
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	// Configuration
	tickInterval time.Duration
}

// Config holds configuration for the cron manager.
type Config struct {
	DatabaseURL  string
	TickInterval time.Duration // How often to check for jobs to run (default: 1 minute).
}

// NewManager creates a new cron manager instance.
func NewManager(config Config) (*Manager, error) {
	if config.TickInterval == 0 {
		config.TickInterval = time.Minute // Default check every minute.
	}

	schema := pg.NewSchema()
	schema.MustRegister("cron_jobs", Job{})
	db, err := pg.Open(context.Background(), schema, config.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	manager := &Manager{
		db:             db,
		jobsRepository: pg.NewRepository[*Job](db),
		jobs:           make(map[string]*Job),
		handlers:       make(map[string]JobHandler),
		ctx:            ctx,
		cancel:         cancel,
		tickInterval:   config.TickInterval,
	}

	// Initialize database table.
	if err := manager.initDB(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return manager, nil
}

// initDB executes further queries for the cron_jobs table if nesessary.
func (m *Manager) initDB() error {
	return nil
}

// RegisterJob registers a job with its handler but doesn't persist it yet.
func (m *Manager) RegisterJob(name, description string, interval time.Duration, handler JobHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job := &Job{
		Name:        name,
		Description: description,
		Interval:    interval,
		Status:      StatusPending,
		NextRun:     time.Now().Add(interval),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Handler:     handler,
	}

	m.jobs[name] = job
	m.handlers[name] = handler
}

// AddJob adds a new job to the database or updates existing one.
func (m *Manager) AddJob(ctx context.Context, job *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Pg library automatically sets UpdatedAt on updates.
	// job.UpdatedAt = time.Now()

	// Check if job exists.
	// Execute row queries like that:
	// var exists bool
	// err := m.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM cron_jobs WHERE name = $1)", job.Name).Scan(&exists)
	// if err != nil {
	// 	return fmt.Errorf("failed to check job existence: %w", err)
	// }
	var err error
	query := `SELECT id FROM cron_jobs WHERE name = $1;`
	job.ID, err = pg.QuerySingle[string](ctx, m.db, query, job.Name)
	if err != nil && !errors.Is(err, pg.ErrNoRows) {
		return fmt.Errorf("failed to check job existence: %w", err)
	}
	if job.ID != "" {
		// Update existing job.
		_, err = m.jobsRepository.UpdateOnlyColumns(ctx, []string{"name", "description", "interval", "status", "next_run"}, job)
	} else {
		// Insert new job.
		// Pg library automatically sets CreatedAt on inserts.
		// job.CreatedAt = time.Now()
		err = m.jobsRepository.InsertSingle(ctx, job, &job.ID)
	}

	if err != nil {
		return fmt.Errorf("failed to save job: %w", err)
	}

	m.jobs[job.Name] = job
	return nil
}

// LoadJobs loads all jobs from the database
func (m *Manager) LoadJobs(ctx context.Context) error {
	query := `SELECT * FROM cron_jobs;`
	jobs, err := m.jobsRepository.Select(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to load jobs: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, job := range jobs {
		// job.Interval = time.Duration(intervalNs)

		// Assign handler if registered
		if handler, exists := m.handlers[job.Name]; exists {
			job.Handler = handler
		}

		m.jobs[job.Name] = job
	}

	return nil
}

// updateJobInDB updates job status in database.
func (m *Manager) updateJobInDB(ctx context.Context, job *Job) error {
	_, err := m.jobsRepository.UpdateOnlyColumns(ctx, []string{"status", "last_run", "next_run", "error_count", "last_error"}, job)
	return err
}

// Start begins the cron manager execution loop.
func (m *Manager) Start(ctx context.Context) error {
	// Load jobs from database first.
	if err := m.LoadJobs(ctx); err != nil {
		return fmt.Errorf("failed to load jobs from database: %w", err)
	}

	m.wg.Add(1)
	go m.run()

	log.Printf("Cron manager started with %d jobs", len(m.jobs))
	return nil
}

// Stop gracefully stops the cron manager.
func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
	m.db.Close()
	log.Println("Cron manager stopped")
}

// run is the main execution loop.
func (m *Manager) run() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.processJobs(m.ctx)
		}
	}
}

// processJobs checks and runs ready jobs.
func (m *Manager) processJobs(ctx context.Context) {
	m.mu.RLock()
	readyJobs := make([]*Job, 0)

	for _, job := range m.jobs {
		if job.IsReady() && job.Handler != nil {
			readyJobs = append(readyJobs, job)
		}
	}
	m.mu.RUnlock()

	for _, job := range readyJobs {
		go m.executeJob(ctx, job)
	}
}

// executeJob runs a single job.
func (m *Manager) executeJob(ctx context.Context, job *Job) {
	m.mu.Lock()
	job.MarkRunning()
	m.updateJobInDB(ctx, job)
	m.mu.Unlock()

	log.Printf("Executing job: %s", job.Name)

	err := job.Handler(job)

	m.mu.Lock()
	if err != nil {
		log.Printf("Job %s failed: %v", job.Name, err)
		job.MarkFailed(err)
	} else {
		log.Printf("Job %s completed successfully", job.Name)
		job.MarkComplete()
	}
	m.updateJobInDB(ctx, job)
	m.mu.Unlock()
}

// GetJob returns a job by ID.
func (m *Manager) GetJob(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, exists := m.jobs[id]
	return job, exists
}

// ListJobs returns all jobs.
func (m *Manager) ListJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobs := make([]*Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// DisableJob disables a job.
func (m *Manager) DisableJob(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, exists := m.jobs[id]
	if !exists {
		return fmt.Errorf("job not found: %s", id)
	}

	job.Status = StatusDisabled
	job.UpdatedAt = time.Now()

	return m.updateJobInDB(ctx, job)
}

// EnableJob enables a disabled job.
func (m *Manager) EnableJob(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, exists := m.jobs[id]
	if !exists {
		return fmt.Errorf("job not found: %s", id)
	}

	job.Status = StatusPending
	job.UpdatedAt = time.Now()
	job.CalculateNextRun()

	return m.updateJobInDB(ctx, job)
}
