package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/x-atlas-consortia/filebackup/internal/aws"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"github.com/x-atlas-consortia/filebackup/internal/database"
)

// chunkSize is the plaintext size of each encrypted chunk. 64GB limit (NIST SP 800-38D 5.2.1.1).
// The Go crypto library limits a single AES-GCM seal to 2**31-1 bytes, so 16MiB chunks are used regardless.
const chunkSize = 16 << 20 // 16 MiB

// processFileWorker processes files: encrypts, uploads to S3, and sends info for database insertion.
func processFileWorker(ctx context.Context, logger *slog.Logger, dbPath, secret, tempDir string, filesCh <-chan string, uploader *aws.AWSS3FileManager, insertFileCh chan<- database.InsertFileItem, walkedPathCh chan<- string) {
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
		case filePath, ok := <-filesCh:
			if !ok {
				// No more files
				logger.Info("No more files to process, shutting down")
				return
			}

			// Mark as seen regardless of upload outcome. The file still exists on disk
			// even if processing it below fails, so it must not be flagged as deleted.
			select {
			case walkedPathCh <- filePath:
			case <-ctx.Done():
				return
			}

			// Process the file
			uploaded, err := processFile(ctx, filePath, secret, tempDir, readOnlyDB, uploader, insertFileCh)
			if err != nil {
				logger.Error("Error processing file",
					slog.String("file", filePath),
					slog.String("error", err.Error()))
			}
			if uploaded {
				logger.Info("Uploaded file to S3", slog.String("file", filePath))
			}

		case <-ctx.Done():
			// Context cancelled
			return
		}
	}
}

// processFile encrypts a file, uploads it to S3, and sends its info for database insertion.
func processFile(ctx context.Context, filePath, secret, tempDir string, db *database.Database, uploader *aws.AWSS3FileManager, insertFileCh chan<- database.InsertFileItem) (bool, error) {
	// Get file info
	info, err := os.Stat(filePath)
	if err != nil {
		return false, fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}
	lastModifiedAt := info.ModTime().UTC() // Use UTC for consistency

	// Check if file already exists in database
	exists, err := db.DoesFileExist(ctx, filePath, info.Size(), lastModifiedAt.Unix())
	if err != nil {
		return false, fmt.Errorf("database check failed for file %s: %w", filePath, err)
	}
	if exists {
		// File already exists with same attributes, skip it
		return false, nil
	}

	// Encrypt the file. Use a random filename in the temp directory
	encFileName, err := core.GenerateRandomURLEncodedString(16)
	if err != nil {
		return false, fmt.Errorf("failed to generate random filename for encryption: %w", err)
	}
	encFilePath := filepath.Join(tempDir, encFileName+".enc")
	defer os.Remove(encFilePath) // clean up whether encryption succeeds or fails
	checksum, err := encryptFile(ctx, filePath, encFilePath, secret)
	if err != nil {
		return false, fmt.Errorf("failed to encrypt file %s: %w", filePath, err)
	}
	awsVersionID, err := uploader.UploadFile(ctx, encFilePath, filePath, filePath, types.StorageClassDeepArchive, lastModifiedAt)
	if err != nil {
		return false, fmt.Errorf("failed to upload file %s to S3: %w", filePath, err)
	}

	// Send file info to insert worker
	insertFileCh <- database.InsertFileItem{
		AWSVersionID:   awsVersionID,
		LastModifiedAt: lastModifiedAt.Unix(),
		Path:           filePath,
		SHA256:         checksum,
		Size:           info.Size(),
	}

	return true, nil
}

// encryptFile encrypts the input file and writes [header][chunks...] to the output file.
// Each chunk is sealed with the serialized header as additional authenticated data, so
// any change to the header (salt, Argon2 parameters, nonce prefix, or chunk size) is detected
// on decryption.
func encryptFile(ctx context.Context, inPath, outPath, secret string) (string, error) {
	inputFile, err := os.Open(inPath)
	if err != nil {
		return "", fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	info, err := inputFile.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat input file: %w", err)
	}
	plaintextSize := info.Size()

	salt, err := core.GenerateRandomBytes(16)
	if err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}
	noncePrefix, err := core.GenerateRandomBytes(7)
	if err != nil {
		return "", fmt.Errorf("failed to generate nonce prefix: %w", err)
	}

	key := core.NewArgon2IDKey(secret, salt, core.Argon2Time, core.Argon2MemoryKiB, core.Argon2Parallelism)
	defer core.ZeroBytes(key) // removes this copy of the derived key once encryptFile returns
	aesgcm, err := core.NewAESGCMCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES-GCM cipher: %w", err)
	}

	header := core.Header{
		Salt:            salt,
		Argon2Time:      core.Argon2Time,
		Argon2MemoryKiB: core.Argon2MemoryKiB,
		Argon2Threads:   core.Argon2Parallelism,
		NoncePrefix:     noncePrefix,
		ChunkSize:       chunkSize,
	}
	headerBytes := header.Bytes()

	outputFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	if _, err = outputFile.Write(headerBytes); err != nil {
		return "", fmt.Errorf("failed to write header to output file: %w", err)
	}

	// Calculate the SHA256 hash of the original file for integrity verification
	checksumHash := sha256.New()
	numChunks := numChunksForSize(plaintextSize, chunkSize)

	buffer := make([]byte, chunkSize)
	defer core.ZeroBytes(buffer) // clear any plaintext remaining in the buffer, particularly the last partial chunk
	for chunkIndex := range numChunks {
		// Check for cancellation between chunks
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		n, err := io.ReadFull(inputFile, buffer)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return "", fmt.Errorf("error reading input file: %w", err)
		}
		plaintext := buffer[:n]
		checksumHash.Write(plaintext)

		isLast := chunkIndex == numChunks-1
		nonce := core.ChunkNonce(noncePrefix, chunkIndex, isLast)
		// Seal appends the 16-byte GCM tag to the returned ciphertext, matching the
		// on-disk chunk format of ciphertext || tag.
		ciphertext := aesgcm.Seal(nil, nonce, plaintext, headerBytes)

		if _, err = outputFile.Write(ciphertext); err != nil {
			return "", fmt.Errorf("failed to write chunk %d: %w", chunkIndex, err)
		}
	}

	checksum := checksumHash.Sum(nil)

	if err := checkIntegrity(ctx, outputFile, key, checksum); err != nil {
		return "", fmt.Errorf("integrity check failed: %w", err)
	}

	return fmt.Sprintf("%x", checksum), nil
}

// numChunksForSize returns the number of chunks a file of the given plaintext size splits into. Empty files still
// produce exactly one empty chunk, so every encrypted file has at least one authenticated chunk following the header.
func numChunksForSize(size int64, chunkSize int) uint32 {
	if size <= 0 {
		return 1
	}
	n := size / int64(chunkSize)
	if size%int64(chunkSize) != 0 {
		n++
	}
	return uint32(n)
}

// checkIntegrity decrypts the encrypted file via the same chunk-decoding path used for restores and verifies its
// SHA256 hash matches expectedHash.
func checkIntegrity(ctx context.Context, encFile *os.File, key []byte, expectedHash []byte) error {
	calculatedHash := sha256.New()
	if err := core.DecryptHeaderedWithKey(ctx, encFile, key, calculatedHash); err != nil {
		return err
	}
	actualHash := calculatedHash.Sum(nil)

	if !bytes.Equal(actualHash, expectedHash) {
		return fmt.Errorf("integrity check failed: expected SHA256 %x, got %x", expectedHash, actualHash)
	}

	return nil
}
