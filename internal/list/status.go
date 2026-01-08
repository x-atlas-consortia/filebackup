package list

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
)

// ListRestoreStatus lists the restore status of files in the provided manifest and writes the output to the specified path or stdout.
func ListRestoreStatus(config core.Config, logLevel slog.Leveler, manifest []aws.ManifestItem, outPath, profile string) error {
	// Setup logger
	logger, _, err := core.NewLogger("restore-status", profile, logLevel)
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return err
	}

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger.Info("Starting restore status listing",
		slog.Int("files", len(manifest)),
		slog.String("output_path", outPath))

	// Initialize the s3 file downloader
	awsConfig := aws.Config{
		AccessKeyID:     config.AWSAccessKeyID,
		SecretAccessKey: config.AWSSecretAccessKey,
		Region:          config.AWSRegion,
		Bucket:          config.AWSS3Bucket,
	}
	manager, err := aws.New(ctx, awsConfig, logger)
	if err != nil {
		logger.Error("Failed to create S3 file manager", slog.String("error", err.Error()))
		return err
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

	// Iterate over manifest items and get their restore status
	for _, item := range manifest {
		restored, expiry, err := manager.ObjectRestoreStatus(ctx, item.Key)
		if err != nil {
			writer.Write([]byte(item.Key + ": Error retrieving status: " + err.Error() + "\n"))
			logger.Error("Error retrieving restore status", slog.String("item", item.Key), slog.String("error", err.Error()))
			continue
		}
		var status string
		if restored {
			status = "Restored"
			if !expiry.IsZero() {
				status += ", Expires on " + expiry.Format("2006-01-02 15:04:05 MST")
			}
		} else {
			status = "Not Restored"
		}
		logger.Info("Retrieved restore status", slog.String("item", item.Key), slog.String("status", status))
		writer.Write([]byte(item.Key + ": " + status + "\n"))
	}

	logger.Info("Completed restore status listing")

	return nil
}
