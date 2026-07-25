package storedobject

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	localObjectKeyBytes  = 32
	objectCipherChunk    = 64 << 10
	objectCipherHeader   = 60
	objectCipherTagBytes = 16
)

var (
	objectCipherMagic = [8]byte{'A', 'C', 'T', 'W', 'O', 'B', 'J', '1'}
	ErrDecrypt        = errors.New("stored object ciphertext authentication failed")
)

type CipherBinding struct {
	WorkspaceID string
	ObjectID    string
	Kind        string
}

type EncryptedStream struct {
	Reader io.ReadCloser
	Size   int64
	KeyID  string
}

type StreamCipher interface {
	KeyID() string
	Encrypt(context.Context, CipherBinding, io.Reader, int64, string) (EncryptedStream, error)
	Decrypt(context.Context, CipherBinding, io.ReadCloser) (io.ReadCloser, error)
}

type LocalChunkCipher struct {
	keyID string
	key   [localObjectKeyBytes]byte
	rand  io.Reader
}

func NewLocalChunkCipher(keyID string, masterKey []byte) (*LocalChunkCipher, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" || len(masterKey) != localObjectKeyBytes {
		return nil, ErrInvalid
	}
	value := &LocalChunkCipher{keyID: keyID, rand: rand.Reader}
	copy(value.key[:], masterKey)
	return value, nil
}

func NewLocalChunkCipherFromBase64(keyID, encodedMasterKey string) (*LocalChunkCipher, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedMasterKey))
	if err != nil {
		return nil, ErrInvalid
	}
	defer wipeBytes(decoded)
	return NewLocalChunkCipher(keyID, decoded)
}

func (ciphertext *LocalChunkCipher) KeyID() string { return ciphertext.keyID }

func (ciphertext *LocalChunkCipher) Encrypt(
	ctx context.Context,
	binding CipherBinding,
	plaintext io.Reader,
	plaintextSize int64,
	plaintextSHA256 string,
) (EncryptedStream, error) {
	plaintextSHA256 = strings.ToLower(strings.TrimSpace(plaintextSHA256))
	if plaintext == nil || plaintextSize < 0 || !validCipherBinding(binding) ||
		!validHash(plaintextSHA256) {
		return EncryptedStream{}, ErrInvalid
	}
	aead, err := ciphertext.aead()
	if err != nil {
		return EncryptedStream{}, err
	}
	chunks := chunkCount(plaintextSize)
	if chunks > int64(^uint32(0)) {
		return EncryptedStream{}, ErrInvalid
	}
	ciphertextSize := int64(objectCipherHeader) + plaintextSize + chunks*objectCipherTagBytes
	noncePrefix := make([]byte, 8)
	if _, err := io.ReadFull(ciphertext.rand, noncePrefix); err != nil {
		return EncryptedStream{}, fmt.Errorf("generate stored object nonce: %w", err)
	}
	header := makeCipherHeader(plaintextSize, noncePrefix, plaintextSHA256)
	reader, writer := io.Pipe()
	go func() {
		err := encryptObjectStream(ctx, writer, plaintext, aead, binding, header,
			noncePrefix, plaintextSize, plaintextSHA256)
		_ = writer.CloseWithError(err)
	}()
	return EncryptedStream{Reader: reader, Size: ciphertextSize, KeyID: ciphertext.keyID}, nil
}

func (ciphertext *LocalChunkCipher) Decrypt(
	ctx context.Context,
	binding CipherBinding,
	source io.ReadCloser,
) (io.ReadCloser, error) {
	if source == nil || !validCipherBinding(binding) {
		return nil, ErrInvalid
	}
	header := make([]byte, objectCipherHeader)
	if _, err := io.ReadFull(source, header); err != nil {
		_ = source.Close()
		return nil, ErrDecrypt
	}
	plaintextSize, noncePrefix, plaintextSHA256, err := parseCipherHeader(header)
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	aead, err := ciphertext.aead()
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	decryptContext, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	go func() {
		defer source.Close()
		err := decryptObjectStream(decryptContext, writer, source, aead, binding,
			header, noncePrefix, plaintextSize, plaintextSHA256)
		_ = writer.CloseWithError(err)
	}()
	return &cancelReadCloser{ReadCloser: reader, cancel: cancel, source: source}, nil
}

