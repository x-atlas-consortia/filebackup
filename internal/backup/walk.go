package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

func walkFiles(ctx context.Context, logger *slog.Logger, directories []string, filesCh chan<- string) {
	workerLogger := logger.With(slog.String("worker", "walk_files"))

	for _, dir := range directories {
		workerLogger.Info("Walking directory", slog.String("directory", dir))
		if err := walkDirectory(ctx, workerLogger, dir, filesCh); err != nil {
			workerLogger.Error("Error walking directory",
				slog.String("directory", dir),
				slog.String("error", err.Error()))
		} else {
			workerLogger.Info("Finished walking directory", slog.String("directory", dir))
		}
	}
}

func walkDirectory(ctx context.Context, logger *slog.Logger, rootDir string, filesCh chan<- string) error {
	// Check if directory exists and is accessible
	if _, err := os.Stat(rootDir); err != nil {
		return fmt.Errorf("cannot access directory %s: %w", rootDir, err)
	}

	var fileCount int

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		// Check for cancellation frequently
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			// Continue walking
			logger.Warn("Error accessing path",
				slog.String("path", path),
				slog.String("error", err.Error()))
			return nil
		}

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			if info.IsDir() {
				logger.Debug("Skipping symlinked directory", slog.String("path", path))
				return filepath.SkipDir
			}
			logger.Debug("Skipping symlinked file", slog.String("path", path))
			return nil
		}

		// Only process regular files
		if !info.Mode().IsRegular() {
			return nil
		}

		// Send the file path to the channel
		select {
		case filesCh <- path:
			fileCount++
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})

	return err
}
