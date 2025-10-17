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

func ListFiles(config core.Config, logLevel slog.Leveler, listPath, outPath, profile string, timestamp time.Time, manifest bool) error {
	// Setup logger
	logger, _, err := core.NewLogger("file-list", profile, logLevel)
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

	// Get files
	files, err := readOnlyDB.GetFiles(ctx, listPath, timestamp.UTC().Unix())
	if err != nil {
		logger.Error("Error retrieving files from database", slog.String("error", err.Error()))
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

	// Print files to writer
	if len(files) == 0 {
		_, err := fmt.Fprintln(writer, "No files found")
		if err != nil {
			logger.Error("Error writing to output", slog.String("error", err.Error()))
			return err
		}
		return nil
	} else {
		for _, file := range files {
			if manifest {
				_, err := fmt.Fprintf(writer, "%s,%s,%s\n",
					config.AWSS3Bucket,
					file.Path,
					file.VersionID)
				if err != nil {
					logger.Error("Error writing to output file", slog.String("error", err.Error()))
					return err
				}
			} else {
				lastModifiedAt := time.Unix(file.LastModifiedAt, 0).UTC()
				_, err := fmt.Fprintf(writer, "Path: %s, Size: %d bytes, LastModified At: %s UTC\n",
					file.Path,
					file.Size,
					lastModifiedAt.Format("2006-01-02 15:04:05"))
				if err != nil {
					logger.Error("Error writing to output file", slog.String("error", err.Error()))
					return err
				}
			}
		}
	}

	return nil
}
