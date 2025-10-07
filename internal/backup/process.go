package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/crypto/argon2"

	"github.com/tjmadonna/filebackup/internal/aws"
	"github.com/tjmadonna/filebackup/internal/database"
)

func processFileWorker(ctx context.Context, logger *slog.Logger, dbPath, secret, tempDir string, filesCh <-chan string, uploader *aws.AWSS3FileUploader, insertFileCh chan<- database.InsertFileItem) {
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
		case filePath, ok := <-filesCh:
			if !ok {
				// No more files
				logger.Info("No more files to process, shutting down")
				return
			}

			// Process the file
			err := processFile(ctx, filePath, secret, tempDir, readOnlyDB, uploader, insertFileCh)
			if err != nil {
				logger.Error("Error processing file",
					slog.String("file", filePath),
					slog.String("error", err.Error()))
			}

		case <-ctx.Done():
			// Context cancelled
			return
		}
	}
}

func processFile(ctx context.Context, filePath, secret, tempDir string, db *database.Database, uploader *aws.AWSS3FileUploader, insertFileCh chan<- database.InsertFileItem) error {
	// Get file info
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}

	// Check if file already exists in database
	exists, err := db.DoesFileExist(ctx, filePath, info.Size(), info.ModTime().Unix())
	if err != nil {
		return fmt.Errorf("database check failed for file %s: %w", filePath, err)
	}
	if exists {
		// File already exists with same attributes, skip it
		return nil
	}

	// Encrypt the file. Use a random filename in the temp directory
	encFileName, err := generateRandomURLEncodedString(16)
	if err != nil {
		return fmt.Errorf("failed to generate random filename for encryption: %w", err)
	}
	encFilePath := filepath.Join(tempDir, encFileName+".enc")
	err = encryptFile(ctx, filePath, encFilePath, secret)
	if err != nil {
		return fmt.Errorf("failed to encrypt file %s: %w", filePath, err)
	}
	defer os.Remove(encFilePath)

	// Upload to S3
	awsVersionID, err := uploader.UploadFile(ctx, encFilePath, filePath)
	if err != nil {
		return fmt.Errorf("failed to upload file %s to S3: %w", filePath, err)
	}

	// Send file info to insert worker
	insertFileCh <- database.InsertFileItem{
		AWSVersionID:   awsVersionID,
		LastModifiedAt: info.ModTime().Unix(),
		Path:           filePath,
		Size:           info.Size(),
	}

	return nil
}

func encryptFile(ctx context.Context, inPath, outPath, secret string) error {
	// 64GB Limit, NIST Special Publication 800-38D section 5.2.1.1)
	// Limit in crypto library is 2**31 - 1 byte
	chunkSize := 1 << 30 // 1GB
	salt, err := generateRandomBytes(16)
	if err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	// Generate an encryption key and salt for each file
	// The Argon2id variant with t=3 and 64 MiB memory is the SECOND RECOMMENDED option
	// RFC 9106 Argon2 Memory-Hard Function for Password Hashing and Proof-of-Work Applications
	// Use SECOND RECOMMENDED option since multiple goroutines are used in file processing (FIRST RECOMMENDED is 2 GiB memory)
	key := argon2.IDKey([]byte(secret), salt, 3, 64*1024, uint8(runtime.NumCPU()), 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Open the input file for reading and the output file for writing
	inputFile, err := os.Open(inPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer inputFile.Close()

	// Create the output file path
	outputFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Write the salt at the beginning of the output file
	if _, err = outputFile.Write(salt); err != nil {
		return fmt.Errorf("failed to write salt to output file: %w", err)
	}

	// Calculate the MD5 hash of the original file for integrity verification
	checksumHash := md5.New()

	// Process the file in chunks
	buffer := make([]byte, chunkSize)
	for {
		// Check if context in chunks
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read a chunk from the input file
		bytesRead, err := inputFile.Read(buffer)
		if err != nil {
			if err == io.EOF {
				// Reached end of file
				break
			}
			return fmt.Errorf("error reading input file: %w", err)
		}

		// If we read fewer bytes than the buffer size, resize the buffer
		if bytesRead < len(buffer) {
			buffer = buffer[:bytesRead]
		}

		// Update the checksum
		checksumHash.Write(buffer)

		// Generate a unique nonce for each chunk
		nonce, err := generateRandomBytes(aesgcm.NonceSize())
		if err != nil {
			return fmt.Errorf("failed to generate nonce: %w", err)
		}

		// Encrypt the chunk
		ciphertext := aesgcm.Seal(nil, nonce, buffer, nil)

		// Format: [nonce][length of ciphertext as uint64][ciphertext]
		// Write the nonce
		if _, err = outputFile.Write(nonce); err != nil {
			return fmt.Errorf("failed to write nonce: %w", err)
		}

		// Write the length of the ciphertext as a uint64 (8 bytes)
		lengthBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(lengthBytes, uint64(len(ciphertext)))
		if _, err = outputFile.Write(lengthBytes); err != nil {
			return fmt.Errorf("failed to write ciphertext length: %w", err)
		}

		// Write the ciphertext
		if _, err = outputFile.Write(ciphertext); err != nil {
			return fmt.Errorf("failed to write ciphertext: %w", err)
		}

		// If we read less than a full buffer, we've reached the end of the file
		if bytesRead < chunkSize {
			break
		}
	}

	inputFile.Close()
	checksum := checksumHash.Sum(nil)
	return checkIntegrity(ctx, outputFile, key, checksum)
}

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

	// Read the salt from the beginning of the file
	salt := make([]byte, 16)
	if _, err := io.ReadFull(encFile, salt); err != nil {
		return fmt.Errorf("failed to read salt from encrypted file: %w", err)
	}

	for {
		// Check if context is done
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read the nonce
		nonce := make([]byte, 12) // AES-GCM standard nonce size
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

	// Calculate the MD5 hash of the decrypted file
	if _, err := decFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start of decrypted file: %w", err)
	}
	calculatedMD5 := md5.New()
	if _, err := io.Copy(calculatedMD5, decFile); err != nil {
		return fmt.Errorf("failed to calculate MD5 of decrypted file: %w", err)
	}
	actualHash := calculatedMD5.Sum(nil)

	if !bytes.Equal(actualHash, expectedHash) {
		return fmt.Errorf("integrity check failed: expected MD5 %x, got %x", expectedHash, actualHash)
	}

	return nil
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
