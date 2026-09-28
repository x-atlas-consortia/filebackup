package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"runtime"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for key derivation. These match RFC 9106's "SECOND RECOMMENDED" option (m=64MiB, t=3, p=4) rather
// than the "FIRST RECOMMENDED" option (m=2GiB), since multiple files are processed concurrently.
const (
	Argon2Time        uint32 = 3
	Argon2MemoryKiB   uint32 = 64 * 1024
	Argon2Parallelism uint8  = 4
)

// Encrypted file format constants.
const (
	// FormatMagic identifies the current (headered) encryption format.
	FormatMagic = "X-FILEBACKUP"
	// FormatVersion is the current header layout version.
	FormatVersion byte = 1

	magicSize       = 12 // len(FormatMagic)
	saltSize        = 16
	noncePrefixSize = 7
	nonceSize       = 12 // noncePrefix(7) + chunk index(4) + last-chunk flag(1)
	gcmTagSize      = 16

	// HeaderSize is the total size of the on-disk header:
	// magic(12) + version(1) + salt(16) + time(4) + memory(4) + parallelism(1) + noncePrefix(7) + chunkSize(4)
	HeaderSize = magicSize + 1 + saltSize + 4 + 4 + 1 + noncePrefixSize + 4
)

// Header describes the per-file encryption parameters. It is written at the start of the encrypted file and
// authenticated as additional data on every chunk's AES-GCM seal/open, so tampering with any
// field (salt, Argon2 cost parameters, nonce prefix, chunk size) causes decryption to fail rather than silently using
// the wrong parameters.
//
// On-disk layout (49 bytes total, all multi-byte integers big-endian):
//
//	Offset  Size  Field                        Notes
//	------  ----  --------------------------   -----------------------------------
//	 0      12    Magic                        "X-FILEBACKUP", identifies this format
//	12       1    Version                      currently 1
//	13      16    Argon2id salt                random per file
//	29       4    Argon2id time cost
//	33       4    Argon2id memory cost (KiB)
//	37       1    Argon2id parallelism
//	38       7    AES-GCM nonce prefix         random per file; see ChunkNonce
//	45       4    Plaintext chunk size (bytes) size of every chunk but the last
//
// Following the header, the rest of the file is a sequence of chunks, each:
//
//	ciphertext (<= chunk size bytes) || 16-byte GCM authentication tag
//
// There is no per-chunk length field. Every chunk except the last is exactly ChunkSize plaintext bytes
// (ChunkSize+16 bytes on disk), and the last chunk is however many bytes remain. A reader determines the chunk
// boundaries from the total file size (see decryptHeadered), and the nonce's last-chunk flag (see ChunkNonce)
// ensures a truncated file fails authentication rather than decrypting as if the truncation point were the real end.
type Header struct {
	Salt            []byte // 16 bytes
	Argon2Time      uint32
	Argon2MemoryKiB uint32
	Argon2Threads   uint8
	NoncePrefix     []byte // 7 bytes
	ChunkSize       uint32 // plaintext bytes per chunk (final chunk may be shorter)
}

// Bytes serializes the header to its fixed-size on-disk representation.
func (h Header) Bytes() []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[0:12], FormatMagic)
	buf[12] = FormatVersion
	copy(buf[13:29], h.Salt)
	binary.BigEndian.PutUint32(buf[29:33], h.Argon2Time)
	binary.BigEndian.PutUint32(buf[33:37], h.Argon2MemoryKiB)
	buf[37] = h.Argon2Threads
	copy(buf[38:45], h.NoncePrefix)
	binary.BigEndian.PutUint32(buf[45:49], h.ChunkSize)
	return buf
}

// Sane upper bounds for header-supplied parameters. These are enforced in ParseHeader so that a corrupted or
// maliciously crafted file is rejected before its values are used to drive an Argon2id key derivation or a chunk
// buffer allocation. Both of which happen before any GCM authentication check, so unbounded header fields would
// otherwise be a resource-exhaustion vector on nothing more than opening the file.
const (
	maxArgon2Time      uint32 = 20
	maxArgon2MemoryKiB uint32 = 4 << 20 // 4 GiB
	maxArgon2Threads   uint8  = 64
	maxChunkSize       uint32 = 1 << 30 // 1 GiB; well under AES-GCM's 2^31-1 byte per-call limit
)

