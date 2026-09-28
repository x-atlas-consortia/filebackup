package core

import (
	"context"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
)

// DecryptToWriter decrypts an encrypted file and writes the plaintext to out. It auto-detects whether the file uses
// the current headered format (FormatMagic prefix) or the legacy unheadered format, and decrypts accordingly.
func DecryptToWriter(ctx context.Context, r io.ReadSeeker, secret string, out io.Writer) error {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start: %w", err)
	}

	prefix := make([]byte, magicSize)
	if _, err := io.ReadFull(r, prefix); err != nil {
		return fmt.Errorf("failed to read format prefix: %w", err)
	}

	if string(prefix) == FormatMagic {
		return decryptHeadered(ctx, r, secret, out)
	}
	return decryptLegacy(ctx, r, prefix, secret, out)
}

// streamSize returns the total size of r without disturbing the caller's
// current read position.
func streamSize(r io.ReadSeeker) (int64, error) {
	cur, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := r.Seek(cur, io.SeekStart); err != nil {
		return 0, err
	}
	return end, nil
}

// decryptHeadered decrypts a file in the current header + chunk format. Derives the key from secret and the
// header's stored Argon2 parameters.
func decryptHeadered(ctx context.Context, r io.ReadSeeker, secret string, out io.Writer) error {
	headerBuf, header, totalSize, err := readHeader(r)
	if err != nil {
		return err
	}

	key := NewArgon2IDKey(secret, header.Salt, header.Argon2Time, header.Argon2MemoryKiB, header.Argon2Threads)
	defer ZeroBytes(key) // remove this copy of the derived key once decryptHeadered returns
	aesgcm, err := NewAESGCMCipher(key)
	if err != nil {
		return err
	}

	return decryptHeaderedChunks(ctx, r, headerBuf, header, aesgcm, totalSize, out)
}

// DecryptHeaderedWithKey decrypts a file already known to be in the current headered format, using a supplied key
// instead of deriving one from a secret. This prevents an additional Argon2id key derivation when the key is
// already available.
func DecryptHeaderedWithKey(ctx context.Context, r io.ReadSeeker, key []byte, out io.Writer) error {
	headerBuf, header, totalSize, err := readHeader(r)
	if err != nil {
		return err
	}

	aesgcm, err := NewAESGCMCipher(key)
	if err != nil {
		return err
	}

	return decryptHeaderedChunks(ctx, r, headerBuf, header, aesgcm, totalSize, out)
}

// readHeader seeks to the start of r, reads and parses the header, and returns the raw header bytes
// (needed as AAD for every chunk), the parsed Header, and the total size of r.
func readHeader(r io.ReadSeeker) (headerBuf []byte, header Header, totalSize int64, err error) {
	totalSize, err = streamSize(r)
	if err != nil {
		return nil, Header{}, 0, fmt.Errorf("failed to determine file size: %w", err)
	}

	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return nil, Header{}, 0, fmt.Errorf("failed to seek to start: %w", err)
	}
	headerBuf = make([]byte, HeaderSize)
	if _, err = io.ReadFull(r, headerBuf); err != nil {
		return nil, Header{}, 0, fmt.Errorf("failed to read header: %w", err)
	}
	header, err = ParseHeader(headerBuf)
	if err != nil {
		return nil, Header{}, 0, err
	}
	return headerBuf, header, totalSize, nil
}

// decryptHeaderedChunks reads and decrypts the chunk stream following the header, using aesgcm
// (already initialized with the correct key) and headerBuf as authenticated data.
func decryptHeaderedChunks(ctx context.Context, r io.Reader, headerBuf []byte, header Header, aesgcm cipher.AEAD, totalSize int64, out io.Writer) error {
	dataSize := totalSize - int64(HeaderSize)
	storedChunkSize := int64(header.ChunkSize) + gcmTagSize
	if dataSize <= 0 {
		return fmt.Errorf("invalid encrypted file: no chunk data (data size %d)", dataSize)
	}

	// Chunk boundaries are derived from the total data size rather than stored per-chunk. Every chunk but the last is
	// exactly chunkSize plaintext bytes, so its on-disk size is fixed at chunkSize+16 (GCM tag). This must mirror the
	// chunking logic used at encryption time.
	numFullChunks := dataSize / storedChunkSize
	remainder := dataSize % storedChunkSize
	numChunks := numFullChunks
	lastStoredSize := storedChunkSize
	if remainder != 0 {
		numChunks++
		lastStoredSize = remainder
	}

	// A valid chunk must be at least gcmTagSize bytes: even an empty-plaintext chunk produces a 16-byte GCM tag.
	// Anything shorter means the file was truncated mid-chunk and report it clearly.
	if lastStoredSize < gcmTagSize {
		return fmt.Errorf("encrypted file appears truncated: final chunk is %d bytes, need at least %d for the GCM authentication tag", lastStoredSize, gcmTagSize)
	}

	buf := make([]byte, storedChunkSize)
	for chunkIndex := range numChunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		size := storedChunkSize
		isLast := chunkIndex == numChunks-1
		if isLast {
			size = lastStoredSize
		}

		if _, err := io.ReadFull(r, buf[:size]); err != nil {
			return fmt.Errorf("failed to read chunk %d: %w", chunkIndex, err)
		}

		nonce := ChunkNonce(header.NoncePrefix, uint32(chunkIndex), isLast)
		plaintext, err := aesgcm.Open(nil, nonce, buf[:size], headerBuf)
		if err != nil {
			return fmt.Errorf("failed to decrypt chunk %d: %w", chunkIndex, err)
		}
		if _, err := out.Write(plaintext); err != nil {
			ZeroBytes(plaintext)
			return fmt.Errorf("failed to write decrypted chunk %d: %w", chunkIndex, err)
		}
		ZeroBytes(plaintext)
	}
	return nil
}

// decryptLegacy decrypts a file in the pre-header format: a 16-byte salt followed by
// [12-byte nonce][8-byte little-endian ciphertext length][ciphertext] chunks, with no additional authenticated data.
func decryptLegacy(ctx context.Context, r io.Reader, prefixAlreadyRead []byte, secret string, out io.Writer) error {
	rest := make([]byte, saltSize-len(prefixAlreadyRead))
	if _, err := io.ReadFull(r, rest); err != nil {
		return fmt.Errorf("failed to read legacy salt: %w", err)
	}
	salt := append(append([]byte{}, prefixAlreadyRead...), rest...)

	key := LegacyArgon2IDKey(secret, salt)
	defer ZeroBytes(key) // remove this copy of the derived key once decryptLegacy returns
	aesgcm, err := NewAESGCMCipher(key)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		nonce := make([]byte, aesgcm.NonceSize())
		if _, err := io.ReadFull(r, nonce); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("failed to read nonce: %w", err)
		}

		lengthBytes := make([]byte, 8)
		if _, err := io.ReadFull(r, lengthBytes); err != nil {
			return fmt.Errorf("failed to read ciphertext length: %w", err)
		}
		ciphertextLength := binary.LittleEndian.Uint64(lengthBytes)

		ciphertext := make([]byte, ciphertextLength)
		if _, err := io.ReadFull(r, ciphertext); err != nil {
			return fmt.Errorf("failed to read ciphertext: %w", err)
		}

		plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return fmt.Errorf("failed to decrypt legacy chunk: %w", err)
		}
		if _, err := out.Write(plaintext); err != nil {
			ZeroBytes(plaintext)
			return fmt.Errorf("failed to write decrypted chunk: %w", err)
		}
		ZeroBytes(plaintext)
	}
}
