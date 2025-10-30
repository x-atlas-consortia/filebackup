package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/database"
)

func Backup(config core.Config, logLevel slog.Leveler, details, tempDir, profile string, directories []string, maxWorkers int) error {
	startTime := time.Now()

	// Setup logger
	logger, logWriter, err := core.NewLogger("backup-start", profile, logLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a temp directory
	tempDirName := fmt.Sprintf("filebackup-backup-%s.log", time.Now().UTC().Format("2006-01-02-15-04-05"))
	tempDir = filepath.Join(tempDir, tempDirName)
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		logger.Error("Failed to create temp directory", slog.String("temp_dir", tempDir), slog.String("error", err.Error()))
		return err
	}
	defer os.RemoveAll(tempDir)

	dbPath := core.GetDatabasePath(profile)

	logger.Info("Starting file backup process",
		slog.Int("directories", len(directories)),
		slog.Int("workers", maxWorkers),
		slog.String("database", dbPath))

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
	numFilesInsertedCh := make(chan int, 1)
	var dbInsertWg sync.WaitGroup

	dbInsertWg.Go(func() {
		numFilesInserted := databaseFileInsertWorker(ctx, logger, dbPath, fileInsertCh, dbReady)
		numFilesInsertedCh <- numFilesInserted
		close(numFilesInsertedCh)
	})

	if err := <-dbReady; err != nil {
		logger.Error("Database initialization failed", slog.String("error", err.Error()))
		cancel()
		return err
	}
	close(dbReady)
	logger.Info("Database insert worker ready")

	// Create channels and wait group for process workers
	processWorkerCount := maxWorkers - 1        // Reserve one worker for db insert
	filesToProcessCh := make(chan string, 1000) // Buffer for file paths
	var processWg sync.WaitGroup

	// Start file processing workers
	logger.Info("Starting file processing workers", slog.Int("count", processWorkerCount))
	for i := range processWorkerCount {
		processWg.Add(1)
		go func(id int) {
			defer processWg.Done()
			workerLogger := logger.With(slog.String("worker", fmt.Sprintf("process_file_%d", id)))
			processFileWorker(ctx, workerLogger, dbPath, config.EncryptionSecret, tempDir, filesToProcessCh, uploader, fileInsertCh)
		}(i)
	}

	// Walk files (blocks until complete)
	logger.Info("Starting file walking")
	walkFiles(ctx, logger, directories, filesToProcessCh)
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

	numFilesInserted := <-numFilesInsertedCh

	// Insert backup event into the database
	err = insertBackupEvent(ctx, dbPath, details, startTime, logger)
	if err != nil {
		logger.Error("Error inserting backup event", slog.String("error", err.Error()))
		return err
	}

	// Upload database to S3 if any files were inserted
	if numFilesInserted > 0 {
		dbName := filepath.Base(dbPath)
		dbVersionID, err := uploader.UploadFile(ctx, dbPath, dbName, types.StorageClassStandard, time.Now().UTC())
		if err != nil {
			logger.Error("Failed to upload database to S3", slog.String("error", err.Error()))
		} else {
			logger.Info("Database uploaded to S3", slog.String("version_id", dbVersionID))
		}
	}

	fmt.Fprintf(logWriter, "time=%s msg=\"Backup process completed\" files=%d duration=%.2f seconds\n",
		time.Now().Format(time.RFC3339), numFilesInserted, time.Since(startTime).Seconds())

	return nil
}

func insertBackupEvent(ctx context.Context, dbPath, details string, startTime time.Time, logger *slog.Logger) error {
	db, err := database.New(ctx, dbPath, false)
	if err != nil {
		return fmt.Errorf("error inserting event: %w", err)
	}
	defer func() {
		if closeErr := db.Close(ctx); closeErr != nil {
			logger.Error("Error closing database", slog.String("error", closeErr.Error()))
		}
	}()

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
