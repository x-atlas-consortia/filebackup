package restore

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

func Restore(config core.Config, logLevel slog.Leveler, manifest []aws.ManifestItem, outDir, details, tempDir, profile string, maxWorkers int) error {
	startTime := time.Now()

	// Setup logger
	logger, logWriter, err := core.NewLogger("restore-start", profile, logLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a temp directory
	tempDirName := fmt.Sprintf("filebackup-restore-%s.log", time.Now().UTC().Format("2006-01-02-15-04-05"))
	tempDir = filepath.Join(tempDir, tempDirName)
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		logger.Error("Failed to create temp directory", slog.String("temp_dir", tempDir), slog.String("error", err.Error()))
		return err
	}
	defer os.RemoveAll(tempDir)

	dbPath := core.GetDatabasePath(profile)

	logger.Info("Starting file restore process",
		slog.Int("files", len(manifest)),
		slog.String("output_directory", outDir),
		slog.String("database", dbPath))

	// Initialize the s3 file downloader
	awsConfig := aws.Config{
		AccessKeyID:     config.AWSAccessKeyID,
		SecretAccessKey: config.AWSSecretAccessKey,
		Region:          config.AWSRegion,
		Bucket:          config.AWSS3Bucket,
	}
	downloader, err := aws.New(ctx, awsConfig, logger)
	if err != nil {
		logger.Error("Failed to create S3 downloader", slog.String("error", err.Error()))
		return err
	}

	// Create channel
	chSize := min(1000, len(manifest))
	manifestItemCh := make(chan aws.ManifestItem, chSize)
	processWorkerCount := maxWorkers
	var processWg sync.WaitGroup

	// Start file processing workers
	logger.Info("Starting manifest item processing workers", slog.Int("count", processWorkerCount))
	for i := range processWorkerCount {
		processWg.Add(1)
		go func(id int) {
			defer processWg.Done()
			workerLogger := logger.With(slog.String("worker", fmt.Sprintf("process_manifest_item_%d", id)))
			processManifestItemWorker(ctx, workerLogger, dbPath, config.EncryptionSecret, outDir, tempDir, manifestItemCh, downloader)
		}(i)
	}

	for _, item := range manifest {
		select {
		case manifestItemCh <- item:
			// Item sent to channel
		case <-ctx.Done():
			// Context cancelled
			return ctx.Err()
		}
	}

	// Close channel and wait for workers to finish
	close(manifestItemCh)
	processWg.Wait()

	// Insert restore event into the database
	err = insertRestoreEvent(ctx, dbPath, details, startTime)
	if err != nil {
		logger.Error("Error inserting restore event", slog.String("error", err.Error()))
		return err
	}

	fmt.Fprintf(logWriter, `time=%s, msg="Restore process completed" duration=%.2f seconds\n`,
		time.Now().Format(time.RFC3339), time.Since(startTime).Seconds())

	return nil
}

func insertRestoreEvent(ctx context.Context, dbPath, details string, startTime time.Time) error {
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
		Type:      "restore",
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
