package aws

import (
	"fmt"
	"os"
	"strings"
)

type ManifestItem struct {
	Bucket    string
	Key       string
	VersionID string
}

func ParseManifestFile(path string) ([]ManifestItem, error) {
	var items []ManifestItem

	// Check if the file exists
	if _, err := os.Stat(path); err != nil {
		return items, err
	}

	// Read file. Each line should have format: bucket,key,version_id
	data, err := os.ReadFile(path)
	if err != nil {
		return items, fmt.Errorf("failed to read manifest file: %w", err)
	}

	lines := string(data)
	for i, line := range strings.Split(lines, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			return items, fmt.Errorf("invalid manifest line %d. Must be in the format: bucket, key, version_id", i)
		}
		item := ManifestItem{
			Bucket:    strings.TrimSpace(parts[0]),
			Key:       strings.TrimSpace(parts[1]),
			VersionID: strings.TrimSpace(parts[2]),
		}
		items = append(items, item)
	}

	return items, nil
}
