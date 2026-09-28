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

// Restore performs the file restore process using the provided configuration and manifest.
// It returns an error if any files in the manifest could not be restored; partial failures
// are logged per-file and counted so the caller gets a clear non-nil result rather than
// a silent success when some files were skipped.
func Restore(ctx context.Context, config core.Config, logLevel slog.Leveler, manifest []aws.ManifestItem, outDir, details, tempDir, profile string, maxWorkers int) error {
	startTime := time.Now()

	// Setup logger
	logger, logWriter, err := core.NewLogger("restore-start", profile, logLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(ctx)
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

	// resultCh collects each worker's failure count. It must be buffered to
	// processWorkerCount so workers never block sending their result before
	// processWg.Done() is called.
	resultCh := make(chan int, processWorkerCount)

	// Start file processing workers
	logger.Info("Starting manifest item processing workers", slog.Int("count", processWorkerCount))
	for i := range processWorkerCount {
		processWg.Add(1)
		go func(id int) {
			defer processWg.Done()
			workerLogger := logger.With(slog.String("worker", fmt.Sprintf("process_manifest_item_%d", id)))
			failed := processManifestItemWorker(ctx, workerLogger, dbPath, config.EncryptionSecret, outDir, tempDir, manifestItemCh, downloader)
			resultCh <- failed
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

	// Close channel and wait for workers to finish, then drain results
	close(manifestItemCh)
	processWg.Wait()
	close(resultCh)

	var totalFailed int
	for n := range resultCh {
		totalFailed += n
	}

	// Always insert the restore event, even for a partial restore, so the
	// database reflects that a restore was attempted and when it ran.
	err = insertRestoreEvent(ctx, dbPath, details, startTime, logger)
	if err != nil {
		logger.Error("Error inserting restore event", slog.String("error", err.Error()))
		return err
	}

	fmt.Fprintf(logWriter, `time=%s, msg="Restore process completed" files=%d failed=%d duration=%.2f seconds\n`,
		time.Now().Format(time.RFC3339), len(manifest), totalFailed, time.Since(startTime).Seconds())

	if totalFailed > 0 {
		return fmt.Errorf("%d of %d file(s) failed to restore; see log for details", totalFailed, len(manifest))
	}
	return nil
}

// insertRestoreEvent inserts a restore event into the database.
func insertRestoreEvent(ctx context.Context, dbPath, details string, startTime time.Time, logger *slog.Logger) error {
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
