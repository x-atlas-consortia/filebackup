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
	uploadToS3 := false
	var writer io.Writer
	if outPath == "" {
		writer = os.Stdout
	} else if aws.IsS3Path(outPath) {
		// create a temporary local file to write the output
		tempFile, err := os.CreateTemp("", "restore_status_*.txt")
		if err != nil {
			logger.Error("Error creating temporary file for S3 upload", slog.String("error", err.Error()))
			return err
		}
		defer func() {
			tempFile.Close()
			os.Remove(tempFile.Name())
		}()
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

	// If output is to S3, upload the temporary file
	if uploadToS3 {
		tempFilePath := writer.(*os.File).Name()
		_, key, err := aws.ParseS3Path(outPath)
		if err != nil {
			logger.Error("Error parsing S3 path", slog.String("error", err.Error()))
			return err
		}

		_, err = manager.UploadFile(ctx, tempFilePath, key, types.StorageClassStandard, time.Now().UTC())
		if err != nil {
			logger.Error("Error uploading restore status file to S3", slog.String("error", err.Error()))
			return err
		}

		msg := fmt.Sprintf("Successfully uploaded restore status to S3: bucket %s, key %s", config.AWSS3Bucket, key)
		fmt.Println(msg)
		return nil
	}

	if writer != os.Stdout {
		msg := fmt.Sprintf("Successfully wrote restore status to file: %s", outPath)
		fmt.Println(msg)
	}

	return nil
}
