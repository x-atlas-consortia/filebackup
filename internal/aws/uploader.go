package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type Config struct {
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	Bucket          string
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*AWSS3FileManager, error) {
	// Create S3 client
	s3Client := s3.New(s3.Options{
		Credentials: credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, ""),
		Region:      config.Region,
	})

	// Check if bucket exists and has versioning enabled
	resp, err := s3Client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(config.Bucket),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket versioning: %w", err)
	}
	isEnabled := resp.Status == types.BucketVersioningStatusEnabled
	if !isEnabled {
		return nil, errors.New("bucket versioning is not enabled")
	}

	return &AWSS3FileManager{
		bucket:   config.Bucket,
		logger:   logger,
		s3Client: s3Client,
	}, nil
}

type AWSS3FileManager struct {
	bucket   string
	logger   *slog.Logger
	s3Client *s3.Client
}

func (m *AWSS3FileManager) UploadFile(ctx context.Context, filePath, objectKey string) (string, error) {
	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}
	fileSize := fileInfo.Size()

	m.logger.Debug("Uploading file to S3 from path",
		slog.String("bucket", m.bucket),
		slog.String("filePath", filePath),
		slog.String("objectKey", objectKey),
		slog.Int64("fileSize", fileSize))

	// Calculate part size based on file size
	var partSize int64
	switch {
	case fileSize < 16*1024*1024: // < 16MB
		partSize = 5 * 1024 * 1024 // 5MB minimum part size
	case fileSize < 100*1024*1024: // 16MB to 100MB
		partSize = 8 * 1024 * 1024 // 8MB parts
	case fileSize < 1024*1024*1024: // 100MB to 1GB
		partSize = 16 * 1024 * 1024 // 16MB parts
	default: // > 1GB
		partSize = 32 * 1024 * 1024 // 32MB parts
	}

	// Create a new Upload Manager for this operation
	uploader := manager.NewUploader(m.s3Client, func(u *manager.Uploader) {
		u.PartSize = partSize
	})

	// Perform the upload
	resp, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(objectKey),
		Body:   file,
		// StorageClass:      types.StorageClassDeepArchive,
		StorageClass:      types.StorageClassStandard,
		ChecksumAlgorithm: types.ChecksumAlgorithmCrc64nvme,
		Metadata: map[string]string{
			"UploadedBy":   "x-atlas/filebackup",
			"OriginalPath": filePath,
		},
	})

	// Handle errors
	if err != nil {
		var apiErr smithy.APIError
		var noBucket *types.NoSuchBucket
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "EntityTooLarge" {
			m.logger.Error("Error while uploading object. The object is too large.",
				slog.String("bucket", m.bucket))
		} else if errors.As(err, &noBucket) {
			m.logger.Error("Bucket does not exist",
				slog.String("bucket", m.bucket))
		} else {
			m.logger.Error("Couldn't upload file",
				slog.String("bucket", m.bucket),
				slog.String("objectKey", objectKey),
				slog.String("error", err.Error()))
		}
		return "", err
	}

	// Wait for object to exist to confirm upload
	err = s3.NewObjectExistsWaiter(m.s3Client).Wait(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(objectKey),
	}, time.Minute)
	if err != nil {
		m.logger.Warn("Failed attempt to wait for object to exist",
			slog.String("objectKey", objectKey))
	}

	// Return the version ID
	versionID := ""
	if resp.VersionID != nil {
		versionID = *resp.VersionID
		m.logger.Debug("File uploaded successfully with version ID",
			slog.String("objectKey", objectKey),
			slog.String("versionID", versionID))
	} else {
		m.logger.Debug("File uploaded successfully (versioning not enabled)",
			slog.String("objectKey", objectKey))
	}

	return versionID, nil
}
