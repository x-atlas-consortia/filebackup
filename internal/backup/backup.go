package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/database"
)

func Backup(config core.Config, details string) error {
	startTime := time.Now()

	// Setup logger
	logger, logWriter, err := core.NewLogger(config.LogDir, "backup", config.LogLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a temp directory
	tempDirName := fmt.Sprintf("filebackup-backup-%s.log", time.Now().UTC().Format("2006-01-02-15-04-05"))
	tempDir := filepath.Join(config.TempDir, tempDirName)
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		logger.Error("Failed to create temp directory", slog.String("temp_dir", tempDir), slog.String("error", err.Error()))
		return err
	}
	defer os.RemoveAll(tempDir)

	logger.Info("Starting file backup process",
		slog.Int("directories", len(config.Directories)),
		slog.Int("workers", config.MaxWorkers),
		slog.String("database", config.DatabasePath))

	// Initialize the s3 file manager
	awsConfig := aws.Config{
		AccessKeyID:     config.AWSAccessKeyID,
		SecretAccessKey: config.AWSSecretAccessKey,
		Region:          config.AWSRegion,
		Bucket:          config.AWSS3Bucket,
	}
	uploader, err := aws.New(ctx, awsConfig, logger)
	if err != nil {
		logger.Error("Failed to create S3 manager", slog.String("error", err.Error()))
		return err
	}

	// Create channel and start database file insert worker
	fileInsertCh := make(chan database.InsertFileItem, 1000)
	dbReady := make(chan error, 1)
	var dbInsertWg sync.WaitGroup

	dbInsertWg.Go(func() {
		databaseFileInsertWorker(ctx, logger, config.DatabasePath, fileInsertCh, dbReady)
	})

	if err := <-dbReady; err != nil {
		logger.Error("Database initialization failed", slog.String("error", err.Error()))
		cancel()
		return err
	}
	close(dbReady)
	logger.Info("Database insert worker ready")

	// Create channels and wait group for process workers
	processWorkerCount := config.MaxWorkers - 1 // Reserve one worker for db insert
	filesToProcessCh := make(chan string, 1000) // Buffer for file paths
	var processWg sync.WaitGroup

	// Start file processing workers
	logger.Info("Starting file processing workers", slog.Int("count", processWorkerCount))
	for i := range processWorkerCount {
		processWg.Add(1)
		go func(id int) {
			defer processWg.Done()
			workerLogger := logger.With(slog.String("worker", fmt.Sprintf("process_file_%d", id)))
			processFileWorker(ctx, workerLogger, config.DatabasePath, config.EncryptionSecret, tempDir, filesToProcessCh, uploader, fileInsertCh)
		}(i)
	}

	// Walk files (blocks until complete)
	logger.Info("Starting file walking")
	walkFiles(ctx, logger, config.Directories, filesToProcessCh)
	logger.Info("File walking completed, closing file channel")

	// Close the files channel to signal process workers no more files are coming
	close(filesToProcessCh)

	// Wait for all process workers to complete
	logger.Info("Waiting for all file processing to complete...")
	processWg.Wait()
	logger.Info("All process workers finished")

	// Close the database insert channel now that all processing is done
	close(fileInsertCh)

	// Wait for the database worker to finish
	logger.Info("Waiting for database worker to complete...")
	dbInsertWg.Wait()
	logger.Info("Database worker finished")

	// Insert backup event into the database
	err = insertBackupEvent(ctx, config.DatabasePath, details, startTime)
	if err != nil {
		logger.Error("Error inserting backup event", slog.String("error", err.Error()))
		return err
	}

	fmt.Fprintf(logWriter, `time=%s, msg="Backup process completed" duration=%.2f seconds\n`,
		time.Now().Format(time.RFC3339), time.Since(startTime).Seconds())

	return nil
}

func insertBackupEvent(ctx context.Context, dbPath, details string, startTime time.Time) error {
	db, err := database.New(ctx, dbPath, false)
	if err != nil {
		return fmt.Errorf("error inserting event: %w", err)
	}
	defer db.Close()

	// Insert the backup event
	event := database.InsertEvent{
		Details:   details,
		EndedAt:   time.Now().UTC().Unix(),
		StartedAt: startTime.UTC().Unix(),
		Type:      "backup",
	}
	err = db.InsertEvent(ctx, event)
	if err != nil {
		return fmt.Errorf("error inserting event: %w", err)
	}

	// Reindex
	err = db.Reindex(ctx)
	if err != nil {
		return fmt.Errorf("error reindexing database: %w", err)
	}

	return nil
}
