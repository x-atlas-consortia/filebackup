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

// ListVersions lists all versions of a specified file from the database and writes them to the specified output path or stdout
func ListVersions(config core.Config, logLevel slog.Leveler, filePath, outPath, profile string) error {
	// Setup logger
	logger, _, err := core.NewLogger("version-list", profile, logLevel)
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

	// Get versions
	versions, err := readOnlyDB.GetVersions(ctx, filePath)
	if err != nil {
		logger.Error("Error retrieving versions from database", slog.String("error", err.Error()))
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

	// Print versions to writer
	if len(versions) == 0 {
		_, err := fmt.Fprintln(writer, "No versions for file found")
		if err != nil {
			logger.Error("Error writing to output", slog.String("error", err.Error()))
			return err
		}
		return nil
	} else {
		for _, version := range versions {
			lastModifiedAt := time.Unix(version.LastModifiedAt, 0).UTC()

			_, err := fmt.Fprintf(writer, "Size: %d bytes, LastModified At: %s UTC, SHA256: %s\n",
				version.Size,
				lastModifiedAt.Format("2006-01-02 15:04:05"),
				version.SHA256)
			if err != nil {
				logger.Error("Error writing to output file", slog.String("error", err.Error()))
				return err
			}
		}
	}

	return nil
}
