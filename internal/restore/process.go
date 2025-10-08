package restore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tjmadonna/filebackup/internal/aws"
	"github.com/tjmadonna/filebackup/internal/database"
	"golang.org/x/crypto/argon2"
)

func processManifestItemWorker(ctx context.Context, logger *slog.Logger, dbPath, secret, outDir, tempDir string, itemCh <-chan aws.ManifestItem, downloader *aws.AWSS3FileManager) {
	// Read mode database
	readOnlyDB, err := database.New(ctx, dbPath, true)
	if err != nil {
		logger.Error("Error opening database in read-only mode", slog.String("error", err.Error()))
		return
	}
	defer func() {
		if closeErr := readOnlyDB.Close(); closeErr != nil {
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

func processManifestItem(ctx context.Context, item aws.ManifestItem, outDir, secret, tempDir string, db *database.Database, downloader *aws.AWSS3FileManager) error {
	// Create a temporary file path to download the object, it will be encrypted
	tmpFileName, err := generateRandomURLEncodedString(16)
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
	if !bytes.Equal(checksum, []byte(dbChecksum)) {
		os.Remove(outPath)
		return fmt.Errorf("checksum mismatch for file %s (expected %x, got %x)", item.Key, dbChecksum, checksum)
	}

	return nil
}

func decryptFile(ctx context.Context, inPath, outPath, secret string) ([]byte, error) {
	inputFile, err := os.Open(inPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file for decryption: %w", err)
	}
	defer inputFile.Close()

	outputFile, err := os.Create(outPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file for decryption: %w", err)
	}
	defer outputFile.Close()

	// Generate the encryption key
	salt := make([]byte, 16)
	n, err := inputFile.Read(salt)
	if err != nil || n != 16 {
		return nil, fmt.Errorf("failed to read salt from input file: %w", err)
	}
	key := argon2.IDKey([]byte(secret), salt, 3, 64*1024, uint8(runtime.NumCPU()), 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Calculate the SHA256 hash of the original file for integrity verification
	checksumHash := sha256.New()

	for {
		// Check the context
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Read the nonce
		nonce := make([]byte, aesgcm.NonceSize())
		n, err := inputFile.Read(nonce)
		if err != nil || n != len(nonce) {
			if err == io.EOF {
				// Reached end of file
				break
			}
			return nil, fmt.Errorf("failed to read nonce from input file: %w", err)
		}

		// Read the length of the ciphertext
		lenBuf := make([]byte, 8)
		n, err = inputFile.Read(lenBuf)
		if err != nil || n != len(lenBuf) {
			return nil, fmt.Errorf("failed to read ciphertext length from input file: %w", err)
		}
		cypherLength := binary.LittleEndian.Uint64(lenBuf)

		// Read the ciphertext
		ciphertext := make([]byte, cypherLength)
		n, err = inputFile.Read(ciphertext)
		if err != nil || uint64(n) != cypherLength {
			return nil, fmt.Errorf("failed to read ciphertext from input file: %w", err)
		}

		// Decrypt the chunk
		plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt chunk: %w", err)
		}

		// Update the checksum
		checksumHash.Write(plaintext)

		// Write the plaintext to the output file
		_, err = outputFile.Write(plaintext)
		if err != nil {
			return nil, fmt.Errorf("failed to write plaintext to output file: %w", err)
		}
	}

	return checksumHash.Sum(nil), nil
}

func generateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return b, nil
}

func generateRandomURLEncodedString(n int) (string, error) {
	b, err := generateRandomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
