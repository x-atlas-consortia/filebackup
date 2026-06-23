package list

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/database"
)

// ListRestores lists all restore events from the database and writes them to the specified output path or stdout
func ListRestores(ctx context.Context, config core.Config, logLevel slog.Leveler, outPath, profile string) error {
	// Setup logger
	logger, _, err := core.NewLogger("restore-list", profile, logLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(ctx)
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
	uploadToS3 := false
	var writer io.Writer
	if outPath == "" {
		writer = os.Stdout
	} else if aws.IsS3Path(outPath) {
		// create a temporary local file to write the output
		tempFile, err := os.CreateTemp("", "restore_list_*.txt")
		if err != nil {
			logger.Error("Error creating temporary file for S3 upload", slog.String("error", err.Error()))
			return err
		}
		defer os.Remove(tempFile.Name())
		uploadToS3 = true
		writer = tempFile
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

	// If uploading to S3, perform the upload
	if uploadToS3 {
		f := writer.(*os.File)

		// ensure all data is flushed and file descriptor released before upload
		if err := f.Sync(); err != nil {
			logger.Error("Error syncing temp file", slog.String("error", err.Error()))
			return err
		}
		if err := f.Close(); err != nil {
			logger.Error("Error closing temp file before upload", slog.String("error", err.Error()))
			return err
		}

		tempFilePath := f.Name()
		_, key, err := aws.ParseS3Path(outPath)
		if err != nil {
			logger.Error("Error parsing S3 path", slog.String("error", err.Error()))
			return err
		}

		awsConfig := aws.Config{
			AccessKeyID:     config.AWSAccessKeyID,
			SecretAccessKey: config.AWSSecretAccessKey,
			Region:          config.AWSRegion,
			Bucket:          config.AWSS3Bucket,
		}
		uploader, err := aws.New(ctx, awsConfig, logger)
		if err != nil {
			logger.Error("Error creating AWS uploader", slog.String("error", err.Error()))
			return err
		}

		_, err = uploader.UploadFile(ctx, tempFilePath, outPath, key, types.StorageClassStandard, time.Now().UTC())
		if err != nil {
			logger.Error("Error uploading restore list to S3", slog.String("error", err.Error()))
			return err
		}

		msg := fmt.Sprintf("Successfully uploaded restore list to S3: bucket %s, key %s", config.AWSS3Bucket, key)
		fmt.Println(msg)
		return nil
	}

	if writer != os.Stdout {
		msg := fmt.Sprintf("Successfully wrote restore list to file: %s", outPath)
		fmt.Println(msg)
	}

	return nil
}