// ParseHeader decodes a header produced by Header.Bytes. buf must be exactly HeaderSize bytes.
func ParseHeader(buf []byte) (Header, error) {
	if len(buf) != HeaderSize {
		return Header{}, fmt.Errorf("invalid header size: got %d, want %d", len(buf), HeaderSize)
	}
	if string(buf[0:12]) != FormatMagic {
		return Header{}, fmt.Errorf("not a recognized encrypted file (bad magic)")
	}
	if buf[12] != FormatVersion {
		return Header{}, fmt.Errorf("unsupported encryption format version: %d", buf[12])
	}
	h := Header{
		Salt:            append([]byte{}, buf[13:29]...),
		Argon2Time:      binary.BigEndian.Uint32(buf[29:33]),
		Argon2MemoryKiB: binary.BigEndian.Uint32(buf[33:37]),
		Argon2Threads:   buf[37],
		NoncePrefix:     append([]byte{}, buf[38:45]...),
		ChunkSize:       binary.BigEndian.Uint32(buf[45:49]),
	}

	if h.Argon2Time == 0 || h.Argon2Time > maxArgon2Time {
		return Header{}, fmt.Errorf("invalid Argon2 time cost: %d", h.Argon2Time)
	}
	if h.Argon2MemoryKiB == 0 || h.Argon2MemoryKiB > maxArgon2MemoryKiB {
		return Header{}, fmt.Errorf("invalid Argon2 memory cost: %d KiB", h.Argon2MemoryKiB)
	}
	if h.Argon2Threads == 0 || h.Argon2Threads > maxArgon2Threads {
		return Header{}, fmt.Errorf("invalid Argon2 parallelism: %d", h.Argon2Threads)
	}
	if h.ChunkSize == 0 || h.ChunkSize > maxChunkSize {
		return Header{}, fmt.Errorf("invalid chunk size: %d", h.ChunkSize)
	}

	return h, nil
}

// ChunkNonce builds the 12-byte AES-GCM nonce for a chunk: the file's 7-byte random prefix, a 4-byte big-endian chunk
// index, and a 1-byte flag marking the final chunk. Binding the index and last-chunk flag into the nonce makes chunk
// reordering, truncation, and splicing between two files' chunks detectable as decryption failures.
func ChunkNonce(prefix []byte, index uint32, isLast bool) []byte {
	nonce := make([]byte, nonceSize)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[7:11], index)
	if isLast {
		nonce[11] = 1
	}
	return nonce
}

// ZeroBytes overwrites b with zeros.
func ZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// GenerateRandomBytes generates n random bytes.
func GenerateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return b, nil
}

// GenerateRandomURLEncodedString generates a URL-safe base64 encoded string of n random bytes.
func GenerateRandomURLEncodedString(n int) (string, error) {
	b, err := GenerateRandomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// NewArgon2IDKey derives a 32-byte key using Argon2id with explicit, caller-supplied parameters.
func NewArgon2IDKey(secret string, salt []byte, time, memoryKiB uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(secret), salt, time, memoryKiB, threads, 32)
}

// LegacyArgon2IDKey reproduces the key derivation used by pre-header encrypted files, where parallelism was
// runtime.NumCPU() at encryption time and was never persisted anywhere in the file. Decrypting a legacy file therefore
// only works on a machine with the same NumCPU() as the one that encrypted it, a pre-existing limitation of the legacy
// format that can't be corrected retroactively.
func LegacyArgon2IDKey(secret string, salt []byte) []byte {
	return argon2.IDKey([]byte(secret), salt, 3, 64*1024, uint8(runtime.NumCPU()), 32)
}

// NewAESGCMCipher creates a new AES-GCM cipher with the given key.
func NewAESGCMCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	return aesgcm, nil
}
