package list

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/database"
)

func ListBackups(config core.Config, logLevel slog.Leveler, outPath, profile string) error {
	// Setup logger
	logger, _, err := core.NewLogger("backup-list", profile, logLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Read mode database
	dbPath := core.GetDatabasePath(profile)
	readOnlyDB, err := database.New(ctx, dbPath, true)
	if err != nil {
		logger.Error("Error opening database in read-only mode", slog.String("error", err.Error()))
		return nil
	}
	defer func() {
		if closeErr := readOnlyDB.Close(); closeErr != nil {
			logger.Error("Error closing database", slog.String("error", closeErr.Error()))
		}
	}()

	backups, err := readOnlyDB.GetEvents(ctx, "backup")
	if err != nil {
		logger.Error("Error retrieving backups from database", slog.String("error", err.Error()))
		return nil
	}

	// Determine output writer
	var writer io.Writer
	if outPath == "" {
		writer = os.Stdout
	} else {
		outFile, err := os.Create(outPath)
		if err != nil {
			logger.Error("Error creating output file", slog.String("error", err.Error()))
			return err
		}
		defer outFile.Close()
		writer = outFile
	}

	// Print backups to writer
	if len(backups) == 0 {
		_, err := fmt.Fprintln(writer, "No backups found")
		if err != nil {
			logger.Error("Error writing to output", slog.String("error", err.Error()))
			return err
		}
		return nil
	} else {
		for _, backup := range backups {
			startedAt := time.Unix(backup.StartedAt, 0).UTC()
			endedAt := time.Unix(backup.EndedAt, 0).UTC()
			details := "N/A"
			if backup.Details.Valid {
				details = backup.Details.String
			}
			_, err := fmt.Fprintf(writer, "ID: %d, Started At: %s UTC, Ended At: %s UTC, Details: %s\n",
				backup.StartedAt,
				startedAt.Format("2006-01-02 15:04:05"),
				endedAt.Format("2006-01-02 15:04:05"), details)
			if err != nil {
				logger.Error("Error writing to output file", slog.String("error", err.Error()))
				return err
			}
		}
	}

	return nil
}
