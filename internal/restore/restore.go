package restore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tjmadonna/filebackup/internal/aws"
	"github.com/tjmadonna/filebackup/internal/core"
)

func Restore(config core.Config, manifest []aws.ManifestItem, outDir string) error {
	// Setup logger
	logger, err := core.NewLogger(config.LogDir, "backup", config.LogLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a temp directory
	tempDirName := fmt.Sprintf("filebackup-restore-%s.log", time.Now().UTC().Format("2006-01-02-15-04-05"))
	tempDir := filepath.Join(config.TempDir, tempDirName)
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		logger.Error("Failed to create temp directory", slog.String("temp_dir", tempDir), slog.String("error", err.Error()))
		return err
	}
	defer os.RemoveAll(tempDir)

	logger.Info("Starting file restore process",
		slog.Int("files", len(manifest)),
		slog.String("output_directory", outDir),
		slog.String("database", config.DatabasePath))

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
	processWorkerCount := config.MaxWorkers
	var processWg sync.WaitGroup

	// Start file processing workers
	logger.Info("Starting manifest item processing workers", slog.Int("count", processWorkerCount))
	for i := range processWorkerCount {
		processWg.Add(1)
		go func(id int) {
			defer processWg.Done()
			workerLogger := logger.With(slog.String("worker", fmt.Sprintf("process_manifest_item_%d", id)))
			processManifestItemWorker(ctx, workerLogger, config.EncryptionSecret, outDir, tempDir, manifestItemCh, downloader)
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

	logger.Info("File restore process completed")

	return nil
}
