package aws

import (
	"errors"
	"slices"
	"strings"
)

// IsS3Path checks if the given path is a valid S3 path.
func IsS3Path(s3Path string) bool {
	return strings.HasPrefix(s3Path, "s3://")
}

// ParseS3Path parses the given S3 path into bucket and key components.
func ParseS3Path(s3Path string) (bucket string, key string, err error) {
	err = validateS3Path(s3Path)
	if err != nil {
		return "", "", err
	}

	s3Path = strings.TrimPrefix(s3Path, "s3://")
	parts := strings.SplitN(s3Path, "/", 2)

	return parts[0], parts[1], nil
}

// validateS3Path validates the structure of the S3 path.
func validateS3Path(s3Path string) error {
	if !strings.HasPrefix(s3Path, "s3://") {
		return errors.New("invalid S3 path: missing 's3://' prefix")
	}

	s3Path = strings.TrimPrefix(s3Path, "s3://")

	parts := strings.Split(s3Path, "/")
	if len(parts) < 2 {
		return errors.New("invalid S3 path: must contain bucket and key")
	}
	if slices.Contains(parts, "") {
		return errors.New("invalid S3 path: contains empty segments")
	}
	return nil
}
