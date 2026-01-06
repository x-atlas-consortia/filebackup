package aws

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// DownloadFile downloads a file from S3 based on the provided ManifestItem and saves it to outPath.
func (m *AWSS3FileManager) DownloadFile(ctx context.Context, item ManifestItem, outPath string) error {
	downloader := manager.NewDownloader(m.s3Client)

	// Create the output file
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", outPath, err)
	}
	defer outFile.Close()

	// Download the file
	dlInput := &s3.GetObjectInput{
		Bucket:       aws.String(item.Bucket),
		ChecksumMode: "ENABLED",
		Key:          aws.String(item.Key),
		VersionId:    aws.String(item.VersionID),
	}
	n, err := downloader.Download(ctx, outFile, dlInput)
	if err != nil {
		// Clean up the partially downloaded file
		outFile.Close()
		os.Remove(outPath)
		return fmt.Errorf("failed to download file %s/%s: %w", item.Bucket, item.Key, err)
	}

	m.logger.Info(
		"Successfully downloaded file",
		"bucket", item.Bucket,
		"key", item.Key,
		"version", item.VersionID,
		"size", n,
		"outPath", outPath,
	)

	return nil
}
