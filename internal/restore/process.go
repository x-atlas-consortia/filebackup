package restore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
func processManifestItemWorker(ctx context.Context, logger *slog.Logger, dbPath, secret, outDir, tempDir string, itemCh <-chan aws.ManifestItem, downloader *aws.AWSS3FileManager) {
	// Read mode database
	readOnlyDB, err := database.New(ctx, dbPath, true)
	if err != nil {
		logger.Error("Error opening database in read-only mode", slog.String("error", err.Error()))
		return
	}
	defer func() {
		if closeErr := readOnlyDB.Close(ctx); closeErr != nil {
			logger.Error("Error closing database", slog.String("error", closeErr.Error()))
		}
	}()

	for {
		select {
		case item, ok := <-itemCh:
			if !ok {
				// No more manifest items
				logger.Info("No more manifest items to process, shutting down")
				return
			}

			// Process the manifest item
			err := processManifestItem(ctx, item, outDir, secret, tempDir, readOnlyDB, downloader)
			if err != nil {
				logger.Error("Error processing manifest item",
					slog.String("bucket", item.Bucket),
					slog.String("key", item.Key),
					slog.String("version_id", item.VersionID),
					slog.String("error", err.Error()))
			}

		case <-ctx.Done():
			// Context cancelled
			return
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

// decryptFile decrypts the input file and writes the decrypted content to the output file.
func decryptFile(ctx context.Context, inPath, outPath, secret string) (string, error) {
	inputFile, err := os.Open(inPath)
	if err != nil {
		return "", fmt.Errorf("failed to open input file for decryption: %w", err)
	}
	defer inputFile.Close()

	outputFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file for decryption: %w", err)
	}
	defer outputFile.Close()

	// Generate the encryption key
	salt := make([]byte, 16)
	if _, err := io.ReadFull(inputFile, salt); err != nil {
		return "", fmt.Errorf("failed to read salt from input file: %w", err)
	}
	key := core.NewArgon2IDKey(secret, salt)
	aesgcm, err := core.NewAESGCMCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES-GCM cipher: %w", err)
	}

	// Calculate the SHA256 hash of the original file for integrity verification
	checksumHash := sha256.New()

	for {
		// Check the context
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// Read the nonce (ensure full read)
		nonce := make([]byte, aesgcm.NonceSize())
		if _, err := io.ReadFull(inputFile, nonce); err != nil {
			// io.ReadFull returns io.EOF if no bytes were read (end of file)
			// and io.ErrUnexpectedEOF for partial reads.
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("failed to read nonce from input file: %w", err)
		}

		// Read the length of the ciphertext
		lenBuf := make([]byte, 8)
		if _, err := io.ReadFull(inputFile, lenBuf); err != nil {
			return "", fmt.Errorf("failed to read ciphertext length from input file: %w", err)
		}
		cypherLength := binary.LittleEndian.Uint64(lenBuf)

		// Read the ciphertext
		if cypherLength == 0 {
			return "", fmt.Errorf("ciphertext length is zero")
		}
		ciphertext := make([]byte, cypherLength)
		if _, err := io.ReadFull(inputFile, ciphertext); err != nil {
			return "", fmt.Errorf("failed to read ciphertext from input file: %w", err)
		}

		// Decrypt the chunk
		plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt chunk: %w", err)
		}

		// Update the checksum
		if _, err := checksumHash.Write(plaintext); err != nil {
			return "", fmt.Errorf("failed to update checksum: %w", err)
		}

		// Write the plaintext to the output file
		if _, err := outputFile.Write(plaintext); err != nil {
			return "", fmt.Errorf("failed to write plaintext to output file: %w", err)
		}
	}

	return fmt.Sprintf("%x", checksumHash.Sum(nil)), nil
}
