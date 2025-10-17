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

func ListRestores(config core.Config, logLevel slog.Leveler, outPath, profile string) error {
	// Setup logger
	logger, _, err := core.NewLogger("restore-list", profile, logLevel)
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
		if closeErr := readOnlyDB.Close(ctx); closeErr != nil {
			logger.Error("Error closing database", slog.String("error", closeErr.Error()))
		}
	}()

	restores, err := readOnlyDB.GetEvents(ctx, "restore")
	if err != nil {
		logger.Error("Error retrieving restores from database", slog.String("error", err.Error()))
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

	// Print restores to writer
	if len(restores) == 0 {
		_, err := fmt.Fprintln(writer, "No restores found")
		if err != nil {
			logger.Error("Error writing to output", slog.String("error", err.Error()))
			return err
		}
		return nil
	} else {
		for _, restore := range restores {
			startedAt := time.Unix(restore.StartedAt, 0).UTC()
			endedAt := time.Unix(restore.EndedAt, 0).UTC()
			details := "N/A"
			if restore.Details.Valid {
				details = restore.Details.String
			}
			_, err := fmt.Fprintf(writer, "ID: %d, Started At: %s UTC, Ended At: %s UTC, Details: %s\n",
				restore.StartedAt,
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
