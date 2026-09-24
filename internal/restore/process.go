package restore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/database"
)

// processManifestItemWorker processes manifest items from the provided channel.
func processManifestItemWorker(ctx context.Context, logger *slog.Logger, dbPath, secret, outDir, tempDir string, itemCh <-chan aws.ManifestItem, downloader *aws.AWSS3FileManager) int {
	// Read mode database
	readOnlyDB, err := database.New(ctx, dbPath, true)
	if err != nil {
		logger.Error("Error opening database in read-only mode", slog.String("error", err.Error()))
		return 0
	}
	defer func() {
		if closeErr := readOnlyDB.Close(ctx); closeErr != nil {
			logger.Error("Error closing database", slog.String("error", closeErr.Error()))
		}
	}()

	var failed int
	for {
		select {
		case item, ok := <-itemCh:
			if !ok {
				// No more manifest items
				logger.Info("No more manifest items to process, shutting down")
				return failed
			}

			// Process the manifest item
			err := processManifestItem(ctx, item, outDir, secret, tempDir, readOnlyDB, downloader)
			if err != nil {
				failed++
				logger.Error("Error processing manifest item",
					slog.String("bucket", item.Bucket),
					slog.String("key", item.Key),
					slog.String("version_id", item.VersionID),
					slog.String("error", err.Error()))
			}

		case <-ctx.Done():
			// Context cancelled
			return failed
		}
	}
}

// processManifestItem processes a single manifest item by downloading, decrypting, and verifying it.
func processManifestItem(ctx context.Context, item aws.ManifestItem, outDir, secret, tempDir string, db *database.Database, downloader *aws.AWSS3FileManager) error {
	// Create a temporary file path to download the object, it will be encrypted
	tmpFileName, err := core.GenerateRandomURLEncodedString(16)
	if err != nil {
		return fmt.Errorf("failed to generate random filename for download: %w", err)
	}
	tmpFilePath := filepath.Join(tempDir, tmpFileName+".enc")
	defer os.Remove(tmpFilePath)

	// Download the file from S3 to the temporary file path
	err = downloader.DownloadFile(ctx, item, tmpFilePath)
	if err != nil {
		return fmt.Errorf("failed to download file from S3: %w", err)
	}

	// Determine the output path
	itemKey := strings.TrimPrefix(item.Key, "/")
	outPath := filepath.Join(outDir, itemKey)
	err = os.MkdirAll(filepath.Dir(outPath), 0o755)
	if err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Decrypt the file
	checksum, err := decryptFile(ctx, tmpFilePath, outPath, secret)
	if err != nil {
		return fmt.Errorf("failed to decrypt file: %w", err)
	}

	// Verify checksum
	dbChecksum, err := db.GetSHA256(ctx, item.Key, item.VersionID)
	if err != nil || dbChecksum == "" {
		return fmt.Errorf("failed to get checksum from database: %w", err)
	}
	if checksum != dbChecksum {
		os.Remove(outPath)
		return fmt.Errorf("checksum mismatch for file %s (expected %s, got %s)", item.Key, dbChecksum, checksum)
	}

	return nil
}

// decryptFile decrypts the input file and writes the decrypted content to the output file. core.DecryptToWriter
// auto-detects whether the input is the current headered format or the legacy pre-header format, so files backed up
// before the format change remain restorable.
func decryptFile(ctx context.Context, inPath, outPath, secret string) (checksum string, err error) {
	inputFile, err := os.Open(inPath)
	if err != nil {
		return "", fmt.Errorf("failed to open input file for decryption: %w", err)
	}
	defer inputFile.Close()

	outputFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file for decryption: %w", err)
	}
	// Close then remove on any error so callers never encounter a partial output file. Keep the file on success.
	defer func() {
		outputFile.Close()
		if err != nil {
			os.Remove(outPath)
		}
	}()

	// Calculate the SHA256 hash of the decrypted content for integrity verification, streaming it alongside the write
	// to outputFile rather than hashing in a second pass.
	checksumHash := sha256.New()
	if err = core.DecryptToWriter(ctx, inputFile, secret, io.MultiWriter(outputFile, checksumHash)); err != nil {
		return "", fmt.Errorf("failed to decrypt file: %w", err)
	}

	return fmt.Sprintf("%x", checksumHash.Sum(nil)), nil
}
