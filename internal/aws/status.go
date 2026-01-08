package aws

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectRestoreStatus returns the raw Restore header, whether a restore is ongoing, and optional expiry time.
func (m *AWSS3FileManager) ObjectRestoreStatus(ctx context.Context, objectKey string) (restored bool, expiry *time.Time, err error) {
	out, err := m.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return false, nil, err
	}
	if out.Restore == nil {
		return false, nil, errors.New("object is not archived")
	}
	raw := *out.Restore
	restored, expiry = parseRestoreHeader(raw)
	return restored, expiry, nil
}

// parseRestoreHeader parses the S3 Restore header. This function extracts information in the 'x-amz-restore' header. https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html
// If an archive copy is already restored, the header value indicates when Amazon S3 is scheduled to delete the object copy. For example: x-amz-restore: ongoing-request="false", expiry-date="Fri, 21 Dec 2012 00:00:00 GMT"
func parseRestoreHeader(raw string) (restored bool, expiry *time.Time) {
	restored = strings.Contains(raw, `ongoing-request="false"`)
	re := regexp.MustCompile(`expiry-date="([^"]+)"`)
	if m := re.FindStringSubmatch(raw); len(m) == 2 {
		if t, err := http.ParseTime(m[1]); err == nil {
			expiry = &t
		}
	}
	return
}
