package cron

import (
	"context"
	"log"
	"time"
)

// IntegrationExample shows how to integrate the cron framework into your existing application.
func IntegrationExample() {
	// This is how you would integrate the cron manager into your existing application.

	// 1. Initialize the cron manager during application startup.
	config := Config{
		DatabaseURL:  "postgres://user:password@localhost:5432/your_database?sslmode=disable",
		TickInterval: 30 * time.Second,
	}

	manager, err := NewManager(config)
	if err != nil {
		log.Fatal("Failed to create cron manager:", err)
	}

	// 2. Register your job handlers.
	registerApplicationJobs(manager)

	// 3. Start the manager.
	if err := manager.Start(context.Background()); err != nil {
		log.Fatal("Failed to start cron manager:", err)
	}

	// 4. In your application shutdown logic, stop the manager.
	defer manager.Stop()
}

func registerApplicationJobs(manager *Manager) {
	// Example: Register and add jobs using the utility functions.

	// Simple cleanup job.
	cleanupJob := SimpleJob(
		"cleanup-logs",
		"Cleanup Old Logs",
		EveryDay,
		LoggingJobWrapper("Cleanup Logs", cleanupOldLogs),
	)
	cleanupJob.Description = "Removes log files older than 30 days"

	// Database maintenance with timeout wrapper.
	dbMaintenanceJob := SimpleJob(
		"db-maintenance",
		"Database Maintenance",
		Every6Hours,
		TimeoutJobWrapper(10*time.Minute, LoggingJobWrapper("DB Maintenance", performDatabaseMaintenance)),
	)
	dbMaintenanceJob.Description = "Performs database vacuum and analyze operations"

	// Email notifications with retry wrapper.
	emailJob := SimpleJob(
		"send-emails",
		"Send Email Notifications",
		Every15Minutes,
		RetryJobWrapper(3, LoggingJobWrapper("Email Notifications", sendEmailNotifications)),
	)
	emailJob.Description = "Sends pending email notifications to users"

	// One-time migration job (runs once at a specific time).
	migrationJob := OneTimeJob(
		"data-migration-2025",
		"Data Migration 2025",
		time.Now().Add(1*time.Hour), // Run in 1 hour
		LoggingJobWrapper("Data Migration", performDataMigration),
	)
	migrationJob.Description = "One-time data migration for 2025 updates"

	// Conditional backup job (only runs during business hours).
	backupJob := SimpleJob(
		"backup-data",
		"Backup Application Data",
		Every2Hours,
		ConditionalJobHandler(isBusinessHours, LoggingJobWrapper("Data Backup", backupApplicationData)),
	)
	backupJob.Description = "Backs up application data during business hours only"

	// Chain multiple operations together.
	compositeJob := SimpleJob(
		"daily-maintenance",
		"Daily Maintenance Tasks",
		EveryDay,
		ChainJobHandlers(
			LoggingJobWrapper("System Health Check", systemHealthCheck),
			LoggingJobWrapper("Update Statistics", updateStatistics),
			LoggingJobWrapper("Generate Reports", generateDailyReports),
		),
	)
	compositeJob.Description = "Runs multiple daily maintenance tasks in sequence"

	// Add all jobs to the manager.
	jobs := []*Job{cleanupJob, dbMaintenanceJob, emailJob, migrationJob, backupJob, compositeJob}

	for _, job := range jobs {
		// Register the handler first.
		manager.RegisterJob(job.Name, job.Description, job.Interval, job.Handler)

		// Then add to database
		if err := manager.AddJob(context.Background(), job); err != nil {
			log.Printf("Failed to add job %s: %v", job.Name, err)
		} else {
			log.Printf("Successfully added job: %s", job.Name)
		}
	}
}

// Example job handler functions.
func cleanupOldLogs(job *Job) error {
	// Implementation for cleaning up old log files.
	log.Printf("Cleaning up logs older than 30 days...")
	// Your cleanup logic here.
	return nil
}

func performDatabaseMaintenance(job *Job) error {
	// Implementation for database maintenance.
	log.Printf("Performing database maintenance...")
	// Your maintenance logic here.
	return nil
}

func sendEmailNotifications(job *Job) error {
	// Implementation for sending email notifications.
	log.Printf("Sending pending email notifications...")
	// Your email sending logic here.
	return nil
}

func performDataMigration(job *Job) error {
	// Implementation for one-time data migration.
	log.Printf("Performing data migration...")
	// Your migration logic here.
	return nil
}

func backupApplicationData(job *Job) error {
	// Implementation for data backup.
	log.Printf("Backing up application data...")
	// Your backup logic here.
	return nil
}

func systemHealthCheck(job *Job) error {
	// Implementation for system health check.
	log.Printf("Performing system health check...")
	// Your health check logic here.
	return nil
}

func updateStatistics(job *Job) error {
	// Implementation for updating statistics.
	log.Printf("Updating application statistics...")
	// Your statistics update logic here.
	return nil
}

func generateDailyReports(job *Job) error {
	// Implementation for generating daily reports.
	log.Printf("Generating daily reports...")
	// Your report generation logic here.
	return nil
}

func isBusinessHours() bool {
	// Simple business hours check (9 AM - 6 PM weekdays).
	now := time.Now()
	weekday := now.Weekday()
	hour := now.Hour()

	return weekday >= time.Monday && weekday <= time.Friday && hour >= 9 && hour <= 18
}

// Example of how to monitor jobs in your application.
func MonitorJobs(manager *Manager) {
	// You could run this periodically to monitor job health.
	stats := manager.GetJobStats()
	log.Printf("Job Statistics: Total=%d, Pending=%d, Running=%d, Failed=%d, Disabled=%d",
		stats.TotalJobs, stats.PendingJobs, stats.RunningJobs, stats.FailedJobs, stats.DisabledJobs)

	// Check for failed jobs.
	failedJobs := manager.GetJobsByStatus(StatusFailed)
	if len(failedJobs) > 0 {
		log.Printf("WARNING: %d jobs have failed", len(failedJobs))
		for _, job := range failedJobs {
			log.Printf("Failed job: %s - %s", job.Name, job.LastError)
		}
	}

	// Check for overdue jobs.
	overdueJobs := manager.GetOverdueJobs()
	if len(overdueJobs) > 0 {
		log.Printf("WARNING: %d jobs are overdue", len(overdueJobs))
		for _, job := range overdueJobs {
			log.Printf("Overdue job: %s - Next run was: %v", job.Name, job.NextRun)
		}
	}
}
