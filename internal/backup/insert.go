package backup

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/x-atlas-consortia/filebackup/internal/database"
)

const databaseBatchSize = 500

func databaseFileInsertWorker(ctx context.Context, logger *slog.Logger, dbPath string, dbFileInsertWorkerInited <-chan database.InsertFileItem, dbInitialized chan<- error) {
	workerLogger := logger.With(slog.String("worker", "database_file_insert"))

	// Write mode database
	db, err := database.New(ctx, dbPath, false)
	if err != nil {
		workerLogger.Error("Error opening database", slog.String("error", err.Error()))
		dbInitialized <- err
		return
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			workerLogger.Error("Error closing database", slog.String("error", closeErr.Error()))
		}
	}()

	// Signal that database is ready
	dbInitialized <- nil
	workerLogger.Info("Database created and ready for use")

	// Batch files coming in through channel
	batch := make([]database.InsertFileItem, 0, databaseBatchSize)
	for {
		select {
		case fileInfoItem, ok := <-dbFileInsertWorkerInited:
			if !ok {
				// Process final batch
				if len(batch) > 0 {
					if err := processBatchFiles(ctx, db, batch); err != nil {
						workerLogger.Error("Error processing final batch", slog.String("error", err.Error()))
					} else {
						workerLogger.Info("Final batch processed successfully", slog.Int("batch_size", len(batch)))
					}
				}
				return
			}

			// Add to batch
			batch = append(batch, fileInfoItem)

			// Process batch when full
			if len(batch) >= databaseBatchSize {
				if err := processBatchFiles(ctx, db, batch); err != nil {
					workerLogger.Error("Error processing batch", slog.String("error", err.Error()))
				} else {
					workerLogger.Info("Batch processed successfully", slog.Int("batch_size", len(batch)))
				}
				batch = batch[:0] // Reset batch
			}

		case <-ctx.Done():
			// Process remaining batch before exiting
			if len(batch) > 0 {
				if err := processBatchFiles(ctx, db, batch); err != nil {
					workerLogger.Error("Error processing final batch on cancellation", slog.String("error", err.Error()))
				} else {
					workerLogger.Info("Final batch processed successfully on cancellation", slog.Int("batch_size", len(batch)))
				}
			}
			return
		}
	}
}

func processBatchFiles(ctx context.Context, db *database.Database, batch []database.InsertFileItem) error {
	if len(batch) == 0 {
		return nil
	}

	if err := db.InsertFiles(ctx, batch); err != nil {
		return fmt.Errorf("batch insert failed: %w", err)
	}

	return nil
}