func (ciphertext *LocalChunkCipher) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(ciphertext.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encryptObjectStream(
	ctx context.Context,
	destination *io.PipeWriter,
	source io.Reader,
	aead cipher.AEAD,
	binding CipherBinding,
	header, noncePrefix []byte,
	plaintextSize int64,
	expectedSHA256 string,
) error {
	if _, err := destination.Write(header); err != nil {
		return err
	}
	hasher := sha256.New()
	remaining := plaintextSize
	buffer := make([]byte, objectCipherChunk)
	for index := uint32(0); remaining > 0; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunkSize := min(int64(len(buffer)), remaining)
		chunk := buffer[:chunkSize]
		if _, err := io.ReadFull(source, chunk); err != nil {
			return ErrIntegrity
		}
		_, _ = hasher.Write(chunk)
		sealed := aead.Seal(nil, objectNonce(noncePrefix, index), chunk,
			objectAAD(binding, header, index))
		if _, err := destination.Write(sealed); err != nil {
			return err
		}
		remaining -= chunkSize
	}
	var extra [1]byte
	count, err := source.Read(extra[:])
	if count != 0 || !errors.Is(err, io.EOF) ||
		hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
		return ErrIntegrity
	}
	return nil
}

func decryptObjectStream(
	ctx context.Context,
	destination *io.PipeWriter,
	source io.Reader,
	aead cipher.AEAD,
	binding CipherBinding,
	header, noncePrefix []byte,
	plaintextSize int64,
	expectedSHA256 string,
) error {
	hasher := sha256.New()
	remaining := plaintextSize
	for index := uint32(0); remaining > 0; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		plainChunkSize := min(int64(objectCipherChunk), remaining)
		sealed := make([]byte, plainChunkSize+int64(aead.Overhead()))
		if _, err := io.ReadFull(source, sealed); err != nil {
			return ErrDecrypt
		}
		plaintext, err := aead.Open(nil, objectNonce(noncePrefix, index), sealed,
			objectAAD(binding, header, index))
		if err != nil {
			return ErrDecrypt
		}
		_, _ = hasher.Write(plaintext)
		if _, err := destination.Write(plaintext); err != nil {
			return err
		}
		wipeBytes(plaintext)
		remaining -= plainChunkSize
	}
	var extra [1]byte
	count, err := source.Read(extra[:])
	if count != 0 || !errors.Is(err, io.EOF) ||
		hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
		return ErrDecrypt
	}
	return nil
}

func makeCipherHeader(plaintextSize int64, noncePrefix []byte, plaintextSHA256 string) []byte {
	header := make([]byte, objectCipherHeader)
	copy(header[:8], objectCipherMagic[:])
	binary.BigEndian.PutUint32(header[8:12], objectCipherChunk)
	binary.BigEndian.PutUint64(header[12:20], uint64(plaintextSize))
	copy(header[20:28], noncePrefix)
	decoded, _ := hex.DecodeString(plaintextSHA256)
	copy(header[28:60], decoded)
	return header
}

func parseCipherHeader(header []byte) (int64, []byte, string, error) {
	if len(header) != objectCipherHeader || !equalBytes(header[:8], objectCipherMagic[:]) ||
		binary.BigEndian.Uint32(header[8:12]) != objectCipherChunk {
		return 0, nil, "", ErrDecrypt
	}
	size := binary.BigEndian.Uint64(header[12:20])
	if size > uint64(5<<40) {
		return 0, nil, "", ErrDecrypt
	}
	return int64(size), append([]byte(nil), header[20:28]...),
		hex.EncodeToString(header[28:60]), nil
}

func objectNonce(prefix []byte, index uint32) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[8:], index)
	return nonce
}

func objectAAD(binding CipherBinding, header []byte, index uint32) []byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("actweave-object-chunk.v1\x00"))
	_, _ = hasher.Write([]byte(binding.WorkspaceID))
	_, _ = hasher.Write([]byte{'\x00'})
	_, _ = hasher.Write([]byte(binding.ObjectID))
	_, _ = hasher.Write([]byte{'\x00'})
	_, _ = hasher.Write([]byte(binding.Kind))
	_, _ = hasher.Write(header)
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], index)
	_, _ = hasher.Write(encoded[:])
	return hasher.Sum(nil)
}

func validCipherBinding(binding CipherBinding) bool {
	return validUUID(strings.TrimSpace(binding.WorkspaceID)) &&
		validUUID(strings.TrimSpace(binding.ObjectID)) && validKind(strings.TrimSpace(binding.Kind))
}

func chunkCount(size int64) int64 {
	if size == 0 {
		return 0
	}
	return (size + objectCipherChunk - 1) / objectCipherChunk
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	source io.Closer
}

func (reader *cancelReadCloser) Close() error {
	reader.cancel()
	return errors.Join(reader.ReadCloser.Close(), reader.source.Close())
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
