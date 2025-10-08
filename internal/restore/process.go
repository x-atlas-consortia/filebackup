package restore

import (
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
	"strings"

	"github.com/tjmadonna/filebackup/internal/aws"
	"golang.org/x/crypto/argon2"
)

func processManifestItemWorker(ctx context.Context, logger *slog.Logger, secret, outDir, tempDir string, itemCh <-chan aws.ManifestItem, downloader *aws.AWSS3FileManager) {
	for {
		select {
		case item, ok := <-itemCh:
			if !ok {
				// No more manifest items
				logger.Info("No more manifest items to process, shutting down")
				return
			}

			// Process the manifest item
			err := processManifestItem(ctx, item, outDir, secret, tempDir, downloader)
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

func processManifestItem(ctx context.Context, item aws.ManifestItem, outDir, secret, tempDir string, downloader *aws.AWSS3FileManager) error {
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
	_, err = decryptFile(ctx, tmpFilePath, outPath, secret)
	if err != nil {
		return fmt.Errorf("failed to decrypt file: %w", err)
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

	// Calculate the MD5 hash of the original file for integrity verification
	checksumHash := md5.New()

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
