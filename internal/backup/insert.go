package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/x-atlas-consortia/filebackup/internal/database"
)

const databaseBatchSize = 500

// databaseWorkerResult reports counts from databaseFileInsertWorker's run.
type databaseWorkerResult struct {
	FilesInserted int
	FilesDeleted  int
}

// databaseFileInsertWorker handles inserting file records and walked paths into the database in batches, then, once
// both channels are drained, detects and marks files deleted under the given directories.
func databaseFileInsertWorker(ctx context.Context, logger *slog.Logger, dbPath string, directories []string, fileInsertCh <-chan database.InsertFileItem, walkedPathCh <-chan string, dbInitialized chan<- error) databaseWorkerResult {
	workerLogger := logger.With(slog.String("worker", "database_file_insert"))

	// Write mode database
	db, err := database.New(ctx, dbPath, false)
	if err != nil {
		workerLogger.Error("Error opening database", slog.String("error", err.Error()))
		dbInitialized <- err
		return databaseWorkerResult{}
	}
	closeDB := func() {
		if closeErr := db.Close(ctx); closeErr != nil {
			workerLogger.Error("Error closing database", slog.String("error", closeErr.Error()))
		}
	}

	// Signal that database is ready
	dbInitialized <- nil
	workerLogger.Info("Database created and ready for use")

	fileBatch := make([]database.InsertFileItem, 0, databaseBatchSize)
	walkedBatch := make([]string, 0, databaseBatchSize)
	totalInserted := 0

	flushFiles := func() {
		if len(fileBatch) == 0 {
			return
		}
		if err := processBatchFiles(ctx, db, fileBatch); err != nil {
			workerLogger.Error("Error processing file batch", slog.String("error", err.Error()))
		} else {
			workerLogger.Info("File batch processed successfully", slog.Int("batch_size", len(fileBatch)))
			totalInserted += len(fileBatch)
		}
		fileBatch = fileBatch[:0]
	}

	flushWalkedPaths := func() {
		if len(walkedBatch) == 0 {
			return
		}
		if err := db.InsertWalkedPaths(ctx, walkedBatch); err != nil {
			workerLogger.Error("Error processing walked path batch", slog.String("error", err.Error()))
		} else {
			workerLogger.Info("Walked path batch processed successfully", slog.Int("batch_size", len(walkedBatch)))
		}
		walkedBatch = walkedBatch[:0]
	}

	// Multiplex both input channels until each is closed and drained.
	fileCh := fileInsertCh
	walkedCh := walkedPathCh
	cancelled := false

loop:
	for fileCh != nil || walkedCh != nil {
		select {
		case item, ok := <-fileCh:
			if !ok {
				fileCh = nil
				flushFiles()
				continue
			}
			fileBatch = append(fileBatch, item)
			if len(fileBatch) >= databaseBatchSize {
				flushFiles()
			}

		case path, ok := <-walkedCh:
			if !ok {
				walkedCh = nil
				flushWalkedPaths()
				continue
			}
			walkedBatch = append(walkedBatch, path)
			if len(walkedBatch) >= databaseBatchSize {
				flushWalkedPaths()
			}

		case <-ctx.Done():
			cancelled = true
			break loop
		}
	}

	// Flush whatever remains, even on cancellation, so no processed data is lost.
	flushFiles()
	flushWalkedPaths()

	deletedCount := 0
	if cancelled {
		workerLogger.Warn("Backup cancelled before completion, skipping deletion detection")
	} else {
		deletedPaths, err := detectDeletedFiles(ctx, db, directories)
		if err != nil {
			workerLogger.Error("Error detecting deleted files", slog.String("error", err.Error()))
		} else if len(deletedPaths) > 0 {
			if err := db.MarkFilesDeleted(ctx, deletedPaths, time.Now().UTC().Unix()); err != nil {
				workerLogger.Error("Error marking files deleted", slog.String("error", err.Error()))
			} else {
				deletedCount = len(deletedPaths)
				workerLogger.Info("Marked files as deleted", slog.Int("count", deletedCount))
			}
		}
	}

	closeDB()
	return databaseWorkerResult{FilesInserted: totalInserted, FilesDeleted: deletedCount}
}

// detectDeletedFiles returns paths under the given directories that were previously known but weren't seen during
// the current walk.
func detectDeletedFiles(ctx context.Context, db *database.Database, directories []string) ([]string, error) {
	var deletedPaths []string
	for _, dir := range directories {
		prefix := dir
		if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
			prefix += string(os.PathSeparator)
		}

		paths, err := db.GetDeletedPaths(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("error querying deleted paths under %q: %w", dir, err)
		}
		deletedPaths = append(deletedPaths, paths...)
	}
	return deletedPaths, nil
}

// processBatchFiles inserts a batch of file records into the database.
func processBatchFiles(ctx context.Context, db *database.Database, batch []database.InsertFileItem) error {
	if len(batch) == 0 {
		return nil
	}

	if err := db.InsertFiles(ctx, batch); err != nil {
		return fmt.Errorf("batch insert failed: %w", err)
	}

	return nil
}
