package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kataras/pg/_examples/cron"
)

func main() {
	// Configuration.
	config := cron.Config{
		DatabaseURL:  "postgres://user:password@localhost:5432/crondb?sslmode=disable",
		TickInterval: 30 * time.Second, // Check for jobs every 30 seconds
	}

	// Create cron manager.
	manager, err := cron.NewManager(config)
	if err != nil {
		log.Fatal("Failed to create cron manager:", err)
	}
	defer manager.Stop()

	// Register job handlers.
	registerJobs(manager)

	// Start the cron manager.
	if err := manager.Start(context.Background()); err != nil {
		log.Fatal("Failed to start cron manager:", err)
	}

	// Wait for interrupt signal.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down...")
}

func registerJobs(manager *cron.Manager) {
	// Example 1: Cleanup job that runs every hour.
	manager.RegisterJob(
		"cleanup-temp-files",
		"Removes temporary files older than 24 hours",
		1*time.Hour,
		cleanupTempFiles,
	)

	// Example 2: Send notifications every 5 minutes.
	manager.RegisterJob(
		"send-notifications",
		"Sends pending notifications to users",
		5*time.Minute,
		sendNotifications,
	)

	// Example 3: Database backup every 6 hours.
	manager.RegisterJob(
		"database-backup",
		"Creates a backup of the database",
		6*time.Hour,
		databaseBackup,
	)

	// Example 4: Health check every 2 minutes.
	manager.RegisterJob(
		"health-check",
		"Performs system health checks",
		2*time.Minute,
		healthCheck,
	)

	// Add the jobs to the database (this will create them if they don't exist).
	jobs := []*cron.Job{
		{
			ID:          "cleanup-temp-files",
			Name:        "Cleanup Temporary Files",
			Description: "Removes temporary files older than 24 hours",
			Interval:    1 * time.Hour,
			Status:      cron.StatusPending,
			NextRun:     time.Now().Add(1 * time.Hour),
		},
		{
			ID:          "send-notifications",
			Name:        "Send Notifications",
			Description: "Sends pending notifications to users",
			Interval:    5 * time.Minute,
			Status:      cron.StatusPending,
			NextRun:     time.Now().Add(5 * time.Minute),
		},
		{
			ID:          "database-backup",
			Name:        "Database Backup",
			Description: "Creates a backup of the database",
			Interval:    6 * time.Hour,
			Status:      cron.StatusPending,
			NextRun:     time.Now().Add(6 * time.Hour),
		},
		{
			ID:          "health-check",
			Name:        "Health Check",
			Description: "Performs system health checks",
			Interval:    2 * time.Minute,
			Status:      cron.StatusPending,
			NextRun:     time.Now().Add(2 * time.Minute),
		},
	}

	for _, job := range jobs {
		if err := manager.AddJob(context.Background(), job); err != nil {
			log.Printf("Failed to add job %s: %v", job.ID, err)
		} else {
			log.Printf("Added job: %s", job.Name)
		}
	}
}

// Job handler functions.
func cleanupTempFiles(job *cron.Job) error {
	log.Printf("Executing %s: Cleaning up temporary files...", job.Name)

	// Simulate cleanup work.
	time.Sleep(2 * time.Second)

	// In a real implementation, you would:
	// - Find temp directories
	// - Remove files older than 24 hours
	// - Log the cleanup results

	log.Printf("Cleanup completed successfully")
	return nil
}

func sendNotifications(job *cron.Job) error {
	log.Printf("Executing %s: Sending notifications...", job.Name)

	// Simulate notification sending.
	time.Sleep(1 * time.Second)

	// In a real implementation, you would:
	// - Query pending notifications from database
	// - Send emails/SMS/push notifications
	// - Mark notifications as sent

	log.Printf("Notifications sent successfully")
	return nil
}

func databaseBackup(job *cron.Job) error {
	log.Printf("Executing %s: Creating database backup...", job.Name)

	// Simulate backup process.
	time.Sleep(5 * time.Second)

	// In a real implementation, you would:
	// - Use pg_dump or similar tool
	// - Upload backup to cloud storage
	// - Clean up old backups

	log.Printf("Database backup completed successfully")
	return nil
}

func healthCheck(job *cron.Job) error {
	log.Printf("Executing %s: Performing health checks...", job.Name)

	// Simulate health checks.
	time.Sleep(500 * time.Millisecond)

	// In a real implementation, you would:
	// - Check database connectivity
	// - Verify external services
	// - Check disk space, memory usage
	// - Send alerts if issues found

	log.Printf("Health check completed - all systems healthy")
	return nil
}
