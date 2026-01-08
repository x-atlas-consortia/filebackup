package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
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

// processFileWorker processes files: encrypts, uploads to S3, and sends info for database insertion.
func processFileWorker(ctx context.Context, logger *slog.Logger, dbPath, secret, tempDir string, filesCh <-chan string, uploader *aws.AWSS3FileManager, insertFileCh chan<- database.InsertFileItem) {
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
	checksum, err := encryptFile(ctx, filePath, encFilePath, secret)
	if err != nil {
		return false, fmt.Errorf("failed to encrypt file %s: %w", filePath, err)
	}
	defer os.Remove(encFilePath)

	// Upload to S3
	awsVersionID, err := uploader.UploadFile(ctx, encFilePath, filePath, types.StorageClassDeepArchive, lastModifiedAt)
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

// encryptFile encrypts the input file and writes the encrypted data to the output file.
func encryptFile(ctx context.Context, inPath, outPath, secret string) (string, error) {
	// 64GB Limit, NIST Special Publication 800-38D section 5.2.1.1)
	// Limit in crypto library is 2**31 - 1 byte
	chunkSize := 16 << 20 // 16 MiB
	salt, err := core.GenerateRandomBytes(16)
	if err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	// Generate the encryption key
	key := core.NewArgon2IDKey(secret, salt)
	aesgcm, err := core.NewAESGCMCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES-GCM cipher: %w", err)
	}

	// Open the input file for reading and the output file for writing
	inputFile, err := os.Open(inPath)
	if err != nil {
		return "", fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	// Create the output file path
	outputFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Write the salt at the beginning of the output file
	if _, err = outputFile.Write(salt); err != nil {
		return "", fmt.Errorf("failed to write salt to output file: %w", err)
	}

	// Calculate the SHA256 hash of the original file for integrity verification
	checksumHash := sha256.New()

	// Process the file in chunks
	buffer := make([]byte, chunkSize)
	for {
		// Check if context in chunks
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// Read a chunk from the input file
		bytesRead, err := inputFile.Read(buffer)
		if err != nil {
			if err == io.EOF {
				// Reached end of file
				break
			}
			return "", fmt.Errorf("error reading input file: %w", err)
		}

		// If we read fewer bytes than the buffer size, resize the buffer
		if bytesRead < len(buffer) {
			buffer = buffer[:bytesRead]
		}

		// Update the checksum
		checksumHash.Write(buffer)

		// Generate a unique nonce for each chunk
		nonce, err := core.GenerateRandomBytes(aesgcm.NonceSize())
		if err != nil {
			return "", fmt.Errorf("failed to generate nonce: %w", err)
		}

		// Encrypt the chunk
		ciphertext := aesgcm.Seal(nil, nonce, buffer, nil)

		// Format: [nonce][length of ciphertext as uint64][ciphertext]
		// Write the nonce
		if _, err = outputFile.Write(nonce); err != nil {
			return "", fmt.Errorf("failed to write nonce: %w", err)
		}

		// Write the length of the ciphertext as a uint64 (8 bytes)
		lengthBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(lengthBytes, uint64(len(ciphertext)))
		if _, err = outputFile.Write(lengthBytes); err != nil {
			return "", fmt.Errorf("failed to write ciphertext length: %w", err)
		}

		// Write the ciphertext
		if _, err = outputFile.Write(ciphertext); err != nil {
			return "", fmt.Errorf("failed to write ciphertext: %w", err)
		}

		// If we read less than a full buffer, we've reached the end of the file
		if bytesRead < chunkSize {
			break
		}
	}

	// Integrity check
	inputFile.Close()
	checksum := checksumHash.Sum(nil)
	err = checkIntegrity(ctx, outputFile, key, checksum)
	if err != nil {
		return "", fmt.Errorf("integrity check failed: %w", err)
	}

	return fmt.Sprintf("%x", checksum), nil
}

// checkIntegrity decrypts the encrypted file and verifies its SHA256 hash matches the expected hash.
func checkIntegrity(ctx context.Context, encFile *os.File, encKey, expectedHash []byte) error {
	// Recreate the AES-GCM cipher
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Create the decrypted file. Append .dec to the encrypted file path
	decFilePath := encFile.Name() + ".dec"
	decFile, err := os.Create(decFilePath)
	if err != nil {
		return fmt.Errorf("failed to create decrypted file: %w", err)
	}
	defer func() {
		// Close and remove the decrypted file
		_ = decFile.Close()
		_ = os.Remove(decFilePath)
	}()

	// Go to the beginning of the encrypted file
	if _, err := encFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start of encrypted file: %w", err)
	}

	// Skip the salt (16 bytes) written at the start of the file
	if _, err := encFile.Seek(16, io.SeekCurrent); err != nil {
		return fmt.Errorf("failed to skip salt in encrypted file: %w", err)
	}

	for {
		// Check if context is done
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read the nonce
		nonce := make([]byte, aesgcm.NonceSize())
		if _, err := io.ReadFull(encFile, nonce); err != nil {
			if err == io.EOF {
				// Reached end of file
				break
			}
			return fmt.Errorf("failed to read nonce: %w", err)
		}

		// Read the length of the ciphertext
		lengthBytes := make([]byte, 8)
		if _, err := io.ReadFull(encFile, lengthBytes); err != nil {
			return fmt.Errorf("failed to read ciphertext length: %w", err)
		}
		ciphertextLength := binary.LittleEndian.Uint64(lengthBytes)

		// Read the ciphertext
		ciphertext := make([]byte, ciphertextLength)
		if _, err := io.ReadFull(encFile, ciphertext); err != nil {
			return fmt.Errorf("failed to read ciphertext: %w", err)
		}

		// Decrypt the chunk
		plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return fmt.Errorf("failed to decrypt chunk: %w", err)
		}

		// Write the decrypted chunk to the decrypted file
		if _, err := decFile.Write(plaintext); err != nil {
			return fmt.Errorf("failed to write decrypted chunk: %w", err)
		}
	}

	// Calculate the SHA256 hash of the decrypted file
	if _, err := decFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start of decrypted file: %w", err)
	}
	calculatedHash := sha256.New()
	if _, err := io.Copy(calculatedHash, decFile); err != nil {
		return fmt.Errorf("failed to calculate SHA256 of decrypted file: %w", err)
	}
	actualHash := calculatedHash.Sum(nil)

	if !bytes.Equal(actualHash, expectedHash) {
		return fmt.Errorf("integrity check failed: expected SHA256 %x, got %x", expectedHash, actualHash)
	}

	return nil
}
